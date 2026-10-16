package repository

import (
	"context"

	"gorm.io/gorm"

	"github.com/xywf221/xuliu/internal/model"
	"github.com/xywf221/xuliu/internal/pkg/apperr"
	"github.com/xywf221/xuliu/internal/pkg/pagination"
)

// 产品域仓储：产品线、产品、模块树、产品计划。
//
// 本文件里的 SQL 有两条不可动摇的约束：
//
//  1. **列名一律带表名前缀**（pm_product.name 而非 name）。产品域的查询
//     常带 join，裸列名在两张表都有同名列时会歧义报错。
//  2. **排序字段只能来自下面的 Sorter 白名单**。ORDER BY 无法参数化，
//     白名单是唯一的防线。

var productlineSorter = pagination.Sorter{
	"id":    "pm_productline.id",
	"name":  "pm_productline.name",
	"grade": "pm_productline.grade",
	"order": "pm_productline.order",
}

var productSorter = pagination.Sorter{
	"id":    "pm_product.id",
	"name":  "pm_product.name",
	"code":  "pm_product.code",
	"type":  "pm_product.type",
	"order": "pm_product.order",
}

var moduleSorter = pagination.Sorter{
	"id":    "pm_module.id",
	"name":  "pm_module.name",
	"grade": "pm_module.grade",
	"order": "pm_module.order",
}

var planSorter = pagination.Sorter{
	"id":    "pm_productplan.id",
	"title": "pm_productplan.title",
	"begin": "pm_productplan.begin",
	"end":   "pm_productplan.end",
	"order": "pm_productplan.order",
}

// ---------------------------------------------------------------------------
// 产品线
// ---------------------------------------------------------------------------

// ProductlineRepo 是产品线仓储。
type ProductlineRepo struct {
	*Repo[model.Productline]
}

// NewProductlineRepo 构造产品线仓储。
func NewProductlineRepo(db *gorm.DB) *ProductlineRepo {
	return &ProductlineRepo{Repo: New[model.Productline](db)}
}

// Tree 返回全部产品线，按层级与排序字段排列。
//
// 产品线数量在几十条量级，一次全取回内存比递归查询更简单，
// 且不依赖任何库的递归 CTE。
func (r *ProductlineRepo) Tree(ctx context.Context) ([]model.Productline, error) {
	var out []model.Productline
	if err := r.DB.WithContext(ctx).
		Order("grade ASC, " + orderColumn(r.DB) + " ASC, id ASC").Find(&out).Error; err != nil {
		return nil, translate(err)
	}
	return out, nil
}

// NameTakenInParent 报告同级下是否已有同名产品线。
func (r *ProductlineRepo) NameTakenInParent(ctx context.Context, name string, parentID, excludeID uint) (bool, error) {
	q := r.DB.WithContext(ctx).Model(&model.Productline{}).
		Where("name = ? AND parent_id = ?", name, parentID)
	if excludeID > 0 {
		q = q.Where("id <> ?", excludeID)
	}
	var count int64
	if err := q.Count(&count).Error; err != nil {
		return false, translate(err)
	}
	return count > 0, nil
}

// HasChildren 报告产品线下是否还有子产品线。
func (r *ProductlineRepo) HasChildren(ctx context.Context, id uint) (bool, error) {
	return r.Exists(ctx, "parent_id = ?", id)
}

// HasProducts 报告产品线下是否还有产品。
func (r *ProductlineRepo) HasProducts(ctx context.Context, id uint) (bool, error) {
	var count int64
	err := r.DB.WithContext(ctx).Model(&model.Product{}).
		Where("productline_id = ?", id).Limit(1).Count(&count).Error
	if err != nil {
		return false, translate(err)
	}
	return count > 0, nil
}

// NameMap 返回 id → 产品线名的映射。
//
// 专门写一个而不让调用方拉全表再内存过滤：
// 产品线虽然不多，但「拉全表只为了查几个名字」
// 是一种会随数据量恶化的习惯。
func (r *ProductlineRepo) NameMap(ctx context.Context, ids []uint) (map[uint]string, error) {
	if len(ids) == 0 {
		return map[uint]string{}, nil
	}
	var rows []model.Productline
	if err := r.DB.WithContext(ctx).
		Select("id", "name").Where("id IN ?", ids).Find(&rows).Error; err != nil {
		return nil, translate(err)
	}
	out := make(map[uint]string, len(rows))
	for _, l := range rows {
		out[l.ID] = l.Name
	}
	return out, nil
}

