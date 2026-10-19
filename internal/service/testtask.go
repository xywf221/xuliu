package service

import (
	"context"
	"encoding/json"
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

// TestTaskService 负责测试单、单内用例与执行记录。
//
// 三条实现约定值得先讲：
//
//  1. **纳入用例时锁定版本**。用例后来改了，单里那条仍记着「当时用的是第几版」，
//     详情里会同时给出用例此刻的版本；不一致就提示「本单结果是对着旧版跑的」。
//     这比静默地让人误以为结果对应最新用例要诚实得多。
//  2. **执行记录是追加的**。同一用例在同一单里可以跑很多次，历史全留；
//     「单内用例的最新状态」只是冗余，不是唯一事实。
//  3. **有执行记录的单不许删**。测试证据不该被一键抹掉；不测了就结单。
//
// 通知仍遵循唯一一条规则：通知下一个要动手的人（指派给新负责人、结单告知提单人）。
// 「用例失败该通知谁」没有唯一答案（研发？产品经理？提需求的人？），
// 所以不猜 —— 失败信息由缺陷流转去通知，那是缺陷域本来就该管的事。
type TestTaskService struct {
	db         *gorm.DB
	tasks      *repository.TestTaskRepo
	links      *repository.TestTaskCaseRepo
	runs       *repository.TestRunRepo
	cases      *repository.TestCaseRepo
	steps      *repository.TestCaseStepRepo
	products   *repository.ProductRepo
	projects   *repository.ProjectRepo
	executions *repository.ExecutionRepo
	builds     *repository.BuildRepo
	users      *repository.UserRepo

	gate     *Gate
	notifier notifier

	recorder *history.Recorder
}

// NewTestTaskService 构造测试单服务。
func NewTestTaskService(
	db *gorm.DB,
	tasks *repository.TestTaskRepo,
	links *repository.TestTaskCaseRepo,
	runs *repository.TestRunRepo,
	cases *repository.TestCaseRepo,
	steps *repository.TestCaseStepRepo,
	products *repository.ProductRepo,
	projects *repository.ProjectRepo,
	executions *repository.ExecutionRepo,
	builds *repository.BuildRepo,
	users *repository.UserRepo,
	gate *Gate,
	notifier notifier,
) *TestTaskService {
	return &TestTaskService{
		db: db, tasks: tasks, links: links, runs: runs, cases: cases, steps: steps,
		products: products, projects: projects, executions: executions,
		builds: builds, users: users,
		gate: gate, notifier: notifier,
		recorder: history.New(TestTaskLabeler{}),
	}
}

// List 分页查询测试单。
func (s *TestTaskService) List(ctx context.Context, req dto.TestTaskFilterReq) (*pagination.Result[dto.TestTaskResp], error) {
	visibleProducts, err := s.gate.VisibleProducts(ctx)
	if err != nil {
		return nil, err
	}
	visibleProjects, err := s.gate.VisibleProjects(ctx)
	if err != nil {
		return nil, err
	}

	result, err := s.tasks.List(ctx, repository.TestTaskFilter{
		ProductID: req.ProductID, ProjectID: req.ProjectID,
		ExecutionID: req.ExecutionID, BuildID: req.BuildID,
		Status: req.Status, Result: req.Result, Pri: req.Pri,
		AssignedTo: req.AssignedTo, OpenedBy: req.OpenedBy,
		Unfinished: req.Unfinished, NoCase: req.NoCase,
		Keywords:   req.Keywords,
		ProductIDs: visibleProducts, ProjectIDs: visibleProjects,
		Pagination: pagination.Params{Page: req.Page, Size: req.Size, Order: req.Order},
	})
	if err != nil {
		return nil, err
	}
	return s.decorate(ctx, result)
}

// Get 取测试单详情（含单内用例清单与执行汇总）。
func (s *TestTaskService) Get(ctx context.Context, id uint) (*dto.TestTaskDetailResp, error) {
	task, err := s.tasks.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := s.assertVisible(ctx, task); err != nil {
		return nil, err
	}

	list, err := s.decorate(ctx, &pagination.Result[model.TestTask]{
		List: []model.TestTask{*task}, Total: 1, Page: 1, Size: 1,
	})
	if err != nil {
		return nil, err
	}

	out := &dto.TestTaskDetailResp{TestTaskResp: list.List[0]}
	cases, err := s.listCases(ctx, id)
	if err != nil {
		return nil, err
	}
	out.Cases = cases
	return out, nil
}

// Create 新建测试单，可同时纳入用例。
func (s *TestTaskService) Create(ctx context.Context, req dto.TestTaskReq) (*dto.TestTaskResp, error) {
	if err := s.assertTarget(ctx, req); err != nil {
		return nil, err
	}

	actor := currentUserID(ctx)
	now := time.Now().UTC()
	begin, end, err := parseTaskDates(req.Begin, req.End)
	if err != nil {
		return nil, err
	}

	task := &model.TestTask{
		ProductID: req.ProductID, ProjectID: req.ProjectID,
		ExecutionID: req.ExecutionID, BuildID: req.BuildID,
		Name: strings.TrimSpace(req.Name), Desc: req.Desc,
		Pri:        defaultLevel(req.Pri),
		Status:     model.TestTaskStatusWait,
		AssignedTo: req.AssignedTo, Begin: begin, End: end,
		OpenedBy: actor, OpenedDate: &now,
		Order: req.Order,
	}
	if task.Name == "" {
		return nil, apperr.Newf(apperr.InvalidParam, "测试单名称不能为空")
	}
	if req.AssignedTo > 0 {
		task.AssignedDate = &now
	}

	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := s.tasks.Create(ctx, tx, task); err != nil {
			return err
		}
		if len(req.CaseIDs) > 0 {
			// 与 AddCases 共用同一套校验：不能因为「建单时顺手带用例」
			// 这条路就绕过「只收评审通过的用例」这种规则。
			if _, err := s.addCasesTx(ctx, tx, task, req.CaseIDs, actor); err != nil {
				return err
			}
		}
		if err := s.record(ctx, tx, history.Entry{
			ObjectType: model.ObjectTypeTestTask, ObjectID: task.ID,
			ProductID: task.ProductID, ProjectID: task.ProjectID, ExecutionID: task.ExecutionID,
			ActorID: actor, Action: model.ActionOpened,
			Changes: []history.Change{{Field: "name", New: task.Name}},
		}); err != nil {
			return err
		}
		if task.AssignedTo > 0 {
			return s.notifyAssigned(ctx, tx, task, actor)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return s.getResp(ctx, task.ID)
}

// Update 编辑测试单元信息。
func (s *TestTaskService) Update(ctx context.Context, id uint, req dto.TestTaskReq) (*dto.TestTaskResp, error) {
	task, err := s.tasks.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := s.assertVisible(ctx, task); err != nil {
		return nil, err
	}
	if _, statusErr := workflow.TestTaskMachine.Resolve("edit", task.Status); statusErr != nil {
		return nil, workflow.TestTaskMachine.DescribeError(statusErr)
	}
	if err := s.assertTarget(ctx, req); err != nil {
		return nil, err
	}
	begin, end, err := parseTaskDates(req.Begin, req.End)
	if err != nil {
		return nil, err
	}

	actor := currentUserID(ctx)
	now := time.Now().UTC()
	fields := map[string]any{
		"product_id": req.ProductID, "project_id": req.ProjectID,
		"execution_id": req.ExecutionID, "build_id": req.BuildID,
		"name": strings.TrimSpace(req.Name), "desc": req.Desc,
		"pri":         defaultLevel(req.Pri),
		"assigned_to": req.AssignedTo,
		"begin":       begin, "end": end, "order": req.Order,
		"last_edited_by": actor, "last_edited_date": &now,
	}
	changes := testTaskDiff(task, fields)

	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := s.tasks.UpdateFields(ctx, tx, id, fields); err != nil {
			return err
		}
		if len(req.CaseIDs) > 0 {
			if _, err := s.addCasesTx(ctx, tx, task, req.CaseIDs, actor); err != nil {
				return err
			}
		}
		return s.record(ctx, tx, history.Entry{
			ObjectType: model.ObjectTypeTestTask, ObjectID: id,
			ProductID: req.ProductID, ProjectID: req.ProjectID, ExecutionID: req.ExecutionID,
			ActorID: actor, Action: model.ActionEdited, Changes: changes,
		})
	})
	if err != nil {
		return nil, err
	}
	return s.getResp(ctx, id)
}

