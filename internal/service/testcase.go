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

// TestCaseService 负责测试用例的维护与评审。
//
// 这个实体有三处值得先说清的实现约定：
//
//  1. **两个正交维度**。评审阶段（stage）走状态机，可执行性（status）是普通字段。
//     评审结论必须走动作接口才能改，编辑接口改不了它 —— 否则任何人都能绕过
//     评审把用例标成「已确认」。
//  2. **编辑会把已确认的用例打回待评审**，同时 Version +1。用例内容变了，
//     当初那份「覆盖足够、步骤可执行」的结论就不该继续生效。这条规则写在
//     Update 里而不是让用户手工再点一次「提交评审」，是因为漏点的可能性太高。
//  3. **步骤整体替换**。顺序是步骤语义的一部分（第 3 步依赖第 2 步的结果），
//     做增量更新要额外表达「插到第几位」，反而更容易写错。
type TestCaseService struct {
	db       *gorm.DB
	cases    *repository.TestCaseRepo
	steps    *repository.TestCaseStepRepo
	products *repository.ProductRepo
	modules  *repository.ModuleRepo
	stories  *repository.StoryRepo
	users    *repository.UserRepo
	files    *repository.FileRepo
	comments *repository.CommentRepo

	gate *Gate

	recorder *history.Recorder
}

// NewTestCaseService 构造用例服务。
func NewTestCaseService(
	db *gorm.DB,
	cases *repository.TestCaseRepo,
	steps *repository.TestCaseStepRepo,
	products *repository.ProductRepo,
	modules *repository.ModuleRepo,
	stories *repository.StoryRepo,
	users *repository.UserRepo,
	files *repository.FileRepo,
	comments *repository.CommentRepo,
	gate *Gate,
) *TestCaseService {
	return &TestCaseService{
		db: db, cases: cases, steps: steps, products: products, modules: modules,
		stories: stories, users: users, files: files, comments: comments,
		gate: gate,
		// 翻译器由各域自己提供：只有它知道 stage 该用哪台状态机、
		// type 该查哪张标签表。
		recorder: history.New(CaseLabeler{}),
	}
}

// List 分页查询用例。
//
// 可见性口径与缺陷一致：只按产品判（用例只挂在产品上）。
func (s *TestCaseService) List(ctx context.Context, req dto.TestCaseFilterReq) (*pagination.Result[dto.TestCaseResp], error) {
	visibleProducts, err := s.gate.VisibleProducts(ctx)
	if err != nil {
		return nil, err
	}

	filter := repository.TestCaseFilter{
		ProductID:  req.ProductID,
		StoryID:    req.StoryID,
		Stage:      req.Stage,
		Status:     req.Status,
		Type:       req.Type,
		Pri:        req.Pri,
		OpenedBy:   req.OpenedBy,
		ReviewedBy: req.ReviewedBy,
		Keywords:   req.Keywords,
		ProductIDs: visibleProducts,
		Pagination: pagination.Params{Page: req.Page, Size: req.Size, Order: req.Order},
	}
	// ModuleID 用指针传下去：不传是「不过滤」，传 0 是「未分类」，
	// 这两种在列表上是完全不同的筛选条件。
	if req.ModuleID > 0 {
		moduleID := req.ModuleID
		filter.ModuleID = &moduleID
	}

	result, err := s.cases.List(ctx, filter)
	if err != nil {
		return nil, err
	}
	return s.decorate(ctx, result)
}

// Get 取用例详情（含步骤全集）。
func (s *TestCaseService) Get(ctx context.Context, id uint) (*dto.TestCaseDetailResp, error) {
	testCase, err := s.cases.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := s.assertVisible(ctx, testCase); err != nil {
		return nil, err
	}

	list, err := s.decorate(ctx, &pagination.Result[model.TestCase]{
		List: []model.TestCase{*testCase}, Total: 1, Page: 1, Size: 1,
	})
	if err != nil {
		return nil, err
	}

	steps, err := s.steps.ListByCase(ctx, id)
	if err != nil {
		return nil, err
	}

	out := &dto.TestCaseDetailResp{
		TestCaseResp: list.List[0],
		Steps:        toStepResps(steps),
	}
	files, err := s.files.ListByObject(ctx, model.ObjectTypeCase, id)
	if err != nil {
		return nil, err
	}
	out.Files = toFileRespList(files)
	return out, nil
}

