package service_test

import (
	"testing"

	"github.com/xywf221/xuliu/internal/dto"
	"github.com/xywf221/xuliu/internal/model"
	"github.com/xywf221/xuliu/internal/pkg/apperr"
	"github.com/xywf221/xuliu/internal/service"
)

// 本文件覆盖 M4 的缺陷闭环：
// 提交 → 确认 → 指派 → 解决 → 关闭 →（必要时）重新激活 → 转需求。
//
// 重点仍是几条口径：
//   - resolution 只在解决时写入，关闭不改它（已关闭的缺陷仍能看出当初怎么解决的）；
//   - 重新激活必须把上一轮的解决痕迹清干净，否则会出现「激活态 + 已修复」的矛盾状态；
//   - 「转需求」与「缺陷改为已解决」必须同生同死，不能只成一半。

// ---------------------------------------------------------------------------
// 脚手架
// ---------------------------------------------------------------------------

// createBug 建一个缺陷并要求成功。
func (h *harness) createBug(req dto.BugReq) *dto.BugResp {
	h.t.Helper()
	if req.Title == "" {
		req.Title = "测试缺陷"
	}
	bug, err := h.bugs.Create(h.ctx, req)
	if err != nil {
		h.t.Fatalf("建缺陷失败: %v", err)
	}
	return bug
}

// mustBug 取缺陷并要求成功。
func (h *harness) mustBug(id uint) *dto.BugResp {
	h.t.Helper()
	bug, err := h.bugs.Get(h.ctx, id)
	if err != nil {
		h.t.Fatalf("取缺陷失败: %v", err)
	}
	return bug
}

// applyBug 执行一个缺陷动作并要求成功。
func (h *harness) applyBug(id uint, in service.BugActionInput) *dto.BugResp {
	h.t.Helper()
	bug, err := h.bugs.Apply(h.ctx, id, in)
	if err != nil {
		h.t.Fatalf("执行缺陷动作 %s 失败: %v", in.Action, err)
	}
	return bug
}

// disableStoryReview 关掉产品的需求评审。
//
// 「转需求」这条链路里，需求会继承产品的评审设置：产品开着评审又没设
// 默认评审人时，新需求根本建不出来。测试要验证的是转换本身，
// 所以这里直接改库关掉评审，而不是绕开需求侧的校验。
func (h *harness) disableStoryReview(productID uint) {
	h.t.Helper()
	if err := h.db.Model(&model.Product{}).Where("id = ?", productID).
		Update("review_story", false).Error; err != nil {
		h.t.Fatalf("关闭产品评审失败: %v", err)
	}
}

// ---------------------------------------------------------------------------
// 全流程
// ---------------------------------------------------------------------------

