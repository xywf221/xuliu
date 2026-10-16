package repository

import (
	"context"
	"errors"

	"gorm.io/gorm"

	"github.com/xywf221/xuliu/internal/model"
	"github.com/xywf221/xuliu/internal/pkg/apperr"
	"github.com/xywf221/xuliu/internal/pkg/pagination"
)

// 项目域仓储：项目、迭代、项目↔产品关联、团队成员。
//
// 与禅道最大的结构差异在这里体现得最明显：禅道把项目与迭代塞进同一张
// zt_project 表，靠 parent/project 列区分；我们拆成 pm_project 与
// pm_execution，因此「查某项目下的迭代」是一次普通的外键查询，
// 不需要任何类型判断。

var projectSorter = pagination.Sorter{
	"id":       "pm_project.id",
	"name":     "pm_project.name",
	"code":     "pm_project.code",
	"begin":    "pm_project.begin",
	"end":      "pm_project.end",
	"status":   "pm_project.status",
	"progress": "pm_project.progress",
}

var executionSorter = pagination.Sorter{
	"id":       "pm_execution.id",
	"name":     "pm_execution.name",
	"code":     "pm_execution.code",
	"begin":    "pm_execution.begin",
	"end":      "pm_execution.end",
	"status":   "pm_execution.status",
	"progress": "pm_execution.progress",
}

var teamSorter = pagination.Sorter{
	"id":    "pm_team.id",
	"join":  "pm_team.join",
	"user":  "pm_team.user_id",
	"hours": "pm_team.hours",
}

// Hours 是一组工时三元组。
//
// 工时在项目、迭代、任务三层都存在，且三层的口径完全相同
// （预计 / 已消耗 / 剩余），所以用一个共享类型表达，
// 免得每层都写一遍三个 float64。
type Hours struct {
	Estimate float64 `json:"estimate"`
	Consumed float64 `json:"consumed"`
	Left     float64 `json:"left"`
}

// ---------------------------------------------------------------------------
// 项目
// ---------------------------------------------------------------------------

// ProjectRepo 是项目仓储。
type ProjectRepo struct {
	*Repo[model.Project]
}

// NewProjectRepo 构造项目仓储。
func NewProjectRepo(db *gorm.DB) *ProjectRepo {
	return &ProjectRepo{Repo: New[model.Project](db)}
}

// ProjectFilter 是项目列表的过滤条件。
type ProjectFilter struct {
	Status   string
	Model    string
	Keywords string
	// ProductID 非零时只返回关联了该产品的项目。
	ProductID uint

	// VisibleIDs 是当前用户可见的项目 id。
	//
	// **空切片表示「一个都看不到」，不是「不过滤」** —— 与产品列表同一个
	// 约定，调用方必须无条件把它当成 IN 条件应用。
	VisibleIDs []uint

	Pagination pagination.Params
}

// List 分页查询项目。
func (r *ProjectRepo) List(ctx context.Context, f ProjectFilter) (*pagination.Result[model.Project], error) {
	q := r.DB.WithContext(ctx).Model(&model.Project{})

	q = pagination.Eq(q, "pm_project.status", f.Status)
	q = pagination.Eq(q, "pm_project.model", f.Model)
	q = pagination.Keyword(q, f.Keywords, "pm_project.name", "pm_project.code", "pm_project.desc")

	if f.ProductID > 0 {
		// 用 EXISTS 而不是 join：一个项目可能关联多个产品，
		// join 会产出重复行，而我们要的是「项目列表」。
		q = q.Where("EXISTS (SELECT 1 FROM "+model.ProjectProduct{}.TableName()+
			" pp WHERE pp.project_id = pm_project.id AND pp.product_id = ?)", f.ProductID)
	}

	// 始终应用可见性过滤（含空切片）。
	q = q.Where("pm_project.id IN ?", f.VisibleIDs)

	return r.Paginate(ctx, q, f.Pagination, projectSorter, "pm_project.id DESC")
}

// FindByCode 按代号查项目。不存在返回 (nil, nil)。
func (r *ProjectRepo) FindByCode(ctx context.Context, code string) (*model.Project, error) {
	if code == "" {
		return nil, nil
	}
	var out model.Project
	err := r.DB.WithContext(ctx).Where("code = ?", code).First(&out).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, translate(err)
	}
	return &out, nil
}

