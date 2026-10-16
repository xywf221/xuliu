package service

import (
	"context"
	"slices"
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

// BugService 负责缺陷的全生命周期。
//
// 与需求、任务相比，缺陷有两处需要特别小心的语义，都是刻意的设计：
//
//  1. **没有独立的 reopen**。「重新激活」就是 activate：状态回到 active，
//     同时把上场解决的痕迹（resolution / resolvedBy / resolvedDate）
//     清干净 —— 一个「已修复」却处于激活态的缺陷是自相矛盾的。
//     清掉的是当前值，历史里那一次解决记录仍然在。
//  2. **resolution 与 status 是两件事**。resolution 说「怎么解决的」，
//     只在 resolve 时写入；close 不改它，所以已关闭的缺陷仍能看出
//     当初是修好的、重复的，还是设计如此。
type BugService struct {
	db         *gorm.DB
	bugs       *repository.BugRepo
	products   *repository.ProductRepo
	modules    *repository.ModuleRepo
	plans      *repository.PlanRepo
	projects   *repository.ProjectRepo
	executions *repository.ExecutionRepo
	tasks      *repository.TaskRepo
	stories    *repository.StoryRepo
	users      *repository.UserRepo
	files      *repository.FileRepo
	comments   *repository.CommentRepo
	gate       *Gate

	storySvc *StoryService
	notifier notifier
	recorder *history.Recorder
}

// NewBugService 构造缺陷服务。
//
// 注入 StoryService 只为一件事：「转需求」要真的建出一条需求。
// 那条需求该有版本快照、评审人回落、阶段与来源，这些规则属于需求域；
// 在这里重写一遍必然与需求侧漂移。
func NewBugService(
	db *gorm.DB,
	bugs *repository.BugRepo,
	products *repository.ProductRepo,
	modules *repository.ModuleRepo,
	plans *repository.PlanRepo,
	projects *repository.ProjectRepo,
	executions *repository.ExecutionRepo,
	tasks *repository.TaskRepo,
	stories *repository.StoryRepo,
	users *repository.UserRepo,
	files *repository.FileRepo,
	comments *repository.CommentRepo,
	storySvc *StoryService,
	gate *Gate,
	notify notifier,
) *BugService {
	return &BugService{
		db: db, bugs: bugs, products: products, modules: modules, plans: plans,
		projects: projects, executions: executions, tasks: tasks, stories: stories,
		users: users, files: files, comments: comments,
		storySvc: storySvc, gate: gate,
		notifier: orNoop(notify),
		recorder: history.New(BugLabeler{}),
	}
}

// ---------------------------------------------------------------------------
// 查询
// ---------------------------------------------------------------------------

// List 分页查询缺陷。
func (s *BugService) List(ctx context.Context, req dto.BugFilterReq) (*pagination.Result[dto.BugResp], error) {
	filter, err := s.buildFilter(ctx, req)
	if err != nil {
		return nil, err
	}
	result, err := s.bugs.List(ctx, filter)
	if err != nil {
		return nil, err
	}
	return s.decorateList(ctx, result)
}

// buildFilter 把查询参数转成仓储过滤条件。
//
// 必须无条件带上可见产品与可见项目：缺陷的可见性有两个来源
// （提给哪个产品、在哪个项目的活里发现的），漏掉任何一个都会漏数据。
func (s *BugService) buildFilter(ctx context.Context, req dto.BugFilterReq) (repository.BugFilter, error) {
	visibleProducts, err := s.gate.VisibleProducts(ctx)
	if err != nil {
		return repository.BugFilter{}, err
	}
	visibleProjects, err := s.gate.VisibleProjects(ctx)
	if err != nil {
		return repository.BugFilter{}, err
	}

	filter := repository.BugFilter{
		ProductID: req.ProductID, ProjectID: req.ProjectID,
		ExecutionID: req.ExecutionID, PlanID: req.PlanID,
		StoryID: req.StoryID, TaskID: req.TaskID,
		Status: req.Status, Resolution: req.Resolution, Unresolved: req.Unresolved,
		Type: req.Type, Severity: req.Severity, Pri: req.Pri, Confirmed: req.Confirmed,
		AssignedTo: req.AssignedTo, OpenedBy: req.OpenedBy, ResolvedBy: req.ResolvedBy,
		Keywords:   req.Keywords,
		ProductIDs: visibleProducts, ProjectIDs: visibleProjects,
		Pagination: pagination.Params{Page: req.Page, Size: req.Size, Order: req.Order},
	}
	if req.ModuleID > 0 {
		moduleID := req.ModuleID
		filter.ModuleID = &moduleID
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

// Counts 返回按状态统计的缺陷数，供列表页顶部标签使用。
func (s *BugService) Counts(ctx context.Context, req dto.BugFilterReq) (*dto.BugStatusCountsResp, error) {
	filter, err := s.buildFilter(ctx, req)
	if err != nil {
		return nil, err
	}
	// 统计不带状态筛选：标签本身就是在展示各状态的条数。
	filter.Status = ""

	counts, err := s.bugs.StatusCounts(ctx, filter)
	if err != nil {
		return nil, err
	}
	out := &dto.BugStatusCountsResp{
		Active:   counts[model.BugStatusActive],
		Resolved: counts[model.BugStatusResolved],
		Closed:   counts[model.BugStatusClosed],
	}
	unresolved := filter
	unresolved.Unresolved = true
	unresolvedCounts, err := s.bugs.StatusCounts(ctx, unresolved)
	if err != nil {
		return nil, err
	}
	for _, n := range unresolvedCounts {
		out.Unresolved += n
	}
	return out, nil
}

// Get 取缺陷详情。
func (s *BugService) Get(ctx context.Context, id uint) (*dto.BugResp, error) {
	bug, err := s.bugs.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := s.assertVisible(ctx, bug); err != nil {
		return nil, err
	}

	decorated, err := s.decorateList(ctx, &pagination.Result[model.Bug]{
		List: []model.Bug{*bug}, Total: 1, Page: 1, Size: 1,
	})
	if err != nil {
		return nil, err
	}
	out := decorated.List[0]
	out.Actions = availableStateActions(ctx, workflow.BugMachine, bug.Status)

	files, err := s.files.ListByObject(ctx, model.ObjectTypeBug, id)
	if err != nil {
		return nil, err
	}
	fileResps, err := s.fileResps(ctx, files)
	if err != nil {
		return nil, err
	}
	out.Files = fileResps
	return &out, nil
}

// fileResps 把附件转成出参并补上传者姓名。
func (s *BugService) fileResps(ctx context.Context, files []model.File) ([]dto.FileResp, error) {
	if len(files) == 0 {
		return nil, nil
	}
	ids := make([]uint, 0, len(files))
	for i := range files {
		ids = append(ids, files[i].UserID)
	}
	names, err := s.users.NameMap(ctx, ids)
	if err != nil {
		return nil, err
	}
	out := toFileRespList(files)
	for i := range out {
		out[i].UserName = names[out[i].UserID]
	}
	return out, nil
}

// decorateList 批量补全缺陷列表的展示字段。
func (s *BugService) decorateList(ctx context.Context, result *pagination.Result[model.Bug]) (*pagination.Result[dto.BugResp], error) {
	bugs := result.List
	if len(bugs) == 0 {
		return &pagination.Result[dto.BugResp]{
			List: []dto.BugResp{}, Total: result.Total, Page: result.Page, Size: result.Size,
		}, nil
	}

	productIDs := make([]uint, 0, len(bugs))
	projectIDs := make([]uint, 0, len(bugs))
	executionIDs := make([]uint, 0, len(bugs))
	moduleIDs := make([]uint, 0, len(bugs))
	planIDs := make([]uint, 0, len(bugs))
	storyIDs := make([]uint, 0, len(bugs))
	taskIDs := make([]uint, 0, len(bugs))
	dupIDs := make([]uint, 0, len(bugs))
	bugIDs := make([]uint, 0, len(bugs))
	userIDs := make([]uint, 0, len(bugs)*4)
	for i := range bugs {
		b := &bugs[i]
		productIDs = append(productIDs, b.ProductID)
		projectIDs = append(projectIDs, b.ProjectID)
		executionIDs = append(executionIDs, b.ExecutionID)
		moduleIDs = append(moduleIDs, b.ModuleID)
		planIDs = append(planIDs, b.PlanID)
		storyIDs = append(storyIDs, b.StoryID, b.ToStoryID)
		taskIDs = append(taskIDs, b.TaskID)
		dupIDs = append(dupIDs, b.DuplicateBugID)
		bugIDs = append(bugIDs, b.ID)
		userIDs = append(userIDs,
			b.OpenedBy, b.AssignedTo, b.ResolvedBy, b.ClosedBy)
	}

	products, err := s.products.NameMap(ctx, productIDs)
	if err != nil {
		return nil, err
	}
	projects, err := s.projects.NameMap(ctx, projectIDs)
	if err != nil {
		return nil, err
	}
	executions, err := s.executions.NameMap(ctx, executionIDs)
	if err != nil {
		return nil, err
	}
	modules, err := s.modules.NameMap(ctx, moduleIDs)
	if err != nil {
		return nil, err
	}
	plans, err := s.plans.TitleMap(ctx, planIDs)
	if err != nil {
		return nil, err
	}
	storyTitles, err := s.stories.TitleMap(ctx, storyIDs)
	if err != nil {
		return nil, err
	}
	taskNames, err := s.tasks.NameMap(ctx, taskIDs)
	if err != nil {
		return nil, err
	}
	dupTitles, err := s.bugs.TitleMap(ctx, dupIDs)
	if err != nil {
		return nil, err
	}
	users, err := s.users.NameMap(ctx, userIDs)
	if err != nil {
		return nil, err
	}
	commentCounts, err := s.comments.CountByObjects(ctx, model.ObjectTypeBug, bugIDs)
	if err != nil {
		return nil, err
	}
	fileCounts, err := s.fileCounts(ctx, bugIDs)
	if err != nil {
		return nil, err
	}

	out := make([]dto.BugResp, 0, len(bugs))
	for i := range bugs {
		b := &bugs[i]
		out = append(out, dto.BugResp{
			ID: b.ID, Code: "BUG-" + itoa(b.ID),

			ProductID: b.ProductID, ProductName: products[b.ProductID],
			ProjectID: b.ProjectID, ProjectName: projects[b.ProjectID],
			ExecutionID: b.ExecutionID, ExecutionName: executions[b.ExecutionID],
			ModuleID: b.ModuleID, ModuleName: modules[b.ModuleID],
			PlanID: b.PlanID, PlanTitle: plans[b.PlanID],
			StoryID: b.StoryID, StoryTitle: storyTitles[b.StoryID],
			StoryVersion: b.StoryVersion,
			TaskID:       b.TaskID, TaskName: taskNames[b.TaskID],

			Title: b.Title, Keywords: b.Keywords, Steps: b.Steps,

			Type: b.Type, TypeLabel: bugTypeLabels[b.Type],
			Severity: b.Severity, SeverityLabel: bugSeverityLabels[itoa(uint(b.Severity))],
			Pri: b.Pri, PriLabel: priLabels[itoa(uint(b.Pri))],

			OS: b.OS, Browser: b.Browser, Hardware: b.Hardware,

			Status: b.Status, StatusLabel: workflow.BugMachine.StatusLabel(b.Status),
			Resolution: b.Resolution, ResolutionLabel: bugResolutionLabels[b.Resolution],
			Confirmed: b.Confirmed, ActivatedCount: b.ActivatedCount,

			OpenedBuild: b.OpenedBuild, ResolvedBuild: b.ResolvedBuild,

			OpenedBy: b.OpenedBy, OpenedName: users[b.OpenedBy],
			OpenedDate: formatTimeValue(b.OpenedDate),
			AssignedTo: b.AssignedTo, AssignName: users[b.AssignedTo],
			AssignedDate: formatTimeValue(b.AssignedDate),
			Deadline:     formatDate(b.Deadline),
			ResolvedBy:   b.ResolvedBy, ResolvedName: users[b.ResolvedBy],
			ResolvedDate: formatTimeValue(b.ResolvedDate),
			ClosedBy:     b.ClosedBy, ClosedName: users[b.ClosedBy],
			ClosedDate: formatTimeValue(b.ClosedDate), ClosedReason: b.ClosedReason,
			ActivatedDate: formatTimeValue(b.ActivatedDate),

			DuplicateBugID: b.DuplicateBugID, DuplicateBugTitle: dupTitles[b.DuplicateBugID],
			ToStoryID: b.ToStoryID, ToStoryTitle: storyTitles[b.ToStoryID],
			RelatedBugIDs: mustDecodeIDs(b.RelatedBugIDs),

			Order:        b.Order,
			CommentCount: commentCounts[b.ID],
			FileCount:    fileCounts[b.ID],
		})
	}
	return &pagination.Result[dto.BugResp]{
		List: out, Total: result.Total, Page: result.Page, Size: result.Size,
	}, nil
}

// fileCounts 批量统计缺陷下的附件数。
func (s *BugService) fileCounts(ctx context.Context, bugIDs []uint) (map[uint]int64, error) {
	var rows []struct {
		ObjectID uint
		Total    int64
	}
	err := s.db.WithContext(ctx).Model(&model.File{}).
		Select("object_id, COUNT(*) AS total").
		Where("object_type = ? AND object_id IN ?", model.ObjectTypeBug, bugIDs).
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

// ---------------------------------------------------------------------------
// 创建与编辑
// ---------------------------------------------------------------------------

// Create 提交缺陷。
func (s *BugService) Create(ctx context.Context, req dto.BugReq) (*dto.BugResp, error) {
	product, err := s.products.FindByID(ctx, req.ProductID)
	if err != nil {
		return nil, err
	}
	if product == nil {
		return nil, apperr.New(apperr.ProductNotFound)
	}
	if err := s.gate.AssertProductVisible(ctx, req.ProductID); err != nil {
		return nil, err
	}
	if product.Status == model.ProductStatusClosed {
		return nil, apperr.Newf(apperr.ProductClosed, "产品已关闭，不能再提交缺陷")
	}
	if err := s.assertModule(ctx, req.ModuleID, req.ProductID); err != nil {
		return nil, err
	}
	if err := s.assertPlan(ctx, req.PlanID, req.ProductID); err != nil {
		return nil, err
	}
	if err := s.assertProjectExecution(ctx, req.ProjectID, req.ExecutionID); err != nil {
		return nil, err
	}
	if err := s.assertStoryTask(ctx, req.StoryID, req.TaskID, req.ProductID); err != nil {
		return nil, err
	}
	if err := s.assertAssignee(ctx, req.AssignedTo); err != nil {
		return nil, err
	}

	title := strings.TrimSpace(req.Title)
	if title == "" {
		return nil, apperr.Newf(apperr.InvalidParam, "缺陷标题不能为空")
	}
	deadline, err := parseOptionalDate(req.Deadline)
	if err != nil {
		return nil, err
	}
	related, err := encodeIDs(req.RelatedBugIDs)
	if err != nil {
		return nil, err
	}

	actor := currentUserID(ctx)
	now := time.Now().UTC()

	bugType := defaultString(req.Type, model.BugTypeCodeError)
	if err := assertBugType(bugType); err != nil {
		return nil, err
	}

	bug := &model.Bug{
		ProductID: req.ProductID, ProjectID: req.ProjectID,
		ExecutionID: req.ExecutionID, ModuleID: req.ModuleID, PlanID: req.PlanID,
		StoryID: req.StoryID, TaskID: req.TaskID,
		// 记下提缺陷时的需求版本：之后需求变了，这个缺陷针对的
		// 可能已经不是同一份描述了。
		StoryVersion: s.storyVersion(ctx, req.StoryID),

		Title: title, Keywords: req.Keywords, Steps: req.Steps,
		Type:     bugType,
		Severity: defaultLevel(req.Severity),
		Pri:      defaultLevel(req.Pri),

		OS: req.OS, Browser: req.Browser, Hardware: req.Hardware,
		OpenedBuild: req.OpenedBuild, Deadline: deadline,

		Status:    model.BugStatusActive,
		Confirmed: model.BugUnconfirmed,

		OpenedBy:      actor,
		OpenedDate:    &now,
		AssignedTo:    req.AssignedTo,
		RelatedBugIDs: related,
		Order:         req.Order,
	}
	if req.AssignedTo > 0 {
		bug.AssignedDate = &now
	}

	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := s.bugs.Create(ctx, tx, bug); err != nil {
			return err
		}
		if len(req.Files) > 0 {
			if err := s.files.BindToObject(ctx, tx, req.Files, model.ObjectTypeBug, bug.ID); err != nil {
				return err
			}
		}
		if err := s.record(ctx, tx, bug, history.Entry{
			ObjectType:  model.ObjectTypeBug,
			ObjectID:    bug.ID,
			ProductID:   bug.ProductID,
			ProjectID:   bug.ProjectID,
			ExecutionID: bug.ExecutionID,
			ActorID:     actor,
			Action:      model.ActionOpened,
			Comment:     req.Steps,
			Files:       req.Files,
			Changes:     []history.Change{{Field: "title", New: title}},
		}); err != nil {
			return err
		}
		noticeTitle, noticeContent := noticeBugAssigned(bug.ID, bug.Title)
		return s.notifier.Send(ctx, tx, bug.AssignedTo, actor,
			model.ObjectTypeBug, bug.ID, noticeTitle, noticeContent)
	})
	if err != nil {
		return nil, err
	}
	return s.Get(ctx, bug.ID)
}

// Update 编辑缺陷。
//
// 任何状态下都允许编辑：缺陷的关键信息常在解决过程中才补全
// ——「其实复现步骤里少了一步」比「必须先激活才能改」重要得多。
func (s *BugService) Update(ctx context.Context, id uint, req dto.BugReq) (*dto.BugResp, error) {
	bug, err := s.bugs.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := s.assertVisible(ctx, bug); err != nil {
		return nil, err
	}
	if req.ProductID != 0 && req.ProductID != bug.ProductID {
		return nil, apperr.Newf(apperr.InvalidParam, "不能把缺陷改到别的产品下")
	}
	if err := s.assertModule(ctx, req.ModuleID, bug.ProductID); err != nil {
		return nil, err
	}
	if err := s.assertPlan(ctx, req.PlanID, bug.ProductID); err != nil {
		return nil, err
	}
	if err := s.assertProjectExecution(ctx, req.ProjectID, req.ExecutionID); err != nil {
		return nil, err
	}
	if err := s.assertStoryTask(ctx, req.StoryID, req.TaskID, bug.ProductID); err != nil {
		return nil, err
	}
	if err := s.assertAssignee(ctx, req.AssignedTo); err != nil {
		return nil, err
	}

	title := strings.TrimSpace(req.Title)
	if title == "" {
		return nil, apperr.Newf(apperr.InvalidParam, "缺陷标题不能为空")
	}
	deadline, err := parseOptionalDate(req.Deadline)
	if err != nil {
		return nil, err
	}
	related, err := encodeIDs(req.RelatedBugIDs)
	if err != nil {
		return nil, err
	}

	actor := currentUserID(ctx)
	now := time.Now().UTC()

	bugType := defaultString(req.Type, bug.Type)
	if err := assertBugType(bugType); err != nil {
		return nil, err
	}

	fields := map[string]any{
		"project_id":      req.ProjectID,
		"execution_id":    req.ExecutionID,
		"module_id":       req.ModuleID,
		"plan_id":         req.PlanID,
		"story_id":        req.StoryID,
		"story_version":   s.storyVersion(ctx, req.StoryID),
		"task_id":         req.TaskID,
		"title":           title,
		"keywords":        req.Keywords,
		"steps":           req.Steps,
		"type":            bugType,
		"severity":        defaultLevel(req.Severity),
		"pri":             defaultLevel(req.Pri),
		"os":              req.OS,
		"browser":         req.Browser,
		"hardware":        req.Hardware,
		"opened_build":    req.OpenedBuild,
		"deadline":        deadline,
		"related_bug_ids": related,
		"order":           req.Order,

		"last_edited_by":   actor,
		"last_edited_date": now,
	}
	if req.AssignedTo != bug.AssignedTo {
		fields["assigned_to"] = req.AssignedTo
		fields["assigned_date"] = now
	}

	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := s.bugs.UpdateFields(ctx, tx, id, fields); err != nil {
			return err
		}
		// 与任务的编辑同口径：换了负责人就通知，且不看有没有字段差异。
		if req.AssignedTo != bug.AssignedTo && req.AssignedTo > 0 {
			noticeTitle, noticeContent := noticeBugAssigned(id, title)
			if err := s.notifier.Send(ctx, tx, req.AssignedTo, actor,
				model.ObjectTypeBug, id, noticeTitle, noticeContent); err != nil {
				return err
			}
		}
		changes := bugDiff(bug, fields)
		if len(changes) == 0 {
			return nil
		}
		return s.record(ctx, tx, bug, history.Entry{
			ObjectType:  model.ObjectTypeBug,
			ObjectID:    id,
			ProductID:   bug.ProductID,
			ProjectID:   bug.ProjectID,
			ExecutionID: bug.ExecutionID,
			ActorID:     actor,
			Action:      model.ActionEdited,
			Changes:     changes,
		})
	})
	if err != nil {
		return nil, err
	}
	return s.Get(ctx, id)
}

