package token_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/xywf221/xuliu/internal/pkg/token"
)

func newManager(t *testing.T, accessTTL, refreshTTL time.Duration) *token.Manager {
	t.Helper()
	m, err := token.NewManager(token.Config{
		Secret:     "test-secret-for-unit-tests-only",
		Issuer:     "xuliu-test",
		AccessTTL:  accessTTL,
		RefreshTTL: refreshTTL,
	})
	if err != nil {
		t.Fatalf("构造管理器失败: %v", err)
	}
	return m
}

// TestIssueAndVerify 覆盖签发与校验的正常路径。
func TestIssueAndVerify(t *testing.T) {
	m := newManager(t, time.Hour, 24*time.Hour)

	pair, err := m.Issue(42, "zhangsan", false)
	if err != nil {
		t.Fatalf("签发失败: %v", err)
	}
	if pair.AccessToken == "" || pair.RefreshToken == "" {
		t.Fatal("两种令牌都不应为空")
	}
	if pair.AccessToken == pair.RefreshToken {
		t.Fatal("访问令牌与刷新令牌不应相同")
	}
	if pair.TokenType != "Bearer" {
		t.Errorf("令牌类型应为 Bearer，实际 %q", pair.TokenType)
	}
	if pair.ExpiresIn != 3600 {
		t.Errorf("过期秒数应为 3600，实际 %d", pair.ExpiresIn)
	}

	claims, err := m.VerifyAccess(pair.AccessToken)
	if err != nil {
		t.Fatalf("校验访问令牌失败: %v", err)
	}
	if claims.UserID != 42 {
		t.Errorf("用户 ID 应为 42，实际 %d", claims.UserID)
	}
	if claims.Account != "zhangsan" {
		t.Errorf("账号应为 zhangsan，实际 %q", claims.Account)
	}

	if _, err := m.VerifyRefresh(pair.RefreshToken); err != nil {
		t.Errorf("校验刷新令牌失败: %v", err)
	}
}

// TestRefreshTokenNotUsableAsAccess 是本包最关键的安全断言。
//
// 若刷新令牌能当访问令牌用，短期访问令牌的设计就完全失效了 ——
// 攻击者拿到一次 refresh 就等于拿到长期有效的通行证。
func TestRefreshTokenNotUsableAsAccess(t *testing.T) {
	m := newManager(t, time.Hour, 24*time.Hour)
	pair, err := m.Issue(1, "alice", false)
	if err != nil {
		t.Fatalf("签发失败: %v", err)
	}

	if _, err := m.VerifyAccess(pair.RefreshToken); err == nil {
		t.Fatal("刷新令牌不应能通过访问令牌校验")
	}

	// 反向也要拦：访问令牌不能用来续期。
	if _, err := m.VerifyRefresh(pair.AccessToken); err == nil {
		t.Fatal("访问令牌不应能通过刷新令牌校验")
	}
}

// TestExpiredToken 验证过期令牌返回 ErrExpired 而非笼统的 ErrInvalid。
//
// 这个区分对前端很重要：过期应静默续期，无效应踢回登录页。
//
// 用注入的时钟把签发时间推到过去，而不是传负数 TTL ——
// 后者会被参数校验拦下，测不到真正想测的路径。
func TestExpiredToken(t *testing.T) {
	past := time.Now().Add(-48 * time.Hour)
	m, err := token.NewManager(token.Config{
		Secret:     "test-secret-for-unit-tests-only",
		Issuer:     "xuliu-test",
		AccessTTL:  time.Hour,
		RefreshTTL: 24 * time.Hour,
		Now:        func() time.Time { return past },
	})
	if err != nil {
		t.Fatalf("构造管理器失败: %v", err)
	}

	pair, err := m.Issue(1, "alice", false)
	if err != nil {
		t.Fatalf("签发失败: %v", err)
	}

	if _, err := m.VerifyAccess(pair.AccessToken); !errors.Is(err, token.ErrExpired) {
		t.Errorf("过期令牌应返回 ErrExpired，实际: %v", err)
	}
}

// TestWrongSecret 验证换密钥后旧令牌失效。
func TestWrongSecret(t *testing.T) {
	m1 := newManager(t, time.Hour, 24*time.Hour)
	pair, err := m1.Issue(1, "alice", false)
	if err != nil {
		t.Fatalf("签发失败: %v", err)
	}

	m2, err := token.NewManager(token.Config{
		Secret: "a-completely-different-secret",
		Issuer: "xuliu-test",
	})
	if err != nil {
		t.Fatalf("构造管理器失败: %v", err)
	}
	if _, err := m2.VerifyAccess(pair.AccessToken); err == nil {
		t.Fatal("用不同密钥签发的令牌不应通过校验")
	}
}

