package repository

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"

	"github.com/xywf221/xuliu/internal/model"
	"github.com/xywf221/xuliu/internal/pkg/apperr"
	"github.com/xywf221/xuliu/internal/pkg/pagination"
)

// 任务域仓储：任务主体、多人任务成员分工、工时记录。

var taskSorter = pagination.Sorter{
	"id":       "pm_task.id",
	"name":     "pm_task.name",
	"pri":      "pm_task.pri",
	"status":   "pm_task.status",
	"type":     "pm_task.type",
	"estimate": "pm_task.estimate",
	"consumed": "pm_task.consumed",
	"left":     "pm_task.left",
	"deadline": "pm_task.deadline",
	"opened":   "pm_task.opened_date",
	"finished": "pm_task.finished_date",
	"order":    "pm_task.order",
}

var effortSorter = pagination.Sorter{
	"id":       "pm_effort.id",
	"date":     "pm_effort.date",
	"consumed": "pm_effort.consumed",
}

// ---------------------------------------------------------------------------
// 任务
// ---------------------------------------------------------------------------

// TaskRepo 是任务仓储。
type TaskRepo struct {
	*Repo[model.Task]
}

// NewTaskRepo 构造任务仓储。
func NewTaskRepo(db *gorm.DB) *TaskRepo {
	return &TaskRepo{Repo: New[model.Task](db)}
}

// TaskFilter 是任务列表的过滤条件。
type TaskFilter struct {
	ProjectID   uint
	ExecutionID uint
	ProductID   uint
	StoryID     uint
	// ModuleID 为 nil 表示不过滤；传 0 表示「未分类」。
	ModuleID *uint
	Status   string
	// Statuses 用于「未完成的一组状态」，与 Status 互斥。
	Statuses   []string
	Type       string
	Pri        *int
	AssignedTo *uint
	OpenedBy   uint
	FinishedBy uint
	// ParentID 为 nil 表示不过滤；传 0 表示只看顶层任务。
	ParentID *uint
	// Unfinished 只看未完成（未开始/进行中/暂停）。
	Unfinished bool

	Keywords string
	// DeadlineFrom/To 按截止日期过滤（用于「快到期」视图）。
	DeadlineFrom *time.Time
	DeadlineTo   *time.Time

	// VisibleProjectIDs 是当前用户可见的项目 id。
	//
	// **空切片表示「一个都看不到」**，调用方必须无条件应用。
	VisibleProjectIDs []uint

	// ProductIDs 是当前用户可见的产品 id。
	//
	// 与 VisibleProjectIDs 是同一条约定：**空切片表示「一个都看不到」**。
	// 项目与产品是**与**的关系 —— 「这个项目我看得到」并不代表
	// 「项目里那条私有产品的任务我也看得到」，所以两层都要判。
	// 产品侧的判定已经把「参与该项目的项目/迭代成员」算成可见，
	// 所以在项目里干活的人不会因此被挡住。
	//
	// product_id = 0 的任务不属于任何产品（内部事务），不受这一层约束。
	ProductIDs []uint

	// AllProducts 跳过产品层过滤。
	//
	// 只给「项目自身的工时汇总」这类内部聚合用：它要的是这个项目的全部工时，
	// 而不是「当前用户看得见的那些任务的工时」—— 后者会算出一个随查看者
	// 变化、又与项目详情页对不上的数字。写成肯定式的名字，
	// 是为了让每一次放行在调用点上一眼可见。
	AllProducts bool

	Pagination pagination.Params
}

// List 分页查询任务。
func (r *TaskRepo) List(ctx context.Context, f TaskFilter) (*pagination.Result[model.Task], error) {
	q := r.taskQuery(ctx, f)
	return r.Paginate(ctx, q, f.Pagination, taskSorter, "pm_task.id DESC")
}

