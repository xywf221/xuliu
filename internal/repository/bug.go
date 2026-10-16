package repository

import (
	"context"
	"time"

	"gorm.io/gorm"

	"github.com/xywf221/xuliu/internal/model"
	"github.com/xywf221/xuliu/internal/pkg/pagination"
)

// 缺陷域仓储。

var bugSorter = pagination.Sorter{
	"id":       "pm_bug.id",
	"title":    "pm_bug.title",
	"status":   "pm_bug.status",
	"type":     "pm_bug.type",
	"severity": "pm_bug.severity",
	"pri":      "pm_bug.pri",
	"opened":   "pm_bug.opened_date",
	"resolved": "pm_bug.resolved_date",
	"closed":   "pm_bug.closed_date",
	"deadline": "pm_bug.deadline",
	"order":    "pm_bug.order",
}

// BugRepo 是缺陷仓储。
type BugRepo struct {
	*Repo[model.Bug]
}

// NewBugRepo 构造缺陷仓储。
func NewBugRepo(db *gorm.DB) *BugRepo {
	return &BugRepo{Repo: New[model.Bug](db)}
}

// BugFilter 是缺陷列表的过滤条件。
type BugFilter struct {
	ProductID   uint
	ProjectID   uint
	ExecutionID uint
	// ModuleID 为 nil 表示不过滤；传 0 表示「未分类」。
	ModuleID *uint
	PlanID   uint

	StoryID uint
	TaskID  uint

	Status string
	// Resolution 按解决方案过滤；空串表示不过滤。
	Resolution string
	// Unresolved 只看还没给出解决方案的（解决方式为空的）。
	Unresolved bool
	Type       string
	Severity   *int
	Pri        *int
	// Confirmed 为 nil 表示不过滤；0 未确认，1 已确认。
	Confirmed *int

	AssignedTo *uint
	OpenedBy   uint
	ResolvedBy uint

	Keywords string
	// DeadlineFrom/To 按截止日期过滤。
	DeadlineFrom *time.Time
	DeadlineTo   *time.Time

	// ProductIDs / ProjectIDs 是当前用户可见的产品与项目 id。
	//
	// **空切片表示「一个都看不到」**，调用方必须无条件应用。
	// 缺陷的可见性有两个来源：产品（提给谁）与项目（在哪个项目的活里发现的）。
	// 只按产品过滤会让「看得到产品、看不到私有项目」的人看到项目内的缺陷，
	// 而按 Gate 的口径（最贴近的那一层说了算）那条缺陷是看不到的。
	ProductIDs []uint
	ProjectIDs []uint

	Pagination pagination.Params
}

// List 分页查询缺陷。
func (r *BugRepo) List(ctx context.Context, f BugFilter) (*pagination.Result[model.Bug], error) {
	q := applyBugFilter(r.DB.WithContext(ctx).Model(&model.Bug{}), f)
	return r.Paginate(ctx, q, f.Pagination, bugSorter, "pm_bug.id DESC")
}

