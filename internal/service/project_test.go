package service_test

import (
	"testing"

	"github.com/xywf221/xuliu/internal/dto"
	"github.com/xywf221/xuliu/internal/model"
	"github.com/xywf221/xuliu/internal/pkg/apperr"
	"github.com/xywf221/xuliu/internal/service"
)

// 本文件覆盖 M3 里「项目/迭代/团队」，以及需求排期这条横跨两个域的链路。
//
// 排期之所以值得单独测，是因为它的约束跨表：需求的状态、需求所属产品与
// 项目的关系、迭代与项目的归属，任何一条松开都会产生「看起来排上了、
// 实际并不成立」的数据。

// ---------------------------------------------------------------------------
// 项目与迭代
// ---------------------------------------------------------------------------

// TestProjectNeedsProduct 验证项目必须关联至少一个产品。
func TestProjectNeedsProduct(t *testing.T) {
	h := newHarness(t)

	if _, err := h.projects.Create(h.ctx, dto.ProjectReq{
		Name: "空壳项目", Code: "EMPTY", ACL: model.ACLOpen, PO: poUser,
	}); errCode(err) != apperr.ProjectNoProduct {
		t.Fatalf("没有产品的项目应报 project_no_product，实际 %v", err)
	}
}

// TestProjectHoursComeFromTasks 验证项目的工时是任务聚合出来的，
// 而不是项目上自己填的数。
func TestProjectHoursComeFromTasks(t *testing.T) {
	h := newHarness(t)
	productID := h.createProduct("产品K", "PK", model.ACLOpen, nil)
	projectID := h.createProject("项目K", "PRJK", productID)

	// 刚建的项目：一切归零，而不是「没数据」。
	project := h.mustProject(projectID)
	if project.Estimate != 0 || project.Progress != 0 || project.TaskCount != 0 {
		t.Fatalf("新项目应无工时无进度，实际 %+v", project)
	}

	task := h.startTask(h.createTask(dto.TaskReq{
		ProjectID: projectID, Name: "做点事", Estimate: 4, AssignedTo: poUser,
	}).ID)
	h.recordEffort(task.ID, dto.EffortReq{Consumed: 1})

	project = h.mustProject(projectID)
	if project.Estimate != 4 || project.Consumed != 1 || project.Left != 3 {
		t.Fatalf("项目工时应随任务走：应为 4/1/3，实际 %v/%v/%v",
			project.Estimate, project.Consumed, project.Left)
	}
	if project.TaskCount != 1 {
		t.Fatalf("项目任务数应为 1，实际 %d", project.TaskCount)
	}
}

// TestProjectPrivateHidesFromOutsider 验证私有项目对团队外的人不可见。
func TestProjectPrivateHidesFromOutsider(t *testing.T) {
	h := newHarness(t)
	productID := h.createProduct("产品L", "PL", model.ACLOpen, nil)

	project, err := h.projects.Create(h.ctx, dto.ProjectReq{
		Name: "内部项目", Code: "INNER", Model: model.ProjectModelScrum,
		ACL: model.ACLPrivate, PO: poUser, PM: poUser,
		ProductIDs: []uint{productID},
	})
	if err != nil {
		h.t.Fatalf("建私有项目失败: %v", err)
	}

	// 换成「路人」（非超管、无权限点）来看：应当查不到这个项目。
	ctx := asUser(h.ctx, outsider, false)
	if _, err := h.projects.Get(ctx, project.ID); errCode(err) != apperr.ProjectAccessDenied {
		t.Fatalf("私有项目对外人应报 project_access_denied，实际 %v", err)
	}
	list, err := h.projects.List(ctx, dto.ProjectFilterReq{})
	if err != nil {
		t.Fatalf("查项目列表失败: %v", err)
	}
	if list.Total != 0 {
		t.Fatalf("外人不应看到任何项目，实际 %d 条", list.Total)
	}
}