// TestBugLifecycleConfirmResolveCloseActivate 走一遍缺陷的完整闭环。
func TestBugLifecycleConfirmResolveCloseActivate(t *testing.T) {
	h := newHarness(t)
	productID := h.createProduct("产品缺陷A", "BUGPA", model.ACLOpen, nil)
	projectID := h.createProject("项目缺陷A", "PRJBA", productID)
	executionID := h.createExecution(projectID, "迭代缺陷1")

	bug := h.createBug(dto.BugReq{
		ProductID: productID, ProjectID: projectID, ExecutionID: executionID,
		Title: "登录页在 Safari 下白屏", Steps: "1. 打开登录页\n2. 白屏",
		Severity: 1, Pri: 2, OpenedBuild: "v1.0",
	})
	if bug.Status != model.BugStatusActive || bug.Confirmed != model.BugUnconfirmed {
		t.Fatalf("新缺陷应为激活且未确认，实际 %s/%d", bug.Status, bug.Confirmed)
	}
	if bug.Code != "BUG-"+itoa(bug.ID) {
		t.Fatalf("缺陷编号应为 BUG-%d，实际 %s", bug.ID, bug.Code)
	}
	// 严重程度留默认值时是 3（1 最高、4 最低）。
	if plain := h.createBug(dto.BugReq{ProductID: productID, Title: "没填等级的缺陷"}); plain.Severity != 3 || plain.Pri != 3 {
		t.Fatalf("未指定等级时应回落到 3/3，实际 %d/%d", plain.Severity, plain.Pri)
	}
	// 环境与关联信息在列表页要能一次带全。
	if bug.ProjectName == "" || bug.ExecutionName == "" {
		t.Fatalf("缺陷应带回项目与迭代名称，实际 %q/%q", bug.ProjectName, bug.ExecutionName)
	}

	// 重复确认要挡住 —— 否则「确认时间」会被反复刷新，失去意义。
	bug = h.applyBug(bug.ID, service.BugActionInput{Action: "confirm", Comment: "确实能复现"})
	if bug.Confirmed != model.BugConfirmed {
		t.Fatalf("确认后应为已确认，实际 %d", bug.Confirmed)
	}
	if _, err := h.bugs.Apply(h.ctx, bug.ID, service.BugActionInput{Action: "confirm"}); errCode(err) != apperr.BugAlreadyConfirmed {
		t.Fatalf("重复确认应报 bug_already_confirmed，实际 %v", err)
	}

	bug = h.applyBug(bug.ID, service.BugActionInput{Action: "assign", AssignedTo: reviewer})
	if bug.AssignedTo != reviewer || bug.AssignName == "" || bug.AssignedDate == "" {
		t.Fatalf("指派后应写入负责人与时间，实际 %d/%q/%q",
			bug.AssignedTo, bug.AssignName, bug.AssignedDate)
	}
	// 缺陷与任务不同：指派不要求对方在项目团队里（测试、客服常常不在），
	// 但必须是一个真实存在的用户。
	if _, err := h.bugs.Apply(h.ctx, bug.ID, service.BugActionInput{
		Action: "assign", AssignedTo: 9999,
	}); errCode(err) != apperr.UserNotFound {
		t.Fatalf("指派给不存在的用户应报 user_not_found，实际 %v", err)
	}

	// 解决必须给方案。
	if _, err := h.bugs.Apply(h.ctx, bug.ID, service.BugActionInput{Action: "resolve"}); errCode(err) != apperr.BugResolutionRequired {
		t.Fatalf("未给解决方案应报 bug_resolution_required，实际 %v", err)
	}
	if _, err := h.bugs.Apply(h.ctx, bug.ID, service.BugActionInput{
		Action: "resolve", Resolution: "nonsense",
	}); errCode(err) != apperr.BugResolutionInvalid {
		t.Fatalf("非法解决方案应报 bug_resolution_invalid，实际 %v", err)
	}

	bug = h.applyBug(bug.ID, service.BugActionInput{
		Action: "resolve", Resolution: model.BugResolutionFixed,
		ResolvedBuild: "v1.1", Comment: "已修复 Safari 的样式兼容",
	})
	if bug.Status != model.BugStatusResolved || bug.Resolution != model.BugResolutionFixed {
		t.Fatalf("解决后应为 已解决/已修复，实际 %s/%s", bug.Status, bug.Resolution)
	}
	if bug.ResolvedName == "" || bug.ResolvedDate == "" || bug.ResolvedBuild != "v1.1" {
		t.Fatalf("解决应写入处理人/时间/版本，实际 %q/%q/%q",
			bug.ResolvedName, bug.ResolvedDate, bug.ResolvedBuild)
	}
	// 已解决的缺陷仍可指派（谁去验证），但不能跳到「解决」之前的状态。
	if _, err := h.bugs.Apply(h.ctx, bug.ID, service.BugActionInput{Action: "resolve"}); errCode(err) != apperr.BugStatusInvalid {
		t.Fatalf("已解决的缺陷不能再解决，实际 %v", err)
	}

	bug = h.applyBug(bug.ID, service.BugActionInput{Action: "close", Comment: "验证通过"})
	if bug.Status != model.BugStatusClosed || bug.ClosedName == "" || bug.ClosedDate == "" {
		t.Fatalf("关闭后应写入状态与关闭人，实际 %s/%q", bug.Status, bug.ClosedName)
	}
	// 关闭不该抹掉解决方式：这是「怎么解决的」的唯一记录。
	if bug.Resolution != model.BugResolutionFixed {
		t.Fatalf("关闭后应保留解决方案，实际 %q", bug.Resolution)
	}
	if bug.ClosedReason != model.BugResolutionFixed {
		t.Fatalf("关闭原因应沿用解决方式，实际 %q", bug.ClosedReason)
	}

	// 重新激活。
	bug = h.applyBug(bug.ID, service.BugActionInput{Action: "activate", Comment: "线上又出现了"})
	if bug.Status != model.BugStatusActive || bug.ActivatedCount != 1 {
		t.Fatalf("重新激活后应为 激活/激活次数1，实际 %s/%d", bug.Status, bug.ActivatedCount)
	}
	if bug.Resolution != "" || bug.ResolvedBy != 0 || bug.ResolvedDate != "" {
		t.Fatalf("重新激活应清空上一轮解决痕迹，实际 %q/%d/%q",
			bug.Resolution, bug.ResolvedBy, bug.ResolvedDate)
	}
	if bug.ClosedBy != 0 || bug.ClosedDate != "" || bug.ClosedReason != "" {
		t.Fatalf("重新激活应清空关闭痕迹，实际 %d/%q/%q",
			bug.ClosedBy, bug.ClosedDate, bug.ClosedReason)
	}
	// 清掉的是「当前值」，历史里那一次解决记录必须还在。
	if n := h.actionCount(model.ObjectTypeBug, bug.ID, model.ActionResolved); n != 1 {
		t.Fatalf("操作历史里应保留 1 条解决记录，实际 %d", n)
	}
	if n := h.actionCount(model.ObjectTypeBug, bug.ID, model.ActionActivated); n != 1 {
		t.Fatalf("操作历史里应有 1 条激活记录，实际 %d", n)
	}
	if n := h.actionCount(model.ObjectTypeBug, bug.ID, model.ActionOpened); n != 1 {
		t.Fatalf("操作历史里应有 1 条创建记录，实际 %d", n)
	}
}