// ListAll 取满足条件的全部任务（不分页，用量上限兜底）。
//
// 与 ListByBoard 的区别是排序：看板按优先级排（同列里谁更急排前面），
// 报表只关心总和与集合，按 id 排更稳定。
func (r *TaskRepo) ListAll(ctx context.Context, f TaskFilter, limit int) ([]model.Task, error) {
	q := r.taskQuery(ctx, f).Order("pm_task.id ASC")
	if limit > 0 {
		q = q.Limit(limit)
	}
	var out []model.Task
	if err := q.Find(&out).Error; err != nil {
		return nil, translate(err)
	}
	return out, nil
}

// CountOverdue 统计已过截止日期却仍未完成的任务数。
//
// 工作台上最有用的一条数：它比「我有几个任务」更能说明问题。
// 判定放在库里而不是取回列表再数 —— 列表有分页，数出来的是错的。
func (r *TaskRepo) CountOverdue(ctx context.Context, f TaskFilter, before time.Time) (int64, error) {
	q := r.taskQuery(ctx, f).
		Where("pm_task.deadline IS NOT NULL AND pm_task.deadline < ?", before).
		Where("pm_task.status IN ?", model.TaskUnfinishedStatuses)
	var count int64
	if err := q.Count(&count).Error; err != nil {
		return 0, translate(err)
	}
	return count, nil
}

// ListByBoard 取看板所需的全部任务（不分页）。
//
// 看板要一次看到整列，分页反而会把同一列切开。这里用 limit 兜底，
// 避免一个超大项目把内存打满。
func (r *TaskRepo) ListByBoard(ctx context.Context, f TaskFilter, limit int) ([]model.Task, error) {
	q := r.taskQuery(ctx, f).Order("pm_task.pri ASC, pm_task.id DESC")
	if limit > 0 {
		q = q.Limit(limit)
	}
	var out []model.Task
	if err := q.Find(&out).Error; err != nil {
		return nil, translate(err)
	}
	return out, nil
}

// taskQuery 拼出任务列表的公共查询条件。
func (r *TaskRepo) taskQuery(ctx context.Context, f TaskFilter) *gorm.DB {
	return applyTaskFilter(r.DB.WithContext(ctx).Model(&model.Task{}), f)
}

// applyTaskFilter 把过滤条件应用到给定的查询上。
//
// 拆出来是为了让「列表」「汇总」「分组统计」三处共用同一份条件：
// 若各自拼一遍，某天给列表加了新过滤条件却忘了给汇总加，
// 就会出现「列表里 10 条、汇总工时却把删掉的那些也算进去」这类不一致。
func applyTaskFilter(q *gorm.DB, f TaskFilter) *gorm.DB {

	q = pagination.Eq(q, "pm_task.project_id", f.ProjectID)
	q = pagination.Eq(q, "pm_task.execution_id", f.ExecutionID)
	q = pagination.Eq(q, "pm_task.product_id", f.ProductID)
	q = pagination.Eq(q, "pm_task.story_id", f.StoryID)
	q = pagination.Eq(q, "pm_task.status", f.Status)
	q = pagination.In(q, "pm_task.status", f.Statuses)
	q = pagination.Eq(q, "pm_task.type", f.Type)
	q = pagination.EqPtr(q, "pm_task.pri", f.Pri)
	q = pagination.EqPtr(q, "pm_task.assigned_to", f.AssignedTo)
	q = pagination.Eq(q, "pm_task.opened_by", f.OpenedBy)
	q = pagination.Eq(q, "pm_task.finished_by", f.FinishedBy)
	q = pagination.EqPtr(q, "pm_task.module_id", f.ModuleID)
	q = pagination.EqPtr(q, "pm_task.parent_id", f.ParentID)
	q = pagination.TimeRange(q, "pm_task.deadline", f.DeadlineFrom, f.DeadlineTo)
	q = pagination.Keyword(q, f.Keywords, "pm_task.name", "pm_task.keywords", "pm_task.id")

	if f.Unfinished {
		q = q.Where("pm_task.status IN ?", model.TaskUnfinishedStatuses)
	}

	q = q.Where("pm_task.project_id IN ?", f.VisibleProjectIDs)
	if !f.AllProducts {
		// 不属于任何产品的任务（内部事务）始终跟着项目走；
		// 挂了产品的任务要产品也可见。
		q = q.Where("pm_task.product_id = 0 OR pm_task.product_id IN ?", f.ProductIDs)
	}
	return q
}

