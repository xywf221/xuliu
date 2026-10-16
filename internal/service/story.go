package service

import (
	"context"
	"fmt"
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

// StoryService 负责需求的全生命周期。
//
// 需求是本系统里最复杂的一个实体，复杂在三处：
//
//  1. **版本化**。主表存当前内容，历史快照进 pm_storyspec。变更时版本 +1
//     并插入新快照；评审「撤销变更」时版本 −1 并删掉那一条。
//  2. **评审**。draft → reviewing →（通过/驳回/待明确/撤销变更）。评审人
//     是具体某个用户，评审结论按人记录在 pm_storyreview。
//  3. **状态流转**。规则全部来自 workflow.StoryMachine 的声明式转移表，
//     本服务只负责「变成那个状态要写哪些列」，不重复判断合法性。
type StoryService struct {
	db         *gorm.DB
	stories    *repository.StoryRepo
	specs      *repository.StorySpecRepo
	reviews    *repository.StoryReviewRepo
	links      *repository.ProjectStoryRepo
	products   *repository.ProductRepo
	modules    *repository.ModuleRepo
	plans      *repository.PlanRepo
	projects   *repository.ProjectRepo
	executions *repository.ExecutionRepo
	tasks      *repository.TaskRepo
	bugs       *repository.BugRepo
	users      *repository.UserRepo
	files      *repository.FileRepo
	comments   *repository.CommentRepo
	actions    *repository.ActionRepo
	gate       *Gate
	notifier   notifier

	recorder *history.Recorder
}

// NewStoryService 构造需求服务。
func NewStoryService(
	db *gorm.DB,
	stories *repository.StoryRepo,
	specs *repository.StorySpecRepo,
	reviews *repository.StoryReviewRepo,
	links *repository.ProjectStoryRepo,
	products *repository.ProductRepo,
	modules *repository.ModuleRepo,
	plans *repository.PlanRepo,
	projects *repository.ProjectRepo,
	executions *repository.ExecutionRepo,
	tasks *repository.TaskRepo,
	bugs *repository.BugRepo,
	users *repository.UserRepo,
	files *repository.FileRepo,
	comments *repository.CommentRepo,
	actions *repository.ActionRepo,
	gate *Gate,
	notify notifier,
) *StoryService {
	return &StoryService{
		db: db, stories: stories, specs: specs, reviews: reviews, links: links,
		products: products, modules: modules, plans: plans,
		projects: projects, executions: executions, tasks: tasks, bugs: bugs,
		users: users, files: files, comments: comments, actions: actions, gate: gate,
		notifier: orNoop(notify),
		recorder: history.New(StoryLabeler{}),
	}
}

// ---------------------------------------------------------------------------
// 查询
// ---------------------------------------------------------------------------

// List 分页查询需求。
func (s *StoryService) List(ctx context.Context, req dto.StoryFilterReq) (*pagination.Result[dto.StoryResp], error) {
	visible, err := s.gate.VisibleProducts(ctx)
	if err != nil {
		return nil, err
	}

	filter := repository.StoryFilter{
		ProductID:        req.ProductID,
		PlanID:           req.PlanID,
		Status:           req.Status,
		Stage:            req.Stage,
		Type:             req.Type,
		Category:         req.Category,
		Source:           req.Source,
		Pri:              req.Pri,
		AssignedTo:       req.AssignedTo,
		OpenedBy:         req.OpenedBy,
		ChangedOnly:      req.ChangedOnly,
		IncludeSubModule: req.IncludeSubModule,
		Keywords:         req.Keywords,
		ProductIDs:       visible,
		Pagination:       pagination.Params{Page: req.Page, Size: req.Size, Order: req.Order},
	}
	// 「只看未分类」与「按某个模块过滤」是互斥的两种表达。
	if req.Unclassified {
		zero := uint(0)
		filter.ModuleID = &zero
	} else if req.ModuleID > 0 {
		moduleID := req.ModuleID
		filter.ModuleID = &moduleID
	}
	if req.TopOnly {
		zero := uint(0)
		filter.ParentID = &zero
	}

	result, err := s.stories.List(ctx, filter)
	if err != nil {
		return nil, err
	}
	return s.decorateList(ctx, result)
}

// Get 取需求详情。
func (s *StoryService) Get(ctx context.Context, id uint) (*dto.StoryDetailResp, error) {
	story, err := s.stories.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := s.gate.AssertProductVisible(ctx, story.ProductID); err != nil {
		return nil, err
	}

	decorated, err := s.decorateList(ctx, &pagination.Result[model.Story]{
		List: []model.Story{*story}, Total: 1, Page: 1, Size: 1,
	})
	if err != nil {
		return nil, err
	}

	out := dto.StoryDetailResp{StoryResp: decorated.List[0]}
	out.Actions = availableStateActions(ctx, workflow.StoryMachine, story.Status)

	// 已排入的项目与迭代：需求详情页要能看出「这条需求现在被哪些项目在做」。
	links, err := s.storyLinks(ctx, []model.Story{*story})
	if err != nil {
		return nil, err
	}
	out.Projects = links[id]

	// 当前版本的规格。
	if spec, err := s.specs.Find(ctx, id, story.Version); err == nil && spec != nil {
		out.Spec = spec.Spec
		out.Verify = spec.Verify
		out.SpecFiles, _ = decodeIDs(spec.Files)
	}
	// 当前版本的评审记录。
	if reviews, err := s.reviews.ListByVersion(ctx, id, story.Version); err == nil {
		out.Reviews = s.toReviewResps(ctx, reviews)
	}
	// 需求上的全部附件。
	if files, err := s.files.ListByObject(ctx, model.ObjectTypeStory, id); err == nil {
		out.Files = s.toFileResps(ctx, files)
	}
	return &out, nil
}

// Versions 返回需求的全部历史版本快照。
func (s *StoryService) Versions(ctx context.Context, id uint) ([]dto.StorySpecResp, error) {
	story, err := s.stories.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := s.gate.AssertProductVisible(ctx, story.ProductID); err != nil {
		return nil, err
	}

	specs, err := s.specs.ListVersions(ctx, id)
	if err != nil {
		return nil, err
	}
	out := make([]dto.StorySpecResp, 0, len(specs))
	for _, sp := range specs {
		out = append(out, dto.StorySpecResp{
			Version:   sp.Version,
			Title:     sp.Title,
			Spec:      sp.Spec,
			Verify:    sp.Verify,
			CreatedAt: sp.CreatedAt.UTC().Format(time.RFC3339),
		})
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// 项目关联（只读）
// ---------------------------------------------------------------------------

// storyLinks 批量取需求被排进了哪些项目与迭代。
//
// 关联的**写入**在项目侧（ProjectService.LinkStory）：排需求要校验
// 「项目关联了该产品」「迭代属于该项目」，这些数据都在项目域里。
// 需求侧只需要读出来展示，就不必把项目的仓储再串一遍。
func (s *StoryService) storyLinks(ctx context.Context, stories []model.Story) (map[uint][]dto.StoryLinkResp, error) {
	ids := make([]uint, 0, len(stories))
	versions := make(map[uint]int, len(stories))
	for _, st := range stories {
		ids = append(ids, st.ID)
		versions[st.ID] = st.Version
	}
	out := make(map[uint][]dto.StoryLinkResp, len(stories))
	rows, err := s.links.ListByStories(ctx, ids)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return out, nil
	}

	projectIDs := make([]uint, 0, len(rows))
	executionIDs := make([]uint, 0, len(rows))
	for _, r := range rows {
		projectIDs = append(projectIDs, r.ProjectID)
		executionIDs = append(executionIDs, r.ExecutionID)
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
		out[r.StoryID] = append(out[r.StoryID], dto.StoryLinkResp{
			ID: r.ID, ProjectID: r.ProjectID, ProjectName: projectNames[r.ProjectID],
			ExecutionID: r.ExecutionID, ExecName: executionNames[r.ExecutionID],
			StoryID: r.StoryID, Version: r.Version,
			// 「过期」= 排入时的版本已经不是当前版本，即需求在排期之后变更过。
			Outdated: versions[r.StoryID] > 0 && r.Version != versions[r.StoryID],
			Order:    r.Order,
		})
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// 创建与编辑
// ---------------------------------------------------------------------------

// Create 新建需求。
func (s *StoryService) Create(ctx context.Context, req dto.CreateStoryReq) (*dto.StoryDetailResp, error) {
	var storyID uint
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		story, err := s.CreateTx(ctx, tx, req)
		if err != nil {
			return err
		}
		storyID = story.ID
		return nil
	})
	if err != nil {
		return nil, err
	}
	return s.Get(ctx, storyID)
}

// CreateTx 在调用方给出的事务里新建需求，返回建好的模型。
//
// 为什么需要它：缺陷「转需求」要在**一个**事务里同时完成「建需求」与
// 「回填缺陷的 to_story_id」。两件事分开提交，中间失败就会留下
// 「需求已经建出来了，缺陷却还停在激活态」的孤儿；用户看到缺陷没动，
// 就会再点一次，于是又多出一条重复需求（而重复的需求最难清理，
// 它可能已经被人排进迭代拆了任务）。让需求创建能被外层事务接纳，
// 是这件事原子化的前提。
//
// 校验（产品可见且未关闭、模块与计划归属、评审人回落）全部在这里做，
// 与 Create 共用同一份实现 —— 转换路径不该绕过任何一条约束。
func (s *StoryService) CreateTx(ctx context.Context, tx *gorm.DB, req dto.CreateStoryReq) (*model.Story, error) {
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
	// 已关闭的产品不允许再提需求 —— 否则新需求会落进一个不再推进的产品里。
	if product.Status == model.ProductStatusClosed {
		return nil, apperr.New(apperr.ProductClosed)
	}
	if err := s.assertModule(ctx, req.ModuleID, req.ProductID); err != nil {
		return nil, err
	}
	if err := s.assertPlan(ctx, req.PlanID, req.ProductID); err != nil {
		return nil, err
	}
	if strings.TrimSpace(req.Title) == "" {
		return nil, apperr.New(apperr.StoryTitleRequired)
	}

	actor := currentUserID(ctx)
	now := time.Now().UTC()

	// 产品可以关掉评审：此时新需求直接进入激活态，省掉一轮走流程。
	status := model.StoryStatusDraft
	if !product.ReviewStory {
		status = model.StoryStatusActive
	}

	story := &model.Story{
		ProductID:  req.ProductID,
		ModuleID:   req.ModuleID,
		PlanID:     req.PlanID,
		ParentID:   req.ParentID,
		Title:      strings.TrimSpace(req.Title),
		Keywords:   req.Keywords,
		Type:       defaultString(req.Type, model.StoryTypeStory),
		Category:   defaultString(req.Category, model.StoryCategoryFeature),
		Source:     defaultString(req.Source, model.StorySourcePO),
		SourceNote: req.SourceNote,
		Pri:        defaultPri(req.Pri),
		Estimate:   req.Estimate,
		Status:     status,
		Stage:      model.StoryStageWait,
		Version:    1,
		OpenedBy:   actor,
		OpenedDate: &now,
		AssignedTo: req.AssignedTo,
	}
	if status == model.StoryStatusActive {
		story.ActivatedDate = &now
	}
	if req.AssignedTo > 0 {
		story.AssignedDate = &now
	}

	// 评审人：优先用请求里指定的，否则回落到产品的默认评审人。
	reviewers := req.Reviewers
	if len(reviewers) == 0 && status == model.StoryStatusDraft {
		reviewers, _ = decodeIDs(product.Reviewer)
	}
	if status == model.StoryStatusDraft && len(reviewers) == 0 {
		// 走评审却没人可评，需求会永远卡在草稿里 —— 这属于配置问题，早点报出来。
		return nil, apperr.Newf(apperr.StoryReviewerRequired,
			"产品「%s」开启了需求评审，但没有设置默认评审人，请先指定评审人", product.Name)
	}
	if len(reviewers) > 0 {
		encoded, err := encodeIDs(reviewers)
		if err != nil {
			return nil, err
		}
		story.ReviewedBy = encoded
	}

	// 附件的归属校验放在事务前：附件不存在就直接失败，不必开事务。
	specFiles, err := encodeIDs(req.Files)
	if err != nil {
		return nil, err
	}

	if err := s.stories.Create(ctx, tx, story); err != nil {
		return nil, err
	}
	// 版本 1 的内容快照。之后每次变更都会新增一条。
	if err := s.specs.Create(ctx, tx, &model.StorySpec{
		StoryID: story.ID, Version: 1,
		Title: story.Title, Spec: req.Spec, Verify: req.Verify,
		Files: specFiles,
	}); err != nil {
		return nil, err
	}
	if len(req.Files) > 0 {
		if err := s.files.BindToObject(ctx, tx, req.Files, model.ObjectTypeStory, story.ID); err != nil {
			return nil, err
		}
	}
	if err := s.record(ctx, tx, history.Entry{
		ObjectType: model.ObjectTypeStory,
		ObjectID:   story.ID,
		ProductID:  story.ProductID,
		ActorID:    actor,
		Action:     model.ActionOpened,
		Comment:    req.Comment,
		Files:      req.Files,
	}); err != nil {
		return nil, err
	}
	// 建需求时就指了人，等于「下一步在他手上」，直接通知。
	// 缺陷「转需求」走的也是这条路径，所以顺带覆盖了那种情况。
	if story.AssignedTo > 0 {
		noticeTitle, noticeContent := noticeStoryAssigned(story.Title)
		if err := s.notifier.Send(ctx, tx, story.AssignedTo, actor,
			model.ObjectTypeStory, story.ID, noticeTitle, noticeContent); err != nil {
			return nil, err
		}
	}
	return story, nil
}

// Update 编辑需求内容（不改状态）。
//
// 版本号的语义要说清楚：普通编辑**不**增加版本号，只有「需求变更」
// （change）才会。原因是二者的重量级不同 —— 改个错字不该向下游声明
// 「需求变了」。
//
// 那么这里的 version 校验防什么？防「你手里的内容已经过时」：甲打开编辑框
// （v1），乙期间提交了一次正式的变更（v1→v2），甲此时提交就会拿到
// story_version_conflict，而不是把乙刚写进去的内容蒸发掉。
func (s *StoryService) Update(ctx context.Context, id uint, req dto.UpdateStoryReq) (*dto.StoryDetailResp, error) {
	story, err := s.stories.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := s.gate.AssertProductVisible(ctx, story.ProductID); err != nil {
		return nil, err
	}

	// 状态机也要放行「编辑」这个动作：已关闭的需求不可再改。
	if _, statusErr := workflow.StoryMachine.Resolve("edit", story.Status); statusErr != nil {
		return nil, workflow.StoryMachine.DescribeError(statusErr)
	}

	if req.ModuleID != nil {
		if err := s.assertModule(ctx, *req.ModuleID, story.ProductID); err != nil {
			return nil, err
		}
	}
	if req.PlanID != nil {
		if err := s.assertPlan(ctx, *req.PlanID, story.ProductID); err != nil {
			return nil, err
		}
	}
	if req.Stage != nil && !isValidStage(*req.Stage) {
		return nil, apperr.Newf(apperr.StoryStageInvalid, "未知的需求阶段：%s", *req.Stage)
	}
	if req.Title != nil && strings.TrimSpace(*req.Title) == "" {
		return nil, apperr.New(apperr.StoryTitleRequired)
	}

	actor := currentUserID(ctx)
	now := time.Now().UTC()

	fields := map[string]any{}
	setIfNotNil(fields, "title", trimPtr(req.Title))
	setIfNotNil(fields, "keywords", req.Keywords)
	setIfNotNil(fields, "type", req.Type)
	setIfNotNil(fields, "category", req.Category)
	setIfNotNil(fields, "source", req.Source)
	setIfNotNil(fields, "stage", req.Stage)
	if req.ModuleID != nil {
		fields["module_id"] = *req.ModuleID
	}
	if req.PlanID != nil {
		fields["plan_id"] = *req.PlanID
	}
	if req.Pri != nil {
		fields["pri"] = *req.Pri
	}
	if req.Estimate != nil {
		fields["estimate"] = *req.Estimate
	}
	if req.AssignedTo != nil {
		if err := s.assertUserExists(ctx, *req.AssignedTo); err != nil {
			return nil, err
		}
		fields["assigned_to"] = *req.AssignedTo
		fields["assigned_date"] = now
	}
	fields["last_edited_by"] = actor
	fields["last_edited_date"] = now

	// 规格内容（描述/验收标准）属于本条版本，写回当前版本的快照行。
	editingSpec := req.Spec != nil || req.Verify != nil

	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := s.stories.UpdateVersioned(ctx, tx, id, req.Version, fields); err != nil {
			return err
		}
		if editingSpec {
			specFields := map[string]any{}
			if req.Spec != nil {
				specFields["spec"] = *req.Spec
			}
			if req.Verify != nil {
				specFields["verify"] = *req.Verify
			}
			// 只改当前版本那一行：历史版本的描述不能被后来的编辑改写，
			// 否则「逐版对比」就失去了意义。
			if _, err := s.specs.UpdateWhere(ctx, tx,
				"story_id = ? AND version = ?", []any{id, story.Version}, specFields); err != nil {
				return err
			}
		}
		if len(req.Files) > 0 {
			if err := s.files.BindToObject(ctx, tx, req.Files, model.ObjectTypeStory, id); err != nil {
				return err
			}
		}

		changes := s.diffForEdit(ctx, story, req)
		if len(changes) == 0 && req.Comment == "" {
			// 什么都没改就不写历史，否则时间线会被空记录刷屏。
			return nil
		}
		return s.record(ctx, tx, history.Entry{
			ObjectType: model.ObjectTypeStory,
			ObjectID:   id,
			ProductID:  story.ProductID,
			ActorID:    actor,
			Action:     model.ActionEdited,
			Comment:    req.Comment,
			Changes:    changes,
			Files:      req.Files,
		})
	})
	if err != nil {
		return nil, err
	}
	return s.Get(ctx, id)
}