// TestResolveAsDuplicateNeedsTarget 验证「重复」必须指明重复于谁。
func TestResolveAsDuplicateNeedsTarget(t *testing.T) {
	h := newHarness(t)
	productID := h.createProduct("产品缺陷B", "BUGPB", model.ACLOpen, nil)

	source := h.createBug(dto.BugReq{ProductID: productID, Title: "原始缺陷"})
	dup := h.createBug(dto.BugReq{ProductID: productID, Title: "看起来重复的缺陷"})

	if _, err := h.bugs.Apply(h.ctx, dup.ID, service.BugActionInput{
		Action: "resolve", Resolution: model.BugResolutionDuplicate,
	}); errCode(err) != apperr.BugDuplicateTargetRequired {
		t.Fatalf("重复缺陷未指明来源应被拒，实际 %v", err)
	}
	if _, err := h.bugs.Apply(h.ctx, dup.ID, service.BugActionInput{
		Action: "resolve", Resolution: model.BugResolutionDuplicate, DuplicateBugID: dup.ID,
	}); errCode(err) != apperr.BugDuplicateOfSelf {
		t.Fatalf("重复缺陷指向自己应被拒，实际 %v", err)
	}

	dup = h.applyBug(dup.ID, service.BugActionInput{
		Action: "resolve", Resolution: model.BugResolutionDuplicate, DuplicateBugID: source.ID,
	})
	if dup.DuplicateBugID != source.ID || dup.DuplicateBugTitle != source.Title {
		t.Fatalf("应记录重复来源及标题，实际 %d/%q", dup.DuplicateBugID, dup.DuplicateBugTitle)
	}
}

// ---------------------------------------------------------------------------
// 转需求
// ---------------------------------------------------------------------------

