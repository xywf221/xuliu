package service

import (
	"context"
	"slices"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/xywf221/xuliu/internal/dto"
	"github.com/xywf221/xuliu/internal/history"
	"github.com/xywf221/xuliu/internal/model"
	"github.com/xywf221/xuliu/internal/pkg/apperr"
	"github.com/xywf221/xuliu/internal/pkg/pagination"
	"github.com/xywf221/xuliu/internal/repository"
	"github.com/xywf221/xuliu/internal/workflow"
)

// TaskService 负责任务的全生命周期。
//
// 任务是本系统里唯一「一切围绕工时」的实体，三条规则决定了整个实现：
//
//  1. **进度不落库**。进度由「已消耗 / (已消耗 + 剩余)」算出来（见
//     hoursProgress）。存一份就多一个可能与工时不一致的字段。
//  2. **工时以记录为准**。每次报工写一条 pm_effort，任务上的
//     已消耗/剩余是这些记录的汇总；单人任务直接写任务，多人任务
//     写成员分工再汇总到任务。这样工时历史可以完整回放。
//  3. **「完成」与「还剩工时」不能并存**。完成任务要求剩余工时为 0，
//     否则进度会算出一个「已完成但只做了 60%」的矛盾结果。
type TaskService struct {
	db         *gorm.DB
	tasks      *repository.TaskRepo
	taskTeams  *repository.TaskTeamRepo
	efforts    *repository.EffortRepo
	stories    *repository.StoryRepo
	products   *repository.ProductRepo
	modules    *repository.ModuleRepo
	executions *repository.ExecutionRepo
	users      *repository.UserRepo
	files      *repository.FileRepo
	comments   *repository.CommentRepo
	actions    *repository.ActionRepo
	gate       *Gate
	projects   *ProjectService
	team       *teamOps
	notifier   notifier

	recorder *history.Recorder
}

// NewTaskService 构造任务服务。
//
// projects 是注入进来的项目服务而不是仓储：任务的工时变化必须触发
// 项目与迭代的工时重算，而那段聚合逻辑属于项目域。方向是单向的
// （任务 → 项目），项目侧不依赖任务服务，不会形成环。
func NewTaskService(
	db *gorm.DB,
	tasks *repository.TaskRepo,
	taskTeams *repository.TaskTeamRepo,
	efforts *repository.EffortRepo,
	stories *repository.StoryRepo,
	products *repository.ProductRepo,
	modules *repository.ModuleRepo,
	executions *repository.ExecutionRepo,
	users *repository.UserRepo,
	files *repository.FileRepo,
	comments *repository.CommentRepo,
	actions *repository.ActionRepo,
	teams *repository.TeamRepo,
	projects *ProjectService,
	gate *Gate,
	notify notifier,
) *TaskService {
	return &TaskService{
		db: db, tasks: tasks, taskTeams: taskTeams, efforts: efforts,
		stories: stories, products: products, modules: modules,
		executions: executions, users: users, files: files, comments: comments,
		actions: actions, gate: gate, projects: projects,
		team: &teamOps{
			teams: teams, users: users, projects: nil,
			executions: executions, tasks: tasks, gate: gate,
		},
		notifier: orNoop(notify),
		recorder: history.New(TaskLabeler{}),
	}
}

// ---------------------------------------------------------------------------
// 查询
// ---------------------------------------------------------------------------

// List 分页查询任务。
func (s *TaskService) List(ctx context.Context, req dto.TaskFilterReq) (*pagination.Result[dto.TaskResp], error) {
	visible, err := s.gate.VisibleProjects(ctx)
	if err != nil {
		return nil, err
	}
	visibleProducts, err := s.gate.VisibleProducts(ctx)
	if err != nil {
		return nil, err
	}
	filter, err := s.buildFilter(ctx, req, visible, visibleProducts)
	if err != nil {
		return nil, err
	}

	result, err := s.tasks.List(ctx, filter)
	if err != nil {
		return nil, err
	}
	return s.decorateList(ctx, result)
}

// buildFilter 把查询参数转成仓储过滤条件。
func (s *TaskService) buildFilter(ctx context.Context, req dto.TaskFilterReq, visible, visibleProducts []uint) (repository.TaskFilter, error) {
	filter := repository.TaskFilter{
		ProjectID:         req.ProjectID,
		ExecutionID:       req.ExecutionID,
		ProductID:         req.ProductID,
		StoryID:           req.StoryID,
		Status:            req.Status,
		Type:              req.Type,
		Pri:               req.Pri,
		AssignedTo:        req.AssignedTo,
		OpenedBy:          req.OpenedBy,
		FinishedBy:        req.FinishedBy,
		Unfinished:        req.Unfinished,
		Keywords:          req.Keywords,
		VisibleProjectIDs: visible,
		ProductIDs:        visibleProducts,
		Pagination:        pagination.Params{Page: req.Page, Size: req.Size, Order: req.Order},
	}
	if req.TopOnly {
		zero := uint(0)
		filter.ParentID = &zero
	} else if req.ModuleID > 0 {
		moduleID := req.ModuleID
		filter.ModuleID = &moduleID
	}
	if req.ProjectID > 0 {
		if err := s.gate.AssertProjectVisible(ctx, req.ProjectID); err != nil {
			return filter, err
		}
	}
	if req.ExecutionID > 0 {
		if err := s.gate.AssertExecutionVisible(ctx, req.ExecutionID); err != nil {
			return filter, err
		}
	}

	from, _, err := parseDateRange(req.DeadlineFrom, "")
	if err != nil {
		return filter, translateDateErr(err, apperr.InvalidParam)
	}
	_, to, err := parseDateRange("", req.DeadlineTo)
	if err != nil {
		return filter, translateDateErr(err, apperr.InvalidParam)
	}
	filter.DeadlineFrom, filter.DeadlineTo = from, to
	return filter, nil
}

// Get 取任务详情。
func (s *TaskService) Get(ctx context.Context, id uint) (*dto.TaskResp, error) {
	task, err := s.tasks.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := s.gate.AssertTaskVisible(ctx, task); err != nil {
		return nil, err
	}

	decorated, err := s.decorateList(ctx, &pagination.Result[model.Task]{
		List: []model.Task{*task}, Total: 1, Page: 1, Size: 1,
	})
	if err != nil {
		return nil, err
	}
	out := decorated.List[0]
	out.Actions = availableStateActions(ctx, workflow.TaskMachine, task.Status)

	team, err := s.taskTeamResps(ctx, id)
	if err != nil {
		return nil, err
	}
	out.Team = team

	efforts, err := s.efforts.ListByObject(ctx, model.ObjectTypeTask, id)
	if err != nil {
		return nil, err
	}
	if out.Efforts, err = s.effortResps(ctx, efforts); err != nil {
		return nil, err
	}
	return &out, nil
}