// NameTaken 报告项目名是否已被占用。
func (r *ProjectRepo) NameTaken(ctx context.Context, name string, excludeID uint) (bool, error) {
	return r.taken(ctx, "name", name, excludeID)
}

// CodeTaken 报告项目代号是否已被占用。
func (r *ProjectRepo) CodeTaken(ctx context.Context, code string, excludeID uint) (bool, error) {
	return r.taken(ctx, "code", code, excludeID)
}

// taken 报告某列的值是否已被别的项目占用。
func (r *ProjectRepo) taken(ctx context.Context, column, value string, excludeID uint) (bool, error) {
	if value == "" {
		return false, nil
	}
	q := r.DB.WithContext(ctx).Model(&model.Project{}).Where(column+" = ?", value)
	if excludeID > 0 {
		q = q.Where("id <> ?", excludeID)
	}
	var count int64
	if err := q.Count(&count).Error; err != nil {
		return false, translate(err)
	}
	return count > 0, nil
}

// NameMap 返回 id → 项目名的映射。
func (r *ProjectRepo) NameMap(ctx context.Context, ids []uint) (map[uint]string, error) {
	return nameMap[model.Project](ctx, r.DB, ids)
}

// ListBrief 返回项目简要列表，供下拉选择与筛选栏使用。
func (r *ProjectRepo) ListBrief(ctx context.Context, visibleIDs []uint, limit int) ([]model.Project, error) {
	q := r.DB.WithContext(ctx).Model(&model.Project{}).
		Where("id IN ?", visibleIDs).Order("name ASC")
	if limit > 0 {
		q = q.Limit(limit)
	}
	var out []model.Project
	if err := q.Find(&out).Error; err != nil {
		return nil, translate(err)
	}
	return out, nil
}

// ListByProduct 返回关联了某产品的全部项目。
//
// 供「把需求排进项目」的选择器使用：能排给哪些项目，
// 取决于项目有没有关联这条需求所属的产品。
func (r *ProjectRepo) ListByProduct(ctx context.Context, productID uint) ([]model.Project, error) {
	var out []model.Project
	err := r.DB.WithContext(ctx).Model(&model.Project{}).
		Where("EXISTS (SELECT 1 FROM "+model.ProjectProduct{}.TableName()+
			" pp WHERE pp.project_id = pm_project.id AND pp.product_id = ?)", productID).
		Order("name ASC").Find(&out).Error
	if err != nil {
		return nil, translate(err)
	}
	return out, nil
}

// SumTaskHours 汇总项目下全部任务的工时。
//
// 项目的工时是任务的聚合，不单独维护一份 —— 存一份就要在每次任务工时
// 变化时同步，那又多了一个可能不一致的地方。任务量级（单项目数百条）
// 下一次聚合查询的代价可以接受。
//
// tx 不为 nil 时在其中执行：任务的工时变更往往发生在事务里，
// 而在 sqlite 上「未提交的写」对另一个连接是不可见的（甚至会互相等锁），
// 所以聚合必须与写共用同一个句柄。
func (r *ProjectRepo) SumTaskHours(ctx context.Context, tx *gorm.DB, projectID uint) (Hours, error) {
	return sumHours(ctx, r.db(tx), "project_id = ?", projectID)
}

// ---------------------------------------------------------------------------
// 迭代
// ---------------------------------------------------------------------------

// ExecutionRepo 是迭代仓储。
type ExecutionRepo struct {
	*Repo[model.Execution]
}

// NewExecutionRepo 构造迭代仓储。
func NewExecutionRepo(db *gorm.DB) *ExecutionRepo {
	return &ExecutionRepo{Repo: New[model.Execution](db)}
}

// ExecutionFilter 是迭代列表的过滤条件。
type ExecutionFilter struct {
	ProjectID uint
	Type      string
	Status    string
	Keywords  string

	// VisibleProjectIDs 是当前用户可见的项目 id，空切片表示一个都看不到。
	VisibleProjectIDs []uint

	Pagination pagination.Params
}

