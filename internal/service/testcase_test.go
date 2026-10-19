package service_test

// 测试用例域的回归。
//
// 这里最该盯住的不是「能不能存下来」，而是三件容易悄悄出错的事：
//   1. 评审结论只能由动作产生 —— 编辑接口改不动 stage，否则评审形同虚设；
//   2. 已确认的用例被改动后必须回到待评审并加版本，漏掉这条就会让
//      「通过的结论」指向一份已经不是当初的内容；
//   3. 步骤顺序是后端定的，客户端传来的序号不作数。

import (
	"testing"

	"github.com/xywf221/xuliu/internal/dto"
	"github.com/xywf221/xuliu/internal/model"
	"github.com/xywf221/xuliu/internal/pkg/apperr"
)

// createCase 是建用例的测试助手，失败即终止用例。
func (h *harness) createCase(req dto.TestCaseReq) *dto.TestCaseResp {
	h.t.Helper()
	created, err := h.cases.Create(h.ctx, req)
	if err != nil {
		h.t.Fatalf("建用例失败: %v", err)
	}
	return created
}

// twoSteps 是最小可用的步骤集合。
func twoSteps() []dto.TestCaseStepReq {
	return []dto.TestCaseStepReq{
		{Order: 1, Step: "打开登录页", Expect: "出现账号输入框"},
		{Order: 2, Step: "输入错误口令", Expect: "提示口令不正确"},
	}
}

// TestTestCaseReviewLifecycle 走一遍评审闭环，并检查步骤顺序被后端重排。
func TestTestCaseReviewLifecycle(t *testing.T) {
	h := newHarness(t)
	productID := h.createProduct("用例生命周期产品", "CASELC", model.ACLOpen, nil)

	// 客户端故意把 order 传成 7 / 2 / 9：入库必须按提交顺序变成 1 / 2 / 3。
	created := h.createCase(dto.TestCaseReq{
		ProductID: productID, Title: "登录失败提示",
		Steps: []dto.TestCaseStepReq{
			{Order: 7, Step: "打开登录页", Expect: "有账号框"},
			{Order: 2, Step: "输入错误口令", Expect: "提示不正确"},
			{Order: 9, Step: "连点三次", Expect: "账号被锁"},
		},
	})
	if created.Stage != model.TestCaseStageWait || created.Version != 1 {
		t.Fatalf("新建用例应是 wait/版本 1，实际 %s/%d", created.Stage, created.Version)
	}
	if created.Code != "CASE-"+itoa(created.ID) {
		t.Errorf("编号应为 CASE-%d，实际 %q", created.ID, created.Code)
	}

	detail, err := h.cases.Get(h.ctx, created.ID)
	if err != nil {
		t.Fatalf("查详情失败: %v", err)
	}
	if len(detail.Steps) != 3 {
		t.Fatalf("应有 3 个步骤，实际 %d", len(detail.Steps))
	}
	for i, step := range detail.Steps {
		if step.Order != i+1 {
			t.Errorf("第 %d 步的 order 应为 %d，实际 %d", i+1, i+1, step.Order)
		}
	}

	// 用路由上的名字（带连字符）提交评审：状态机会归一化成 submitreview。
	if _, err := h.cases.Apply(h.ctx, created.ID, "submit-review", "请评审"); err != nil {
		t.Fatalf("提交评审失败: %v", err)
	}
	if stage := h.caseStage(created.ID); stage != model.TestCaseStageReviewing {
		t.Fatalf("提交后应是 reviewing，实际 %s", stage)
	}

	// 评审中不许编辑 —— 否则结论还没出内容就变了。
	if _, err := h.cases.Update(h.ctx, created.ID, dto.TestCaseReq{
		ProductID: productID, Title: "偷偷改标题", Steps: twoSteps(),
	}); errCode(err) != apperr.TestCaseStageInvalid {
		t.Fatalf("评审中编辑应报状态不允许，实际 %v", err)
	}

	// 撤回 → 再提交 → 通过。
	if _, err := h.cases.Apply(h.ctx, created.ID, "recall", "先补个前置条件"); err != nil {
		t.Fatalf("撤回评审失败: %v", err)
	}
	if stage := h.caseStage(created.ID); stage != model.TestCaseStageWait {
		t.Fatalf("撤回后应回到 wait，实际 %s", stage)
	}
	if _, err := h.cases.Apply(h.ctx, created.ID, "submitreview", ""); err != nil {
		t.Fatalf("再次提交评审失败: %v", err)
	}
	passed, err := h.cases.Apply(h.ctx, created.ID, "pass", "覆盖够了")
	if err != nil {
		t.Fatalf("评审通过失败: %v", err)
	}
	if passed.Stage != model.TestCaseStageConfirmed {
		t.Fatalf("通过后应是 confirmed，实际 %s", passed.Stage)
	}
	if passResp, err := h.cases.Get(h.ctx, created.ID); err != nil {
		t.Fatalf("查详情失败: %v", err)
	} else if passResp.ReviewedBy != poUser {
		t.Errorf("评审人应是当前操作者 %d，实际 %d", poUser, passResp.ReviewedBy)
	}

	// 每一步都要留痕，且动作名与状态机里登记的一致。
	for action, want := range map[string]int64{
		model.ActionOpened:       1,
		model.ActionEdited:       0, // 一次内容编辑都没发生
		model.ActionSubmitReview: 2,
		model.ActionRecalled:     1,
		model.ActionReviewed:     1,
	} {
		if got := h.actionCount(model.ObjectTypeCase, created.ID, action); got != want {
			t.Errorf("动作 %s 应有 %d 条历史，实际 %d", action, want, got)
		}
	}

	// 评审结论在时间线上要显示成人话（由 CaseLabeler 翻译）。
	hist, err := h.actions.List(h.ctx, dto.ActionFilterReq{
		ObjectType: model.ObjectTypeCase, ObjectID: created.ID, Size: 50,
	})
	if err != nil {
		t.Fatalf("查操作历史失败: %v", err)
	}
	var labelled bool
	for _, e := range hist.List {
		for _, c := range e.Changes {
			// 断言的是 NewText（可读值）而不是 New（原始枚举串）：
			// 时间线给人看的就是前者，翻译没接上时后者看起来仍然「有值」。
			if c.Field == "stage" && c.NewText == "已确认" {
				labelled = true
			}
		}
	}
	if !labelled {
		t.Errorf("历史里应有一条把 stage 译成「已确认」的变更，实际 %+v", hist.List)
	}
}