// TestBugToStoryCreatesStoryAndResolvesBug 验证「转需求」两边同时落地。
func TestBugToStoryCreatesStoryAndResolvesBug(t *testing.T) {
	h := newHarness(t)
	productID := h.createProduct("产品缺陷C", "BUGPC", model.ACLOpen, nil)
	h.disableStoryReview(productID)

	bug := h.createBug(dto.BugReq{
		ProductID: productID, Title: "缺少批量导入", Steps: "1. 进入列表\n2. 找不到导入入口",
	})

	converted := h.applyBug(bug.ID, service.BugActionInput{
		Action: "tostory", Comment: "这其实是个需求", AssignedTo: poUser,
	})
	if converted.Status != model.BugStatusResolved ||
		converted.Resolution != model.BugResolutionToStory {
		t.Fatalf("转需求后缺陷应为 已解决/转为需求，实际 %s/%s",
			converted.Status, converted.Resolution)
	}
	if converted.ToStoryID == 0 || converted.ToStoryTitle == "" {
		t.Fatalf("转需求后应回填新需求，实际 %d/%q",
			converted.ToStoryID, converted.ToStoryTitle)
	}

	story, err := h.stories.Get(h.ctx, converted.ToStoryID)
	if err != nil {
		t.Fatalf("取新需求失败: %v", err)
	}
	if story.Title != bug.Title {
		t.Fatalf("默认应以缺陷标题为需求标题，实际 %q", story.Title)
	}
	// 需求详情页要能看出它是由缺陷转来的，并带上原始重现步骤。
	if story.Source != model.StorySourceBug {
		t.Fatalf("需求来源应为 bug，实际 %q", story.Source)
	}
	if story.Spec != bug.Steps {
		t.Fatalf("需求描述应沿用缺陷的重现步骤，实际 %q", story.Spec)
	}

	// 转出来的需求不能被删（来龙去脉得留着），所以缺陷也不能删。
	if err := h.bugs.Delete(h.ctx, bug.ID); errCode(err) != apperr.BugHasDownstream {
		t.Fatalf("已转需求的缺陷应拒绝删除，实际 %v", err)
	}
}

// TestBugToStoryRollsBackWhenStoryInvalid 验证转需求是原子的。
//
// 需求建不出来（产品没配默认评审人却开着评审）时，缺陷必须原封不动：
// 只成功一半会留下「需求建了、缺陷还在激活态」的孤儿，用户再点一次
// 就多出一条重复需求。
func TestBugToStoryRollsBackWhenStoryInvalid(t *testing.T) {
	h := newHarness(t)
	// 开着评审且没有默认评审人：需求会卡在「没人可评」上建不出来。
	productID := h.createProduct("产品缺陷D", "BUGPD", model.ACLOpen, nil)
	targetID := h.createProduct("产品缺陷E", "BUGPE", model.ACLOpen, nil)

	bug := h.createBug(dto.BugReq{ProductID: productID, Title: "想转到另一个产品"})

	if _, err := h.bugs.Apply(h.ctx, bug.ID, service.BugActionInput{
		Action: "tostory", StoryProductID: targetID,
	}); errCode(err) != apperr.StoryReviewerRequired {
		t.Fatalf("目标产品没配评审人时应报 story_reviewer_required，实际 %v", err)
	}

	unchanged := h.mustBug(bug.ID)
	if unchanged.Status != model.BugStatusActive || unchanged.ToStoryID != 0 {
		t.Fatalf("转需求失败后缺陷应保持激活且未关联需求，实际 %s/%d",
			unchanged.Status, unchanged.ToStoryID)
	}
	var storyCount int64
	if err := h.db.Model(&model.Story{}).Where("product_id = ?", targetID).
		Count(&storyCount).Error; err != nil {
		t.Fatalf("统计需求失败: %v", err)
	}
	if storyCount != 0 {
		t.Fatalf("失败的需求创建不应留下数据，实际 %d 条", storyCount)
	}
	if n := h.actionCount(model.ObjectTypeBug, bug.ID, model.ActionLinked); n != 0 {
		t.Fatalf("失败的操作不应留下历史记录，实际 %d 条", n)
	}
}