// List 分页查询迭代。
func (r *ExecutionRepo) List(ctx context.Context, f ExecutionFilter) (*pagination.Result[model.Execution], error) {
	q := r.DB.WithContext(ctx).Model(&model.Execution{})

	q = pagination.Eq(q, "pm_execution.project_id", f.ProjectID)
	q = pagination.Eq(q, "pm_execution.type", f.Type)
	q = pagination.Eq(q, "pm_execution.status", f.Status)
	q = pagination.Keyword(q, f.Keywords, "pm_execution.name", "pm_execution.code", "pm_execution.desc")

	// 迭代的可见性由所属项目决定：看不到项目就不该看到它的迭代。
	q = q.Where("pm_execution.project_id IN ?", f.VisibleProjectIDs)

	return r.Paginate(ctx, q, f.Pagination, executionSorter, "pm_execution.id DESC")
}

// ListByProject 返回某项目下的全部迭代（不分页），供下拉与看板使用。
func (r *ExecutionRepo) ListByProject(ctx context.Context, projectID uint) ([]model.Execution, error) {
	var out []model.Execution
	if err := r.DB.WithContext(ctx).Model(&model.Execution{}).
		Where("project_id = ?", projectID).
		Order("begin ASC, id ASC").Find(&out).Error; err != nil {
		return nil, translate(err)
	}
	return out, nil
}

// NameMap 返回 id → 迭代名的映射。
func (r *ExecutionRepo) NameMap(ctx context.Context, ids []uint) (map[uint]string, error) {
	return nameMap[model.Execution](ctx, r.DB, ids)
}

// NameTakenInProject 报告同项目下是否已有同名的迭代。
func (r *ExecutionRepo) NameTakenInProject(ctx context.Context, projectID uint, name string, excludeID uint) (bool, error) {
	q := r.DB.WithContext(ctx).Model(&model.Execution{}).
		Where("project_id = ? AND name = ?", projectID, name)
	if excludeID > 0 {
		q = q.Where("id <> ?", excludeID)
	}
	var count int64
	if err := q.Count(&count).Error; err != nil {
		return false, translate(err)
	}
	return count > 0, nil
}

// SumTaskHours 汇总迭代下全部任务的工时。tx 语义同 ProjectRepo.SumTaskHours。
func (r *ExecutionRepo) SumTaskHours(ctx context.Context, tx *gorm.DB, executionID uint) (Hours, error) {
	return sumHours(ctx, r.db(tx), "execution_id = ?", executionID)
}

// ---------------------------------------------------------------------------
// 项目 ↔ 产品
// ---------------------------------------------------------------------------

// ProjectProductRepo 是项目与产品关联的仓储。
type ProjectProductRepo struct {
	*Repo[model.ProjectProduct]
}

// NewProjectProductRepo 构造项目产品关联仓储。
func NewProjectProductRepo(db *gorm.DB) *ProjectProductRepo {
	return &ProjectProductRepo{Repo: New[model.ProjectProduct](db)}
}

// ListByProject 返回项目关联的全部产品关联行。
func (r *ProjectProductRepo) ListByProject(ctx context.Context, projectID uint) ([]model.ProjectProduct, error) {
	var out []model.ProjectProduct
	if err := r.DB.WithContext(ctx).
		Where("project_id = ?", projectID).
		Order("id ASC").Find(&out).Error; err != nil {
		return nil, translate(err)
	}
	return out, nil
}

// ProductIDs 返回项目关联的产品 id。
func (r *ProjectProductRepo) ProductIDs(ctx context.Context, projectID uint) ([]uint, error) {
	var ids []uint
	err := r.DB.WithContext(ctx).Model(&model.ProjectProduct{}).
		Where("project_id = ?", projectID).Pluck("product_id", &ids).Error
	if err != nil {
		return nil, translate(err)
	}
	return ids, nil
}

// ListByProducts 批量返回「产品 id → 关联的项目行」。
//
// 供列表接口一次取回多个项目的产品归属，避免逐行查询。
func (r *ProjectProductRepo) ListByProducts(ctx context.Context, productIDs []uint) ([]model.ProjectProduct, error) {
	if len(productIDs) == 0 {
		return nil, nil
	}
	var out []model.ProjectProduct
	if err := r.DB.WithContext(ctx).
		Where("product_id IN ?", productIDs).Find(&out).Error; err != nil {
		return nil, translate(err)
	}
	return out, nil
}