// Kanban 返回看板视图。
//
// 范围二选一：给了 executionID 就是迭代看板，否则是项目看板
// （包含项目下所有迭代、以及不属于任何迭代的任务）。
func (s *TaskService) Kanban(ctx context.Context, projectID, executionID uint) (*dto.KanbanResp, error) {
	visible, err := s.gate.VisibleProjects(ctx)
	if err != nil {
		return nil, err
	}
	visibleProducts, err := s.gate.VisibleProducts(ctx)
	if err != nil {
		return nil, err
	}
	if projectID == 0 && executionID == 0 {
		return nil, apperr.Newf(apperr.InvalidParam, "看板需要指定项目或迭代")
	}
	if executionID > 0 {
		if err := s.gate.AssertExecutionVisible(ctx, executionID); err != nil {
			return nil, err
		}
	} else if err := s.gate.AssertProjectVisible(ctx, projectID); err != nil {
		return nil, err
	}

	filter := repository.TaskFilter{
		ProjectID: projectID, ExecutionID: executionID,
		VisibleProjectIDs: visible,
		ProductIDs:        visibleProducts,
	}
	tasks, err := s.tasks.ListByBoard(ctx, filter, kanbanLimit)
	if err != nil {
		return nil, err
	}

	counts, err := s.tasks.StatusCounts(ctx, filter)
	if err != nil {
		return nil, err
	}
	decorated, err := s.decorateList(ctx, &pagination.Result[model.Task]{
		List: tasks, Total: int64(len(tasks)), Page: 1, Size: len(tasks),
	})
	if err != nil {
		return nil, err
	}

	byStatus := make(map[string][]dto.TaskResp, len(workflow.TaskMachine.Statuses))
	for _, item := range decorated.List {
		byStatus[item.Status] = append(byStatus[item.Status], item)
	}

	// 列的顺序与内容都来自状态机，前端不再自己维护一份状态字典。
	columns := make([]dto.KanbanColumn, 0, len(workflow.TaskMachine.Statuses))
	for _, status := range workflow.TaskMachine.Statuses {
		items := byStatus[status]
		hours := dto.HoursResp{}
		for _, item := range items {
			hours.Estimate += item.Estimate
			hours.Consumed += item.Consumed
			hours.Left += item.Left
		}
		columns = append(columns, dto.KanbanColumn{
			Status: status,
			Label:  workflow.TaskMachine.StatusLabel(status),
			Count:  counts[status],
			Hours:  hours,
			Tasks:  items,
		})
	}

	scope := "project"
	if executionID > 0 {
		scope = "execution"
	}
	return &dto.KanbanResp{
		Scope: scope, ProjectID: projectID, ExecutionID: executionID,
		Columns: columns,
	}, nil
}

// kanbanLimit 是看板一次最多取回的任务数。
//
// 看板不分页（分页会把同一列切开），但也不能无上限：真到了几千条时，
// 看板本身已经不可读了，此时该做的是筛选而不是硬撑。
const kanbanLimit = 300

// decorateList 批量补全任务列表的展示字段。
func (s *TaskService) decorateList(ctx context.Context, result *pagination.Result[model.Task]) (*pagination.Result[dto.TaskResp], error) {
	tasks := result.List
	if len(tasks) == 0 {
		return &pagination.Result[dto.TaskResp]{
			List: []dto.TaskResp{}, Total: result.Total, Page: result.Page, Size: result.Size,
		}, nil
	}

	projectIDs := make([]uint, 0, len(tasks))
	executionIDs := make([]uint, 0, len(tasks))
	moduleIDs := make([]uint, 0, len(tasks))
	storyIDs := make([]uint, 0, len(tasks))
	taskIDs := make([]uint, 0, len(tasks))
	userIDs := make([]uint, 0, len(tasks)*3)
	for i := range tasks {
		t := &tasks[i]
		projectIDs = append(projectIDs, t.ProjectID)
		executionIDs = append(executionIDs, t.ExecutionID)
		moduleIDs = append(moduleIDs, t.ModuleID)
		storyIDs = append(storyIDs, t.StoryID)
		taskIDs = append(taskIDs, t.ID)
		userIDs = append(userIDs, t.OpenedBy, t.AssignedTo, t.FinishedBy)
	}

	projectNames, err := s.projects.NameMap(ctx, projectIDs)
	if err != nil {
		return nil, err
	}
	executionNames, err := s.executions.NameMap(ctx, executionIDs)
	if err != nil {
		return nil, err
	}
	moduleNames, err := s.modules.NameMap(ctx, moduleIDs)
	if err != nil {
		return nil, err
	}
	userNames, err := s.users.NameMap(ctx, userIDs)
	if err != nil {
		return nil, err
	}
	storyTitles, err := s.stories.TitleMap(ctx, storyIDs)
	if err != nil {
		return nil, err
	}
	storyVersions, err := s.storyVersionMap(ctx, storyIDs)
	if err != nil {
		return nil, err
	}
	commentCounts, err := s.comments.CountByObjects(ctx, model.ObjectTypeTask, taskIDs)
	if err != nil {
		return nil, err
	}
	fileCounts, err := s.fileCounts(ctx, taskIDs)
	if err != nil {
		return nil, err
	}
	effortCounts, err := s.efforts.CountByObjects(ctx, model.ObjectTypeTask, taskIDs)
	if err != nil {
		return nil, err
	}

	out := make([]dto.TaskResp, 0, len(tasks))
	for i := range tasks {
		t := &tasks[i]
		out = append(out, dto.TaskResp{
			ID: t.ID, Number: "TASK-" + itoa(t.ID),
			ProjectID: t.ProjectID, ProjectName: projectNames[t.ProjectID],
			ExecutionID: t.ExecutionID, ExecutionName: executionNames[t.ExecutionID],
			ProductID: t.ProductID,
			ModuleID:  t.ModuleID, ModuleName: moduleNames[t.ModuleID],
			ParentID: t.ParentID, IsParent: t.IsParent,

			StoryID: t.StoryID, StoryTitle: storyTitles[t.StoryID],
			StoryVersion: t.StoryVersion,
			// 版本不一致说明需求在本任务开工之后变更过，前端要提示确认。
			StoryChanged: t.StoryID > 0 && t.StoryVersion > 0 &&
				storyVersions[t.StoryID] != t.StoryVersion,
			FromBugID: t.FromBugID,

			Name: t.Name, Keywords: t.Keywords, Desc: t.Desc,
			Type: t.Type, TypeLabel: taskTypeLabels[t.Type],
			Mode: t.Mode, ModeLabel: taskModeLabels[t.Mode],

			Pri: t.Pri, Estimate: t.Estimate, Consumed: t.Consumed, Left: t.Left,
			Progress: hoursProgress(t.Consumed, t.Left),
			Deadline: formatDate(t.Deadline),

			Status: t.Status, StatusLabel: workflow.TaskMachine.StatusLabel(t.Status),
			Version: t.Version,

			OpenedBy: t.OpenedBy, OpenedName: userNames[t.OpenedBy],
			OpenedDate: formatTimeValue(t.OpenedDate),
			AssignedTo: t.AssignedTo, AssignName: userNames[t.AssignedTo],
			AssignedDate: formatTimeValue(t.AssignedDate),
			EstStarted:   formatDate(t.EstStarted), RealStarted: formatTimeValue(t.RealStarted),
			FinishedBy: t.FinishedBy, FinishedName: userNames[t.FinishedBy],
			FinishedDate: formatTimeValue(t.FinishedDate),
			CanceledBy:   t.CanceledBy, CanceledDate: formatTimeValue(t.CanceledDate),
			ClosedBy: t.ClosedBy, ClosedDate: formatTimeValue(t.ClosedDate),
			ClosedReason:  t.ClosedReason,
			ActivatedDate: formatTimeValue(t.ActivatedDate),

			Order:        t.Order,
			CommentCount: commentCounts[t.ID],
			FileCount:    fileCounts[t.ID],
			EffortCount:  effortCounts[t.ID],
		})
	}
	return &pagination.Result[dto.TaskResp]{
		List: out, Total: result.Total, Page: result.Page, Size: result.Size,
	}, nil
}

// storyVersionMap 批量取需求的当前版本号，用于判断任务关联的需求是否已变更。
func (s *TaskService) storyVersionMap(ctx context.Context, storyIDs []uint) (map[uint]int, error) {
	stories, err := s.stories.ListByIDs(ctx, storyIDs)
	if err != nil {
		return nil, err
	}
	out := make(map[uint]int, len(stories))
	for _, st := range stories {
		out[st.ID] = st.Version
	}
	return out, nil
}