// Delete 删除测试单。
//
// 已有执行记录时拒绝：测试单删了，那些「哪天谁跑过什么、结果如何」的记录
// 要么一起消失、要么成为指向空气的孤儿，两种都不可接受。
func (s *TestTaskService) Delete(ctx context.Context, id uint) error {
	task, err := s.tasks.GetByID(ctx, id)
	if err != nil {
		return err
	}
	if err := s.assertVisible(ctx, task); err != nil {
		return err
	}
	runCount, err := s.runs.CountByTask(ctx, id)
	if err != nil {
		return err
	}
	if runCount > 0 {
		return apperr.Newf(apperr.TestTaskHasRuns,
			"该测试单已有 %d 条执行记录，不能删除；如不再需要请结单", runCount)
	}

	actor := currentUserID(ctx)
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := s.record(ctx, tx, history.Entry{
			ObjectType: model.ObjectTypeTestTask, ObjectID: id,
			ProductID: task.ProductID, ProjectID: task.ProjectID, ExecutionID: task.ExecutionID,
			ActorID: actor, Action: model.ActionDeleted,
		}); err != nil {
			return err
		}
		if err := s.links.DeleteByTask(ctx, tx, id); err != nil {
			return err
		}
		return s.tasks.Delete(ctx, tx, id)
	})
}