// Replace 用给定产品集合覆盖项目的产品关联。
//
// 「覆盖」而不是「增删」：项目的产品集合通常一次选完，而多出一个
// 产品就会多出一批可排入的需求，改动很重，让调用方每次提交完整集合
// 比维护增量差异少一类出错可能。
func (r *ProjectProductRepo) Replace(ctx context.Context, tx *gorm.DB, projectID uint, productIDs []uint) error {
	handle := r.db(tx)

	if err := handle.WithContext(ctx).
		Where("project_id = ?", projectID).Delete(&model.ProjectProduct{}).Error; err != nil {
		return translate(err)
	}

	rows := make([]model.ProjectProduct, 0, len(productIDs))
	seen := make(map[uint]struct{}, len(productIDs))
	for _, pid := range productIDs {
		if pid == 0 {
			continue
		}
		if _, dup := seen[pid]; dup {
			continue
		}
		seen[pid] = struct{}{}
		rows = append(rows, model.ProjectProduct{ProjectID: projectID, ProductID: pid})
	}
	if len(rows) == 0 {
		return nil
	}
	if err := handle.WithContext(ctx).Create(&rows).Error; err != nil {
		return translate(err)
	}
	return nil
}

// CountByProduct 统计产品被多少个项目引用。
func (r *ProjectProductRepo) CountByProduct(ctx context.Context, productID uint) (int64, error) {
	return r.Count(ctx, "product_id = ?", productID)
}

// ---------------------------------------------------------------------------
// 团队成员
// ---------------------------------------------------------------------------

// TeamRepo 是项目/迭代团队成员仓储。
type TeamRepo struct {
	*Repo[model.Team]
}

// NewTeamRepo 构造团队仓储。
func NewTeamRepo(db *gorm.DB) *TeamRepo {
	return &TeamRepo{Repo: New[model.Team](db)}
}

// ListByRoot 返回某个项目（或迭代）的团队成员，按加入顺序排列。
func (r *TeamRepo) ListByRoot(ctx context.Context, rootType string, rootID uint) ([]model.Team, error) {
	var out []model.Team
	if err := r.DB.WithContext(ctx).
		Where("root_type = ? AND root_id = ?", rootType, rootID).
		Order(orderColumn(r.DB) + " ASC, id ASC").Find(&out).Error; err != nil {
		return nil, translate(err)
	}
	return out, nil
}

// ListByRoots 批量返回多个团队的成员。
//
// 供项目列表一次取回「成员数与成员头像」，避免每行一次查询。
func (r *TeamRepo) ListByRoots(ctx context.Context, rootType string, rootIDs []uint) ([]model.Team, error) {
	if len(rootIDs) == 0 {
		return nil, nil
	}
	var out []model.Team
	if err := r.DB.WithContext(ctx).
		Where("root_type = ? AND root_id IN ?", rootType, rootIDs).
		Find(&out).Error; err != nil {
		return nil, translate(err)
	}
	return out, nil
}

// MemberIDs 返回团队成员的用户 id。
func (r *TeamRepo) MemberIDs(ctx context.Context, rootType string, rootID uint) ([]uint, error) {
	members, err := r.ListByRoot(ctx, rootType, rootID)
	if err != nil {
		return nil, err
	}
	out := make([]uint, 0, len(members))
	for _, m := range members {
		out = append(out, m.UserID)
	}
	return out, nil
}

// ListByUser 返回某用户参与的所有团队行。
func (r *TeamRepo) ListByUser(ctx context.Context, rootType string, userID uint) ([]model.Team, error) {
	var out []model.Team
	if err := r.DB.WithContext(ctx).
		Where("root_type = ? AND user_id = ?", rootType, userID).Find(&out).Error; err != nil {
		return nil, translate(err)
	}
	return out, nil
}

// IsMember 报告用户是否在团队中。
func (r *TeamRepo) IsMember(ctx context.Context, rootType string, rootID, userID uint) (bool, error) {
	if userID == 0 {
		return false, nil
	}
	return r.Exists(ctx, "root_type = ? AND root_id = ? AND user_id = ?", rootType, rootID, userID)
}

