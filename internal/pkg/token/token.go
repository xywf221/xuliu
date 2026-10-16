// Package token 负责签发与校验登录凭据（JWT）。
//
// 双令牌设计：
//
//	access  短期（默认 2h），放在 Authorization 头里，用于每次请求鉴权；
//	refresh 长期（默认 7d），只用于换取新的 access，避免长期凭据频繁传输。
//
// 两者用不同的 audience 区分，**refresh 令牌不能当 access 使用** ——
// 否则短期令牌的意义就没了。校验时会显式检查 this。
package token

import (
	"errors"
	"fmt"
	"strconv"
	"time"

	"uuid"

	"github.com/golang-jwt/jwt/v5"
)

// 令牌用途，写入 JWT 的 audience 字段。
const (
	AudienceAccess  = "xuliu:access"
	AudienceRefresh = "xuliu:refresh"
)

// 校验错误。
var (
	// ErrExpired 令牌已过期。
	ErrExpired = errors.New("token: 令牌已过期")
	// ErrInvalid 令牌无效（签名不符、格式错误、用途不匹配等）。
	ErrInvalid = errors.New("token: 令牌无效")
)

// Claims 是令牌载荷。
//
// 只放鉴权必需的字段（用户 id、账号、令牌用途）。
// **不放权限点**：权限点在登录时载入会话，若塞进 JWT 则每次改权限
// 都要等令牌过期才生效，而权限变更恰恰需要立即生效。
type Claims struct {
	UserID  uint   `json:"uid"`
	Account string `json:"acc"`
	// IsSuper 冗余标记超管，省掉每次请求查公司配置。
	// 撤销超管权限后需等令牌过期才生效，这是可接受的取舍：
	// 超管变更极低频，而令牌有效期只有 2 小时。
	IsSuper bool `json:"sup,omitempty"`
	// TokenID 是令牌唯一标识，为将来做「主动失效某令牌」预留。
	TokenID string `json:"jti"`
	// Purpose 与 audience 冗余互证，便于排查误用。
	Purpose string `json:"pur"`

	jwt.RegisteredClaims
}

// Config 是签发参数。
type Config struct {
	Secret     string
	Issuer     string
	AccessTTL  time.Duration
	RefreshTTL time.Duration

	// Now 是可注入的时钟。
	//
	// 生产代码应留空（默认 time.Now）。它的用途是让「令牌过期」
	// 这类与时间强相关的行为可被确定性地测试，而不必靠「传负数 TTL」
	// 这类会绕过参数校验的技巧。
	Now func() time.Time
}

// Manager 签发与校验令牌。
type Manager struct {
	cfg Config
}

