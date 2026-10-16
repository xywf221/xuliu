package service_test

import (
	"strings"
	"testing"
	"time"

	"github.com/xywf221/xuliu/internal/dto"
	"github.com/xywf221/xuliu/internal/model"
	"github.com/xywf221/xuliu/internal/pkg/pagination"
	"github.com/xywf221/xuliu/internal/repository"
	"github.com/xywf221/xuliu/internal/service"
)

// 本文件覆盖 M5「工作台与通知」。
//
// 三条最容易被写错、写错了也不会有人立刻发现的口径：
//   - 通知只发给「下一个要动手的人」，且同一个对象上只留一条未读（待办语义）；
//   - 工作台只显示「我的」，而且仍然受可见性约束；
//   - 燃尽图的每一天都是从那天的工时记录里还原出来的，不是拿任务当前值画的。

// ---------------------------------------------------------------------------
// 脚手架
// ---------------------------------------------------------------------------

// unreadOf 取某人的未读通知。
func (h *harness) unreadOf(userID uint) []model.Notice {
	h.t.Helper()
	unread := true
	result, err := h.noticeRepo.List(h.ctx, repository.NoticeFilter{
		ToUser:     userID,
		Unread:     &unread,
		Pagination: pagination.Params{Page: 1, Size: 100},
	})
	if err != nil {
		h.t.Fatalf("查通知失败: %v", err)
	}
	return result.List
}

// applyTask 执行一个任务动作并要求成功。
func (h *harness) applyTask(id uint, in service.TaskActionInput) {
	h.t.Helper()
	if _, err := h.tasks.Apply(h.ctx, id, in); err != nil {
		h.t.Fatalf("执行任务动作 %s 失败: %v", in.Action, err)
	}
}

// createPrivateProject 建一个私有项目。
//
// createProject 固定建公开项目（公开项目人人可见），要验证「看不见的就
// 真看不见」必须用私有项目。
func (h *harness) createPrivateProject(name, code string, productIDs ...uint) uint {
	h.t.Helper()
	project, err := h.projects.Create(h.ctx, dto.ProjectReq{
		Name: name, Code: code, Model: model.ProjectModelScrum,
		ACL: model.ACLPrivate, PO: poUser, PM: poUser,
		ProductIDs: productIDs,
	})
	if err != nil {
		h.t.Fatalf("建私有项目失败: %v", err)
	}
	return project.ID
}

// mustUnreadCount 取某人的未读通知数。
func (h *harness) mustUnreadCount(userID uint) int64 {
	h.t.Helper()
	count, err := h.noticeRepo.CountUnread(h.ctx, userID)
	if err != nil {
		h.t.Fatalf("统计未读通知失败: %v", err)
	}
	return count
}

// ---------------------------------------------------------------------------
// 通知
// ---------------------------------------------------------------------------

