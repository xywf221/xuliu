package service_test

// 测试单与执行记录的回归。
//
// 这一层最容易出错的地方不是 CRUD，而是三处「状态之间的牵连」：
//   1. 纳入用例时锁版本 —— 用例后来改了，单里的记录要说得清当时用的是哪版；
//   2. 一次执行要同时更新执行记录、单内用例状态、测试单最后执行时间，
//      少更新一处就会出现「跑过了但列表还显示未执行」；
//   3. 结单必须给结论，且有执行记录的单不能删。

import (
	"testing"

	"github.com/xywf221/xuliu/internal/dto"
	"github.com/xywf221/xuliu/internal/model"
	"github.com/xywf221/xuliu/internal/pkg/apperr"
)

// confirmedCase 建一条并已评审通过的用例（测试单只收这种）。
func (h *harness) confirmedCase(productID uint, title string) uint {
	h.t.Helper()
	created := h.createCase(dto.TestCaseReq{ProductID: productID, Title: title, Steps: twoSteps()})
	if _, err := h.cases.Apply(h.ctx, created.ID, "submitreview", ""); err != nil {
		h.t.Fatalf("提交用例评审失败: %v", err)
	}
	if _, err := h.cases.Apply(h.ctx, created.ID, "pass", ""); err != nil {
		h.t.Fatalf("用例评审通过失败: %v", err)
	}
	return created.ID
}

// createTask 是建测试单的测试助手。
func (h *harness) createTestTask(req dto.TestTaskReq) *dto.TestTaskResp {
	h.t.Helper()
	created, err := h.testTasks.Create(h.ctx, req)
	if err != nil {
		h.t.Fatalf("建测试单失败: %v", err)
	}
	return created
}

