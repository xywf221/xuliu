package repository

import (
	"context"
	"errors"
	"fmt"

	"gorm.io/gorm"

	"github.com/xywf221/xuliu/internal/model"
	"github.com/xywf221/xuliu/internal/pkg/apperr"
	"github.com/xywf221/xuliu/internal/pkg/pagination"
)

// 各实体的可排序字段白名单。ORDER BY 无法参数化，所以排序键必须是
// 代码里写死的表达式 —— 这是防注入的关键，不能改成从请求里拼。

var userSorter = pagination.Sorter{
	"id":       "sys_user.id",
	"account":  "sys_user.account",
	"realname": "sys_user.realname",
	"dept":     "sys_user.dept_id",
	"join":     "sys_user.join",
	"last":     "sys_user.last",
	"visits":   "sys_user.visits",
}

var deptSorter = pagination.Sorter{
	"id":    "sys_dept.id",
	"name":  "sys_dept.name",
	"grade": "sys_dept.grade",
	"order": "sys_dept.order",
}

var groupSorter = pagination.Sorter{
	"id":   "sys_group.id",
	"name": "sys_group.name",
}

// UserRepo 是用户仓储。
type UserRepo struct {
	*Repo[model.User]
}

// NewUserRepo 构造用户仓储。
func NewUserRepo(db *gorm.DB) *UserRepo {
	return &UserRepo{Repo: New[model.User](db)}
}

// UserFilter 是用户列表的过滤条件。
type UserFilter struct {
	DeptID uint
	// IncludeSubDept 为 true 时连同子部门的用户一并返回。
	IncludeSubDept bool
	JobRole        string
	Type           string
	// Disabled 为 nil 表示不过滤启用状态（同时返回启用与停用）。
	Disabled *bool
	// Keywords 在账号、姓名、拼音、邮箱上做模糊匹配。
	Keywords string

	Pagination pagination.Params
}

// List 分页查询用户。
func (r *UserRepo) List(ctx context.Context, f UserFilter) (*pagination.Result[model.User], error) {
	q := r.DB.WithContext(ctx).Model(&model.User{})

	if f.DeptID > 0 {
		if f.IncludeSubDept {
			// 部门树用物化路径，取子孙靠前缀匹配。
			// 子查询先算出目标部门的 path，再按前缀筛用户。
			sub := r.DB.Model(&model.Dept{}).Select("path").Where("id = ?", f.DeptID)
			q = q.Where("sys_user.dept_id IN (?)",
				r.DB.Model(&model.Dept{}).Select("id").
					Where("path = (?) OR path LIKE CONCAT((?), ',%')", sub, sub))
		} else {
			q = q.Where("sys_user.dept_id = ?", f.DeptID)
		}
	}

	q = pagination.Eq(q, "sys_user.job_role", f.JobRole)
	q = pagination.Eq(q, "sys_user.type", f.Type)
	q = pagination.EqPtr(q, "sys_user.disabled", f.Disabled)
	q = pagination.Keyword(q, f.Keywords,
		"sys_user.account", "sys_user.realname", "sys_user.pinyin", "sys_user.email")

	return r.Paginate(ctx, q, f.Pagination, userSorter, "sys_user.id DESC")
}

// GetByAccount 按账号查询。不存在返回 apperr.NotFound。
//
// 账号有唯一索引，所以这里能安全地用 First 而不是找一堆再筛。
func (r *UserRepo) GetByAccount(ctx context.Context, account string) (*model.User, error) {
	if account == "" {
		return nil, apperr.New(apperr.NotFound)
	}
	var out model.User
	if err := r.DB.WithContext(ctx).Where("account = ?", account).First(&out).Error; err != nil {
		return nil, translate(err)
	}
	return &out, nil
}

// FindByAccount 按账号查询。不存在返回 (nil, nil)。
func (r *UserRepo) FindByAccount(ctx context.Context, account string) (*model.User, error) {
	if account == "" {
		return nil, nil
	}
	var out model.User
	err := r.DB.WithContext(ctx).Where("account = ?", account).First(&out).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, translate(err)
	}
	return &out, nil
}

