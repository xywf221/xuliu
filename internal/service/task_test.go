package service_test

import (
	"testing"
	"time"

	"github.com/xywf221/xuliu/internal/dto"
	"github.com/xywf221/xuliu/internal/model"
	"github.com/xywf221/xuliu/internal/pkg/apperr"
	"github.com/xywf221/xuliu/internal/service"
)

// 本文件覆盖 M3 的「项目 → 迭代 → 任务 → 工时 → 进度」这条主链路。
//
// 重点不是「接口能调通」，而是几条口径一致：工时三元组在任务、迭代、
// 项目三层必须互相对得上，进度必须是同一个公式算出来的，
// 而「完成」与「还剩工时」不能同时成立。

// ---------------------------------------------------------------------------
// 测试脚手架
// ---------------------------------------------------------------------------

// createProject 建一个测试项目（关联给定的产品）。
func (h *harness) createProject(name, code string, productIDs ...uint) uint {
	h.t.Helper()
	multiple := true
	project, err := h.projects.Create(h.ctx, dto.ProjectReq{
		Name: name, Code: code, Model: model.ProjectModelScrum,
		ACL: model.ACLOpen, PO: poUser, PM: poUser,
		ProductIDs: productIDs, Multiple: &multiple,
	})
	if err != nil {
		h.t.Fatalf("建项目失败: %v", err)
	}
	return project.ID
}

// createExecution 在项目下建一个迭代。
func (h *harness) createExecution(projectID uint, name string) uint {
	h.t.Helper()
	execution, err := h.executions.Create(h.ctx, dto.ExecutionReq{
		ProjectID: projectID, Name: name, Type: model.ExecutionTypeSprint,
	})
	if err != nil {
		h.t.Fatalf("建迭代失败: %v", err)
	}
	return execution.ID
}

// addProjectMember 把用户加入项目团队。
func (h *harness) addProjectMember(projectID, userID uint) {
	h.t.Helper()
	if err := h.projects.AddTeam(h.ctx, projectID, dto.TeamMemberReq{
		UserID: userID, Role: "dev",
	}); err != nil {
		h.t.Fatalf("加项目成员失败: %v", err)
	}
}

// createTask 建一个任务并要求成功。
func (h *harness) createTask(req dto.TaskReq) *dto.TaskResp {
	h.t.Helper()
	task, err := h.tasks.Create(h.ctx, req)
	if err != nil {
		h.t.Fatalf("建任务失败: %v", err)
	}
	return task
}

// mustProject/mustExecution 取项目与迭代的当前状态。
func (h *harness) mustProject(id uint) *dto.ProjectResp {
	h.t.Helper()
	project, err := h.projects.Get(h.ctx, id)
	if err != nil {
		h.t.Fatalf("取项目失败: %v", err)
	}
	return project
}

func (h *harness) mustExecution(id uint) *dto.ExecutionResp {
	h.t.Helper()
	execution, err := h.executions.Get(h.ctx, id)
	if err != nil {
		h.t.Fatalf("取迭代失败: %v", err)
	}
	return execution
}

// startTask 把任务推进到「进行中」。
func (h *harness) startTask(id uint) *dto.TaskResp {
	h.t.Helper()
	task, err := h.tasks.Apply(h.ctx, id, service.TaskActionInput{Action: "start"})
	if err != nil {
		h.t.Fatalf("开始任务失败: %v", err)
	}
	return task
}

// recordEffort 记一笔工时并要求成功。
func (h *harness) recordEffort(id uint, req dto.EffortReq) *dto.TaskResp {
	h.t.Helper()
	task, err := h.tasks.RecordEffort(h.ctx, id, req)
	if err != nil {
		h.t.Fatalf("登记工时失败: %v", err)
	}
	return task
}

// ---------------------------------------------------------------------------
// 工时与进度
// ---------------------------------------------------------------------------

