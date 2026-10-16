package acl

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"strings"

	"gorm.io/gorm"

	"github.com/xywf221/xuliu/internal/identity"
	"github.com/xywf221/xuliu/internal/model"
)

// Enforcer 实现 identity.PermChecker，是权限判定的唯一入口。
//
// 判定分两层：
//
//  1. **权限点**（Can）：用户所属权限组的权限点并集是否包含所需权限点。
//     超管短路。
//  2. **数据可见性**（CanAccessProduct / CanAccessProject）：对象级 ACL
//     （open/private）+ 白名单。这一层**超管也要受约束** ——
//     私有产品的语义就是「没被加入就看不到」，超管理应一视同仁，
//     否则「私有」形同虚设。
type Enforcer struct {
	db *gorm.DB
}

// New 构造 Enforcer。
func New(db *gorm.DB) *Enforcer { return &Enforcer{db: db} }

// Can 报告用户是否拥有某个权限点。实现 identity.PermChecker。
//
// 判定本身就是「集合包含」加一个超管短路，已在 identity.User.Has 里表达，
// 这里直接委托，避免同一条规则写两遍。
func (e *Enforcer) Can(user *identity.User, perm string) bool {
	return user.Has(perm)
}

// LoadRights 加载用户的权限点集合与所属组，用于登录后填充 identity.User。
//
// 一次查询拿到全部权限点并去重：权限点在会话期间不变，
// 逐次请求查库既慢又没必要。
func (e *Enforcer) LoadRights(ctx context.Context, userID uint) (map[string]bool, []uint, error) {
	var rows []struct {
		Perm    string
		GroupID uint
	}

	err := e.db.WithContext(ctx).
		Table(model.UserGroup{}.TableName()+" AS ug").
		Select("gp.perm AS perm, ug.group_id AS group_id").
		Joins("JOIN "+model.GroupPriv{}.TableName()+" AS gp ON gp.group_id = ug.group_id").
		Where("ug.user_id = ?", userID).
		Scan(&rows).Error
	if err != nil {
		return nil, nil, fmt.Errorf("acl: 加载用户权限失败: %w", err)
	}

	rights := make(map[string]bool, len(rows))
	groupSeen := make(map[uint]struct{})
	groups := make([]uint, 0, 4)
	for _, r := range rows {
		rights[r.Perm] = true
		if _, ok := groupSeen[r.GroupID]; !ok {
			groupSeen[r.GroupID] = struct{}{}
			groups = append(groups, r.GroupID)
		}
	}
	return rights, groups, nil
}

// IsSuperAdmin 报告某用户是否为超级管理员。
//
// 判定依据是 sys_company.admins 里的账号列表。
// 用账号而不是 id：admins 是一个可人工编辑的配置字段，
// 让人能直接看懂改谁，而不必去查用户 id。
func (e *Enforcer) IsSuperAdmin(ctx context.Context, account string) (bool, error) {
	company, err := e.company(ctx)
	if err != nil {
		return false, err
	}
	if company == nil {
		return false, nil
	}
	for _, admin := range splitCSV(company.Admins) {
		if admin == account {
			return true, nil
		}
	}
	return false, nil
}

// company 读取唯一的公司记录。
func (e *Enforcer) company(ctx context.Context) (*model.Company, error) {
	var c model.Company
	err := e.db.WithContext(ctx).First(&c).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("acl: 读取公司信息失败: %w", err)
	}
	return &c, nil
}

// CanAccessProduct 判定用户能否访问某产品。
//
// 规则：
//   - 公开产品（acl=open）人人可见；
//   - 私有产品仅白名单、三位负责人、以及关联项目的团队成员可见；
//   - 超管**不**自动放行（见 Enforcer 的说明）。
func (e *Enforcer) CanAccessProduct(ctx context.Context, userID uint, productID uint) (bool, error) {
	var p model.Product
	if err := e.db.WithContext(ctx).First(&p, productID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return false, nil
		}
		return false, fmt.Errorf("acl: 读取产品失败: %w", err)
	}
	if p.ACL != model.ACLPrivate {
		return true, nil
	}

	// 负责人天然可见。
	if userID != 0 && (p.PO == userID || p.QD == userID || p.RD == userID) {
		return true, nil
	}
	if containsID(p.Whitelist, userID) {
		return true, nil
	}
	// 关联项目的团队成员也可见 —— 否则开发接不到私有产品下的需求。
	return e.isMemberOfProductProject(ctx, userID, productID)
}

// CanAccessProject 判定用户能否访问某项目。
func (e *Enforcer) CanAccessProject(ctx context.Context, userID uint, projectID uint) (bool, error) {
	var p model.Project
	if err := e.db.WithContext(ctx).First(&p, projectID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return false, nil
		}
		return false, fmt.Errorf("acl: 读取项目失败: %w", err)
	}
	if p.ACL != model.ACLPrivate {
		return true, nil
	}
	if userID != 0 && (p.PM == userID || p.PO == userID || p.QD == userID || p.RD == userID) {
		return true, nil
	}
	if containsID(p.Whitelist, userID) {
		return true, nil
	}
	return e.isTeamMember(ctx, model.TeamRootProject, projectID, userID)
}