// Assign 指派测试单负责人。
//
// comment 会落进操作历史：指派常带一句「这台要先验一下」，
// 收下却不写，等于在界面上骗人写了个字。
func (s *TestTaskService) Assign(ctx context.Context, id, userID uint, comment string) (*dto.TestTaskResp, error) {
	task, err := s.tasks.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := s.assertVisible(ctx, task); err != nil {
		return nil, err
	}
	if _, statusErr := workflow.TestTaskMachine.Resolve("assign", task.Status); statusErr != nil {
		return nil, workflow.TestTaskMachine.DescribeError(statusErr)
	}
	if err := assertUserExists(ctx, s.users, userID); err != nil {
		return nil, err
	}

	actor := currentUserID(ctx)
	now := time.Now().UTC()
	fields := map[string]any{
		"assigned_to": userID, "assigned_date": &now,
		"last_edited_by": actor, "last_edited_date": &now,
	}
	changes := testTaskDiff(task, map[string]any{"assigned_to": userID})

	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := s.tasks.UpdateFields(ctx, tx, id, fields); err != nil {
			return err
		}
		if err := s.record(ctx, tx, history.Entry{
			ObjectType: model.ObjectTypeTestTask, ObjectID: id,
			ProductID: task.ProductID, ProjectID: task.ProjectID, ExecutionID: task.ExecutionID,
			ActorID: actor, Action: model.ActionAssigned,
			// Extra 存被指派人：与需求/缺陷同一口径，时间线要能显示给了谁。
			Extra: itoa(userID), Changes: changes,
			Comment: defaultString(comment, "指派测试单负责人"),
		}); err != nil {
			return err
		}
		assigned := *task
		assigned.AssignedTo = userID
		return s.notifyAssigned(ctx, tx, &assigned, actor)
	})
	if err != nil {
		return nil, err
	}
	return s.getResp(ctx, id)
}

// Apply 执行不带来额外参数的状态动作：start / block / activate。
func (s *TestTaskService) Apply(ctx context.Context, id uint, action, comment string) (*dto.TestTaskResp, error) {
	return s.apply(ctx, id, action, comment, "", "")
}

// Finish 结单：必须给出结论（pass / fail / blocked）。
func (s *TestTaskService) Finish(ctx context.Context, id uint, req dto.TestTaskFinishReq) (*dto.TestTaskResp, error) {
	if !slices.Contains(model.TestTaskResults, req.Result) {
		return nil, apperr.Newf(apperr.TestTaskResultRequired,
			"结单必须给出结论，取值：pass / fail / blocked")
	}
	return s.apply(ctx, id, "finish", req.Report, req.Result, "")
}

// apply 是所有状态动作的共同实现。
func (s *TestTaskService) apply(ctx context.Context, id uint, action, comment, result, _ string) (*dto.TestTaskResp, error) {
	task, err := s.tasks.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := s.assertVisible(ctx, task); err != nil {
		return nil, err
	}
	canonical, statusErr := workflow.TestTaskMachine.Resolve(action, task.Status)
	if statusErr != nil {
		return nil, workflow.TestTaskMachine.DescribeError(statusErr)
	}

	actor := currentUserID(ctx)
	now := time.Now().UTC()
	fields := map[string]any{"status": canonical.To}
	switch canonical.Action {
	case "start":
		// 只有第一次开始才写 StartedAt：挂起再开始不该改写「这单什么时候动的身」，
		// 但 blocked ↔ doing 的往返本来也不算重新开始，所以这里判空。
		if task.StartedAt == nil {
			fields["started_at"] = &now
		}
	case "finish":
		fields["result"] = result
		fields["finished_at"] = &now
		fields["closed_by"] = actor
		if comment != "" {
			fields["report"] = comment
		}
	case "activate":
		// 重开不清 Result：留着上一次的结论做对照，下次结单时才覆盖。
	}

	changes := testTaskDiff(task, fields)
	extra := ""
	if canonical.Action == "finish" {
		extra = result
	} else if canonical.To != "" {
		extra = workflow.StatusChangeExtra(task.Status, canonical.To)
	}

	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := s.tasks.UpdateFields(ctx, tx, id, fields); err != nil {
			return err
		}
		if err := s.record(ctx, tx, history.Entry{
			ObjectType: model.ObjectTypeTestTask, ObjectID: id,
			ProductID: task.ProductID, ProjectID: task.ProjectID, ExecutionID: task.ExecutionID,
			ActorID: actor, Action: workflow.TestTaskMachine.LogActionOf(canonical.Action),
			Comment: comment, Extra: extra, Changes: changes,
		}); err != nil {
			return err
		}
		if canonical.Action == "finish" {
			// 结单告知提单人：是他发起的这次测试，结论要回到他手上。
			// Send 内部已处理「收件人是自己/为 0 就不发」，这里不再判一遍。
			return s.notifier.Send(ctx, tx, task.OpenedBy, actor,
				model.ObjectTypeTestTask, id, "测试单已结单",
				"「"+task.Name+"」已结单，结论："+testTaskResultLabel(result))
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return s.getResp(ctx, id)
}

// AddCases 往测试单里纳入用例。
//
// 只收「评审通过（confirmed）」的用例：未确认的用例本身还没被认可，
// 拿它跑出来的结果不该进测试报告。
func (s *TestTaskService) AddCases(ctx context.Context, taskID uint, caseIDs []uint) (*dto.TestTaskDetailResp, error) {
	task, err := s.tasks.GetByID(ctx, taskID)
	if err != nil {
		return nil, err
	}
	if err := s.assertVisible(ctx, task); err != nil {
		return nil, err
	}

	actor := currentUserID(ctx)
	added, err := func() (int, error) {
		count := 0
		err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			n, err := s.addCasesTx(ctx, tx, task, caseIDs, actor)
			count = n
			if err != nil {
				return err
			}
			return s.record(ctx, tx, history.Entry{
				ObjectType: model.ObjectTypeTestTask, ObjectID: taskID,
				ProductID: task.ProductID, ProjectID: task.ProjectID, ExecutionID: task.ExecutionID,
				ActorID: actor, Action: model.ActionLinked,
				Extra: itoa(uint(n)), Comment: "纳入用例",
			})
		})
		return count, err
	}()
	if err != nil {
		return nil, err
	}
	if added == 0 {
		return nil, apperr.Newf(apperr.TestTaskCaseExists, "所选用例都已在该测试单里")
	}
	return s.Get(ctx, taskID)
}

