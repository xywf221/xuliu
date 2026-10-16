package service

import (
	"context"
	"fmt"
	"sort"

	"gorm.io/gorm"

	"github.com/xywf221/xuliu/internal/acl"
	"github.com/xywf221/xuliu/internal/dto"
	"github.com/xywf221/xuliu/internal/model"
	"github.com/xywf221/xuliu/internal/pkg/apperr"
	"github.com/xywf221/xuliu/internal/pkg/pagination"
	"github.com/xywf221/xuliu/internal/repository"
)

// DeptService 负责部门树的维护。
type DeptService struct {
	db     *gorm.DB
	depts  *repository.DeptRepo
	users  *repository.UserRepo
	groups *repository.GroupRepo
}

// NewDeptService 构造部门服务。
func NewDeptService(db *gorm.DB, depts *repository.DeptRepo, users *repository.UserRepo, groups *repository.GroupRepo) *DeptService {
	return &DeptService{db: db, depts: depts, users: users, groups: groups}
}

// Tree 返回部门树。
//
// 一次性取回全部部门在内存里组树，而不是递归查询：部门数量在百级，
// 内存组树更快，也避免了递归 CTE 在三库上的语法差异。
func (s *DeptService) Tree(ctx context.Context) ([]dto.DeptResp, error) {
	depts, err := s.depts.Tree(ctx)
	if err != nil {
		return nil, err
	}

	// 一次性统计各部门的直属人数，避免逐部门查询。
	counts, err := s.userCountByDept(ctx)
	if err != nil {
		return nil, err
	}

	// 一次性取部门负责人姓名。
	managerNames := map[uint]string{}
	managerIDs := make([]uint, 0, len(depts))
	for _, d := range depts {
		if d.Manager > 0 {
			managerIDs = append(managerIDs, d.Manager)
		}
	}
	if len(managerIDs) > 0 {
		if names, err := s.users.NameMap(ctx, managerIDs); err == nil {
			managerNames = names
		}
	}

	// 先构造扁平响应并建索引。
	nodes := make(map[uint]*dto.DeptResp, len(depts))
	for i := range depts {
		d := depts[i]
		nodes[d.ID] = &dto.DeptResp{
			ID:          d.ID,
			Name:        d.Name,
			ParentID:    d.ParentID,
			Path:        d.Path,
			Grade:       d.Grade,
			Order:       d.Order,
			ManagerID:   d.Manager,
			ManagerName: managerNames[d.Manager],
			UserCount:   counts[d.ID],
		}
	}

	// 再挂接父子关系。
	roots := make([]*dto.DeptResp, 0)
	for i := range depts {
		d := depts[i]
		node := nodes[d.ID]
		if parent, ok := nodes[d.ParentID]; ok && d.ParentID != 0 {
			parent.Children = append(parent.Children, *node)
			parent.HasChildren = true
			// 由于节点会被 copy 进父节点的 Children，需要事后同步，
			// 见下方 rebuild 步骤。
		} else {
			roots = append(roots, node)
		}
	}

	// 上面 append 的是值拷贝，父子关系需要重新自底向上组装一遍。
	out := buildDeptTree(roots, nodes)
	return out, nil
}

