package repository

import (
	"context"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/xywf221/xuliu/internal/model"
	"github.com/xywf221/xuliu/internal/pkg/pagination"
)

// 测试单与执行记录仓储。
//
// 三张表的关系：pm_testtask 1─N pm_testtaskcase N─1 pm_testcase，
// pm_testrun 记录每次执行（同一用例在同一单里可以有多条）。
// 「单里每条用例的最新状态」冗余在 pm_testtaskcase 上，历史全在 pm_testrun。

var testTaskSorter = pagination.Sorter{
	"id":        "pm_testtask.id",
	"name":      "pm_testtask.name",
	"status":    "pm_testtask.status",
	"result":    "pm_testtask.result",
	"pri":       "pm_testtask.pri",
	"product":   "pm_testtask.product_id",
	"project":   "pm_testtask.project_id",
	"assigned":  "pm_testtask.assigned_to",
	"opened":    "pm_testtask.opened_date",
	"begin":     "pm_testtask.begin",
	"ended":     "pm_testtask.end",
	"lastrun":   "pm_testtask.last_run_date",
	"casecount": "pm_testtask.case_count",
	"order":     "pm_testtask.order",
}

// TestTaskRepo 是测试单仓储。
type TestTaskRepo struct {
	*Repo[model.TestTask]
}

// NewTestTaskRepo 构造测试单仓储。
func NewTestTaskRepo(db *gorm.DB) *TestTaskRepo {
	return &TestTaskRepo{Repo: New[model.TestTask](db)}
}

// TestTaskFilter 是测试单列表的过滤条件。
type TestTaskFilter struct {
	ProductID   uint
	ProjectID   uint
	ExecutionID uint
	BuildID     uint

	Status string
	Result string
	Pri    *int

	AssignedTo uint
	OpenedBy   uint
	// Unfinished 只看没结单的（wait/doing/blocked）。
	Unfinished bool
	// NoCase 只看还没纳入任何用例的单（提醒「建了单还没选用例」）。
	NoCase bool

	Keywords string

	// 可见性：与缺陷同一口径 —— 挂了项目就按项目判，只挂产品的按产品判。
	ProductIDs []uint
	ProjectIDs []uint
	Pagination pagination.Params
}

// List 分页查询测试单。
func (r *TestTaskRepo) List(ctx context.Context, f TestTaskFilter) (*pagination.Result[model.TestTask], error) {
	q := applyTestTaskFilter(r.DB.WithContext(ctx).Model(&model.TestTask{}), f)
	return r.Paginate(ctx, q, f.Pagination, testTaskSorter, "pm_testtask.id DESC")
}

// applyTestTaskFilter 把过滤条件应用到查询上。
//
// 单独成函数是为了让列表与「按状态统计」共用一份条件。
func applyTestTaskFilter(q *gorm.DB, f TestTaskFilter) *gorm.DB {
	q = pagination.Eq(q, "pm_testtask.product_id", f.ProductID)
	q = pagination.Eq(q, "pm_testtask.project_id", f.ProjectID)
	q = pagination.Eq(q, "pm_testtask.execution_id", f.ExecutionID)
	q = pagination.Eq(q, "pm_testtask.build_id", f.BuildID)

	q = pagination.Eq(q, "pm_testtask.status", f.Status)
	q = pagination.Eq(q, "pm_testtask.result", f.Result)
	q = pagination.EqPtr(q, "pm_testtask.pri", f.Pri)
	q = pagination.Eq(q, "pm_testtask.assigned_to", f.AssignedTo)
	q = pagination.Eq(q, "pm_testtask.opened_by", f.OpenedBy)
	q = pagination.Keyword(q, f.Keywords,
		"pm_testtask.name", "pm_testtask.desc", "pm_testtask.id")

	if f.Unfinished {
		q = q.Where("pm_testtask.status <> ?", model.TestTaskStatusClosed)
	}
	if f.NoCase {
		q = q.Where("pm_testtask.case_count = 0")
	}

	// 「或」而不是「与」：理由同缺陷那边 —— 挂在可见项目里的测试单，
	// 不该因为它的产品不在可见清单里就从列表消失。
	q = q.Where(
		"(pm_testtask.project_id > 0 AND pm_testtask.project_id IN ?) "+
			"OR (pm_testtask.project_id = 0 AND pm_testtask.product_id IN ?)",
		f.ProjectIDs, f.ProductIDs)
	return q
}

// CountByStatus 按状态统计测试单数量，供列表页顶部的标签计数。
func (r *TestTaskRepo) CountByStatus(ctx context.Context, f TestTaskFilter) (map[string]int64, error) {
	// 统计不能被 status 自身影响，否则每个标签都只显示自己那一类。
	f.Status = ""
	var rows []struct {
		Status string
		Total  int64
	}
	err := applyTestTaskFilter(r.DB.WithContext(ctx).Model(&model.TestTask{}), f).
		Select("pm_testtask.status, COUNT(*) AS total").
		Group("pm_testtask.status").Scan(&rows).Error
	if err != nil {
		return nil, translate(err)
	}
	out := make(map[string]int64, len(rows))
	for _, row := range rows {
		out[row.Status] = row.Total
	}
	return out, nil
}