// addCasesTx 在事务内纳入若干用例，返回实际新增条数。
//
// 已存在的关联会被跳过而不报错：批量勾选时「有一半早加了」是常态，
// 为这种幂等操作回滚整个请求只会让前端更难写。
func (s *TestTaskService) addCasesTx(ctx context.Context, tx *gorm.DB, task *model.TestTask, caseIDs []uint, actor uint) (int, error) {
	if len(caseIDs) == 0 {
		return 0, nil
	}
	visible, err := s.gate.VisibleProducts(ctx)
	if err != nil {
		return 0, err
	}
	rows, err := s.cases.ListByIDs(ctx, caseIDs)
	if err != nil {
		return 0, err
	}
	found := make(map[uint]bool, len(rows))
	for i := range rows {
		found[rows[i].ID] = true
	}
	for _, id := range caseIDs {
		if !found[id] {
			return 0, apperr.Newf(apperr.TestCaseNotFound, "用例 %d 不存在", id)
		}
	}

	added := 0
	for i := range rows {
		c := &rows[i]
		if !slices.Contains(visible, c.ProductID) {
			return 0, apperr.Newf(apperr.ProductAccessDenied, "用例 %d 所属产品不可见", c.ID)
		}
		if c.ProductID != task.ProductID {
			// 测试单是「测这个产品」的活动，报告也按产品汇总：
			// 混进别的产品用例，结论会替一个没被测过的产品背书。
			return 0, apperr.Newf(apperr.InvalidParam,
				"用例 %d 属于别的产品，不能纳入本单（本单测的是产品 %d）",
				c.ID, task.ProductID)
		}
		if c.Stage != model.TestCaseStageConfirmed {
			return 0, apperr.Newf(apperr.TestTaskCaseNotConfirmed,
				"用例 %d 尚未评审通过（当前阶段：%s），不能纳入测试单",
				c.ID, workflow.TestCaseMachine.StatusLabel(c.Stage))
		}
		link, err := s.links.Find(ctx, task.ID, c.ID)
		if err != nil {
			return 0, err
		}
		if link != nil {
			continue
		}
		newLink := &model.TestTaskCase{
			TaskID: task.ID, CaseID: c.ID, CaseVersion: c.Version,
			ProductID: c.ProductID, StoryID: c.StoryID,
			Order: task.CaseCount + added + 1,
		}
		if err := s.links.Create(ctx, tx, newLink); err != nil {
			return 0, err
		}
		added++
	}
	if added > 0 {
		if err := s.tasks.AddCaseCount(ctx, tx, task.ID, added); err != nil {
			return 0, err
		}
	}
	return added, nil
}

// RemoveCase 把用例移出测试单。
//
// 只删关联，用例与已有的执行记录都不动：移出后仍要能回答
// 「这条用例当初在这个单里跑过、结果是什么」。
func (s *TestTaskService) RemoveCase(ctx context.Context, taskID, caseID uint) (*dto.TestTaskDetailResp, error) {
	task, err := s.tasks.GetByID(ctx, taskID)
	if err != nil {
		return nil, err
	}
	if err := s.assertVisible(ctx, task); err != nil {
		return nil, err
	}
	link, err := s.links.Find(ctx, taskID, caseID)
	if err != nil {
		return nil, err
	}
	if link == nil {
		return nil, apperr.New(apperr.TestTaskCaseNotFound)
	}

	actor := currentUserID(ctx)
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := s.links.Delete(ctx, tx, taskID, caseID); err != nil {
			return err
		}
		if err := s.tasks.AddCaseCount(ctx, tx, taskID, -1); err != nil {
			return err
		}
		return s.record(ctx, tx, history.Entry{
			ObjectType: model.ObjectTypeTestTask, ObjectID: taskID,
			ProductID: task.ProductID, ProjectID: task.ProjectID, ExecutionID: task.ExecutionID,
			ActorID: actor, Action: model.ActionUnlinked, Extra: itoa(caseID),
		})
	})
	if err != nil {
		return nil, err
	}
	return s.Get(ctx, taskID)
}