// fileCounts 批量统计任务下的附件数。
func (s *TaskService) fileCounts(ctx context.Context, taskIDs []uint) (map[uint]int64, error) {
	var rows []struct {
		ObjectID uint
		Total    int64
	}
	err := s.db.WithContext(ctx).Model(&model.File{}).
		Select("object_id, COUNT(*) AS total").
		Where("object_type = ? AND object_id IN ?", model.ObjectTypeTask, taskIDs).
		Group("object_id").Scan(&rows).Error
	if err != nil {
		return nil, apperr.Wrap(apperr.Internal, err)
	}
	out := make(map[uint]int64, len(rows))
	for _, row := range rows {
		out[row.ObjectID] = row.Total
	}
	return out, nil
}

// taskTeamResps 取任务的成员分工出参。
func (s *TaskService) taskTeamResps(ctx context.Context, taskID uint) ([]dto.TaskTeamResp, error) {
	members, err := s.taskTeams.ListByTask(ctx, taskID)
	if err != nil {
		return nil, err
	}
	if len(members) == 0 {
		return nil, nil
	}
	ids := make([]uint, 0, len(members))
	for _, m := range members {
		ids = append(ids, m.UserID)
	}
	names, err := s.users.NameMap(ctx, ids)
	if err != nil {
		return nil, err
	}

	out := make([]dto.TaskTeamResp, 0, len(members))
	for _, m := range members {
		out = append(out, dto.TaskTeamResp{
			UserID: m.UserID, Realname: names[m.UserID],
			Estimate: m.Estimate, Consumed: m.Consumed, Left: m.Left,
			Progress:    hoursProgress(m.Consumed, m.Left),
			Status:      m.Status,
			StatusLabel: workflow.TaskMachine.StatusLabel(m.Status),
			Transfer:    m.Transfer,
			Order:       m.Order,
		})
	}
	return out, nil
}

// effortResps 把工时记录转为出参。
func (s *TaskService) effortResps(ctx context.Context, efforts []model.Effort) ([]dto.EffortResp, error) {
	if len(efforts) == 0 {
		return nil, nil
	}
	ids := make([]uint, 0, len(efforts))
	for i := range efforts {
		ids = append(ids, efforts[i].UserID)
	}
	names, err := s.users.NameMap(ctx, ids)
	if err != nil {
		return nil, err
	}
	return toEffortResps(efforts, names), nil
}

// toEffortResps 把工时模型转成出参（纯函数，姓名映射由调用方批量备好）。
func toEffortResps(efforts []model.Effort, names map[uint]string) []dto.EffortResp {
	out := make([]dto.EffortResp, 0, len(efforts))
	for i := range efforts {
		e := &efforts[i]
		out = append(out, dto.EffortResp{
			ID: e.ID, ObjectType: e.ObjectType, ObjectID: e.ObjectID,
			ProjectID: e.ProjectID, ExecutionID: e.ExecutionID,
			UserID: e.UserID, Realname: names[e.UserID],
			Work: e.Work, Date: formatDate(&e.Date),
			Consumed: e.Consumed, Left: e.Left,
			Begin: e.Begin, End: e.End,
			CreatedAt: e.CreatedAt.UTC().Format(time.RFC3339),
		})
	}
	return out
}

// ListEfforts 分页查询工时记录（用于「我的工时」「项目工时」页）。
func (s *TaskService) ListEfforts(ctx context.Context, req dto.EffortFilterReq) (*pagination.Result[dto.EffortResp], error) {
	visible, err := s.gate.VisibleProjects(ctx)
	if err != nil {
		return nil, err
	}
	// 按对象查时先验可见性：工时里带着「干了什么」，属于业务内容。
	if req.ObjectType != "" && req.ObjectID > 0 {
		if err := s.gate.AssertVisible(ctx, req.ObjectType, req.ObjectID); err != nil {
			return nil, err
		}
	}
	if req.ProjectID > 0 {
		if err := s.gate.AssertProjectVisible(ctx, req.ProjectID); err != nil {
			return nil, err
		}
	}

	from, _, err := parseDateRange(req.DateFrom, "")
	if err != nil {
		return nil, translateDateErr(err, apperr.InvalidParam)
	}
	_, to, err := parseDateRange("", req.DateTo)
	if err != nil {
		return nil, translateDateErr(err, apperr.InvalidParam)
	}

	result, err := s.efforts.List(ctx, repository.EffortFilter{
		ObjectType: req.ObjectType, ObjectID: req.ObjectID,
		ProjectID: req.ProjectID, ExecutionID: req.ExecutionID,
		UserID: req.UserID, DateFrom: from, DateTo: to,
		VisibleProjectIDs: visible,
		Pagination:        pagination.Params{Page: req.Page, Size: req.Size, Order: req.Order},
	})
	if err != nil {
		return nil, err
	}

	ids := make([]uint, 0, len(result.List))
	for i := range result.List {
		ids = append(ids, result.List[i].UserID)
	}
	names, err := s.users.NameMap(ctx, ids)
	if err != nil {
		return nil, err
	}
	return &pagination.Result[dto.EffortResp]{
		List:  toEffortResps(result.List, names),
		Total: result.Total, Page: result.Page, Size: result.Size,
	}, nil
}

// ---------------------------------------------------------------------------
// 创建与编辑
// ---------------------------------------------------------------------------