// ListByIDs 按 id 批量取任务。
func (r *TaskRepo) ListByIDs(ctx context.Context, ids []uint) ([]model.Task, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	var out []model.Task
	if err := r.DB.WithContext(ctx).Where("id IN ?", ids).Find(&out).Error; err != nil {
		return nil, translate(err)
	}
	return out, nil
}

// NameMap 返回 id → 任务名的映射。
func (r *TaskRepo) NameMap(ctx context.Context, ids []uint) (map[uint]string, error) {
	tasks, err := r.ListByIDs(ctx, ids)
	if err != nil {
		return nil, err
	}
	out := make(map[uint]string, len(tasks))
	for _, t := range tasks {
		out[t.ID] = t.Name
	}
	return out, nil
}

// CountByStory 统计需求下的任务数（含已完成）。
func (r *TaskRepo) CountByStory(ctx context.Context, storyID uint) (int64, error) {
	return r.Count(ctx, "story_id = ?", storyID)
}

// CountUnfinishedByStory 统计需求下尚未收尾的任务数。
//
// 需求删除前用它判断是否还有下游工作挂着：只有未完成的任务才构成阻塞，
// 已完成/已关闭的任务是历史，不该让需求永远删不掉。
func (r *TaskRepo) CountUnfinishedByStory(ctx context.Context, storyID uint) (int64, error) {
	var count int64
	err := r.DB.WithContext(ctx).Model(&model.Task{}).
		Where("story_id = ? AND status IN ?", storyID, model.TaskUnfinishedStatuses).
		Count(&count).Error
	if err != nil {
		return 0, translate(err)
	}
	return count, nil
}

// CountUnfinishedByProject 统计项目下未完成的任务数。
func (r *TaskRepo) CountUnfinishedByProject(ctx context.Context, projectID uint) (int64, error) {
	return r.countUnfinished(ctx, "project_id = ?", projectID)
}

// CountUnfinishedByExecution 统计迭代下未完成的任务数。
func (r *TaskRepo) CountUnfinishedByExecution(ctx context.Context, executionID uint) (int64, error) {
	return r.countUnfinished(ctx, "execution_id = ?", executionID)
}

func (r *TaskRepo) countUnfinished(ctx context.Context, query string, arg uint) (int64, error) {
	var count int64
	err := r.DB.WithContext(ctx).Model(&model.Task{}).
		Where(query+" AND status IN ?", arg, model.TaskUnfinishedStatuses).
		Count(&count).Error
	if err != nil {
		return 0, translate(err)
	}
	return count, nil
}

// CountByModule 统计模块下的任务数（阻止删除非空模块）。
func (r *TaskRepo) CountByModule(ctx context.Context, moduleID uint) (int64, error) {
	return r.Count(ctx, "module_id = ?", moduleID)
}

// CountChildren 统计子任务数。
func (r *TaskRepo) CountChildren(ctx context.Context, taskID uint) (int64, error) {
	return r.Count(ctx, "parent_id = ?", taskID)
}

// CountUnfinishedChildren 统计未完成的子任务数。
func (r *TaskRepo) CountUnfinishedChildren(ctx context.Context, taskID uint) (int64, error) {
	var count int64
	err := r.DB.WithContext(ctx).Model(&model.Task{}).
		Where("parent_id = ? AND status IN ?", taskID, model.TaskUnfinishedStatuses).
		Count(&count).Error
	if err != nil {
		return 0, translate(err)
	}
	return count, nil
}