// AddCaseCount 增减测试单上的用例数快照。
//
// 用 SQL 表达式而不是「读出来加一再写回」：纳入用例是并发动作，
// 读改写会把中间的更新覆盖掉。
func (r *TestTaskRepo) AddCaseCount(ctx context.Context, tx *gorm.DB, taskID uint, delta int) error {
	return translate(r.db(tx).WithContext(ctx).Model(&model.TestTask{}).
		Where("id = ?", taskID).
		UpdateColumn("case_count", gorm.Expr("case_count + ?", delta)).Error)
}

// TouchLastRun 把测试单的「最后执行时间」推到指定时刻。
func (r *TestTaskRepo) TouchLastRun(ctx context.Context, tx *gorm.DB, taskID uint, at any) error {
	return translate(r.db(tx).WithContext(ctx).Model(&model.TestTask{}).
		Where("id = ?", taskID).UpdateColumn("last_run_date", at).Error)
}

// ---------------------------------------------------------------------------
// 测试单与用例的关联
// ---------------------------------------------------------------------------

var testTaskCaseSorter = pagination.Sorter{
	"id":      "pm_testtaskcase.id",
	"case":    "pm_testtaskcase.case_id",
	"result":  "pm_testtaskcase.last_result",
	"lastrun": "pm_testtaskcase.last_run_date",
	"runs":    "pm_testtaskcase.run_count",
	"order":   "pm_testtaskcase.order",
}

// TestTaskCaseRepo 是「单内用例」仓储。
type TestTaskCaseRepo struct {
	*Repo[model.TestTaskCase]
}

// NewTestTaskCaseRepo 构造关联仓储。
func NewTestTaskCaseRepo(db *gorm.DB) *TestTaskCaseRepo {
	return &TestTaskCaseRepo{Repo: New[model.TestTaskCase](db)}
}

// TestTaskCaseFilter 是单内用例列表的过滤条件。
type TestTaskCaseFilter struct {
	TaskID  uint
	Product uint
	StoryID uint
	// LastResult 按最新执行结论过滤；传 "none" 表示还没跑过。
	LastResult string
	// AssignedTo 按单内执行人过滤。
	AssignedTo *uint

	ProductIDs []uint

	Pagination pagination.Params
}

// List 分页查询某单内的用例。
func (r *TestTaskCaseRepo) List(ctx context.Context, f TestTaskCaseFilter) (*pagination.Result[model.TestTaskCase], error) {
	q := r.DB.WithContext(ctx).Model(&model.TestTaskCase{})
	q = pagination.Eq(q, "pm_testtaskcase.task_id", f.TaskID)
	q = pagination.Eq(q, "pm_testtaskcase.product_id", f.Product)
	q = pagination.Eq(q, "pm_testtaskcase.story_id", f.StoryID)
	q = pagination.EqPtr(q, "pm_testtaskcase.assigned_to", f.AssignedTo)

	switch f.LastResult {
	case "":
		// 不过滤
	case "none":
		q = q.Where("pm_testtaskcase.last_result = ''")
	default:
		q = pagination.Eq(q, "pm_testtaskcase.last_result", f.LastResult)
	}

	// 用例的可见性跟着它的产品走：单本身可见，不代表单里所有用例都可见
	// （跨产品的单不该带出别人产品的用例内容）。
	q = q.Where("pm_testtaskcase.product_id IN ?", f.ProductIDs)

	// 默认按纳入顺序（order）排：测试是按顺序跑的，
	// 而 order 是保留字，必须走 clause 让 GORM 加引号。
	return r.Paginate(ctx, q, f.Pagination, testTaskCaseSorter,
		"pm_testtaskcase.order ASC")
}

// ListByTask 取某单的全部用例（不分页，用于统计与快照校验）。
func (r *TestTaskCaseRepo) ListByTask(ctx context.Context, taskID uint) ([]model.TestTaskCase, error) {
	var out []model.TestTaskCase
	err := r.DB.WithContext(ctx).
		Where("task_id = ?", taskID).
		Order(clause.OrderBy{Columns: []clause.OrderByColumn{
			{Column: clause.Column{Table: "pm_testtaskcase", Name: "order"}},
			{Column: clause.Column{Table: "pm_testtaskcase", Name: "id"}},
		}}).
		Find(&out).Error
	return out, translate(err)
}

// Find 定位单内的一条用例关联。不存在返回 (nil, nil)。
func (r *TestTaskCaseRepo) Find(ctx context.Context, taskID, caseID uint) (*model.TestTaskCase, error) {
	var out model.TestTaskCase
	err := r.DB.WithContext(ctx).
		Where("task_id = ? AND case_id = ?", taskID, caseID).First(&out).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, translate(err)
	}
	return &out, nil
}

// Delete 从单里移出某条用例（只删关联，用例本身不动）。
func (r *TestTaskCaseRepo) Delete(ctx context.Context, tx *gorm.DB, taskID, caseID uint) error {
	return translate(r.db(tx).WithContext(ctx).
		Where("task_id = ? AND case_id = ?", taskID, caseID).
		Delete(&model.TestTaskCase{}).Error)
}