// TestExecutionDatesMustFallInsideProject 验证迭代周期不能超出项目周期。
func TestExecutionDatesMustFallInsideProject(t *testing.T) {
	h := newHarness(t)
	productID := h.createProduct("产品M", "PM", model.ACLOpen, nil)

	project, err := h.projects.Create(h.ctx, dto.ProjectReq{
		Name: "有周期的项目", Code: "DAYS", ACL: model.ACLOpen, PO: poUser,
		ProductIDs: []uint{productID},
		Begin:      "2026-03-01", End: "2026-03-31",
	})
	if err != nil {
		h.t.Fatalf("建项目失败: %v", err)
	}
	if project.Days != 31 {
		t.Fatalf("项目工期应为 31 天，实际 %d", project.Days)
	}

	if _, err := h.executions.Create(h.ctx, dto.ExecutionReq{
		ProjectID: project.ID, Name: "超期迭代",
		Begin: "2026-02-01", End: "2026-03-10",
	}); errCode(err) != apperr.ExecutionDateOutOfProject {
		t.Fatalf("越界的迭代应报 execution_date_out_of_project，实际 %v", err)
	}

	execution, err := h.executions.Create(h.ctx, dto.ExecutionReq{
		ProjectID: project.ID, Name: "正常迭代",
		Begin: "2026-03-01", End: "2026-03-10",
	})
	if err != nil {
		t.Fatalf("范围内的迭代应能建: %v", err)
	}
	if execution.ProjectID != project.ID || execution.ProjectName == "" {
		t.Fatalf("迭代出参应带上所属项目，实际 %+v", execution)
	}
}

// TestRemoveTeamMemberBlockedByUnfinishedTask 验证身上有未完成任务的成员
// 不能被移出团队。
func TestRemoveTeamMemberBlockedByUnfinishedTask(t *testing.T) {
	h := newHarness(t)
	productID := h.createProduct("产品N", "PN", model.ACLOpen, nil)
	projectID := h.createProject("项目N", "PRJN", productID)
	h.addProjectMember(projectID, reviewer)

	task := h.createTask(dto.TaskReq{
		ProjectID: projectID, Name: "交给 reviewer 的活", AssignedTo: reviewer,
	})

	if err := h.projects.RemoveTeam(h.ctx, projectID, reviewer); errCode(err) != apperr.Conflict {
		t.Fatalf("有未完成任务的成员应拒绝移出团队，实际 %v", err)
	}

	// 取消任务后就能移出了。
	if _, err := h.tasks.Apply(h.ctx, task.ID, service.TaskActionInput{Action: "cancel"}); err != nil {
		t.Fatalf("取消任务失败: %v", err)
	}
	if err := h.projects.RemoveTeam(h.ctx, projectID, reviewer); err != nil {
		t.Fatalf("任务收尾后应能移出团队: %v", err)
	}
}