// ListByIDs 按 id 批量取用户。
//
// 供「把 id 列表翻成姓名」的场景使用（列表页显示经办人），
// 一次查询取回全部，避免逐行查库造成 N+1。
func (r *UserRepo) ListByIDs(ctx context.Context, ids []uint) ([]model.User, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	var out []model.User
	if err := r.DB.WithContext(ctx).Where("id IN ?", ids).Find(&out).Error; err != nil {
		return nil, translate(err)
	}
	return out, nil
}

// NameMap 返回 id → 显示名的映射。
//
// 显示名优先用真实姓名，缺失时回退到账号，保证界面上不会出现空白。
func (r *UserRepo) NameMap(ctx context.Context, ids []uint) (map[uint]string, error) {
	users, err := r.ListByIDs(ctx, ids)
	if err != nil {
		return nil, err
	}
	out := make(map[uint]string, len(users))
	for _, u := range users {
		out[u.ID] = u.DisplayName()
	}
	return out, nil
}

// CountByDept 统计部门下的用户数（用于阻止删除非空部门）。
func (r *UserRepo) CountByDept(ctx context.Context, deptID uint) (int64, error) {
	return r.Count(ctx, "dept_id = ?", deptID)
}

// UpdateLoginSuccess 记录一次成功登录。
func (r *UserRepo) UpdateLoginSuccess(ctx context.Context, tx *gorm.DB, id uint, at any) error {
	return r.updateLoginFields(ctx, tx, id, map[string]any{
		"last":   at,
		"fails":  0,
		"locked": nil,
		"visits": gorm.Expr("visits + 1"),
	})
}

// UpdateLoginFailure 记录一次失败登录，并按需锁定账号。
//
// lockedAt 为 nil 表示尚未达到锁定阈值，只累加计数。
func (r *UserRepo) UpdateLoginFailure(ctx context.Context, id uint, lockedAt any) error {
	return r.updateLoginFields(ctx, nil, id, map[string]any{
		"fails":  gorm.Expr("fails + 1"),
		"locked": lockedAt,
	})
}

// updateLoginFields 更新登录相关字段。
func (r *UserRepo) updateLoginFields(ctx context.Context, tx *gorm.DB, id uint, fields map[string]any) error {
	// 用 UpdateColumns 而非 Updates：后者会跳过零值，
	// 而这里要传的零值（fails=0）恰恰是有意义的。
	res := r.db(tx).WithContext(ctx).Model(&model.User{}).Where("id = ?", id).Updates(fields)
	if res.Error != nil {
		return translate(res.Error)
	}
	return nil
}

// GetPasswordHash 读取用户的口令哈希。
//
// 单独取而不是把 Password 字段暴露在常规查询里：模型上该字段标了
// json:"-"，但仍会被 Select * 取出来。这个方法的用途是改密校验。
func (r *UserRepo) GetPasswordHash(ctx context.Context, id uint) (string, error) {
	var out struct{ Password string }
	if err := r.DB.WithContext(ctx).Model(&model.User{}).
		Select("password").Where("id = ?", id).Scan(&out).Error; err != nil {
		return "", translate(err)
	}
	if out.Password == "" {
		return "", apperr.New(apperr.UserNotFound)
	}
	return out.Password, nil
}

// ---------------------------------------------------------------------------
// 部门
// ---------------------------------------------------------------------------

// DeptRepo 是部门仓储。
type DeptRepo struct {
	*Repo[model.Dept]
}

// NewDeptRepo 构造部门仓储。
func NewDeptRepo(db *gorm.DB) *DeptRepo {
	return &DeptRepo{Repo: New[model.Dept](db)}
}