// Delete 删除缺陷。
//
// 已经转成需求或任务的缺陷不能删：那条需求/任务还在，删掉缺陷会让
// 「它是从哪来的」这段来龙去脉断掉。这种缺陷该走关闭。
func (s *BugService) Delete(ctx context.Context, id uint) error {
	bug, err := s.bugs.GetByID(ctx, id)
	if err != nil {
		return err
	}
	if err := s.assertVisible(ctx, bug); err != nil {
		return err
	}
	if bug.ToStoryID > 0 || bug.ToTaskID > 0 {
		return apperr.New(apperr.BugHasDownstream)
	}

	actor := currentUserID(ctx)
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if _, err := s.comments.DeleteByObject(ctx, tx, model.ObjectTypeBug, id); err != nil {
			return err
		}
		if res := tx.WithContext(ctx).Model(&model.File{}).
			Where("object_type = ? AND object_id = ?", model.ObjectTypeBug, id).
			Updates(map[string]any{"object_type": "", "object_id": 0}); res.Error != nil {
			return apperr.Wrap(apperr.Internal, res.Error)
		}
		if err := s.record(ctx, tx, bug, history.Entry{
			ObjectType:  model.ObjectTypeBug,
			ObjectID:    id,
			ProductID:   bug.ProductID,
			ProjectID:   bug.ProjectID,
			ExecutionID: bug.ExecutionID,
			ActorID:     actor,
			Action:      model.ActionDeleted,
		}); err != nil {
			return err
		}
		return s.bugs.Delete(ctx, tx, id)
	})
}