// AssignCase 给单内某条用例指派执行人。
//
// 一个测试单常常是几个人分头跑的，所以执行人落在「单内的这条用例」上，
// 而不是只有整单一个负责人。
func (s *TestTaskService) AssignCase(ctx context.Context, taskID, caseID, userID uint, comment string) (*dto.TestTaskDetailResp, error) {
	task, err := s.tasks.GetByID(ctx, taskID)
	if err != nil {
		return nil, err
	}
	if err := s.assertVisible(ctx, task); err != nil {
		return nil, err
	}
	link, err := s.links.Find(ctx, taskID, caseID)
	if err != nil {
		return nil, err
	}
	if link == nil {
		return nil, apperr.New(apperr.TestTaskCaseNotFound)
	}
	if err := assertUserExists(ctx, s.users, userID); err != nil {
		return nil, err
	}

	actor := currentUserID(ctx)
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := s.links.UpdateFields(ctx, tx, link.ID, map[string]any{"assigned_to": userID}); err != nil {
			return err
		}
		if err := s.record(ctx, tx, history.Entry{
			ObjectType: model.ObjectTypeTestTask, ObjectID: taskID,
			ProductID: task.ProductID, ProjectID: task.ProjectID, ExecutionID: task.ExecutionID,
			ActorID: actor, Action: model.ActionAssigned, Extra: itoa(userID),
			Comment: defaultString(comment, "指派用例执行人"),
		}); err != nil {
			return err
		}
		if userID != actor {
			return s.notifier.Send(ctx, tx, userID, actor, model.ObjectTypeTestTask, taskID,
				"有用例指派给你测试", "测试单「"+task.Name+"」里有用例由你执行")
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return s.Get(ctx, taskID)
}

// RecordRun 记录一次用例执行结果。
//
// 一个事务里做三件事：写执行记录、刷新单内用例的最新状态、推测试单的
// 最后执行时间。少做任何一件，列表就会出现「跑过了但还显示未执行」这类
// 说不清的状态。
func (s *TestTaskService) RecordRun(ctx context.Context, taskID, caseID uint, req dto.TestRunReq) (*dto.TestRunResp, error) {
	if !slices.Contains(model.TestRunResults, req.Result) {
		return nil, apperr.Newf(apperr.InvalidParam, "执行结论取值不合法：%s", req.Result)
	}
	task, err := s.tasks.GetByID(ctx, taskID)
	if err != nil {
		return nil, err
	}
	if err := s.assertVisible(ctx, task); err != nil {
		return nil, err
	}
	// 结了的单不再记结果：结果要进的是当次测试活动，
	// 复测应该重开单或者新建单，否则历史会被混淆到已定论的单上。
	if task.Status == model.TestTaskStatusClosed {
		return nil, apperr.Newf(apperr.TestTaskStatusInvalid, "测试单已结单，不能继续记录执行结果；请先重新打开")
	}
	link, err := s.links.Find(ctx, taskID, caseID)
	if err != nil {
		return nil, err
	}
	if link == nil {
		return nil, apperr.New(apperr.TestTaskCaseNotFound)
	}
	testCase, err := s.cases.GetByID(ctx, caseID)
	if err != nil {
		return nil, err
	}

	// 步骤快照取用例「此刻」的内容，但记下它的版本：
	// 单里锁的是纳入时的版本，实际跑的可能是更新的一版，两者都留痕。
	steps, err := s.steps.ListByCase(ctx, caseID)
	if err != nil {
		return nil, err
	}
	snapshot := snapshotSteps(steps, req.Steps)
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		return nil, apperr.Wrap(apperr.Internal, err)
	}
	bugIDs, err := encodeIDs(req.BugIDs)
	if err != nil {
		return nil, err
	}

	actor := currentUserID(ctx)
	now := time.Now().UTC()
	buildID := req.BuildID
	if buildID == 0 {
		buildID = task.BuildID
	}

	run := &model.TestRun{
		TaskID: taskID, CaseID: caseID, CaseVersion: testCase.Version,
		ProductID: testCase.ProductID, ProjectID: task.ProjectID,
		ExecutionID: task.ExecutionID, BuildID: buildID,
		Result: req.Result, Steps: string(encoded), Note: req.Note,
		BugIDs:     bugIDs,
		ExecutedBy: actor, ExecutedDate: &now,
		Duration: req.Duration,
	}

	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := s.runs.Create(ctx, tx, run); err != nil {
			return err
		}
		if err := s.links.UpdateAfterRun(ctx, tx, taskID, caseID, req.Result, now); err != nil {
			return err
		}
		if err := s.tasks.TouchLastRun(ctx, tx, taskID, now); err != nil {
			return err
		}
		return s.record(ctx, tx, history.Entry{
			ObjectType: model.ObjectTypeTestTask, ObjectID: taskID,
			ProductID: task.ProductID, ProjectID: task.ProjectID, ExecutionID: task.ExecutionID,
			ActorID: actor, Action: model.ActionRun,
			// Extra 存「用例 id:结论」：时间线上要能一眼看出跑了哪条、结果如何。
			Extra:   itoa(caseID) + ":" + req.Result,
			Comment: req.Note,
		})
	})
	if err != nil {
		return nil, err
	}

	resp, err := s.toRunResp(ctx, []model.TestRun{*run})
	if err != nil {
		return nil, err
	}
	return &resp[0], nil
}

// ListRuns 查询某单（可选某用例）的执行历史。
func (s *TestTaskService) ListRuns(ctx context.Context, req dto.TestRunFilterReq) (*pagination.Result[dto.TestRunResp], error) {
	visible, err := s.gate.VisibleProducts(ctx)
	if err != nil {
		return nil, err
	}
	result, err := s.runs.List(ctx, repository.TestRunFilter{
		TaskID: req.TaskID, CaseID: req.CaseID, Result: req.Result,
		ProductIDs: visible,
		Pagination: pagination.Params{Page: req.Page, Size: req.Size, Order: req.Order},
	})
	if err != nil {
		return nil, err
	}
	resps, err := s.toRunResp(ctx, result.List)
	if err != nil {
		return nil, err
	}
	return &pagination.Result[dto.TestRunResp]{
		List: resps, Total: result.Total, Page: result.Page, Size: result.Size,
	}, nil
}