// buildDeptTree 按 id 索引重建树形结构。
//
// 这样写是为了避开「值拷贝导致引用失效」的问题：先建立完整的父子索引，
// 再从根递归展开。
func buildDeptTree(roots []*dto.DeptResp, nodes map[uint]*dto.DeptResp) []dto.DeptResp {
	// childrenOf 记录每个节点的子节点 id。
	childrenOf := map[uint][]uint{}
	for id, node := range nodes {
		if node.ParentID != 0 {
			if _, ok := nodes[node.ParentID]; ok {
				childrenOf[node.ParentID] = append(childrenOf[node.ParentID], id)
			}
		}
	}
	// 子节点按 order、id 排序，保证展示顺序稳定。
	for pid := range childrenOf {
		ids := childrenOf[pid]
		sort.Slice(ids, func(i, j int) bool {
			a, b := nodes[ids[i]], nodes[ids[j]]
			if a.Order != b.Order {
				return a.Order < b.Order
			}
			return a.ID < b.ID
		})
	}

	var expand func(n *dto.DeptResp) dto.DeptResp
	expand = func(n *dto.DeptResp) dto.DeptResp {
		out := *n
		out.Children = nil
		for _, cid := range childrenOf[n.ID] {
			out.Children = append(out.Children, expand(nodes[cid]))
		}
		out.HasChildren = len(out.Children) > 0
		return out
	}

	out := make([]dto.DeptResp, 0, len(roots))
	for _, r := range roots {
		out = append(out, expand(r))
	}
	return out
}

// Create 新建部门。
func (s *DeptService) Create(ctx context.Context, req dto.CreateDeptReq) (*dto.DeptResp, error) {
	dept := &model.Dept{Name: req.Name, ParentID: req.ParentID, Manager: req.Manager, Order: req.Order}

	// 计算层级与物化路径。
	if req.ParentID == 0 {
		dept.Grade = 1
	} else {
		parent, err := s.depts.GetByID(ctx, req.ParentID)
		if err != nil {
			return nil, apperr.Wrapf(apperr.DeptNotFound, err, "上级部门不存在")
		}
		dept.Grade = parent.Grade + 1
	}

	// 同级重名检查。
	taken, err := s.depts.NameTakenInParent(ctx, req.Name, req.ParentID, 0)
	if err != nil {
		return nil, err
	}
	if taken {
		return nil, apperr.New(apperr.DeptNameTaken)
	}

	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := s.depts.Create(ctx, tx, dept); err != nil {
			return err
		}
		// path 需要自己的 id，所以只能在插入后回写。
		path := fmt.Sprintf("%d", dept.ID)
		if req.ParentID != 0 {
			parent, err := s.depts.GetByID(ctx, req.ParentID)
			if err != nil {
				return err
			}
			path = parent.Path + "," + path
		}
		return s.depts.UpdateFields(ctx, tx, dept.ID, map[string]any{"path": path})
	})
	if err != nil {
		return nil, err
	}

	return s.Get(ctx, dept.ID)
}

// Get 取部门详情。
func (s *DeptService) Get(ctx context.Context, id uint) (*dto.DeptResp, error) {
	dept, err := s.depts.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	counts, err := s.userCountByDept(ctx)
	if err != nil {
		return nil, err
	}
	hasChildren, err := s.depts.HasChildren(ctx, id)
	if err != nil {
		return nil, err
	}

	out := &dto.DeptResp{
		ID:          dept.ID,
		Name:        dept.Name,
		ParentID:    dept.ParentID,
		Path:        dept.Path,
		Grade:       dept.Grade,
		Order:       dept.Order,
		ManagerID:   dept.Manager,
		UserCount:   counts[dept.ID],
		HasChildren: hasChildren,
	}
	if dept.Manager > 0 {
		if names, err := s.users.NameMap(ctx, []uint{dept.Manager}); err == nil {
			out.ManagerName = names[dept.Manager]
		}
	}
	return out, nil
}

// Update 编辑部门。
func (s *DeptService) Update(ctx context.Context, id uint, req dto.UpdateDeptReq) (*dto.DeptResp, error) {
	dept, err := s.depts.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}

	fields := map[string]any{}
	if req.Name != nil {
		parentID := dept.ParentID
		if req.ParentID != nil {
			parentID = *req.ParentID
		}
		taken, err := s.depts.NameTakenInParent(ctx, *req.Name, parentID, id)
		if err != nil {
			return nil, err
		}
		if taken {
			return nil, apperr.New(apperr.DeptNameTaken)
		}
		fields["name"] = *req.Name
	}
	if req.Manager != nil {
		fields["manager"] = *req.Manager
	}
	if req.Order != nil {
		fields["order"] = *req.Order
	}

	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// 换上级是最容易出错的一步：必须先确认不会形成环，
		// 否则树会断开且无法自愈（A 的父是 B，B 的父又是 A）。
		if req.ParentID != nil && *req.ParentID != dept.ParentID {
			if err := s.moveDept(ctx, tx, dept, *req.ParentID); err != nil {
				return err
			}
		}
		if len(fields) > 0 {
			return s.depts.UpdateFields(ctx, tx, id, fields)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	return s.Get(ctx, id)
}

