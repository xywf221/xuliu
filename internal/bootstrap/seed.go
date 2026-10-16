package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"time"

	"gorm.io/gorm"

	"github.com/xywf221/xuliu/internal/acl"
	"github.com/xywf221/xuliu/internal/config"
	"github.com/xywf221/xuliu/internal/model"
	"github.com/xywf221/xuliu/internal/pkg/hash"
)

// Seed 写入基础数据，让系统在空库状态下即可登录使用。
//
// 每一步都先检查「是否已存在」，因此重复启动是安全的。
// 判定依据是业务唯一键（账号、组名）而非 id，这样即使库被手工清理过也能自愈。
func Seed(ctx context.Context, gdb *gorm.DB, cfg *config.Config, logger *slog.Logger) error {
	if err := seedCompany(ctx, gdb, cfg, logger); err != nil {
		return err
	}
	if err := seedDepts(ctx, gdb, logger); err != nil {
		return err
	}
	if err := seedGroups(ctx, gdb, logger); err != nil {
		return err
	}
	if err := seedAdmin(ctx, gdb, cfg, logger); err != nil {
		return err
	}
	if err := seedConfigs(ctx, gdb, logger); err != nil {
		return err
	}
	return nil
}

// seedCompany 建立唯一的公司记录。
//
// 公司记录承载 Admins 字段（超管账号列表），是鉴权的关键依赖：
// 没有它，acl.Enforcer 就无从判断谁是超管，所有人都会被当成普通用户。
func seedCompany(ctx context.Context, gdb *gorm.DB, cfg *config.Config, logger *slog.Logger) error {
	var count int64
	if err := gdb.WithContext(ctx).Model(&model.Company{}).Count(&count).Error; err != nil {
		return fmt.Errorf("bootstrap: 查询公司信息失败: %w", err)
	}
	if count > 0 {
		return nil
	}

	adminAccount := cfg.App.Bootstrap.Account
	if adminAccount == "" {
		adminAccount = "admin"
	}

	company := &model.Company{
		Name:   "序流",
		Admins: adminAccount,
	}
	if err := gdb.WithContext(ctx).Create(company).Error; err != nil {
		return fmt.Errorf("bootstrap: 创建公司信息失败: %w", err)
	}
	logger.Info("已创建公司信息", slog.String("超管账号", adminAccount))
	return nil
}

// seedDepts 建立一个根部门。
//
// 用户表有 deptId 字段，给一个默认部门可以避免新建用户时
// 因「部门不存在」而失败。
func seedDepts(ctx context.Context, gdb *gorm.DB, logger *slog.Logger) error {
	var count int64
	if err := gdb.WithContext(ctx).Model(&model.Dept{}).Count(&count).Error; err != nil {
		return fmt.Errorf("bootstrap: 查询部门失败: %w", err)
	}
	if count > 0 {
		return nil
	}

	root := &model.Dept{Name: "序流科技", Grade: 1}
	if err := gdb.WithContext(ctx).Create(root).Error; err != nil {
		return fmt.Errorf("bootstrap: 创建根部门失败: %w", err)
	}
	// 物化路径：根节点的 path 就是它自己的 id，这样「取子孙」的
	// 前缀匹配（path LIKE '1,%'）也能覆盖到根节点自身之外的层级。
	path := fmt.Sprintf("%d", root.ID)
	if err := gdb.WithContext(ctx).Model(root).Update("path", path).Error; err != nil {
		return fmt.Errorf("bootstrap: 更新根部门路径失败: %w", err)
	}

	logger.Info("已创建根部门", slog.String("名称", root.Name))
	return nil
}

// seedGroups 按内置角色建立权限组。
//
// 这些组是「开箱可用」的起点：新建用户时直接勾选角色即可获得
// 一组合理的权限，而不是从零开始配置几十个权限点。
func seedGroups(ctx context.Context, gdb *gorm.DB, logger *slog.Logger) error {
	// 按组名排序，保证多次运行与不同环境下的创建顺序一致。
	names := make([]string, 0, len(acl.DefaultGroupPrivs))
	for name := range acl.DefaultGroupPrivs {
		names = append(names, name)
	}
	sort.Strings(names)

	created := 0
	for _, name := range names {
		privs := acl.DefaultGroupPrivs[name]

		var group model.Group
		err := gdb.WithContext(ctx).Where("name = ? AND project_id = 0", name).First(&group).Error
		switch {
		case err == nil:
			// 已存在：只补齐缺失的权限点，不删除已有的。
			// 这样管理员手工加过的权限不会被下次启动抹掉。
			if err := syncGroupPrivs(ctx, gdb, group.ID, privs); err != nil {
				return err
			}
			continue
		case !errors.Is(err, gorm.ErrRecordNotFound):
			return fmt.Errorf("bootstrap: 查询权限组 %s 失败: %w", name, err)
		}

		group = model.Group{Name: name, Desc: "内置角色组"}
		if err := gdb.WithContext(ctx).Create(&group).Error; err != nil {
			return fmt.Errorf("bootstrap: 创建权限组 %s 失败: %w", name, err)
		}
		if err := replaceGroupPrivs(ctx, gdb, group.ID, privs); err != nil {
			return err
		}
		created++
	}

	if created > 0 {
		logger.Info("已创建内置权限组", slog.Int("数量", created))
	}
	return nil
}

