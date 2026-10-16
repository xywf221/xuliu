package service

import (
	"context"
	"math"
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

// ProjectService 负责项目、项目团队，以及与产品的关联。
//
// 项目在模型上是「两层结构的上层」：往下挂迭代，迭代里放需求与任务。
// 因此本服务承担三件容易被忽略的事：
//
//  1. **关联产品**。没有产品的项目排不进需求、建不了任务，所以创建时
//     就要求至少一个产品，而不是等到用不了再报错。
//  2. **工时与进度的汇总**。项目自身的工时是它下面全部任务的聚合值，
//     任务侧每次动工时都会回头叫 RecomputeHours 重算一遍。
//  3. **团队**。团队成员既是权限（私有项目的可见性来源之一），
//     也是业务约束（任务只能派给团队成员）。
type ProjectService struct {
	db          *gorm.DB
	projects    *repository.ProjectRepo
	executions  *repository.ExecutionRepo
	tasks       *repository.TaskRepo
	products    *repository.ProductRepo
	proProducts *repository.ProjectProductRepo
	links       *repository.ProjectStoryRepo
	stories     *repository.StoryRepo
	users       *repository.UserRepo
	gate        *Gate
	team        *teamOps

	recorder *history.Recorder
}

// NewProjectService 构造项目服务。
func NewProjectService(
	db *gorm.DB,
	projects *repository.ProjectRepo,
	executions *repository.ExecutionRepo,
	tasks *repository.TaskRepo,
	products *repository.ProductRepo,
	proProducts *repository.ProjectProductRepo,
	links *repository.ProjectStoryRepo,
	stories *repository.StoryRepo,
	users *repository.UserRepo,
	teams *repository.TeamRepo,
	gate *Gate,
) *ProjectService {
	return &ProjectService{
		db: db, projects: projects, executions: executions, tasks: tasks,
		products: products, proProducts: proProducts, links: links,
		stories: stories, users: users,
		gate: gate,
		team: &teamOps{
			teams: teams, users: users, projects: projects,
			executions: executions, tasks: tasks, gate: gate,
		},
		recorder: history.New(ProjectLabeler{}),
	}
}

// ---------------------------------------------------------------------------
// 查询
// ---------------------------------------------------------------------------

// List 分页查询项目。
func (s *ProjectService) List(ctx context.Context, req dto.ProjectFilterReq) (*pagination.Result[dto.ProjectResp], error) {
	visible, err := s.gate.VisibleProjects(ctx)
	if err != nil {
		return nil, err
	}

	result, err := s.projects.List(ctx, repository.ProjectFilter{
		Status:     req.Status,
		Model:      req.Model,
		Keywords:   req.Keywords,
		ProductID:  req.ProductID,
		VisibleIDs: visible,
		Pagination: pagination.Params{Page: req.Page, Size: req.Size, Order: req.Order},
	})
	if err != nil {
		return nil, err
	}
	return s.decorateList(ctx, result)
}

// ListBrief 返回项目简要列表，供下拉与筛选栏使用。
func (s *ProjectService) ListBrief(ctx context.Context, limit int) ([]dto.ProjectBrief, error) {
	visible, err := s.gate.VisibleProjects(ctx)
	if err != nil {
		return nil, err
	}
	projects, err := s.projects.ListBrief(ctx, visible, limit)
	if err != nil {
		return nil, err
	}
	out := make([]dto.ProjectBrief, 0, len(projects))
	for _, p := range projects {
		out = append(out, dto.ProjectBrief{ID: p.ID, Name: p.Name, Code: p.Code})
	}
	return out, nil
}

// Get 取项目详情。
func (s *ProjectService) Get(ctx context.Context, id uint) (*dto.ProjectResp, error) {
	project, err := s.projects.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := s.gate.AssertProjectVisible(ctx, id); err != nil {
		return nil, err
	}

	decorated, err := s.decorateList(ctx, &pagination.Result[model.Project]{
		List: []model.Project{*project}, Total: 1, Page: 1, Size: 1,
	})
	if err != nil {
		return nil, err
	}
	out := decorated.List[0]
	out.Actions = availableStateActions(ctx, workflow.ProjectMachine, project.Status)

	team, err := s.team.list(ctx, model.TeamRootProject, id)
	if err != nil {
		return nil, err
	}
	out.Team = team

	if out.ExecutionCount, err = s.executions.Count(ctx, "project_id = ?", id); err != nil {
		return nil, err
	}
	if out.TaskCount, err = s.tasks.Count(ctx, "project_id = ?", id); err != nil {
		return nil, err
	}
	if out.StoryCount, err = s.links.CountByProject(ctx, id); err != nil {
		return nil, err
	}
	return &out, nil
}

// decorateList 批量补全列表所需的展示字段。
func (s *ProjectService) decorateList(ctx context.Context, result *pagination.Result[model.Project]) (*pagination.Result[dto.ProjectResp], error) {
	projects := result.List
	if len(projects) == 0 {
		return &pagination.Result[dto.ProjectResp]{
			List: []dto.ProjectResp{}, Total: result.Total, Page: result.Page, Size: result.Size,
		}, nil
	}

	projectIDs := make([]uint, 0, len(projects))
	userIDs := make([]uint, 0, len(projects)*5)
	for i := range projects {
		p := &projects[i]
		projectIDs = append(projectIDs, p.ID)
		userIDs = append(userIDs, p.OpenedBy, p.PO, p.PM, p.QD, p.RD)
	}

	userNames, err := s.users.NameMap(ctx, userIDs)
	if err != nil {
		return nil, err
	}
	teamRows, err := s.team.memberRows(ctx, model.TeamRootProject, projectIDs)
	if err != nil {
		return nil, err
	}

	out := make([]dto.ProjectResp, 0, len(projects))
	for i := range projects {
		p := &projects[i]
		item := projectToResp(p)
		item.OpenedName = userNames[p.OpenedBy]
		item.POName = userNames[p.PO]
		item.PMName = userNames[p.PM]
		item.QDName = userNames[p.QD]
		item.RDName = userNames[p.RD]
		item.MemberCount = len(teamRows[p.ID])
		out = append(out, item)
	}

	result2 := &pagination.Result[dto.ProjectResp]{
		List: out, Total: result.Total, Page: result.Page, Size: result.Size,
	}
	// 关联产品的名字也一次补齐。
	if err := s.fillProducts(ctx, result2.List); err != nil {
		return nil, err
	}
	return result2, nil
}

// fillProducts 批量补全项目关联的产品。
func (s *ProjectService) fillProducts(ctx context.Context, list []dto.ProjectResp) error {
	if len(list) == 0 {
		return nil
	}
	projectIDs := make([]uint, 0, len(list))
	index := make(map[uint]int, len(list))
	for i := range list {
		projectIDs = append(projectIDs, list[i].ID)
		index[list[i].ID] = i
	}

	var rows []model.ProjectProduct
	if err := s.db.WithContext(ctx).Model(&model.ProjectProduct{}).
		Where("project_id IN ?", projectIDs).Order("id ASC").Find(&rows).Error; err != nil {
		return apperr.Wrap(apperr.Internal, err)
	}
	if len(rows) == 0 {
		return nil
	}

	productIDs := make([]uint, 0, len(rows))
	for _, r := range rows {
		productIDs = append(productIDs, r.ProductID)
	}
	products, err := s.products.ListByIDs(ctx, productIDs)
	if err != nil {
		return err
	}
	names := make(map[uint]dto.ProjectProductResp, len(products))
	for _, p := range products {
		names[p.ID] = dto.ProjectProductResp{
			ProductID: p.ID, ProductName: p.Name, ProductCode: p.Code,
		}
	}
	for _, r := range rows {
		i, ok := index[r.ProjectID]
		if !ok {
			continue
		}
		if item, ok := names[r.ProductID]; ok {
			list[i].Products = append(list[i].Products, item)
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// 创建与编辑
// ---------------------------------------------------------------------------

// Create 新建项目。
func (s *ProjectService) Create(ctx context.Context, req dto.ProjectReq) (*dto.ProjectResp, error) {
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return nil, apperr.Newf(apperr.InvalidParam, "项目名称不能为空")
	}
	code := strings.ToUpper(strings.TrimSpace(req.Code))
	if code == "" {
		return nil, apperr.Newf(apperr.InvalidParam, "项目代号不能为空")
	}

	if taken, err := s.projects.NameTaken(ctx, name, 0); err != nil {
		return nil, err
	} else if taken {
		return nil, apperr.New(apperr.ProjectNameTaken)
	}
	if taken, err := s.projects.CodeTaken(ctx, code, 0); err != nil {
		return nil, err
	} else if taken {
		return nil, apperr.New(apperr.ProjectCodeTaken)
	}

	begin, end, err := parseDateRange(req.Begin, req.End)
	if err != nil {
		return nil, translateDateErr(err, apperr.ProjectDateInvalid)
	}
	if err := s.assertProducts(ctx, req.ProductIDs); err != nil {
		return nil, err
	}
	if err := s.team.assertMembers(ctx, req.Team); err != nil {
		return nil, err
	}

	whitelist, err := encodeIDs(req.Whitelist)
	if err != nil {
		return nil, err
	}

	actor := currentUserID(ctx)
	now := time.Now().UTC()

	project := &model.Project{
		Name: name, Code: code,
		Model:  defaultString(req.Model, model.ProjectModelScrum),
		Status: model.ProjectStatusWait,

		Begin: begin, End: end, Days: calendarDays(begin, end),

		Desc: req.Desc,
		PO:   req.PO, PM: req.PM, QD: req.QD, RD: req.RD,

		ACL:       defaultString(req.ACL, model.ACLOpen),
		Whitelist: whitelist,
		// 默认允许一个项目下多个迭代：迭代是排期的主要单位，
		// 一个项目只跑一轮迭代反而少见。
		Multiple: req.Multiple == nil || *req.Multiple,

		OpenedBy:   actor,
		OpenedDate: &now,
		Order:      req.Order,
	}

	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := s.projects.Create(ctx, tx, project); err != nil {
			return err
		}
		if err := s.proProducts.Replace(ctx, tx, project.ID, req.ProductIDs); err != nil {
			return err
		}
		if err := s.team.addOwnersAndMembers(ctx, tx, model.TeamRootProject, project.ID, req.Team,
			[]model.Team{
				{UserID: req.PO, Role: model.TeamRolePO},
				{UserID: req.PM, Role: model.TeamRolePM},
				{UserID: req.QD, Role: model.TeamRoleQD},
				{UserID: req.RD, Role: model.TeamRoleRD},
				// 建项目的人进团队。私有项目的可见性依赖团队成员，
				// 少了这一条，建完项目的人可能自己就打不开了。
				{UserID: actor},
			}); err != nil {
			return err
		}
		return s.record(ctx, tx, history.Entry{
			ObjectType: model.ObjectTypeProject,
			ObjectID:   project.ID,
			ProjectID:  project.ID,
			ActorID:    actor,
			Action:     model.ActionOpened,
			Comment:    "",
			Changes: []history.Change{
				{Field: "Name", Old: "", New: name},
			},
		})
	})
	if err != nil {
		return nil, err
	}
	return s.Get(ctx, project.ID)
}

// Update 编辑项目。
//
// 状态**不**在这里改：项目的开始/挂起/关闭/激活各有独立动作与权限点，
// 混进编辑里会让「谁能改状态」变得模糊。
func (s *ProjectService) Update(ctx context.Context, id uint, req dto.ProjectReq) (*dto.ProjectResp, error) {
	project, err := s.projects.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := s.gate.AssertProjectVisible(ctx, id); err != nil {
		return nil, err
	}
	if _, statusErr := workflow.ProjectMachine.Resolve("edit", project.Status); statusErr != nil {
		return nil, workflow.ProjectMachine.DescribeError(statusErr)
	}

	name := strings.TrimSpace(req.Name)
	if name == "" {
		return nil, apperr.Newf(apperr.InvalidParam, "项目名称不能为空")
	}
	code := strings.ToUpper(strings.TrimSpace(req.Code))
	if taken, err := s.projects.NameTaken(ctx, name, id); err != nil {
		return nil, err
	} else if taken {
		return nil, apperr.New(apperr.ProjectNameTaken)
	}
	if taken, err := s.projects.CodeTaken(ctx, code, id); err != nil {
		return nil, err
	} else if taken {
		return nil, apperr.New(apperr.ProjectCodeTaken)
	}

	begin, end, err := parseDateRange(req.Begin, req.End)
	if err != nil {
		return nil, translateDateErr(err, apperr.ProjectDateInvalid)
	}
	if err := s.assertProducts(ctx, req.ProductIDs); err != nil {
		return nil, err
	}
	if err := s.team.assertMembers(ctx, req.Team); err != nil {
		return nil, err
	}
	whitelist, err := encodeIDs(req.Whitelist)
	if err != nil {
		return nil, err
	}

	actor := currentUserID(ctx)
	now := time.Now().UTC()

	fields := map[string]any{
		"name": name, "code": code,
		"model":            defaultString(req.Model, project.Model),
		"begin":            begin,
		"end":              end,
		"days":             calendarDays(begin, end),
		"desc":             req.Desc,
		"po":               req.PO,
		"pm":               req.PM,
		"qd":               req.QD,
		"rd":               req.RD,
		"acl":              defaultString(req.ACL, model.ACLOpen),
		"whitelist":        whitelist,
		"multiple":         project.Multiple,
		"order":            req.Order,
		"last_edited_by":   actor,
		"last_edited_date": now,
	}
	if req.Multiple != nil {
		fields["multiple"] = *req.Multiple
	}

	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := s.projects.UpdateFields(ctx, tx, id, fields); err != nil {
			return err
		}
		if err := s.proProducts.Replace(ctx, tx, id, req.ProductIDs); err != nil {
			return err
		}
		if err := s.team.addOwnersAndMembers(ctx, tx, model.TeamRootProject, id, req.Team, nil); err != nil {
			return err
		}
		changes := projectDiff(project, fields)
		if len(changes) == 0 {
			return nil
		}
		return s.record(ctx, tx, history.Entry{
			ObjectType: model.ObjectTypeProject,
			ObjectID:   id,
			ProjectID:  id,
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

// Delete 删除项目。
//
// 只有在「底下什么都没有」时才允许：迭代、任务、已排入的需求都是
// 真实的工作记录，删掉项目就地把它们一起抹掉，代价太大。
func (s *ProjectService) Delete(ctx context.Context, id uint) error {
	if _, err := s.projects.GetByID(ctx, id); err != nil {
		return err
	}
	if err := s.gate.AssertProjectVisible(ctx, id); err != nil {
		return err
	}

	execCount, err := s.executions.Count(ctx, "project_id = ?", id)
	if err != nil {
		return err
	}
	if execCount > 0 {
		return apperr.Newf(apperr.Conflict, "项目下还有 %d 个迭代，请先删除迭代", execCount)
	}
	taskCount, err := s.tasks.Count(ctx, "project_id = ?", id)
	if err != nil {
		return err
	}
	if taskCount > 0 {
		return apperr.Newf(apperr.Conflict, "项目下还有 %d 个任务，请先删除任务", taskCount)
	}
	storyCount, err := s.links.CountByProject(ctx, id)
	if err != nil {
		return err
	}
	if storyCount > 0 {
		return apperr.Newf(apperr.Conflict, "项目下还排有 %d 条需求，请先移出", storyCount)
	}

	actor := currentUserID(ctx)
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// 项目下的缺陷退回产品级：产品还在，缺陷仍是该产品的一个质量问题，
		// 只是不再属于任何项目。不这样做的话，缺陷会指向一个已删的项目，
		// 而列表按项目过滤、详情按项目校验可见性 —— 它会从两边都消失。
		if res := tx.WithContext(ctx).Model(&model.Bug{}).
			Where("project_id = ?", id).
			Updates(map[string]any{"project_id": 0, "execution_id": 0}); res.Error != nil {
			return apperr.Wrap(apperr.Internal, res.Error)
		}
		if err := s.proProducts.Replace(ctx, tx, id, nil); err != nil {
			return err
		}
		if err := s.team.teams.DeleteByRoot(ctx, tx, model.TeamRootProject, id); err != nil {
			return err
		}
		if err := s.record(ctx, tx, history.Entry{
			ObjectType: model.ObjectTypeProject,
			ObjectID:   id,
			ProjectID:  id,
			ActorID:    actor,
			Action:     model.ActionDeleted,
		}); err != nil {
			return err
		}
		return s.projects.Delete(ctx, tx, id)
	})
}

// ---------------------------------------------------------------------------
// 需求排期
// ---------------------------------------------------------------------------

// ListStories 返回项目（或某个迭代）下已排入的需求。
//
// 不传 executionID 时返回项目下全部（含未分配到迭代的），
// 这是项目详情「需求」页要的视图。
func (s *ProjectService) ListStories(ctx context.Context, projectID, executionID uint) ([]dto.StoryLinkResp, error) {
	if _, err := s.projects.GetByID(ctx, projectID); err != nil {
		return nil, err
	}
	if err := s.gate.AssertProjectVisible(ctx, projectID); err != nil {
		return nil, err
	}

	var (
		rows []model.ProjectStory
		err  error
	)
	if executionID > 0 {
		if _, err := s.assertExecutionInProject(ctx, projectID, executionID); err != nil {
			return nil, err
		}
		rows, err = s.links.ListByExecution(ctx, executionID)
	} else {
		rows, err = s.links.ListByProject(ctx, projectID)
	}
	if err != nil {
		return nil, err
	}
	return s.linkResps(ctx, rows)
}

// linkResps 把关联行补全成出参（需求标题、迭代名、是否过期）。
func (s *ProjectService) linkResps(ctx context.Context, rows []model.ProjectStory) ([]dto.StoryLinkResp, error) {
	out := make([]dto.StoryLinkResp, 0, len(rows))
	if len(rows) == 0 {
		return out, nil
	}

	storyIDs := make([]uint, 0, len(rows))
	projectIDs := make([]uint, 0, len(rows))
	executionIDs := make([]uint, 0, len(rows))
	for _, r := range rows {
		storyIDs = append(storyIDs, r.StoryID)
		projectIDs = append(projectIDs, r.ProjectID)
		executionIDs = append(executionIDs, r.ExecutionID)
	}
	stories, err := s.stories.ListByIDs(ctx, storyIDs)
	if err != nil {
		return nil, err
	}
	byID := make(map[uint]model.Story, len(stories))
	for _, st := range stories {
		byID[st.ID] = st
	}
	projectNames, err := s.projects.NameMap(ctx, projectIDs)
	if err != nil {
		return nil, err
	}
	executionNames, err := s.executions.NameMap(ctx, executionIDs)
	if err != nil {
		return nil, err
	}

	for _, r := range rows {
		st := byID[r.StoryID]
		out = append(out, dto.StoryLinkResp{
			ID: r.ID, ProjectID: r.ProjectID, ProjectName: projectNames[r.ProjectID],
			ExecutionID: r.ExecutionID, ExecName: executionNames[r.ExecutionID],
			// 需求可能已被删除（软删）而关联还在：那时标题为空，
			// 前端显示成「已删除的需求」，总比整行消失让人困惑好。
			StoryID: r.StoryID, StoryTitle: st.Title,
			Version:  r.Version,
			Outdated: st.ID > 0 && r.Version != st.Version,
			Order:    r.Order,
		})
	}
	return out, nil
}

// LinkStory 把需求排入项目（可指定迭代）。
//
// 校验三件事，缺一不可：
//
//  1. 需求处于可排期的状态（草稿/评审中/已关闭都不行）；
//  2. 需求所属产品关联到了该项目 —— 项目只做它负责的产品；
//  3. 指定的迭代确实属于该项目。
func (s *ProjectService) LinkStory(ctx context.Context, projectID uint, req dto.StoryLinkReq) (*dto.StoryLinkResp, error) {
	project, err := s.projects.GetByID(ctx, projectID)
	if err != nil {
		return nil, err
	}
	if err := s.gate.AssertProjectVisible(ctx, projectID); err != nil {
		return nil, err
	}
	if project.Status == model.ProjectStatusClosed {
		return nil, apperr.Newf(apperr.ProjectStatusInvalid, "项目已关闭，不能再排入需求")
	}

	story, err := s.stories.FindByID(ctx, req.StoryID)
	if err != nil {
		return nil, err
	}
	if story == nil {
		return nil, apperr.New(apperr.StoryNotFound)
	}
	if err := s.gate.AssertProductVisible(ctx, story.ProductID); err != nil {
		return nil, err
	}
	if !slices.Contains(model.StoryPlannableStatuses, story.Status) {
		return nil, apperr.Newf(apperr.StoryStatusInvalid,
			"只有已评审通过的需求才能排入项目（当前状态：%s）",
			workflow.StoryMachine.StatusLabel(story.Status))
	}

	productIDs, err := s.proProducts.ProductIDs(ctx, projectID)
	if err != nil {
		return nil, err
	}
	if !slices.Contains(productIDs, story.ProductID) {
		return nil, apperr.Newf(apperr.StoryProductNotInProject,
			"该需求所属产品未关联到此项目，请先在项目里关联产品")
	}
	if _, err := s.assertExecutionInProject(ctx, projectID, req.ExecutionID); err != nil {
		return nil, err
	}

	// 需求被排入项目意味着它进入了实施阶段，阶段随之推进到「已排期」。
	// 已经是更靠后的阶段（设计中、开发中…）时不动它 —— 排期不该把
	// 一条正在开发的需求拽回起点。
	stageFields := map[string]any{}
	if story.Stage == model.StoryStageWait || story.Stage == model.StoryStagePlanned {
		stageFields["stage"] = model.StoryStageProjected
	}

	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := s.links.Link(ctx, tx, model.ProjectStory{
			ProjectID: projectID, ExecutionID: req.ExecutionID,
			ProductID: story.ProductID, StoryID: story.ID,
			Version: story.Version, Order: req.Order,
		}); err != nil {
			return err
		}
		if len(stageFields) > 0 {
			if err := s.stories.UpdateFields(ctx, tx, story.ID, stageFields); err != nil {
				return err
			}
		}
		changes := []history.Change{{
			Field: "project", Old: "", New: project.Name,
		}}
		if req.ExecutionID > 0 {
			execution, err := s.executions.FindByID(ctx, req.ExecutionID)
			if err == nil && execution != nil {
				changes = append(changes, history.Change{
					Field: "execution", Old: "", New: execution.Name,
				})
			}
		}
		return s.recordStoryLink(ctx, tx, story, projectID, req.ExecutionID,
			model.ActionLinked, changes)
	})
	if err != nil {
		return nil, err
	}

	rows, err := s.links.ListByStories(ctx, []uint{story.ID})
	if err != nil {
		return nil, err
	}
	resps, err := s.linkResps(ctx, rows)
	if err != nil {
		return nil, err
	}
	for i := range resps {
		if resps[i].ProjectID == projectID && resps[i].ExecutionID == req.ExecutionID {
			return &resps[i], nil
		}
	}
	if len(resps) > 0 {
		return &resps[0], nil
	}
	return nil, nil
}