// moveDept 变更部门的上级，并更新自身与全部子孙的 path/grade。
func (s *DeptService) moveDept(ctx context.Context, tx *gorm.DB, dept *model.Dept, newParentID uint) error {
	var newPath string
	var newGrade int

	if newParentID == 0 {
		newPath = fmt.Sprintf("%d", dept.ID)
		newGrade = 1
	} else {
		// 不能把部门挂到自己的子孙下面，否则形成环。
		descendants, err := s.depts.DescendantIDs(ctx, dept.ID)
		if err != nil {
			return err
		}
		for _, d := range descendants {
			if d == newParentID {
				return apperr.New(apperr.DeptCycle)
			}
		}

		parent, err := s.depts.GetByID(ctx, newParentID)
		if err != nil {
			return apperr.Wrapf(apperr.DeptNotFound, err, "上级部门不存在")
		}
		newPath = parent.Path + "," + fmt.Sprintf("%d", dept.ID)
		newGrade = parent.Grade + 1
	}

	oldPath := dept.Path
	oldGrade := dept.Grade
	gradeDelta := newGrade - oldGrade

	if err := s.depts.UpdateFields(ctx, tx, dept.ID, map[string]any{
		"parent_id": newParentID,
		"path":      newPath,
		"grade":     newGrade,
	}); err != nil {
		return err
	}

	// 子孙的 path 前缀要整体替换，grade 整体平移。
	if oldPath != "" && oldPath != newPath {
		var descendants []model.Dept
		if err := tx.WithContext(ctx).
			Where("path LIKE ?", oldPath+",%").
			Find(&descendants).Error; err != nil {
			return err
		}
		for _, d := range descendants {
			updated := newPath + d.Path[len(oldPath):]
			if err := s.depts.UpdateFields(ctx, tx, d.ID, map[string]any{
				"path":  updated,
				"grade": d.Grade + gradeDelta,
			}); err != nil {
				return err
			}
		}
	}
	return nil
}

// Delete 删除部门。
//
// 有子部门或有用户时拒绝删除 —— 前者会让子部门变成孤儿，
// 后者会让用户的部门引用悬空。
func (s *DeptService) Delete(ctx context.Context, id uint) error {
	if _, err := s.depts.GetByID(ctx, id); err != nil {
		return err
	}

	hasChildren, err := s.depts.HasChildren(ctx, id)
	if err != nil {
		return err
	}
	if hasChildren {
		return apperr.New(apperr.DeptHasChildren)
	}

	userCount, err := s.users.CountByDept(ctx, id)
	if err != nil {
		return err
	}
	if userCount > 0 {
		return apperr.Newf(apperr.Conflict, "该部门下还有 %d 名用户，请先调整他们的部门", userCount)
	}

	return s.depts.Delete(ctx, nil, id)
}