// ---------------------------------------------------------------------------
// 查询与可见性
// ---------------------------------------------------------------------------

// TestListBugsFiltersAndCounts 验证列表筛选与状态计数。
func TestListBugsFiltersAndCounts(t *testing.T) {
	h := newHarness(t)
	productID := h.createProduct("产品缺陷F", "BUGPF", model.ACLOpen, nil)

	h.createBug(dto.BugReq{ProductID: productID, Title: "登录报错", Keywords: "login"})
	assigned := h.createBug(dto.BugReq{
		ProductID: productID, Title: "导出乱码", AssignedTo: reviewer,
	})
	resolved := h.createBug(dto.BugReq{ProductID: productID, Title: "样式错位"})
	h.applyBug(resolved.ID, service.BugActionInput{
		Action: "resolve", Resolution: model.BugResolutionFixed,
	})

	// 按指派人过滤。
	assignee := uint(reviewer)
	result, err := h.bugs.List(h.ctx, dto.BugFilterReq{AssignedTo: &assignee})
	if err != nil {
		t.Fatalf("按指派人查缺陷失败: %v", err)
	}
	if result.Total != 1 || result.List[0].ID != assigned.ID {
		t.Fatalf("按指派人过滤应只剩 1 条，实际 %d 条", result.Total)
	}

	// 关键字覆盖标题与关键词。
	if result, err = h.bugs.List(h.ctx, dto.BugFilterReq{Keywords: "login"}); err != nil || result.Total != 1 {
		t.Fatalf("按关键词应查到 1 条，实际 %d 条 (%v)", result.Total, err)
	}

	// 「未解决」只看还没给出解决方案的。
	if result, err = h.bugs.List(h.ctx, dto.BugFilterReq{Unresolved: true}); err != nil || result.Total != 2 {
		t.Fatalf("未解决的应有 2 条，实际 %d 条 (%v)", result.Total, err)
	}

	// 状态标签：激活 2 / 已解决 1 / 未解决 2。
	counts, err := h.bugs.Counts(h.ctx, dto.BugFilterReq{})
	if err != nil {
		t.Fatalf("统计缺陷状态失败: %v", err)
	}
	if counts.Active != 2 || counts.Resolved != 1 || counts.Closed != 0 || counts.Unresolved != 2 {
		t.Fatalf("状态计数应为 2/1/0/2，实际 %+v", counts)
	}
	// 按状态筛选时，标签值不受状态筛选影响（否则一点「已解决」其他标签就归零）。
	counts, err = h.bugs.Counts(h.ctx, dto.BugFilterReq{Status: model.BugStatusResolved})
	if err != nil {
		t.Fatalf("统计缺陷状态失败: %v", err)
	}
	if counts.Active != 2 || counts.Resolved != 1 {
		t.Fatalf("统计不应被状态筛选影响，实际 %+v", counts)
	}
}

// TestBugVisibilityFollowsProductAndProject 验证缺陷的可见性口径。
func TestBugVisibilityFollowsProductAndProject(t *testing.T) {
	h := newHarness(t)
	openID := h.createProduct("产品缺陷G", "BUGPG", model.ACLOpen, nil)
	privateID := h.createProduct("私有产品缺陷", "BUGPH", model.ACLPrivate, nil)

	h.createBug(dto.BugReq{ProductID: openID, Title: "公开产品下的缺陷"})
	hidden := h.createBug(dto.BugReq{ProductID: privateID, Title: "私有产品下的缺陷"})

	// 路人看不到私有产品下的缺陷 —— 列表与详情必须一致。
	outsiderCtx := asUser(h.ctx, outsider, false)
	listed, err := h.bugs.List(outsiderCtx, dto.BugFilterReq{})
	if err != nil {
		t.Fatalf("路人查缺陷列表失败: %v", err)
	}
	if listed.Total != 1 || listed.List[0].ProductID != openID {
		t.Fatalf("路人应只看到公开产品的 1 条缺陷，实际 %d 条", listed.Total)
	}
	if _, err := h.bugs.Get(outsiderCtx, hidden.ID); errCode(err) != apperr.ProductAccessDenied {
		t.Fatalf("路人应打不开私有产品的缺陷，实际 %v", err)
	}
	// 私有产品下的缺陷挂评论/附件时走的是同一个网关，这里确认缺陷本身
	// 已经被网关认识 —— 不认识的类型会被一律拒绝，那会让评论功能直接不可用。
	loc, err := h.gate.Locate(h.ctx, model.ObjectTypeBug, hidden.ID)
	if err != nil {
		t.Fatalf("定位缺陷失败: %v", err)
	}
	if loc.ProductID != privateID {
		t.Fatalf("缺陷应定位到产品 %d，实际 %d", privateID, loc.ProductID)
	}
}