// Create 新建任务。
func (s *TaskService) Create(ctx context.Context, req dto.TaskReq) (*dto.TaskResp, error) {
	project, err := s.projects.Get(ctx, req.ProjectID)
	if err != nil {
		return nil, err
	}
	if project.Status == model.ProjectStatusClosed {
		return nil, apperr.Newf(apperr.TaskStatusInvalid, "项目已关闭，不能新建任务")
	}

	execution, err := s.assertExecution(ctx, req.ExecutionID, req.ProjectID)
	if err != nil {
		return nil, err
	}
	if execution != nil && execution.Status == model.ExecutionStatusClosed {
		return nil, apperr.Newf(apperr.TaskStatusInvalid, "迭代已关闭，不能新建任务")
	}

	if err := s.assertProductInProject(ctx, req.ProductID, req.ProjectID); err != nil {
		return nil, err
	}
	if err := s.assertModule(ctx, req.ModuleID, req.ProductID); err != nil {
		return nil, err
	}
	if err := s.assertTaskHours(req); err != nil {
		return nil, err
	}

	name := strings.TrimSpace(req.Name)
	if name == "" {
		return nil, apperr.Newf(apperr.InvalidParam, "任务名称不能为空")
	}

	deadline, err := parseOptionalDate(req.Deadline)
	if err != nil {
		return nil, err
	}
	estStarted, err := parseOptionalDate(req.EstStarted)
	if err != nil {
		return nil, err
	}

	// 关联需求：记下当时的版本号，日后需求变更就能看出来。
	storyID, storyVersion, err := s.resolveStory(ctx, req.StoryID)
	if err != nil {
		return nil, err
	}
	if err := s.assertStoryVisible(ctx, storyID); err != nil {
		return nil, err
	}

	mode := normalTaskMode(req.Mode, len(req.Team))
	members, err := s.buildMembers(ctx, req.ProjectID, req.ExecutionID, req.Team, mode)
	if err != nil {
		return nil, err
	}
	if err := s.assertAssignable(ctx, req.ProjectID, req.ExecutionID, req.AssignedTo); err != nil {
		return nil, err
	}

	actor := currentUserID(ctx)
	now := time.Now().UTC()

	task := &model.Task{
		ProjectID: req.ProjectID, ExecutionID: req.ExecutionID,
		ProductID: req.ProductID, ModuleID: req.ModuleID,
		StoryID: storyID, StoryVersion: storyVersion,

		Name: name, Keywords: req.Keywords, Desc: req.Desc,
		Type: defaultString(req.Type, model.TaskTypeDevel),
		Mode: mode,
		Pri:  defaultPriInt(req.Pri),

		Deadline: deadline, EstStarted: estStarted,

		Status:  model.TaskStatusWait,
		Version: 1,

		OpenedBy:   actor,
		OpenedDate: &now,
		Order:      req.Order,
	}
	task.Estimate = taskEstimate(req, members)
	task.Left = taskLeft(req, members, task.Estimate)
	task.AssignedTo = req.AssignedTo
	if task.AssignedTo == 0 && len(members) > 0 {
		task.AssignedTo = members[0].UserID
	}
	if task.AssignedTo > 0 {
		task.AssignedDate = &now
	}

	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := s.tasks.Create(ctx, tx, task); err != nil {
			return err
		}
		// 任务的物化路径：目前不支持子任务树之外的层级，
		// 但 Path 保持「含自身 id」的统一口径。
		if err := s.tasks.UpdateFields(ctx, tx, task.ID, map[string]any{
			"path": itoa(task.ID), "grade": 1,
		}); err != nil {
			return err
		}
		if len(members) > 0 {
			if err := s.taskTeams.Replace(ctx, tx, task.ID, members); err != nil {
				return err
			}
		}
		if err := s.projects.RecomputeHours(ctx, tx, task.ProjectID, task.ExecutionID); err != nil {
			return err
		}
		if err := s.record(ctx, tx, task.ProjectID, task.ExecutionID, history.Entry{
			ObjectType: model.ObjectTypeTask,
			ObjectID:   task.ID,
			ActorID:    actor,
			Action:     model.ActionOpened,
			Comment:    req.Desc,
			Changes:    []history.Change{{Field: "name", Old: "", New: name}},
		}); err != nil {
			return err
		}
		// 建任务时就指了人，等于「下一步在他手上」，直接通知。
		title, content := noticeTaskAssigned(name)
		return s.notifier.Send(ctx, tx, task.AssignedTo, actor,
			model.ObjectTypeTask, task.ID, title, content)
	})
	if err != nil {
		return nil, err
	}
	return s.Get(ctx, task.ID)
}

// Update 编辑任务。
//
// 乐观锁口径与需求不同：任务**每次编辑都会把 version 加一**。
// 任务没有「需求变更」那种重量级的独立动作，每次保存都是对同一条
// 工作项的实质修改，所以每一步都该被「有人在我前面改过」挡住。
func (s *TaskService) Update(ctx context.Context, id uint, req dto.TaskReq) (*dto.TaskResp, error) {
	task, err := s.tasks.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := s.gate.AssertTaskVisible(ctx, task); err != nil {
		return nil, err
	}
	if _, statusErr := workflow.TaskMachine.Resolve("edit", task.Status); statusErr != nil {
		return nil, workflow.TaskMachine.DescribeError(statusErr)
	}
	// 编辑不允许把一个任务搬到别的项目：项目决定了权限、团队与工时归属，
	// 「搬项目」是另一件事（相当于删掉重建），不该藏在编辑里。
	if req.ProjectID != 0 && req.ProjectID != task.ProjectID {
		return nil, apperr.Newf(apperr.InvalidParam, "不能把任务改到别的项目下")
	}
	if _, err := s.assertExecution(ctx, req.ExecutionID, task.ProjectID); err != nil {
		return nil, err
	}
	if err := s.assertProductInProject(ctx, req.ProductID, task.ProjectID); err != nil {
		return nil, err
	}
	if err := s.assertModule(ctx, req.ModuleID, req.ProductID); err != nil {
		return nil, err
	}
	if err := s.assertTaskHours(req); err != nil {
		return nil, err
	}

	name := strings.TrimSpace(req.Name)
	if name == "" {
		return nil, apperr.Newf(apperr.InvalidParam, "任务名称不能为空")
	}
	deadline, err := parseOptionalDate(req.Deadline)
	if err != nil {
		return nil, err
	}
	estStarted, err := parseOptionalDate(req.EstStarted)
	if err != nil {
		return nil, err
	}

	storyID, storyVersion, err := s.resolveStory(ctx, req.StoryID)
	if err != nil {
		return nil, err
	}
	if err := s.assertStoryVisible(ctx, storyID); err != nil {
		return nil, err
	}
	if err := s.assertAssignable(ctx, task.ProjectID, req.ExecutionID, req.AssignedTo); err != nil {
		return nil, err
	}

	mode := normalTaskMode(req.Mode, len(req.Team))
	// 改成多人任务时必须给出名单（否则任务会进入「多人」却没有任何成员）；
	// 反过来改回单人不需要名单。
	if mode != model.TaskModeSingle && task.Mode != mode && len(req.Team) == 0 {
		return nil, apperr.Newf(apperr.InvalidParam, "改为多人任务时必须给出成员名单")
	}
	members, err := s.buildMembers(ctx, task.ProjectID, task.ExecutionID, req.Team, mode)
	if err != nil {
		return nil, err
	}

	actor := currentUserID(ctx)
	now := time.Now().UTC()

	fields := map[string]any{
		"execution_id":  req.ExecutionID,
		"product_id":    req.ProductID,
		"module_id":     req.ModuleID,
		"story_id":      storyID,
		"story_version": storyVersion,
		"name":          name,
		"keywords":      req.Keywords,
		"desc":          req.Desc,
		"type":          defaultString(req.Type, task.Type),
		"mode":          mode,
		"pri":           defaultPriInt(req.Pri),
		"deadline":      deadline,
		"est_started":   estStarted,

		"version":          task.Version + 1,
		"last_edited_by":   actor,
		"last_edited_date": now,
	}
	// 剩余工时只在显式给出时改写：编辑任务不该顺手把「还剩多少」
	// 也算一遍，那个数字要么由报工推进，要么由用户明确填写。
	if req.Left != nil {
		fields["left"] = *req.Left
	}
	if len(members) > 0 {
		// 多人任务：工时是成员分工之和。
		fields["estimate"] = taskEstimate(req, members)
	} else if mode == model.TaskModeSingle {
		fields["estimate"] = req.Estimate
	} else {
		// 多人任务但本次没给名单：保留原有分工与工时，
		// 不能按「空名单」处理 —— 那会把成员连同已发生的工时一起抹掉。
	}
	if req.AssignedTo != task.AssignedTo {
		fields["assigned_to"] = req.AssignedTo
		fields["assigned_date"] = now
	}

	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := s.tasks.UpdateVersioned(ctx, tx, id, task.Version, fields); err != nil {
			return err
		}
		switch {
		case mode == model.TaskModeSingle:
			// 改回单人任务：成员分工不再有意义，清掉。
			// 工时留在任务上（它是已经发生的历史），不再按成员重算。
			if task.Mode != model.TaskModeSingle {
				if err := s.taskTeams.DeleteByTask(ctx, tx, id); err != nil {
					return err
				}
			}
		case len(members) > 0:
			if err := s.taskTeams.Replace(ctx, tx, id, members); err != nil {
				return err
			}
			h, err := s.taskTeams.SumByTask(ctx, tx, id)
			if err != nil {
				return err
			}
			if err := s.tasks.RecomputeHours(ctx, tx, id, h); err != nil {
				return err
			}
		}
		if err := s.projects.RecomputeHours(ctx, tx, task.ProjectID, task.ExecutionID); err != nil {
			return err
		}
		// 编辑时顺手换了负责人，与「指派」是同一件事，通知口径保持一致。
		// 放在变更比对之前：通知该发不该发，取决于人有没有变，
		// 而不是取决于这次编辑有没有产生字段差异。
		if req.AssignedTo != task.AssignedTo && req.AssignedTo > 0 {
			title, content := noticeTaskAssigned(name)
			if err := s.notifier.Send(ctx, tx, req.AssignedTo, actor,
				model.ObjectTypeTask, task.ID, title, content); err != nil {
				return err
			}
		}
		changes := taskDiff(task, fields)
		if len(changes) == 0 {
			return nil
		}
		return s.record(ctx, tx, task.ProjectID, task.ExecutionID, history.Entry{
			ObjectType: model.ObjectTypeTask,
			ObjectID:   id,
			ActorID:    actor,
			Action:     model.ActionEdited,
			Changes:    changes,
		})
	})
	if err != nil {
		return nil, err
	}
	return s.Get(ctx, id)
}