// TestExecutionTeamNarrowsAssignableMembers 验证迭代自带团队时，
// 可指派的人收窄到迭代团队；迭代没配团队时沿用项目团队。
//
// 这条规则的意义在于「迭代抽调了谁」是显式表达：项目团队里有 5 个人，
// 这个迭代只要 2 个，那么另外 3 个就不该被派到本迭代的活。
func TestExecutionTeamNarrowsAssignableMembers(t *testing.T) {
	h := newHarness(t)
	productID := h.createProduct("产品O", "POA", model.ACLOpen, nil)
	projectID := h.createProject("项目O", "PRJO", productID)
	executionID := h.createExecution(projectID, "迭代O")
	h.addProjectMember(projectID, reviewer)

	// 项目团队现在有 PO 与 reviewer 两人；迭代还没配自己的团队。
	ids, err := h.projects.MemberIDs(h.ctx, projectID)
	if err != nil {
		t.Fatalf("取项目成员失败: %v", err)
	}
	if len(ids) != 2 {
		t.Fatalf("项目团队应有 2 人（创建者 + reviewer），实际 %v", ids)
	}

	// 迭代没配团队时，项目团队的成员都能被指派。
	h.createTask(dto.TaskReq{
		ProjectID: projectID, ExecutionID: executionID,
		Name: "迭代里的任务", AssignedTo: reviewer,
	})

	// 给迭代配一个只有 PO 的团队：此后 reviewer 不能再被派到本迭代的活。
	if err := h.executions.AddTeam(h.ctx, executionID, dto.TeamMemberReq{
		UserID: poUser, Role: "PM",
	}); err != nil {
		t.Fatalf("给迭代加团队成员失败: %v", err)
	}
	if _, err := h.tasks.Create(h.ctx, dto.TaskReq{
		ProjectID: projectID, ExecutionID: executionID,
		Name: "派给迭代外的人", AssignedTo: reviewer,
	}); errCode(err) != apperr.TeamMemberNotInProject {
		t.Fatalf("迭代自带团队时应拒绝派给团队外的人，实际 %v", err)
	}
	// 但同一批人在项目层面的任务仍可指派 —— 收窄只作用于这个迭代。
	if _, err := h.tasks.Create(h.ctx, dto.TaskReq{
		ProjectID: projectID, Name: "项目层面的任务", AssignedTo: reviewer,
	}); err != nil {
		t.Fatalf("迭代团队不应影响项目层面的指派: %v", err)
	}

	// 迭代团队成员必须是项目团队成员。
	if err := h.executions.AddTeam(h.ctx, executionID, dto.TeamMemberReq{
		UserID: outsider, Role: "dev",
	}); errCode(err) != apperr.TeamMemberNotInProject {
		t.Fatalf("向迭代团队加非项目成员应报 team_member_not_in_project，实际 %v", err)
	}
}

// ---------------------------------------------------------------------------
// 需求排期
// ---------------------------------------------------------------------------

// activeStory 建一条已评审通过的需求。
func (h *harness) activeStory(productID uint, title string) uint {
	h.t.Helper()
	storyID := h.createStory(productID, title, []uint{reviewer})
	if _, err := h.stories.Apply(h.ctx, storyID, pendingReview()); err != nil {
		h.t.Fatalf("提交评审失败: %v", err)
	}
	if _, err := h.stories.Apply(asUser(h.ctx, reviewer, true), storyID,
		service.ActionInput{Action: "pass", Result: model.ReviewPass}); err != nil {
		h.t.Fatalf("评审通过失败: %v", err)
	}
	return storyID
}