// Create 新建用例。
func (s *TestCaseService) Create(ctx context.Context, req dto.TestCaseReq) (*dto.TestCaseResp, error) {
	if err := s.assertTarget(ctx, req.ProductID, req.ModuleID, req.StoryID); err != nil {
		return nil, err
	}
	steps, err := normalizeSteps(req.Steps)
	if err != nil {
		return nil, err
	}

	caseType := defaultString(req.Type, model.TestCaseTypeFeature)
	caseStatus := defaultString(req.Status, model.TestCaseStatusNormal)
	if err := assertCaseType(caseType); err != nil {
		return nil, err
	}
	if err := assertCaseStatus(caseStatus); err != nil {
		return nil, err
	}

	// 记下溯源需求的当前版本：之后需求变了，这条用例针对的可能已不是同一份描述。
	story, err := s.stories.FindByID(ctx, req.StoryID)
	if err != nil {
		return nil, err
	}
	storyVersion := 0
	if story != nil {
		storyVersion = story.Version
	}

	actor := currentUserID(ctx)
	now := time.Now().UTC()

	testCase := &model.TestCase{
		ProductID: req.ProductID, ModuleID: req.ModuleID,
		StoryID: req.StoryID, StoryVersion: storyVersion,

		Title:        strings.TrimSpace(req.Title),
		Precondition: req.Precondition,
		Keywords:     req.Keywords,
		Desc:         req.Desc,
		Type:         caseType,
		Pri:          defaultLevel(req.Pri),
		Stage:        model.TestCaseStageWait,
		Status:       caseStatus,
		// 第一版就是 1：Version 表达的是「这是第几版内容」，不是「被改了几次」。
		Version: 1,

		OpenedBy: actor, OpenedDate: &now,
		Order: req.Order,
	}

	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := s.cases.Create(ctx, tx, testCase); err != nil {
			return err
		}
		if err := s.steps.Replace(ctx, tx, testCase.ID, steps); err != nil {
			return err
		}
		return s.record(ctx, tx, testCase, history.Entry{
			ObjectType: model.ObjectTypeCase,
			ObjectID:   testCase.ID,
			ProductID:  testCase.ProductID,
			ActorID:    actor,
			Action:     model.ActionOpened,
			// 新建只记标题：版本从 1 开始不是「变化」，写进时间线只是噪音。
			Changes: []history.Change{{Field: "title", New: testCase.Title}},
		})
	})
	if err != nil {
		return nil, err
	}

	return s.getResp(ctx, testCase.ID)
}