// SumHours 汇总符合条件任务的工时三元组。
//
// 与 project.go 里的 sumHours 相比多了一层过滤条件：看板与迭代详情
// 需要「某一列的任务工时」这类细分口径，而不只是整块合计。
func (r *TaskRepo) SumHours(ctx context.Context, tx *gorm.DB, f TaskFilter) (Hours, error) {
	var row struct {
		Estimate float64
		Consumed float64
		Left     float64
	}
	err := applyTaskFilter(r.db(tx).WithContext(ctx).Model(&model.Task{}), f).
		Select("COALESCE(SUM(estimate), 0) AS estimate, " +
			"COALESCE(SUM(consumed), 0) AS consumed, " +
			"COALESCE(SUM(left), 0) AS left").
		Scan(&row).Error
	if err != nil {
		return Hours{}, translate(err)
	}
	return Hours{Estimate: row.Estimate, Consumed: row.Consumed, Left: row.Left}, nil
}

// StatusCounts 统计某范围内各状态的任务数，供看板列头显示「N 条」。
func (r *TaskRepo) StatusCounts(ctx context.Context, f TaskFilter) (map[string]int64, error) {
	var rows []struct {
		Status string
		Total  int64
	}
	err := applyTaskFilter(r.DB.WithContext(ctx).Model(&model.Task{}), f).
		Select("pm_task.status AS status, COUNT(*) AS total").
		Group("pm_task.status").Scan(&rows).Error
	if err != nil {
		return nil, translate(err)
	}
	out := make(map[string]int64, len(rows))
	for _, row := range rows {
		out[row.Status] = row.Total
	}
	return out, nil
}

// UpdateVersioned 带版本条件的字段更新，实现任务的乐观锁。
//
// 与需求不同，任务的版本号**每次编辑都会 +1**（禅道的 task.version
// 也是这个口径）。原因是任务没有「需求变更」那样重量级的独立动作，
// 每次保存都是对同一条工作项的实质性修改，因此每一步都该被
// 「有人在我前面改过」这道锁挡住。
func (r *TaskRepo) UpdateVersioned(ctx context.Context, tx *gorm.DB, id uint, expectVersion int, fields map[string]any) error {
	if len(fields) == 0 {
		return nil
	}
	res := r.db(tx).WithContext(ctx).Model(&model.Task{}).
		Where("id = ? AND version = ?", id, expectVersion).Updates(fields)
	if res.Error != nil {
		return translate(res.Error)
	}
	if res.RowsAffected > 0 {
		return nil
	}

	var current struct {
		Version int
	}
	err := r.db(tx).WithContext(ctx).Model(&model.Task{}).
		Select("version").Where("id = ?", id).Take(&current).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return apperr.New(apperr.TaskNotFound)
	}
	if err != nil {
		return translate(err)
	}
	if current.Version != expectVersion {
		return apperr.New(apperr.Conflict)
	}
	// 版本一致却没有影响行数：字段没变化（MySQL 不计「无变化行」）。
	return nil
}

// RecomputeHours 用一个工时三元组覆盖任务的工时字段。
//
// 多人任务里，任务上的工时是各成员分工的汇总值，每次成员报工时
// 都要把它重算一遍，所以单独有个方法而不是散在 service 里。
func (r *TaskRepo) RecomputeHours(ctx context.Context, tx *gorm.DB, id uint, h Hours) error {
	return r.UpdateFields(ctx, tx, id, map[string]any{
		"estimate": h.Estimate,
		"consumed": h.Consumed,
		"left":     h.Left,
	})
}