// TestTaskNoticeFollowsTheNextActor 验证任务的通知只发给「接下来该动手的人」：
// 建任务指派给他 → 通知他；他完成后 → 通知提任务的人；其它动作不发。
func TestTaskNoticeFollowsTheNextActor(t *testing.T) {
	h := newHarness(t)
	productID := h.createProduct("通知产品", "NOTI", model.ACLOpen, nil)
	projectID := h.createProject("通知项目", "NP", productID)
	executionID := h.createExecution(projectID, "通知迭代")
	h.addProjectMember(projectID, reviewer)

	// 由 poUser 建任务并指派给 reviewer。
	task := h.createTask(dto.TaskReq{
		ProjectID: projectID, ExecutionID: executionID, ProductID: productID,
		Name: "把登录日志接进审计", AssignedTo: reviewer,
	})
	if n := h.mustUnreadCount(reviewer); n != 1 {
		t.Fatalf("被指派的人应收到 1 条未读通知，实际 %d", n)
	}
	if n := h.mustUnreadCount(poUser); n != 0 {
		t.Fatalf("操作者自己不该收到通知，实际 %d", n)
	}
	notices := h.unreadOf(reviewer)
	if notices[0].ObjectType != model.ObjectTypeTask || notices[0].ObjectID != task.ID {
		t.Fatalf("通知应指向任务 %d，实际 %s/%d",
			task.ID, notices[0].ObjectType, notices[0].ObjectID)
	}
	if notices[0].Link != "/tasks/"+itoa(task.ID) {
		t.Fatalf("通知应带上跳转路径，实际 %q", notices[0].Link)
	}
	if notices[0].FromUser != poUser {
		t.Fatalf("通知的触发者应为 %d，实际 %d", poUser, notices[0].FromUser)
	}

	// 「开始」不改变「该谁动手」，不该产生新通知。
	dev := asUser(h.ctx, reviewer, true)
	if _, err := h.tasks.Apply(dev, task.ID, service.TaskActionInput{Action: "start"}); err != nil {
		t.Fatalf("开始任务失败: %v", err)
	}
	if n := h.mustUnreadCount(reviewer); n != 1 {
		t.Fatalf("开始任务不该发通知，实际未读 %d", n)
	}

	// 完成的任务要交回提任务的人验收。
	if _, err := h.tasks.Apply(dev, task.ID, service.TaskActionInput{Action: "finish"}); err != nil {
		t.Fatalf("完成任务失败: %v", err)
	}
	if n := h.mustUnreadCount(poUser); n != 1 {
		t.Fatalf("任务完成后提任务的人应收到通知，实际 %d", n)
	}
	if got := h.unreadOf(poUser)[0].Title; got != "任务已完成" {
		t.Fatalf("通知标题应为「任务已完成」，实际 %q", got)
	}

	// 重新激活 → 负责人要继续处理。
	h.applyTask(task.ID, service.TaskActionInput{Action: "activate"})
	last := h.unreadOf(reviewer)
	if len(last) != 1 || last[0].Title != "任务重新开始" {
		t.Fatalf("重新激活应通知负责人，实际 %+v", last)
	}
}

// TestNoticeUpsertKeepsOneUnreadPerObject 验证通知的「待办」语义。
//
// 同一条缺陷来回指派三次，收件人不该攒下三条一模一样的提醒 ——
// 他需要知道的是「现在该我处理它了」。想看来龙去脉有操作历史那条线。
func TestNoticeUpsertKeepsOneUnreadPerObject(t *testing.T) {
	h := newHarness(t)
	productID := h.createProduct("通知产品B", "NOTIB", model.ACLOpen, nil)

	bug := h.createBug(dto.BugReq{ProductID: productID, Title: "导出乱码"})
	h.applyBug(bug.ID, service.BugActionInput{Action: "assign", AssignedTo: reviewer})
	h.applyBug(bug.ID, service.BugActionInput{Action: "assign", AssignedTo: outsider})
	h.applyBug(bug.ID, service.BugActionInput{Action: "assign", AssignedTo: reviewer})

	if n := h.mustUnreadCount(reviewer); n != 1 {
		t.Fatalf("同一对象上的未读应被覆盖而不是累加，实际 %d", n)
	}
	// 另一位被指派过的人留着的那条不该被顺手清掉：那是他的待办，
	// 只是他手里的这条已经不是最新的了。
	if n := h.mustUnreadCount(outsider); n != 1 {
		t.Fatalf("另一个人的未读不该受影响，实际 %d", n)
	}
	if got := h.unreadOf(reviewer)[0].Content; got == "" {
		t.Fatal("覆盖后应保留正文")
	}
}