// Update 编辑用例内容。
//
// 已确认（confirmed）的用例被改动后打回待评审，并 Version +1：
// 评审针对的是那份内容，内容换了结论不能留着。
func (s *TestCaseService) Update(ctx context.Context, id uint, req dto.TestCaseReq) (*dto.TestCaseResp, error) {
	testCase, err := s.cases.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := s.assertVisible(ctx, testCase); err != nil {
		return nil, err
	}
	// reviewing 状态下不许编辑（要先撤回评审），规则来自状态机而不是这里。
	if _, statusErr := workflow.TestCaseMachine.Resolve("edit", testCase.Stage); statusErr != nil {
		return nil, workflow.TestCaseMachine.DescribeError(statusErr)
	}
	if err := s.assertTarget(ctx, req.ProductID, req.ModuleID, req.StoryID); err != nil {
		return nil, err
	}
	steps, err := normalizeSteps(req.Steps)
	if err != nil {
		return nil, err
	}

	caseType := defaultString(req.Type, model.TestCaseTypeFeature)
	caseStatus := defaultString(req.Status, testCase.Status)
	if err := assertCaseType(caseType); err != nil {
		return nil, err
	}
	if err := assertCaseStatus(caseStatus); err != nil {
		return nil, err
	}

	story, err := s.stories.FindByID(ctx, req.StoryID)
	if err != nil {
		return nil, err
	}
	storyVersion := 0
	if story != nil {
		storyVersion = story.Version
	}

	title := strings.TrimSpace(req.Title)

	// 内容真的变了才加版本、才打回评审：只调整排序或原样提交一次，
	// 不该让用例凭空多一版并把评审结论抹掉。
	contentChanged := testCase.Title != title ||
		testCase.Precondition != req.Precondition ||
		testCase.Desc != req.Desc ||
		testCase.Type != caseType ||
		testCase.StoryID != req.StoryID ||
		testCase.ModuleID != req.ModuleID ||
		s.stepsChanged(ctx, id, steps)

	version := testCase.Version
	stage := testCase.Stage
	if contentChanged {
		version++
		if stage == model.TestCaseStageConfirmed {
			stage = model.TestCaseStageWait
		}
	}

	actor := currentUserID(ctx)
	now := time.Now().UTC()
	fields := map[string]any{
		"product_id": req.ProductID, "module_id": req.ModuleID,
		"story_id": req.StoryID, "story_version": storyVersion,
		"title": title, "precondition": req.Precondition,
		"keywords": req.Keywords, "desc": req.Desc,
		"type": caseType, "pri": defaultLevel(req.Pri), "status": caseStatus,
		"version": version, "stage": stage, "order": req.Order,
		"last_edited_by": actor, "last_edited_date": &now,
	}
	// 时间线只收 caseFieldLabels 认得的字段：product_id 这类结构字段和
	// last_edited_* 这类审计字段会被跳过，不该刷屏。
	changes := caseDiff(testCase, fields)

	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := s.cases.UpdateFields(ctx, tx, id, fields); err != nil {
			return err
		}
		// 步骤始终整体替换：即便上面的标量字段判定为「没变」，
		// 重排顺序也算变更，而顺序差异不体现在那些字段里。
		if err := s.steps.Replace(ctx, tx, id, steps); err != nil {
			return err
		}
		return s.record(ctx, tx, testCase, history.Entry{
			ObjectType: model.ObjectTypeCase,
			ObjectID:   id,
			ProductID:  req.ProductID,
			ActorID:    actor,
			Action:     model.ActionEdited,
			Changes:    changes,
		})
	})
	if err != nil {
		return nil, err
	}

	return s.getResp(ctx, id)
}

// Delete 删除用例（软删），并一并删除其步骤。
func (s *TestCaseService) Delete(ctx context.Context, id uint) error {
	testCase, err := s.cases.GetByID(ctx, id)
	if err != nil {
		return err
	}
	if err := s.assertVisible(ctx, testCase); err != nil {
		return err
	}

	actor := currentUserID(ctx)
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := s.record(ctx, tx, testCase, history.Entry{
			ObjectType: model.ObjectTypeCase,
			ObjectID:   id,
			ProductID:  testCase.ProductID,
			ActorID:    actor,
			Action:     model.ActionDeleted,
		}); err != nil {
			return err
		}
		// 步骤跟着删：留下孤儿子步骤不只是脏数据，
		// 还会让「按用例统计步骤数」把已删用例算进去。
		if err := s.steps.DeleteByCase(ctx, tx, id); err != nil {
			return err
		}
		return s.cases.Delete(ctx, tx, id)
	})
}