// replaceGroupPrivs 覆盖写入组的权限点。
func replaceGroupPrivs(ctx context.Context, gdb *gorm.DB, groupID uint, privs []acl.Priv) error {
	// 清洗：过滤掉未登记的权限点并去重，避免脏数据进库。
	clean := acl.FilterValid(privs)
	rows := make([]model.GroupPriv, 0, len(clean))
	for _, p := range clean {
		rows = append(rows, model.GroupPriv{GroupID: groupID, Perm: string(p)})
	}
	if len(rows) == 0 {
		return nil
	}
	if err := gdb.WithContext(ctx).Create(&rows).Error; err != nil {
		return fmt.Errorf("bootstrap: 写入权限点失败: %w", err)
	}
	return nil
}

// syncGroupPrivs 补齐缺失的权限点，不动已有的。
func syncGroupPrivs(ctx context.Context, gdb *gorm.DB, groupID uint, privs []acl.Priv) error {
	var existing []string
	if err := gdb.WithContext(ctx).Model(&model.GroupPriv{}).
		Where("group_id = ?", groupID).Pluck("perm", &existing).Error; err != nil {
		return fmt.Errorf("bootstrap: 查询已有权限点失败: %w", err)
	}

	have := make(map[string]struct{}, len(existing))
	for _, p := range existing {
		have[p] = struct{}{}
	}

	clean := acl.FilterValid(privs)
	missing := make([]model.GroupPriv, 0)
	for _, p := range clean {
		if _, ok := have[string(p)]; ok {
			continue
		}
		missing = append(missing, model.GroupPriv{GroupID: groupID, Perm: string(p)})
	}
	if len(missing) == 0 {
		return nil
	}
	if err := gdb.WithContext(ctx).Create(&missing).Error; err != nil {
		return fmt.Errorf("bootstrap: 补齐权限点失败: %w", err)
	}
	return nil
}

// seedAdmin 创建初始管理员账号。
//
// 仅在库里一个用户都没有时创建，所以：
//   - 首次启动即可登录；
//   - 后续启动不会覆盖管理员自己改过的密码。
//
// 生产环境若不带 bootstrap 配置，这里会跳过，由运维通过其他途径建号。
func seedAdmin(ctx context.Context, gdb *gorm.DB, cfg *config.Config, logger *slog.Logger) error {
	var count int64
	if err := gdb.WithContext(ctx).Model(&model.User{}).Count(&count).Error; err != nil {
		return fmt.Errorf("bootstrap: 查询用户失败: %w", err)
	}
	if count > 0 {
		return nil
	}

	bs := cfg.App.Bootstrap
	if !bs.Enabled {
		logger.Warn("未启用初始化账号，且库中没有任何用户，系统将无法登录",
			slog.String("提示", "设置 app.bootstrap.enabled=true 后重启，或通过其他途径创建账号"))
		return nil
	}

	account := bs.Account
	if account == "" {
		account = "admin"
	}
	password := bs.Password
	if password == "" {
		password = "admin123456"
	}
	realname := bs.Realname
	if realname == "" {
		realname = "系统管理员"
	}

	// 初始口令不做强度校验：它是运维显式配置的值，且首次登录后应当由
	// 用户自行修改。这里拒绝反而会让「配置里写了个短口令」变成启动失败，
	// 把简单问题复杂化。真正的防线是登录后的强制改密提示。
	encoded, err := hash.Password(password, cfg.Auth.BcryptCost)
	if err != nil {
		return fmt.Errorf("bootstrap: 哈希初始口令失败: %w", err)
	}

	now := time.Now().UTC()
	user := &model.User{
		Account:  account,
		Password: encoded,
		Realname: realname,
		Email:    bs.Email,
		Type:     model.UserTypeInside,
		JobRole:  model.JobRoleTop,
		Visits:   0,
		Join:     &now,
		Last:     nil,
	}

	// 关联到默认部门，避免后续新建数据时因部门为空而出问题。
	var rootDept model.Dept
	if err := gdb.WithContext(ctx).Order("id ASC").First(&rootDept).Error; err == nil {
		user.DeptID = rootDept.ID
	}
	var company model.Company
	if err := gdb.WithContext(ctx).First(&company).Error; err == nil {
		user.CompanyID = company.ID
	}

	if err := gdb.WithContext(ctx).Create(user).Error; err != nil {
		return fmt.Errorf("bootstrap: 创建初始管理员失败: %w", err)
	}

	// 超管通过 company.admins 获得全部权限，因此不必加入任何权限组。
	logger.Warn("已创建初始管理员，请登录后立即修改口令",
		slog.String("账号", account),
		slog.String("姓名", realname),
	)
	return nil
}

// seedConfigs 写入系统配置默认值。
func seedConfigs(ctx context.Context, gdb *gorm.DB, logger *slog.Logger) error {
	defaults := []model.Config{
		{Key: "system.name", Value: "序流", Desc: "系统名称"},
		{Key: "system.notice.interval", Value: "300", Desc: "通知轮询间隔（秒）"},
		{Key: "story.review.required", Value: "true", Desc: "需求是否需要评审"},
		{Key: "bug.default.severity", Value: "3", Desc: "缺陷默认严重程度"},
		{Key: "bug.default.pri", Value: "3", Desc: "缺陷默认优先级"},
		{Key: "task.default.pri", Value: "3", Desc: "任务默认优先级"},
	}

	created := 0
	for _, c := range defaults {
		var existing model.Config
		err := gdb.WithContext(ctx).Where("key = ?", c.Key).First(&existing).Error
		if err == nil {
			continue // 已存在，保留现值（可能已被管理员改过）
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return fmt.Errorf("bootstrap: 查询配置 %s 失败: %w", c.Key, err)
		}
		if err := gdb.WithContext(ctx).Create(&c).Error; err != nil {
			return fmt.Errorf("bootstrap: 写入配置 %s 失败: %w", c.Key, err)
		}
		created++
	}

	if created > 0 {
		logger.Info("已写入系统配置默认值", slog.Int("数量", created))
	}
	return nil
}