// Delete 删除需求。
//
// 级联清理规格、评审、评论与附件归属：这些都是需求的从属数据，
// 留着只会指向一个不存在的需求。
func (s *StoryService) Delete(ctx context.Context, id uint) error {
	story, err := s.stories.GetByID(ctx, id)
	if err != nil {
		return err
	}
	if err := s.gate.AssertProductVisible(ctx, story.ProductID); err != nil {
		return err
	}
	// 已发布的需求带着下游承诺，不允许直接删。
	if story.Stage == model.StoryStageReleased {
		return apperr.New(apperr.StoryCannotDeleteReleased)
	}
	// 有子需求时不允许删：子需求会失去父节点，变成找不到归属的孤儿。
	if count, err := s.stories.CountChildren(ctx, id); err != nil {
		return err
	} else if count > 0 {
		return apperr.Newf(apperr.StoryHasChildren,
			"该需求下还有 %d 条子需求，请先删除或转移", count)
	}
	// 未完成的任务同样构成阻塞：它们还挂在需求的进度上。
	// 已完成/已取消/已关闭的任务是历史，不该让需求永远删不掉。
	if count, err := s.tasks.CountUnfinishedByStory(ctx, id); err != nil {
		return err
	} else if count > 0 {
		return apperr.Newf(apperr.StoryHasTasks,
			"该需求下还有 %d 个未完成的任务，请先完成或取消它们", count)
	}
	// 由缺陷转入的需求不能删：那条缺陷还写着「已解决/转为需求」并指向它，
	// 删掉需求就等于抹掉缺陷的处理结果。这类需求该走关闭。
	if count, err := s.bugs.CountConvertedTo(ctx, id); err != nil {
		return err
	} else if count > 0 {
		return apperr.Newf(apperr.StoryFromBug,
			"该需求是由 %d 个缺陷转入的，不能删除；如需下线请改为关闭", count)
	}

	actor := currentUserID(ctx)
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// 解除与项目/迭代的关联：需求没了，这些排期记录也就失去了指向。
		if _, err := s.links.DeleteWhere(ctx, tx, "story_id = ?", id); err != nil {
			return err
		}
		// 缺陷里指向本条需求的关联也一并清空。缺陷本身留着（它是产品的问题，
		// 不该随着需求消失），但再挂着一个已删的需求 id，列表里只会显示成
		// 一片空白，看起来像数据损坏。
		if res := tx.WithContext(ctx).Model(&model.Bug{}).
			Where("story_id = ?", id).
			Updates(map[string]any{"story_id": 0, "story_version": 0}); res.Error != nil {
			return apperr.Wrap(apperr.Internal, res.Error)
		}
		if res := tx.WithContext(ctx).Where("story_id = ?", id).
			Delete(&model.StorySpec{}); res.Error != nil {
			return fmt.Errorf("service: 清理需求规格失败: %w", res.Error)
		}
		if res := tx.WithContext(ctx).Where("story_id = ?", id).
			Delete(&model.StoryReview{}); res.Error != nil {
			return fmt.Errorf("service: 清理评审记录失败: %w", res.Error)
		}
		if _, err := s.comments.DeleteByObject(ctx, tx, model.ObjectTypeStory, id); err != nil {
			return err
		}
		// 附件本身不删（可能被别处引用），只解除归属，让它变回「未挂载」。
		if res := tx.WithContext(ctx).Model(&model.File{}).
			Where("object_type = ? AND object_id = ?", model.ObjectTypeStory, id).
			Updates(map[string]any{"object_type": "", "object_id": 0}); res.Error != nil {
			return fmt.Errorf("service: 解除附件归属失败: %w", res.Error)
		}
		if err := s.record(ctx, tx, history.Entry{
			ObjectType: model.ObjectTypeStory,
			ObjectID:   id,
			ProductID:  story.ProductID,
			ActorID:    actor,
			Action:     model.ActionDeleted,
		}); err != nil {
			return err
		}
		return s.stories.Delete(ctx, tx, id)
	})
}