// TestBugNoticeGoesToReporterThenBackToResolver 走一遍缺陷通知的两个转向。
func TestBugNoticeGoesToReporterThenBackToResolver(t *testing.T) {
	h := newHarness(t)
	productID := h.createProduct("通知产品C", "NOTIC", model.ACLOpen, nil)

	// poUser 提缺陷并指派给 reviewer。
	bug := h.createBug(dto.BugReq{
		ProductID: productID, Title: "批量删除没反应", AssignedTo: reviewer,
	})

	// reviewer 解决它 → 通知提缺陷的人（验证与关闭归他）。
	hotfix := asUser(h.ctx, reviewer, true)
	if _, err := h.bugs.Apply(hotfix, bug.ID, service.BugActionInput{
		Action: "resolve", Resolution: model.BugResolutionFixed,
	}); err != nil {
		t.Fatalf("解决缺陷失败: %v", err)
	}
	unread := h.unreadOf(poUser)
	if len(unread) != 1 || unread[0].Title != "缺陷已解决" {
		t.Fatalf("解决后应通知提缺陷的人，实际 %+v", unread)
	}

	// poUser 验收不通过，重新激活 → 通知上次解决它的人。
	h.applyBug(bug.ID, service.BugActionInput{Action: "activate", Comment: "还是不行"})
	back := h.unreadOf(reviewer)
	if len(back) != 1 || back[0].Title != "缺陷重新激活" {
		t.Fatalf("重新激活应通知上次解决的人，实际 %+v", back)
	}
	// reviewer 在这条缺陷上只留一条未读（指派那条被覆盖了）。
	if n := h.mustUnreadCount(reviewer); n != 1 {
		t.Fatalf("reviewer 应只有 1 条未读，实际 %d", n)
	}
}

// TestStoryNoticeOnSubmitReviewAndResult 验证需求评审的两端都有人被通知。
func TestStoryNoticeOnSubmitReviewAndResult(t *testing.T) {
	h := newHarness(t)
	productID := h.createProduct("通知产品D", "NOTID", model.ACLOpen, []uint{reviewer})
	storyID := h.createStory(productID, "支持导出 Excel", nil)

	if _, err := h.stories.Apply(h.ctx, storyID, pendingReview()); err != nil {
		t.Fatalf("提交评审失败: %v", err)
	}
	// 提交人（poUser）不在名单里，只有评审人该收到。
	if n := h.mustUnreadCount(reviewer); n != 1 {
		t.Fatalf("评审人应收到 1 条待评审通知，实际 %d", n)
	}
	if got := h.unreadOf(reviewer)[0].Title; got != "需求待你评审" {
		t.Fatalf("通知标题应为「需求待你评审」，实际 %q", got)
	}

	// 给出结论 → 通知提需求的人，正文要带上结论本身。
	if _, err := h.stories.Apply(asUser(h.ctx, reviewer, true), storyID,
		service.ActionInput{Action: "pass", Result: model.ReviewPass}); err != nil {
		t.Fatalf("评审通过失败: %v", err)
	}
	unread := h.unreadOf(poUser)
	if len(unread) != 1 || unread[0].Title != "需求评审完成" {
		t.Fatalf("评审后应通知提需求的人，实际 %+v", unread)
	}
	if !strings.Contains(unread[0].Content, "确认通过") {
		t.Fatalf("通知正文应带上结论的中文名，实际 %q", unread[0].Content)
	}
}

// TestNoticeNotSentToSelf 验证不给自己发通知。
//
// 这是一条容易漏的规则：自己建、自己负责的任务，如果照发不误，
// 每个人的工作台都会被自己制造的动作塞满。
func TestNoticeNotSentToSelf(t *testing.T) {
	h := newHarness(t)
	productID := h.createProduct("通知产品E", "NOTIE", model.ACLOpen, nil)
	// poUser 是项目的 PO，建项目时就进了团队，所以这里不必再加一次。
	projectID := h.createProject("通知项目E", "NPE", productID)

	h.createTask(dto.TaskReq{
		ProjectID: projectID, Name: "自己给自己的任务", AssignedTo: poUser,
	})
	if n := h.mustUnreadCount(poUser); n != 0 {
		t.Fatalf("不该给自己发通知，实际未读 %d", n)
	}
}

// ---------------------------------------------------------------------------
// 通知的读写边界
// ---------------------------------------------------------------------------