// HoursInTx 在事务内取任务的工时三元组。
//
// 「完成任务」要在同一个事务里先报工时、再校验剩余工时是否归零，
// 而这个校验必须读到刚写进去的值，所以需要一个走事务的取数方法。
func (r *TaskRepo) HoursInTx(ctx context.Context, tx *gorm.DB, id uint) (Hours, error) {
	var row struct {
		Estimate float64
		Consumed float64
		Left     float64
	}
	err := r.db(tx).WithContext(ctx).Model(&model.Task{}).
		Select("estimate, consumed, left").Where("id = ?", id).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return Hours{}, apperr.New(apperr.TaskNotFound)
	}
	if err != nil {
		return Hours{}, translate(err)
	}
	return Hours{Estimate: row.Estimate, Consumed: row.Consumed, Left: row.Left}, nil
}

// ---------------------------------------------------------------------------
// 多人任务的成员分工
// ---------------------------------------------------------------------------

// TaskTeamRepo 是多人任务成员分工仓储。
type TaskTeamRepo struct {
	*Repo[model.TaskTeam]
}

// NewTaskTeamRepo 构造任务成员仓储。
func NewTaskTeamRepo(db *gorm.DB) *TaskTeamRepo {
	return &TaskTeamRepo{Repo: New[model.TaskTeam](db)}
}

// ListByTask 返回任务的成员分工，按串行顺序排列。
func (r *TaskTeamRepo) ListByTask(ctx context.Context, taskID uint) ([]model.TaskTeam, error) {
	var out []model.TaskTeam
	if err := r.DB.WithContext(ctx).
		Where("task_id = ?", taskID).
		Order(orderColumn(r.DB) + " ASC, id ASC").Find(&out).Error; err != nil {
		return nil, translate(err)
	}
	return out, nil
}

// ListByTasks 批量返回多个任务的成员分工。
func (r *TaskTeamRepo) ListByTasks(ctx context.Context, taskIDs []uint) ([]model.TaskTeam, error) {
	if len(taskIDs) == 0 {
		return nil, nil
	}
	var out []model.TaskTeam
	if err := r.DB.WithContext(ctx).
		Where("task_id IN ?", taskIDs).
		Order(orderColumn(r.DB) + " ASC, id ASC").Find(&out).Error; err != nil {
		return nil, translate(err)
	}
	return out, nil
}

// FindMember 取某个成员的分工行。不存在返回 (nil, nil)。
func (r *TaskTeamRepo) FindMember(ctx context.Context, taskID, userID uint) (*model.TaskTeam, error) {
	var out model.TaskTeam
	err := r.DB.WithContext(ctx).
		Where("task_id = ? AND user_id = ?", taskID, userID).First(&out).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, translate(err)
	}
	return &out, nil
}

// Replace 用给定成员覆盖任务的分工表。
func (r *TaskTeamRepo) Replace(ctx context.Context, tx *gorm.DB, taskID uint, members []model.TaskTeam) error {
	handle := r.db(tx)
	if err := handle.WithContext(ctx).
		Where("task_id = ?", taskID).Delete(&model.TaskTeam{}).Error; err != nil {
		return translate(err)
	}
	if len(members) == 0 {
		return nil
	}

	rows := make([]model.TaskTeam, 0, len(members))
	seen := make(map[uint]struct{}, len(members))
	for i, m := range members {
		if m.UserID == 0 {
			continue
		}
		if _, dup := seen[m.UserID]; dup {
			continue
		}
		seen[m.UserID] = struct{}{}
		m.TaskID = taskID
		if m.Order == 0 {
			m.Order = i + 1
		}
		if m.Status == "" {
			m.Status = model.TaskStatusWait
		}
		// StoryVersion 记下成员开工时的需求版本，需求变更后据此提示。
		rows = append(rows, m)
	}
	if len(rows) == 0 {
		return nil
	}
	if err := handle.WithContext(ctx).Create(&rows).Error; err != nil {
		return translate(err)
	}
	return nil
}