// TestTaskHoursRollUpToProject 走一遍「建任务 → 开始 → 报工」，
// 校验任务、迭代、项目三层的工时与进度完全一致。
func TestTaskHoursRollUpToProject(t *testing.T) {
	h := newHarness(t)
	productID := h.createProduct("产品A", "PA", model.ACLOpen, nil)
	projectID := h.createProject("项目A", "PRJA", productID)
	executionID := h.createExecution(projectID, "迭代1")

	task := h.createTask(dto.TaskReq{
		ProjectID: projectID, ExecutionID: executionID, ProductID: productID,
		Name: "实现登录", Estimate: 8, AssignedTo: poUser,
	})
	// 没给剩余工时时默认等于预计工时：新任务「还剩多少」就是「预计多少」。
	if task.Estimate != 8 || task.Left != 8 || task.Consumed != 0 {
		t.Fatalf("新任务工时三元组应为 8/0/8，实际 %v/%v/%v",
			task.Estimate, task.Consumed, task.Left)
	}
	if task.Status != model.TaskStatusWait || task.Progress != 0 {
		t.Fatalf("新任务应为未开始且进度 0，实际 %s/%d", task.Status, task.Progress)
	}

	task = h.startTask(task.ID)
	if task.Status != model.TaskStatusDoing || task.RealStarted == "" {
		t.Fatalf("开始后应为进行中并有实际开始时间，实际 %s/%q", task.Status, task.RealStarted)
	}

	// 报了 3 小时、还剩 5 小时：进度按 3/8 算，四舍五入到 38。
	task = h.recordEffort(task.ID, dto.EffortReq{Consumed: 3, Work: "写登录页"})
	if task.Consumed != 3 || task.Left != 5 || task.Progress != 38 {
		t.Fatalf("报工后应为 已消耗3/剩余5/进度38，实际 %v/%v/%d",
			task.Consumed, task.Left, task.Progress)
	}
	if len(task.Efforts) != 1 {
		t.Fatalf("详情应带回 1 条工时记录，实际 %d", len(task.Efforts))
	}

	// 迭代与项目是任务工时的聚合值，三层必须一致。
	execution := h.mustExecution(executionID)
	if execution.TaskHours.Estimate != 8 || execution.TaskHours.Consumed != 3 ||
		execution.TaskHours.Left != 5 {
		t.Fatalf("迭代工时应为 8/3/5，实际 %+v", execution.TaskHours)
	}
	if execution.Progress != 38 {
		t.Fatalf("迭代进度应为 38，实际 %d", execution.Progress)
	}

	project := h.mustProject(projectID)
	if project.Estimate != 8 || project.Consumed != 3 || project.Left != 5 ||
		project.Progress != 38 {
		t.Fatalf("项目工时应为 8/3/5 且进度 38，实际 %v/%v/%v/%d",
			project.Estimate, project.Consumed, project.Left, project.Progress)
	}

	// 工时是审计数据，任务被删掉它也就没了意义 —— 所以有工时的任务不能删。
	if err := h.tasks.Delete(h.ctx, task.ID); errCode(err) != apperr.Conflict {
		t.Fatalf("有工时记录的任务应拒绝删除，实际 %v", err)
	}
}

// TestFinishNeedsZeroLeft 验证「完成」与「还剩工时」不能并存，
// 以及「最后一次报工顺手完成」这个最常见的用法能用。
func TestFinishNeedsZeroLeft(t *testing.T) {
	h := newHarness(t)
	productID := h.createProduct("产品B", "PB", model.ACLOpen, nil)
	projectID := h.createProject("项目B", "PRJB", productID)

	task := h.createTask(dto.TaskReq{
		ProjectID: projectID, Name: "改造缓存", Estimate: 5, AssignedTo: poUser,
	})
	task = h.startTask(task.ID)

	// 还剩 5 小时就想完成：进度会算出「已完成但 0%」，必须挡住。
	if _, err := h.tasks.Apply(h.ctx, task.ID, service.TaskActionInput{Action: "finish"}); errCode(err) != apperr.TaskLeftNotZero {
		t.Fatalf("剩余工时不为 0 时应拒绝完成，实际 %v", err)
	}

	// 一次请求里「报完剩余的 5 小时 + 完成」，是最常见的用法。
	done, err := h.tasks.Apply(h.ctx, task.ID, service.TaskActionInput{
		Action: "finish", Consumed: 5, Comment: "收尾",
	})
	if err != nil {
		t.Fatalf("报完工时并完成应成功: %v", err)
	}
	if done.Status != model.TaskStatusDone || done.Left != 0 ||
		done.Consumed != 5 || done.Progress != 100 {
		t.Fatalf("完成后应为 已完成/剩余0/已消耗5/进度100，实际 %s/%v/%v/%d",
			done.Status, done.Left, done.Consumed, done.Progress)
	}

	project := h.mustProject(projectID)
	if project.Progress != 100 || project.Left != 0 {
		t.Fatalf("任务完成后项目进度应为 100 且无剩余，实际 %d/%v",
			project.Progress, project.Left)
	}
}