// TestNoticeReadIsScopedToOwner 验证「标记已读」只能改自己的。
//
// 少了 to_user 这个条件，任何人拿到 id 就能把别人的提醒清掉 ——
// 那等于一个不需要权限的静音开关。
func TestNoticeReadIsScopedToOwner(t *testing.T) {
	h := newHarness(t)
	productID := h.createProduct("通知产品F", "NOTIF", model.ACLOpen, nil)
	h.createBug(dto.BugReq{ProductID: productID, Title: "指派给 reviewer", AssignedTo: reviewer})

	notices := h.unreadOf(reviewer)
	if len(notices) != 1 {
		t.Fatalf("准备数据失败：reviewer 应有 1 条未读，实际 %d", len(notices))
	}
	noticeID := notices[0].ID

	// 以 outsider 的身份去标记 reviewer 的通知。
	other := asUser(h.ctx, outsider, true)
	if _, err := h.notices.MarkRead(other, dto.NoticeReadReq{IDs: []uint{noticeID}}); err != nil {
		t.Fatalf("标记已读不该报错（只是不生效），实际 %v", err)
	}
	if n := h.mustUnreadCount(reviewer); n != 1 {
		t.Fatalf("别人的未读不该被标记，实际 %d", n)
	}

	// 本人来标才生效。
	if _, err := h.notices.MarkRead(asUser(h.ctx, reviewer, true),
		dto.NoticeReadReq{IDs: []uint{noticeID}}); err != nil {
		t.Fatalf("标记已读失败: %v", err)
	}
	if n := h.mustUnreadCount(reviewer); n != 0 {
		t.Fatalf("本人标记后应为 0，实际 %d", n)
	}
}

// TestNoticeUnreadFilterDirection 验证「只看未读」这个筛选的方向。
//
// 这个条件写反过一次：把 Unread 当成 read 列该有的值，于是
// 「只看未读」列出的全是已读。它不报错、不崩溃，只是永远空着 ——
// 靠手点一次接口才发现，所以在这里钉住。
func TestNoticeUnreadFilterDirection(t *testing.T) {
	h := newHarness(t)
	productID := h.createProduct("通知产品H2", "NOTIH", model.ACLOpen, nil)

	first := h.createBug(dto.BugReq{ProductID: productID, Title: "第一条", AssignedTo: reviewer})
	h.createBug(dto.BugReq{ProductID: productID, Title: "第二条", AssignedTo: reviewer})

	// 把第一条读掉。
	if _, err := h.notices.MarkRead(asUser(h.ctx, reviewer, true),
		dto.NoticeReadReq{IDs: []uint{h.noticeOn(reviewer, first.ID)}}); err != nil {
		t.Fatalf("标记已读失败: %v", err)
	}

	unread := true
	onlyUnread, err := h.noticeRepo.List(h.ctx, repository.NoticeFilter{
		ToUser: reviewer, Unread: &unread, Pagination: pagination.Params{Page: 1, Size: 10},
	})
	if err != nil {
		t.Fatalf("查未读失败: %v", err)
	}
	if onlyUnread.Total != 1 || onlyUnread.List[0].Read {
		t.Fatalf("只看未读应返回 1 条未读，实际 %d 条（首条 read=%v）",
			onlyUnread.Total, len(onlyUnread.List) > 0 && onlyUnread.List[0].Read)
	}

	read := false
	onlyRead, err := h.noticeRepo.List(h.ctx, repository.NoticeFilter{
		ToUser: reviewer, Unread: &read, Pagination: pagination.Params{Page: 1, Size: 10},
	})
	if err != nil {
		t.Fatalf("查已读失败: %v", err)
	}
	if onlyRead.Total != 1 || !onlyRead.List[0].Read {
		t.Fatalf("只看已读应返回 1 条已读，实际 %d 条", onlyRead.Total)
	}

	// 不传就是全都看。
	all, err := h.noticeRepo.List(h.ctx, repository.NoticeFilter{
		ToUser: reviewer, Pagination: pagination.Params{Page: 1, Size: 10},
	})
	if err != nil {
		t.Fatalf("查全部失败: %v", err)
	}
	if all.Total != 2 {
		t.Fatalf("不传 unread 时应返回全部 2 条，实际 %d", all.Total)
	}
}

