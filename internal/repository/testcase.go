package repository

import (
	"context"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/xywf221/xuliu/internal/model"
	"github.com/xywf221/xuliu/internal/pkg/pagination"
)

// 测试用例域仓储。
//
// 用例只挂在产品上（可再挂模块与溯源需求），不像缺陷/任务那样可能落进项目，
// 所以这里的可见性只有一层：产品可见才可见用例。将来测试单把用例带进项目时，
// 也仍按用例自己的产品判 —— 否则会出现「项目里能看到用例，产品里看不到」
// 这种按入口不同而结果不同的可见性，最难排查。

// testCaseSorter 是可排序列白名单。
//
// 白名单是安全边界而不是装饰：order 参数来自查询串，直接拼进 ORDER BY
// 就是注入点；只允许映射到固定列才安全。
var testCaseSorter = pagination.Sorter{
	"id":       "pm_testcase.id",
	"title":    "pm_testcase.title",
	"pri":      "pm_testcase.pri",
	"stage":    "pm_testcase.stage",
	"status":   "pm_testcase.status",
	"type":     "pm_testcase.type",
	"version":  "pm_testcase.version",
	"opened":   "pm_testcase.opened_date",
	"reviewed": "pm_testcase.reviewed_date",
	"product":  "pm_testcase.product_id",
	"order":    "pm_testcase.order",
}

// TestCaseRepo 是测试用例仓储。
type TestCaseRepo struct {
	*Repo[model.TestCase]
}

// NewTestCaseRepo 构造用例仓储。
func NewTestCaseRepo(db *gorm.DB) *TestCaseRepo {
	return &TestCaseRepo{Repo: New[model.TestCase](db)}
}

// TestCaseFilter 是用例列表的过滤条件。
type TestCaseFilter struct {
	ProductID uint
	// ModuleID 为 nil 表示不过滤；传 0 表示「未分类」。
	ModuleID *uint
	StoryID  uint

	Stage  string
	Status string
	Type   string
	Pri    *int

	OpenedBy   uint
	ReviewedBy uint

	Keywords string

	// ProductIDs 是当前用户可见的产品集合，由 Gate 提供。
	// 为空切片表示一个都看不到（GORM 会生成 IN (NULL)）。
	ProductIDs []uint

	Pagination pagination.Params
}

// List 分页查询用例。
func (r *TestCaseRepo) List(ctx context.Context, f TestCaseFilter) (*pagination.Result[model.TestCase], error) {
	q := applyTestCaseFilter(r.DB.WithContext(ctx).Model(&model.TestCase{}), f)
	return r.Paginate(ctx, q, f.Pagination, testCaseSorter, "pm_testcase.id DESC")
}

// applyTestCaseFilter 把过滤条件应用到查询上。
//
// 抽成独立函数是为了让「列表」和以后要加的「按阶段统计」共用同一份条件，
// 免得给列表加了过滤却忘了给统计加（缺陷那边同样的问题曾出现过）。
func applyTestCaseFilter(q *gorm.DB, f TestCaseFilter) *gorm.DB {
	q = pagination.Eq(q, "pm_testcase.product_id", f.ProductID)
	q = pagination.EqPtr(q, "pm_testcase.module_id", f.ModuleID)
	q = pagination.Eq(q, "pm_testcase.story_id", f.StoryID)

	q = pagination.Eq(q, "pm_testcase.stage", f.Stage)
	q = pagination.Eq(q, "pm_testcase.status", f.Status)
	q = pagination.Eq(q, "pm_testcase.type", f.Type)
	q = pagination.EqPtr(q, "pm_testcase.pri", f.Pri)

	q = pagination.Eq(q, "pm_testcase.opened_by", f.OpenedBy)
	q = pagination.Eq(q, "pm_testcase.reviewed_by", f.ReviewedBy)
	q = pagination.Keyword(q, f.Keywords,
		"pm_testcase.title", "pm_testcase.keywords", "pm_testcase.id")

	q = q.Where("pm_testcase.product_id IN ?", f.ProductIDs)
	return q
}

// ListByIDs 按 id 批量取用例。
func (r *TestCaseRepo) ListByIDs(ctx context.Context, ids []uint) ([]model.TestCase, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	var out []model.TestCase
	err := r.DB.WithContext(ctx).Where("id IN ?", ids).Find(&out).Error
	return out, translate(err)
}

// TitleMap 批量取用例标题，供其它列表补全展示字段。
func (r *TestCaseRepo) TitleMap(ctx context.Context, ids []uint) (map[uint]string, error) {
	cases, err := r.ListByIDs(ctx, ids)
	if err != nil {
		return nil, err
	}
	out := make(map[uint]string, len(cases))
	for i := range cases {
		out[cases[i].ID] = cases[i].Title
	}
	return out, nil
}

// CountByProduct 统计某产品下的用例数，用于删除产品前的拦截。
func (r *TestCaseRepo) CountByProduct(ctx context.Context, productID uint) (int64, error) {
	var count int64
	err := r.DB.WithContext(ctx).Model(&model.TestCase{}).
		Where("product_id = ?", productID).Count(&count).Error
	return count, translate(err)
}