// userCountByDept 一次性统计各部门的直属人数。
func (s *DeptService) userCountByDept(ctx context.Context) (map[uint]int64, error) {
	var rows []struct {
		DeptID uint
		Total  int64
	}
	err := s.db.WithContext(ctx).Model(&model.User{}).
		Select("dept_id, COUNT(*) AS total").
		Group("dept_id").Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("service: 统计部门人数失败: %w", err)
	}
	out := make(map[uint]int64, len(rows))
	for _, r := range rows {
		out[r.DeptID] = r.Total
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// 权限组
// ---------------------------------------------------------------------------

// GroupService 负责权限组的维护。
type GroupService struct {
	db     *gorm.DB
	groups *repository.GroupRepo
	users  *repository.UserRepo
}

// NewGroupService 构造权限组服务。
func NewGroupService(db *gorm.DB, groups *repository.GroupRepo, users *repository.UserRepo) *GroupService {
	return &GroupService{db: db, groups: groups, users: users}
}

// List 分页查询权限组。
func (s *GroupService) List(ctx context.Context, keywords string, params pagination.Params) (*pagination.Result[dto.GroupResp], error) {
	result, err := s.groups.List(ctx, keywords, params)
	if err != nil {
		return nil, err
	}

	ids := make([]uint, 0, len(result.List))
	for _, g := range result.List {
		ids = append(ids, g.ID)
	}

	// 一次性取回各组的权限点数量与成员数，避免逐组查询。
	privCounts, err := s.privCountByGroups(ctx, ids)
	if err != nil {
		return nil, err
	}
	userCounts, err := s.groups.UserCountByGroups(ctx, ids)
	if err != nil {
		return nil, err
	}

	out := make([]dto.GroupResp, 0, len(result.List))
	for _, g := range result.List {
		out = append(out, dto.GroupResp{
			ID:        g.ID,
			Name:      g.Name,
			JobRole:   g.JobRole,
			Desc:      g.Desc,
			ProjectID: g.ProjectID,
			PrivCount: privCounts[g.ID],
			UserCount: userCounts[g.ID],
		})
	}

	return &pagination.Result[dto.GroupResp]{
		List: out, Total: result.Total, Page: result.Page, Size: result.Size,
	}, nil
}

// Get 取权限组详情（含权限点与成员）。
func (s *GroupService) Get(ctx context.Context, id uint) (*dto.GroupResp, error) {
	group, err := s.groups.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}

	privs, err := s.groups.Privs(ctx, id)
	if err != nil {
		return nil, err
	}

	out := &dto.GroupResp{
		ID:        group.ID,
		Name:      group.Name,
		JobRole:   group.JobRole,
		Desc:      group.Desc,
		ProjectID: group.ProjectID,
		Privs:     privs,
		PrivCount: len(privs),
	}

	members, err := s.groups.ListUsersInGroup(ctx, id)
	if err != nil {
		return nil, err
	}
	out.UserCount = int64(len(members))
	for _, u := range members {
		out.Users = append(out.Users, dto.UserBrief{
			ID:          u.ID,
			Account:     u.Account,
			Realname:    u.Realname,
			Avatar:      u.Avatar,
			DisplayName: u.DisplayName(),
		})
	}
	return out, nil
}

// Create 新建权限组。
func (s *GroupService) Create(ctx context.Context, req dto.CreateGroupReq) (*dto.GroupResp, error) {
	taken, err := s.groups.NameTaken(ctx, req.Name, 0)
	if err != nil {
		return nil, err
	}
	if taken {
		return nil, apperr.New(apperr.GroupNameTaken)
	}

	group := &model.Group{Name: req.Name, JobRole: req.JobRole, Desc: req.Desc}

	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := s.groups.Create(ctx, tx, group); err != nil {
			if repository.IsDuplicated(err) {
				return apperr.New(apperr.GroupNameTaken)
			}
			return err
		}
		return s.replacePrivs(ctx, tx, group.ID, req.Privs)
	})
	if err != nil {
		return nil, err
	}

	return s.Get(ctx, group.ID)
}