// noticeOn 取某人在某对象上的那条通知的 id。
func (h *harness) noticeOn(userID, objectID uint) uint {
	h.t.Helper()
	for _, n := range h.unreadOf(userID) {
		if n.ObjectID == objectID {
			return n.ID
		}
	}
	h.t.Fatalf("用户 %d 在对象 %d 上没有未读通知", userID, objectID)
	return 0
}

// TestNoticeDeleteRejectsOthersNotice 验证删别人的通知按「不存在」处理。
func TestNoticeDeleteRejectsOthersNotice(t *testing.T) {
	h := newHarness(t)
	productID := h.createProduct("通知产品G", "NOTIG", model.ACLOpen, nil)
	h.createBug(dto.BugReq{ProductID: productID, Title: "指派给 reviewer", AssignedTo: reviewer})
	noticeID := h.unreadOf(reviewer)[0].ID

	if err := h.notices.Delete(asUser(h.ctx, outsider, true), noticeID); err == nil {
		t.Fatal("删别人的通知应当失败")
	}
	if n := h.mustUnreadCount(reviewer); n != 1 {
		t.Fatalf("别人的通知不该被删掉，实际未读 %d", n)
	}
}

// ---------------------------------------------------------------------------
// 工作台
// ---------------------------------------------------------------------------

// TestWorkbenchShowsOnlyMineAndRespectsVisibility 验证工作台的两层收窄：
// 先是「我的」，再是「我看得到的」。
func TestWorkbenchShowsOnlyMineAndRespectsVisibility(t *testing.T) {
	h := newHarness(t)

	openProduct := h.createProduct("工作台公开产品", "WBOPEN", model.ACLOpen, nil)
	openProject := h.createProject("工作台公开项目", "WBP1", openProduct)
	h.addProjectMember(openProject, reviewer)
	h.createTask(dto.TaskReq{
		ProjectID: openProject, ProductID: openProduct,
		Name: "公开项目里派给研发的任务", AssignedTo: reviewer,
	})

	// 私有产品 + 私有项目，reviewer 既不是成员也不在白名单里。
	// 这里直接写库造一条「指派给他、但项目已经看不见」的任务：
	// 这正是「人还在项目里的时候被派了活，之后离开了项目」的样子，
	// 走服务接口造不出来（服务不允许把任务派给非成员）。
	privateProduct := h.createProduct("工作台私有产品", "WBPRIV", model.ACLPrivate, nil)
	privateProject := h.createPrivateProject("工作台私有项目", "WBP2", privateProduct)
	if err := h.db.Create(&model.Task{
		ProjectID: privateProject, ProductID: privateProduct,
		Name: "私有项目里派给研发的任务", AssignedTo: reviewer,
		Status: model.TaskStatusWait, Mode: model.TaskModeSingle,
	}).Error; err != nil {
		t.Fatalf("造私有项目任务失败: %v", err)
	}

	// reviewer 看得到公开那条，看不到私有那条。
	mine, err := h.workbench.Get(asUser(h.ctx, reviewer, true))
	if err != nil {
		t.Fatalf("取工作台失败: %v", err)
	}
	if len(mine.Tasks) != 1 || mine.Tasks[0].Name != "公开项目里派给研发的任务" {
		t.Fatalf("工作台应只列出可见项目里的任务，实际 %+v", mine.Tasks)
	}
	if mine.Counts.TaskWait != 1 {
		t.Fatalf("待办计数应与列表一致，实际 %d", mine.Counts.TaskWait)
	}
	if mine.Tasks[0].StatusLabel == "" {
		t.Fatal("工作台的条目应带上状态中文名")
	}

	// outsider 什么项目都不是成员，工作台应当是空的 —— 而不是把
	// 别人的任务当成「我的」列出来。
	other, err := h.workbench.Get(asUser(h.ctx, outsider, true))
	if err != nil {
		t.Fatalf("取工作台失败: %v", err)
	}
	if len(other.Tasks) != 0 || len(other.Bugs) != 0 || other.Counts.TaskWait != 0 {
		t.Fatalf("outsider 的工作台应为空，实际 %+v", other)
	}
}