// TestEffortOverrunRequiresExplicitLeft 验证超支报工必须显式给出剩余工时。
//
// 允许「本次消耗 > 剩余」是必要的（活比想的多），但结果不能是负数：
// 那一刻用户其实是在重新估算，就该把新的剩余值填进来。
func TestEffortOverrunRequiresExplicitLeft(t *testing.T) {
	h := newHarness(t)
	productID := h.createProduct("产品C", "PC", model.ACLOpen, nil)
	projectID := h.createProject("项目C", "PRJC", productID)

	task := h.startTask(h.createTask(dto.TaskReq{
		ProjectID: projectID, Name: "排查线上问题", Estimate: 3, AssignedTo: poUser,
	}).ID)

	if _, err := h.tasks.RecordEffort(h.ctx, task.ID, dto.EffortReq{Consumed: 5}); errCode(err) != apperr.TaskEffortExceedsLeft {
		t.Fatalf("超支且未给剩余工时应报 task_effort_exceeds_left，实际 %v", err)
	}

	left := 7.0
	task = h.recordEffort(task.ID, dto.EffortReq{Consumed: 5, Left: &left})
	if task.Consumed != 5 || task.Left != 7 {
		t.Fatalf("显式给出剩余工时应按它落库，实际 已消耗%v/剩余%v",
			task.Consumed, task.Left)
	}
	// 进度按「重新估算后」的口径算：5/(5+7) ≈ 42。
	if task.Progress != 42 {
		t.Fatalf("进度应为 42，实际 %d", task.Progress)
	}
}

// TestEffortRequiresDoing 验证只有进行中的任务能报工。
func TestEffortRequiresDoing(t *testing.T) {
	h := newHarness(t)
	productID := h.createProduct("产品D", "PD", model.ACLOpen, nil)
	projectID := h.createProject("项目D", "PRJD", productID)

	task := h.createTask(dto.TaskReq{
		ProjectID: projectID, Name: "未开工的任务", Estimate: 2, AssignedTo: poUser,
	})
	if _, err := h.tasks.RecordEffort(h.ctx, task.ID, dto.EffortReq{Consumed: 1}); errCode(err) != apperr.TaskStatusInvalid {
		t.Fatalf("未开始的任务应拒绝报工，实际 %v", err)
	}
}

// ---------------------------------------------------------------------------
// 多人任务
// ---------------------------------------------------------------------------

// TestMultiTaskAggregatesMembers 验证多人任务的工时是成员分工的汇总，
// 且只有成员分工里的人能报工。
func TestMultiTaskAggregatesMembers(t *testing.T) {
	h := newHarness(t)
	productID := h.createProduct("产品E", "PE", model.ACLOpen, nil)
	projectID := h.createProject("项目E", "PRJE", productID)
	h.addProjectMember(projectID, reviewer)
	h.addProjectMember(projectID, outsider)

	task := h.createTask(dto.TaskReq{
		ProjectID: projectID, Name: "多人并行任务",
		Mode: model.TaskModeMulti,
		Team: []dto.TaskTeamReq{
			{UserID: reviewer, Estimate: 4, Left: 4},
			{UserID: outsider, Estimate: 6, Left: 6},
		},
	})
	if task.Estimate != 10 || task.Left != 10 {
		t.Fatalf("多人任务工时应为成员之和 10/10，实际 %v/%v", task.Estimate, task.Left)
	}
	task = h.startTask(task.ID)

	// 不在成员分工里的用户不能报工 —— 那种工时会挂不上任何分工行。
	if _, err := h.tasks.RecordEffort(h.ctx, task.ID, dto.EffortReq{Consumed: 1}); errCode(err) != apperr.TaskNotAssigned {
		t.Fatalf("非成员报工应报 task_not_assigned，实际 %v", err)
	}

	task = h.recordEffort(task.ID, dto.EffortReq{Consumed: 2, UserID: reviewer})
	if task.Consumed != 2 || task.Left != 8 {
		t.Fatalf("成员报 2 小时后任务应为 已消耗2/剩余8，实际 %v/%v",
			task.Consumed, task.Left)
	}
	if task.Progress != 20 {
		t.Fatalf("任务进度应为 20，实际 %d", task.Progress)
	}
	if len(task.Team) != 2 {
		t.Fatalf("详情应带回 2 位成员，实际 %d", len(task.Team))
	}
	// 成员各自看自己的进度：reviewer 报了 2 小时、还剩 2 小时 → 50。
	for _, m := range task.Team {
		if m.UserID == reviewer && (m.Consumed != 2 || m.Left != 2 || m.Progress != 50) {
			t.Fatalf("成员 %d 应为 2/2/50，实际 %v/%v/%d",
				m.UserID, m.Consumed, m.Left, m.Progress)
		}
	}
}