// Apply 执行一个用例状态动作（submitreview / recall / pass / reject）。
//
// 合法性全部来自 workflow.TestCaseMachine：这里只写「变成那个状态要动哪些列」。
func (s *TestCaseService) Apply(ctx context.Context, id uint, action, comment string) (*dto.TestCaseResp, error) {
	testCase, err := s.cases.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := s.assertVisible(ctx, testCase); err != nil {
		return nil, err
	}

	trans, statusErr := workflow.TestCaseMachine.Resolve(action, testCase.Stage)
	if statusErr != nil {
		return nil, workflow.TestCaseMachine.DescribeError(statusErr)
	}

	actor := currentUserID(ctx)
	now := time.Now().UTC()

	fields := map[string]any{"stage": trans.To}
	// 只有出结论的动作才记评审人；提交/撤回改的是阶段，不是结论。
	switch trans.Action {
	case "pass", "reject":
		fields["reviewed_by"] = actor
		fields["reviewed_date"] = &now
	}

	// 这里比的是原始值（wait/confirmed 这样的枚举串），可读文本由
	// CaseLabeler 在写入时翻译 —— 与任务/缺陷同一套路。
	changes := caseDiff(testCase, fields)

	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := s.cases.UpdateFields(ctx, tx, id, fields); err != nil {
			return err
		}
		return s.record(ctx, tx, testCase, history.Entry{
			ObjectType: model.ObjectTypeCase,
			ObjectID:   id,
			ProductID:  testCase.ProductID,
			ActorID:    actor,
			// Extra 用 "old|new"：与需求/缺陷同一口径，时间线上可直接还原流转。
			Extra:   workflow.StatusChangeExtra(testCase.Stage, trans.To),
			Action:  workflow.TestCaseMachine.LogActionOf(action),
			Comment: comment,
			Changes: changes,
		})
	})
	if err != nil {
		return nil, err
	}

	return s.getResp(ctx, id)
}

// ---------------------------------------------------------------------------
// 内部实现
// ---------------------------------------------------------------------------

// assertTarget 校验用例挂靠的产品/模块/需求三者成立，且当前用户看得到该产品。
//
// 三者的关系必须显式校验，不能只靠外键存在：模块属于别的产品、需求来自别的产品
// 都是合法 id 但语义错误的组合，写进去之后「按产品筛用例」会筛出别人的东西。
func (s *TestCaseService) assertTarget(ctx context.Context, productID, moduleID, storyID uint) error {
	if productID == 0 {
		return apperr.Newf(apperr.InvalidParam, "用例必须归属一个产品")
	}
	product, err := s.products.FindByID(ctx, productID)
	if err != nil {
		return err
	}
	if product == nil {
		return apperr.New(apperr.ProductNotFound)
	}
	if err := s.gate.AssertProductVisible(ctx, productID); err != nil {
		return err
	}
	if product.Status == model.ProductStatusClosed {
		return apperr.Newf(apperr.ProductClosed, "产品已关闭，不能再维护用例")
	}

	if moduleID > 0 {
		module, err := s.modules.FindByID(ctx, moduleID)
		if err != nil {
			return err
		}
		// 既要根对得上（同一产品），也要树对得上（用例树）：
		// 同一产品下需求/任务/缺陷/用例是四棵独立的树，
		// 只看 root 会把需求节点挂到用例上，两边的模块树都会显示错乱。
		if module == nil || module.Root != productID || module.Type != model.ModuleTypeCase {
			return apperr.Newf(apperr.ModuleWrongRoot, "所选模块不属于该产品的用例模块树")
		}
	}

	if storyID > 0 {
		story, err := s.stories.FindByID(ctx, storyID)
		if err != nil {
			return err
		}
		if story == nil || story.ProductID != productID {
			return apperr.Newf(apperr.StoryNotFound, "所选需求不存在或不属于该产品")
		}
	}
	return nil
}

// normalizeSteps 把请求里的步骤整理成模型行。
//
// 空步骤直接拒绝：没有步骤的用例无法执行，也无法回答「断在第几步」。
// Order 由后端按提交顺序重排为 1..n —— 前端传来的序号可能重复或跳号，
// 而步骤顺序是语义的一部分，不能把这种不确定带进库。
func normalizeSteps(req []dto.TestCaseStepReq) ([]model.TestCaseStep, error) {
	if len(req) == 0 {
		return nil, apperr.New(apperr.TestCaseStepRequired)
	}
	out := make([]model.TestCaseStep, 0, len(req))
	for i, step := range req {
		text := strings.TrimSpace(step.Step)
		if text == "" {
			return nil, apperr.Newf(apperr.InvalidParam, "第 %d 步的操作内容不能为空", i+1)
		}
		out = append(out, model.TestCaseStep{
			Order:  i + 1,
			Step:   text,
			Expect: strings.TrimSpace(step.Expect),
		})
	}
	return out, nil
}

