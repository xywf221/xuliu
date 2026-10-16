package service

import (
	"context"
	"time"

	"gorm.io/gorm"

	"github.com/xywf221/xuliu/internal/config"
	"github.com/xywf221/xuliu/internal/dto"
	"github.com/xywf221/xuliu/internal/identity"
	"github.com/xywf221/xuliu/internal/model"
	"github.com/xywf221/xuliu/internal/pkg/apperr"
	"github.com/xywf221/xuliu/internal/pkg/hash"
	"github.com/xywf221/xuliu/internal/pkg/pagination"
	"github.com/xywf221/xuliu/internal/repository"
)

// UserService 负责用户的增删改查。
type UserService struct {
	cfg    *config.Config
	users  *repository.UserRepo
	depts  *repository.DeptRepo
	groups *repository.GroupRepo
	db     *gorm.DB
}

// NewUserService 构造用户服务。
func NewUserService(
	cfg *config.Config,
	db *gorm.DB,
	users *repository.UserRepo,
	depts *repository.DeptRepo,
	groups *repository.GroupRepo,
) *UserService {
	return &UserService{cfg: cfg, users: users, depts: depts, groups: groups, db: db}
}

// List 分页查询用户。
//
// 返回时批量补全部门名，避免每行一次查询（列表 20 行就会变成 21 次查询）。
func (s *UserService) List(ctx context.Context, req dto.UserFilterReq) (*pagination.Result[dto.UserResp], error) {
	filter := repository.UserFilter{
		DeptID:         req.DeptID,
		IncludeSubDept: req.IncludeSubDept,
		JobRole:        req.JobRole,
		Type:           req.Type,
		Disabled:       req.Disabled,
		Keywords:       req.Keywords,
		Pagination:     pagination.Params{Page: req.Page, Size: req.Size, Order: req.Order},
	}

	result, err := s.users.List(ctx, filter)
	if err != nil {
		return nil, err
	}

	deptNames, err := s.deptNameMap(ctx)
	if err != nil {
		return nil, err
	}

	now := time.Now()
	out := make([]dto.UserResp, 0, len(result.List))
	for i := range result.List {
		out = append(out, toUserResp(&result.List[i], deptNames, now, s.cfg.Auth.LockDuration))
	}

	return &pagination.Result[dto.UserResp]{
		List:  out,
		Total: result.Total,
		Page:  result.Page,
		Size:  result.Size,
	}, nil
}

// Get 取用户详情（含所属权限组）。
func (s *UserService) Get(ctx context.Context, id uint) (*dto.UserResp, error) {
	user, err := s.users.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}

	deptNames, err := s.deptNameMap(ctx)
	if err != nil {
		return nil, err
	}
	out := toUserResp(user, deptNames, time.Now(), s.cfg.Auth.LockDuration)

	// 补全所属组。
	groupIDs, err := s.groups.UserGroupIDs(ctx, id)
	if err != nil {
		return nil, err
	}
	for _, gid := range groupIDs {
		g, err := s.groups.FindByID(ctx, gid)
		if err != nil || g == nil {
			continue
		}
		out.Groups = append(out.Groups, dto.GroupBrief{ID: g.ID, Name: g.Name})
	}

	// 补全上级姓名。
	if user.Superior > 0 {
		if sup, err := s.users.FindByID(ctx, user.Superior); err == nil && sup != nil {
			out.SuperiorName = sup.DisplayName()
		}
	}
	return &out, nil
}

