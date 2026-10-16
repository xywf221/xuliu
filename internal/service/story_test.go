package service_test

import (
	"strconv"
	"testing"

	"github.com/xywf221/xuliu/internal/dto"
	"github.com/xywf221/xuliu/internal/model"
	"github.com/xywf221/xuliu/internal/pkg/apperr"
	"github.com/xywf221/xuliu/internal/service"
)

// productReq 拼一个测试用的产品请求。
func productReq(name, code, aclMode string, reviewers []uint) dto.ProductReq {
	return dto.ProductReq{
		Name: name, Code: code,
		ACL:      aclMode,
		Reviewer: reviewers,
		// 负责人必须写上：私有产品的可见性判定里，三位负责人天然可见。
		// 少了它，连建产品的人都打不开自己建的产品。
		PO: poUser,
	}
}

// storyReq 拼一个测试用的建需求请求。
func storyReq(productID uint, title string, reviewers []uint, spec, verify string) dto.CreateStoryReq {
	return dto.CreateStoryReq{
		ProductID: productID,
		Title:     title,
		Spec:      spec,
		Verify:    verify,
		Reviewers: reviewers,
	}
}

// pendingReview 是「提交评审」的请求。
func pendingReview() service.ActionInput {
	return service.ActionInput{Action: "submitreview"}
}

// ErrCode 取错误的错误码，非应用错误时返回空串。
func errCode(err error) apperr.Code {
	if err == nil {
		return ""
	}
	return apperr.As(err).Code()
}

// TestStoryReviewFlow 走一遍「草稿 → 提交评审 → 评审通过 → 激活」。
func TestStoryReviewFlow(t *testing.T) {
	h := newHarness(t)
	productID := h.createProduct("订单中台", "ORDER", model.ACLOpen, []uint{reviewer})
	storyID := h.createStory(productID, "支持批量导出", nil)

	// 产品开启了评审，新建的需求应当停在草稿。
	story, err := h.stories.Get(h.ctx, storyID)
	if err != nil {
		t.Fatalf("取需求失败: %v", err)
	}
	if story.Status != model.StoryStatusDraft {
		t.Fatalf("开启评审时新需求应为草稿，实际 %s", story.Status)
	}
	if story.Version != 1 {
		t.Fatalf("初始版本应为 1，实际 %d", story.Version)
	}

	// 评审行是「提交评审」时才建的：提交之前只有「打算让谁评」，
	// 没有「谁评过了」。
	if len(story.Reviews) != 0 {
		t.Fatalf("提交评审前不应有评审记录，实际 %+v", story.Reviews)
	}

	submitted, err := h.stories.Apply(h.ctx, storyID, pendingReview())
	if err != nil {
		t.Fatalf("提交评审失败: %v", err)
	}
	if submitted.Status != model.StoryStatusReviewing {
		t.Fatalf("提交评审后应为评审中，实际 %s", submitted.Status)
	}
	// 评审人留空时回落到了产品的默认评审人。
	if len(submitted.Reviews) != 1 || submitted.Reviews[0].Reviewer != reviewer {
		t.Fatalf("应按产品默认评审人建好评审行，实际 %+v", submitted.Reviews)
	}
	if submitted.Reviews[0].Result != "" {
		t.Errorf("刚提交时结论应为空，实际 %q", submitted.Reviews[0].Result)
	}

	// 换评审人身份给出结论。
	ctx := asUser(h.ctx, reviewer, true)
	result, err := h.stories.Apply(ctx, storyID, service.ActionInput{
		Action: "pass", Result: model.ReviewPass, Comment: "没问题",
	})
	if err != nil {
		t.Fatalf("评审通过失败: %v", err)
	}
	if result.Status != model.StoryStatusActive {
		t.Fatalf("评审通过后应为激活，实际 %s", result.Status)
	}
	if result.Reviews[0].Result != model.ReviewPass {
		t.Fatalf("应记下本人结论，实际 %+v", result.Reviews[0])
	}

	// 每一步都要留痕。
	if n := h.actionCount(model.ObjectTypeStory, storyID, model.ActionSubmitReview); n != 1 {
		t.Errorf("应有 1 条「提交评审」历史，实际 %d", n)
	}
	if n := h.actionCount(model.ObjectTypeStory, storyID, model.ActionReviewed); n != 1 {
		t.Errorf("应有 1 条「评审」历史，实际 %d", n)
	}
	if n := h.actionCount(model.ObjectTypeStory, storyID, model.ActionOpened); n != 1 {
		t.Errorf("应有 1 条「创建」历史，实际 %d", n)
	}
}