// UpdateMember 更新成员分工的字段。
func (r *TaskTeamRepo) UpdateMember(ctx context.Context, tx *gorm.DB, taskID, userID uint, fields map[string]any) error {
	if len(fields) == 0 {
		return nil
	}
	res := r.db(tx).WithContext(ctx).Model(&model.TaskTeam{}).
		Where("task_id = ? AND user_id = ?", taskID, userID).Updates(fields)
	if res.Error != nil {
		return translate(res.Error)
	}
	if res.RowsAffected == 0 {
		var count int64
		if err := r.db(tx).WithContext(ctx).Model(&model.TaskTeam{}).
			Where("task_id = ? AND user_id = ?", taskID, userID).
			Limit(1).Count(&count).Error; err != nil {
			return translate(err)
		}
		if count == 0 {
			return apperr.Newf(apperr.TaskNotAssigned, "该成员不在任务的成员分工里")
		}
	}
	return nil
}

// DeleteByTask 删除任务的成员分工（任务被删除时调用）。
func (r *TaskTeamRepo) DeleteByTask(ctx context.Context, tx *gorm.DB, taskID uint) error {
	_, err := r.DeleteWhere(ctx, tx, "task_id = ?", taskID)
	return err
}

// SumByTask 汇总任务各成员的工时。
func (r *TaskTeamRepo) SumByTask(ctx context.Context, tx *gorm.DB, taskID uint) (Hours, error) {
	var row struct {
		Estimate float64
		Consumed float64
		Left     float64
	}
	err := r.db(tx).WithContext(ctx).Model(&model.TaskTeam{}).
		Select("COALESCE(SUM(estimate), 0) AS estimate, "+
			"COALESCE(SUM(consumed), 0) AS consumed, "+
			"COALESCE(SUM(left), 0) AS left").
		Where("task_id = ?", taskID).Scan(&row).Error
	if err != nil {
		return Hours{}, translate(err)
	}
	return Hours{Estimate: row.Estimate, Consumed: row.Consumed, Left: row.Left}, nil
}

// ---------------------------------------------------------------------------
// 工时记录
// ---------------------------------------------------------------------------

// EffortRepo 是工时记录仓储。
type EffortRepo struct {
	*Repo[model.Effort]
}

// NewEffortRepo 构造工时仓储。
func NewEffortRepo(db *gorm.DB) *EffortRepo {
	return &EffortRepo{Repo: New[model.Effort](db)}
}

// EffortFilter 是工时列表的过滤条件。
type EffortFilter struct {
	ObjectType  string
	ObjectID    uint
	ProjectID   uint
	ExecutionID uint
	UserID      uint
	// DateFrom/DateTo 按报工日期过滤（闭区间）。
	DateFrom *time.Time
	DateTo   *time.Time

	// VisibleProjectIDs 是当前用户可见的项目 id（空表示一个都看不到）。
	VisibleProjectIDs []uint

	Pagination pagination.Params
}

// List 分页查询工时。
func (r *EffortRepo) List(ctx context.Context, f EffortFilter) (*pagination.Result[model.Effort], error) {
	q := r.DB.WithContext(ctx).Model(&model.Effort{})

	q = pagination.Eq(q, "pm_effort.object_type", f.ObjectType)
	q = pagination.Eq(q, "pm_effort.object_id", f.ObjectID)
	q = pagination.Eq(q, "pm_effort.project_id", f.ProjectID)
	q = pagination.Eq(q, "pm_effort.execution_id", f.ExecutionID)
	q = pagination.Eq(q, "pm_effort.user_id", f.UserID)
	q = pagination.TimeRange(q, "pm_effort.date", f.DateFrom, f.DateTo)

	q = q.Where("pm_effort.project_id IN ?", f.VisibleProjectIDs)

	return r.Paginate(ctx, q, f.Pagination, effortSorter, "pm_effort.date DESC, pm_effort.id DESC")
}

// ListByObject 返回某对象的全部工时记录（按时间倒序）。
//
// 不分页：单个任务/需求的工时条数通常个位数，详情页要一次看全。
func (r *EffortRepo) ListByObject(ctx context.Context, objectType string, objectID uint) ([]model.Effort, error) {
	var out []model.Effort
	if err := r.DB.WithContext(ctx).
		Where("object_type = ? AND object_id = ?", objectType, objectID).
		Order("date DESC, id DESC").Find(&out).Error; err != nil {
		return nil, translate(err)
	}
	return out, nil
}

