package service

import (
	"context"
	"time"

	"github.com/xywf221/xuliu/internal/dto"
	"github.com/xywf221/xuliu/internal/model"
	"github.com/xywf221/xuliu/internal/pkg/apperr"
	"github.com/xywf221/xuliu/internal/pkg/pagination"
	"github.com/xywf221/xuliu/internal/repository"
	"github.com/xywf221/xuliu/internal/workflow"
)

// WorkbenchService 工作台与燃尽图。
//
// 放在一起是因为两者性质相同：都是**只读的聚合视图**，都建立在
// 任务/缺陷/需求/工时这几张表上，都只回答「现在怎么样了」；
// 差别只是工作台看的是「我的」，燃尽图看的是「某个迭代/项目的」。
//
// 这里没有任何写操作：工作台不该成为绕过业务规则改数据的捷径。
type WorkbenchService struct {
	tasks      *repository.TaskRepo
	bugs       *repository.BugRepo
	stories    *repository.StoryRepo
	reviews    *repository.StoryReviewRepo
	efforts    *repository.EffortRepo
	notices    *repository.NoticeRepo
	users      *repository.UserRepo
	products   *repository.ProductRepo
	projects   *repository.ProjectRepo
	executions *repository.ExecutionRepo
	gate       *Gate
}

// NewWorkbenchService 构造工作台服务。
func NewWorkbenchService(
	tasks *repository.TaskRepo,
	bugs *repository.BugRepo,
	stories *repository.StoryRepo,
	reviews *repository.StoryReviewRepo,
	efforts *repository.EffortRepo,
	notices *repository.NoticeRepo,
	users *repository.UserRepo,
	products *repository.ProductRepo,
	projects *repository.ProjectRepo,
	executions *repository.ExecutionRepo,
	gate *Gate,
) *WorkbenchService {
	return &WorkbenchService{
		tasks: tasks, bugs: bugs, stories: stories, reviews: reviews,
		efforts: efforts, notices: notices, users: users,
		products: products, projects: projects, executions: executions, gate: gate,
	}
}

// 工作台与燃尽图各自取多少条。
//
// 都是「首屏看得见」的量级：工作台是待办清单，几十条足够；
// 燃尽图要把范围内的任务与工时全读出来才能算，上限给得大一些
// （真到五千个任务的迭代，图本身也已经没有可读性了）。
const (
	workbenchLimit = 50
	burndownLimit  = 5000
)