// UnlinkStory 把需求从项目（或迭代）里撤出。
//
// 项目下还有任务关联这条需求时不允许撤：那些任务会指向一个
// 「不在本项目里」的需求，看板上就成了孤儿。
func (s *ProjectService) UnlinkStory(ctx context.Context, projectID, storyID uint) error {
	story, err := s.stories.FindByID(ctx, storyID)
	if err != nil {
		return err
	}
	if story == nil {
		return apperr.New(apperr.StoryNotFound)
	}
	if err := s.gate.AssertProjectVisible(ctx, projectID); err != nil {
		return err
	}

	link, err := s.links.Find(ctx, projectID, storyID)
	if err != nil {
		return err
	}
	if link == nil {
		return apperr.Newf(apperr.NotFound, "该需求未排入此项目")
	}

	count, err := s.tasks.Count(ctx, "story_id = ? AND project_id = ?", storyID, projectID)
	if err != nil {
		return err
	}
	if count > 0 {
		return apperr.Newf(apperr.StoryHasTasks,
			"该项目下还有 %d 个任务关联此需求，请先处理它们再撤出", count)
	}

	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := s.links.Unlink(ctx, tx, projectID, storyID); err != nil {
			return err
		}
		return s.recordStoryLink(ctx, tx, story, projectID, link.ExecutionID,
			model.ActionUnlinked, nil)
	})
}