// Tree 返回全部部门（按层级与排序字段排列）。
//
// 部门数量通常在百级，一次全取回内存组装树比递归查询更简单也更快，
// 且三种数据库行为完全一致（不依赖递归 CTE，sqlite 老版本也不支持）。
func (r *DeptRepo) Tree(ctx context.Context) ([]model.Dept, error) {
	var out []model.Dept
	if err := r.DB.WithContext(ctx).Order("grade ASC, " + orderColumn(r.DB) + " ASC, id ASC").Find(&out).Error; err != nil {
		return nil, translate(err)
	}
	return out, nil
}

// NameTakenInParent 报告同级下是否已有同名部门。
//
// excludeID 用于编辑场景：改自己的名字时不应把自己算作冲突。
func (r *DeptRepo) NameTakenInParent(ctx context.Context, name string, parentID, excludeID uint) (bool, error) {
	q := r.DB.WithContext(ctx).Model(&model.Dept{}).Where("name = ? AND parent_id = ?", name, parentID)
	if excludeID > 0 {
		q = q.Where("id <> ?", excludeID)
	}
	var count int64
	if err := q.Count(&count).Error; err != nil {
		return false, translate(err)
	}
	return count > 0, nil
}

// HasChildren 报告部门下是否还有子部门。
func (r *DeptRepo) HasChildren(ctx context.Context, id uint) (bool, error) {
	return r.Exists(ctx, "parent_id = ?", id)
}

// DescendantIDs 返回部门自身及全部子孙的 id。
//
// 依赖物化路径：子孙的 path 以「本部门 path + 逗号」为前缀。
func (r *DeptRepo) DescendantIDs(ctx context.Context, id uint) ([]uint, error) {
	var self model.Dept
	if err := r.DB.WithContext(ctx).First(&self, id).Error; err != nil {
		return nil, translate(err)
	}
	if self.Path == "" {
		return []uint{id}, nil
	}

	var ids []uint
	err := r.DB.WithContext(ctx).Model(&model.Dept{}).
		Where("path = ? OR path LIKE ?", self.Path, self.Path+",%").
		Pluck("id", &ids).Error
	if err != nil {
		return nil, translate(err)
	}
	return ids, nil
}

// ---------------------------------------------------------------------------
// 权限组
// ---------------------------------------------------------------------------

// GroupRepo 是权限组仓储。
type GroupRepo struct {
	*Repo[model.Group]
}

// NewGroupRepo 构造权限组仓储。
func NewGroupRepo(db *gorm.DB) *GroupRepo {
	return &GroupRepo{Repo: New[model.Group](db)}
}

// List 分页查询权限组。
func (r *GroupRepo) List(ctx context.Context, keywords string, params pagination.Params) (*pagination.Result[model.Group], error) {
	q := r.DB.WithContext(ctx).Model(&model.Group{})
	q = pagination.Keyword(q, keywords, "sys_group.name", "sys_group.desc")
	return r.Paginate(ctx, q, params, groupSorter, "sys_group.id ASC")
}

// All 返回全部权限组，供新建用户时的下拉选择。
func (r *GroupRepo) All(ctx context.Context) ([]model.Group, error) {
	var out []model.Group
	if err := r.DB.WithContext(ctx).Order("id ASC").Find(&out).Error; err != nil {
		return nil, translate(err)
	}
	return out, nil
}

// NameTaken 报告组名是否已被占用。
func (r *GroupRepo) NameTaken(ctx context.Context, name string, excludeID uint) (bool, error) {
	q := r.DB.WithContext(ctx).Model(&model.Group{}).Where("name = ?", name)
	if excludeID > 0 {
		q = q.Where("id <> ?", excludeID)
	}
	var count int64
	if err := q.Count(&count).Error; err != nil {
		return false, translate(err)
	}
	return count > 0, nil
}

// Privs 返回组持有的权限点列表。
func (r *GroupRepo) Privs(ctx context.Context, groupID uint) ([]string, error) {
	var out []string
	err := r.DB.WithContext(ctx).Model(&model.GroupPriv{}).
		Where("group_id = ?", groupID).Order("perm ASC").Pluck("perm", &out).Error
	if err != nil {
		return nil, translate(err)
	}
	return out, nil
}