// CanAccessExecution 判定用户能否访问某迭代。
//
// 迭代自身有 ACL，但它同时受所属项目的可见性约束：
// 看不到项目就不该看到项目下的迭代。
func (e *Enforcer) CanAccessExecution(ctx context.Context, userID uint, executionID uint) (bool, error) {
	var ex model.Execution
	if err := e.db.WithContext(ctx).First(&ex, executionID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return false, nil
		}
		return false, fmt.Errorf("acl: 读取迭代失败: %w", err)
	}
	if ok, err := e.CanAccessProject(ctx, userID, ex.ProjectID); err != nil {
		return false, err
	} else if !ok {
		return false, nil
	}
	if ex.ACL != model.ACLPrivate {
		return true, nil
	}
	if userID != 0 && (ex.PM == userID || ex.PO == userID || ex.QD == userID || ex.RD == userID) {
		return true, nil
	}
	if containsID(ex.Whitelist, userID) {
		return true, nil
	}
	return e.isTeamMember(ctx, model.TeamRootExecution, executionID, userID)
}

// VisibleProductIDs 返回用户可见的产品 id 列表。
//
// 供列表接口做过滤。**空切片（含 nil）表示「一个产品都看不到」**，
// 调用方必须无条件把它应用上去 —— 仓储层拿到空切片会渲染成
// `IN (NULL)`，一条都不会匹配。别把空当成「不限」：
// 「不限」是内部聚合才需要的东西，在过滤器上留这样一个默认值
// 只会让忘记传参的调用方悄悄放开全部数据。
//
// 与 CanAccessProduct 必须给出同一个答案（一个是列表、一个是逐条判定），
// 否则会出现「列表里没有、但直接打开详情却能看」这类自相矛盾的状态。
func (e *Enforcer) VisibleProductIDs(ctx context.Context, userID uint) ([]uint, error) {
	var ids []uint
	err := e.db.WithContext(ctx).
		Model(&model.Product{}).
		Where("acl = ?", model.ACLOpen).
		Pluck("id", &ids).Error
	if err != nil {
		return nil, fmt.Errorf("acl: 加载可见产品失败: %w", err)
	}

	// 私有产品：负责人的、白名单里的、以及参与项目所属的。
	var privateProducts []model.Product
	if err := e.db.WithContext(ctx).
		Where("acl = ?", model.ACLPrivate).
		Find(&privateProducts).Error; err != nil {
		return nil, fmt.Errorf("acl: 加载私有产品失败: %w", err)
	}
	if len(privateProducts) == 0 {
		return ids, nil
	}

	joined, err := e.teamedProductIDs(ctx, userID)
	if err != nil {
		return nil, err
	}

	for _, p := range privateProducts {
		switch {
		case p.PO == userID || p.QD == userID || p.RD == userID,
			containsID(p.Whitelist, userID):
			ids = append(ids, p.ID)
		default:
			if _, ok := joined[p.ID]; ok {
				ids = append(ids, p.ID)
			}
		}
	}
	return ids, nil
}

// VisibleProjectIDs 返回用户可见的项目 id 列表。
//
// 与 VisibleProductIDs 同样的约定：返回非 nil（空切片表示「一个都看不到」），
// 调用方必须无条件当成 IN 过滤条件。
func (e *Enforcer) VisibleProjectIDs(ctx context.Context, userID uint) ([]uint, error) {
	var ids []uint
	err := e.db.WithContext(ctx).
		Model(&model.Project{}).
		Where("acl = ?", model.ACLOpen).
		Pluck("id", &ids).Error
	if err != nil {
		return nil, fmt.Errorf("acl: 加载可见项目失败: %w", err)
	}

	var privateProjects []model.Project
	if err := e.db.WithContext(ctx).
		Where("acl = ?", model.ACLPrivate).
		Find(&privateProjects).Error; err != nil {
		return nil, fmt.Errorf("acl: 加载私有项目失败: %w", err)
	}
	if len(privateProjects) == 0 {
		return ids, nil
	}

	// 用户以团队成员身份参与的项目。
	var joined []uint
	err = e.db.WithContext(ctx).
		Model(&model.Team{}).
		Where("root_type = ? AND user_id = ?", model.TeamRootProject, userID).
		Pluck("root_id", &joined).Error
	if err != nil {
		return nil, fmt.Errorf("acl: 加载参与项目失败: %w", err)
	}
	joinedSet := make(map[uint]struct{}, len(joined))
	for _, id := range joined {
		joinedSet[id] = struct{}{}
	}

	for _, p := range privateProjects {
		switch {
		case p.PM == userID || p.PO == userID || p.QD == userID || p.RD == userID,
			containsID(p.Whitelist, userID):
			ids = append(ids, p.ID)
		default:
			if _, ok := joinedSet[p.ID]; ok {
				ids = append(ids, p.ID)
			}
		}
	}
	return ids, nil
}