// stepsChanged 比较步骤是否与库里一致（只看顺序与文本，不比 id）。
func (s *TestCaseService) stepsChanged(ctx context.Context, caseID uint, incoming []model.TestCaseStep) bool {
	current, err := s.steps.ListByCase(ctx, caseID)
	if err != nil || len(current) != len(incoming) {
		return true
	}
	for i := range current {
		if current[i].Step != incoming[i].Step || current[i].Expect != incoming[i].Expect {
			return true
		}
	}
	return false
}

// assertVisible 校验当前用户能看到这条用例（按所属产品的可见性）。
func (s *TestCaseService) assertVisible(ctx context.Context, c *model.TestCase) error {
	return s.gate.AssertProductVisible(ctx, c.ProductID)
}

// record 写入一条操作历史。
func (s *TestCaseService) record(ctx context.Context, tx *gorm.DB, c *model.TestCase, e history.Entry) error {
	if e.Changes = fillUserNames(ctx, s.users, e.Changes); len(e.Changes) == 0 {
		e.Changes = nil
	}
	_, err := s.recorder.Record(ctx, tx, e)
	return err
}

// getResp 返回不含步骤的用例出参（写接口的返回体）。
func (s *TestCaseService) getResp(ctx context.Context, id uint) (*dto.TestCaseResp, error) {
	detail, err := s.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	return &detail.TestCaseResp, nil
}

// assertCaseType / assertCaseStatus 在服务层再挡一道取值。
//
// DTO 上的 oneof 只覆盖 HTTP 那条路；从别的入口调用服务时，
// 没登记过的取值会一路写进库，最后在前端显示成一个空白标签。
func assertCaseType(v string) error {
	if !slices.Contains(model.TestCaseTypes, v) {
		return apperr.Newf(apperr.InvalidParam, "用例类型取值不合法：%s", v)
	}
	return nil
}

func assertCaseStatus(v string) error {
	if !slices.Contains(model.TestCaseStatuses, v) {
		return apperr.Newf(apperr.InvalidParam, "用例可执行性取值不合法：%s", v)
	}
	return nil
}

func toStepResps(steps []model.TestCaseStep) []dto.TestCaseStepResp {
	out := make([]dto.TestCaseStepResp, 0, len(steps))
	for _, st := range steps {
		out = append(out, dto.TestCaseStepResp{
			ID: st.ID, Order: st.Order, Step: st.Step, Expect: st.Expect,
		})
	}
	return out
}