// Delete 删除任务。
//
// 有工时记录的任务不能删：工时是审计数据（谁在什么时候做了多久），
// 删掉任务就把它一起抹掉了。这种情况应该改为「取消」——
// 取消保留记录，只是不再占用剩余工时。
func (s *TaskService) Delete(ctx context.Context, id uint) error {
	task, err := s.tasks.GetByID(ctx, id)
	if err != nil {
		return err
	}
	if err := s.gate.AssertTaskVisible(ctx, task); err != nil {
		return err
	}

	children, err := s.tasks.CountChildren(ctx, id)
	if err != nil {
		return err
	}
	if children > 0 {
		return apperr.New(apperr.TaskHasChildren)
	}
	effortCount, err := s.efforts.Count(ctx, "object_type = ? AND object_id = ?", model.ObjectTypeTask, id)
	if err != nil {
		return err
	}
	if effortCount > 0 {
		return apperr.Newf(apperr.Conflict,
			"该任务下还有 %d 条工时记录，无法删除；如不再需要请改为取消", effortCount)
	}

	actor := currentUserID(ctx)
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := s.taskTeams.DeleteByTask(ctx, tx, id); err != nil {
			return err
		}
		if _, err := s.comments.DeleteByObject(ctx, tx, model.ObjectTypeTask, id); err != nil {
			return err
		}
		if res := tx.WithContext(ctx).Model(&model.File{}).
			Where("object_type = ? AND object_id = ?", model.ObjectTypeTask, id).
			Updates(map[string]any{"object_type": "", "object_id": 0}); res.Error != nil {
			return apperr.Wrap(apperr.Internal, res.Error)
		}
		if err := s.record(ctx, tx, task.ProjectID, task.ExecutionID, history.Entry{
			ObjectType: model.ObjectTypeTask,
			ObjectID:   id,
			ActorID:    actor,
			Action:     model.ActionDeleted,
		}); err != nil {
			return err
		}
		if err := s.tasks.Delete(ctx, tx, id); err != nil {
			return err
		}
		return s.projects.RecomputeHours(ctx, tx, task.ProjectID, task.ExecutionID)
	})
	if err != nil {
		return err
	}
	return nil
}

// ---------------------------------------------------------------------------
// 状态流转
// ---------------------------------------------------------------------------

// TaskActionInput 是任务动作的公共入参。
type TaskActionInput struct {
	Action  string
	Comment string
	// AssignedTo 用于 assign。
	AssignedTo uint
	// Consumed 用于 finish：顺手把这次干完的工时一起报掉。
	Consumed float64
}

// Apply 执行一个任务状态动作。
func (s *TaskService) Apply(ctx context.Context, id uint, in TaskActionInput) (*dto.TaskResp, error) {
	task, err := s.tasks.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := s.gate.AssertTaskVisible(ctx, task); err != nil {
		return nil, err
	}

	trans, statusErr := workflow.TaskMachine.Resolve(in.Action, task.Status)
	if statusErr != nil {
		return nil, workflow.TaskMachine.DescribeError(statusErr)
	}

	actor := currentUserID(ctx)
	now := time.Now().UTC()

	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		fields, err := s.actionFields(ctx, tx, task, in, trans.To, actor, now)
		if err != nil {
			return err
		}
		if err := s.tasks.UpdateVersioned(ctx, tx, id, task.Version,
			withVersion(fields, task.Version+1, actor, now)); err != nil {
			return err
		}
		if err := s.projects.RecomputeHours(ctx, tx, task.ProjectID, task.ExecutionID); err != nil {
			return err
		}

		entry := history.Entry{
			ObjectType:  model.ObjectTypeTask,
			ObjectID:    id,
			ProjectID:   task.ProjectID,
			ExecutionID: task.ExecutionID,
			ActorID:     actor,
			Action:      workflow.TaskMachine.LogActionOf(in.Action),
			Comment:     in.Comment,
			Extra:       workflow.StatusChangeExtra(task.Status, trans.To),
			Changes:     []history.Change{{Field: "status", Old: task.Status, New: trans.To}},
		}
		if in.Action == "assign" {
			// 指派动作的 Extra 存目标用户 id（该形态与禅道一致，便于导入老数据）。
			entry.Extra = itoa(in.AssignedTo)
			entry.Changes = []history.Change{
				{Field: "assigned_to", Old: itoa(task.AssignedTo), New: itoa(in.AssignedTo)},
			}
		}
		if err := s.record(ctx, tx, task.ProjectID, task.ExecutionID, entry); err != nil {
			return err
		}

		// 每一步只通知「接下来该动手的那个人」：
		//   指派 → 新的负责人；完成 → 提任务的人（验收/关闭归他）；
		//   重新激活 → 负责人。暂停、关闭这类动作不改变「该谁动」，不发。
		to := uint(0)
		var title, content string
		switch in.Action {
		case "assign":
			to = in.AssignedTo
			title, content = noticeTaskAssigned(task.Name)
		case "finish":
			to = task.OpenedBy
			title, content = noticeTaskFinished(task.Name)
		case "activate":
			to = task.AssignedTo
			title, content = noticeTaskActivated(task.Name)
		default:
			return nil
		}
		return s.notifier.Send(ctx, tx, to, actor, model.ObjectTypeTask, task.ID, title, content)
	})
	if err != nil {
		return nil, err
	}
	return s.Get(ctx, id)
}