// isMemberOfProductProject 判定用户是否是「关联了该产品的任一项目」的成员。
//
// 只查项目团队就够了：加进迭代团队的人必须先成为项目团队成员
// （见 service.teamOps.assertParentMember 的 team_member_not_in_project）。
// 也就是说「迭代成员 ⊂ 项目成员」，所以项目团队这一份名单已经把两边都覆盖了。
func (e *Enforcer) isMemberOfProductProject(ctx context.Context, userID, productID uint) (bool, error) {
	if userID == 0 {
		return false, nil
	}
	var count int64
	err := e.db.WithContext(ctx).
		Table(model.Team{}.TableName()+" AS t").
		Joins("JOIN "+model.ProjectProduct{}.TableName()+" AS pp ON pp.project_id = t.root_id").
		Where("t.root_type = ? AND t.user_id = ? AND pp.product_id = ?",
			model.TeamRootProject, userID, productID).
		Count(&count).Error
	if err != nil {
		return false, fmt.Errorf("acl: 判定项目成员失败: %w", err)
	}
	return count > 0, nil
}

// teamedProductIDs 返回「用户参与的项目所关联的产品 id」。
//
// 判定口径必须与 isMemberOfProductProject 一致：一个是列表、一个是逐条，
// 对不上就会出现「详情能打开、列表里却没有」这种自相矛盾的状态。
func (e *Enforcer) teamedProductIDs(ctx context.Context, userID uint) (map[uint]struct{}, error) {
	if userID == 0 {
		return map[uint]struct{}{}, nil
	}

	var ids []uint
	err := e.db.WithContext(ctx).
		Table(model.ProjectProduct{}.TableName()+" AS pp").
		Select("DISTINCT pp.product_id").
		Joins("JOIN "+model.Team{}.TableName()+" AS t ON t.root_type = ? AND t.root_id = pp.project_id",
			model.TeamRootProject).
		Where("t.user_id = ?", userID).
		Pluck("pp.product_id", &ids).Error
	if err != nil {
		return nil, fmt.Errorf("acl: 加载参与项目的产品失败: %w", err)
	}

	out := make(map[uint]struct{}, len(ids))
	for _, id := range ids {
		out[id] = struct{}{}
	}
	return out, nil
}

// isTeamMember 判定用户是否在指定团队中。
func (e *Enforcer) isTeamMember(ctx context.Context, rootType string, rootID, userID uint) (bool, error) {
	if userID == 0 {
		return false, nil
	}
	var count int64
	err := e.db.WithContext(ctx).
		Model(&model.Team{}).
		Where("root_type = ? AND root_id = ? AND user_id = ?", rootType, rootID, userID).
		Count(&count).Error
	if err != nil {
		return false, fmt.Errorf("acl: 判定团队成员失败: %w", err)
	}
	return count > 0, nil
}

// LoadUser 组装完整的 identity.User（含权限点与超管标记）。
//
// 登录成功后调用一次，结果作为会话内容使用。
func (e *Enforcer) LoadUser(ctx context.Context, u *model.User) (*identity.User, error) {
	isSuper, err := e.IsSuperAdmin(ctx, u.Account)
	if err != nil {
		return nil, err
	}

	// 超管不必加载权限点：Can 对它恒真，省一次查询。
	var rights map[string]bool
	var groups []uint
	if !isSuper {
		rights, groups, err = e.LoadRights(ctx, u.ID)
		if err != nil {
			return nil, err
		}
	} else {
		rights = map[string]bool{}
	}

	return &identity.User{
		ID:       u.ID,
		Account:  u.Account,
		Realname: u.Realname,
		DeptID:   u.DeptID,
		IsSuper:  isSuper,
		Rights:   rights,
		GroupIDs: groups,
	}, nil
}

// splitCSV 切分逗号分隔列表，忽略空白项。
//
// admins 字段是人工维护的，写成 "admin, zhangsan ,lisi" 这种带空格的
// 形式很常见，所以每一项都要 TrimSpace。
func splitCSV(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, item := range parts {
		if item = strings.TrimSpace(item); item != "" {
			out = append(out, item)
		}
	}
	return out
}

// containsID 判定 JSON 数组形式的 id 列表里是否含指定 id。
//
// 白名单字段用 JSON 数组存（见 model.Product.Whitelist），
// 解析失败一律视为「不含」，避免脏数据意外放开权限。
func containsID(jsonIDs string, id uint) bool {
	if jsonIDs == "" || id == 0 {
		return false
	}
	var ids []uint
	if err := json.Unmarshal([]byte(jsonIDs), &ids); err != nil {
		return false
	}
	for _, v := range ids {
		if v == id {
			return true
		}
	}
	return false
}

// 确保 Enforcer 满足中间件预期的接口。
var _ identity.PermChecker = (*Enforcer)(nil)