// TestTestTaskLifecycle 走一遍「建单 → 纳入 → 开始 → 执行 → 结单」。
func TestTestTaskLifecycle(t *testing.T) {
	h := newHarness(t)
	productID := h.createProduct("测试单生命周期", "TASKLC", model.ACLOpen, nil)
	caseID := h.confirmedCase(productID, "登录失败提示")

	task := h.createTestTask(dto.TestTaskReq{ProductID: productID, Name: "v1.0 登录回归"})
	if task.Status != model.TestTaskStatusWait {
		t.Fatalf("新建测试单应是 wait，实际 %s", task.Status)
	}

	// 纳入用例。
	if _, err := h.testTasks.AddCases(h.ctx, task.ID, []uint{caseID}); err != nil {
		t.Fatalf("纳入用例失败: %v", err)
	}
	detail, err := h.testTasks.Get(h.ctx, task.ID)
	if err != nil {
		t.Fatalf("查详情失败: %v", err)
	}
	if detail.CaseCount != 1 || len(detail.Cases) != 1 {
		t.Fatalf("应纳入 1 条用例，实际 caseCount=%d 清单 %d 条", detail.CaseCount, len(detail.Cases))
	}
	if detail.Cases[0].CaseVersion != 1 {
		t.Errorf("纳入时应锁定用例版本 1，实际 %d", detail.Cases[0].CaseVersion)
	}

	// 重复纳入是幂等的：不该报错，也不该把计数加到 2。
	if _, err := h.testTasks.AddCases(h.ctx, task.ID, []uint{caseID}); errCode(err) != apperr.TestTaskCaseExists {
		t.Errorf("重复纳入全部用例应提示已在单内，实际 %v", err)
	}
	if after := h.taskCaseCount(task.ID); after != 1 {
		t.Errorf("重复纳入后用例数应仍是 1，实际 %d", after)
	}

	// 未开始的单不能直接阻塞（没有「阻塞」可言）。
	if _, err := h.testTasks.Apply(h.ctx, task.ID, "block", ""); errCode(err) != apperr.TestTaskStatusInvalid {
		t.Errorf("wait 状态不应能 block，实际 %v", err)
	}
	if _, err := h.testTasks.Apply(h.ctx, task.ID, "start", ""); err != nil {
		t.Fatalf("开始测试单失败: %v", err)
	}
	if status := h.taskStatus(task.ID); status != model.TestTaskStatusDoing {
		t.Fatalf("开始后应是 doing，实际 %s", status)
	}

	// 记录一次失败。
	run, err := h.testTasks.RecordRun(h.ctx, task.ID, caseID, dto.TestRunReq{
		Result: model.TestRunResultFail,
		Note:   "第 2 步提示文案不对",
		Steps: []dto.TestRunStepReq{
			{Order: 2, Actual: "提示的是「登录失败」而不是「口令不正确」"},
		},
	})
	if err != nil {
		t.Fatalf("记录执行结果失败: %v", err)
	}
	// 步骤的操作/期望由后端回填，不采信客户端传来的副本。
	if len(run.Steps) != 2 || run.Steps[0].Step != "打开登录页" {
		t.Fatalf("执行快照应有 2 步且含原文，实际 %+v", run.Steps)
	}
	if run.Steps[1].Actual == "" {
		t.Errorf("第 2 步的实际结果应被记录，实际 %+v", run.Steps[1])
	}

	// 三处状态要一起更新。
	if summary := h.taskSummary(task.ID); summary.Fail != 1 || summary.Total != 1 {
		t.Errorf("单内用例汇总应是 fail=1，实际 %+v", summary)
	}
	if h.taskLastRunAt(task.ID) == "" {
		t.Errorf("测试单的最后执行时间应被推上去")
	}

	// 复测通过。
	if _, err := h.testTasks.RecordRun(h.ctx, task.ID, caseID, dto.TestRunReq{
		Result: model.TestRunResultPass,
	}); err != nil {
		t.Fatalf("复测记录失败: %v", err)
	}
	if summary := h.taskSummary(task.ID); summary.Pass != 1 || summary.Fail != 0 {
		t.Errorf("复测后应 pass=1/fail=0，实际 %+v", summary)
	}
	// 历次结果都在，没有被后一次覆盖掉。
	runs, err := h.testTasks.ListRuns(h.ctx, dto.TestRunFilterReq{TaskID: task.ID, CaseID: caseID})
	if err != nil {
		t.Fatalf("查执行历史失败: %v", err)
	}
	if runs.Total != 2 {
		t.Fatalf("应有 2 条执行记录，实际 %d", runs.Total)
	}

	// 没给结论就结单，必须被拒。
	if _, err := h.testTasks.Finish(h.ctx, task.ID, dto.TestTaskFinishReq{}); errCode(err) != apperr.TestTaskResultRequired {
		t.Errorf("结单缺结论应被拒，实际 %v", err)
	}
	if _, err := h.testTasks.Finish(h.ctx, task.ID, dto.TestTaskFinishReq{
		Result: model.TestTaskResultFail, Report: "有一条用例首跑失败，修复后复测通过",
	}); err != nil {
		t.Fatalf("结单失败: %v", err)
	}
	closed, err := h.testTasks.Get(h.ctx, task.ID)
	if err != nil {
		t.Fatalf("查详情失败: %v", err)
	}
	if closed.Status != model.TestTaskStatusClosed || closed.Result != model.TestTaskResultFail {
		t.Fatalf("结单后应是 closed/fail，实际 %s/%s", closed.Status, closed.Result)
	}

	// 结了的单不再接受执行结果。
	if _, err := h.testTasks.RecordRun(h.ctx, task.ID, caseID, dto.TestRunReq{
		Result: model.TestRunResultPass,
	}); errCode(err) != apperr.TestTaskStatusInvalid {
		t.Errorf("已结单不应能记录结果，实际 %v", err)
	}

	// 重开再结一次。
	if _, err := h.testTasks.Apply(h.ctx, task.ID, "activate", "补一轮回归"); err != nil {
		t.Fatalf("重开测试单失败: %v", err)
	}
	if status := h.taskStatus(task.ID); status != model.TestTaskStatusDoing {
		t.Fatalf("重开后应是 doing，实际 %s", status)
	}

	// 有执行记录的单不能删。
	if err := h.testTasks.Delete(h.ctx, task.ID); errCode(err) != apperr.TestTaskHasRuns {
		t.Errorf("有执行记录应拒绝删除，实际 %v", err)
	}
}