// Get 返回工作台首屏所需的全部数据。
func (s *WorkbenchService) Get(ctx context.Context) (*dto.WorkbenchResp, error) {
	userID := currentUserID(ctx)
	if userID == 0 {
		return nil, apperr.New(apperr.Unauthorized)
	}

	visibleProjects, err := s.gate.VisibleProjects(ctx)
	if err != nil {
		return nil, err
	}
	visibleProducts, err := s.gate.VisibleProducts(ctx)
	if err != nil {
		return nil, err
	}
	today := truncateToDay(time.Now().UTC())
	mine := userID

	resp := &dto.WorkbenchResp{
		Tasks:   []dto.WorkbenchTaskResp{},
		Bugs:    []dto.WorkbenchBugResp{},
		Reviews: []dto.WorkbenchReviewResp{},
		Notices: []dto.NoticeResp{},
	}

	// ---- 我的任务 ----
	// 「我的任务」用可见项目夹一层：一个人被移出项目后，项目里残留的
	// 指派不该继续出现在他的工作台上（那是他既看不到也处理不了的东西）。
	taskFilter := repository.TaskFilter{
		AssignedTo:        &mine,
		Unfinished:        true,
		VisibleProjectIDs: visibleProjects,
		ProductIDs:        visibleProducts,
		Pagination:        pagination.Params{Page: 1, Size: workbenchLimit, Order: "deadline_asc"},
	}
	tasks, err := s.tasks.List(ctx, taskFilter)
	if err != nil {
		return nil, err
	}
	taskCounts, err := s.tasks.StatusCounts(ctx, taskFilter)
	if err != nil {
		return nil, err
	}
	overdue, err := s.tasks.CountOverdue(ctx, taskFilter, today)
	if err != nil {
		return nil, err
	}
	resp.Counts.TaskDoing = int(taskCounts[model.TaskStatusDoing])
	// 暂停中的任务也算「还没开工/没做完」，与"未完成"的判定保持一致。
	resp.Counts.TaskWait = int(taskCounts[model.TaskStatusWait] + taskCounts[model.TaskStatusPause])
	resp.Counts.TaskOverdue = int(overdue)

	projectNames, err := s.projects.NameMap(ctx, collectIDs(tasks.List, func(task *model.Task) uint {
		return task.ProjectID
	}))
	if err != nil {
		return nil, err
	}
	execNames, err := s.executions.NameMap(ctx, collectIDs(tasks.List, func(task *model.Task) uint {
		return task.ExecutionID
	}))
	if err != nil {
		return nil, err
	}
	for i := range tasks.List {
		task := &tasks.List[i]
		resp.Tasks = append(resp.Tasks, dto.WorkbenchTaskResp{
			ID: task.ID, Name: task.Name,
			ProjectID: task.ProjectID, ProjectName: projectNames[task.ProjectID],
			ExecName: execNames[task.ExecutionID],
			Status:   task.Status, StatusLabel: statusLabelOf(model.ObjectTypeTask, task.Status),
			Pri: task.Pri, Estimate: task.Estimate, Consumed: task.Consumed, Left: task.Left,
			Progress: hoursProgress(task.Consumed, task.Left),
			Deadline: formatDate(task.Deadline),
			Overdue:  isOverdue(task.Deadline, task.Status, today),
		})
	}

	// ---- 我的缺陷 ----
	// 列表只列「未解决」的：已解决的缺陷不该再占工作台的位置。
	// 计数则用不带状态的同一条件，顺手拿到各状态分布。
	bugScope := repository.BugFilter{
		AssignedTo: &mine,
		ProductIDs: visibleProducts, ProjectIDs: visibleProjects,
	}
	bugList := bugScope
	bugList.Status = model.BugStatusActive
	bugList.Pagination = pagination.Params{
		Page: 1, Size: workbenchLimit, Order: "severity_asc,deadline_asc",
	}

	bugs, err := s.bugs.List(ctx, bugList)
	if err != nil {
		return nil, err
	}
	bugCounts, err := s.bugs.StatusCounts(ctx, bugScope)
	if err != nil {
		return nil, err
	}
	resp.Counts.BugActive = int(bugCounts[model.BugStatusActive])

	bugProducts, err := s.products.NameMap(ctx, collectIDs(bugs.List, func(b *model.Bug) uint {
		return b.ProductID
	}))
	if err != nil {
		return nil, err
	}
	for i := range bugs.List {
		bug := &bugs.List[i]
		resp.Bugs = append(resp.Bugs, dto.WorkbenchBugResp{
			ID: bug.ID, Code: "BUG-" + itoa(bug.ID), Title: bug.Title,
			ProductID: bug.ProductID, ProductName: bugProducts[bug.ProductID],
			Type: bug.Type, TypeLabel: bugTypeLabels[bug.Type],
			Severity: bug.Severity, Pri: bug.Pri,
			Status: bug.Status, StatusLabel: statusLabelOf(model.ObjectTypeBug, bug.Status),
			Deadline: formatDate(bug.Deadline),
			// 缺陷没有「完成」，过了截止日期还在激活态就是逾期。
			Overdue: isOverdue(bug.Deadline, model.BugStatusActive, today),
		})
	}

	// ---- 待我评审的需求 ----
	if err := s.fillReviews(ctx, resp, userID); err != nil {
		return nil, err
	}

	// ---- 未读通知 ----
	unread, err := s.notices.CountUnread(ctx, userID)
	if err != nil {
		return nil, err
	}
	resp.Counts.NoticeUnread = unread
	noticeRows, err := s.notices.ListUnread(ctx, userID, workbenchLimit)
	if err != nil {
		return nil, err
	}
	fromNames, err := s.users.NameMap(ctx, collectIDs(noticeRows, func(n *model.Notice) uint {
		return n.FromUser
	}))
	if err != nil {
		return nil, err
	}
	for i := range noticeRows {
		n := &noticeRows[i]
		resp.Notices = append(resp.Notices, dto.NoticeResp{
			ID: n.ID, FromUser: n.FromUser, FromName: fromNames[n.FromUser],
			ObjectType: n.ObjectType, ObjectID: n.ObjectID,
			Title: n.Title, Content: n.Content, Link: n.Link,
			CreatedAt: n.CreatedAt.UTC().Format(time.RFC3339),
		})
	}

	// ---- 我的工时 ----
	// 周起点取周一：这是团队协作里最常用的一周口径。
	weekBegin := today.AddDate(0, 0, -int((today.Weekday()+6)%7))
	tomorrow := today.AddDate(0, 0, 1)
	if resp.Efforts.Today, err = s.efforts.SumConsumed(ctx, userID, &today, &tomorrow); err != nil {
		return nil, err
	}
	if resp.Efforts.Week, err = s.efforts.SumConsumed(ctx, userID, &weekBegin, &tomorrow); err != nil {
		return nil, err
	}
	if resp.Efforts.Total, err = s.efforts.SumConsumed(ctx, userID, nil, nil); err != nil {
		return nil, err
	}

	return resp, nil
}