// decorate 把模型行整理成出参，并批量补齐展示字段。
//
// 名称类字段（产品名、模块名、需求标题与版本、经办人姓名）一律批量查，
// 不在循环里逐行查 —— 列表 20 行逐行补全会变成上百次查询。
func (s *TestCaseService) decorate(ctx context.Context, result *pagination.Result[model.TestCase]) (*pagination.Result[dto.TestCaseResp], error) {
	cases := result.List
	if len(cases) == 0 {
		return &pagination.Result[dto.TestCaseResp]{
			List: []dto.TestCaseResp{}, Total: result.Total, Page: result.Page, Size: result.Size,
		}, nil
	}

	caseIDs := make([]uint, 0, len(cases))
	productIDs := make([]uint, 0, len(cases))
	moduleIDs := make([]uint, 0, len(cases))
	storyIDs := make([]uint, 0, len(cases))
	userIDs := make([]uint, 0, len(cases)*3)
	for i := range cases {
		c := &cases[i]
		caseIDs = append(caseIDs, c.ID)
		productIDs = append(productIDs, c.ProductID)
		moduleIDs = append(moduleIDs, c.ModuleID)
		storyIDs = append(storyIDs, c.StoryID)
		userIDs = append(userIDs, c.OpenedBy, c.ReviewedBy, c.LastEditedBy)
	}

	products, err := s.products.NameMap(ctx, productIDs)
	if err != nil {
		return nil, err
	}
	modules, err := s.modules.NameMap(ctx, moduleIDs)
	if err != nil {
		return nil, err
	}
	users, err := s.users.NameMap(ctx, userIDs)
	if err != nil {
		return nil, err
	}
	// 需求一次查完，标题与当前版本都从这份结果里取：
	// 「版本对不对得上」因此不需要额外查询，而它正是复核提示的依据。
	storyRows, err := s.stories.ListByIDs(ctx, storyIDs)
	if err != nil {
		return nil, err
	}
	storyTitles := make(map[uint]string, len(storyRows))
	storyVersions := make(map[uint]int, len(storyRows))
	for i := range storyRows {
		storyTitles[storyRows[i].ID] = storyRows[i].Title
		storyVersions[storyRows[i].ID] = storyRows[i].Version
	}

	stepGroups, err := s.steps.ListByCases(ctx, caseIDs)
	if err != nil {
		return nil, err
	}
	commentCounts, err := s.comments.CountByObjects(ctx, model.ObjectTypeCase, caseIDs)
	if err != nil {
		return nil, err
	}
	fileCounts, err := s.caseFileCounts(ctx, caseIDs)
	if err != nil {
		return nil, err
	}

	// 按钮可用性只取决于 stage，按 stage 缓存一份，避免每行重算。
	actionsByStage := make(map[string][]dto.TransitionResp, 4)

	out := make([]dto.TestCaseResp, 0, len(cases))
	for i := range cases {
		c := &cases[i]
		resp := dto.TestCaseResp{
			ID: c.ID, Code: "CASE-" + itoa(c.ID),
			ProductID: c.ProductID, ProductName: products[c.ProductID],
			ModuleID: c.ModuleID, ModuleName: modules[c.ModuleID],
			StoryID: c.StoryID, StoryTitle: storyTitles[c.StoryID],
			StoryVersion: c.StoryVersion,

			Title:        c.Title,
			Keywords:     c.Keywords,
			Type:         c.Type,
			Pri:          c.Pri,
			Stage:        c.Stage,
			Status:       c.Status,
			Version:      c.Version,
			StepCount:    len(stepGroups[c.ID]),
			Precondition: c.Precondition,
			Desc:         c.Desc,

			OpenedBy: c.OpenedBy, OpenedByName: users[c.OpenedBy],
			OpenedDate: formatTimeValue(c.OpenedDate),
			ReviewedBy: c.ReviewedBy, ReviewedByName: users[c.ReviewedBy],
			ReviewedDate:   formatTimeValue(c.ReviewedDate),
			LastEditedBy:   c.LastEditedBy,
			LastEditedDate: formatTimeValue(c.LastEditedDate),
			Order:          c.Order,

			CommentCount: commentCounts[c.ID],
			FileCount:    fileCounts[c.ID],
		}
		// 溯源需求已出新版，说明这条用例针对的描述可能不再是那一份。
		if c.StoryID > 0 {
			resp.StoryStale = storyVersions[c.StoryID] != c.StoryVersion
		}

		if cached, ok := actionsByStage[c.Stage]; ok {
			resp.Actions = cached
		} else {
			resp.Actions = availableStateActions(ctx, workflow.TestCaseMachine, c.Stage)
			actionsByStage[c.Stage] = resp.Actions
		}
		out = append(out, resp)
	}

	return &pagination.Result[dto.TestCaseResp]{
		List: out, Total: result.Total, Page: result.Page, Size: result.Size,
	}, nil
}

// caseFileCounts 批量统计用例上的附件数。
func (s *TestCaseService) caseFileCounts(ctx context.Context, caseIDs []uint) (map[uint]int64, error) {
	var rows []struct {
		ObjectID uint
		Total    int64
	}
	err := s.db.WithContext(ctx).Model(&model.File{}).
		Select("object_id, COUNT(*) AS total").
		Where("object_type = ? AND object_id IN ?", model.ObjectTypeCase, caseIDs).
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