// ---------------------------------------------------------------------------
// 状态流转
// ---------------------------------------------------------------------------

// ActionInput 是所有需求动作的公共入参。
//
// 用一个结构体承载而不是为每个动作定义一个方法：动作之间有大量共享
// 逻辑（权限判定、状态校验、乐观锁、写历史），拆散会把这些逻辑复制
// 十来遍。动作特有的字段在这里留空即可。
type ActionInput struct {
	Action  string
	Comment string
	// Version 是乐观锁版本号；0 表示不校验（不涉及内容变更的动作不必带）。
	Version int

	// 提交评审 / 评审
	Reviewers []uint
	Result    string

	// 关闭 / 驳回
	ClosedReason     string
	DuplicateStoryID uint

	// 指派 / 激活
	AssignedTo uint

	// 变更
	Title  string
	Spec   string
	Verify string
	Reason string

	// 阶段推进
	Stage string

	Files []uint
}

// Apply 执行一个需求动作。
//
// 所有流转都收敛到这一个入口，三段式：解析动作 → 事务内落库 → 写历史。
// 状态合法性交给 workflow.StoryMachine 判定，本方法只负责「到了那个
// 状态要写哪些列」。
func (s *StoryService) Apply(ctx context.Context, id uint, in ActionInput) (*dto.StoryDetailResp, error) {
	story, err := s.stories.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := s.gate.AssertProductVisible(ctx, story.ProductID); err != nil {
		return nil, err
	}

	// 合法性判定交给状态机：动作不存在、或当前状态不允许，都在这里被挡下，
	// 并给出「现在需要处于哪些状态」这样的可操作提示。
	if _, statusErr := workflow.StoryMachine.Resolve(in.Action, story.Status); statusErr != nil {
		return nil, workflow.StoryMachine.DescribeError(statusErr)
	}

	actor := currentUserID(ctx)
	now := time.Now().UTC()

	switch in.Action {
	case "submitreview":
		err = s.applySubmitReview(ctx, story, in, actor, now)
	case "recall":
		err = s.applyRecall(ctx, story, in, actor, now)
	case "pass", "clarify", "revert", "reject":
		err = s.applyReview(ctx, story, in, actor, now)
	case "change":
		err = s.applyChange(ctx, story, in, actor, now)
	case "close":
		err = s.applyClose(ctx, story, in, actor, now)
	case "activate":
		err = s.applyActivate(ctx, story, in, actor, now)
	case "assign":
		err = s.applyAssign(ctx, story, in, actor, now)
	default:
		// workflow 里定义了但本服务没实现 —— 属于编码遗漏，不是用户输入问题。
		err = apperr.Newf(apperr.Internal, "动作 %s 尚未实现", in.Action)
	}
	if err != nil {
		return nil, err
	}
	return s.Get(ctx, id)
}