// CountByObjects 批量统计多个对象的工时记录条数，供列表一次取回。
func (r *EffortRepo) CountByObjects(ctx context.Context, objectType string, objectIDs []uint) (map[uint]int64, error) {
	if len(objectIDs) == 0 {
		return map[uint]int64{}, nil
	}
	var rows []struct {
		ObjectID uint
		Total    int64
	}
	err := r.DB.WithContext(ctx).Model(&model.Effort{}).
		Select("object_id, COUNT(*) AS total").
		Where("object_type = ? AND object_id IN ?", objectType, objectIDs).
		Group("object_id").Scan(&rows).Error
	if err != nil {
		return nil, translate(err)
	}
	out := make(map[uint]int64, len(rows))
	for _, row := range rows {
		out[row.ObjectID] = row.Total
	}
	return out, nil
}

// SumConsumed 汇总某个用户在时间区间内消耗的工时（nil 表示不限）。
func (r *EffortRepo) SumConsumed(ctx context.Context, userID uint, from, to *time.Time) (float64, error) {
	q := r.DB.WithContext(ctx).Model(&model.Effort{}).
		Where("pm_effort.user_id = ?", userID)
	q = pagination.TimeRange(q, "pm_effort.date", from, to)

	var total float64
	if err := q.Select("COALESCE(SUM(consumed), 0)").Scan(&total).Error; err != nil {
		return 0, translate(err)
	}
	return total, nil
}

// ListByObjectIDs 取一批对象上的全部工时记录。
//
// 燃尽图要逐条读「当时剩余多少」——那是它唯一的数据来源，
// 先汇总成总和就再也回溯不出曲线了。
func (r *EffortRepo) ListByObjectIDs(ctx context.Context, objectType string, ids []uint) ([]model.Effort, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	var out []model.Effort
	if err := r.DB.WithContext(ctx).
		Where("object_type = ? AND object_id IN ?", objectType, ids).
		Order("date ASC, id ASC").Find(&out).Error; err != nil {
		return nil, translate(err)
	}
	return out, nil
}

// SumByObject 汇总某对象全部工时记录里「本次消耗」之和。
func (r *EffortRepo) SumByObject(ctx context.Context, tx *gorm.DB, objectType string, objectID uint) (float64, error) {
	var total float64
	err := r.db(tx).WithContext(ctx).Model(&model.Effort{}).
		Select("COALESCE(SUM(consumed), 0)").
		Where("object_type = ? AND object_id = ?", objectType, objectID).
		Scan(&total).Error
	if err != nil {
		return 0, translate(err)
	}
	return total, nil
}

// LastLeft 返回某对象最后一条工时记录里的「剩余工时」。
//
// 剩余工时的口径是「以最后一条记录为准」而不是求和：每条记录存的
// 都是「记完之后还剩多少」，所以最新那条就是当前值。
func (r *EffortRepo) LastLeft(ctx context.Context, tx *gorm.DB, objectType string, objectID uint) (float64, bool, error) {
	var row struct {
		Left float64
	}
	err := r.db(tx).WithContext(ctx).Model(&model.Effort{}).
		Select("left").Where("object_type = ? AND object_id = ?", objectType, objectID).
		Order("date DESC, id DESC").Limit(1).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, translate(err)
	}
	return row.Left, true, nil
}

// DeleteByObject 删除某对象的全部工时（对象被删除时调用）。
func (r *EffortRepo) DeleteByObject(ctx context.Context, tx *gorm.DB, objectType string, objectID uint) error {
	_, err := r.DeleteWhere(ctx, tx, "object_type = ? AND object_id = ?", objectType, objectID)
	return err
}