// ---------------------------------------------------------------------------
// 装配与校验
// ---------------------------------------------------------------------------

// assertTarget 校验测试单挂靠的产品/项目/迭代/构建彼此一致。
func (s *TestTaskService) assertTarget(ctx context.Context, req dto.TestTaskReq) error {
	product, err := s.products.FindByID(ctx, req.ProductID)
	if err != nil {
		return err
	}
	if product == nil {
		return apperr.New(apperr.ProductNotFound)
	}
	if err := s.gate.AssertProductVisible(ctx, req.ProductID); err != nil {
		return err
	}

	if req.ProjectID > 0 {
		if err := s.gate.AssertProjectVisible(ctx, req.ProjectID); err != nil {
			return err
		}
		if _, err := s.projects.GetByID(ctx, req.ProjectID); err != nil {
			return err
		}
	}
	if req.ExecutionID > 0 {
		if err := s.gate.AssertExecutionVisible(ctx, req.ExecutionID); err != nil {
			return err
		}
		execution, err := s.executions.GetByID(ctx, req.ExecutionID)
		if err != nil {
			return err
		}
		// 迭代必须属于所选项目（给了项目的情况下），否则筛选会出现
		// 「在项目 A 的列表里看到挂着迭代 B 的单」。
		if req.ProjectID > 0 && execution.ProjectID != req.ProjectID {
			return apperr.Newf(apperr.ExecutionNotFound, "所选迭代不属于该项目")
		}
	}
	if req.BuildID > 0 {
		build, err := s.builds.FindByID(ctx, req.BuildID)
		if err != nil {
			return err
		}
		if build == nil {
			return apperr.Newf(apperr.InvalidParam, "所选构建不存在")
		}
		if build.ProductID != req.ProductID {
			return apperr.Newf(apperr.InvalidParam, "所选构建不属于该产品")
		}
	}
	if req.End != "" && req.Begin != "" && req.End < req.Begin {
		return apperr.Newf(apperr.ProjectDateInvalid, "结束日期不能早于开始日期")
	}
	return nil
}

// parseTaskDates 解析计划起止日期。
func parseTaskDates(begin, end string) (*time.Time, *time.Time, error) {
	b, err := parseOptionalDate(begin)
	if err != nil {
		return nil, nil, err
	}
	e, err := parseOptionalDate(end)
	if err != nil {
		return nil, nil, err
	}
	return b, e, nil
}

// snapshotSteps 把客户端提交的「实际结果」与用例步骤原文合成快照。
//
// Step/Expect 由后端按用例内容回填，而不是相信客户端传来的：
// 前端传什么就存什么的话，一份被改过的步骤描述会伪装成「当时跑的就是这样」。
func snapshotSteps(caseSteps []model.TestCaseStep, submitted []dto.TestRunStepReq) []dto.TestRunStepReq {
	byOrder := make(map[int]dto.TestRunStepReq, len(submitted))
	for _, item := range submitted {
		byOrder[item.Order] = item
	}
	out := make([]dto.TestRunStepReq, 0, len(caseSteps))
	for i, step := range caseSteps {
		order := step.Order
		if order == 0 {
			order = i + 1
		}
		got := dto.TestRunStepReq{
			Order: order, Step: step.Step, Expect: step.Expect,
		}
		if in, ok := byOrder[order]; ok {
			got.Actual = in.Actual
			got.Result = in.Result
		}
		out = append(out, got)
	}
	return out
}

// assertVisible 按「最贴近的那一层」判可见性，与缺陷同一口径。
func (s *TestTaskService) assertVisible(ctx context.Context, task *model.TestTask) error {
	switch {
	case task.ExecutionID > 0:
		return s.gate.AssertExecutionVisible(ctx, task.ExecutionID)
	case task.ProjectID > 0:
		return s.gate.AssertProjectVisible(ctx, task.ProjectID)
	default:
		return s.gate.AssertProductVisible(ctx, task.ProductID)
	}
}

// notifyAssigned 通知新的测试单负责人。
func (s *TestTaskService) notifyAssigned(ctx context.Context, tx *gorm.DB, task *model.TestTask, actor uint) error {
	if task.AssignedTo == 0 || task.AssignedTo == actor {
		return nil
	}
	return s.notifier.Send(ctx, tx, task.AssignedTo, actor,
		model.ObjectTypeTestTask, task.ID, "有测试单指派给你",
		"测试单「"+task.Name+"」由你负责")
}

// record 写操作历史。
func (s *TestTaskService) record(ctx context.Context, tx *gorm.DB, e history.Entry) error {
	if e.Changes = fillUserNames(ctx, s.users, e.Changes); len(e.Changes) == 0 {
		e.Changes = nil
	}
	_, err := s.recorder.Record(ctx, tx, e)
	return err
}

// getResp 返回详情（写接口的返回体）。
func (s *TestTaskService) getResp(ctx context.Context, id uint) (*dto.TestTaskResp, error) {
	detail, err := s.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	return &detail.TestTaskResp, nil
}