// actionFields 按动作算出要写入的字段。
func (s *TaskService) actionFields(
	ctx context.Context, tx *gorm.DB, task *model.Task,
	in TaskActionInput, to string, actor uint, now time.Time,
) (map[string]any, error) {
	fields := map[string]any{"status": to}

	switch in.Action {
	case "assign":
		if in.AssignedTo == 0 {
			return nil, apperr.Newf(apperr.InvalidParam, "请选择指派人")
		}
		if in.AssignedTo == task.AssignedTo {
			return nil, apperr.Newf(apperr.InvalidParam, "任务已经指派给该成员")
		}
		if err := s.assertAssignable(ctx, task.ProjectID, task.ExecutionID, in.AssignedTo); err != nil {
			return nil, err
		}
		fields["assigned_to"] = in.AssignedTo
		fields["assigned_date"] = now
		fields["status"] = task.Status

	case "start":
		// 没人认领的任务，谁点「开始」就算谁的 —— 否则任务进入
		// 进行中却没人负责，工时也没处记。
		if task.AssignedTo == 0 {
			if err := s.assertAssignable(ctx, task.ProjectID, task.ExecutionID, actor); err != nil {
				return nil, err
			}
			fields["assigned_to"] = actor
			fields["assigned_date"] = now
		}
		fields["real_started"] = now
		if err := s.setTeamStatus(ctx, tx, task, actor, model.TaskStatusDoing); err != nil {
			return nil, err
		}

	case "pause", "restart", "activate":
		if in.Action == "activate" {
			fields["activated_date"] = now
		}
		if err := s.setTeamStatus(ctx, tx, task, actor, to); err != nil {
			return nil, err
		}

	case "finish":
		unfinished, err := s.tasks.CountUnfinishedChildren(ctx, task.ID)
		if err != nil {
			return nil, err
		}
		if unfinished > 0 {
			return nil, apperr.Newf(apperr.TaskHasChildren,
				"还有 %d 个子任务未完成，请先完成或取消它们", unfinished)
		}
		if in.Consumed > 0 {
			// 「干完了顺手报工时」是最常见的用法，一次请求办完两件事。
			// 必须先报工时再校验剩余 —— 否则「把剩下的工时一次报完并完成」
			// 这个最常见的用法会被自己的校验挡住。
			if err := s.recordEffortTx(ctx, tx, task, dto.EffortReq{
				Consumed: in.Consumed,
			}, actor, now); err != nil {
				return nil, err
			}
		}
		// 校验用的是刚写进去的值，所以从事务里重新读一次。
		hours, err := s.tasks.HoursInTx(ctx, tx, task.ID)
		if err != nil {
			return nil, err
		}
		// 「已完成」与「还剩 6 小时」互相矛盾：进度会算出 60%。
		// 与其让这种数据进库，不如在提交时挡住并告诉用户怎么处理。
		if hours.Left > 0 {
			return nil, apperr.Newf(apperr.TaskLeftNotZero,
				"任务还剩 %s 小时，请先登记工时或把剩余工时改为 0 再完成", trimZero(hours.Left))
		}
		if task.RealStarted == nil {
			fields["real_started"] = now
		}
		fields["finished_by"] = actor
		fields["finished_date"] = now
		if err := s.setTeamStatus(ctx, tx, task, actor, to); err != nil {
			return nil, err
		}

	case "cancel":
		fields["canceled_by"] = actor
		fields["canceled_date"] = now
		fields["closed_reason"] = model.TaskCloseCancel
		// 取消即不再投入：剩余工时清零，否则它会一直挂在项目的
		// 「剩余工作量」里，等于给一个不会做的活留了预算。
		fields["left"] = 0.0
		if err := s.zeroTeamLeft(ctx, tx, task); err != nil {
			return nil, err
		}
		if err := s.setTeamStatus(ctx, tx, task, actor, to); err != nil {
			return nil, err
		}

	case "close":
		fields["closed_by"] = actor
		fields["closed_date"] = now
		if task.Status == model.TaskStatusCancel {
			fields["closed_reason"] = model.TaskCloseCancel
		} else {
			fields["closed_reason"] = model.TaskCloseDone
		}
		fields["left"] = 0.0
		if err := s.zeroTeamLeft(ctx, tx, task); err != nil {
			return nil, err
		}
		if err := s.setTeamStatus(ctx, tx, task, actor, to); err != nil {
			return nil, err
		}

	default:
		return nil, apperr.Newf(apperr.Internal, "动作 %s 尚未实现", in.Action)
	}
	return fields, nil
}

// setTeamStatus 同步多人任务成员的推进状态。
//
// 单人任务没有成员分工行，这里什么都不做。多人任务里，整个任务的
// 状态变化要落到成员身上：比如「开始」意味着当前负责的那个成员开始做，
// 否则看板上会出现「任务进行中、成员却都还没开始」的矛盾。
func (s *TaskService) setTeamStatus(ctx context.Context, tx *gorm.DB, task *model.Task, actor uint, status string) error {
	if task.Mode == model.TaskModeSingle {
		return nil
	}
	members, err := s.taskTeams.ListByTask(ctx, task.ID)
	if err != nil {
		return err
	}
	if len(members) == 0 {
		return nil
	}

	// 只动「该动的那一个」：串行任务动当前负责的成员，
	// 并行任务动全部未完成的成员。已完成/已取消的成员不再被改写。
	for _, m := range members {
		if m.Status == model.TaskStatusDone || m.Status == model.TaskStatusCancel {
			continue
		}
		if task.Mode == model.TaskModeLinear && m.UserID != task.AssignedTo {
			continue
		}
		if err := s.taskTeams.UpdateMember(ctx, tx, task.ID, m.UserID,
			map[string]any{"status": status}); err != nil {
			return err
		}
	}
	return nil
}

// zeroTeamLeft 把全部成员的剩余工时清零。
//
// 取消与关闭意味着这活不做了（或已经做完），成员分工上的剩余工时要跟着
// 归零：否则下一次无论哪位成员报工时，汇总都会把那份「剩余」重新算回
// 任务上 —— 一个已关闭的任务又冒出了剩余工作量。
func (s *TaskService) zeroTeamLeft(ctx context.Context, tx *gorm.DB, task *model.Task) error {
	if task.Mode == model.TaskModeSingle {
		return nil
	}
	_, err := s.taskTeams.UpdateWhere(ctx, tx, "task_id = ?", []any{task.ID},
		map[string]any{"left": 0.0})
	return err
}

// ---------------------------------------------------------------------------
// 工时
// ---------------------------------------------------------------------------

// RecordEffort 登记工时。
func (s *TaskService) RecordEffort(ctx context.Context, id uint, req dto.EffortReq) (*dto.TaskResp, error) {
	task, err := s.tasks.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := s.gate.AssertTaskVisible(ctx, task); err != nil {
		return nil, err
	}
	// 只有进行中的任务能记工时 —— 未开始就报工时说明流程走错了。
	// 这条规则与状态机里 recordeffort 的来源状态一致。
	if _, statusErr := workflow.TaskMachine.Resolve("recordeffort", task.Status); statusErr != nil {
		return nil, workflow.TaskMachine.DescribeError(statusErr)
	}

	actor := currentUserID(ctx)
	now := time.Now().UTC()

	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := s.recordEffortTx(ctx, tx, task, req, actor, now); err != nil {
			return err
		}
		if err := s.projects.RecomputeHours(ctx, tx, task.ProjectID, task.ExecutionID); err != nil {
			return err
		}
		entry := history.Entry{
			ObjectType:  model.ObjectTypeTask,
			ObjectID:    id,
			ProjectID:   task.ProjectID,
			ExecutionID: task.ExecutionID,
			ActorID:     actor,
			Action:      model.ActionRecordWork,
			Comment:     req.Comment,
		}
		if req.Work != "" {
			entry.Comment = strings.TrimSpace(req.Comment + " " + req.Work)
		}
		_, err := s.recorder.Record(ctx, tx, entry)
		return err
	})
	if err != nil {
		return nil, err
	}
	return s.Get(ctx, id)
}