// DeleteByTask 删除某单的全部关联，用于删单时不留孤儿。
func (r *TestTaskCaseRepo) DeleteByTask(ctx context.Context, tx *gorm.DB, taskID uint) error {
	return translate(r.db(tx).WithContext(ctx).
		Where("task_id = ?", taskID).Delete(&model.TestTaskCase{}).Error)
}

// UpdateAfterRun 记录一次执行后刷新单内用例的最新状态。
func (r *TestTaskCaseRepo) UpdateAfterRun(ctx context.Context, tx *gorm.DB, taskID, caseID uint, result string, at any) error {
	return translate(r.db(tx).WithContext(ctx).Model(&model.TestTaskCase{}).
		Where("task_id = ? AND case_id = ?", taskID, caseID).
		Updates(map[string]any{
			"last_result":   result,
			"last_run_date": at,
			"run_count":     gorm.Expr("run_count + 1"),
		}).Error)
}

// CountByCase 统计某用例被多少测试单引用，用于删用例前的拦截。
func (r *TestTaskCaseRepo) CountByCase(ctx context.Context, caseID uint) (int64, error) {
	var count int64
	err := r.DB.WithContext(ctx).Model(&model.TestTaskCase{}).
		Where("case_id = ?", caseID).Count(&count).Error
	return count, translate(err)
}

// ResultSummary 汇总某单的逐结论用例数（passed/failed/blocked/unrun）。
type ResultSummary struct {
	Pass    int64
	Fail    int64
	Blocked int64
	Unrun   int64
}

// Summary 汇总单内用例的执行情况，供详情头部与结单前的提示。
func (r *TestTaskCaseRepo) Summary(ctx context.Context, taskID uint) (ResultSummary, error) {
	var rows []struct {
		LastResult string
		Total      int64
	}
	err := r.DB.WithContext(ctx).Model(&model.TestTaskCase{}).
		Where("task_id = ?", taskID).
		Select("pm_testtaskcase.last_result, COUNT(*) AS total").
		Group("pm_testtaskcase.last_result").Scan(&rows).Error
	if err != nil {
		return ResultSummary{}, translate(err)
	}
	var out ResultSummary
	for _, row := range rows {
		switch row.LastResult {
		case model.TestRunResultPass:
			out.Pass += row.Total
		case model.TestRunResultFail:
			out.Fail += row.Total
		case model.TestRunResultBlocked:
			out.Blocked += row.Total
		default:
			out.Unrun += row.Total
		}
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// 执行记录
// ---------------------------------------------------------------------------

var testRunSorter = pagination.Sorter{
	"id":      "pm_testrun.id",
	"case":    "pm_testrun.case_id",
	"result":  "pm_testrun.result",
	"date":    "pm_testrun.executed_date",
	"run":     "pm_testrun.id",
	"version": "pm_testrun.case_version",
}

// TestRunRepo 是执行记录仓储。
type TestRunRepo struct {
	*Repo[model.TestRun]
}

// NewTestRunRepo 构造执行记录仓储。
func NewTestRunRepo(db *gorm.DB) *TestRunRepo {
	return &TestRunRepo{Repo: New[model.TestRun](db)}
}

// TestRunFilter 是执行记录的过滤条件。
type TestRunFilter struct {
	TaskID uint
	CaseID uint
	Result string
	// 只看某次执行：RunID 用于「查这条记录的完整步骤快照」。
	RunID      uint
	ProductIDs []uint
	Pagination pagination.Params
}

// List 分页查询执行记录（默认时间倒序）。
func (r *TestRunRepo) List(ctx context.Context, f TestRunFilter) (*pagination.Result[model.TestRun], error) {
	q := r.DB.WithContext(ctx).Model(&model.TestRun{})
	q = pagination.Eq(q, "pm_testrun.task_id", f.TaskID)
	q = pagination.Eq(q, "pm_testrun.case_id", f.CaseID)
	q = pagination.Eq(q, "pm_testrun.result", f.Result)
	if f.RunID > 0 {
		q = q.Where("pm_testrun.id = ?", f.RunID)
	}
	q = q.Where("pm_testrun.product_id IN ?", f.ProductIDs)
	return r.Paginate(ctx, q, f.Pagination, testRunSorter, "pm_testrun.id DESC")
}

// ListByCase 取某用例在某单内的全部执行历史（时间倒序）。
func (r *TestRunRepo) ListByCase(ctx context.Context, taskID, caseID uint) ([]model.TestRun, error) {
	var out []model.TestRun
	err := r.DB.WithContext(ctx).
		Where("task_id = ? AND case_id = ?", taskID, caseID).
		Order("pm_testrun.id DESC").Limit(200).Find(&out).Error
	return out, translate(err)
}

// CountByTask 统计某单的执行次数，用于「有记录的单不能删」。
func (r *TestRunRepo) CountByTask(ctx context.Context, taskID uint) (int64, error) {
	var count int64
	err := r.DB.WithContext(ctx).Model(&model.TestRun{}).
		Where("task_id = ?", taskID).Count(&count).Error
	return count, translate(err)
}