// applyBugFilter 把过滤条件应用到查询上。
//
// 与任务那边同一个理由：列表、分组统计、汇总共用一份条件，
// 免得某天给列表加了过滤却忘了给统计加。
func applyBugFilter(q *gorm.DB, f BugFilter) *gorm.DB {
	q = pagination.Eq(q, "pm_bug.product_id", f.ProductID)
	q = pagination.Eq(q, "pm_bug.project_id", f.ProjectID)
	q = pagination.Eq(q, "pm_bug.execution_id", f.ExecutionID)
	q = pagination.EqPtr(q, "pm_bug.module_id", f.ModuleID)
	q = pagination.Eq(q, "pm_bug.plan_id", f.PlanID)
	q = pagination.Eq(q, "pm_bug.story_id", f.StoryID)
	q = pagination.Eq(q, "pm_bug.task_id", f.TaskID)

	q = pagination.Eq(q, "pm_bug.status", f.Status)
	q = pagination.Eq(q, "pm_bug.resolution", f.Resolution)
	q = pagination.Eq(q, "pm_bug.type", f.Type)
	q = pagination.EqPtr(q, "pm_bug.severity", f.Severity)
	q = pagination.EqPtr(q, "pm_bug.pri", f.Pri)
	q = pagination.EqPtr(q, "pm_bug.confirmed", f.Confirmed)

	q = pagination.EqPtr(q, "pm_bug.assigned_to", f.AssignedTo)
	q = pagination.Eq(q, "pm_bug.opened_by", f.OpenedBy)
	q = pagination.Eq(q, "pm_bug.resolved_by", f.ResolvedBy)
	q = pagination.TimeRange(q, "pm_bug.deadline", f.DeadlineFrom, f.DeadlineTo)
	q = pagination.Keyword(q, f.Keywords, "pm_bug.title", "pm_bug.keywords", "pm_bug.id")

	if f.Unresolved {
		q = q.Where("pm_bug.resolution = ''")
	}

	// 可见性与 Gate 同一口径：缺陷挂着项目就按项目判，只挂产品的按产品判。
	//
	// 关键是这里用「或」而不是「与」：一条落在可见项目里的缺陷，不该因为
	// 它的产品对当前用户不可见就从列表里消失 —— 详情页打得开、列表里却
	// 找不到，是最难排查的一类问题（用户会以为是数据丢了）。
	// 两个切片为空时 GORM 生成 `IN (NULL)`，即一个都匹配不上，
	// 「空切片 = 一个都看不到」的语义在这里自然成立。
	q = q.Where(
		"(pm_bug.project_id > 0 AND pm_bug.project_id IN ?) "+
			"OR (pm_bug.project_id = 0 AND pm_bug.product_id IN ?)",
		f.ProjectIDs, f.ProductIDs)
	return q
}

// ListByIDs 按 id 批量取缺陷。
func (r *BugRepo) ListByIDs(ctx context.Context, ids []uint) ([]model.Bug, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	var out []model.Bug
	if err := r.DB.WithContext(ctx).Where("id IN ?", ids).Find(&out).Error; err != nil {
		return nil, translate(err)
	}
	return out, nil
}

// TitleMap 返回 id → 缺陷标题的映射。
func (r *BugRepo) TitleMap(ctx context.Context, ids []uint) (map[uint]string, error) {
	bugs, err := r.ListByIDs(ctx, ids)
	if err != nil {
		return nil, err
	}
	out := make(map[uint]string, len(bugs))
	for _, b := range bugs {
		out[b.ID] = b.Title
	}
	return out, nil
}

// StatusCounts 按状态分组统计，供列表页顶部的「激活 12 / 已解决 3」标签用。
func (r *BugRepo) StatusCounts(ctx context.Context, f BugFilter) (map[string]int64, error) {
	var rows []struct {
		Status string
		Total  int64
	}
	err := applyBugFilter(r.DB.WithContext(ctx).Model(&model.Bug{}), f).
		Select("pm_bug.status AS status, COUNT(*) AS total").
		Group("pm_bug.status").Scan(&rows).Error
	if err != nil {
		return nil, translate(err)
	}
	out := make(map[string]int64, len(rows))
	for _, row := range rows {
		out[row.Status] = row.Total
	}
	return out, nil
}

// CountConvertedTo 统计「由该需求转入」的缺陷数。
//
// 用于阻止删除这类需求：缺陷还写着「已解决 / 转为需求」并指向它，
// 删掉需求就等于抹掉了那条缺陷的处理结果。
func (r *BugRepo) CountConvertedTo(ctx context.Context, storyID uint) (int64, error) {
	return r.Count(ctx, "to_story_id = ?", storyID)
}

// CountByModule 统计模块下的缺陷数（阻止删除非空模块）。
func (r *BugRepo) CountByModule(ctx context.Context, moduleID uint) (int64, error) {
	return r.Count(ctx, "module_id = ?", moduleID)
}