// recordStoryLink 写一条「需求排入/撤出项目」的操作历史。
//
// 历史记在**需求**上而不是项目上：从需求的时间线看，这才是一次
// 与它有关的变动；项目的时间线里塞满几十条排期记录只会淹没真正的进展。
func (s *ProjectService) recordStoryLink(
	ctx context.Context, tx *gorm.DB, story *model.Story,
	projectID, executionID uint, action string, changes []history.Change,
) error {
	_, err := s.recorder.Record(ctx, tx, history.Entry{
		ObjectType:  model.ObjectTypeStory,
		ObjectID:    story.ID,
		ProductID:   story.ProductID,
		ProjectID:   projectID,
		ExecutionID: executionID,
		ActorID:     currentUserID(ctx),
		Action:      action,
		Changes:     changes,
	})
	return err
}

// assertExecutionInProject 校验迭代属于该项目；executionID 为 0 时直接通过。
func (s *ProjectService) assertExecutionInProject(ctx context.Context, projectID, executionID uint) (*model.Execution, error) {
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
	if execution.Status == model.ExecutionStatusClosed {
		return nil, apperr.Newf(apperr.ExecutionStatusInvalid, "迭代已关闭，不能再排入需求")
	}
	return execution, nil
}

// ---------------------------------------------------------------------------
// 状态流转
// ---------------------------------------------------------------------------