// ---------------------------------------------------------------------------
// 状态流转
// ---------------------------------------------------------------------------

// BugActionInput 是缺陷动作的公共入参。
type BugActionInput struct {
	Action  string
	Comment string

	// 指派 / 激活
	AssignedTo uint

	// 解决
	Resolution     string
	ResolvedBuild  string
	DuplicateBugID uint

	// 转需求
	StoryProductID uint
	StoryTitle     string
	StorySpec      string
	StoryVerify    string
	StoryFiles     []uint
	StoryReviewers []uint
}

// Apply 执行一个缺陷动作。
func (s *BugService) Apply(ctx context.Context, id uint, in BugActionInput) (*dto.BugResp, error) {
	bug, err := s.bugs.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := s.assertVisible(ctx, bug); err != nil {
		return nil, err
	}

	trans, statusErr := workflow.BugMachine.Resolve(in.Action, bug.Status)
	if statusErr != nil {
		return nil, workflow.BugMachine.DescribeError(statusErr)
	}

	actor := currentUserID(ctx)
	now := time.Now().UTC()

	var changes []history.Change
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		fields, cs, err := s.actionFields(ctx, tx, bug, in, trans, actor, now)
		if err != nil {
			return err
		}
		changes = cs
		if err := s.bugs.UpdateFields(ctx, tx, id, fields); err != nil {
			return err
		}
		if err := s.record(ctx, tx, bug, history.Entry{
			ObjectType:  model.ObjectTypeBug,
			ObjectID:    id,
			ProductID:   bug.ProductID,
			ProjectID:   bug.ProjectID,
			ExecutionID: bug.ExecutionID,
			ActorID:     actor,
			Action:      workflow.BugMachine.LogActionOf(in.Action),
			Comment:     in.Comment,
			Extra:       s.actionExtra(in, bug),
			Changes:     changes,
		}); err != nil {
			return err
		}

		// 只通知「接下来该动手的那个人」：
		//   指派 → 新的负责人；解决 → 提缺陷的人（验证与关闭归他）；
		//   重新激活 → 上次解决它的人，没有则当前负责人。
		// 确认、关闭这两个动作不改变「该谁动」，不发通知。
		to := uint(0)
		var noticeTitle, noticeContent string
		switch in.Action {
		case "assign":
			to = in.AssignedTo
			noticeTitle, noticeContent = noticeBugAssigned(id, bug.Title)
		case "resolve":
			to = bug.OpenedBy
			noticeTitle, noticeContent = noticeBugResolved(id)
		case "activate":
			to = bugActivateTarget(bug)
			noticeTitle, noticeContent = noticeBugActivated(id)
		default:
			return nil
		}
		return s.notifier.Send(ctx, tx, to, actor,
			model.ObjectTypeBug, id, noticeTitle, noticeContent)
	})
	if err != nil {
		return nil, err
	}
	return s.Get(ctx, id)
}