// TestTaskAssigneeMustBeProjectMember 验证任务只能派给项目团队成员。
func TestTaskAssigneeMustBeProjectMember(t *testing.T) {
	h := newHarness(t)
	productID := h.createProduct("产品F", "PF", model.ACLOpen, nil)
	projectID := h.createProject("项目F", "PRJF", productID)

	// 建任务时就派给非成员，应当被拒。
	if _, err := h.tasks.Create(h.ctx, dto.TaskReq{
		ProjectID: projectID, Name: "派给外人的任务", AssignedTo: outsider,
	}); errCode(err) != apperr.TeamMemberNotInProject {
		t.Fatalf("派给非项目成员应报 team_member_not_in_project，实际 %v", err)
	}

	task := h.createTask(dto.TaskReq{
		ProjectID: projectID, Name: "内部任务", AssignedTo: poUser,
	})
	if _, err := h.tasks.Apply(h.ctx, task.ID, service.TaskActionInput{
		Action: "assign", AssignedTo: outsider,
	}); errCode(err) != apperr.TeamMemberNotInProject {
		t.Fatalf("指派给非项目成员应报 team_member_not_in_project，实际 %v", err)
	}

	// 加入团队后就能派了。
	h.addProjectMember(projectID, outsider)
	task, err := h.tasks.Apply(h.ctx, task.ID, service.TaskActionInput{
		Action: "assign", AssignedTo: outsider,
	})
	if err != nil {
		t.Fatalf("指派给项目成员应成功: %v", err)
	}
	if task.AssignedTo != outsider || task.AssignName == "" {
		t.Fatalf("指派后应为新负责人且带姓名，实际 %d/%q", task.AssignedTo, task.AssignName)
	}
}

// ---------------------------------------------------------------------------
// 删除与子任务
// ---------------------------------------------------------------------------

// TestDeleteTaskBlockedByChildren 验证有子任务的任务不能删。
func TestDeleteTaskBlockedByChildren(t *testing.T) {
	h := newHarness(t)
	productID := h.createProduct("产品G", "PG", model.ACLOpen, nil)
	projectID := h.createProject("项目G", "PRJG", productID)

	parent := h.createTask(dto.TaskReq{
		ProjectID: projectID, Name: "父任务", Estimate: 4, AssignedTo: poUser,
	})

	// 子任务挂在父任务下：直接把 parent_id 写进去（建接口里父子是分开的）。
	child := &model.Task{
		ProjectID: projectID, ParentID: parent.ID, Name: "子任务",
		Type: model.TaskTypeDevel, Mode: model.TaskModeSingle,
		Status: model.TaskStatusWait, Version: 1, Order: 1,
	}
	if err := h.db.Create(child).Error; err != nil {
		t.Fatalf("写子任务失败: %v", err)
	}
	if err := h.db.Model(&model.Task{}).Where("id = ?", parent.ID).
		Update("is_parent", true).Error; err != nil {
		t.Fatalf("标记父任务失败: %v", err)
	}

	if err := h.tasks.Delete(h.ctx, parent.ID); errCode(err) != apperr.TaskHasChildren {
		t.Fatalf("有子任务时删除应报 task_has_children，实际 %v", err)
	}
}