// TestChangeBumpsVersionAndRevertRollsItBack 覆盖需求版本化里最微妙的一对操作。
//
// 禅道的 revert（撤销变更）语义是「版本 −1，并删掉最新那版 spec 与 review」。
// 这两件事必须同进同退，否则会出现「版本回到 1 但版本 2 的快照还在」的
// 幽灵数据，逐版对比时会看到一段不存在的内容。
func TestChangeBumpsVersionAndRevertRollsItBack(t *testing.T) {
	h := newHarness(t)
	productID := h.createProduct("订单中台", "ORDER", model.ACLOpen, []uint{reviewer})
	storyID := h.createStory(productID, "支持批量导出", []uint{reviewer})

	// 先推到激活态，因为只有激活的需求才能变更。
	if _, err := h.stories.Apply(h.ctx, storyID, pendingReview()); err != nil {
		t.Fatalf("提交评审失败: %v", err)
	}
	if _, err := h.stories.Apply(asUser(h.ctx, reviewer, true), storyID, service.ActionInput{
		Action: "pass", Result: model.ReviewPass,
	}); err != nil {
		t.Fatalf("评审通过失败: %v", err)
	}
	if got := h.specCount(storyID); got != 1 {
		t.Fatalf("初始应有 1 条版本快照，实际 %d", got)
	}

	// 变更：版本 +1，新增一条快照。
	changed, err := h.stories.Apply(h.ctx, storyID, service.ActionInput{
		Action: "change", Version: 1,
		Title: "支持批量导出（含 CSV）", Spec: "新描述", Verify: "新验收",
		Reason: "客户要求支持 CSV 格式",
	})
	if err != nil {
		t.Fatalf("需求变更失败: %v", err)
	}
	if changed.Version != 2 {
		t.Fatalf("变更后版本应为 2，实际 %d", changed.Version)
	}
	if changed.Status != model.StoryStatusChanging {
		t.Fatalf("变更后状态应为变更中，实际 %s", changed.Status)
	}
	if !changed.StoryChanged {
		t.Error("变更后应标记 storyChanged，下游据此提示「需求已变更」")
	}
	if got := h.specCount(storyID); got != 2 {
		t.Fatalf("变更后应有 2 条版本快照，实际 %d", got)
	}

	// 送审后撤销变更：版本回退，最新那版快照消失。
	if _, err := h.stories.Apply(h.ctx, storyID, pendingReview()); err != nil {
		t.Fatalf("变更后提交评审失败: %v", err)
	}
	if _, err := h.stories.Apply(asUser(h.ctx, reviewer, true), storyID, service.ActionInput{
		Action: "revert", Result: model.ReviewRevert, Comment: "这个改动先不做",
	}); err != nil {
		t.Fatalf("撤销变更失败: %v", err)
	}

	story := h.mustStory(storyID)
	if story.Version != 1 {
		t.Errorf("撤销变更后版本应回到 1，实际 %d", story.Version)
	}
	if story.Status != model.StoryStatusActive {
		t.Errorf("撤销变更后应回到激活态，实际 %s", story.Status)
	}
	if story.StoryChanged {
		t.Error("撤销变更后不应再标记 storyChanged")
	}
	if got := h.specCount(storyID); got != 1 {
		t.Errorf("撤销变更后应只剩 1 条版本快照，实际 %d", got)
	}

	// 撤销后当前版本的内容应当是原始那版，而不是被作废的新内容。
	detail, err := h.stories.Get(h.ctx, storyID)
	if err != nil {
		t.Fatalf("取需求失败: %v", err)
	}
	if detail.Spec != "描述 支持批量导出" {
		t.Errorf("撤销变更后应回到原内容，实际 %q", detail.Spec)
	}
}