// bugActivateTarget 决定「缺陷被重新激活」该通知谁。
//
// 优先上次把它解决掉的人：你说解决了、结果别人一看还在，那条缺陷
// 最该回到的就是你的待办里。没有解决人（例如直接从关闭激活）时
// 退回到当前负责人；再没有就不发（Send 会忽略收件人为 0）。
func bugActivateTarget(bug *model.Bug) uint {
	if bug.ResolvedBy > 0 {
		return bug.ResolvedBy
	}
	return bug.AssignedTo
}

// actionExtra 生成操作历史里的附加信息。
//
// Extra 的两种形态：指派动作存目标用户 id，状态变更动作存 "old|new"。
// 这两种形态与禅道一致，便于把老库里的历史直接导进来。
func (s *BugService) actionExtra(in BugActionInput, bug *model.Bug) string {
	if in.Action == "assign" {
		return itoa(in.AssignedTo)
	}
	if target, ok := workflow.BugMachine.Transition(in.Action); ok && target.To != "" {
		return workflow.StatusChangeExtra(bug.Status, target.To)
	}
	return ""
}

// actionFields 按动作算出要写入的字段与字段级变更。
func (s *BugService) actionFields(
	ctx context.Context, tx *gorm.DB, bug *model.Bug,
	in BugActionInput, trans workflow.Transition, actor uint, now time.Time,
) (map[string]any, []history.Change, error) {
	fields := map[string]any{}

	switch in.Action {
	case "confirm":
		if bug.Confirmed == model.BugConfirmed {
			return nil, nil, apperr.New(apperr.BugAlreadyConfirmed)
		}
		fields["confirmed"] = model.BugConfirmed
		return fields, []history.Change{{Field: "confirmed", Old: "0", New: "1"}}, nil

	case "assign":
		if in.AssignedTo == 0 {
			return nil, nil, apperr.Newf(apperr.InvalidParam, "请选择指派人")
		}
		if in.AssignedTo == bug.AssignedTo {
			return nil, nil, apperr.Newf(apperr.InvalidParam, "缺陷已经指派给该成员")
		}
		if err := s.assertAssignee(ctx, in.AssignedTo); err != nil {
			return nil, nil, err
		}
		fields["assigned_to"] = in.AssignedTo
		fields["assigned_date"] = now
		return fields, []history.Change{{
			Field: "assigned_to",
			Old:   itoa(bug.AssignedTo),
			New:   itoa(in.AssignedTo),
		}}, nil

	case "resolve":
		resolution := strings.TrimSpace(in.Resolution)
		if resolution == "" {
			return nil, nil, apperr.New(apperr.BugResolutionRequired)
		}
		if !slices.Contains(model.BugResolutions, resolution) {
			return nil, nil, apperr.Newf(apperr.BugResolutionInvalid,
				"解决方案取值不合法：%s", resolution)
		}
		if resolution == model.BugResolutionDuplicate {
			// 「重复」必须说清重复于哪一条 —— 否则这条缺陷等于被丢弃了。
			if in.DuplicateBugID == 0 {
				return nil, nil, apperr.New(apperr.BugDuplicateTargetRequired)
			}
			if in.DuplicateBugID == bug.ID {
				return nil, nil, apperr.New(apperr.BugDuplicateOfSelf)
			}
			target, err := s.bugs.FindByID(ctx, in.DuplicateBugID)
			if err != nil {
				return nil, nil, err
			}
			if target == nil {
				return nil, nil, apperr.New(apperr.BugNotFound)
			}
			fields["duplicate_bug_id"] = in.DuplicateBugID
		}

		fields["status"] = trans.To
		fields["resolution"] = resolution
		fields["resolved_build"] = in.ResolvedBuild
		fields["resolved_by"] = actor
		fields["resolved_date"] = now
		changes := []history.Change{
			{Field: "status", Old: bug.Status, New: trans.To},
			{Field: "resolution", Old: bug.Resolution, New: resolution},
		}
		return fields, changes, nil

	case "close":
		fields["status"] = trans.To
		fields["closed_by"] = actor
		fields["closed_date"] = now
		// 关闭原因取当前的解决方式：一条被关闭的缺陷，它「为什么关」
		// 就是当初「怎么解决的」。这里不另设一套取值，省掉一次对不齐的风险。
		fields["closed_reason"] = bug.Resolution
		return fields, []history.Change{
			{Field: "status", Old: bug.Status, New: trans.To},
		}, nil

	case "activate":
		// 重新激活要把上一轮的解决痕迹清干净：状态回到 active 却还写着
		// 「已修复」是自相矛盾的。历史里那一次解决记录不受影响。
		//
		// 但 to_story_id 不清：那个需求是真的建出来了，还留在产品里。
		// 把这条线抹掉只会让人以为它凭空出现；保留着，缺陷上就写着
		// 「我是从这条需求来的」。
		fields["status"] = trans.To
		fields["resolution"] = ""
		fields["resolved_by"] = 0
		fields["resolved_date"] = nil
		fields["closed_by"] = 0
		fields["closed_date"] = nil
		fields["closed_reason"] = ""
		fields["duplicate_bug_id"] = 0
		fields["activated_count"] = bug.ActivatedCount + 1
		fields["activated_date"] = now
		if in.AssignedTo > 0 {
			if err := s.assertAssignee(ctx, in.AssignedTo); err != nil {
				return nil, nil, err
			}
			fields["assigned_to"] = in.AssignedTo
			fields["assigned_date"] = now
		}
		return fields, []history.Change{
			{Field: "status", Old: bug.Status, New: trans.To},
			{Field: "resolution", Old: bug.Resolution, New: ""},
		}, nil

	case "tostory":
		return s.toStoryFields(ctx, tx, bug, in, actor, now)
	}

	return nil, nil, apperr.Newf(apperr.Internal, "动作 %s 尚未实现", in.Action)
}