// ---------------------------------------------------------------------------
// 与项目/迭代/需求的联动
// ---------------------------------------------------------------------------

// TestDeleteProjectAndExecutionDetachBugs 验证项目与迭代被删后，缺陷退回产品级。
//
// 不这么做的话，缺陷会指向一个已经不存在的项目/迭代：列表按项目过滤会漏掉它，
// 详情按项目校验可见性会拒绝打开它 —— 数据还在库里，却从界面上彻底消失。
func TestDeleteProjectAndExecutionDetachBugs(t *testing.T) {
	h := newHarness(t)
	productID := h.createProduct("产品缺陷H", "BUGPI", model.ACLOpen, nil)
	projectID := h.createProject("项目缺陷H", "PRJBH", productID)
	executionID := h.createExecution(projectID, "迭代缺陷H")

	bug := h.createBug(dto.BugReq{
		ProductID: productID, ProjectID: projectID, ExecutionID: executionID,
		Title: "迭代里发现的缺陷",
	})

	if err := h.executions.Delete(h.ctx, executionID); err != nil {
		t.Fatalf("删迭代失败: %v", err)
	}
	detached := h.mustBug(bug.ID)
	if detached.ExecutionID != 0 {
		t.Fatalf("迭代删除后缺陷不应再挂着它，实际 %d", detached.ExecutionID)
	}
	if detached.ProductID != productID {
		t.Fatalf("缺陷应退回产品级并保留产品归属，实际 %d", detached.ProductID)
	}

	if err := h.projects.Delete(h.ctx, projectID); err != nil {
		t.Fatalf("删项目失败: %v", err)
	}
	if detached = h.mustBug(bug.ID); detached.ProjectID != 0 || detached.ExecutionID != 0 {
		t.Fatalf("项目删除后缺陷应完全退回产品级，实际 %d/%d",
			detached.ProjectID, detached.ExecutionID)
	}
	// 退回产品级之后，它仍然是一条能在列表里查到的缺陷。
	listed, err := h.bugs.List(h.ctx, dto.BugFilterReq{ProductID: productID})
	if err != nil || listed.Total != 1 {
		t.Fatalf("退回产品级的缺陷仍应可查，实际 %d 条 (%v)", listed.Total, err)
	}
}

// TestStoryDeleteGuardsAgainstBugs 验证需求删除与缺陷的两种关系各走各的路。
//
// 「缺陷转来的需求」不能删（会抹掉缺陷的处理结果）；
// 「被缺陷引用的需求」可以删，但引用关系要一并清干净。
func TestStoryDeleteGuardsAgainstBugs(t *testing.T) {
	h := newHarness(t)
	productID := h.createProduct("产品缺陷I", "BUGPJ", model.ACLOpen, nil)
	h.disableStoryReview(productID)

	converted := h.createBug(dto.BugReq{ProductID: productID, Title: "转成需求的缺陷"})
	converted = h.applyBug(converted.ID, service.BugActionInput{Action: "tostory"})
	if err := h.stories.Delete(h.ctx, converted.ToStoryID); errCode(err) != apperr.StoryFromBug {
		t.Fatalf("由缺陷转入的需求应拒绝删除，实际 %v", err)
	}

	// 被缺陷引用的需求：删除后缺陷还在，只是不再指着它。
	referenced := h.createStory(productID, "被缺陷引用的需求", nil)
	bug := h.createBug(dto.BugReq{
		ProductID: productID, StoryID: referenced, Title: "验证时发现的缺陷",
	})
	if bug.StoryVersion != 1 {
		t.Fatalf("缺陷应记下提交时的需求版本，实际 %d", bug.StoryVersion)
	}
	if err := h.stories.Delete(h.ctx, referenced); err != nil {
		t.Fatalf("被缺陷引用的需求应可删除: %v", err)
	}
	orphan := h.mustBug(bug.ID)
	if orphan.StoryID != 0 || orphan.StoryVersion != 0 {
		t.Fatalf("需求删除后缺陷的关联应被清空，实际 %d/%d",
			orphan.StoryID, orphan.StoryVersion)
	}
}