// TestWorkbenchListsMyBugsAndReviews 验证工作台把「等我动手」的几类东西都列出来。
func TestWorkbenchListsMyBugsAndReviews(t *testing.T) {
	h := newHarness(t)
	productID := h.createProduct("工作台产品H", "WBH", model.ACLOpen, []uint{reviewer})

	bug := h.createBug(dto.BugReq{
		ProductID: productID, Title: "工作台里的缺陷", AssignedTo: reviewer,
	})
	storyID := h.createStory(productID, "工作台里的需求", nil)
	if _, err := h.stories.Apply(h.ctx, storyID, pendingReview()); err != nil {
		t.Fatalf("提交评审失败: %v", err)
	}

	board, err := h.workbench.Get(asUser(h.ctx, reviewer, true))
	if err != nil {
		t.Fatalf("取工作台失败: %v", err)
	}
	if len(board.Bugs) != 1 || board.Bugs[0].ID != bug.ID {
		t.Fatalf("应列出派给我的缺陷，实际 %+v", board.Bugs)
	}
	if board.Counts.BugActive != 1 {
		t.Fatalf("激活缺陷计数应为 1，实际 %d", board.Counts.BugActive)
	}
	if len(board.Reviews) != 1 || board.Reviews[0].StoryID != storyID {
		t.Fatalf("应列出待我评审的需求，实际 %+v", board.Reviews)
	}
	if board.Counts.StoryReview != 1 {
		t.Fatalf("待评审计数应为 1，实际 %d", board.Counts.StoryReview)
	}
	// 通知也在这一个响应里，前端首屏不必再打一次请求。
	if board.Counts.NoticeUnread < 1 || len(board.Notices) < 1 {
		t.Fatalf("工作台应带上未读通知，实际 %d/%d", board.Counts.NoticeUnread, len(board.Notices))
	}

	// 别人给结论之后，这条需求不该继续占着待办位置。
	if _, err := h.stories.Apply(asUser(h.ctx, reviewer, true), storyID,
		service.ActionInput{Action: "pass", Result: model.ReviewPass}); err != nil {
		t.Fatalf("评审失败: %v", err)
	}
	if board, err = h.workbench.Get(asUser(h.ctx, reviewer, true)); err != nil {
		t.Fatalf("取工作台失败: %v", err)
	} else if len(board.Reviews) != 0 {
		t.Fatalf("已给过结论的需求不该再列为待评审，实际 %+v", board.Reviews)
	}
}

// TestActionListHidesOthersHistoryBehindVisibility 是 M5 补上的一道口子。
//
// 时间线接口原先没有任何可见性过滤：只要传 ?actorId=<别人>，
// 就能把别人的操作历史 —— 连同备注原文与字段变更 —— 整段翻出来。
func TestActionListHidesOthersHistoryBehindVisibility(t *testing.T) {
	h := newHarness(t)
	productID := h.createProduct("时间线私有产品", "TLSEC", model.ACLPrivate, []uint{reviewer})
	h.createStory(productID, "私有产品里的需求", nil)

	// 产品负责人能查到自己在这条需求上的历史。
	own, err := h.actions.List(h.ctx, dto.ActionFilterReq{ActorID: poUser})
	if err != nil {
		t.Fatalf("查自己的历史失败: %v", err)
	}
	if own.Total == 0 {
		t.Fatal("应能查到自己可见范围内的历史")
	}

	// 局外人按同一个人去查，应当一条都拿不到 —— 而不是拿到全部。
	other, err := h.actions.List(asUser(h.ctx, outsider, true), dto.ActionFilterReq{ActorID: poUser})
	if err != nil {
		t.Fatalf("查他人历史失败: %v", err)
	}
	if other.Total != 0 {
		t.Fatalf("不可见的对象不该出现在时间线里，实际 %d 条", other.Total)
	}
}

// ---------------------------------------------------------------------------
// 燃尽图
// ---------------------------------------------------------------------------