// TestTestTaskRejectsUnconfirmedCase 验证「只收评审通过的用例」。
func TestTestTaskRejectsUnconfirmedCase(t *testing.T) {
	h := newHarness(t)
	productID := h.createProduct("测试单收单校验", "TASKCONF", model.ACLOpen, nil)

	// 只建不评审。
	draft := h.createCase(dto.TestCaseReq{ProductID: productID, Title: "还没评审", Steps: twoSteps()})
	task := h.createTestTask(dto.TestTaskReq{ProductID: productID, Name: "不该收到未评审用例"})

	if _, err := h.testTasks.AddCases(h.ctx, task.ID, []uint{draft.ID}); errCode(err) != apperr.TestTaskCaseNotConfirmed {
		t.Fatalf("未确认的用例应被拒，实际 %v", err)
	}
	// 被拒后不能留下半个状态。
	if after := h.taskCaseCount(task.ID); after != 0 {
		t.Errorf("被拒后用例数应为 0，实际 %d", after)
	}

	// 建单时一并带上也一样被拒。
	if _, err := h.testTasks.Create(h.ctx, dto.TestTaskReq{
		ProductID: productID, Name: "建单时带未确认用例", CaseIDs: []uint{draft.ID},
	}); errCode(err) != apperr.TestTaskCaseNotConfirmed {
		t.Fatalf("建单时带未确认用例也应被拒，实际 %v", err)
	}
}

// TestTestTaskCaseVersionLocking 验证「用例改了之后，单里仍记着当时用的版本」。
func TestTestTaskCaseVersionLocking(t *testing.T) {
	h := newHarness(t)
	productID := h.createProduct("版本锁定", "TASKVER", model.ACLOpen, nil)
	caseID := h.confirmedCase(productID, "会被改的用例")

	task := h.createTestTask(dto.TestTaskReq{ProductID: productID, Name: "版本对照单"})
	if _, err := h.testTasks.AddCases(h.ctx, task.ID, []uint{caseID}); err != nil {
		t.Fatalf("纳入用例失败: %v", err)
	}

	// 改标题即换版：用例现在第 2 版（同时被打了回待评审，见用例域的测试）。
	if _, err := h.cases.Update(h.ctx, caseID, dto.TestCaseReq{
		ProductID: productID, Title: "改过的用例", Steps: twoSteps(),
	}); err != nil {
		t.Fatalf("改用例失败: %v", err)
	}

	detail, err := h.testTasks.Get(h.ctx, task.ID)
	if err != nil {
		t.Fatalf("查详情失败: %v", err)
	}
	if len(detail.Cases) != 1 {
		t.Fatalf("应有 1 条单内用例，实际 %d", len(detail.Cases))
	}
	got := detail.Cases[0]
	if got.CaseVersion != 1 {
		t.Errorf("单内应仍记着纳入时的版本 1，实际 %d", got.CaseVersion)
	}
	// 同时给出此刻的版本，前端才能提示「结果是对着旧版跑的」。
	if got.CaseCurrentVersion != 2 {
		t.Errorf("应同时给出用例此刻版本 2，实际 %d", got.CaseCurrentVersion)
	}
}