// Apply 执行一个项目状态动作（start / suspend / close / activate）。
func (s *ProjectService) Apply(ctx context.Context, id uint, action, comment string) (*dto.ProjectResp, error) {
	project, err := s.projects.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := s.gate.AssertProjectVisible(ctx, id); err != nil {
		return nil, err
	}

	trans, statusErr := workflow.ProjectMachine.Resolve(action, project.Status)
	if statusErr != nil {
		return nil, workflow.ProjectMachine.DescribeError(statusErr)
	}

	actor := currentUserID(ctx)
	now := time.Now().UTC()

	fields := map[string]any{"status": trans.To}
	switch action {
	case "start":
		// 首次开始时记下实际开始时间；从挂起恢复时不覆盖。
		if project.RealBegan == nil {
			fields["real_begin"] = now
		}
	case "suspend":
		fields["suspended_date"] = now
	case "close":
		fields["closed_by"] = actor
		fields["closed_date"] = now
		fields["real_end"] = now
	case "activate":
		fields["closed_by"] = 0
		fields["closed_date"] = nil
		fields["closed_reason"] = ""
		fields["suspended_date"] = nil
		if project.RealBegan == nil {
			fields["real_begin"] = now
		}
	default:
		return nil, apperr.Newf(apperr.Internal, "动作 %s 尚未实现", action)
	}

	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := s.projects.UpdateFields(ctx, tx, id, fields); err != nil {
			return err
		}
		return s.recordStatus(ctx, tx, model.ObjectTypeProject, id, id, 0, actor, action,
			comment, project.Status, trans.To)
	})
	if err != nil {
		return nil, err
	}
	return s.Get(ctx, id)
}