// recordEffortTx 在事务内写一条工时记录并同步工时字段。
//
// 归属分两种：单人任务直接记在任务上；多人任务记在成员分工上，
// 再看情况把成员工时汇总回任务。
func (s *TaskService) recordEffortTx(
	ctx context.Context, tx *gorm.DB, task *model.Task,
	req dto.EffortReq, actor uint, now time.Time,
) error {
	if req.Consumed < 0 {
		return apperr.New(apperr.TaskHoursInvalid)
	}
	if req.Consumed == 0 {
		return apperr.Newf(apperr.InvalidParam, "本次消耗工时必须大于 0")
	}

	// Date 是「业务日期」（模型注释就这么写的），不是记录创建时间，
	// 所以两条写入路径必须给出同一种东西：一个 UTC 零点。
	//
	// 之前默认走 now（带时分秒）、显式传 date 走 UTC 零点，同一列混了两种精度，
	// 后果有两个：按天聚合的燃尽图与工时报表会把同一天的工时切成两桶，
	// 以及「按 date 倒序」的列表会因为精度不同而排不出稳定的先后 ——
	// 客户端在东八区凌晨报的工，与服务端 UTC 时间差一天。
	date := startOfUTCDay(now)
	if req.Date != "" {
		parsed, err := parseOptionalDate(req.Date)
		if err != nil {
			return err
		}
		if parsed != nil {
			// 走 parseDateRange 出来本就是 UTC 零点，这里再压一次是让
			// 「这列只存整天」由代码保证，而不是靠下游解析函数恰好没改。
			date = startOfUTCDay(*parsed)
		}
	}

	var member *model.TaskTeam
	if task.Mode != model.TaskModeSingle {
		target := req.UserID
		if target == 0 {
			target = actor
		}
		m, err := s.taskTeams.FindMember(ctx, task.ID, target)
		if err != nil {
			return err
		}
		if m == nil {
			return apperr.Newf(apperr.TaskNotAssigned,
				"该成员不在任务的成员分工里，无法为其登记工时")
		}
		if m.Status == model.TaskStatusDone || m.Status == model.TaskStatusCancel {
			return apperr.Newf(apperr.TaskStatusInvalid,
				"该成员的这部分工作已结束，不能再登记工时")
		}
		// 串行任务只有当前负责的那位能推进。
		if task.Mode == model.TaskModeLinear && task.AssignedTo > 0 && m.UserID != task.AssignedTo {
			return apperr.Newf(apperr.TaskStatusInvalid,
				"多人串行任务当前由其他成员负责，请等任务转交后再登记工时")
		}
		member = m
	}

	baseConsumed, baseLeft := task.Consumed, task.Left
	if member != nil {
		baseConsumed, baseLeft = member.Consumed, member.Left
	}
	newConsumed := baseConsumed + req.Consumed
	newLeft := baseLeft - req.Consumed
	if req.Left != nil {
		newLeft = *req.Left
	}
	if newLeft < 0 {
		// 工时超支是常事（活比想的多），但「还剩负数小时」没有意义。
		// 这时必须让用户明确给出剩余工时 —— 那正是重新估算的时刻。
		return apperr.Newf(apperr.TaskEffortExceedsLeft,
			"本次消耗 %.2f 小时超过剩余 %.2f 小时，请明确填写记录后的剩余工时", req.Consumed, baseLeft)
	}

	effort := &model.Effort{
		ObjectType: model.ObjectTypeTask, ObjectID: task.ID,
		ProductID: task.ProductID, ProjectID: task.ProjectID, ExecutionID: task.ExecutionID,
		UserID: actor, Work: req.Work, Date: date,
		Consumed: req.Consumed, Left: newLeft,
		Begin: req.Begin, End: req.End,
	}
	if member != nil {
		effort.TaskTeamID = member.ID
	}
	if err := s.efforts.Create(ctx, tx, effort); err != nil {
		return err
	}

	if member == nil {
		return s.tasks.UpdateFields(ctx, tx, task.ID, map[string]any{
			"consumed": newConsumed, "left": newLeft,
			"last_edited_date": now,
		})
	}

	if err := s.taskTeams.UpdateMember(ctx, tx, task.ID, member.UserID, map[string]any{
		"consumed": newConsumed, "left": newLeft,
	}); err != nil {
		return err
	}
	// 串行任务里，一位成员的活干完了就把任务转交给下一位。
	if task.Mode == model.TaskModeLinear && newLeft == 0 {
		if err := s.handOverLinear(ctx, tx, task, member.UserID, now); err != nil {
			return err
		}
	}

	h, err := s.taskTeams.SumByTask(ctx, tx, task.ID)
	if err != nil {
		return err
	}
	return s.tasks.RecomputeHours(ctx, tx, task.ID, h)
}

// handOverLinear 把串行任务转交给下一位还没做完的成员。
//
// 找不到下一位时不动任务状态：转交是排期动作，而「完成」是人的决定，
// 由负责人在看板上点。这里只把负责人换掉并留痕。
func (s *TaskService) handOverLinear(
	ctx context.Context, tx *gorm.DB, task *model.Task, finishedUser uint, now time.Time,
) error {
	if err := s.taskTeams.UpdateMember(ctx, tx, task.ID, finishedUser,
		map[string]any{"status": model.TaskStatusDone}); err != nil {
		return err
	}

	members, err := s.taskTeams.ListByTask(ctx, task.ID)
	if err != nil {
		return err
	}
	next := uint(0)
	for _, m := range members {
		if m.UserID == finishedUser {
			continue
		}
		if m.Status == model.TaskStatusDone || m.Status == model.TaskStatusCancel {
			continue
		}
		next = m.UserID
		break
	}
	if next == 0 || next == task.AssignedTo {
		return nil
	}

	if err := s.tasks.UpdateFields(ctx, tx, task.ID, map[string]any{
		"assigned_to": next, "assigned_date": now,
	}); err != nil {
		return err
	}
	_, err = s.recorder.Record(ctx, tx, history.Entry{
		ObjectType:  model.ObjectTypeTask,
		ObjectID:    task.ID,
		ProjectID:   task.ProjectID,
		ExecutionID: task.ExecutionID,
		ActorID:     0, // 系统自动转交
		Action:      model.ActionAssigned,
		Extra:       itoa(next),
		Comment:     "前一位成员已完成，任务自动转交给下一位",
	})
	return err
}

// ---------------------------------------------------------------------------
// 内部辅助
// ---------------------------------------------------------------------------

// assertExecution 校验迭代属于该项目；executionID 为 0 时返回 nil。
func (s *TaskService) assertExecution(ctx context.Context, executionID, projectID uint) (*model.Execution, error) {
	if executionID == 0 {
		return nil, nil
	}
	execution, err := s.executions.FindByID(ctx, executionID)
	if err != nil {
		return nil, err
	}
	if execution == nil || execution.ProjectID != projectID {
		return nil, apperr.Newf(apperr.ExecutionNotFound, "所选迭代不属于该项目")
	}
	if err := s.gate.AssertExecutionVisible(ctx, executionID); err != nil {
		return nil, err
	}
	return execution, nil
}

// assertProductInProject 校验任务所属产品确实关联到了该项目。
func (s *TaskService) assertProductInProject(ctx context.Context, productID, projectID uint) error {
	if productID == 0 {
		return nil
	}
	if err := s.gate.AssertProductVisible(ctx, productID); err != nil {
		return err
	}
	linked, err := s.projects.IsProductLinked(ctx, projectID, productID)
	if err != nil {
		return err
	}
	if !linked {
		return apperr.Newf(apperr.ProductNotFound, "该产品未关联到此项目")
	}
	return nil
}

// assertModule 校验模块属于该产品，且是任务树。
func (s *TaskService) assertModule(ctx context.Context, moduleID, productID uint) error {
	if moduleID == 0 {
		return nil
	}
	m, err := s.modules.FindByID(ctx, moduleID)
	if err != nil {
		return err
	}
	if m == nil {
		return apperr.New(apperr.ModuleNotFound)
	}
	if m.Root != productID || m.Type != model.ModuleTypeTask {
		return apperr.Newf(apperr.ModuleWrongRoot, "所选模块不属于该产品的任务模块树")
	}
	return nil
}