// TestCancelZeroesLeft 验证取消会把剩余工时清零。
//
// 不清零的话，一条砍掉的任务会一直挂在项目的「剩余工作量」里，
// 等于给一个不会做的工作留着预算。
func TestCancelZeroesLeft(t *testing.T) {
	h := newHarness(t)
	productID := h.createProduct("产品H", "PH", model.ACLOpen, nil)
	projectID := h.createProject("项目H", "PRJH", productID)

	task := h.createTask(dto.TaskReq{
		ProjectID: projectID, Name: "可能不做的任务", Estimate: 4, AssignedTo: poUser,
	})
	task, err := h.tasks.Apply(h.ctx, task.ID, service.TaskActionInput{
		Action: "cancel", Comment: "需求砍了",
	})
	if err != nil {
		t.Fatalf("取消任务应成功: %v", err)
	}
	if task.Status != model.TaskStatusCancel || task.Left != 0 {
		t.Fatalf("取消后应为 已取消/剩余0，实际 %s/%v", task.Status, task.Left)
	}
	if project := h.mustProject(projectID); project.Left != 0 {
		t.Fatalf("取消后项目剩余工时应为 0，实际 %v", project.Left)
	}
	if n := h.actionCount(model.ObjectTypeTask, task.ID, model.ActionCanceled); n != 1 {
		t.Fatalf("取消应留下 1 条操作历史，实际 %d", n)
	}
}

// ---------------------------------------------------------------------------
// 看板与列表
// ---------------------------------------------------------------------------

// TestKanbanGroupsByStatus 验证看板按状态分列，列头计数与工时一致。
func TestKanbanGroupsByStatus(t *testing.T) {
	h := newHarness(t)
	productID := h.createProduct("产品I", "PI", model.ACLOpen, nil)
	projectID := h.createProject("项目I", "PRJI", productID)
	executionID := h.createExecution(projectID, "迭代1")

	doing := h.startTask(h.createTask(dto.TaskReq{
		ProjectID: projectID, ExecutionID: executionID, Name: "进行中的任务",
		Estimate: 2, AssignedTo: poUser,
	}).ID)
	h.createTask(dto.TaskReq{
		ProjectID: projectID, ExecutionID: executionID, Name: "未开始的任务",
		Estimate: 4, AssignedTo: poUser,
	})

	board, err := h.tasks.Kanban(h.ctx, projectID, executionID)
	if err != nil {
		t.Fatalf("取看板失败: %v", err)
	}
	if board.Scope != "execution" {
		t.Fatalf("带迭代的看板范围应为 execution，实际 %s", board.Scope)
	}

	var doingCol *dto.KanbanColumn
	for i := range board.Columns {
		if board.Columns[i].Status == model.TaskStatusDoing {
			doingCol = &board.Columns[i]
		}
	}
	if doingCol == nil {
		t.Fatal("看板应包含「进行中」这一列")
	}
	if doingCol.Count != 1 || len(doingCol.Tasks) != 1 {
		t.Fatalf("进行中一列应有 1 条任务，实际 %d/%d", doingCol.Count, len(doingCol.Tasks))
	}
	if doingCol.Tasks[0].ID != doing.ID {
		t.Fatalf("进行中一列应装着刚开始的任务，实际 %d", doingCol.Tasks[0].ID)
	}
	if doingCol.Hours.Estimate != 2 {
		t.Fatalf("进行中一列的预计工时应为 2，实际 %v", doingCol.Hours.Estimate)
	}
}

// TestListTasksFiltersByProject 验证任务列表按项目过滤，且工时明细可查。
func TestListTasksFiltersByProject(t *testing.T) {
	h := newHarness(t)
	productID := h.createProduct("产品J", "PJ", model.ACLOpen, nil)
	projectA := h.createProject("项目JA", "PRJJA", productID)
	projectB := h.createProject("项目JB", "PRJJB", productID)

	h.createTask(dto.TaskReq{ProjectID: projectA, Name: "A 的任务", AssignedTo: poUser})
	taskB := h.createTask(dto.TaskReq{
		ProjectID: projectB, Name: "B 的任务", Estimate: 2, AssignedTo: poUser,
	})

	result, err := h.tasks.List(h.ctx, dto.TaskFilterReq{ProjectID: projectA})
	if err != nil {
		t.Fatalf("查任务列表失败: %v", err)
	}
	if result.Total != 1 || result.List[0].ProjectID != projectA {
		t.Fatalf("按项目过滤应只返回 A 的 1 条任务，实际 %d 条", result.Total)
	}

	// 「我的工时」按项目过滤时，要能查到每小时记录。
	doingB := h.startTask(taskB.ID)
	h.recordEffort(doingB.ID, dto.EffortReq{Consumed: 2, Date: "2026-03-01", Work: "搭环境"})
	efforts, err := h.tasks.ListEfforts(h.ctx, dto.EffortFilterReq{
		ProjectID: projectB, DateFrom: "2026-03-01", DateTo: "2026-03-31",
	})
	if err != nil {
		t.Fatalf("查工时明细失败: %v", err)
	}
	if efforts.Total != 1 || efforts.List[0].Work != "搭环境" {
		t.Fatalf("应查到 1 条工时记录，实际 %+v", efforts)
	}
	if efforts.List[0].Realname == "" {
		t.Fatal("工时记录应带上记录人姓名")
	}
}