// DescendantIDs 返回产品线自身及全部子孙的 id。
//
// 依赖物化路径：子孙的 path 以「自身 path + 逗号」为前缀。
// 注意 Path **包含自身 id**，所以「path = 自身 path」命中的就是自己。
func (r *ProductlineRepo) DescendantIDs(ctx context.Context, id uint) ([]uint, error) {
	return descendantIDs[model.Productline](ctx, r.DB, id)
}

// ---------------------------------------------------------------------------
// 产品
// ---------------------------------------------------------------------------

// ProductRepo 是产品仓储。
type ProductRepo struct {
	*Repo[model.Product]
}

// NewProductRepo 构造产品仓储。
func NewProductRepo(db *gorm.DB) *ProductRepo {
	return &ProductRepo{Repo: New[model.Product](db)}
}

// ProductFilter 是产品列表的过滤条件。
type ProductFilter struct {
	ProductlineID uint
	Status        string
	Type          string
	Keywords      string

	// VisibleIDs 是当前用户可见的产品 id。
	//
	// **空切片表示「一个都看不到」，不是「不过滤」** —— acl.VisibleProductIDs
	// 保证返回非 nil，正是为了让这个区分可靠。调用方必须无条件应用它，
	// 否则私有产品会漏给无权用户。
	VisibleIDs []uint

	Pagination pagination.Params
}

// List 分页查询产品。
func (r *ProductRepo) List(ctx context.Context, f ProductFilter) (*pagination.Result[model.Product], error) {
	q := r.DB.WithContext(ctx).Model(&model.Product{})

	q = pagination.Eq(q, "pm_product.productline_id", f.ProductlineID)
	q = pagination.Eq(q, "pm_product.status", f.Status)
	q = pagination.Eq(q, "pm_product.type", f.Type)
	q = pagination.Keyword(q, f.Keywords, "pm_product.name", "pm_product.code", "pm_product.desc")
	// 始终应用可见性过滤（含空切片）。
	q = q.Where("pm_product.id IN ?", f.VisibleIDs)

	return r.Paginate(ctx, q, f.Pagination, productSorter, "pm_product.order ASC, pm_product.id DESC")
}

// FindByCode 按代号查产品。不存在返回 (nil, nil)。
func (r *ProductRepo) FindByCode(ctx context.Context, code string) (*model.Product, error) {
	if code == "" {
		return nil, nil
	}
	var out model.Product
	err := r.DB.WithContext(ctx).Where("code = ?", code).First(&out).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, translate(err)
	}
	return &out, nil
}

// NameTaken 报告产品名是否已被占用。
func (r *ProductRepo) NameTaken(ctx context.Context, name string, excludeID uint) (bool, error) {
	q := r.DB.WithContext(ctx).Model(&model.Product{}).Where("name = ?", name)
	if excludeID > 0 {
		q = q.Where("id <> ?", excludeID)
	}
	var count int64
	if err := q.Count(&count).Error; err != nil {
		return false, translate(err)
	}
	return count > 0, nil
}

// CodeTaken 报告产品代号是否已被占用。
func (r *ProductRepo) CodeTaken(ctx context.Context, code string, excludeID uint) (bool, error) {
	q := r.DB.WithContext(ctx).Model(&model.Product{}).Where("code = ?", code)
	if excludeID > 0 {
		q = q.Where("id <> ?", excludeID)
	}
	var count int64
	if err := q.Count(&count).Error; err != nil {
		return false, translate(err)
	}
	return count > 0, nil
}

// CountStories 统计产品下的需求数（用于阻止删除非空产品）。
//
// 只数未关闭的需求：全是已关闭需求的产品是允许删除的，
// 否则产品会永远删不掉，只能靠改库。
func (r *ProductRepo) CountStories(ctx context.Context, productID uint) (int64, error) {
	var count int64
	err := r.DB.WithContext(ctx).Model(&model.Story{}).
		Where("product_id = ? AND status <> ?", productID, model.StoryStatusClosed).
		Count(&count).Error
	if err != nil {
		return 0, translate(err)
	}
	return count, nil
}