// TestTestTaskGuards 覆盖建单时的归属校验与可见性。
func TestTestTaskGuards(t *testing.T) {
	h := newHarness(t)
	productA := h.createProduct("测试单产品A", "TASKA", model.ACLOpen, nil)
	productB := h.createProduct("测试单产品B", "TASKB", model.ACLOpen, nil)
	privateID := h.createProduct("私有的测试单产品", "TASKP", model.ACLPrivate, nil)
	_ = h.confirmedCase(productB, "B 的用例")

	// 产品不存在。
	if _, err := h.testTasks.Create(h.ctx, dto.TestTaskReq{ProductID: 9999, Name: "无产品"}); errCode(err) != apperr.ProductNotFound {
		t.Errorf("产品不存在应报 ProductNotFound，实际 %v", err)
	}
	// 名称为空。
	if _, err := h.testTasks.Create(h.ctx, dto.TestTaskReq{ProductID: productA, Name: "   "}); errCode(err) != apperr.InvalidParam {
		t.Errorf("空名称应被拒，实际 %v", err)
	}
	// 用例来自别的产品：必须拒。这张单的报告只替自己那个产品说话，
	// 混进别的产品用例会让它给一个没被测过的产品背书。
	otherTask := h.createTestTask(dto.TestTaskReq{ProductID: productA, Name: "A 的单"})
	caseInB, err := h.cases.List(h.ctx, dto.TestCaseFilterReq{ProductID: productB})
	if err != nil || caseInB.Total == 0 {
		t.Fatalf("准备 B 产品的用例失败: %v", err)
	}
	if _, err := h.testTasks.AddCases(h.ctx, otherTask.ID, []uint{caseInB.List[0].ID}); errCode(err) != apperr.InvalidParam {
		t.Fatalf("跨产品纳入用例应被拒，实际 %v", err)
	}
	if after := h.taskCaseCount(otherTask.ID); after != 0 {
		t.Errorf("被拒后不应留下半个状态，实际用例数 %d", after)
	}

	// 私有产品的单：路人列表里看不到、详情打不开、也加不进用例。
	privateTask := h.createTestTask(dto.TestTaskReq{ProductID: privateID, Name: "私有单"})
	outsiderCtx := asUser(h.ctx, outsider, false)
	listed, err := h.testTasks.List(outsiderCtx, dto.TestTaskFilterReq{})
	if err != nil {
		t.Fatalf("路人查测试单列表失败: %v", err)
	}
	for _, item := range listed.List {
		if item.ProductID == privateID {
			t.Fatalf("路人不应看到私有产品下的测试单")
		}
	}
	if _, err := h.testTasks.Get(outsiderCtx, privateTask.ID); errCode(err) != apperr.ProductAccessDenied {
		t.Errorf("路人应打不开私有产品的测试单，实际 %v", err)
	}
}

// TestTestTaskNotifications 验证「通知下一个要动手的人」。
func TestTestTaskNotifications(t *testing.T) {
	h := newHarness(t)
	productID := h.createProduct("测试单通知", "TASKNT", model.ACLOpen, nil)
	h.createTestTask(dto.TestTaskReq{ProductID: productID, Name: "未指派不该发通知"})
	if count := h.noticeCount(reviewer); count != 0 {
		t.Errorf("没指派就不该有通知，实际 %d 条", count)
	}

	task := h.createTestTask(dto.TestTaskReq{ProductID: productID, Name: "要指派的单"})
	if _, err := h.testTasks.Assign(h.ctx, task.ID, reviewer, ""); err != nil {
		t.Fatalf("指派失败: %v", err)
	}
	if count := h.noticeCount(reviewer); count != 1 {
		t.Errorf("指派后负责人应有 1 条待办，实际 %d 条", count)
	}
}

// ---------------------------------------------------------------------------
// 测试助手：直接读库，绕过出参装配
// ---------------------------------------------------------------------------

func (h *harness) taskStatus(id uint) string {
	h.t.Helper()
	var status string
	if err := h.db.Model(&model.TestTask{}).Where("id = ?", id).
		Select("status").Scan(&status).Error; err != nil {
		h.t.Fatalf("查 status 失败: %v", err)
	}
	return status
}

func (h *harness) taskCaseCount(id uint) int {
	h.t.Helper()
	var count int
	if err := h.db.Model(&model.TestTask{}).Where("id = ?", id).
		Select("case_count").Scan(&count).Error; err != nil {
		h.t.Fatalf("查 case_count 失败: %v", err)
	}
	return count
}

func (h *harness) taskLastRunAt(id uint) string {
	h.t.Helper()
	var at *string
	if err := h.db.Model(&model.TestTask{}).Where("id = ?", id).
		Select("last_run_date").Scan(&at).Error; err != nil {
		h.t.Fatalf("查 last_run_date 失败: %v", err)
	}
	if at == nil {
		return ""
	}
	return *at
}

func (h *harness) taskSummary(id uint) dto.TestTaskSummary {
	h.t.Helper()
	detail, err := h.testTasks.Get(h.ctx, id)
	if err != nil {
		h.t.Fatalf("查测试单详情失败: %v", err)
	}
	return detail.Summary
}

func (h *harness) noticeCount(to uint) int {
	h.t.Helper()
	var count int64
	if err := h.db.Model(&model.Notice{}).Where("to_user = ?", to).Count(&count).Error; err != nil {
		h.t.Fatalf("查通知失败: %v", err)
	}
	return int(count)
}