// fillReviews 填「待我评审」这一段。
//
// 评审待办取自 pm_storyreview 的「未给结论」行，而不是去解析
// 需求上的评审人字段：结论是按人记的，按人查才不会有"我评过了
// 但名单里还留着我"这种需要二次过滤的麻烦。
func (s *WorkbenchService) fillReviews(ctx context.Context, resp *dto.WorkbenchResp, userID uint) error {
	rows, err := s.reviews.ListPendingByReviewer(ctx, userID, workbenchLimit)
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	storyIDs := make([]uint, 0, len(rows))
	for i := range rows {
		storyIDs = append(storyIDs, rows[i].StoryID)
	}
	stories, err := s.stories.ListByIDs(ctx, storyIDs)
	if err != nil {
		return err
	}
	productNames, err := s.products.NameMap(ctx, collectIDs(stories, func(story *model.Story) uint {
		return story.ProductID
	}))
	if err != nil {
		return err
	}
	openerNames, err := s.users.NameMap(ctx, collectIDs(stories, func(story *model.Story) uint {
		return story.OpenedBy
	}))
	if err != nil {
		return err
	}
	for _, story := range stories {
		// 名单里还留着、但需求已经不在评审中的（别人先给了结论），
		// 不该继续占着待办位置。
		if story.Status != model.StoryStatusReviewing {
			continue
		}
		resp.Reviews = append(resp.Reviews, dto.WorkbenchReviewResp{
			StoryID: story.ID, Title: story.Title,
			ProductID: story.ProductID, ProductName: productNames[story.ProductID],
			Version: story.Version, Pri: story.Pri,
			OpenedBy: story.OpenedBy, OpenedName: openerNames[story.OpenedBy],
			OpenedDate: formatTimeValue(story.OpenedDate),
		})
	}
	resp.Counts.StoryReview = len(resp.Reviews)
	return nil
}

// ---------------------------------------------------------------------------
// 燃尽图
// ---------------------------------------------------------------------------