// Create 新建用户。
func (s *UserService) Create(ctx context.Context, req dto.CreateUserReq) (*dto.UserResp, error) {
	// 账号唯一性检查放在哈希之前：账号冲突是常见的用户输入错误，
	// 没必要为它付出一次 250ms 的 bcrypt 计算。
	exists, err := s.users.FindByAccount(ctx, req.Account)
	if err != nil {
		return nil, err
	}
	if exists != nil {
		return nil, apperr.New(apperr.AccountTaken)
	}

	if err := hash.ValidatePasswordStrength(req.Password); err != nil {
		return nil, apperr.Wrapf(apperr.InvalidParam, err, "%s", err.Error())
	}

	// 部门存在性校验：避免创建出挂在不存在部门下的用户。
	if req.DeptID > 0 {
		if _, err := s.depts.GetByID(ctx, req.DeptID); err != nil {
			return nil, apperr.Wrapf(apperr.DeptNotFound, err, "所选部门不存在")
		}
	}

	encoded, err := hash.Password(req.Password, s.cfg.Auth.BcryptCost)
	if err != nil {
		return nil, apperr.Wrap(apperr.Internal, err)
	}

	user := &model.User{
		Account:  req.Account,
		Password: encoded,
		Realname: req.Realname,
		Nickname: req.Nickname,
		DeptID:   req.DeptID,
		JobRole:  req.JobRole,
		Type:     defaultString(req.Type, model.UserTypeInside),
		Gender:   req.Gender,
		Email:    req.Email,
		Mobile:   req.Mobile,
		Phone:    req.Phone,
		Weixin:   req.Weixin,
		QQ:       req.QQ,
		Superior: req.SuperiorID,
	}

	// 归属公司：单实例系统，取唯一那条。
	var company model.Company
	if err := s.db.WithContext(ctx).First(&company).Error; err == nil {
		user.CompanyID = company.ID
	}

	if req.Join != "" {
		if t, err := time.Parse("2006-01-02", req.Join); err == nil {
			user.Join = &t
		}
	}

	// 用户名与分组要一起写，用事务保证不会出现「用户建了但组没绑上」。
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := s.users.Create(ctx, tx, user); err != nil {
			// 并发场景下可能绕过前面的检查，这里再兜一次唯一冲突。
			if repository.IsDuplicated(err) {
				return apperr.New(apperr.AccountTaken)
			}
			return err
		}
		if len(req.Groups) > 0 {
			if err := s.groups.ReplaceUserGroups(ctx, tx, user.ID, req.Groups); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	return s.Get(ctx, user.ID)
}

// Update 编辑用户。
func (s *UserService) Update(ctx context.Context, id uint, req dto.UpdateUserReq) (*dto.UserResp, error) {
	// 先确认目标存在，再逐项校验字段 —— 否则会把「用户不存在」报成 400。
	if _, err := s.users.GetByID(ctx, id); err != nil {
		return nil, err
	}

	fields := map[string]any{}
	setString(fields, "realname", req.Realname)
	setString(fields, "nickname", req.Nickname)
	setString(fields, "job_role", req.JobRole)
	setString(fields, "type", req.Type)
	setString(fields, "gender", req.Gender)
	setString(fields, "email", req.Email)
	setString(fields, "mobile", req.Mobile)
	setString(fields, "phone", req.Phone)
	setString(fields, "weixin", req.Weixin)
	setString(fields, "qq", req.QQ)
	if req.SuperiorID != nil {
		if err := s.validateSuperior(ctx, id, *req.SuperiorID); err != nil {
			return nil, err
		}
		fields["superior"] = *req.SuperiorID
	}
	if req.DeptID != nil {
		if *req.DeptID > 0 {
			if _, err := s.depts.GetByID(ctx, *req.DeptID); err != nil {
				return nil, apperr.Wrapf(apperr.DeptNotFound, err, "所选部门不存在")
			}
		}
		fields["dept_id"] = *req.DeptID
	}
	if req.Disabled != nil {
		// 不允许停用自己：会把管理员自己锁在系统外，且只能靠改库恢复。
		if *req.Disabled && id == currentUserID(ctx) {
			return nil, apperr.Newf(apperr.InvalidParam, "不能停用当前登录的账号")
		}
		fields["disabled"] = *req.Disabled
	}
	if req.Join != nil {
		if *req.Join == "" {
			fields["join"] = nil
		} else {
			t, err := time.Parse("2006-01-02", *req.Join)
			if err != nil {
				return nil, apperr.Newf(apperr.InvalidParam, "入职日期格式应为 YYYY-MM-DD")
			}
			fields["join"] = t
		}
	}

	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if len(fields) > 0 {
			if err := s.users.UpdateFields(ctx, tx, id, fields); err != nil {
				return err
			}
		}
		// Groups 为 nil 表示不改动；传空数组表示清空。
		if req.Groups != nil {
			if err := s.groups.ReplaceUserGroups(ctx, tx, id, *req.Groups); err != nil {
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

// Delete 删除用户（软删除）。
//
// 已被业务数据引用的用户不允许删除，只能停用 —— 否则需求/任务的
// 经办人外键会指向不存在的用户，历史记录里就出现了「无名人氏」。
func (s *UserService) Delete(ctx context.Context, id uint) error {
	if id == currentUserID(ctx) {
		return apperr.Newf(apperr.InvalidParam, "不能删除当前登录的账号")
	}
	if _, err := s.users.GetByID(ctx, id); err != nil {
		return err
	}

	if err := s.checkUserUnreferenced(ctx, id); err != nil {
		return err
	}

	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// 先解绑分组，避免留下指向已删用户的孤儿关联。
		if err := s.groups.ReplaceUserGroups(ctx, tx, id, nil); err != nil {
			return err
		}
		return s.users.Delete(ctx, tx, id)
	})
}

// Brief 返回用户简要列表，供下拉选择。
func (s *UserService) Brief(ctx context.Context, keywords string, limit int) ([]dto.UserBrief, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	disabled := false
	filter := repository.UserFilter{
		Keywords:   keywords,
		Disabled:   &disabled,
		Pagination: pagination.Params{Page: 1, Size: limit, Order: "realname_asc"},
	}
	result, err := s.users.List(ctx, filter)
	if err != nil {
		return nil, err
	}

	deptNames, err := s.deptNameMap(ctx)
	if err != nil {
		return nil, err
	}

	out := make([]dto.UserBrief, 0, len(result.List))
	for _, u := range result.List {
		out = append(out, dto.UserBrief{
			ID:          u.ID,
			Account:     u.Account,
			Realname:    u.Realname,
			Avatar:      u.Avatar,
			DisplayName: u.DisplayName(),
			DeptName:    deptNames[u.DeptID],
		})
	}
	return out, nil
}

// deptNameMap 返回 id → 部门名的映射。
func (s *UserService) deptNameMap(ctx context.Context) (map[uint]string, error) {
	depts, err := s.depts.Tree(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[uint]string, len(depts))
	for _, d := range depts {
		out[d.ID] = d.Name
	}
	return out, nil
}

// validateSuperior 校验直属上级的合法性。
//
// 只拦一个明显错误：把自己设成自己的上级。
// 更长的环（A→B→A）需要遍历上下级链，实践中极少见，
// 且不会造成功能故障（只是一处显示不对），不值得引入遍历成本。
func (s *UserService) validateSuperior(ctx context.Context, userID, superiorID uint) error {
	if superiorID == 0 {
		return nil
	}
	if superiorID == userID {
		return apperr.Newf(apperr.InvalidParam, "不能把自己设为直属上级")
	}
	if _, err := s.users.FindByID(ctx, superiorID); err != nil {
		return err
	}
	return nil
}

// checkUserUnreferenced 检查用户是否已被业务数据引用。
//
// 逐表检查而不是靠外键约束：三库对「外键 + 软删除」的行为不一致，
// 显式检查的结论更可预期，报错信息也更具体。
func (s *UserService) checkUserUnreferenced(ctx context.Context, userID uint) error {
	checks := []struct {
		table string
		query string
		desc  string
	}{
		{"pm_story", "opened_by = ? OR assigned_to = ?", "需求"},
		{"pm_task", "opened_by = ? OR assigned_to = ? OR finished_by = ?", "任务"},
		{"pm_bug", "opened_by = ? OR assigned_to = ? OR resolved_by = ?", "缺陷"},
		{"pm_effort", "user_id = ?", "工时记录"},
		{"pm_project", "opened_by = ? OR pm = ?", "项目"},
		{"pm_team", "user_id = ?", "项目团队"},
	}

	for _, c := range checks {
		// 三个占位符对应上面的三列；多余的位置参数会被驱动忽略，
		// 但为清晰起见按实际列数构造参数。
		argCount := countPlaceholders(c.query)
		args := make([]any, argCount)
		for i := range args {
			args[i] = userID
		}

		var count int64
		err := s.db.WithContext(ctx).Table(c.table).Where(c.query, args...).Count(&count).Error
		if err != nil {
			// 表尚未创建（如二阶段才启用的模块）不算错误。
			continue
		}
		if count > 0 {
			return apperr.Newf(apperr.UserHasReferences,
				"该用户已被 %s 等数据引用，无法删除，请改为停用", c.desc)
		}
	}
	return nil
}

// countPlaceholders 数出 SQL 里的 ? 个数。
func countPlaceholders(query string) int {
	n := 0
	for i := 0; i < len(query); i++ {
		if query[i] == '?' {
			n++
		}
	}
	return n
}

// ---- 辅助 ----

// toUserResp 把模型转为出参。
func toUserResp(u *model.User, deptNames map[uint]string, now time.Time, lockDuration time.Duration) dto.UserResp {
	return dto.UserResp{
		ID:         u.ID,
		Account:    u.Account,
		Realname:   u.Realname,
		Nickname:   u.Nickname,
		JobRole:    u.JobRole,
		Type:       u.Type,
		DeptID:     u.DeptID,
		DeptName:   deptNames[u.DeptID],
		Gender:     u.Gender,
		Email:      u.Email,
		Mobile:     u.Mobile,
		Phone:      u.Phone,
		Weixin:     u.Weixin,
		QQ:         u.QQ,
		Avatar:     u.Avatar,
		SuperiorID: u.Superior,
		Join:       formatTimePtr(u.Join),
		Last:       formatTimePtr(u.Last),
		Visits:     u.Visits,
		Disabled:   u.Disabled,
		Locked:     u.IsLockedAt(now, lockDuration),
	}
}

// setString 在指针非 nil 时写入字段（允许写入空串以表达「清空」）。
func setString(fields map[string]any, column string, value *string) {
	if value != nil {
		fields[column] = *value
	}
}

// defaultString 在值为空时返回默认值。
func defaultString(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

// currentUserID 从上下文取当前用户 id，未登录返回 0。
//
// 放在 service 包内作为薄封装：各 service 都要写「判断当前用户是不是
// 自己」这类逻辑，集中一处比每处都处理一遍 identity 的返回值更省事。
func currentUserID(ctx context.Context) uint {
	if u, ok := identity.CurrentUser(ctx); ok {
		return u.ID
	}
	return 0
}