// TestBurndownRebuildsEachDayFromEffortHistory 验证燃尽图里的每一天
// 都是从当天的工时记录还原出来的。
//
// 这是燃尽图唯一说得通的算法：只看着任务当前的 Estimate/Left 画不出历史，
// 那只是「此刻」的快照。每条工时记录都写着「记完之后还剩多少」，
// 于是任意一天的剩余都能重放出来。
func TestBurndownRebuildsEachDayFromEffortHistory(t *testing.T) {
	h := newHarness(t)
	productID := h.createProduct("燃尽产品", "BURN", model.ACLOpen, nil)
	projectID := h.createProject("燃尽项目", "BP", productID)
	executionID := h.createExecution(projectID, "燃尽迭代")
	h.addProjectMember(projectID, reviewer)

	task := h.createTask(dto.TaskReq{
		ProjectID: projectID, ExecutionID: executionID, ProductID: productID,
		Name: "三天做完的活", Estimate: 10, AssignedTo: reviewer,
	})
	if task.Left != 10 {
		t.Fatalf("任务初始剩余应等于估算 10，实际 %.2f", task.Left)
	}

	// 曲线从三天前开始：把任务的创建时间挪到第一天，
	// 否则它会被算成「最后一天才出现的新增」。
	begin := truncateDay(time.Now().UTC()).AddDate(0, 0, -2)
	if err := h.db.Model(&model.Task{}).Where("id = ?", task.ID).
		Update("created_at", begin).Error; err != nil {
		t.Fatalf("调整任务创建时间失败: %v", err)
	}

	day := func(offset int) string {
		return begin.AddDate(0, 0, offset).Format("2006-01-02")
	}
	h.startTask(task.ID)
	h.recordEffort(task.ID, dto.EffortReq{Consumed: 2, Date: day(0)}) // 剩 8
	h.recordEffort(task.ID, dto.EffortReq{Consumed: 3, Date: day(1)}) // 剩 5

	board, err := h.workbench.Burndown(h.ctx, dto.BurndownReq{
		ProjectID: projectID, ExecutionID: executionID,
		Begin: day(0), End: day(2),
	})
	if err != nil {
		t.Fatalf("取燃尽图失败: %v", err)
	}

	if board.Scope != "execution" || board.ScopeID != executionID {
		t.Fatalf("范围应为该迭代，实际 %s/%d", board.Scope, board.ScopeID)
	}
	if len(board.Days) != 3 {
		t.Fatalf("应为 3 个数据点，实际 %d", len(board.Days))
	}
	if board.TotalEstimate != 10 || board.TotalConsumed != 5 {
		t.Fatalf("总估算/总消耗应为 10/5，实际 %.2f/%.2f",
			board.TotalEstimate, board.TotalConsumed)
	}

	// 第一天报完 2 小时之后剩 8，第二天剩 5，第三天没人报工沿用 5。
	wantRemaining := []float64{8, 5, 5}
	wantConsumed := []float64{2, 3, 0}
	for i := range board.Days {
		if board.Days[i].Remaining != wantRemaining[i] {
			t.Errorf("第 %d 天剩余应为 %.2f，实际 %.2f",
				i+1, wantRemaining[i], board.Days[i].Remaining)
		}
		if board.Days[i].Consumed != wantConsumed[i] {
			t.Errorf("第 %d 天消耗应为 %.2f，实际 %.2f",
				i+1, wantConsumed[i], board.Days[i].Consumed)
		}
	}
	// 理想线从 10 线性降到 0：三天两点步长是 5。
	if board.Days[0].Ideal != 10 || board.Days[1].Ideal != 5 || board.Days[2].Ideal != 0 {
		t.Fatalf("理想线应为 10/5/0，实际 %.2f/%.2f/%.2f",
			board.Days[0].Ideal, board.Days[1].Ideal, board.Days[2].Ideal)
	}
	// 第一天的新增就是那条任务的全部估算。
	if board.Days[0].Added != 10 {
		t.Fatalf("第一天应记入 10 小时的新增，实际 %.2f", board.Days[0].Added)
	}
}