// TestEditMultiTaskKeepsMembersWhenTeamOmitted 验证编辑多人任务时，
// 请求没带成员名单不会把成员和工时抹掉。
//
// 这是个很容易踩的坑：编辑接口是「整体覆盖」语义，多人任务的名单一旦
// 按「空 = 清空」处理，用户只改个截止日期就会连带丢掉成员分工，
// 而任务上的已消耗工时会随之归零 —— 那是不可逆的数据损坏。
func TestEditMultiTaskKeepsMembersWhenTeamOmitted(t *testing.T) {
	h := newHarness(t)
	productID := h.createProduct("产品U", "PU", model.ACLOpen, nil)
	projectID := h.createProject("项目U", "PRJU", productID)
	h.addProjectMember(projectID, reviewer)

	task := h.startTask(h.createTask(dto.TaskReq{
		ProjectID: projectID, Name: "多人任务",
		Mode: model.TaskModeMulti,
		Team: []dto.TaskTeamReq{{UserID: reviewer, Estimate: 4, Left: 4}},
	}).ID)
	task = h.recordEffort(task.ID, dto.EffortReq{Consumed: 1, UserID: reviewer})

	// 只改名称，不带 Team。
	updated, err := h.tasks.Update(h.ctx, task.ID, dto.TaskReq{
		ProjectID: projectID, Name: "改过名字的多人任务",
		Mode: model.TaskModeMulti, Deadline: "2026-12-31",
	})
	if err != nil {
		t.Fatalf("编辑多人任务失败: %v", err)
	}
	if updated.Name != "改过名字的多人任务" {
		t.Fatalf("名称应被改掉，实际 %q", updated.Name)
	}
	if len(updated.Team) != 1 || updated.Team[0].UserID != reviewer {
		t.Fatalf("未带名单时应保留原成员分工，实际 %+v", updated.Team)
	}
	if updated.Consumed != 1 || updated.Estimate != 4 || updated.Left != 3 {
		t.Fatalf("工时不应被清零：应为 4/1/3，实际 %v/%v/%v",
			updated.Estimate, updated.Consumed, updated.Left)
	}

	// 改回单人任务：成员分工被清掉，工时留在任务上。
	single, err := h.tasks.Update(h.ctx, task.ID, dto.TaskReq{
		ProjectID: projectID, Name: "改成单人的任务", Mode: model.TaskModeSingle,
		Estimate: 4, Left: floatPtr(3),
	})
	if err != nil {
		t.Fatalf("改为单人任务失败: %v", err)
	}
	if single.Mode != model.TaskModeSingle || len(single.Team) != 0 {
		t.Fatalf("改回单人后不应再有成员分工，实际 %+v", single.Team)
	}
	if single.Consumed != 1 {
		t.Fatalf("改回单人后已消耗工时应保留，实际 %v", single.Consumed)
	}
	var teamRows int64
	if err := h.db.Model(&model.TaskTeam{}).Where("task_id = ?", task.ID).
		Count(&teamRows).Error; err != nil {
		t.Fatalf("统计成员分工失败: %v", err)
	}
	if teamRows != 0 {
		t.Fatalf("改回单人后成员分工行应被删除，实际剩 %d 行", teamRows)
	}
}

// floatPtr 返回浮点数的指针，用于「显式给出剩余工时」这类可空字段。
func floatPtr(v float64) *float64 { return &v }

// ---------------------------------------------------------------------------
// 可见性：项目之外还有产品这一层
// ---------------------------------------------------------------------------