// listCases 取单内用例清单，并补上标题与「用例此刻的版本」。
func (s *TestTaskService) listCases(ctx context.Context, taskID uint) ([]dto.TaskCaseResp, error) {
	visible, err := s.gate.VisibleProducts(ctx)
	if err != nil {
		return nil, err
	}
	result, err := s.links.List(ctx, repository.TestTaskCaseFilter{
		TaskID: taskID, ProductIDs: visible,
		Pagination: pagination.Params{Page: 1, Size: 500},
	})
	if err != nil {
		return nil, err
	}
	if len(result.List) == 0 {
		return nil, nil
	}

	caseIDs := make([]uint, 0, len(result.List))
	userIDs := make([]uint, 0, len(result.List))
	for i := range result.List {
		caseIDs = append(caseIDs, result.List[i].CaseID)
		userIDs = append(userIDs, result.List[i].AssignedTo)
	}
	titles, err := s.cases.TitleMap(ctx, caseIDs)
	if err != nil {
		return nil, err
	}
	rows, err := s.cases.ListByIDs(ctx, caseIDs)
	if err != nil {
		return nil, err
	}
	currentVersions := make(map[uint]int, len(rows))
	for i := range rows {
		currentVersions[rows[i].ID] = rows[i].Version
	}
	names, err := s.users.NameMap(ctx, userIDs)
	if err != nil {
		return nil, err
	}

	out := make([]dto.TaskCaseResp, 0, len(result.List))
	for i := range result.List {
		link := &result.List[i]
		out = append(out, dto.TaskCaseResp{
			CaseID: link.CaseID, CaseTitle: titles[link.CaseID],
			CaseVersion: link.CaseVersion, CaseCurrentVersion: currentVersions[link.CaseID],
			ProductID: link.ProductID, StoryID: link.StoryID,
			LastResult: link.LastResult, LastRunDate: formatTimeValue(link.LastRunDate),
			RunCount:   link.RunCount,
			AssignedTo: link.AssignedTo, AssignedName: names[link.AssignedTo],
			Order: link.Order,
		})
	}
	return out, nil
}

// decorate 补齐展示字段。
func (s *TestTaskService) decorate(ctx context.Context, result *pagination.Result[model.TestTask]) (*pagination.Result[dto.TestTaskResp], error) {
	tasks := result.List
	if len(tasks) == 0 {
		return &pagination.Result[dto.TestTaskResp]{
			List: []dto.TestTaskResp{}, Total: result.Total, Page: result.Page, Size: result.Size,
		}, nil
	}

	productIDs := make([]uint, 0, len(tasks))
	projectIDs := make([]uint, 0, len(tasks))
	executionIDs := make([]uint, 0, len(tasks))
	buildIDs := make([]uint, 0, len(tasks))
	taskIDs := make([]uint, 0, len(tasks))
	userIDs := make([]uint, 0, len(tasks)*3)
	for i := range tasks {
		t := &tasks[i]
		productIDs = append(productIDs, t.ProductID)
		projectIDs = append(projectIDs, t.ProjectID)
		executionIDs = append(executionIDs, t.ExecutionID)
		buildIDs = append(buildIDs, t.BuildID)
		taskIDs = append(taskIDs, t.ID)
		userIDs = append(userIDs, t.AssignedTo, t.OpenedBy)
	}

	products, err := s.products.NameMap(ctx, productIDs)
	if err != nil {
		return nil, err
	}
	projects, err := s.projects.NameMap(ctx, projectIDs)
	if err != nil {
		return nil, err
	}
	builds, err := s.builds.NameMap(ctx, buildIDs)
	if err != nil {
		return nil, err
	}
	users, err := s.users.NameMap(ctx, userIDs)
	if err != nil {
		return nil, err
	}
	summaries, err := s.summaries(ctx, taskIDs)
	if err != nil {
		return nil, err
	}

	actionsByStatus := make(map[string][]dto.TransitionResp, 4)
	out := make([]dto.TestTaskResp, 0, len(tasks))
	for i := range tasks {
		t := &tasks[i]
		resp := dto.TestTaskResp{
			ID: t.ID, Code: "TESTTASK-" + itoa(t.ID),
			ProductID: t.ProductID, ProductName: products[t.ProductID],
			ProjectID: t.ProjectID, ProjectName: projects[t.ProjectID],
			ExecutionID: t.ExecutionID, BuildID: t.BuildID,
			BuildName: builds[t.BuildID],
			Name:      t.Name, Desc: t.Desc, Pri: t.Pri,
			Status: t.Status, Result: t.Result, Report: t.Report,
			AssignedTo: t.AssignedTo, AssignedName: users[t.AssignedTo],
			Begin: formatDate(t.Begin), End: formatDate(t.End),
			StartedAt:   formatTimeValue(t.StartedAt),
			FinishedAt:  formatTimeValue(t.FinishedAt),
			LastRunDate: formatTimeValue(t.LastRunDate),
			OpenedBy:    t.OpenedBy, OpenedName: users[t.OpenedBy],
			OpenedDate: formatTimeValue(t.OpenedDate),
			CaseCount:  t.CaseCount,
			Summary:    summaries[t.ID],
			Actions:    cachedActions(ctx, actionsByStatus, t.Status),
		}
		out = append(out, resp)
	}
	return &pagination.Result[dto.TestTaskResp]{
		List: out, Total: result.Total, Page: result.Page, Size: result.Size,
	}, nil
}