// TestRevertOnFirstVersionIsRejected 验证「没有变更可撤」时给出明确错误，
// 而不是把版本减成 0。
func TestRevertOnFirstVersionIsRejected(t *testing.T) {
	h := newHarness(t)
	productID := h.createProduct("订单中台", "ORDER", model.ACLOpen, []uint{reviewer})
	storyID := h.createStory(productID, "支持批量导出", []uint{reviewer})

	if _, err := h.stories.Apply(h.ctx, storyID, pendingReview()); err != nil {
		t.Fatalf("提交评审失败: %v", err)
	}
	_, err := h.stories.Apply(asUser(h.ctx, reviewer, true), storyID, service.ActionInput{
		Action: "revert", Result: model.ReviewRevert,
	})
	if code := errCode(err); code != apperr.StoryNotChanged {
		t.Fatalf("首版撤销变更应报 story_not_changed，实际 %v", err)
	}
	if got := h.mustStory(storyID).Version; got != 1 {
		t.Errorf("被拒后版本不应变化，实际 %d", got)
	}
}

// TestCloseThenActivateRestoresPreviousStatus 验证「激活」能把需求送回
// 关闭前的状态，而这一信息来自操作历史的 "old|new"。
func TestCloseThenActivateRestoresPreviousStatus(t *testing.T) {
	h := newHarness(t)
	productID := h.createProduct("订单中台", "ORDER", model.ACLOpen, []uint{reviewer})
	storyID := h.createStory(productID, "支持批量导出", nil)

	if got := h.statusOf(storyID); got != model.StoryStatusDraft {
		t.Fatalf("前置条件：应为草稿，实际 %s", got)
	}

	if _, err := h.stories.Apply(h.ctx, storyID, service.ActionInput{
		Action: "close", ClosedReason: model.StoryClosePostponed, Comment: "先放一放",
	}); err != nil {
		t.Fatalf("关闭需求失败: %v", err)
	}
	if got := h.statusOf(storyID); got != model.StoryStatusClosed {
		t.Fatalf("关闭后应为已关闭，实际 %s", got)
	}

	activated, err := h.stories.Apply(h.ctx, storyID, service.ActionInput{Action: "activate"})
	if err != nil {
		t.Fatalf("激活需求失败: %v", err)
	}
	if activated.Status != model.StoryStatusDraft {
		t.Fatalf("激活后应恢复到关闭前的草稿态，实际 %s", activated.Status)
	}
	// 关闭信息要清干净，否则「已激活」的需求还挂着关闭原因。
	row := h.mustStory(storyID)
	if row.ClosedReason != "" || row.ClosedDate != nil {
		t.Errorf("激活后应清空关闭信息，实际 reason=%q date=%v", row.ClosedReason, row.ClosedDate)
	}
	if row.ActivatedDate == nil {
		t.Error("激活应记录激活时间")
	}
	_ = activated
}

// TestCloseDuplicateRequiresValidTarget 验证「重复」这个关闭原因必须指向
// 另一条真实存在的需求。
func TestCloseDuplicateRequiresValidTarget(t *testing.T) {
	h := newHarness(t)
	productID := h.createProduct("订单中台", "ORDER", model.ACLOpen, []uint{reviewer})
	storyID := h.createStory(productID, "重复的那条", nil)

	// 指向自己不合法。
	_, err := h.stories.Apply(h.ctx, storyID, service.ActionInput{
		Action: "close", ClosedReason: model.StoryCloseDuplicate, DuplicateStoryID: storyID,
	})
	if code := errCode(err); code != apperr.StoryDuplicateTargetInvalid {
		t.Fatalf("指向自身应被拒，实际 %v", err)
	}

	// 指向不存在的需求同样不合法。
	_, err = h.stories.Apply(h.ctx, storyID, service.ActionInput{
		Action: "close", ClosedReason: model.StoryCloseDuplicate, DuplicateStoryID: 9999,
	})
	if code := errCode(err); code != apperr.StoryDuplicateTargetInvalid {
		t.Fatalf("指向不存在的需求应被拒，实际 %v", err)
	}

	// 指向真实存在的另一条需求则通过。
	otherID := h.createStory(productID, "被重复的那条", nil)
	if _, err := h.stories.Apply(h.ctx, storyID, service.ActionInput{
		Action: "close", ClosedReason: model.StoryCloseDuplicate, DuplicateStoryID: otherID,
	}); err != nil {
		t.Fatalf("指向真实需求应成功，实际 %v", err)
	}
	if got := h.mustStory(storyID).DuplicateStoryID; got != otherID {
		t.Errorf("应记录重复目标，实际 %d", got)
	}
}