// RecomputeHours 按任务重算迭代与项目的工时与进度。
//
// 由任务侧在每次工时变化后调用（记工时、完成任务、删除任务）。
// 汇总分两级：迭代的工时是迭代下任务的合计，项目的工时是项目下任务的合计。
// 跨迭代的任务（execution_id=0）也算进项目，所以项目这一级不能靠
// 「各迭代相加」得到，必须独立聚合。
func (s *ProjectService) RecomputeHours(ctx context.Context, tx *gorm.DB, projectID, executionID uint) error {
	now := time.Now().UTC()

	if executionID > 0 {
		execution, err := s.executions.FindByID(ctx, executionID)
		if err != nil {
			return err
		}
		if execution != nil {
			h, err := s.executions.SumTaskHours(ctx, tx, executionID)
			if err != nil {
				return err
			}
			if err := s.executions.UpdateFields(ctx, tx, executionID, map[string]any{
				"estimate": h.Estimate, "consumed": h.Consumed, "left": h.Left,
				"progress":         hoursProgress(h.Consumed, h.Left),
				"last_edited_date": now,
			}); err != nil {
				return err
			}
		}
	}

	if projectID == 0 {
		return nil
	}
	project, err := s.projects.FindByID(ctx, projectID)
	if err != nil {
		return err
	}
	if project == nil {
		return nil
	}
	h, err := s.projects.SumTaskHours(ctx, tx, projectID)
	if err != nil {
		return err
	}
	return s.projects.UpdateFields(ctx, tx, projectID, map[string]any{
		"estimate": h.Estimate, "consumed": h.Consumed, "left": h.Left,
		"progress":         hoursProgress(h.Consumed, h.Left),
		"last_edited_date": now,
	})
}

// MemberIDs 返回项目团队成员，供任务校验负责人用。
func (s *ProjectService) MemberIDs(ctx context.Context, projectID uint) ([]uint, error) {
	return s.team.teams.MemberIDs(ctx, model.TeamRootProject, projectID)
}

// NameMap 返回 id → 项目名的映射。
//
// 任务、需求等相邻领域要展示项目名，但它们不该自己去碰项目仓储，
// 这里给一个只读入口。
func (s *ProjectService) NameMap(ctx context.Context, ids []uint) (map[uint]string, error) {
	return s.projects.NameMap(ctx, ids)
}

// IsProductLinked 报告某产品是否关联到了某项目。
func (s *ProjectService) IsProductLinked(ctx context.Context, projectID, productID uint) (bool, error) {
	if productID == 0 {
		return true, nil
	}
	ids, err := s.proProducts.ProductIDs(ctx, projectID)
	if err != nil {
		return false, err
	}
	return slices.Contains(ids, productID), nil
}

// ---------------------------------------------------------------------------
// 团队
// ---------------------------------------------------------------------------

// ListTeam 返回项目团队。
func (s *ProjectService) ListTeam(ctx context.Context, id uint) ([]dto.TeamMemberResp, error) {
	return s.team.list(ctx, model.TeamRootProject, id)
}

// AddTeam 往项目团队里加成员。
func (s *ProjectService) AddTeam(ctx context.Context, id uint, req dto.TeamMemberReq) error {
	return s.team.add(ctx, model.TeamRootProject, id, req)
}

// UpdateTeam 更新某成员在项目团队里的信息。
func (s *ProjectService) UpdateTeam(ctx context.Context, id, userID uint, req dto.TeamMemberReq) error {
	return s.team.update(ctx, model.TeamRootProject, id, userID, req)
}

// RemoveTeam 把成员移出项目团队。
//
// 有未完成任务的人不能直接移出 —— 否则那些任务会挂在一个
// 不在团队里的人身上，既不合法也没人做。
func (s *ProjectService) RemoveTeam(ctx context.Context, id, userID uint) error {
	return s.team.remove(ctx, model.TeamRootProject, id, userID)
}

// ---------------------------------------------------------------------------
// 内部辅助
// ---------------------------------------------------------------------------

// assertProducts 校验产品存在且当前用户可见。
func (s *ProjectService) assertProducts(ctx context.Context, productIDs []uint) error {
	if len(productIDs) == 0 {
		return apperr.New(apperr.ProjectNoProduct)
	}
	for _, pid := range dedupIDs(productIDs) {
		if err := s.gate.AssertProductVisible(ctx, pid); err != nil {
			return err
		}
	}
	return nil
}

// recordStatus 写一条状态变更历史。
func (s *ProjectService) recordStatus(
	ctx context.Context, tx *gorm.DB,
	objectType string, objectID, projectID uint, executionID uint,
	actor uint, action, comment, from, to string,
) error {
	return s.record(ctx, tx, history.Entry{
		ObjectType:  objectType,
		ObjectID:    objectID,
		ProjectID:   projectID,
		ExecutionID: executionID,
		ActorID:     actor,
		Action:      action,
		Comment:     comment,
		Extra:       workflow.StatusChangeExtra(from, to),
		Changes:     []history.Change{{Field: "Status", Old: from, New: to}},
	})
}

// record 写入一条操作历史，并补齐变更里的用户姓名。
func (s *ProjectService) record(ctx context.Context, tx *gorm.DB, e history.Entry) error {
	_, err := s.recorder.Record(ctx, tx, e)
	return err
}

// ---------------------------------------------------------------------------
// 迭代
// ---------------------------------------------------------------------------

// ExecutionService 负责迭代。
//
// 迭代与项目共用状态机形状（wait/doing/suspended/closed），但权限点、
// 日期校验与团队口径都不同，所以是独立的服务而不是项目服务的几个方法。
type ExecutionService struct {
	db         *gorm.DB
	executions *repository.ExecutionRepo
	projects   *repository.ProjectRepo
	tasks      *repository.TaskRepo
	links      *repository.ProjectStoryRepo
	users      *repository.UserRepo
	gate       *Gate
	team       *teamOps

	recorder *history.Recorder
}

// NewExecutionService 构造迭代服务。
func NewExecutionService(
	db *gorm.DB,
	executions *repository.ExecutionRepo,
	projects *repository.ProjectRepo,
	tasks *repository.TaskRepo,
	links *repository.ProjectStoryRepo,
	users *repository.UserRepo,
	teams *repository.TeamRepo,
	gate *Gate,
) *ExecutionService {
	return &ExecutionService{
		db: db, executions: executions, projects: projects, tasks: tasks,
		links: links, users: users, gate: gate,
		team: &teamOps{
			teams: teams, users: users, projects: projects,
			executions: executions, tasks: tasks, gate: gate,
		},
		recorder: history.New(ProjectLabeler{}),
	}
}

// List 分页查询迭代。
func (s *ExecutionService) List(ctx context.Context, req dto.ExecutionFilterReq) (*pagination.Result[dto.ExecutionResp], error) {
	visible, err := s.gate.VisibleProjects(ctx)
	if err != nil {
		return nil, err
	}
	if req.ProjectID > 0 {
		if err := s.gate.AssertProjectVisible(ctx, req.ProjectID); err != nil {
			return nil, err
		}
	}

	result, err := s.executions.List(ctx, repository.ExecutionFilter{
		ProjectID:         req.ProjectID,
		Type:              req.Type,
		Status:            req.Status,
		Keywords:          req.Keywords,
		VisibleProjectIDs: visible,
		Pagination:        pagination.Params{Page: req.Page, Size: req.Size, Order: req.Order},
	})
	if err != nil {
		return nil, err
	}
	return s.decorateList(ctx, result)
}