// ReplacePrivs 覆盖写入组的权限点。
//
// db 参数让调用方可以把「删旧 + 写新」纳入一个事务，
// 否则中途失败会让组处于「权限被清空」的中间态。
func (r *GroupRepo) ReplacePrivs(ctx context.Context, db *gorm.DB, groupID uint, privs []string) error {
	handle := r.db(db)

	if err := handle.WithContext(ctx).Where("group_id = ?", groupID).
		Delete(&model.GroupPriv{}).Error; err != nil {
		return translate(err)
	}
	if len(privs) == 0 {
		return nil
	}

	rows := make([]model.GroupPriv, 0, len(privs))
	for _, p := range privs {
		rows = append(rows, model.GroupPriv{GroupID: groupID, Perm: p})
	}
	if err := handle.WithContext(ctx).Create(&rows).Error; err != nil {
		return translate(err)
	}
	return nil
}

// CountUsers 统计组内成员数（用于阻止删除非空组）。
func (r *GroupRepo) CountUsers(ctx context.Context, groupID uint) (int64, error) {
	var count int64
	err := r.DB.WithContext(ctx).Model(&model.UserGroup{}).
		Where("group_id = ?", groupID).Count(&count).Error
	if err != nil {
		return 0, translate(err)
	}
	return count, nil
}

// UserGroupIDs 返回用户所属的权限组 id。
func (r *GroupRepo) UserGroupIDs(ctx context.Context, userID uint) ([]uint, error) {
	var out []uint
	err := r.DB.WithContext(ctx).Model(&model.UserGroup{}).
		Where("user_id = ?", userID).Pluck("group_id", &out).Error
	if err != nil {
		return nil, translate(err)
	}
	return out, nil
}

// ReplaceUserGroups 覆盖写入用户的权限组归属。
func (r *GroupRepo) ReplaceUserGroups(ctx context.Context, db *gorm.DB, userID uint, groupIDs []uint) error {
	handle := r.db(db)

	if err := handle.WithContext(ctx).Where("user_id = ?", userID).
		Delete(&model.UserGroup{}).Error; err != nil {
		return translate(err)
	}
	if len(groupIDs) == 0 {
		return nil
	}

	rows := make([]model.UserGroup, 0, len(groupIDs))
	for _, gid := range groupIDs {
		rows = append(rows, model.UserGroup{UserID: userID, GroupID: gid})
	}
	if err := handle.WithContext(ctx).Create(&rows).Error; err != nil {
		return translate(err)
	}
	return nil
}

// ListUsersInGroup 返回组内成员（供组详情页展示）。
func (r *GroupRepo) ListUsersInGroup(ctx context.Context, groupID uint) ([]model.User, error) {
	var out []model.User
	err := r.DB.WithContext(ctx).Model(&model.User{}).
		Joins("JOIN "+model.UserGroup{}.TableName()+" AS ug ON ug.user_id = sys_user.id").
		Where("ug.group_id = ?", groupID).
		Order("sys_user.id ASC").Find(&out).Error
	if err != nil {
		return nil, translate(err)
	}
	return out, nil
}

// UserCountByGroups 统计多个组的成员数，供列表页一次性展示。
//
// 一次 GROUP BY 取回全部计数，避免每行一次查询。
func (r *GroupRepo) UserCountByGroups(ctx context.Context, groupIDs []uint) (map[uint]int64, error) {
	if len(groupIDs) == 0 {
		return map[uint]int64{}, nil
	}

	var rows []struct {
		GroupID uint
		Total   int64
	}
	err := r.DB.WithContext(ctx).Model(&model.UserGroup{}).
		Select("group_id, COUNT(*) AS total").
		Where("group_id IN ?", groupIDs).
		Group("group_id").Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("repository: 统计组成员数失败: %w", err)
	}

	out := make(map[uint]int64, len(rows))
	for _, row := range rows {
		out[row.GroupID] = row.Total
	}
	return out, nil
}