// Burndown 返回某迭代或某项目的燃尽图。
//
// 数据来源是**工时记录**而不是任务的当前值：每条 pm_effort 都记着
// 「记完之后还剩多少」，于是任意一天的剩余工作量都能还原。
// 只看任务的 Estimate/Left 画不出历史 —— 那只是「此刻」的快照。
//
// 三处必须说清楚的取舍：
//
//  1. 任务在第一次报工之前，按它的 Estimate 计入剩余。也就是把「估算」
//     当作一开始就存在的工作量，而不是等报工了才凭空出现。
//  2. 后来改过的 Estimate 会同时改写历史曲线。我们没有估算的快照表，
//     所以画出的是「按当前估算重算的过去」，不是当时的原始值。
//  3. 理想线是总估算从起点到终点线性递减，不扣节假日 ——
//     我们没有工作日历，硬凑一个只会给出一个兑现不了的承诺。
func (s *WorkbenchService) Burndown(ctx context.Context, req dto.BurndownReq) (*dto.BurndownResp, error) {
	begin, end, err := s.burndownRange(ctx, req)
	if err != nil {
		return nil, err
	}

	visibleProducts, err := s.gate.VisibleProducts(ctx)
	if err != nil {
		return nil, err
	}
	tasks, err := s.tasks.ListAll(ctx, repository.TaskFilter{
		ProjectID: req.ProjectID, ExecutionID: req.ExecutionID,
		// 指定了单个项目/迭代时就以它为准（可见性已在 burndownRange 里验过），
		// 只按项目画时需要一份"可见项目"清单来兜底。
		VisibleProjectIDs: burndownVisibleProjects(ctx, s.gate, req.ProjectID),
		ProductIDs:        visibleProducts,
	}, burndownLimit)
	if err != nil {
		return nil, err
	}

	beginKey, endKey := begin.Format(dateLayout), end.Format(dateLayout)
	// 窗口结束后才建的任务不属于这条曲线：它在那段时间里还不存在。
	// 窗口开始前就有的任务当然算 —— 它一开始就占着工作量。
	scoped := make([]model.Task, 0, len(tasks))
	taskIDs := make([]uint, 0, len(tasks))
	for i := range tasks {
		if tasks[i].CreatedAt.UTC().Format(dateLayout) > endKey {
			continue
		}
		scoped = append(scoped, tasks[i])
		taskIDs = append(taskIDs, tasks[i].ID)
	}
	tasks = scoped

	efforts, err := s.efforts.ListByObjectIDs(ctx, model.ObjectTypeTask, taskIDs)
	if err != nil {
		return nil, err
	}

	resp := &dto.BurndownResp{
		Scope:     req.Scope(),
		ScopeID:   req.ScopeID(),
		Begin:     beginKey,
		End:       endKey,
		TaskCount: len(tasks),
		Days:      []dto.BurndownDayResp{},
	}
	for i := range tasks {
		resp.TotalEstimate += tasks[i].Estimate
	}

	// 把每天发生的两件事整理好：当天报掉的工时、当天报完之后的剩余。
	// 只统计落在曲线范围内的工时 —— 范围外报的工不属于这条曲线。
	consumedByDay := make(map[string]float64, len(efforts))
	leftByTaskByDay := make(map[string]map[uint]float64, len(efforts))
	for i := range efforts {
		effort := &efforts[i]
		day := effort.Date.UTC().Format(dateLayout)
		if day < beginKey || day > endKey {
			continue
		}
		resp.TotalConsumed += effort.Consumed
		consumedByDay[day] += effort.Consumed
		if leftByTaskByDay[day] == nil {
			leftByTaskByDay[day] = make(map[uint]float64, 8)
		}
		// 同一天同一任务报了多次：以最后一条为准（查询已按时间排序）。
		leftByTaskByDay[day][effort.ObjectID] = effort.Left
	}
	addedByDay := make(map[string]float64, len(tasks))
	for i := range tasks {
		created := tasks[i].CreatedAt.UTC().Format(dateLayout)
		if created < beginKey || created > endKey {
			continue
		}
		addedByDay[created] += tasks[i].Estimate
	}

	// 每天的剩余 = 每个「当天已经在范围内」的任务，取它当天最后已知的剩余
	// （当天报过工就是报完后的值，没报过就沿用上一次的值，从未报过就按估算）。
	//
	// 任务从**被创建的那天**才开始计入：一上来就把全部任务算进去的话，
	// 曲线会在任务还不存在的时候就开始消耗，而中途加进迭代的任务也永远
	// 不会表现为一次新增 —— 那正是 Added 这一列要说明的事。
	knownLeft := make(map[uint]float64, len(tasks))
	days := daysBetween(begin, end)
	idealStep := 0.0
	if len(days) > 1 {
		idealStep = resp.TotalEstimate / float64(len(days)-1)
	}
	for i, day := range days {
		key := day.Format(dateLayout)
		for taskID, left := range leftByTaskByDay[key] {
			knownLeft[taskID] = left
		}
		remaining := 0.0
		for ti := range tasks {
			task := &tasks[ti]
			if task.CreatedAt.UTC().Format(dateLayout) > key {
				continue
			}
			if left, known := knownLeft[task.ID]; known {
				remaining += left
			} else {
				remaining += task.Estimate
			}
		}
		ideal := resp.TotalEstimate - idealStep*float64(i)
		if ideal < 0 {
			ideal = 0
		}
		resp.Days = append(resp.Days, dto.BurndownDayResp{
			Date:      key,
			Remaining: round2(remaining),
			Consumed:  round2(consumedByDay[key]),
			Added:     round2(addedByDay[key]),
			Ideal:     round2(ideal),
		})
	}
	return resp, nil
}