// applySubmitReview 提交评审：draft/changing → reviewing。
func (s *StoryService) applySubmitReview(ctx context.Context, story *model.Story, in ActionInput, actor uint, now time.Time) error {
	reviewers := in.Reviewers
	if len(reviewers) == 0 {
		// 没指定就沿用上次的名单（可能是产品默认评审人，也可能是上次提交时选的）。
		reviewers, _ = decodeIDs(story.ReviewedBy)
	}
	if len(reviewers) == 0 {
		return apperr.New(apperr.StoryReviewerRequired)
	}
	encoded, err := encodeIDs(reviewers)
	if err != nil {
		return err
	}

	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		fields := map[string]any{
			"status":        model.StoryStatusReviewing,
			"reviewed_by":   encoded,
			"reviewed_date": nil,
		}
		if err := s.updateStory(ctx, tx, story, in.Version, fields); err != nil {
			return err
		}
		// 换人重评时清掉旧结论：版本没变但评审人变了，
		// 旧结论代表的是另一个人对另一份名单的意见。
		if err := s.reviews.Replace(ctx, tx, story.ID, story.Version, reviewers); err != nil {
			return err
		}
		if err := s.recordStatus(ctx, tx, story, actor, in, model.ActionSubmitReview,
			story.Status, model.StoryStatusReviewing, now); err != nil {
			return err
		}
		// 每个评审人各收到一条：名单上的人都是「下一步要动手的人」。
		// 提交人自己在名单里时 Send 会跳过，不必在这里判。
		noticeTitle, noticeContent := noticeStoryReviewRequested(story.Title)
		for _, reviewer := range reviewers {
			if err := s.notifier.Send(ctx, tx, reviewer, actor,
				model.ObjectTypeStory, story.ID, noticeTitle, noticeContent); err != nil {
				return err
			}
		}
		return nil
	})
}

// applyRecall 撤销评审：reviewing/changing →（有变更则 changing，否则 draft）。
func (s *StoryService) applyRecall(ctx context.Context, story *model.Story, in ActionInput, actor uint, now time.Time) error {
	target := recallTarget(story)

	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := s.updateStory(ctx, tx, story, in.Version,
			map[string]any{"status": target}); err != nil {
			return err
		}
		// 撤销即作废本次评审：删掉当前版本的评审行，
		// 否则下次提交评审会看到一批「上次留下的」结论。
		if err := s.reviews.Replace(ctx, tx, story.ID, story.Version, nil); err != nil {
			return err
		}
		return s.recordStatus(ctx, tx, story, actor, in, model.ActionRecalled,
			story.Status, target, now)
	})
}