// CountByStory 统计某需求被多少用例覆盖，供需求详情回答「这条需求测了吗」。
func (r *TestCaseRepo) CountByStory(ctx context.Context, storyID uint) (int64, error) {
	var count int64
	err := r.DB.WithContext(ctx).Model(&model.TestCase{}).
		Where("story_id = ?", storyID).Count(&count).Error
	return count, translate(err)
}

// CountByModule 统计某模块下的用例数，用于删除模块前的拦截。
func (r *TestCaseRepo) CountByModule(ctx context.Context, moduleID uint) (int64, error) {
	var count int64
	err := r.DB.WithContext(ctx).Model(&model.TestCase{}).
		Where("module_id = ?", moduleID).Count(&count).Error
	return count, translate(err)
}

// ---------------------------------------------------------------------------
// 用例步骤
// ---------------------------------------------------------------------------

// TestCaseStepRepo 是用例步骤仓储。
type TestCaseStepRepo struct {
	*Repo[model.TestCaseStep]
}

// NewTestCaseStepRepo 构造步骤仓储。
func NewTestCaseStepRepo(db *gorm.DB) *TestCaseStepRepo {
	return &TestCaseStepRepo{Repo: New[model.TestCaseStep](db)}
}

// ListByCase 取某条用例的全部步骤，按 order 升序。
//
// 这里必须用 clause.Column 而不是写一段字符串：order 是 SQL 的保留字，
// 拼成 `ORDER BY pm_testcasestep.order` 在 SQLite 上直接是语法错误
// （实测报 near "order": syntax error），而各库的引号规则不一样
// （MySQL 用反引号、PG 用双引号），手写哪一种都会在另一种库上崩。
// 交给 GORM 按方言加引号才是三库通用的写法。
//
// 另一个坑：Order 只认 clause.OrderBy / clause.OrderByColumn / string 三种形态，
// 传 []clause.OrderByColumn 切片会被 switch 静默忽略 —— 排序不报错，只是没了。
// 所以这里必须套一层 clause.OrderBy{Columns: …}。
func (r *TestCaseStepRepo) ListByCase(ctx context.Context, caseID uint) ([]model.TestCaseStep, error) {
	var out []model.TestCaseStep
	err := r.DB.WithContext(ctx).
		Where("case_id = ?", caseID).
		Order(clause.OrderBy{Columns: []clause.OrderByColumn{
			{Column: clause.Column{Table: "pm_testcasestep", Name: "order"}},
			{Column: clause.Column{Table: "pm_testcasestep", Name: "id"}},
		}}).
		Find(&out).Error
	return out, translate(err)
}

// ListByCases 批量取多条用例的步骤，按用例分组返回。
//
// 一次查完而不是每条用例查一次：列表页 20 行如果逐行查步骤，
// 就变成 21 次查询，而步骤是列表上「StepCount」这一列的数据来源。
func (r *TestCaseStepRepo) ListByCases(ctx context.Context, caseIDs []uint) (map[uint][]model.TestCaseStep, error) {
	out := make(map[uint][]model.TestCaseStep, len(caseIDs))
	if len(caseIDs) == 0 {
		return out, nil
	}
	var steps []model.TestCaseStep
	err := r.DB.WithContext(ctx).
		Where("case_id IN ?", caseIDs).
		Order(clause.OrderBy{Columns: []clause.OrderByColumn{
			{Column: clause.Column{Table: "pm_testcasestep", Name: "case_id"}},
			{Column: clause.Column{Table: "pm_testcasestep", Name: "order"}},
			{Column: clause.Column{Table: "pm_testcasestep", Name: "id"}},
		}}).
		Find(&steps).Error
	if err != nil {
		return nil, translate(err)
	}
	for i := range steps {
		s := steps[i]
		out[s.CaseID] = append(out[s.CaseID], s)
	}
	return out, nil
}

// Replace 用给定步骤整体覆盖某条用例的步骤集合。
//
// 覆盖而不是 diff：步骤顺序本身是语义（第 3 步依赖第 2 步的结果），
// 增量更新要额外表达「插到第几位」，接口反而更难用对。
func (r *TestCaseStepRepo) Replace(ctx context.Context, tx *gorm.DB, caseID uint, steps []model.TestCaseStep) error {
	handle := r.db(tx)
	if err := handle.WithContext(ctx).
		Where("case_id = ?", caseID).Delete(&model.TestCaseStep{}).Error; err != nil {
		return translate(err)
	}
	if len(steps) == 0 {
		return nil
	}
	// 统一挂到这条用例上，调用方不必逐条填 CaseID。
	for i := range steps {
		steps[i].CaseID = caseID
	}
	return translate(handle.WithContext(ctx).Create(&steps).Error)
}

// DeleteByCase 删除某条用例的全部步骤。
//
// 用例删除时调用：留着孤儿子步骤不只是脏数据，还会让「按用例 id 统计步骤数」
// 这类查询把已删用例算进去。
func (r *TestCaseStepRepo) DeleteByCase(ctx context.Context, tx *gorm.DB, caseID uint) error {
	return translate(r.db(tx).WithContext(ctx).
		Where("case_id = ?", caseID).Delete(&model.TestCaseStep{}).Error)
}