// toStoryFields 把缺陷转成需求，并算出缺陷侧要写的字段。
//
// 需求是**新建**出来的，所以这里必须能拿到事务：需求与「缺陷已转需求」
// 这两件事要么都成，要么都不成 —— 否则会出现「需求建了、缺陷还挂在激活态」
// 的孤儿，或者反过来「缺陷说已转需求，需求却不存在」。
func (s *BugService) toStoryFields(
	ctx context.Context, tx *gorm.DB, bug *model.Bug,
	in BugActionInput, actor uint, now time.Time,
) (map[string]any, []history.Change, error) {
	productID := in.StoryProductID
	if productID == 0 {
		productID = bug.ProductID
	}
	title := strings.TrimSpace(in.StoryTitle)
	if title == "" {
		title = bug.Title
	}
	spec := in.StorySpec
	if spec == "" {
		// 缺陷的重现步骤就是最好的需求描述 —— 它已经在讲「怎么出问题的」。
		spec = bug.Steps
	}

	story, err := s.storySvc.CreateTx(ctx, tx, dto.CreateStoryReq{
		ProductID: productID,
		Title:     title,
		Spec:      spec,
		Verify:    in.StoryVerify,
		// 来源标成「由缺陷转入」，需求列表里能一眼看出它的出处。
		Source:     model.StorySourceBug,
		SourceNote: "缺陷 #" + itoa(bug.ID),
		AssignedTo: in.AssignedTo,
		// 附件的归属由 BindToObject 在需求侧改写：缺陷的截图正好
		// 就是这条需求最该带上的证据。
		Files:     in.StoryFiles,
		Reviewers: in.StoryReviewers,
		Comment:   in.Comment,
	})
	if err != nil {
		return nil, nil, err
	}

	fields := map[string]any{
		"status":        model.BugStatusResolved,
		"resolution":    model.BugResolutionToStory,
		"to_story_id":   story.ID,
		"resolved_by":   actor,
		"resolved_date": now,
	}
	return fields, []history.Change{
		{Field: "status", Old: bug.Status, New: model.BugStatusResolved},
		{Field: "resolution", Old: bug.Resolution, New: model.BugResolutionToStory},
		{Field: "to_story_id", Old: itoa(bug.ToStoryID), New: itoa(story.ID)},
	}, nil
}