// TestWrongIssuer 验证签发方不匹配时拒绝。
func TestWrongIssuer(t *testing.T) {
	m, err := token.NewManager(token.Config{
		Secret: "s", Issuer: "issuer-a", AccessTTL: time.Hour, RefreshTTL: 2 * time.Hour,
	})
	if err != nil {
		t.Fatalf("构造管理器失败: %v", err)
	}
	pair, err := m.Issue(1, "alice", false)
	if err != nil {
		t.Fatalf("签发失败: %v", err)
	}

	other, err := token.NewManager(token.Config{
		Secret: "s", Issuer: "issuer-b", AccessTTL: time.Hour, RefreshTTL: 2 * time.Hour,
	})
	if err != nil {
		t.Fatalf("构造管理器失败: %v", err)
	}
	if _, err := other.VerifyAccess(pair.AccessToken); err == nil {
		t.Fatal("签发方不匹配的令牌不应通过校验")
	}
}

// TestRejectsAlgNone 验证经典的算法混淆攻击被挡住。
//
// 做法：手工构造 alg=none 的令牌。若校验时接受了这个算法，
// 任何人都能自己签发令牌。
func TestRejectsAlgNone(t *testing.T) {
	m := newManager(t, time.Hour, 24*time.Hour)

	claims := jwt.MapClaims{
		"iss": "xuliu-test",
		"aud": []string{token.AudienceAccess},
		"uid": float64(1),
		"acc": "attacker",
		"pur": token.AudienceAccess,
		"exp": time.Now().Add(time.Hour).Unix(),
		"iat": time.Now().Unix(),
		"nbf": time.Now().Unix(),
		"sub": "1",
	}
	// jwt 库在 SigningMethodNone 下需要 UnsafeAllowNoneSignatureType。
	forged, err := jwt.NewWithClaims(jwt.SigningMethodNone, claims).
		SignedString(jwt.UnsafeAllowNoneSignatureType)
	if err != nil {
		t.Fatalf("构造伪造令牌失败: %v", err)
	}

	if _, err := m.VerifyAccess(forged); err == nil {
		t.Fatal("alg=none 的伪造令牌不应通过校验")
	}
}

// TestRejectsTamperedPayload 验证篡改载荷会导致签名校验失败。
func TestRejectsTamperedPayload(t *testing.T) {
	m := newManager(t, time.Hour, 24*time.Hour)
	pair, err := m.Issue(1, "alice", false)
	if err != nil {
		t.Fatalf("签发失败: %v", err)
	}

	// 把载荷段整体换掉，签名就不再匹配。
	parts := strings.Split(pair.AccessToken, ".")
	if len(parts) != 3 {
		t.Fatalf("JWT 应为三段，实际 %d 段", len(parts))
	}
	tampered := parts[0] + "." + parts[1][:len(parts[1])-2] + "XY." + parts[2]

	if _, err := m.VerifyAccess(tampered); err == nil {
		t.Fatal("被篡改的令牌不应通过校验")
	}
}

// TestRejectsEmptyAndGarbage 验证异常输入不会 panic。
func TestRejectsEmptyAndGarbage(t *testing.T) {
	m := newManager(t, time.Hour, 24*time.Hour)

	for _, bad := range []string{
		"",
		"not-a-token",
		"a.b.c",
		"eyJhbGciOiJIUzI1NiJ9.",
		strings.Repeat("x", 5000),
	} {
		if _, err := m.VerifyAccess(bad); err == nil {
			t.Errorf("输入 %q 不应通过校验", bad)
		}
	}
}

// TestConfigValidation 覆盖构造时的参数校验。
func TestConfigValidation(t *testing.T) {
	if _, err := token.NewManager(token.Config{Secret: ""}); err == nil {
		t.Error("空密钥应被拒绝")
	}

	// 刷新令牌短于访问令牌没有意义，应被拒绝。
	if _, err := token.NewManager(token.Config{
		Secret: "s", AccessTTL: 24 * time.Hour, RefreshTTL: time.Hour,
	}); err == nil {
		t.Error("刷新令牌有效期短于访问令牌时应被拒绝")
	}

	// 缺省 TTL 应被补齐，而不是留成 0。
	m, err := token.NewManager(token.Config{Secret: "s"})
	if err != nil {
		t.Fatalf("应能补齐缺省 TTL: %v", err)
	}
	if m.AccessTTL() <= 0 || m.RefreshTTL() <= 0 {
		t.Error("缺省 TTL 应被补齐为正值")
	}
}

// TestSuperFlagRoundTrip 验证超管标记能正确往返。
func TestSuperFlagRoundTrip(t *testing.T) {
	m := newManager(t, time.Hour, 24*time.Hour)

	adminPair, err := m.Issue(1, "admin", true)
	if err != nil {
		t.Fatalf("签发失败: %v", err)
	}
	claims, err := m.VerifyAccess(adminPair.AccessToken)
	if err != nil {
		t.Fatalf("校验失败: %v", err)
	}
	if !claims.IsSuper {
		t.Error("超管标记应保留在令牌里")
	}

	// 普通用户不应带上该标记。
	userPair, err := m.Issue(2, "bob", false)
	if err != nil {
		t.Fatalf("签发失败: %v", err)
	}
	claims, err = m.VerifyAccess(userPair.AccessToken)
	if err != nil {
		t.Fatalf("校验失败: %v", err)
	}
	if claims.IsSuper {
		t.Error("普通用户不应带超管标记")
	}
}