// TestVersionConflictWhenContentChangedUnderneath 验证「手里的内容已经过时」
// 这件事会被挡住。
//
// 版本号只在「需求变更」时增加，所以这道锁防的不是「两个人同时改标题」——
// 那件事版本号根本不参与——而是：甲打开编辑框时是 v1，乙期间提交了一次正式
// 变更（v1→v2），甲此时提交会把乙刚写进去的内容覆盖掉。这必须被拒绝。
func TestVersionConflictWhenContentChangedUnderneath(t *testing.T) {
	h := newHarness(t)
	productID := h.createProduct("订单中台", "ORDER", model.ACLOpen, []uint{reviewer})
	storyID := h.createStory(productID, "支持批量导出", []uint{reviewer})

	// 推激活态：只有激活的需求才能发起变更。
	if _, err := h.stories.Apply(h.ctx, storyID, pendingReview()); err != nil {
		t.Fatalf("提交评审失败: %v", err)
	}
	if _, err := h.stories.Apply(asUser(h.ctx, reviewer, true), storyID, service.ActionInput{
		Action: "pass", Result: model.ReviewPass,
	}); err != nil {
		t.Fatalf("评审通过失败: %v", err)
	}

	// 乙提交变更：版本 1 → 2。
	if _, err := h.stories.Apply(h.ctx, storyID, service.ActionInput{
		Action: "change", Version: 1, Title: "支持批量导出（乙改）",
		Spec: "乙的描述", Reason: "客户要求",
	}); err != nil {
		t.Fatalf("变更失败: %v", err)
	}
	if got := h.mustStory(storyID).Version; got != 2 {
		t.Fatalf("变更后应为版本 2，实际 %d", got)
	}

	// 甲手上还是版本 1，提交编辑应当被拒。
	stale := "支持批量导出（甲改）"
	_, err := h.stories.Update(h.ctx, storyID, dto.UpdateStoryReq{Version: 1, Title: &stale})
	if code := errCode(err); code != apperr.StoryVersionConflict {
		t.Fatalf("过期版本应报 story_version_conflict，实际 %v", err)
	}
	if got := h.mustStory(storyID).Title; got != "支持批量导出（乙改）" {
		t.Errorf("被拒的修改不应落库，实际标题 %q", got)
	}

	// 刷新拿到最新版本号后就能正常提交。
	if _, err := h.stories.Update(h.ctx, storyID, dto.UpdateStoryReq{
		Version: h.mustStory(storyID).Version, Title: &stale,
	}); err != nil {
		t.Fatalf("用最新版本号编辑应成功: %v", err)
	}
	if got := h.mustStory(storyID).Title; got != stale {
		t.Errorf("编辑应生效，实际标题 %q", got)
	}
}

// TestPlainEditDoesNotBumpVersion 把「编辑不加版本号」这条语义钉住。
//
// 它不是一个实现细节：前端的「逐版对比」完全建立在这条约定上，
// 一旦普通编辑也开始加版本，历史里会塞满没有意义的版本。
func TestPlainEditDoesNotBumpVersion(t *testing.T) {
	h := newHarness(t)
	productID := h.createProduct("订单中台", "ORDER", model.ACLOpen, []uint{reviewer})
	storyID := h.createStory(productID, "支持批量导出", nil)

	title := "改个标题"
	if _, err := h.stories.Update(h.ctx, storyID, dto.UpdateStoryReq{Version: 1, Title: &title}); err != nil {
		t.Fatalf("编辑失败: %v", err)
	}
	if got := h.mustStory(storyID).Version; got != 1 {
		t.Errorf("普通编辑不应增加版本号，实际 %d", got)
	}
	if got := h.specCount(storyID); got != 1 {
		t.Errorf("普通编辑不应新增版本快照，实际 %d", got)
	}
}