// TestTestCaseEditResetsConfirmedReview 验证「改内容就打回评审」。
func TestTestCaseEditResetsConfirmedReview(t *testing.T) {
	h := newHarness(t)
	productID := h.createProduct("用例回归产品", "CASERE", model.ACLOpen, nil)

	created := h.createCase(dto.TestCaseReq{
		ProductID: productID, Title: "原始标题", Type: model.TestCaseTypeFeature,
		Steps: twoSteps(),
	})
	if _, err := h.cases.Apply(h.ctx, created.ID, "submitreview", ""); err != nil {
		t.Fatalf("提交评审失败: %v", err)
	}
	if _, err := h.cases.Apply(h.ctx, created.ID, "pass", ""); err != nil {
		t.Fatalf("评审通过失败: %v", err)
	}

	// 原样提交一次（内容确实没变）：不该加版本，也不该打回评审。
	noop, err := h.cases.Update(h.ctx, created.ID, dto.TestCaseReq{
		ProductID: productID, Title: "原始标题", Type: model.TestCaseTypeFeature,
		Steps: twoSteps(),
	})
	if err != nil {
		t.Fatalf("原样编辑失败: %v", err)
	}
	if noop.Version != 1 || noop.Stage != model.TestCaseStageConfirmed {
		t.Fatalf("内容没变时不应加版本/改阶段，实际 version=%d stage=%s",
			noop.Version, noop.Stage)
	}

	// 改标题：版本 +1，并且打回待评审。
	changed, err := h.cases.Update(h.ctx, created.ID, dto.TestCaseReq{
		ProductID: productID, Title: "改了标题", Type: model.TestCaseTypeFeature,
		Steps: twoSteps(),
	})
	if err != nil {
		t.Fatalf("改标题失败: %v", err)
	}
	if changed.Version != 2 {
		t.Errorf("内容变了版本应加到 2，实际 %d", changed.Version)
	}
	if changed.Stage != model.TestCaseStageWait {
		t.Errorf("已确认的用例被改动后应回到 wait，实际 %s", changed.Stage)
	}

	// 只重排步骤顺序也算变了：顺序是步骤语义的一部分。
	reordered, err := h.cases.Update(h.ctx, created.ID, dto.TestCaseReq{
		ProductID: productID, Title: "改了标题", Type: model.TestCaseTypeFeature,
		Steps: []dto.TestCaseStepReq{
			twoSteps()[1], twoSteps()[0],
		},
	})
	if err != nil {
		t.Fatalf("重排步骤失败: %v", err)
	}
	if reordered.Version != 3 {
		t.Errorf("只重排步骤也应加版本，实际 %d", reordered.Version)
	}
}