// TestBurndownCountsTaskOnlyFromItsCreationDay 验证中途加进来的任务
// 从它被创建的那天才开始占用工作量。
//
// 若一上来就把全部任务算进去，曲线会在任务还不存在的时候就开始消耗，
// 而「迭代中途加了活」这件事也永远不会在图上表现出来。
func TestBurndownCountsTaskOnlyFromItsCreationDay(t *testing.T) {
	h := newHarness(t)
	productID := h.createProduct("燃尽产品B", "BURNB", model.ACLOpen, nil)
	projectID := h.createProject("燃尽项目B", "BPB", productID)

	begin := truncateDay(time.Now().UTC()).AddDate(0, 0, -3)
	day := func(offset int) string {
		return begin.AddDate(0, 0, offset).Format("2006-01-02")
	}

	// 第一天就有的任务。
	old := h.createTask(dto.TaskReq{ProjectID: projectID, Name: "一开始就有的活", Estimate: 4})
	if err := h.db.Model(&model.Task{}).Where("id = ?", old.ID).
		Update("created_at", begin).Error; err != nil {
		t.Fatalf("调整创建时间失败: %v", err)
	}
	// 第三天加进来的任务。
	fresh := h.createTask(dto.TaskReq{ProjectID: projectID, Name: "后来加进来的活", Estimate: 6})
	if err := h.db.Model(&model.Task{}).Where("id = ?", fresh.ID).
		Update("created_at", begin.AddDate(0, 0, 2)).Error; err != nil {
		t.Fatalf("调整创建时间失败: %v", err)
	}

	board, err := h.workbench.Burndown(h.ctx, dto.BurndownReq{
		ProjectID: projectID, Begin: day(0), End: day(3),
	})
	if err != nil {
		t.Fatalf("取燃尽图失败: %v", err)
	}
	if board.TaskCount != 2 || board.TotalEstimate != 10 {
		t.Fatalf("两条任务、总估算 10，实际 %d/%.2f", board.TaskCount, board.TotalEstimate)
	}

	// 前三天只有那 4 小时的活；第三天新增 6 小时，所以抬头到 10。
	want := []float64{4, 4, 10, 10}
	for i := range board.Days {
		if board.Days[i].Remaining != want[i] {
			t.Fatalf("第 %d 天剩余应为 %.2f，实际 %.2f（曲线把还没建的任务提前算进去了）",
				i+1, want[i], board.Days[i].Remaining)
		}
	}
	if board.Days[2].Added != 6 {
		t.Fatalf("第三天应记入 6 小时新增，实际 %.2f", board.Days[2].Added)
	}
}

// TestBurndownRequiresARangeAndVisibleScope 验证燃尽图的入参与可见性。
func TestBurndownRequiresARangeAndVisibleScope(t *testing.T) {
	h := newHarness(t)
	productID := h.createProduct("燃尽产品C", "BURNC", model.ACLPrivate, nil)
	projectID := h.createPrivateProject("燃尽项目C", "BPC", productID)

	// 既没给项目也没给迭代：无法确定画哪条曲线。
	if _, err := h.workbench.Burndown(h.ctx, dto.BurndownReq{}); err == nil {
		t.Fatal("既没有项目也没有迭代时应当报错")
	}
	// 没给日期、项目自身也没有起止日期：不该猜一个范围。
	if _, err := h.workbench.Burndown(h.ctx, dto.BurndownReq{ProjectID: projectID}); err == nil {
		t.Fatal("缺少起止日期时应当报错，而不是猜一个范围")
	}
	// 局外人看不到这个私有项目。
	if _, err := h.workbench.Burndown(asUser(h.ctx, outsider, true), dto.BurndownReq{
		ProjectID: projectID, Begin: "2026-01-01", End: "2026-01-03",
	}); err == nil {
		t.Fatal("局外人不该拿到私有项目的燃尽图")
	}
}

// truncateDay 把时间截到当天零点（与 service 内部的口径一致）。
func truncateDay(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}