// burndownRange 确定曲线的起止日期。
//
// 优先用请求里给的日期，其次用迭代/项目自身的起止日期；都没有就问调用方要
// —— 猜一个范围只会画出一条没人认得的曲线。
func (s *WorkbenchService) burndownRange(ctx context.Context, req dto.BurndownReq) (time.Time, time.Time, error) {
	var begin, end *time.Time

	switch {
	case req.ExecutionID > 0:
		if err := s.gate.AssertExecutionVisible(ctx, req.ExecutionID); err != nil {
			return time.Time{}, time.Time{}, err
		}
		execution, err := s.executions.GetByID(ctx, req.ExecutionID)
		if err != nil {
			return time.Time{}, time.Time{}, err
		}
		if req.ProjectID > 0 && execution.ProjectID != req.ProjectID {
			return time.Time{}, time.Time{}, apperr.Newf(apperr.ExecutionNotFound,
				"所选迭代不属于该项目")
		}
		begin, end = execution.Begin, execution.End

	case req.ProjectID > 0:
		if err := s.gate.AssertProjectVisible(ctx, req.ProjectID); err != nil {
			return time.Time{}, time.Time{}, err
		}
		project, err := s.projects.GetByID(ctx, req.ProjectID)
		if err != nil {
			return time.Time{}, time.Time{}, err
		}
		begin, end = project.Begin, project.End

	default:
		return time.Time{}, time.Time{}, apperr.Newf(apperr.InvalidParam,
			"请指定 projectId 或 executionId")
	}

	if req.Begin != "" {
		parsed, err := parseOptionalDate(req.Begin)
		if err != nil || parsed == nil {
			return time.Time{}, time.Time{}, apperr.Newf(apperr.InvalidParam,
				"开始日期格式应为 YYYY-MM-DD")
		}
		begin = parsed
	}
	if req.End != "" {
		parsed, err := parseOptionalDate(req.End)
		if err != nil || parsed == nil {
			return time.Time{}, time.Time{}, apperr.Newf(apperr.InvalidParam,
				"结束日期格式应为 YYYY-MM-DD")
		}
		end = parsed
	}
	if begin == nil || end == nil {
		return time.Time{}, time.Time{}, apperr.Newf(apperr.InvalidParam,
			"该对象还没有起止日期，请在请求里显式给出 begin 与 end")
	}

	b, e := truncateToDay(*begin), truncateToDay(*end)
	if e.Before(b) {
		return time.Time{}, time.Time{}, apperr.Newf(apperr.InvalidParam,
			"结束日期不能早于开始日期")
	}
	return b, e, nil
}

// burndownVisibleProjects 给出任务查询要用的可见项目。
//
// 指定了单个项目时就用它自己：既省一次全量查询，也避免出现
// 「能打开这个项目、它的 id 却没在可见列表里」这种空曲线。
func burndownVisibleProjects(ctx context.Context, gate *Gate, projectID uint) []uint {
	if projectID > 0 {
		return []uint{projectID}
	}
	ids, err := gate.VisibleProjects(ctx)
	if err != nil {
		return []uint{}
	}
	return ids
}

// dateLayout 是燃尽图里日期点的格式。
const dateLayout = "2006-01-02"

// daysBetween 返回 [begin, end] 逐日的日期列表（含首尾）。
func daysBetween(begin, end time.Time) []time.Time {
	out := make([]time.Time, 0, 32)
	for day := begin; !day.After(end); day = day.AddDate(0, 0, 1) {
		out = append(out, day)
		if len(out) >= 3660 {
			// 十年以上的范围基本可以断定是参数写错了，不必再往内存里堆。
			break
		}
	}
	return out
}

// truncateToDay 把时间截到当天零点（UTC）。
func truncateToDay(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

// terminalStatuses 是「已经收尾」的状态，这些状态谈不上逾期。
//
// 任务与缺陷的 closed 是同一个字面量，所以这里只列一次；
// 用集合而不是 switch，正是因为 switch 的 case 不接受重复的字面量。
var terminalStatuses = map[string]struct{}{
	model.TaskStatusDone:    {},
	model.TaskStatusClosed:  {}, // 任务与缺陷共用 "closed"
	model.TaskStatusCancel:  {},
	model.BugStatusResolved: {},
}

// isOverdue 判断截止日期是否已经过了（当天不算逾期）。
func isOverdue(deadline *time.Time, status string, today time.Time) bool {
	if deadline == nil {
		return false
	}
	if _, done := terminalStatuses[status]; done {
		return false
	}
	return truncateToDay(*deadline).Before(today)
}

// round2 保留两位小数，免得浮点误差在 JSON 里变成 7.999999999。
func round2(v float64) float64 { return roundTo(v, 2) }

// statusLabelOf 取某对象状态下对应的中文名。
func statusLabelOf(objectType, status string) string {
	if machine, ok := workflow.Get(objectType); ok {
		return machine.StatusLabel(status)
	}
	return status
}

// collectIDs 从一批模型里收集某个 uint 字段，去重并跳过 0（0 表示"没有"）。
func collectIDs[T any](rows []T, pick func(*T) uint) []uint {
	out := make([]uint, 0, len(rows))
	seen := make(map[uint]struct{}, len(rows))
	for i := range rows {
		id := pick(&rows[i])
		if id == 0 {
			continue
		}
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}