// NewManager 构造令牌管理器。
func NewManager(cfg Config) (*Manager, error) {
	if cfg.Secret == "" {
		return nil, errors.New("token: 签名密钥不能为空")
	}
	if cfg.Issuer == "" {
		cfg.Issuer = "xuliu"
	}
	// 0 表示「未设置」，填默认值；负数表示明确的配置错误，直接拒绝。
	// 把负数也填成默认值会掩盖配置问题（比如误写成 -1h 却以为生效了）。
	if cfg.AccessTTL == 0 {
		cfg.AccessTTL = 2 * time.Hour
	}
	if cfg.RefreshTTL == 0 {
		cfg.RefreshTTL = 7 * 24 * time.Hour
	}
	if cfg.AccessTTL < 0 || cfg.RefreshTTL < 0 {
		return nil, errors.New("token: 有效期不能为负数")
	}
	// 刷新令牌比访问令牌短没有意义：会在换新之前就失效。
	if cfg.RefreshTTL < cfg.AccessTTL {
		return nil, errors.New("token: 刷新令牌有效期不应短于访问令牌")
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &Manager{cfg: cfg}, nil
}

// Pair 是一对签发给客户端的令牌。
type Pair struct {
	AccessToken  string `json:"accessToken"`
	RefreshToken string `json:"refreshToken"`
	// ExpiresIn 是 access 令牌的剩余有效秒数，供前端做静默续期。
	ExpiresIn int64 `json:"expiresIn"`
	// TokenType 固定为 Bearer。
	TokenType string `json:"tokenType"`
}

// Issue 为用户签发一对令牌。
func (m *Manager) Issue(userID uint, account string, isSuper bool) (*Pair, error) {
	access, err := m.sign(userID, account, isSuper, AudienceAccess, m.cfg.AccessTTL)
	if err != nil {
		return nil, err
	}
	refresh, err := m.sign(userID, account, isSuper, AudienceRefresh, m.cfg.RefreshTTL)
	if err != nil {
		return nil, err
	}
	return &Pair{
		AccessToken:  access,
		RefreshToken: refresh,
		ExpiresIn:    int64(m.cfg.AccessTTL.Seconds()),
		TokenType:    "Bearer",
	}, nil
}

// sign 生成一个令牌。
func (m *Manager) sign(userID uint, account string, isSuper bool, audience string, ttl time.Duration) (string, error) {
	now := m.cfg.Now()
	claims := Claims{
		UserID:  userID,
		Account: account,
		IsSuper: isSuper,
		TokenID: uuid.NewV7().String(),
		Purpose: audience,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    m.cfg.Issuer,
			Subject:   strconv.FormatUint(uint64(userID), 10),
			Audience:  jwt.ClaimStrings{audience},
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(ttl)),
		},
	}

	signed, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(m.cfg.Secret))
	if err != nil {
		return "", fmt.Errorf("token: 签发令牌失败: %w", err)
	}
	return signed, nil
}

// VerifyAccess 校验访问令牌。
func (m *Manager) VerifyAccess(raw string) (*Claims, error) {
	return m.verify(raw, AudienceAccess)
}

// VerifyRefresh 校验刷新令牌。
func (m *Manager) VerifyRefresh(raw string) (*Claims, error) {
	return m.verify(raw, AudienceRefresh)
}

// verify 校验签名、有效期与用途。
func (m *Manager) verify(raw, audience string) (*Claims, error) {
	if raw == "" {
		return nil, ErrInvalid
	}

	claims := &Claims{}
	parsed, err := jwt.ParseWithClaims(raw, claims, func(t *jwt.Token) (any, error) {
		// 显式锁定算法：不检查的话，攻击者可以把 alg 改成 none 或换成
		// 非对称算法来伪造令牌（经典的算法混淆攻击）。
		if t.Method.Alg() != jwt.SigningMethodHS256.Alg() {
			return nil, fmt.Errorf("token: 不接受的签名算法 %s", t.Method.Alg())
		}
		return []byte(m.cfg.Secret), nil
	},
		jwt.WithIssuer(m.cfg.Issuer),
		jwt.WithAudience(audience),
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
	)
	if err != nil {
		// 过期与无效要分开：前者前端应静默去续期，后者应踢回登录页。
		if errors.Is(err, jwt.ErrTokenExpired) {
			return nil, ErrExpired
		}
		return nil, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	if !parsed.Valid {
		return nil, ErrInvalid
	}
	// audience 已由 WithAudience 校验，这里再核一次冗余字段，
	// 防止将来有人误改签发逻辑导致两种用途混用。
	if claims.Purpose != audience {
		return nil, fmt.Errorf("%w: 令牌用途不符（期望 %s，实际 %s）",
			ErrInvalid, audience, claims.Purpose)
	}
	if claims.UserID == 0 {
		return nil, fmt.Errorf("%w: 缺少用户标识", ErrInvalid)
	}
	return claims, nil
}

// AccessTTL 返回访问令牌有效期。
func (m *Manager) AccessTTL() time.Duration { return m.cfg.AccessTTL }

// RefreshTTL 返回刷新令牌有效期。
func (m *Manager) RefreshTTL() time.Duration { return m.cfg.RefreshTTL }