// TestLinkStoryGuards 覆盖排期的三条约束。
func TestLinkStoryGuards(t *testing.T) {
	h := newHarness(t)
	productA := h.createProduct("产品P", "PP", model.ACLOpen, []uint{reviewer})
	productB := h.createProduct("产品Q", "PQ", model.ACLOpen, []uint{reviewer})

	// 项目只关联产品 A。
	projectID := h.createProject("项目P", "PRJP", productA)
	otherProjectID := h.createProject("项目Q", "PRJQ", productB)

	// 1）草稿状态的需求不能排。
	draftID := h.createStory(productA, "还没评审的需求", []uint{reviewer})
	if _, err := h.projects.LinkStory(h.ctx, projectID, dto.StoryLinkReq{
		StoryID: draftID,
	}); errCode(err) != apperr.StoryStatusInvalid {
		t.Fatalf("草稿需求排期应报 story_status_invalid，实际 %v", err)
	}

	// 2）需求所属产品没关联到该项目，不能排。
	foreignID := h.activeStory(productB, "别家产品的需求")
	if _, err := h.projects.LinkStory(h.ctx, projectID, dto.StoryLinkReq{
		StoryID: foreignID,
	}); errCode(err) != apperr.StoryProductNotInProject {
		t.Fatalf("跨产品排期应报 story_product_not_in_project，实际 %v", err)
	}

	// 3）迭代必须属于该项目。
	storyID := h.activeStory(productA, "可排期的需求")
	foreignExecution := h.createExecution(otherProjectID, "别家的迭代")
	if _, err := h.projects.LinkStory(h.ctx, projectID, dto.StoryLinkReq{
		StoryID: storyID, ExecutionID: foreignExecution,
	}); errCode(err) != apperr.ExecutionNotFound {
		t.Fatalf("用别家迭代排期应报 execution_not_found，实际 %v", err)
	}

	// 三条都过之后应当成功，并把阶段推进到「已立项」。
	executionID := h.createExecution(projectID, "迭代P")
	link, err := h.projects.LinkStory(h.ctx, projectID, dto.StoryLinkReq{
		StoryID: storyID, ExecutionID: executionID,
	})
	if err != nil {
		t.Fatalf("排需求失败: %v", err)
	}
	if link.ProjectID != projectID || link.ExecutionID != executionID ||
		link.StoryTitle != "可排期的需求" {
		t.Fatalf("排期出参不正确：%+v", link)
	}
	if link.Version != 1 || link.Outdated {
		t.Fatalf("刚排入时应记录当前版本且未过期，实际 %d/%v", link.Version, link.Outdated)
	}

	story := h.mustStory(storyID)
	if story.Stage != model.StoryStageProjected {
		t.Fatalf("排入后阶段应为 projected，实际 %s", story.Stage)
	}
	if n := h.actionCount(model.ObjectTypeStory, storyID, model.ActionLinked); n != 1 {
		t.Fatalf("排期应留下 1 条操作历史，实际 %d", n)
	}

	// 需求详情要能看到「被哪些项目在做」。
	detail, err := h.stories.Get(h.ctx, storyID)
	if err != nil {
		t.Fatalf("取需求详情失败: %v", err)
	}
	if len(detail.Projects) != 1 || detail.Projects[0].ProjectID != projectID {
		t.Fatalf("需求详情应带出排入的项目，实际 %+v", detail.Projects)
	}

	// 项目侧的需求列表同样要能看到它。
	stories, err := h.projects.ListStories(h.ctx, projectID, 0)
	if err != nil {
		t.Fatalf("取项目需求失败: %v", err)
	}
	if len(stories) != 1 || stories[0].StoryID != storyID {
		t.Fatalf("项目需求列表应有 1 条，实际 %+v", stories)
	}

	// 排入之后再排一次（换迭代）应当更新而不是报错。
	if _, err := h.projects.LinkStory(h.ctx, projectID, dto.StoryLinkReq{
		StoryID: storyID,
	}); err != nil {
		t.Fatalf("重复排期应更新已有记录: %v", err)
	}
	if stories, err = h.projects.ListStories(h.ctx, projectID, 0); err != nil || len(stories) != 1 {
		t.Fatalf("重复排期不应产生第二条记录，实际 %d 条（err=%v）", len(stories), err)
	}
	if stories[0].ExecutionID != 0 {
		t.Fatalf("重复排期未带迭代时应把迭代归属清空，实际 %d", stories[0].ExecutionID)
	}
}

// TestOutdatedLinkMarksStoryVersion 验证需求变更后，已排期的关联被标为过期。
func TestOutdatedLinkMarksStoryVersion(t *testing.T) {
	h := newHarness(t)
	productID := h.createProduct("产品R", "PR", model.ACLOpen, []uint{reviewer})
	projectID := h.createProject("项目R", "PRJR", productID)
	storyID := h.activeStory(productID, "会变更的需求")

	if _, err := h.projects.LinkStory(h.ctx, projectID, dto.StoryLinkReq{StoryID: storyID}); err != nil {
		t.Fatalf("排需求失败: %v", err)
	}

	// 需求变更：版本 +1。
	if _, err := h.stories.Apply(h.ctx, storyID, service.ActionInput{
		Action: "change", Version: 1, Title: "会变更的需求", Spec: "新描述", Reason: "客户改主意了",
	}); err != nil {
		t.Fatalf("需求变更失败: %v", err)
	}

	stories, err := h.projects.ListStories(h.ctx, projectID, 0)
	if err != nil {
		t.Fatalf("取项目需求失败: %v", err)
	}
	if len(stories) != 1 {
		t.Fatalf("应仍有 1 条关联，实际 %d", len(stories))
	}
	if !stories[0].Outdated || stories[0].Version != 1 {
		t.Fatalf("需求变更后关联应标为过期且保留排入时的版本，实际 %+v", stories[0])
	}
}