// TestTestCaseGuards 覆盖写用例时的几道校验。
func TestTestCaseGuards(t *testing.T) {
	h := newHarness(t)
	productA := h.createProduct("用例产品A", "CASEA", model.ACLOpen, nil)
	productB := h.createProduct("用例产品B", "CASEB", model.ACLOpen, nil)
	storyB := h.createStory(productB, "B 产品的需求", []uint{reviewer})

	// 用例只认「用例树」上的模块：h.createModule 建的是需求树节点，
	// 拿它当用例模块必须被拒（同一个产品下四棵树 id 空间相同、语义不同）。
	storyTreeModule := h.createModule(productA, 0, "需求树节点")
	caseTreeModuleB := h.createCaseModule(productB, "登录模块")

	cases := []struct {
		name string
		req  dto.TestCaseReq
		want apperr.Code
	}{
		{
			name: "没有步骤",
			req:  dto.TestCaseReq{ProductID: productA, Title: "空步骤用例"},
			want: apperr.TestCaseStepRequired,
		},
		{
			name: "步骤文本全空白",
			req: dto.TestCaseReq{ProductID: productA, Title: "空白步骤",
				Steps: []dto.TestCaseStepReq{{Step: "   "}}},
			want: apperr.InvalidParam,
		},
		{
			// 同一产品的需求树节点也不行：模块树按用途分四棵。
			name: "模块不是用例树",
			req: dto.TestCaseReq{ProductID: productA, Title: "错树模块",
				ModuleID: storyTreeModule, Steps: twoSteps()},
			want: apperr.ModuleWrongRoot,
		},
		{
			name: "模块属于别的产品",
			req: dto.TestCaseReq{ProductID: productA, Title: "错产品模块",
				ModuleID: caseTreeModuleB, Steps: twoSteps()},
			want: apperr.ModuleWrongRoot,
		},
		{
			name: "需求属于别的产品",
			req: dto.TestCaseReq{ProductID: productA, Title: "错需求",
				StoryID: storyB, Steps: twoSteps()},
			want: apperr.StoryNotFound,
		},
		{
			name: "类型取值不合法",
			req: dto.TestCaseReq{ProductID: productA, Title: "怪类型",
				Type: "quantum", Steps: twoSteps()},
			want: apperr.InvalidParam,
		},
	}

	for _, tc := range cases {
		if _, err := h.cases.Create(h.ctx, tc.req); errCode(err) != tc.want {
			t.Errorf("%s: 应报 %s，实际 %v", tc.name, tc.want, err)
		}
	}

	// 反向也要成立：用例树上的模块才收得下，否则上面的拒绝只是「全都拒」。
	if _, err := h.cases.Create(h.ctx, dto.TestCaseReq{
		ProductID: productB, Title: "挂在用例树上", ModuleID: caseTreeModuleB,
		Steps: twoSteps(),
	}); err != nil {
		t.Errorf("用例树上的模块应可挂用例，实际 %v", err)
	}
}