// TestBugTypeValidatedInService 验证服务层也会挡住未登记的缺陷类型。
//
// HTTP 那条路上 DTO 的 oneof 已经挡过一次，但服务层可能被别的入口
// （缺陷转需求、内部调度）直接调用，这里再挡一道成本极低。
func TestBugTypeValidatedInService(t *testing.T) {
	h := newHarness(t)
	productID := h.createProduct("产品缺陷J", "BUGPK", model.ACLOpen, nil)

	if _, err := h.bugs.Create(h.ctx, dto.BugReq{
		ProductID: productID, Title: "类型非法", Type: "nonsense",
	}); errCode(err) != apperr.InvalidParam {
		t.Fatalf("未登记的缺陷类型应被拒，实际 %v", err)
	}

	bug := h.createBug(dto.BugReq{
		ProductID: productID, Title: "性能问题", Type: model.BugTypePerformance,
	})
	if bug.TypeLabel == "" || bug.SeverityLabel == "" || bug.PriLabel == "" {
		t.Fatalf("缺陷应带回类型/严重程度/优先级的中文标签，实际 %q/%q/%q",
			bug.TypeLabel, bug.SeverityLabel, bug.PriLabel)
	}
}

// TestBugVisibilityWhenNothingIsVisible 验证「一个产品都看不到」时确实什么都看不到。
//
// 这是最容易被写错的一种：可见 id 列表为空时，若把它当成「不过滤」，
// 私有产品的缺陷就会连同数量统计一起漏出去 —— 列表漏数据看得见，
// 统计漏数据（标签上那个数字）几乎没人会发现。
func TestBugVisibilityWhenNothingIsVisible(t *testing.T) {
	h := newHarness(t)
	privateA := h.createProduct("私有产品K", "BUGPL", model.ACLPrivate, nil)
	privateB := h.createProduct("私有产品L", "BUGPM", model.ACLPrivate, nil)

	h.createBug(dto.BugReq{ProductID: privateA, Title: "私有缺陷一"})
	h.createBug(dto.BugReq{ProductID: privateB, Title: "私有缺陷二", AssignedTo: reviewer})

	outsiderCtx := asUser(h.ctx, outsider, false)
	listed, err := h.bugs.List(outsiderCtx, dto.BugFilterReq{})
	if err != nil {
		t.Fatalf("路人查缺陷列表失败: %v", err)
	}
	if listed.Total != 0 || len(listed.List) != 0 {
		t.Fatalf("一个产品都看不到时不应返回任何缺陷，实际 %d 条", listed.Total)
	}
	counts, err := h.bugs.Counts(outsiderCtx, dto.BugFilterReq{})
	if err != nil {
		t.Fatalf("路人统计缺陷失败: %v", err)
	}
	if counts.Active != 0 || counts.Unresolved != 0 {
		t.Fatalf("统计也必须按可见性过滤，实际 %+v", counts)
	}
	// 换成有权限的人，数据还在。
	listed, err = h.bugs.List(h.ctx, dto.BugFilterReq{})
	if err != nil || listed.Total != 2 {
		t.Fatalf("有权限的人应看到 2 条缺陷，实际 %d 条 (%v)", listed.Total, err)
	}
}
