package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/xywf221/xuliu/internal/acl"
	"github.com/xywf221/xuliu/internal/config"
	"github.com/xywf221/xuliu/internal/dto"
	"github.com/xywf221/xuliu/internal/identity"
	"github.com/xywf221/xuliu/internal/model"
	"github.com/xywf221/xuliu/internal/pkg/apperr"
	"github.com/xywf221/xuliu/internal/pkg/hash"
	"github.com/xywf221/xuliu/internal/pkg/token"
	"github.com/xywf221/xuliu/internal/repository"
)

// AuthService 负责登录、登出、令牌续期与口令维护。
type AuthService struct {
	cfg     *config.Config
	enforce *acl.Enforcer
	users   *repository.UserRepo
	groups  *repository.GroupRepo
	depts   *repository.DeptRepo
	tokens  *token.Manager
	// clock 可注入，便于测试账号锁定这类与时间相关的行为。
	clock func() time.Time
}

// NewAuthService 构造认证服务。
func NewAuthService(
	cfg *config.Config,
	enforce *acl.Enforcer,
	users *repository.UserRepo,
	groups *repository.GroupRepo,
	depts *repository.DeptRepo,
) (*AuthService, error) {
	tm, err := token.NewManager(token.Config{
		Secret:     cfg.Auth.JWTSecret,
		Issuer:     cfg.Auth.Issuer,
		AccessTTL:  cfg.Auth.AccessTTL,
		RefreshTTL: cfg.Auth.RefreshTTL,
	})
	if err != nil {
		return nil, err
	}

	return &AuthService{
		cfg:     cfg,
		enforce: enforce,
		users:   users,
		groups:  groups,
		depts:   depts,
		tokens:  tm,
		clock:   time.Now,
	}, nil
}

// Tokens 暴露令牌管理器，供中间件校验请求凭据。
func (s *AuthService) Tokens() *token.Manager { return s.tokens }

// now 返回当前时间。
func (s *AuthService) now() time.Time {
	if s.clock != nil {
		return s.clock()
	}
	return time.Now()
}

// Login 校验凭据并签发令牌。
//
// 防枚举：账号不存在与口令错误都返回同一个 LoginFailed，
// 不给出「用户不存在」这种能让攻击者逐个试出有效账号的提示。
//
// 防爆破：连续失败达到阈值即锁定一段时间，成功登录清零计数。
func (s *AuthService) Login(ctx context.Context, req dto.LoginReq) (*dto.LoginResp, error) {
	user, err := s.users.FindByAccount(ctx, req.Account)
	if err != nil {
		return nil, err
	}
	if user == nil {
		// 账号不存在时也要走一次哈希比对，让响应耗时与「口令错误」
		// 接近，否则可以靠响应时间差把有效账号枚举出来。
		_ = hash.VerifyPassword(dummyHash, req.Password)
		return nil, apperr.New(apperr.LoginFailed)
	}

	now := s.now()

	// 账号被管理员停用。
	if user.Disabled {
		return nil, apperr.New(apperr.AccountDisabled)
	}

	// 处于锁定期内直接拒绝，且不再累加计数（避免不断延长锁定）。
	if user.IsLockedAt(now, s.cfg.Auth.LockDuration) {
		return nil, apperr.New(apperr.AccountLocked)
	}

	if err := hash.VerifyPassword(user.Password, req.Password); err != nil {
		if errors.Is(err, hash.ErrMismatch) {
			s.recordLoginFailure(ctx, user)
			return nil, apperr.New(apperr.LoginFailed)
		}
		// 哈希本身损坏属于数据问题，需要运维介入，不能当成登录失败吞掉。
		return nil, apperr.Wrap(apperr.Internal, fmt.Errorf("校验口令时出错: %w", err))
	}

	// 登录成功：清零失败计数、记录时间与次数。
	if err := s.users.UpdateLoginSuccess(ctx, nil, user.ID, now); err != nil {
		// 统计字段写失败不该阻止登录，记日志即可。
		slog.WarnContext(ctx, "更新登录信息失败",
			slog.Uint64("userId", uint64(user.ID)), slog.Any("error", err))
	}

	profile, err := s.buildProfile(ctx, user)
	if err != nil {
		return nil, err
	}

	pair, err := s.tokens.Issue(user.ID, user.Account, profile.IsSuper)
	if err != nil {
		return nil, apperr.Wrap(apperr.Internal, err)
	}

	return &dto.LoginResp{
		AccessToken:  pair.AccessToken,
		RefreshToken: pair.RefreshToken,
		ExpiresIn:    pair.ExpiresIn,
		TokenType:    pair.TokenType,
		Profile:      profile,
	}, nil
}