// createCaseModule 建一个「用例树」上的模块。
//
// 不能复用 h.createModule：它建的是需求树节点，而用例只认用例树，
// 用它做正向断言会得到与实际代码一致的假阴性。
func (h *harness) createCaseModule(productID uint, name string) uint {
	h.t.Helper()
	module, err := h.modules.Create(h.ctx, dto.ModuleReq{
		Root: productID, Type: model.ModuleTypeCase, Name: name,
	})
	if err != nil {
		h.t.Fatalf("建用例模块 %s 失败: %v", name, err)
	}
	return module.ID
}

// TestTestCaseVisibility 验证私有产品下的用例对外人不可见，
// 并且评论功能认得「case」这个对象类型（不认就会直接被网关拒掉）。
func TestTestCaseVisibility(t *testing.T) {
	h := newHarness(t)
	privateID := h.createProduct("私有用例产品", "CASEPR", model.ACLPrivate, nil)
	openID := h.createProduct("公开用例产品", "CASEOP", model.ACLOpen, nil)

	hidden := h.createCase(dto.TestCaseReq{ProductID: privateID, Title: "私有用例", Steps: twoSteps()})
	h.createCase(dto.TestCaseReq{ProductID: openID, Title: "公开用例", Steps: twoSteps()})

	outsiderCtx := asUser(h.ctx, outsider, false)
	listed, err := h.cases.List(outsiderCtx, dto.TestCaseFilterReq{})
	if err != nil {
		t.Fatalf("路人查用例列表失败: %v", err)
	}
	if listed.Total != 1 || listed.List[0].ProductID != openID {
		t.Fatalf("路人应只看到公开产品的 1 条用例，实际 %d 条", listed.Total)
	}
	if _, err := h.cases.Get(outsiderCtx, hidden.ID); errCode(err) != apperr.ProductAccessDenied {
		t.Fatalf("路人应打不开私有产品的用例，实际 %v", err)
	}

	// 网关必须认识 case：评论/附件/历史都靠它判可见性。
	if _, err := h.gate.Locate(h.ctx, model.ObjectTypeCase, hidden.ID); err != nil {
		t.Fatalf("定位用例失败: %v", err)
	}
	if _, err := h.comments.Create(outsiderCtx, dto.CommentReq{
		ObjectType: model.ObjectTypeCase, ObjectID: hidden.ID, Content: "看得到吗",
	}); errCode(err) != apperr.ProductAccessDenied {
		t.Fatalf("路人给私有产品的用例评论应被拒，实际 %v", err)
	}
	if _, err := h.comments.Create(h.ctx, dto.CommentReq{
		ObjectType: model.ObjectTypeCase, ObjectID: hidden.ID, Content: "产品负责人评论",
	}); err != nil {
		t.Fatalf("产品负责人应能评论自己产品的用例: %v", err)
	}
}

// TestTestCaseDeleteRemovesSteps 验证删用例时步骤不留下孤儿。
func TestTestCaseDeleteRemovesSteps(t *testing.T) {
	h := newHarness(t)
	productID := h.createProduct("用例删除产品", "CASEDL", model.ACLOpen, nil)
	created := h.createCase(dto.TestCaseReq{ProductID: productID, Title: "将被删除", Steps: twoSteps()})

	if err := h.cases.Delete(h.ctx, created.ID); err != nil {
		t.Fatalf("删用例失败: %v", err)
	}
	if _, err := h.cases.Get(h.ctx, created.ID); errCode(err) != apperr.NotFound {
		t.Fatalf("删除后应查不到用例，实际 %v", err)
	}

	var steps int64
	if err := h.db.Model(&model.TestCaseStep{}).
		Where("case_id = ? AND deleted_at IS NULL", created.ID).Count(&steps).Error; err != nil {
		t.Fatalf("查步骤失败: %v", err)
	}
	if steps != 0 {
		t.Errorf("删除用例后不应留下未删的步骤，实际剩 %d 条", steps)
	}
}

// caseStage 直接读库里的评审阶段，绕过出参装配，用于断言状态确实落库。
func (h *harness) caseStage(id uint) string {
	h.t.Helper()
	var stage string
	if err := h.db.Model(&model.TestCase{}).Where("id = ?", id).
		Select("stage").Scan(&stage).Error; err != nil {
		h.t.Fatalf("查 stage 失败: %v", err)
	}
	return stage
}