// assertTaskHours 校验工时数非负。
func (s *TaskService) assertTaskHours(req dto.TaskReq) error {
	if req.Estimate < 0 {
		return apperr.New(apperr.TaskEstimateInvalid)
	}
	if req.Left != nil && *req.Left < 0 {
		return apperr.New(apperr.TaskHoursInvalid)
	}
	for _, m := range req.Team {
		if m.Estimate < 0 || m.Left < 0 {
			return apperr.New(apperr.TaskHoursInvalid)
		}
	}
	return nil
}

// resolveStory 校验关联需求并返回其当前版本号。
func (s *TaskService) resolveStory(ctx context.Context, storyID uint) (uint, int, error) {
	if storyID == 0 {
		return 0, 0, nil
	}
	story, err := s.stories.FindByID(ctx, storyID)
	if err != nil {
		return 0, 0, err
	}
	if story == nil {
		return 0, 0, apperr.New(apperr.StoryNotFound)
	}
	if story.Status == model.StoryStatusClosed {
		return 0, 0, apperr.Newf(apperr.StoryStatusInvalid, "需求已关闭，不能再为其建任务")
	}
	return story.ID, story.Version, nil
}

// assertStoryVisible 校验关联需求对当前用户可见。
func (s *TaskService) assertStoryVisible(ctx context.Context, storyID uint) error {
	if storyID == 0 {
		return nil
	}
	story, err := s.stories.FindByID(ctx, storyID)
	if err != nil {
		return err
	}
	if story == nil {
		return apperr.New(apperr.StoryNotFound)
	}
	return s.gate.AssertProductVisible(ctx, story.ProductID)
}

// assertAssignable 校验负责人属于「本任务可用的团队」。
//
// 「谁在做」这件事不能挂在一个不在团队里的人身上：那个人看不到任务，
// 也收不到任何通知，任务实际上会变成没人做的悬空条目。
//
// 可用团队按 EffectiveMemberIDs 的口径取：迭代若配了自己的团队，
// 就只认迭代团队（那是「这个迭代抽调了谁」的显式表达）；没配则沿用项目团队。
func (s *TaskService) assertAssignable(ctx context.Context, projectID, executionID, userID uint) error {
	if userID == 0 {
		return nil
	}
	if err := s.team.assertUserExists(ctx, userID); err != nil {
		return err
	}
	ids, err := s.team.EffectiveMemberIDs(ctx, projectID, executionID)
	if err != nil {
		return err
	}
	if !slices.Contains(ids, userID) {
		return apperr.Newf(apperr.TeamMemberNotInProject,
			"该成员不在本任务的可用团队里，请先把他加入项目团队（迭代自带团队时以迭代团队为准）")
	}
	return nil
}

// buildMembers 把请求里的成员转成分工行，并补齐默认值。
//
// 成员同样必须在项目团队里：多人任务的成员与「指派人」是一回事，
// 都决定「这件事归谁」。放到团队之外，那个人既看不到任务，
// 也不会出现在任何待办里。
func (s *TaskService) buildMembers(ctx context.Context, projectID, executionID uint, req []dto.TaskTeamReq, mode string) ([]model.TaskTeam, error) {
	if mode == model.TaskModeSingle || len(req) == 0 {
		return nil, nil
	}
	out := make([]model.TaskTeam, 0, len(req))
	for i, m := range req {
		if m.UserID == 0 {
			continue
		}
		if err := s.assertAssignable(ctx, projectID, executionID, m.UserID); err != nil {
			return nil, err
		}
		left := m.Left
		// 未给出剩余工时时默认等于预计工时 —— 与新建任务的口径一致。
		if left == 0 && m.Estimate > 0 {
			left = m.Estimate
		}
		order := m.Order
		if order == 0 {
			order = i + 1
		}
		out = append(out, model.TaskTeam{
			UserID: m.UserID, Estimate: m.Estimate, Left: left,
			Status: model.TaskStatusWait, Order: order,
		})
	}
	if len(out) == 0 {
		return nil, apperr.Newf(apperr.InvalidParam, "多人任务必须指定至少一位成员")
	}
	return out, nil
}

// taskEstimate 取任务的预计工时：多人任务是各成员之和。
func taskEstimate(req dto.TaskReq, members []model.TaskTeam) float64 {
	if len(members) == 0 {
		return req.Estimate
	}
	total := 0.0
	for _, m := range members {
		total += m.Estimate
	}
	return total
}

// taskLeft 取任务的剩余工时：多人任务是各成员之和。
func taskLeft(req dto.TaskReq, members []model.TaskTeam, estimate float64) float64 {
	if len(members) > 0 {
		total := 0.0
		for _, m := range members {
			total += m.Left
		}
		return total
	}
	if req.Left != nil {
		return *req.Left
	}
	// 未指定剩余工时时默认等于预计工时：新任务「还剩多少」就是「预计多少」。
	return estimate
}

// normalTaskMode 归一化多人模式。
//
// 给了成员名单却没写模式时按并行处理：那是最常见的一种，
// 也让「填了就有人负责」这个直觉成立。
func normalTaskMode(mode string, memberCount int) string {
	switch mode {
	case model.TaskModeLinear, model.TaskModeMulti, model.TaskModeSingle:
		return mode
	case "":
		if memberCount > 0 {
			return model.TaskModeMulti
		}
		return model.TaskModeSingle
	default:
		return model.TaskModeSingle
	}
}

// withVersion 补上乐观锁版本号与编辑时间。
func withVersion(fields map[string]any, version int, actor uint, now time.Time) map[string]any {
	fields["version"] = version
	fields["last_edited_by"] = actor
	fields["last_edited_date"] = now
	return fields
}

// record 写一条任务操作历史。
func (s *TaskService) record(ctx context.Context, tx *gorm.DB, projectID, executionID uint, e history.Entry) error {
	if len(e.Changes) > 0 {
		e.Changes = fillUserNames(ctx, s.users, e.Changes)
	}
	_, err := s.recorder.Record(ctx, tx, e)
	return err
}

// parseOptionalDate 解析 YYYY-MM-DD；空串返回 nil。
func parseOptionalDate(value string) (*time.Time, error) {
	begin, _, err := parseDateRange(value, "")
	if err != nil {
		return nil, translateDateErr(err, apperr.InvalidParam)
	}
	return begin, nil
}

// startOfUTCDay 把任意时间压到它所属 UTC 日的零点。
//
// 存在的理由是给「业务日期」这一列定死一种精度：调用点要么来自用户传来的
// YYYY-MM-DD（本身就是 UTC 零点），要么来自 time.Now()。不在这里对齐的话，
// 同一列里就会同时存在「某一刻」和「某一天」，按天分组与排序都会说谎。
//
// 全平台时间口径是 UTC（见 configs/config.yaml 与 DSN 里的 loc/TimeZone），
// 所以「哪一天」也按 UTC 判：跨库一致，且不受服务器时区影响。
func startOfUTCDay(t time.Time) time.Time {
	u := t.UTC()
	return time.Date(u.Year(), u.Month(), u.Day(), 0, 0, 0, 0, time.UTC)
}

// defaultPriInt 未指定优先级时取「中」（禅道默认 3）。
func defaultPriInt(pri int) int {
	if pri <= 0 {
		return model.PriLow
	}
	return pri
}

// trimZero 去掉浮点数的尾随零，用于拼提示文案。
//
// 用 'f',-1 直接得到最短表示：3.50 → "3.5"、6 → "6"。
func trimZero(v float64) string {
	return strconv.FormatFloat(v, 'f', -1, 64)
}