// Update 编辑权限组。
func (s *GroupService) Update(ctx context.Context, id uint, req dto.UpdateGroupReq) (*dto.GroupResp, error) {
	if _, err := s.groups.GetByID(ctx, id); err != nil {
		return nil, err
	}

	fields := map[string]any{}
	if req.Name != nil {
		taken, err := s.groups.NameTaken(ctx, *req.Name, id)
		if err != nil {
			return nil, err
		}
		if taken {
			return nil, apperr.New(apperr.GroupNameTaken)
		}
		fields["name"] = *req.Name
	}
	if req.JobRole != nil {
		fields["job_role"] = *req.JobRole
	}
	if req.Desc != nil {
		fields["desc"] = *req.Desc
	}

	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if len(fields) > 0 {
			if err := s.groups.UpdateFields(ctx, tx, id, fields); err != nil {
				return err
			}
		}
		if req.Privs != nil {
			if err := s.replacePrivs(ctx, tx, id, *req.Privs); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	return s.Get(ctx, id)
}

// Delete 删除权限组。
func (s *GroupService) Delete(ctx context.Context, id uint) error {
	if _, err := s.groups.GetByID(ctx, id); err != nil {
		return err
	}

	count, err := s.groups.CountUsers(ctx, id)
	if err != nil {
		return err
	}
	if count > 0 {
		return apperr.Newf(apperr.GroupHasUsers, "该权限组下还有 %d 名成员，请先移除后再删除", count)
	}

	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// 先清权限点，避免留下孤儿记录。
		if err := s.groups.ReplacePrivs(ctx, tx, id, nil); err != nil {
			return err
		}
		return s.groups.Delete(ctx, tx, id)
	})
}

// Privileges 返回全部权限点（按模块分组），可选标记某组是否已持有。
//
// groupID 为 0 时不标记（用于新建组时的默认勾选状态）。
func (s *GroupService) Privileges(ctx context.Context, groupID uint) ([]dto.PrivGroupResp, error) {
	held := map[string]bool{}
	if groupID > 0 {
		privs, err := s.groups.Privs(ctx, groupID)
		if err != nil {
			return nil, err
		}
		for _, p := range privs {
			held[p] = true
		}
	}

	groups := acl.ModulePrivs()
	out := make([]dto.PrivGroupResp, 0, len(groups))
	for _, g := range groups {
		item := dto.PrivGroupResp{Module: g.Module, Name: g.Name}
		for _, p := range g.Privs {
			item.Privs = append(item.Privs, dto.PrivResp{
				Priv:   string(p.Priv),
				Module: p.Module,
				Method: p.Method,
				Name:   p.Name,
				Held:   held[string(p.Priv)],
			})
		}
		out = append(out, item)
	}
	return out, nil
}

// replacePrivs 校验并覆盖写入权限点。
//
// 非法权限点被静默过滤而非报错：前端可能带着过期版本的权限点列表，
// 因为一个已下线的功能就拒绝整个保存请求，对用户是纯粹的困扰。
func (s *GroupService) replacePrivs(ctx context.Context, tx *gorm.DB, groupID uint, privs []string) error {
	typed := make([]acl.Priv, 0, len(privs))
	for _, p := range privs {
		typed = append(typed, acl.Priv(p))
	}
	clean := acl.FilterValid(typed)

	out := make([]string, 0, len(clean))
	for _, p := range clean {
		out = append(out, string(p))
	}
	return s.groups.ReplacePrivs(ctx, tx, groupID, out)
}

// privCountByGroups 一次性统计各组的权限点数量。
func (s *GroupService) privCountByGroups(ctx context.Context, groupIDs []uint) (map[uint]int, error) {
	out := make(map[uint]int)
	if len(groupIDs) == 0 {
		return out, nil
	}

	var rows []struct {
		GroupID uint
		Total   int
	}
	err := s.db.WithContext(ctx).Model(&model.GroupPriv{}).
		Select("group_id, COUNT(*) AS total").
		Where("group_id IN ?", groupIDs).
		Group("group_id").Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("service: 统计权限点数量失败: %w", err)
	}
	for _, r := range rows {
		out[r.GroupID] = r.Total
	}
	return out, nil
}