// ---------------------------------------------------------------------------
// 内部辅助
// ---------------------------------------------------------------------------

// assertVisible 按缺陷所在的最贴近层级校验可见性。
func (s *BugService) assertVisible(ctx context.Context, bug *model.Bug) error {
	// 缺陷可能挂在迭代/项目下，也可能只属于某个产品。
	// 与 Gate.assertLocation 同一口径：越贴近的层级越有发言权。
	switch {
	case bug.ExecutionID > 0:
		return s.gate.AssertExecutionVisible(ctx, bug.ExecutionID)
	case bug.ProjectID > 0:
		return s.gate.AssertProjectVisible(ctx, bug.ProjectID)
	default:
		return s.gate.AssertProductVisible(ctx, bug.ProductID)
	}
}

// assertModule 校验模块属于该产品，且是缺陷模块树。
func (s *BugService) assertModule(ctx context.Context, moduleID, productID uint) error {
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
	if m.Root != productID || m.Type != model.ModuleTypeBug {
		return apperr.Newf(apperr.ModuleWrongRoot, "所选模块不属于该产品的缺陷模块树")
	}
	return nil
}

// assertPlan 校验计划属于该产品。
func (s *BugService) assertPlan(ctx context.Context, planID, productID uint) error {
	if planID == 0 {
		return nil
	}
	plan, err := s.plans.FindByID(ctx, planID)
	if err != nil {
		return err
	}
	if plan == nil {
		return apperr.New(apperr.PlanNotFound)
	}
	if plan.ProductID != productID {
		return apperr.Newf(apperr.PlanStatusInvalid, "所选计划不属于该产品")
	}
	return nil
}