// Get 取迭代详情。
func (s *ExecutionService) Get(ctx context.Context, id uint) (*dto.ExecutionResp, error) {
	execution, err := s.executions.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := s.gate.AssertExecutionVisible(ctx, id); err != nil {
		return nil, err
	}

	decorated, err := s.decorateList(ctx, &pagination.Result[model.Execution]{
		List: []model.Execution{*execution}, Total: 1, Page: 1, Size: 1,
	})
	if err != nil {
		return nil, err
	}
	out := decorated.List[0]
	out.Actions = availableStateActions(ctx, workflow.ExecutionMachine, execution.Status)

	team, err := s.team.list(ctx, model.TeamRootExecution, id)
	if err != nil {
		return nil, err
	}
	out.Team = team

	if out.StoryCount, err = s.links.CountByExecution(ctx, id); err != nil {
		return nil, err
	}
	if out.TaskCount, err = s.tasks.Count(ctx, "execution_id = ?", id); err != nil {
		return nil, err
	}
	// 汇总迭代下任务的工时。
	//
	// 两个可见性条件都必须显式写出来：TaskFilter 的口径是「空切片 = 一个
	// 都看不到」，而不是「不过滤」。这里能走到这一步说明迭代本身已经过
	// 可见性校验，所以把它的项目放进去即可。
	//
	// 产品那一层则要**放行**（AllProducts）：这是迭代自身的工时汇总，
	// 答的是「这个迭代一共投了多少工时」。若按查看者能看到的任务去算，
	// 同一个迭代在不同人眼里会给出不同的数字，项目与迭代对不上账。
	// 迭代的可见性已经由上面的校验挡住了越权者。
	if h, err := s.tasks.SumHours(ctx, nil, repository.TaskFilter{
		ExecutionID:       id,
		VisibleProjectIDs: []uint{execution.ProjectID},
		AllProducts:       true,
	}); err != nil {
		return nil, err
	} else {
		out.TaskHours = dto.HoursResp{Estimate: h.Estimate, Consumed: h.Consumed, Left: h.Left}
	}
	return &out, nil
}

// decorateList 批量补全迭代列表的展示字段。
func (s *ExecutionService) decorateList(ctx context.Context, result *pagination.Result[model.Execution]) (*pagination.Result[dto.ExecutionResp], error) {
	executions := result.List
	if len(executions) == 0 {
		return &pagination.Result[dto.ExecutionResp]{
			List: []dto.ExecutionResp{}, Total: result.Total, Page: result.Page, Size: result.Size,
		}, nil
	}

	projectIDs := make([]uint, 0, len(executions))
	executionIDs := make([]uint, 0, len(executions))
	for i := range executions {
		projectIDs = append(projectIDs, executions[i].ProjectID)
		executionIDs = append(executionIDs, executions[i].ID)
	}

	projectNames, err := s.projects.NameMap(ctx, projectIDs)
	if err != nil {
		return nil, err
	}
	teamRows, err := s.team.memberRows(ctx, model.TeamRootExecution, executionIDs)
	if err != nil {
		return nil, err
	}
	// 迭代团队为空表示「沿用项目团队」，人数也得按项目团队算 ——
	// 否则界面上写着「0 人」，而任务明明派得出去。
	projectTeams, err := s.team.memberRows(ctx, model.TeamRootProject, projectIDs)
	if err != nil {
		return nil, err
	}

	out := make([]dto.ExecutionResp, 0, len(executions))
	for i := range executions {
		e := &executions[i]
		item := executionToResp(e)
		item.ProjectName = projectNames[e.ProjectID]
		item.MemberCount = len(teamRows[e.ID])
		if item.MemberCount == 0 {
			item.MemberCount = len(projectTeams[e.ProjectID])
		}
		out = append(out, item)
	}
	return &pagination.Result[dto.ExecutionResp]{
		List: out, Total: result.Total, Page: result.Page, Size: result.Size,
	}, nil
}

// Create 新建迭代。
func (s *ExecutionService) Create(ctx context.Context, req dto.ExecutionReq) (*dto.ExecutionResp, error) {
	project, err := s.projects.GetByID(ctx, req.ProjectID)
	if err != nil {
		return nil, err
	}
	if err := s.gate.AssertProjectVisible(ctx, req.ProjectID); err != nil {
		return nil, err
	}
	if project.Status == model.ProjectStatusClosed {
		return nil, apperr.Newf(apperr.ExecutionStatusInvalid, "项目已关闭，不能新建迭代")
	}

	name := strings.TrimSpace(req.Name)
	if name == "" {
		return nil, apperr.Newf(apperr.InvalidParam, "迭代名称不能为空")
	}
	if taken, err := s.executions.NameTakenInProject(ctx, req.ProjectID, name, 0); err != nil {
		return nil, err
	} else if taken {
		return nil, apperr.Newf(apperr.InvalidParam, "该项目下已存在同名迭代")
	}

	begin, end, err := parseDateRange(req.Begin, req.End)
	if err != nil {
		return nil, translateDateErr(err, apperr.ExecutionDateInvalid)
	}
	if err := assertWithinProject(project, begin, end); err != nil {
		return nil, err
	}
	if err := s.team.assertMembers(ctx, req.Team); err != nil {
		return nil, err
	}
	whitelist, err := encodeIDs(req.Whitelist)
	if err != nil {
		return nil, err
	}

	actor := currentUserID(ctx)
	now := time.Now().UTC()

	execution := &model.Execution{
		ProjectID: req.ProjectID,
		Name:      name,
		Code:      strings.TrimSpace(req.Code),
		Type:      defaultString(req.Type, model.ExecutionTypeSprint),
		Status:    model.ExecutionStatusWait,

		Begin: begin, End: end, Days: calendarDays(begin, end),

		Desc: req.Desc,
		PO:   req.PO, PM: req.PM, QD: req.QD, RD: req.RD,

		ACL:       defaultString(req.ACL, model.ACLOpen),
		Whitelist: whitelist,

		OpenedBy:   actor,
		OpenedDate: &now,
		Order:      req.Order,
	}

	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := s.executions.Create(ctx, tx, execution); err != nil {
			return err
		}
		// 迭代的树字段：暂时是扁平的（都直接挂在项目下），
		// 但 Path 保持「含自身 id」的口径，将来支持子阶段时不必回填。
		if err := s.executions.UpdateFields(ctx, tx, execution.ID, map[string]any{
			"path":  itoa(execution.ID),
			"grade": 1,
		}); err != nil {
			return err
		}
		// 迭代团队**只**写请求里明列的人，负责人与创建者都不自动加入。
		//
		// 与项目团队的做法相反，原因在于两者的语义不同：项目团队是
		// 「谁可以参与这个项目」，负责人天然在列；而迭代团队是
		// 「这个迭代从项目团队里抽调了谁」的**收窄**表达，留空即沿用
		// 项目团队（见 teamOps 的说明）。一旦自动塞人，每个迭代的团队
		// 就都非空了，于是「留空 = 沿用项目团队」形同虚设 ——
		// 更糟的是，刚刚建好的迭代会立刻只剩创建者本人能被指派任务。
		if err := s.team.addOwnersAndMembers(ctx, tx, model.TeamRootExecution, execution.ID,
			req.Team, nil); err != nil {
			return err
		}
		return s.record(ctx, tx, history.Entry{
			ObjectType:  model.ObjectTypeExecution,
			ObjectID:    execution.ID,
			ProjectID:   req.ProjectID,
			ExecutionID: execution.ID,
			ActorID:     actor,
			Action:      model.ActionOpened,
			Changes:     []history.Change{{Field: "Name", Old: "", New: name}},
		})
	})
	if err != nil {
		return nil, err
	}
	return s.Get(ctx, execution.ID)
}