// cachedActions 按状态取可用动作，同页内同一状态只算一次。
//
// 可用动作只取决于「状态 + 当前用户权限」，与具体是哪张单无关，
// 所以同状态的 20 行不必问 20 次机器。
func cachedActions(ctx context.Context, cache map[string][]dto.TransitionResp, status string) []dto.TransitionResp {
	if actions, ok := cache[status]; ok {
		return actions
	}
	actions := availableStateActions(ctx, workflow.TestTaskMachine, status)
	cache[status] = actions
	return actions
}

// summaries 批量取多张单的执行汇总。
func (s *TestTaskService) summaries(ctx context.Context, taskIDs []uint) (map[uint]dto.TestTaskSummary, error) {
	out := make(map[uint]dto.TestTaskSummary, len(taskIDs))
	var rows []struct {
		TaskID     uint
		LastResult string
		Total      int64
	}
	err := s.db.WithContext(ctx).Model(&model.TestTaskCase{}).
		Where("task_id IN ?", taskIDs).
		Select("task_id, pm_testtaskcase.last_result, COUNT(*) AS total").
		Group("task_id, pm_testtaskcase.last_result").Scan(&rows).Error
	if err != nil {
		return nil, apperr.Wrap(apperr.Internal, err)
	}
	for _, row := range rows {
		summary := out[row.TaskID]
		switch row.LastResult {
		case model.TestRunResultPass:
			summary.Pass += row.Total
		case model.TestRunResultFail:
			summary.Fail += row.Total
		case model.TestRunResultBlocked:
			summary.Blocked += row.Total
		default:
			summary.Unrun += row.Total
		}
		summary.Total += row.Total
		out[row.TaskID] = summary
	}
	return out, nil
}

// toRunResp 把执行记录整理成出参（补用例标题、执行人姓名、缺陷摘要）。
func (s *TestTaskService) toRunResp(ctx context.Context, runs []model.TestRun) ([]dto.TestRunResp, error) {
	if len(runs) == 0 {
		return nil, nil
	}
	caseIDs := make([]uint, 0, len(runs))
	userIDs := make([]uint, 0, len(runs))
	bugIDs := make([]uint, 0, len(runs))
	for i := range runs {
		caseIDs = append(caseIDs, runs[i].CaseID)
		userIDs = append(userIDs, runs[i].ExecutedBy)
		ids, err := decodeIDs(runs[i].BugIDs)
		if err != nil {
			continue
		}
		bugIDs = append(bugIDs, ids...)
	}
	titles, err := s.cases.TitleMap(ctx, caseIDs)
	if err != nil {
		return nil, err
	}
	names, err := s.users.NameMap(ctx, userIDs)
	if err != nil {
		return nil, err
	}
	bugNames := make(map[uint]string, len(bugIDs))
	if len(bugIDs) > 0 {
		bugNames, err = s.bugTitles(ctx, bugIDs)
		if err != nil {
			return nil, err
		}
	}

	out := make([]dto.TestRunResp, 0, len(runs))
	for i := range runs {
		r := &runs[i]
		var steps []dto.TestRunStepReq
		if r.Steps != "" {
			// 快照坏了不影响其它字段：历史宁可少显示一段步骤，也不该整条读不出来。
			var decoded []dto.TestRunStepReq
			if err := json.Unmarshal([]byte(r.Steps), &decoded); err == nil {
				steps = decoded
			}
		}
		ids, _ := decodeIDs(r.BugIDs)
		resp := dto.TestRunResp{
			ID: r.ID, TaskID: r.TaskID, CaseID: r.CaseID,
			CaseTitle: titles[r.CaseID], CaseVersion: r.CaseVersion,
			ProductID: r.ProductID, ProjectID: r.ProjectID,
			ExecutionID: r.ExecutionID, BuildID: r.BuildID,
			Result: r.Result, Steps: steps, Note: r.Note,
			BugIDs: ids, ExecutedBy: r.ExecutedBy,
			ExecutedName: names[r.ExecutedBy],
			ExecutedDate: formatTimeValue(r.ExecutedDate),
			Duration:     r.Duration,
		}
		for _, id := range ids {
			if name, ok := bugNames[id]; ok {
				resp.BugNames = append(resp.BugNames, name)
			}
		}
		out = append(out, resp)
	}
	return out, nil
}

// bugTitles 批量取缺陷标题（执行记录里关联的缺陷要显示得出来）。
func (s *TestTaskService) bugTitles(ctx context.Context, ids []uint) (map[uint]string, error) {
	var rows []struct {
		ID    uint
		Title string
	}
	err := s.db.WithContext(ctx).Model(&model.Bug{}).
		Where("id IN ?", ids).Select("id, title").Scan(&rows).Error
	if err != nil {
		return nil, apperr.Wrap(apperr.Internal, err)
	}
	out := make(map[uint]string, len(rows))
	for _, row := range rows {
		out[row.ID] = "BUG-" + itoa(row.ID) + " " + row.Title
	}
	return out, nil
}

// testTaskResultLabel 是结论的中文名。
func testTaskResultLabel(result string) string {
	switch result {
	case model.TestTaskResultPass:
		return "通过"
	case model.TestTaskResultFail:
		return "不通过"
	case model.TestTaskResultBlocked:
		return "因故未完成"
	default:
		return result
	}
}