// assertProjectExecution 校验项目可见、迭代属于该项目。
func (s *BugService) assertProjectExecution(ctx context.Context, projectID, executionID uint) error {
	if executionID > 0 {
		execution, err := s.executions.FindByID(ctx, executionID)
		if err != nil {
			return err
		}
		if execution == nil {
			return apperr.New(apperr.ExecutionNotFound)
		}
		if projectID > 0 && execution.ProjectID != projectID {
			return apperr.Newf(apperr.ExecutionNotFound, "所选迭代不属于该项目")
		}
		return s.gate.AssertExecutionVisible(ctx, executionID)
	}
	if projectID > 0 {
		return s.gate.AssertProjectVisible(ctx, projectID)
	}
	return nil
}

// assertStoryTask 校验关联的需求与任务。
//
// 需求必须与缺陷同属一个产品：跨产品的关联会让缺陷出现在一个它
// 其实不属于的产品视图里（点开关联需求还会直接越权）。
// 任务只校验存在 —— 任务归属于项目，而项目可以关联多个产品，
// 没有唯一的产品归属可以拿来比对。
func (s *BugService) assertStoryTask(ctx context.Context, storyID, taskID, productID uint) error {
	if storyID > 0 {
		story, err := s.stories.FindByID(ctx, storyID)
		if err != nil {
			return err
		}
		if story == nil {
			return apperr.New(apperr.StoryNotFound)
		}
		if story.ProductID != productID {
			return apperr.Newf(apperr.InvalidParam, "关联的需求不属于该产品")
		}
	}
	if taskID > 0 {
		task, err := s.tasks.FindByID(ctx, taskID)
		if err != nil {
			return err
		}
		if task == nil {
			return apperr.New(apperr.TaskNotFound)
		}
	}
	return nil
}