// applyReview 处理四种评审结论。
//
// 采用「即时生效」语义：任意一位评审人给出结论，需求状态立即按该结论
// 变化，不等待其余评审人。理由是我们的评审名单规模很小（一两位），
// 而「等所有人到齐」会带来一个额外的中间状态和一套超时逻辑，
// 收益不抵复杂度。
func (s *StoryService) applyReview(ctx context.Context, story *model.Story, in ActionInput, actor uint, now time.Time) error {
	// revert（撤销变更）只对「变更后送审」有意义：版本 1 的需求没有变更可撤。
	if in.Action == "revert" && story.Version <= 1 {
		return apperr.Newf(apperr.StoryNotChanged, "该需求还没有变更记录，无法撤销变更")
	}
	if in.Action == "reject" && in.ClosedReason == "" {
		return apperr.New(apperr.StoryClosedReasonRequired)
	}

	target := map[string]string{
		"pass":    model.StoryStatusActive,
		"clarify": clarifyTarget(story),
		"revert":  model.StoryStatusActive,
		"reject":  model.StoryStatusClosed,
	}[in.Action]

	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		fields := map[string]any{
			"status":        target,
			"reviewed_date": now,
		}
		if target == model.StoryStatusActive {
			fields["activated_date"] = now
		}
		if in.Action == "reject" {
			fields["closed_by"] = actor
			fields["closed_date"] = now
			fields["closed_reason"] = in.ClosedReason
		}

		// 版本回退：撤销变更要把最新那版内容作废。
		//
		// 顺序很关键 —— 先删掉「当前版本」的 spec 行与评审行，再写入
		// version-1，否则会把刚删的内容又留在库里。
		if in.Action == "revert" {
			if _, err := s.specs.DeleteLatest(ctx, tx, story.ID, story.Version); err != nil {
				return err
			}
			if err := s.reviews.Replace(ctx, tx, story.ID, story.Version, nil); err != nil {
				return err
			}
			fields["version"] = story.Version - 1
			fields["story_changed"] = false
			fields["changed_by"] = 0
			fields["changed_date"] = nil
		}

		if err := s.updateStory(ctx, tx, story, in.Version, fields); err != nil {
			return err
		}
		// 记录本人结论。不在评审人名单里会在这里被拒。
		if err := s.reviews.MarkResult(ctx, tx, story.ID, story.Version, actor, in.Result, now); err != nil {
			return err
		}

		entryAction := model.ActionReviewed
		extra := workflow.StatusChangeExtra(story.Status, target)
		if in.Action == "reject" {
			extra = in.ClosedReason
		}
		if err := s.record(ctx, tx, history.Entry{
			ObjectType: model.ObjectTypeStory,
			ObjectID:   story.ID,
			ProductID:  story.ProductID,
			ActorID:    actor,
			Action:     entryAction,
			Comment:    in.Comment,
			Extra:      extra,
			Changes:    s.diffFields(story, fields),
		}); err != nil {
			return err
		}
		// 结论交给提需求的人：不管通过还是驳回，接下来都是他去推进。
		// Result 为空时回退到 Action —— 二者的取值本来就是一回事。
		noticeTitle, noticeContent := noticeStoryReviewed(story.Title,
			defaultString(in.Result, in.Action))
		return s.notifier.Send(ctx, tx, story.OpenedBy, actor,
			model.ObjectTypeStory, story.ID, noticeTitle, noticeContent)
	})
}

// applyChange 需求变更：active → changing，版本 +1 并写入新内容。
func (s *StoryService) applyChange(ctx context.Context, story *model.Story, in ActionInput, actor uint, now time.Time) error {
	if strings.TrimSpace(in.Reason) == "" {
		return apperr.Newf(apperr.InvalidParam, "请填写变更原因")
	}
	title := strings.TrimSpace(in.Title)
	if title == "" {
		title = story.Title
	}
	newVersion := story.Version + 1
	specFiles, err := encodeIDs(in.Files)
	if err != nil {
		return err
	}

	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		fields := map[string]any{
			"status":           model.StoryStatusChanging,
			"version":          newVersion,
			"story_changed":    true,
			"changed_by":       actor,
			"changed_date":     now,
			"last_edited_by":   actor,
			"last_edited_date": now,
		}
		if strings.TrimSpace(in.Title) != "" {
			fields["title"] = title
		}
		if err := s.updateStory(ctx, tx, story, in.Version, fields); err != nil {
			return err
		}
		// 新版本的内容快照。旧版本的快照留在库里，供逐版对比。
		if err := s.specs.Create(ctx, tx, &model.StorySpec{
			StoryID: story.ID, Version: newVersion,
			Title: title, Spec: in.Spec, Verify: in.Verify,
			Files: specFiles,
		}); err != nil {
			return err
		}
		if len(in.Files) > 0 {
			if err := s.files.BindToObject(ctx, tx, in.Files, model.ObjectTypeStory, story.ID); err != nil {
				return err
			}
		}
		return s.record(ctx, tx, history.Entry{
			ObjectType: model.ObjectTypeStory,
			ObjectID:   story.ID,
			ProductID:  story.ProductID,
			ActorID:    actor,
			Action:     model.ActionEdited,
			Comment:    in.Comment,
			// 变更原因放进 Extra：下游看时间线时最想知道的就是「为什么变」。
			Extra: in.Reason,
			Changes: append(
				[]history.Change{{Field: "Version", Old: strconv.Itoa(story.Version), New: strconv.Itoa(newVersion)}},
				s.diffFields(story, fields)...),
			Files: in.Files,
		})
	})
}

// applyClose 关闭需求。
func (s *StoryService) applyClose(ctx context.Context, story *model.Story, in ActionInput, actor uint, now time.Time) error {
	if in.ClosedReason == "" {
		return apperr.New(apperr.StoryClosedReasonRequired)
	}
	if in.ClosedReason == model.StoryCloseDuplicate {
		if in.DuplicateStoryID == 0 {
			return apperr.New(apperr.StoryDuplicateTargetInvalid)
		}
		if in.DuplicateStoryID == story.ID {
			return apperr.Newf(apperr.StoryDuplicateTargetInvalid, "不能把需求标记为它自己的重复项")
		}
		target, err := s.stories.FindByID(ctx, in.DuplicateStoryID)
		if err != nil {
			return err
		}
		if target == nil {
			return apperr.New(apperr.StoryDuplicateTargetInvalid)
		}
	}

	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		fields := map[string]any{
			"status":             model.StoryStatusClosed,
			"closed_by":          actor,
			"closed_date":        now,
			"closed_reason":      in.ClosedReason,
			"duplicate_story_id": in.DuplicateStoryID,
		}
		if in.ClosedReason != model.StoryCloseDuplicate {
			fields["duplicate_story_id"] = 0
		}
		if err := s.updateStory(ctx, tx, story, in.Version, fields); err != nil {
			return err
		}
		return s.recordStatus(ctx, tx, story, actor, in, model.ActionClosed,
			story.Status, model.StoryStatusClosed, now)
	})
}