// TestTaskInOpenProjectStillNeedsProductAccess 验证「项目公开」不等于
// 「项目里那些私有产品的任务也公开」。
//
// 这是一个真实存在过的窟窿：需求列表按可见产品过滤，任务列表只看项目，
// 于是同一份私有工作会从任务这一侧漏出去 —— 需求看不见，需求拆出来的
// 任务却连名字带描述都能读。列表与详情必须一起改，否则只是把窟窿挪个位置。
func TestTaskInOpenProjectStillNeedsProductAccess(t *testing.T) {
	h := newHarness(t)
	privateProduct := h.createProduct("私有产品T", "PRIVT", model.ACLPrivate, nil)
	// 公开项目：人人可见。
	projectID := h.createProject("公开项目T", "OPENT", privateProduct)

	hidden := h.createTask(dto.TaskReq{
		ProjectID: projectID, ProductID: privateProduct,
		Name: "私有产品下的实现任务",
	})
	plain := h.createTask(dto.TaskReq{ProjectID: projectID, Name: "内部事务任务"})

	outsiderCtx := asUser(h.ctx, outsider, false)
	listed, err := h.tasks.List(outsiderCtx, dto.TaskFilterReq{})
	if err != nil {
		t.Fatalf("路人查任务列表失败: %v", err)
	}
	// 与产品无关的那条照常可见 —— 它不属于任何产品，没有产品层可判。
	if listed.Total != 1 || listed.List[0].ID != plain.ID {
		t.Fatalf("路人应只看到不挂产品的那条任务，实际 %d 条", listed.Total)
	}
	if _, err := h.tasks.Get(outsiderCtx, hidden.ID); errCode(err) != apperr.ProductAccessDenied {
		t.Fatalf("路人应打不开私有产品下的任务，实际 %v", err)
	}
	// 看得到产品的人（这里是它的负责人）照常能用。
	if _, err := h.tasks.Get(h.ctx, hidden.ID); err != nil {
		t.Fatalf("产品负责人应能打开该任务，实际 %v", err)
	}
}

// TestProjectMemberSeesLinkedPrivateProduct 验证「在项目里干活的人
// 看得到这个项目的产品」这条兜底。
//
// 少了它，把产品设为私有一勾，项目成员就会连自己的任务都看不见 ——
// 那是比原来的窟窿更糟的问题。
func TestProjectMemberSeesLinkedPrivateProduct(t *testing.T) {
	h := newHarness(t)
	privateProduct := h.createProduct("私有产品U", "PRIVU", model.ACLPrivate, nil)
	projectID := h.createProject("公开项目U", "OPENU", privateProduct)
	h.addProjectMember(projectID, reviewer)

	task := h.createTask(dto.TaskReq{
		ProjectID: projectID, ProductID: privateProduct, Name: "研发要做的活",
	})

	dev := asUser(h.ctx, reviewer, false)
	if _, err := h.tasks.Get(dev, task.ID); err != nil {
		t.Fatalf("项目成员应能打开本项目的任务，实际 %v", err)
	}
	listed, err := h.tasks.List(dev, dto.TaskFilterReq{})
	if err != nil {
		t.Fatalf("项目成员查任务列表失败: %v", err)
	}
	if listed.Total != 1 {
		t.Fatalf("项目成员应看到那条任务，实际 %d 条", listed.Total)
	}
}

// TestExecutionTeamRequiresProjectMembership 钉住一条被别处依赖的不变式：
// **迭代成员必须是项目团队成员**。
//
// 产品的可见性判定里，「在项目里干活的人看得到这个项目的私有产品」这一条
// 只查了项目团队。它之所以够用，正是因为这里挡着 —— 加不进项目团队的人
// 也加不进任何迭代团队，所以「迭代成员 ⊂ 项目成员」。
// 哪天这条不变式被放开，那个只查项目团队的判定就会开始漏人。
func TestExecutionTeamRequiresProjectMembership(t *testing.T) {
	h := newHarness(t)
	productID := h.createProduct("公开产品W", "OPENW", model.ACLOpen, nil)
	projectID := h.createProject("公开项目W", "OPENW2", productID)
	executionID := h.createExecution(projectID, "迭代W")

	err := h.executions.AddTeam(h.ctx, executionID, dto.TeamMemberReq{
		UserID: outsider, Role: "dev",
	})
	if errCode(err) != apperr.TeamMemberNotInProject {
		t.Fatalf("非项目成员加入迭代团队应报 team_member_not_in_project，实际 %v", err)
	}

	// 先入项目团队就放行了。
	h.addProjectMember(projectID, outsider)
	if err := h.executions.AddTeam(h.ctx, executionID, dto.TeamMemberReq{
		UserID: outsider, Role: "dev",
	}); err != nil {
		t.Fatalf("项目成员加入迭代团队应当成功，实际 %v", err)
	}
}