// Update 编辑迭代。
func (s *ExecutionService) Update(ctx context.Context, id uint, req dto.ExecutionReq) (*dto.ExecutionResp, error) {
	execution, err := s.executions.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := s.gate.AssertExecutionVisible(ctx, id); err != nil {
		return nil, err
	}
	if _, statusErr := workflow.ExecutionMachine.Resolve("edit", execution.Status); statusErr != nil {
		return nil, workflow.ExecutionMachine.DescribeError(statusErr)
	}

	name := strings.TrimSpace(req.Name)
	if name == "" {
		return nil, apperr.Newf(apperr.InvalidParam, "迭代名称不能为空")
	}
	if taken, err := s.executions.NameTakenInProject(ctx, execution.ProjectID, name, id); err != nil {
		return nil, err
	} else if taken {
		return nil, apperr.Newf(apperr.InvalidParam, "该项目下已存在同名迭代")
	}

	begin, end, err := parseDateRange(req.Begin, req.End)
	if err != nil {
		return nil, translateDateErr(err, apperr.ExecutionDateInvalid)
	}
	if project, err := s.projects.FindByID(ctx, execution.ProjectID); err != nil {
		return nil, err
	} else if project != nil {
		if err := assertWithinProject(project, begin, end); err != nil {
			return nil, err
		}
	}
	if err := s.team.assertMembers(ctx, req.Team); err != nil {
		return nil, err
	}
	whitelist, err := encodeIDs(req.Whitelist)
	if err != nil {
		return nil, err
	}

	actor := currentUserID(ctx)
	now := time.Now().UTC()

	fields := map[string]any{
		"name":  name,
		"code":  strings.TrimSpace(req.Code),
		"type":  defaultString(req.Type, execution.Type),
		"begin": begin, "end": end,
		"days":             calendarDays(begin, end),
		"desc":             req.Desc,
		"po":               req.PO,
		"pm":               req.PM,
		"qd":               req.QD,
		"rd":               req.RD,
		"acl":              defaultString(req.ACL, model.ACLOpen),
		"whitelist":        whitelist,
		"order":            req.Order,
		"last_edited_by":   actor,
		"last_edited_date": now,
	}

	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := s.executions.UpdateFields(ctx, tx, id, fields); err != nil {
			return err
		}
		if err := s.team.addOwnersAndMembers(ctx, tx, model.TeamRootExecution, id, req.Team, nil); err != nil {
			return err
		}
		changes := executionDiff(execution, fields)
		if len(changes) == 0 {
			return nil
		}
		return s.record(ctx, tx, history.Entry{
			ObjectType:  model.ObjectTypeExecution,
			ObjectID:    id,
			ProjectID:   execution.ProjectID,
			ExecutionID: id,
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

// Delete 删除迭代。
//
// 迭代下的需求关联会被一并解除（需求本身还在产品里），
// 但只要还有任务就不允许删 —— 任务是有工时记录的真实工作。
func (s *ExecutionService) Delete(ctx context.Context, id uint) error {
	execution, err := s.executions.GetByID(ctx, id)
	if err != nil {
		return err
	}
	if err := s.gate.AssertExecutionVisible(ctx, id); err != nil {
		return err
	}

	taskCount, err := s.tasks.Count(ctx, "execution_id = ?", id)
	if err != nil {
		return err
	}
	if taskCount > 0 {
		return apperr.Newf(apperr.Conflict, "迭代下还有 %d 个任务，请先移出或删除", taskCount)
	}

	actor := currentUserID(ctx)
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// 解除需求与本迭代的关联：需求还留在产品的需求池里，
		// 只是不再排进这个迭代。
		if res := tx.WithContext(ctx).Model(&model.ProjectStory{}).
			Where("execution_id = ?", id).
			Updates(map[string]any{"execution_id": 0}); res.Error != nil {
			return apperr.Wrap(apperr.Internal, res.Error)
		}
		// 缺陷同理，只是层级不同：缺陷是「产品的质量问题」，
		// 迭代只是「在哪一轮活里发现的」。迭代没了，缺陷退回产品级
		// （product_id 是必填的，它不会变成无主数据）。
		//
		// 这里刻意不改成「有缺陷就不许删迭代」：那会让一条一年前的
		// 历史缺陷永远卡住整个迭代的清理，而缺陷本身并没有变脏。
		if res := tx.WithContext(ctx).Model(&model.Bug{}).
			Where("execution_id = ?", id).
			Updates(map[string]any{"execution_id": 0}); res.Error != nil {
			return apperr.Wrap(apperr.Internal, res.Error)
		}
		if err := s.team.teams.DeleteByRoot(ctx, tx, model.TeamRootExecution, id); err != nil {
			return err
		}
		if err := s.record(ctx, tx, history.Entry{
			ObjectType:  model.ObjectTypeExecution,
			ObjectID:    id,
			ProjectID:   execution.ProjectID,
			ExecutionID: id,
			ActorID:     actor,
			Action:      model.ActionDeleted,
		}); err != nil {
			return err
		}
		return s.executions.Delete(ctx, tx, id)
	})
}

// Apply 执行一个迭代状态动作。
func (s *ExecutionService) Apply(ctx context.Context, id uint, action, comment string) (*dto.ExecutionResp, error) {
	execution, err := s.executions.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := s.gate.AssertExecutionVisible(ctx, id); err != nil {
		return nil, err
	}

	trans, statusErr := workflow.ExecutionMachine.Resolve(action, execution.Status)
	if statusErr != nil {
		return nil, workflow.ExecutionMachine.DescribeError(statusErr)
	}

	actor := currentUserID(ctx)
	now := time.Now().UTC()

	fields := map[string]any{"status": trans.To}
	switch action {
	case "start":
		if execution.RealBegan == nil {
			fields["real_begin"] = now
		}
	case "suspend":
		fields["suspended_date"] = now
	case "close":
		fields["closed_by"] = actor
		fields["closed_date"] = now
		fields["real_end"] = now
	case "activate":
		fields["closed_by"] = 0
		fields["closed_date"] = nil
		fields["closed_reason"] = ""
		fields["suspended_date"] = nil
		if execution.RealBegan == nil {
			fields["real_begin"] = now
		}
	default:
		return nil, apperr.Newf(apperr.Internal, "动作 %s 尚未实现", action)
	}

	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := s.executions.UpdateFields(ctx, tx, id, fields); err != nil {
			return err
		}
		_, err := s.recorder.Record(ctx, tx, history.Entry{
			ObjectType:  model.ObjectTypeExecution,
			ObjectID:    id,
			ProjectID:   execution.ProjectID,
			ExecutionID: id,
			ActorID:     actor,
			Action:      action,
			Comment:     comment,
			Extra:       workflow.StatusChangeExtra(execution.Status, trans.To),
			Changes:     []history.Change{{Field: "Status", Old: execution.Status, New: trans.To}},
		})
		return err
	})
	if err != nil {
		return nil, err
	}
	return s.Get(ctx, id)
}