// applyActivate 激活已关闭的需求，恢复到关闭前的状态。
//
// 前状态从操作历史里读（见 repository.ActionRepo.LastStatusChange），
// 而不是在需求上另存一列「关闭前状态」：那列只在关闭/激活这一对动作里
// 有意义，为它占一列会让实体的字段越堆越多，而历史里本来就有这个信息。
func (s *StoryService) applyActivate(ctx context.Context, story *model.Story, in ActionInput, actor uint, now time.Time) error {
	target := model.StoryStatusActive

	last, err := s.actions.LastStatusChange(ctx, model.ObjectTypeStory, story.ID)
	if err != nil {
		return err
	}
	if last != nil {
		if from, _, ok := workflow.ParseStatusChangeExtra(last.Extra); ok && from != "" {
			// 关闭前的状态才是要恢复的目标；若那时也是 closed（连续关闭），
			// 说明历史异常，退回默认的 active。
			if from != model.StoryStatusClosed && workflow.StoryMachine.IsValidStatus(from) {
				target = from
			}
		}
	}

	if in.AssignedTo > 0 {
		if err := s.assertUserExists(ctx, in.AssignedTo); err != nil {
			return err
		}
	}

	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		fields := map[string]any{
			"status":             target,
			"closed_by":          0,
			"closed_date":        nil,
			"closed_reason":      "",
			"duplicate_story_id": 0,
			"activated_date":     now,
		}
		if in.AssignedTo > 0 {
			fields["assigned_to"] = in.AssignedTo
			fields["assigned_date"] = now
		}
		if err := s.updateStory(ctx, tx, story, in.Version, fields); err != nil {
			return err
		}
		return s.recordStatus(ctx, tx, story, actor, in, model.ActionActivated,
			story.Status, target, now)
	})
}

// applyAssign 指派需求。
func (s *StoryService) applyAssign(ctx context.Context, story *model.Story, in ActionInput, actor uint, now time.Time) error {
	if in.AssignedTo == 0 {
		return apperr.Newf(apperr.InvalidParam, "请选择指派人")
	}
	if err := s.assertUserExists(ctx, in.AssignedTo); err != nil {
		return err
	}

	// 只能指派给产品可见范围内的人：把需求指给一个打不开它的人，
	// 等于制造一条悬空待办。
	if err := s.gate.AssertProductVisible(ctx, story.ProductID); err != nil {
		return err
	}

	if in.AssignedTo == story.AssignedTo {
		return apperr.Newf(apperr.InvalidParam, "需求已经指派给该成员")
	}

	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		fields := map[string]any{
			"assigned_to":   in.AssignedTo,
			"assigned_date": now,
		}
		if err := s.updateStory(ctx, tx, story, in.Version, fields); err != nil {
			return err
		}
		if err := s.record(ctx, tx, history.Entry{
			ObjectType: model.ObjectTypeStory,
			ObjectID:   story.ID,
			ProductID:  story.ProductID,
			ActorID:    actor,
			Action:     model.ActionAssigned,
			Comment:    in.Comment,
			// Extra 存目标用户 id（该形态与禅道一致，便于导入老数据）。
			Extra:   strconv.FormatUint(uint64(in.AssignedTo), 10),
			Changes: s.diffFields(story, fields),
		}); err != nil {
			return err
		}
		noticeTitle, noticeContent := noticeStoryAssigned(story.Title)
		return s.notifier.Send(ctx, tx, in.AssignedTo, actor,
			model.ObjectTypeStory, story.ID, noticeTitle, noticeContent)
	})
}

// ChangeStage 推进需求阶段。
//
// 阶段（stage）与状态（status）是两条正交的线：状态管「需求本身走到哪一步」
// （草稿/评审/激活/关闭），阶段管「交付推进到哪一步」（研发中/测试中/已发布）。
// 因此阶段不进入状态机，只做取值校验。
func (s *StoryService) ChangeStage(ctx context.Context, id uint, stage, comment string) (*dto.StoryDetailResp, error) {
	if !isValidStage(stage) {
		return nil, apperr.Newf(apperr.StoryStageInvalid, "未知的需求阶段：%s", stage)
	}

	story, err := s.stories.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := s.gate.AssertProductVisible(ctx, story.ProductID); err != nil {
		return nil, err
	}
	if story.Status == model.StoryStatusClosed {
		return nil, apperr.Newf(apperr.StoryStageInvalid, "已关闭的需求不能再推进阶段")
	}
	if story.Stage == stage {
		return nil, apperr.Newf(apperr.StoryStageInvalid, "需求已处于该阶段")
	}

	actor := currentUserID(ctx)
	now := time.Now().UTC()

	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		fields := map[string]any{
			"stage":     stage,
			"staged_by": actor,
		}
		// 进入/离开「已验收」时要维护验收时间，它是发布前的验收凭据。
		if stage == model.StoryStageVerified {
			fields["verified_date"] = now
		}
		if err := s.stories.UpdateFields(ctx, tx, id, fields); err != nil {
			return err
		}
		return s.record(ctx, tx, history.Entry{
			ObjectType: model.ObjectTypeStory,
			ObjectID:   id,
			ProductID:  story.ProductID,
			ActorID:    actor,
			Action:     model.ActionEdited,
			Comment:    comment,
			Changes:    s.diffFields(story, fields),
		})
	})
	if err != nil {
		return nil, err
	}
	return s.Get(ctx, id)
}

// ---------------------------------------------------------------------------
// 内部辅助
// ---------------------------------------------------------------------------

// updateStory 按是否需要乐观锁写入字段。
//
// 带 Version 的请求走带版本条件的更新；不带的多是「状态类」动作
// （评审通过、关闭），它们不修改内容，也就没有覆盖别人编辑的风险。
func (s *StoryService) updateStory(ctx context.Context, tx *gorm.DB, story *model.Story, version int, fields map[string]any) error {
	if version > 0 {
		return s.stories.UpdateVersioned(ctx, tx, story.ID, version, fields)
	}
	return s.stories.UpdateFields(ctx, tx, story.ID, fields)
}

// recordStatus 写一条「状态变更」历史。
//
// Extra 存 "old|new"：需求的「激活」要靠它恢复关闭前的状态，
// 所以这个字符串是有语义的，不只是给人看的。
func (s *StoryService) recordStatus(
	ctx context.Context, tx *gorm.DB, story *model.Story,
	actor uint, in ActionInput, action, from, to string, now time.Time,
) error {
	return s.record(ctx, tx, history.Entry{
		ObjectType: model.ObjectTypeStory,
		ObjectID:   story.ID,
		ProductID:  story.ProductID,
		ActorID:    actor,
		Action:     action,
		Comment:    in.Comment,
		Extra:      workflow.StatusChangeExtra(from, to),
		Changes: []history.Change{
			{Field: "Status", Old: from, New: to},
		},
		Files: in.Files,
	})
}

// record 写入一条操作历史。
//
// 姓名补全在这里统一做：history.Labeler 是纯函数，拿不到 context，
// 所以「用户 id → 姓名」这类需要查库的翻译只能在本层完成。
func (s *StoryService) record(ctx context.Context, tx *gorm.DB, e history.Entry) error {
	if len(e.Changes) > 0 {
		e.Changes = fillUserNames(ctx, s.users, e.Changes)
	}
	_, err := s.recorder.Record(ctx, tx, e)
	return err
}