// ---------------------------------------------------------------------------
// 工时的日期精度
// ---------------------------------------------------------------------------

// TestEffortDateIsStoredAsWholeUTCDay 钉住一个只在时区边界上才会暴露的 bug。
//
// pm_effort.date 的语义是「业务日期」（哪一天干的活），不是记录创建时间。
// 但两条写入路径一度给出两种精度：不传 date 时直接用 time.Now()（带时分秒），
// 显式传 date 时解析成 UTC 零点。同一列混两种东西，后果是：
//
//  1. 按天聚合的燃尽图与工时报表把同一天的活切进两个桶；
//  2. 「按日期倒序」的工时列表排序变得依赖客户端时区 —— 东八区凌晨报的工，
//     客户端写的是本地日（已是明天），服务端写的是 UTC 瞬时，两条记录差一天，
//     于是刚报的那条反而排到了后面。
//
// 这个测试不靠「碰巧在某个时刻运行」来发现它：直接读库里的列，要求时间部分为零。
func TestEffortDateIsStoredAsWholeUTCDay(t *testing.T) {
	h := newHarness(t)
	productID := h.createProduct("产品D", "PD", model.ACLOpen, nil)
	projectID := h.createProject("项目D", "PRJD", productID)
	executionID := h.createExecution(projectID, "迭代D")

	task := h.createTask(dto.TaskReq{
		ProjectID: projectID, ExecutionID: executionID, ProductID: productID,
		Name: "带日期精度的任务", Estimate: 8, AssignedTo: poUser,
	})
	h.startTask(task.ID)

	// 路径一：不传 date，由服务端补默认值。
	h.recordEffort(task.ID, dto.EffortReq{Consumed: 5})
	// 路径二：显式传「今天」。
	h.recordEffort(task.ID, dto.EffortReq{
		Consumed: 1, Date: time.Now().UTC().Format("2006-01-02"),
	})

	var rows []model.Effort
	if err := h.db.Where("object_type = ? AND object_id = ?", model.ObjectTypeTask, task.ID).
		Order("date DESC, id DESC").Find(&rows).Error; err != nil {
		t.Fatalf("读工时失败: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("应有两条工时，实际 %d", len(rows))
	}
	for _, r := range rows {
		utc := r.Date.UTC()
		if utc.Hour() != 0 || utc.Minute() != 0 || utc.Second() != 0 || utc.Nanosecond() != 0 {
			t.Errorf("工时 %s 的 date 存成了瞬时 %s，应为 UTC 零点：%s",
				r.Work, r.Date.Format(time.RFC3339), "同一列混两种精度会让按天聚合与倒序都失真")
		}
	}

	// 默认值与显式值都落在同一个 UTC 日，所以列表必须由近及远（id 大在前）。
	// 这一条正是那个 bug 的用户可见症状。
	if rows[0].Consumed != 1 {
		t.Errorf("工时列表应按日期倒序、同日按 id 倒序，第一条应是后报的 1 小时，实际 %v",
			rows[0].Consumed)
	}

	// 路径三：完成时顺带报的工时同样要走这个口径。
	if _, err := h.tasks.Apply(h.ctx, task.ID, service.TaskActionInput{
		Action: "finish", Consumed: 2,
	}); err != nil {
		t.Fatalf("完成任务失败: %v", err)
	}
	var finishRow model.Effort
	if err := h.db.Where("object_type = ? AND object_id = ?", model.ObjectTypeTask, task.ID).
		Order("id DESC").First(&finishRow).Error; err != nil {
		t.Fatalf("读收尾工时失败: %v", err)
	}
	if u := finishRow.Date.UTC(); u.Hour() != 0 || u.Minute() != 0 || u.Second() != 0 {
		t.Errorf("finish 顺带报的工时 date 也是瞬时 %s，应与另外两条同精度",
			finishRow.Date.Format(time.RFC3339))
	}
}