// ListTeam 返回迭代团队。
func (s *ExecutionService) ListTeam(ctx context.Context, id uint) ([]dto.TeamMemberResp, error) {
	return s.team.list(ctx, model.TeamRootExecution, id)
}

// AddTeam 往迭代团队里加成员。
func (s *ExecutionService) AddTeam(ctx context.Context, id uint, req dto.TeamMemberReq) error {
	return s.team.add(ctx, model.TeamRootExecution, id, req)
}

// UpdateTeam 更新成员在迭代团队里的信息。
func (s *ExecutionService) UpdateTeam(ctx context.Context, id, userID uint, req dto.TeamMemberReq) error {
	return s.team.update(ctx, model.TeamRootExecution, id, userID, req)
}

// RemoveTeam 把成员移出迭代团队。
func (s *ExecutionService) RemoveTeam(ctx context.Context, id, userID uint) error {
	return s.team.remove(ctx, model.TeamRootExecution, id, userID)
}

func (s *ExecutionService) record(ctx context.Context, tx *gorm.DB, e history.Entry) error {
	_, err := s.recorder.Record(ctx, tx, e)
	return err
}

// ---------------------------------------------------------------------------
// 纯函数辅助
// ---------------------------------------------------------------------------

// projectToResp 把项目模型转为出参。
func projectToResp(p *model.Project) dto.ProjectResp {
	return dto.ProjectResp{
		ID: p.ID, Code: p.Code, Number: "PROJECT-" + itoa(p.ID),
		Name: p.Name, Model: p.Model, ModelLabel: projectModelLabels[p.Model],
		Status: p.Status, StatusLabel: workflow.ProjectMachine.StatusLabel(p.Status),
		Desc: p.Desc,

		Begin: formatDate(p.Begin), End: formatDate(p.End),
		RealBegan: formatTimeValue(p.RealBegan), RealEnd: formatTimeValue(p.RealEnd),
		Days: p.Days, Progress: p.Progress,
		Estimate: p.Estimate, Consumed: p.Consumed, Left: p.Left,

		PO: p.PO, PM: p.PM, QD: p.QD, RD: p.RD,
		ACL: p.ACL, Whitelist: mustDecodeIDs(p.Whitelist), Multiple: p.Multiple,

		OpenedBy: p.OpenedBy, OpenedDate: formatTimeValue(p.OpenedDate),
		ClosedBy: p.ClosedBy, ClosedDate: formatTimeValue(p.ClosedDate),
		ClosedReason:  p.ClosedReason,
		SuspendedDate: formatTimeValue(p.SuspendedDate),
		Order:         p.Order,
	}
}

// executionToResp 把迭代模型转为出参。
func executionToResp(e *model.Execution) dto.ExecutionResp {
	return dto.ExecutionResp{
		ID: e.ID, Code: e.Code, Number: "EXEC-" + itoa(e.ID),
		ProjectID: e.ProjectID,
		Name:      e.Name, Type: e.Type, TypeLabel: executionTypeLabels[e.Type],
		Status: e.Status, StatusLabel: workflow.ExecutionMachine.StatusLabel(e.Status),
		Desc: e.Desc,

		Begin: formatDate(e.Begin), End: formatDate(e.End),
		RealBegan: formatTimeValue(e.RealBegan), RealEnd: formatTimeValue(e.RealEnd),
		Days: e.Days, Progress: e.Progress,
		Estimate: e.Estimate, Consumed: e.Consumed, Left: e.Left,

		PO: e.PO, PM: e.PM, QD: e.QD, RD: e.RD,
		ACL: e.ACL, Whitelist: mustDecodeIDs(e.Whitelist),

		OpenedBy: e.OpenedBy, OpenedDate: formatTimeValue(e.OpenedDate),
		ClosedBy: e.ClosedBy, ClosedDate: formatTimeValue(e.ClosedDate),
		ClosedReason:  e.ClosedReason,
		SuspendedDate: formatTimeValue(e.SuspendedDate),
		Order:         e.Order,
	}
}

// calendarDays 计算起止之间的日历天数（含首尾）。
//
// 注意这是**日历日**而不是工作日：真正的排期要扣掉周末与节假日，
// 那需要一份节假日日历（禅道也是靠 calendar 配置做的）。
// 在拿到那份日历之前，这里给出的是一个明确、可解释的口径，
// 而不是一个假装是工期的数字。
func calendarDays(begin, end *time.Time) int {
	if begin == nil || end == nil {
		return 0
	}
	days := int(math.Round(end.Sub(*begin).Hours() / 24))
	if days < 0 {
		return 0
	}
	return days + 1
}

// assertWithinProject 校验迭代的起止落在项目周期内。
//
// 只有「项目与迭代都给了日期」时才校验：项目常常先不定日期就开工，
// 那时把一个没写完的周期当成约束会拦住正常操作。
func assertWithinProject(project *model.Project, begin, end *time.Time) error {
	if project.Begin == nil || project.End == nil {
		return nil
	}
	if begin != nil && begin.Before(*project.Begin) {
		return apperr.Newf(apperr.ExecutionDateOutOfProject, "迭代开始日期不能早于项目开始日期")
	}
	if end != nil && end.After(*project.End) {
		return apperr.Newf(apperr.ExecutionDateOutOfProject, "迭代结束日期不能晚于项目结束日期")
	}
	return nil
}

// calculateProgress 已由 hoursProgress 承担，见 task.go。

// formatDate 把时间指针格式化为 YYYY-MM-DD；nil 返回空串。
func formatDate(t *time.Time) string {
	if t == nil || t.IsZero() {
		return ""
	}
	return t.UTC().Format("2006-01-02")
}

// dedupIDs 去重并保持顺序。
func dedupIDs(ids []uint) []uint {
	if len(ids) < 2 {
		return ids
	}
	seen := make(map[uint]struct{}, len(ids))
	out := make([]uint, 0, len(ids))
	for _, id := range ids {
		if id == 0 {
			continue
		}
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}