// TestProductWithoutReviewActivatesImmediately 验证关掉评审的产品，
// 新建需求直接进入激活态。
func TestProductWithoutReviewActivatesImmediately(t *testing.T) {
	h := newHarness(t)
	noReview := false
	product, err := h.products.Create(h.ctx, dto.ProductReq{
		Name: "内部工具", Code: "TOOL", ACL: model.ACLOpen, ReviewStory: &noReview,
	})
	if err != nil {
		t.Fatalf("建产品失败: %v", err)
	}
	// 没有评审人也不该报错：这条链路根本不需要评审。
	storyID := h.createStory(product.ID, "导入日志清理", nil)

	story := h.mustStory(storyID)
	if story.Status != model.StoryStatusActive {
		t.Fatalf("关闭评审时新需求应直接激活，实际 %s", story.Status)
	}
	if story.ActivatedDate == nil {
		t.Error("直接激活的需求应记录激活时间")
	}
}

// TestPrivateProductHidesStoryFromOutsider 验证私有产品的数据级隔离。
//
// 这一层**超管也要受约束**：私有产品的语义就是「没被加入就看不到」，
// 所以这里连超管身份都挡，否则「私有」形同虚设。
func TestPrivateProductHidesStoryFromOutsider(t *testing.T) {
	h := newHarness(t)
	productID := h.createProduct("内部机密产品", "SECRET", model.ACLPrivate, []uint{reviewer})
	storyID := h.createStory(productID, "不能外泄的需求", nil)

	// 拥有者本人可见。
	if _, err := h.stories.Get(h.ctx, storyID); err != nil {
		t.Fatalf("产品负责人应能访问: %v", err)
	}

	// 超管但不属于白名单 —— 同样看不到。
	outsiderCtx := asUser(h.ctx, outsider, true)
	_, err := h.stories.Get(outsiderCtx, storyID)
	if code := errCode(err); code != apperr.ProductAccessDenied {
		t.Fatalf("私有产品对无关用户应报 product_access_denied，实际 %v", err)
	}

	// 列表里也不该出现。
	result, err := h.stories.List(outsiderCtx, dto.StoryFilterReq{})
	if err != nil {
		t.Fatalf("列表查询不应报错: %v", err)
	}
	if len(result.List) != 0 {
		t.Errorf("私有产品的需求不应出现在无关用户的列表里，实际 %d 条", len(result.List))
	}
}

// TestCommentRequiresObjectVisibility 验证评论不能成为绕过产品隔离的旁路。
func TestCommentRequiresObjectVisibility(t *testing.T) {
	h := newHarness(t)
	productID := h.createProduct("内部机密产品", "SECRET", model.ACLPrivate, []uint{reviewer})
	storyID := h.createStory(productID, "不能外泄的需求", nil)

	outsiderCtx := asUser(h.ctx, outsider, true)
	_, err := h.comments.Create(outsiderCtx, dto.CommentReq{
		ObjectType: model.ObjectTypeStory, ObjectID: storyID, Content: "偷看",
	})
	if code := errCode(err); code != apperr.ProductAccessDenied {
		t.Fatalf("对不可见对象评论应报 product_access_denied，实际 %v", err)
	}

	// 可见用户评论成功，并且同时进操作历史。
	resp, err := h.comments.Create(h.ctx, dto.CommentReq{
		ObjectType: model.ObjectTypeStory, ObjectID: storyID, Content: "这条要拆成两个需求",
	})
	if err != nil {
		t.Fatalf("发表评论失败: %v", err)
	}
	if resp.ID == 0 {
		t.Error("应回填评论 id")
	}
	if n := h.actionCount(model.ObjectTypeStory, storyID, model.ActionCommented); n != 1 {
		t.Errorf("评论应写入操作历史，实际 %d 条", n)
	}
}

// TestActionListRequiresScope 验证操作历史不能无条件全表查。
func TestActionListRequiresScope(t *testing.T) {
	h := newHarness(t)
	_, err := h.actions.List(h.ctx, dto.ActionFilterReq{})
	if code := errCode(err); code != apperr.InvalidParam {
		t.Fatalf("无范围查询应被拒，实际 %v", err)
	}

	// 给出范围后正常返回。
	if _, err := h.actions.List(h.ctx, dto.ActionFilterReq{ActorID: poUser, Page: 1, Size: 10}); err != nil {
		t.Fatalf("按操作者查询应成功: %v", err)
	}
}