// diffFields 把「要写入的字段」与「当前值」比对成变更列表。
//
// fields 是 map，所以这里按 storyFieldLabels 里登记的字段逐个取值 ——
// 只有登记过的字段才进时间线，避免把内部字段（如 updated_at）
// 泄漏成一条用户看不懂的动态。
func (s *StoryService) diffFields(story *model.Story, fields map[string]any) []history.Change {
	changes := make([]history.Change, 0, len(fields))
	for field := range fields {
		label, ok := storyFieldLabels[field]
		if !ok {
			continue
		}
		_ = label
		old, new := storyFieldValues(story, field, fields[field])
		if old == new {
			continue
		}
		changes = append(changes, history.Change{Field: field, Old: old, New: new})
	}
	sortChanges(changes)
	return changes
}

// diffForEdit 比对编辑请求与当前值。
//
// 与 diffFields 的区别：编辑的入参是 DTO（指针字段），不能直接喂给
// 字段比对，所以先把 DTO 规整成「字段 → 新值」的 map 再复用同一套逻辑。
func (s *StoryService) diffForEdit(ctx context.Context, story *model.Story, req dto.UpdateStoryReq) []history.Change {
	fields := map[string]any{}
	if req.Title != nil {
		fields["Title"] = strings.TrimSpace(*req.Title)
	}
	if req.Keywords != nil {
		fields["Keywords"] = *req.Keywords
	}
	if req.Type != nil {
		fields["Type"] = *req.Type
	}
	if req.Category != nil {
		fields["Category"] = *req.Category
	}
	if req.Source != nil {
		fields["Source"] = *req.Source
	}
	if req.Stage != nil {
		fields["Stage"] = *req.Stage
	}
	if req.ModuleID != nil {
		fields["ModuleID"] = *req.ModuleID
	}
	if req.PlanID != nil {
		fields["PlanID"] = *req.PlanID
	}
	if req.Pri != nil {
		fields["Pri"] = *req.Pri
	}
	if req.Estimate != nil {
		fields["Estimate"] = *req.Estimate
	}
	if req.AssignedTo != nil {
		fields["AssignedTo"] = *req.AssignedTo
	}

	changes := make([]history.Change, 0, len(fields))
	for field, value := range fields {
		old, new := storyFieldValues(story, field, value)
		if old == new {
			continue
		}
		changes = append(changes, history.Change{Field: field, Old: old, New: new})
	}
	sortChanges(changes)
	return changes
}

// storyFieldValues 取某字段的「旧值字符串」与「新值字符串」。
//
// 字段名用 Go 风格大驼峰（与 storyFieldLabels 的键一致），
// 这样可以复用 history.Labeler 的字段名映射。
func storyFieldValues(story *model.Story, field string, newValue any) (old, new string) {
	toStr := func(v any) string {
		if v == nil {
			return ""
		}
		switch t := v.(type) {
		case string:
			return t
		case uint8:
			return strconv.FormatUint(uint64(t), 10)
		case int:
			return strconv.Itoa(t)
		case uint:
			return strconv.FormatUint(uint64(t), 10)
		case float64:
			return strconv.FormatFloat(t, 'f', -1, 64)
		default:
			return fmt.Sprint(v)
		}
	}
	new = toStr(newValue)

	switch field {
	case "Title":
		old = story.Title
	case "Keywords":
		old = story.Keywords
	case "Type":
		old = story.Type
	case "Category":
		old = story.Category
	case "Source":
		old = story.Source
	case "Stage":
		old = story.Stage
	case "ModuleID":
		old = strconv.FormatUint(uint64(story.ModuleID), 10)
	case "PlanID":
		old = strconv.FormatUint(uint64(story.PlanID), 10)
	case "Pri":
		old = strconv.Itoa(int(story.Pri))
	case "Estimate":
		if story.Estimate != nil {
			old = strconv.FormatFloat(*story.Estimate, 'f', -1, 64)
		}
	case "AssignedTo":
		old = strconv.FormatUint(uint64(story.AssignedTo), 10)
	case "Status":
		old = story.Status
	case "ClosedBy":
		old = strconv.FormatUint(uint64(story.ClosedBy), 10)
	case "ClosedReason":
		old = story.ClosedReason
	case "DuplicateStoryID":
		old = strconv.FormatUint(uint64(story.DuplicateStoryID), 10)
	case "Version":
		old = strconv.Itoa(story.Version)
	}
	return old, new
}

// decorateList 批量补全列表所需的展示字段。
//
// 全部用「一次查询取回、内存里拼」，避免列表 20 行变成 20 次
// 「查产品名 + 查模块名 + 查人名」的 N+1。
func (s *StoryService) decorateList(ctx context.Context, result *pagination.Result[model.Story]) (*pagination.Result[dto.StoryResp], error) {
	stories := result.List
	if len(stories) == 0 {
		return &pagination.Result[dto.StoryResp]{
			List: []dto.StoryResp{}, Total: result.Total, Page: result.Page, Size: result.Size,
		}, nil
	}

	productIDs := make([]uint, 0, len(stories))
	moduleIDs := make([]uint, 0, len(stories))
	planIDs := make([]uint, 0, len(stories))
	userIDs := make([]uint, 0, len(stories)*2)
	storyIDs := make([]uint, 0, len(stories))
	for i := range stories {
		st := &stories[i]
		productIDs = append(productIDs, st.ProductID)
		moduleIDs = append(moduleIDs, st.ModuleID)
		planIDs = append(planIDs, st.PlanID)
		userIDs = append(userIDs, st.OpenedBy, st.AssignedTo)
		storyIDs = append(storyIDs, st.ID)
	}

	productNames, err := s.products.NameMap(ctx, productIDs)
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
	planTitles, err := s.planTitleMap(ctx, planIDs)
	if err != nil {
		return nil, err
	}
	commentCounts, err := s.comments.CountByObjects(ctx, model.ObjectTypeStory, storyIDs)
	if err != nil {
		return nil, err
	}
	fileCounts, err := s.fileCounts(ctx, storyIDs)
	if err != nil {
		return nil, err
	}

	out := make([]dto.StoryResp, 0, len(stories))
	for i := range stories {
		st := &stories[i]
		out = append(out, dto.StoryResp{
			ID: st.ID, Code: storyCode(st.ID),
			ProductID: st.ProductID, ProductName: productNames[st.ProductID],
			ModuleID: st.ModuleID, ModuleName: moduleNames[st.ModuleID],
			PlanID: st.PlanID, PlanTitle: planTitles[st.PlanID],
			ParentID: st.ParentID, IsParent: st.IsParent,
			Title: st.Title, Keywords: st.Keywords,
			Type: st.Type, Category: st.Category,
			Source: st.Source, SourceNote: st.SourceNote,
			Pri: st.Pri, Estimate: st.Estimate,
			Status: st.Status, StatusLabel: workflow.StoryMachine.StatusLabel(st.Status),
			Stage: st.Stage, StageLabel: model.StoryStageLabels[st.Stage],
			Version: st.Version, StoryChanged: st.StoryChanged,
			OpenedBy: st.OpenedBy, OpenedByName: userNames[st.OpenedBy],
			OpenedDate: formatTimeValue(st.OpenedDate),
			AssignedTo: st.AssignedTo, AssignName: userNames[st.AssignedTo],
			AssignedDate: formatTimeValue(st.AssignedDate),
			ChangedBy:    st.ChangedBy, ChangedDate: formatTimeValue(st.ChangedDate),
			ClosedBy: st.ClosedBy, ClosedDate: formatTimeValue(st.ClosedDate),
			ClosedReason:     st.ClosedReason,
			DuplicateStoryID: st.DuplicateStoryID,
			FromBugID:        st.FromBugID, FromStoryID: st.FromStoryID,
			CommentCount: commentCounts[st.ID],
			FileCount:    fileCounts[st.ID],
		})
	}

	return &pagination.Result[dto.StoryResp]{
		List: out, Total: result.Total, Page: result.Page, Size: result.Size,
	}, nil
}