// ListBrief 返回产品简要列表，供下拉选择与筛选栏使用。
func (r *ProductRepo) ListBrief(ctx context.Context, visibleIDs []uint, limit int) ([]model.Product, error) {
	q := r.DB.WithContext(ctx).Model(&model.Product{}).
		Where("id IN ?", visibleIDs).
		Order("name ASC")
	if limit > 0 {
		q = q.Limit(limit)
	}
	var out []model.Product
	if err := q.Find(&out).Error; err != nil {
		return nil, translate(err)
	}
	return out, nil
}

// ListByIDs 按 id 批量取产品（只取列表展示需要的列）。
func (r *ProductRepo) ListByIDs(ctx context.Context, ids []uint) ([]model.Product, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	var out []model.Product
	if err := r.DB.WithContext(ctx).
		Select("id", "name", "code").Where("id IN ?", ids).Find(&out).Error; err != nil {
		return nil, translate(err)
	}
	return out, nil
}

// NameMap 返回 id → 产品名的映射。
func (r *ProductRepo) NameMap(ctx context.Context, ids []uint) (map[uint]string, error) {
	if len(ids) == 0 {
		return map[uint]string{}, nil
	}
	var rows []model.Product
	if err := r.DB.WithContext(ctx).
		Select("id", "name").Where("id IN ?", ids).Find(&rows).Error; err != nil {
		return nil, translate(err)
	}
	out := make(map[uint]string, len(rows))
	for _, p := range rows {
		out[p.ID] = p.Name
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// 模块树
// ---------------------------------------------------------------------------

// ModuleRepo 是模块树仓储。
type ModuleRepo struct {
	*Repo[model.Module]
}

// NewModuleRepo 构造模块树仓储。
func NewModuleRepo(db *gorm.DB) *ModuleRepo {
	return &ModuleRepo{Repo: New[model.Module](db)}
}

// Tree 返回某个产品下指定用途的整棵模块树。
//
// root 是产品 id，moduleType 取 story / task / bug —— 同一个产品下
// 会有多棵同 root 但不同类型的树。
func (r *ModuleRepo) Tree(ctx context.Context, root uint, moduleType string) ([]model.Module, error) {
	var out []model.Module
	if err := r.DB.WithContext(ctx).
		Where("root = ? AND type = ?", root, moduleType).
		Order("grade ASC, " + orderColumn(r.DB) + " ASC, id ASC").Find(&out).Error; err != nil {
		return nil, translate(err)
	}
	return out, nil
}

// NameTakenInParent 报告同级下是否已有同名模块。
func (r *ModuleRepo) NameTakenInParent(ctx context.Context, name string, root uint, moduleType string, parentID, excludeID uint) (bool, error) {
	q := r.DB.WithContext(ctx).Model(&model.Module{}).
		Where("name = ? AND parent_id = ? AND root = ? AND type = ?", name, parentID, root, moduleType)
	if excludeID > 0 {
		q = q.Where("id <> ?", excludeID)
	}
	var count int64
	if err := q.Count(&count).Error; err != nil {
		return false, translate(err)
	}
	return count > 0, nil
}

// HasChildren 报告模块下是否还有子模块。
func (r *ModuleRepo) HasChildren(ctx context.Context, id uint) (bool, error) {
	return r.Exists(ctx, "parent_id = ?", id)
}

// DescendantIDs 返回模块自身及全部子孙的 id。
func (r *ModuleRepo) DescendantIDs(ctx context.Context, id uint) ([]uint, error) {
	return descendantIDs[model.Module](ctx, r.DB, id)
}

// NameMap 返回 id → 模块名的映射。
func (r *ModuleRepo) NameMap(ctx context.Context, ids []uint) (map[uint]string, error) {
	if len(ids) == 0 {
		return map[uint]string{}, nil
	}
	var rows []model.Module
	if err := r.DB.WithContext(ctx).
		Select("id", "name").Where("id IN ?", ids).Find(&rows).Error; err != nil {
		return nil, translate(err)
	}
	out := make(map[uint]string, len(rows))
	for _, m := range rows {
		out[m.ID] = m.Name
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// 产品计划
// ---------------------------------------------------------------------------

// PlanRepo 是产品计划仓储。
type PlanRepo struct {
	*Repo[model.ProductPlan]
}

// NewPlanRepo 构造产品计划仓储。
func NewPlanRepo(db *gorm.DB) *PlanRepo {
	return &PlanRepo{Repo: New[model.ProductPlan](db)}
}

// List 分页查询产品计划。
func (r *PlanRepo) List(ctx context.Context, productID uint, status, keywords string, params pagination.Params) (*pagination.Result[model.ProductPlan], error) {
	q := r.DB.WithContext(ctx).Model(&model.ProductPlan{})
	q = pagination.Eq(q, "pm_productplan.product_id", productID)
	q = pagination.Eq(q, "pm_productplan.status", status)
	q = pagination.Keyword(q, keywords, "pm_productplan.title", "pm_productplan.desc")
	return r.Paginate(ctx, q, params, planSorter, "pm_productplan.id DESC")
}

// TitleTaken 报告同一产品下是否已有同名计划。
func (r *PlanRepo) TitleTaken(ctx context.Context, productID uint, title string, excludeID uint) (bool, error) {
	q := r.DB.WithContext(ctx).Model(&model.ProductPlan{}).
		Where("product_id = ? AND title = ?", productID, title)
	if excludeID > 0 {
		q = q.Where("id <> ?", excludeID)
	}
	var count int64
	if err := q.Count(&count).Error; err != nil {
		return false, translate(err)
	}
	return count > 0, nil
}

// TitleMap 返回 id → 计划标题的映射。
//
// 需求与缺陷都要展示「所属计划」，逐个查会把列表页变成 N+1；
// 一次批量取回标题，缺的（计划已删）自然落空。
func (r *PlanRepo) TitleMap(ctx context.Context, ids []uint) (map[uint]string, error) {
	if len(ids) == 0 {
		return map[uint]string{}, nil
	}
	var rows []model.ProductPlan
	if err := r.DB.WithContext(ctx).
		Select("id", "title").Where("id IN ?", ids).Find(&rows).Error; err != nil {
		return nil, translate(err)
	}
	out := make(map[uint]string, len(rows))
	for _, p := range rows {
		out[p.ID] = p.Title
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// 通用：物化路径辅助
// ---------------------------------------------------------------------------

// pathNode 约束「带 Path 字段的树节点」。
//
// 用类型约束而不是反射：产品线与模块的子孙查询逻辑完全相同，
// 但只有它们有 Path 字段，约束让这件事在编译期就成立。
type pathNode interface {
	model.Productline | model.Module
}

// descendantIDs 返回节点自身及全部子孙的 id。
//
// 两棵树共用一份实现：Path 存「从根到自身的 id 逗号串」（含自身），
// 于是子孙 = path 等于自身 path 的（即自己）∪ path 以「自身 path + 逗号」
// 为前缀的。一次 LIKE 前缀匹配搞定，不依赖递归 CTE。
func descendantIDs[T pathNode](ctx context.Context, db *gorm.DB, id uint) ([]uint, error) {
	var self struct {
		Path string
	}
	if err := db.WithContext(ctx).Model(new(T)).Select("path").
		Where("id = ?", id).Take(&self).Error; err != nil {
		return nil, translate(err)
	}
	if self.Path == "" {
		return []uint{id}, nil
	}

	var ids []uint
	err := db.WithContext(ctx).Model(new(T)).
		Where("path = ? OR path LIKE ?", self.Path, self.Path+",%").
		Pluck("id", &ids).Error
	if err != nil {
		return nil, translate(err)
	}
	// Pluck 在无命中时给出非 nil 空切片；这里的查询必然命中自身，
	// 但防御性地兜一下，避免上层拿到空列表后误判为「无权限」。
	if len(ids) == 0 {
		return []uint{id}, nil
	}
	return ids, nil
}

// ensureProductExists 校验产品存在，返回面向用户的错误。
func ensureProductExists(ctx context.Context, repo *ProductRepo, productID uint) (*model.Product, error) {
	p, err := repo.FindByID(ctx, productID)
	if err != nil {
		return nil, err
	}
	if p == nil {
		return nil, apperr.New(apperr.ProductNotFound)
	}
	return p, nil
}