// TestModuleTreeMoveRewritesDescendantPaths 验证移动模块时，
// 子孙的物化路径与层级被整体重写。
func TestModuleTreeMoveRewritesDescendantPaths(t *testing.T) {
	h := newHarness(t)
	productID := h.createProduct("订单中台", "ORDER", model.ACLOpen, []uint{reviewer})

	rootID := h.createModule(productID, 0, "订单")
	childID := h.createModule(productID, rootID, "下单")
	grandID := h.createModule(productID, childID, "校验")

	if got := h.mustModule(grandID).Path; got != wantPath(rootID, childID, grandID) {
		t.Fatalf("初始路径不正确，实际 %q", got)
	}
	if got := h.mustModule(grandID).Grade; got != 3 {
		t.Fatalf("初始层级应为 3，实际 %d", got)
	}

	// 把中间那层搬到顶层：它自己与孙子的路径、层级都要跟着变。
	if _, err := h.modules.Update(h.ctx, childID, dto.ModuleReq{
		Root: productID, Type: model.ModuleTypeStory, ParentID: 0, Name: "下单",
	}); err != nil {
		t.Fatalf("移动模块失败: %v", err)
	}

	if got := h.mustModule(childID).Path; got != strconv.Itoa(int(childID)) {
		t.Errorf("移动后自身路径应只剩自己，实际 %q", got)
	}
	if got := h.mustModule(childID).Grade; got != 1 {
		t.Errorf("移动后自身层级应为 1，实际 %d", got)
	}
	if got := h.mustModule(grandID).Path; got != wantPath(childID, grandID) {
		t.Errorf("移动后孙子的路径应被重写，实际 %q", got)
	}
	if got := h.mustModule(grandID).Grade; got != 2 {
		t.Errorf("移动后孙子的层级应为 2，实际 %d", got)
	}
}

// TestModuleCannotMoveUnderItsOwnDescendant 验证成环被拒。
func TestModuleCannotMoveUnderItsOwnDescendant(t *testing.T) {
	h := newHarness(t)
	productID := h.createProduct("订单中台", "ORDER", model.ACLOpen, []uint{reviewer})

	rootID := h.createModule(productID, 0, "订单")
	childID := h.createModule(productID, rootID, "下单")

	_, err := h.modules.Update(h.ctx, rootID, dto.ModuleReq{
		Root: productID, Type: model.ModuleTypeStory, ParentID: childID, Name: "订单",
	})
	if code := errCode(err); code != apperr.ModuleCycle {
		t.Fatalf("把模块挂到自己的子孙下应报 module_cycle，实际 %v", err)
	}
}

// ---------------------------------------------------------------------------
// harness 上的取数辅助
// ---------------------------------------------------------------------------

func (h *harness) statusOf(storyID uint) string { return h.mustStory(storyID).Status }

func (h *harness) mustStory(id uint) *model.Story {
	h.t.Helper()
	// 直接读模型行而不是复用出参：出参丢掉了很多字段（关闭原因、
	// 关闭时间等），而「激活后这些字段有没有被清干净」正是要断言的东西。
	var story model.Story
	if err := h.db.First(&story, id).Error; err != nil {
		h.t.Fatalf("读需求 %d 失败: %v", id, err)
	}
	return &story
}

func (h *harness) createModule(productID, parentID uint, name string) uint {
	h.t.Helper()
	m, err := h.modules.Create(h.ctx, dto.ModuleReq{
		Root: productID, Type: model.ModuleTypeStory,
		ParentID: parentID, Name: name,
	})
	if err != nil {
		h.t.Fatalf("建模块 %s 失败: %v", name, err)
	}
	return m.ID
}

func (h *harness) mustModule(id uint) *dto.ModuleResp {
	h.t.Helper()
	m, err := h.modules.Get(h.ctx, id)
	if err != nil {
		h.t.Fatalf("取模块 %d 失败: %v", id, err)
	}
	return m
}

// wantPath 拼出期望的物化路径。
func wantPath(ids ...uint) string {
	out := ""
	for i, id := range ids {
		if i > 0 {
			out += ","
		}
		out += strconv.Itoa(int(id))
	}
	return out
}