// planTitleMap 批量取计划标题。
//
// 计划表没有 NameMap（它只按产品查询），这里就地用一个轻查询补上。
func (s *StoryService) planTitleMap(ctx context.Context, ids []uint) (map[uint]string, error) {
	wanted := make([]uint, 0, len(ids))
	seen := make(map[uint]struct{}, len(ids))
	for _, id := range ids {
		if id == 0 {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		wanted = append(wanted, id)
	}
	if len(wanted) == 0 {
		return map[uint]string{}, nil
	}

	var rows []model.ProductPlan
	if err := s.db.WithContext(ctx).Model(&model.ProductPlan{}).
		Select("id", "title").Where("id IN ?", wanted).Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("service: 查询产品计划失败: %w", err)
	}
	out := make(map[uint]string, len(rows))
	for _, p := range rows {
		out[p.ID] = p.Title
	}
	return out, nil
}

// fileCounts 批量统计需求下的附件数。
func (s *StoryService) fileCounts(ctx context.Context, storyIDs []uint) (map[uint]int64, error) {
	var rows []struct {
		ObjectID uint
		Total    int64
	}
	err := s.db.WithContext(ctx).Model(&model.File{}).
		Select("object_id, COUNT(*) AS total").
		Where("object_type = ? AND object_id IN ?", model.ObjectTypeStory, storyIDs).
		Group("object_id").Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("service: 统计需求附件数失败: %w", err)
	}
	out := make(map[uint]int64, len(rows))
	for _, row := range rows {
		out[row.ObjectID] = row.Total
	}
	return out, nil
}

// toReviewResps 把评审记录转为出参并补全姓名与结论文案。
func (s *StoryService) toReviewResps(ctx context.Context, reviews []model.StoryReview) []dto.StoryReviewResp {
	if len(reviews) == 0 {
		return nil
	}
	ids := make([]uint, 0, len(reviews))
	for i := range reviews {
		ids = append(ids, reviews[i].Reviewer)
	}
	names, _ := s.users.NameMap(ctx, ids)

	out := make([]dto.StoryReviewResp, 0, len(reviews))
	for i := range reviews {
		r := &reviews[i]
		out = append(out, dto.StoryReviewResp{
			Reviewer:     r.Reviewer,
			ReviewerName: names[r.Reviewer],
			Result:       r.Result,
			ResultText:   reviewResultLabels[r.Result],
			ReviewDate:   formatTimeValue(r.ReviewDate),
		})
	}
	return out
}

// toFileResps 把附件转为出参（补全上传者姓名与人读大小）。
func (s *StoryService) toFileResps(ctx context.Context, files []model.File) []dto.FileResp {
	if len(files) == 0 {
		return nil
	}
	ids := make([]uint, 0, len(files))
	for i := range files {
		ids = append(ids, files[i].UserID)
	}
	names, _ := s.users.NameMap(ctx, ids)

	out := make([]dto.FileResp, 0, len(files))
	for i := range files {
		f := &files[i]
		out = append(out, dto.FileResp{
			ID: f.ID, Name: f.Name, Size: f.Size,
			ContentType: f.ContentType, Downloads: f.Downloads,
			UserID: f.UserID, UserName: names[f.UserID],
			CreatedAt: f.CreatedAt.UTC().Format(time.RFC3339),
		})
	}
	return out
}

// assertModule 校验模块属于该产品，且是需求树。
func (s *StoryService) assertModule(ctx context.Context, moduleID, productID uint) error {
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
	if m.Root != productID || m.Type != model.ModuleTypeStory {
		return apperr.Newf(apperr.ModuleWrongRoot, "所选模块不属于该产品的需求模块树")
	}
	return nil
}

// assertPlan 校验计划属于该产品。
func (s *StoryService) assertPlan(ctx context.Context, planID, productID uint) error {
	if planID == 0 {
		return nil
	}
	p, err := s.plans.FindByID(ctx, planID)
	if err != nil {
		return err
	}
	if p == nil {
		return apperr.New(apperr.PlanNotFound)
	}
	if p.ProductID != productID {
		return apperr.Newf(apperr.PlanNotFound, "所选计划不属于该产品")
	}
	return nil
}

// assertUserExists 校验用户存在。
func (s *StoryService) assertUserExists(ctx context.Context, userID uint) error {
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

// ---------------------------------------------------------------------------
// 纯函数辅助
// ---------------------------------------------------------------------------

// storyCode 生成需求编号。内部仍是自增 id，对外额外给一个稳定可读的编号。
func storyCode(id uint) string { return fmt.Sprintf("STORY-%d", id) }

// recallTarget 决定撤销评审后回到哪个状态。
//
// 有过变更的需求要回到 changing（它还有一次变更没走完），
// 否则回到 draft。判断依据是 ChangedBy：它只在变更时被写入。
func recallTarget(story *model.Story) string {
	if story.ChangedBy > 0 || story.Status == model.StoryStatusChanging {
		return model.StoryStatusChanging
	}
	return model.StoryStatusDraft
}

// clarifyTarget 决定「有待明确」后回到哪个状态，规则同 recallTarget。
func clarifyTarget(story *model.Story) string {
	return recallTarget(story)
}

// isValidStage 报告阶段取值是否合法。
func isValidStage(stage string) bool {
	_, ok := model.StoryStageLabels[stage]
	return ok
}

// defaultPri 未指定优先级时取「中」（禅道默认 3）。
func defaultPri(pri uint8) int {
	if pri == 0 {
		return model.PriLow
	}
	return int(pri)
}

// trimPtr 去空格并返回指针；入参为 nil 时返回 nil。
func trimPtr(s *string) *string {
	if s == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*s)
	return &trimmed
}

// setIfNotNil 在指针非 nil 时写入 map。
func setIfNotNil(fields map[string]any, key string, value *string) {
	if value != nil {
		fields[key] = *value
	}
}

// formatTimeValue 把时间指针格式化为 RFC3339；nil 返回空串。
//
// 与 formatTimePtr 的区别：出参里这类字段是 string 而非 *string，
// 前端拿到空串就当没值，比处理 null 少一层判断。
func formatTimeValue(t *time.Time) string {
	if t == nil || t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

// sortChanges 按字段名排序，保证历史里的变更顺序稳定。
func sortChanges(changes []history.Change) {
	for i := 1; i < len(changes); i++ {
		for j := i; j > 0 && changes[j].Field < changes[j-1].Field; j-- {
			changes[j], changes[j-1] = changes[j-1], changes[j]
		}
	}
}