// recordLoginFailure 累加失败次数，达到阈值时锁定账号。
func (s *AuthService) recordLoginFailure(ctx context.Context, user *model.User) {
	next := user.Fails + 1

	var lockedAt any
	if s.cfg.Auth.MaxLoginFailures > 0 && next >= s.cfg.Auth.MaxLoginFailures {
		lockedAt = s.now()
		slog.WarnContext(ctx, "账号因连续登录失败被锁定",
			slog.String("account", user.Account),
			slog.Int("失败次数", next),
			slog.Duration("锁定时长", s.cfg.Auth.LockDuration),
		)
	}

	if err := s.users.UpdateLoginFailure(ctx, user.ID, lockedAt); err != nil {
		slog.WarnContext(ctx, "记录登录失败次数失败",
			slog.Uint64("userId", uint64(user.ID)), slog.Any("error", err))
	}
}

// Refresh 用刷新令牌换取新的令牌对。
//
// 会重新读取用户与权限：刷新是低频操作，而权限变更需要尽快生效，
// 若直接沿用旧令牌里的信息，被移除权限的用户还能继续用满一个刷新周期。
func (s *AuthService) Refresh(ctx context.Context, refreshToken string) (*dto.LoginResp, error) {
	claims, err := s.tokens.VerifyRefresh(refreshToken)
	if err != nil {
		if errors.Is(err, token.ErrExpired) {
			return nil, apperr.Wrap(apperr.TokenExpired, err)
		}
		return nil, apperr.Wrap(apperr.TokenInvalid, err)
	}

	user, err := s.users.FindByID(ctx, claims.UserID)
	if err != nil {
		return nil, err
	}
	if user == nil {
		// 用户已被删除，令牌自然失效。
		return nil, apperr.New(apperr.TokenRevoked)
	}
	if user.Disabled {
		return nil, apperr.New(apperr.AccountDisabled)
	}

	profile, err := s.buildProfile(ctx, user)
	if err != nil {
		return nil, err
	}

	pair, err := s.tokens.Issue(user.ID, user.Account, profile.IsSuper)
	if err != nil {
		return nil, apperr.Wrap(apperr.Internal, err)
	}

	return &dto.LoginResp{
		AccessToken:  pair.AccessToken,
		RefreshToken: pair.RefreshToken,
		ExpiresIn:    pair.ExpiresIn,
		TokenType:    pair.TokenType,
		Profile:      profile,
	}, nil
}

// Profile 返回指定用户的信息与权限。
func (s *AuthService) Profile(ctx context.Context, userID uint) (*dto.ProfileResp, error) {
	user, err := s.users.GetByID(ctx, userID)
	if err != nil {
		return nil, err
	}
	return s.buildProfile(ctx, user)
}

// Authenticate 校验访问令牌并组装请求上下文所需的用户身份。
//
// 由鉴权中间件在每个请求上调用。这里**每次都查一次库**，
// 而不是完全信任令牌内容：
//
//   - 权限点无法放进令牌（否则改权限要等令牌过期才生效）；
//   - 用户可能已被停用或删除，而令牌仍在有效期内。
//
// 代价是每请求一次查询；对这类管理平台的流量规模完全可接受，
// 换来的是「停用账号立即生效」这一必要的安全性质。
func (s *AuthService) Authenticate(ctx context.Context, accessToken string) (*identity.User, error) {
	claims, err := s.tokens.VerifyAccess(accessToken)
	if err != nil {
		if errors.Is(err, token.ErrExpired) {
			return nil, apperr.Wrap(apperr.TokenExpired, err)
		}
		return nil, apperr.Wrap(apperr.TokenInvalid, err)
	}

	user, err := s.users.FindByID(ctx, claims.UserID)
	if err != nil {
		return nil, err
	}
	if user == nil {
		return nil, apperr.New(apperr.TokenRevoked)
	}
	if user.Disabled {
		return nil, apperr.New(apperr.AccountDisabled)
	}

	return s.enforce.LoadUser(ctx, user)
}

// ChangePassword 修改本人口令。
func (s *AuthService) ChangePassword(ctx context.Context, userID uint, req dto.ChangePasswordReq) error {
	user, err := s.users.GetByID(ctx, userID)
	if err != nil {
		return err
	}

	if err := hash.VerifyPassword(user.Password, req.OldPassword); err != nil {
		if errors.Is(err, hash.ErrMismatch) {
			return apperr.New(apperr.OldPasswordWrong)
		}
		return apperr.Wrap(apperr.Internal, err)
	}

	if err := hash.ValidatePasswordStrength(req.NewPassword); err != nil {
		return apperr.Wrapf(apperr.InvalidParam, err, "%s", err.Error())
	}

	// 新旧口令相同没有意义，且容易让用户误以为改了。
	if req.OldPassword == req.NewPassword {
		return apperr.Newf(apperr.InvalidParam, "新口令不能与原口令相同")
	}

	encoded, err := hash.Password(req.NewPassword, s.cfg.Auth.BcryptCost)
	if err != nil {
		return apperr.Wrap(apperr.Internal, err)
	}

	return s.users.UpdateFields(ctx, nil, userID, map[string]any{"password": encoded})
}