// Add 批量加入成员，已在团队里的会被忽略（幂等）。
func (r *TeamRepo) Add(ctx context.Context, tx *gorm.DB, rootType string, rootID uint, members []model.Team) error {
	if len(members) == 0 {
		return nil
	}
	existing, err := r.MemberIDs(ctx, rootType, rootID)
	if err != nil {
		return err
	}
	have := make(map[uint]struct{}, len(existing))
	for _, id := range existing {
		have[id] = struct{}{}
	}

	rows := make([]model.Team, 0, len(members))
	for _, m := range members {
		if m.UserID == 0 {
			continue
		}
		if _, dup := have[m.UserID]; dup {
			continue
		}
		have[m.UserID] = struct{}{}
		m.RootType = rootType
		m.RootID = rootID
		rows = append(rows, m)
	}
	if len(rows) == 0 {
		return nil
	}
	if err := r.db(tx).WithContext(ctx).Create(&rows).Error; err != nil {
		return translate(err)
	}
	return nil
}

// UpdateMember 更新某个成员的团队信息。
func (r *TeamRepo) UpdateMember(ctx context.Context, tx *gorm.DB, rootType string, rootID, userID uint, fields map[string]any) error {
	if len(fields) == 0 {
		return nil
	}
	res := r.db(tx).WithContext(ctx).Model(&model.Team{}).
		Where("root_type = ? AND root_id = ? AND user_id = ?", rootType, rootID, userID).
		Updates(fields)
	if res.Error != nil {
		return translate(res.Error)
	}
	if res.RowsAffected == 0 {
		ok, err := r.Exists(ctx, "root_type = ? AND root_id = ? AND user_id = ?", rootType, rootID, userID)
		if err != nil {
			return err
		}
		if !ok {
			return apperr.New(apperr.TeamMemberNotFound)
		}
	}
	return nil
}

// Remove 把成员移出团队。
func (r *TeamRepo) Remove(ctx context.Context, tx *gorm.DB, rootType string, rootID, userID uint) error {
	res := r.db(tx).WithContext(ctx).
		Where("root_type = ? AND root_id = ? AND user_id = ?", rootType, rootID, userID).
		Delete(&model.Team{})
	if res.Error != nil {
		return translate(res.Error)
	}
	if res.RowsAffected == 0 {
		return apperr.New(apperr.TeamMemberNotFound)
	}
	return nil
}

// DeleteByRoot 删除整个团队（项目/迭代被删除时调用）。
func (r *TeamRepo) DeleteByRoot(ctx context.Context, tx *gorm.DB, rootType string, rootID uint) error {
	_, err := r.DeleteWhere(ctx, tx, "root_type = ? AND root_id = ?", rootType, rootID)
	return err
}

// ---------------------------------------------------------------------------
// 通用辅助
// ---------------------------------------------------------------------------

// nameMap 批量取「id → name」的映射。
//
// 项目与迭代都有 Name 列，重复的取数逻辑用泛型收一处。
func nameMap[T any](ctx context.Context, db *gorm.DB, ids []uint) (map[uint]string, error) {
	if len(ids) == 0 {
		return map[uint]string{}, nil
	}
	var rows []struct {
		ID   uint
		Name string
	}
	if err := db.WithContext(ctx).Model(new(T)).
		Select("id", "name").Where("id IN ?", ids).Find(&rows).Error; err != nil {
		return nil, translate(err)
	}
	out := make(map[uint]string, len(rows))
	for _, row := range rows {
		out[row.ID] = row.Name
	}
	return out, nil
}

// sumHours 汇总某条件下全部任务的工时三元组。
//
// COALESCE 是必须的：SQL 的 SUM 在没有行时返回 NULL，
// 直接扫进 float64 会报错（sqlite）或得到 0（另一些驱动），
// 这里统一成 0，让三库行为一致。
func sumHours(ctx context.Context, db *gorm.DB, query string, args ...any) (Hours, error) {
	var row struct {
		Estimate float64
		Consumed float64
		Left     float64
	}
	err := db.WithContext(ctx).Model(&model.Task{}).
		Select("COALESCE(SUM(estimate), 0) AS estimate, "+
			"COALESCE(SUM(consumed), 0) AS consumed, "+
			"COALESCE(SUM(left), 0) AS left").
		Where(query, args...).Scan(&row).Error
	if err != nil {
		return Hours{}, translate(err)
	}
	return Hours{Estimate: row.Estimate, Consumed: row.Consumed, Left: row.Left}, nil
}