// TestUnlinkBlockedByProjectTask 验证项目下还有关联任务时不能撤出需求。
func TestUnlinkBlockedByProjectTask(t *testing.T) {
	h := newHarness(t)
	productID := h.createProduct("产品S", "PS", model.ACLOpen, []uint{reviewer})
	projectID := h.createProject("项目S", "PRJS", productID)
	storyID := h.activeStory(productID, "带任务的需求")

	if _, err := h.projects.LinkStory(h.ctx, projectID, dto.StoryLinkReq{StoryID: storyID}); err != nil {
		h.t.Fatalf("排需求失败: %v", err)
	}
	task := h.createTask(dto.TaskReq{
		ProjectID: projectID, ProductID: productID, StoryID: storyID,
		Name: "实现它", AssignedTo: poUser,
	})
	if task.StoryID != storyID || task.StoryTitle == "" {
		t.Fatalf("任务应带上关联需求的标题，实际 %+v", task)
	}

	if err := h.projects.UnlinkStory(h.ctx, projectID, storyID); errCode(err) != apperr.StoryHasTasks {
		t.Fatalf("有任务关联时撤出需求应报 story_has_tasks，实际 %v", err)
	}

	if err := h.tasks.Delete(h.ctx, task.ID); err != nil {
		t.Fatalf("删除任务失败: %v", err)
	}
	if err := h.projects.UnlinkStory(h.ctx, projectID, storyID); err != nil {
		t.Fatalf("任务清掉后应能撤出: %v", err)
	}
	stories, err := h.projects.ListStories(h.ctx, projectID, 0)
	if err != nil {
		t.Fatalf("取项目需求失败: %v", err)
	}
	if len(stories) != 0 {
		t.Fatalf("撤出后项目下不应再有需求，实际 %+v", stories)
	}
	if n := h.actionCount(model.ObjectTypeStory, storyID, model.ActionUnlinked); n != 1 {
		t.Fatalf("撤出应留下 1 条操作历史，实际 %d", n)
	}
}

// TestStoryDeleteBlockedByUnfinishedTask 验证需求下有未完成任务时不能删。
//
// 「未完成」的口径是未开始/进行中/暂停：已完成或已取消的任务是历史，
// 不该让一条需求永远删不掉。
func TestStoryDeleteBlockedByUnfinishedTask(t *testing.T) {
	h := newHarness(t)
	productID := h.createProduct("产品T", "PT", model.ACLOpen, []uint{reviewer})
	projectID := h.createProject("项目T", "PRJT", productID)
	storyID := h.activeStory(productID, "下挂任务的需求")

	task := h.createTask(dto.TaskReq{
		ProjectID: projectID, ProductID: productID, StoryID: storyID,
		Name: "未完成的任务", AssignedTo: poUser,
	})

	if err := h.stories.Delete(h.ctx, storyID); errCode(err) != apperr.StoryHasTasks {
		t.Fatalf("有未完成任务时应拒绝删除需求，实际 %v", err)
	}

	// 任务取消后就不构成阻塞了。
	if _, err := h.tasks.Apply(h.ctx, task.ID, service.TaskActionInput{Action: "cancel"}); err != nil {
		t.Fatalf("取消任务失败: %v", err)
	}
	if err := h.stories.Delete(h.ctx, storyID); err != nil {
		t.Fatalf("任务收尾后应能删除需求: %v", err)
	}
	if n := h.actionCount(model.ObjectTypeStory, storyID, model.ActionDeleted); n != 1 {
		t.Fatalf("删除应留下 1 条操作历史，实际 %d", n)
	}
}