// assertAssignee 校验指派人存在。
//
// 这里刻意**不**要求团队归属：缺陷可能来自测试、客服、甚至客户支持，
// 指派对象常常不在项目团队里。缺陷的产品侧也没有团队概念，
// 拿项目团队去卡会让「把缺陷派给测试同学」直接不可用。
func (s *BugService) assertAssignee(ctx context.Context, userID uint) error {
	if userID == 0 {
		return nil
	}
	user, err := s.users.FindByID(ctx, userID)
	if err != nil {
		return err
	}
	if user == nil {
		return apperr.New(apperr.UserNotFound)
	}
	return nil
}

// storyVersion 取需求的当前版本号。
//
// 用于记下「提交缺陷时需求是第几版」：需求后来改了，这个缺陷针对的
// 可能已经不是同一份描述了。查不到就记 0（需求不存在或已删除）。
func (s *BugService) storyVersion(ctx context.Context, storyID uint) int {
	if storyID == 0 {
		return 0
	}
	story, err := s.stories.FindByID(ctx, storyID)
	if err != nil || story == nil {
		return 0
	}
	return story.Version
}

// record 写一条缺陷操作历史。
func (s *BugService) record(ctx context.Context, tx *gorm.DB, bug *model.Bug, e history.Entry) error {
	// 缺陷的变更里带着用户 id（指派、解决、关闭），在事务外批量把名字补齐。
	if e.Changes = fillUserNames(ctx, s.users, e.Changes); len(e.Changes) == 0 {
		e.Changes = nil
	}
	_, err := s.recorder.Record(ctx, tx, e)
	return err
}

// assertBugType 校验缺陷类型取值。
//
// DTO 上的 oneof 校验只在 HTTP 那条路上生效（服务层也可能被别的入口直接调用），
// 所以这里再挡一道 —— 一个没登记的 type 会一路写进库，
// 然后在前端显示成一个空白标签。
func assertBugType(bugType string) error {
	if !slices.Contains(model.BugTypes, bugType) {
		return apperr.Newf(apperr.InvalidParam, "缺陷类型取值不合法：%s", bugType)
	}
	return nil
}

// defaultLevel 把 0 或越界的等级拉回默认值。
//
// 严重程度与优先级的默认都是 3（1 最高、4 最低）。
func defaultLevel(v int) int {
	if v < 1 || v > 4 {
		return 3
	}
	return v
}