// ResetPassword 由管理员重置他人口令。
//
// 不做原口令校验（管理员本就不该知道用户口令），但会记录警告日志 ——
// 这是一个高权限操作，应当留下痕迹。
func (s *AuthService) ResetPassword(ctx context.Context, targetUserID uint, newPassword string, operatorID uint) error {
	if err := hash.ValidatePasswordStrength(newPassword); err != nil {
		return apperr.Wrapf(apperr.InvalidParam, err, "%s", err.Error())
	}

	user, err := s.users.FindByID(ctx, targetUserID)
	if err != nil {
		return err
	}
	if user == nil {
		return apperr.New(apperr.UserNotFound)
	}

	encoded, err := hash.Password(newPassword, s.cfg.Auth.BcryptCost)
	if err != nil {
		return apperr.Wrap(apperr.Internal, err)
	}

	// 重置口令同时解锁：管理员重置的常见场景正是「用户把自己锁住了」。
	if err := s.users.UpdateFields(ctx, nil, targetUserID, map[string]any{
		"password": encoded,
		"fails":    0,
		"locked":   nil,
	}); err != nil {
		return err
	}

	slog.WarnContext(ctx, "管理员重置了用户口令",
		slog.Uint64("操作者", uint64(operatorID)),
		slog.Uint64("目标用户", uint64(targetUserID)),
		slog.String("账号", user.Account),
	)
	return nil
}

// buildProfile 组装用户信息与权限。
func (s *AuthService) buildProfile(ctx context.Context, user *model.User) (*dto.ProfileResp, error) {
	ident, err := s.enforce.LoadUser(ctx, user)
	if err != nil {
		return nil, err
	}

	// 超管不必物化全部权限点（Can 对它恒真），但前端要据此渲染菜单，
	// 所以这里补全为「全部权限点」，让界面与后端行为一致 ——
	// 否则超管会看到一堆被隐藏的菜单，与它实际能做的事不符。
	rights := make([]string, 0, len(ident.Rights))
	if ident.IsSuper {
		for _, m := range acl.All() {
			rights = append(rights, string(m.Priv))
		}
	} else {
		for p := range ident.Rights {
			rights = append(rights, p)
		}
	}
	sortStrings(rights)

	profile := &dto.ProfileResp{
		ID:          user.ID,
		Account:     user.Account,
		Realname:    user.Realname,
		Email:       user.Email,
		Mobile:      user.Mobile,
		Avatar:      user.Avatar,
		JobRole:     user.JobRole,
		DeptID:      user.DeptID,
		IsSuper:     ident.IsSuper,
		Rights:      rights,
		DisplayName: user.DisplayName(),
		Visits:      user.Visits,
		Last:        formatTimePtr(user.Last),
	}

	// 部门名与组名用于界面展示，取不到不算错误。
	if dept, err := s.depts.FindByID(ctx, user.DeptID); err == nil && dept != nil {
		profile.DeptName = dept.Name
	}
	if len(ident.GroupIDs) > 0 {
		names := make([]string, 0, len(ident.GroupIDs))
		for _, gid := range ident.GroupIDs {
			g, err := s.groups.FindByID(ctx, gid)
			if err != nil || g == nil {
				continue
			}
			names = append(names, g.Name)
		}
		sortStrings(names)
		profile.Groups = names
	}

	return profile, nil
}

// dummyHash 是一个合法的 bcrypt 哈希，用于「账号不存在」时消耗等量时间。
//
// 明文为 "xuliu-dummy-password-for-timing-equalization"，
// cost 与生产配置无关 —— 它的唯一用途是让校验路径不提前返回。
const dummyHash = "$2a$10$N9qo8uLOickgx2ZMRZoMyeIjZAgcfl7p92ldGxad68LJZdL17lhWy"

// formatTimePtr 把时间格式化为 RFC3339 字符串，nil 返回 nil。
func formatTimePtr(t *time.Time) *string {
	if t == nil || t.IsZero() {
		return nil
	}
	out := t.UTC().Format(time.RFC3339)
	return &out
}

// sortStrings 就地排序字符串切片。
//
// 用插入排序而非 sort.Strings：权限点数量在百级，插入排序足够快，
// 且省掉一次 sort 包的依赖引入（本文件已有足够多依赖）。
func sortStrings(items []string) {
	for i := 1; i < len(items); i++ {
		for j := i; j > 0 && items[j] < items[j-1]; j-- {
			items[j], items[j-1] = items[j-1], items[j]
		}
	}
}
