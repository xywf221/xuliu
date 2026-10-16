package hash_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/xywf221/xuliu/internal/pkg/hash"
)

// TestPasswordRoundTrip 验证基本的口令哈希与校验。
func TestPasswordRoundTrip(t *testing.T) {
	// 用例里用最低 cost，否则每次哈希要 200ms，测试会明显变慢。
	// cost 只影响耗时，不影响正确性。
	const cost = 4

	encoded, err := hash.Password("correct-horse-1", cost)
	if err != nil {
		t.Fatalf("生成哈希失败: %v", err)
	}
	if encoded == "correct-horse-1" {
		t.Fatal("哈希结果不应等于明文")
	}

	if err := hash.VerifyPassword(encoded, "correct-horse-1"); err != nil {
		t.Errorf("正确口令应校验通过，实际: %v", err)
	}
	if err := hash.VerifyPassword(encoded, "wrong-horse-1"); !errors.Is(err, hash.ErrMismatch) {
		t.Errorf("错误口令应返回 ErrMismatch，实际: %v", err)
	}
}

// TestSamePasswordDifferentHash 验证每次哈希结果都不同（bcrypt 自带随机盐）。
func TestSamePasswordDifferentHash(t *testing.T) {
	const cost = 4
	a, err := hash.Password("same-password-1", cost)
	if err != nil {
		t.Fatalf("生成哈希失败: %v", err)
	}
	b, err := hash.Password("same-password-1", cost)
	if err != nil {
		t.Fatalf("生成哈希失败: %v", err)
	}
	if a == b {
		t.Error("相同口令两次哈希应因随机盐而不同")
	}
	// 但两者都必须能校验通过。
	if err := hash.VerifyPassword(a, "same-password-1"); err != nil {
		t.Errorf("第一个哈希应校验通过: %v", err)
	}
	if err := hash.VerifyPassword(b, "same-password-1"); err != nil {
		t.Errorf("第二个哈希应校验通过: %v", err)
	}
}

// TestLongPasswordNotTruncated 是本文件最重要的用例。
//
// bcrypt 只使用前 72 字节，若不预哈希，下面这两个只在前 72 字节之后
// 有差异的口令会互相通过校验 —— 这是一个真实的认证绕过隐患。
func TestLongPasswordNotTruncated(t *testing.T) {
	const cost = 4

	// 构造两个前 72 字节完全相同、之后不同的口令。
	base := strings.Repeat("a", 72)
	passA := base + "AAAA"
	passB := base + "BBBB"

	encoded, err := hash.Password(passA, cost)
	if err != nil {
		t.Fatalf("生成哈希失败: %v", err)
	}

	if err := hash.VerifyPassword(encoded, passA); err != nil {
		t.Errorf("原口令应校验通过: %v", err)
	}
	if err := hash.VerifyPassword(encoded, passB); !errors.Is(err, hash.ErrMismatch) {
		t.Errorf("前 72 字节相同但后缀不同的口令不应通过校验（否则存在绕过），实际: %v", err)
	}
}

// TestVerifyRejectsEmpty 验证空输入一律失败，且不 panic。
func TestVerifyRejectsEmpty(t *testing.T) {
	if err := hash.VerifyPassword("", "x"); !errors.Is(err, hash.ErrMismatch) {
		t.Error("空哈希应校验失败")
	}
	if err := hash.VerifyPassword("x", ""); !errors.Is(err, hash.ErrMismatch) {
		t.Error("空口令应校验失败")
	}
}

// TestPasswordStrength 覆盖口令强度策略的各个分支。
func TestPasswordStrength(t *testing.T) {
	// 应被接受的口令。
	for _, ok := range []string{
		"xuliu2024",
		"mySecure2024",
		"中文口令abc123",
		"aa11bb22", // 有重复但未超长游程，不应被误伤
	} {
		if err := hash.ValidatePasswordStrength(ok); err != nil {
			t.Errorf("口令 %q 应被接受，实际: %v", ok, err)
		}
	}

	// 应被拒绝的口令及其原因关键词。
	cases := map[string]string{
		"abc123":                       "长度",    // 太短
		"abcdefgh":                     "字母和数字", // 无数字
		"12345678":                     "字母和数字", // 无字母
		"aaaaaaaa":                     "字母和数字", // 无数字
		"aaaaaaaa1":                    "连续重复",  // 长游程，熵极低
		"password123":                  "常见",    // 弱口令
		strings.Repeat("a", 200) + "1": "128",   // 过长
	}
	for pw, wantSubstr := range cases {
		err := hash.ValidatePasswordStrength(pw)
		if err == nil {
			t.Errorf("口令 %q 应被拒绝", pw)
			continue
		}
		if !strings.Contains(err.Error(), wantSubstr) {
			t.Errorf("口令 %q 的拒绝理由应包含 %q，实际: %v", pw, wantSubstr, err)
		}
	}
}

// TestTokenAndHash 验证令牌生成与其哈希的确定性。
func TestTokenAndHash(t *testing.T) {
	a := hash.Token()
	b := hash.Token()
	if a == b {
		t.Error("两次生成的令牌不应相同")
	}
	if len(a) < 20 {
		t.Errorf("令牌长度过短，可能熵不足：%q", a)
	}

	// 同一令牌的哈希必须稳定，否则重置流程无法比对。
	if hash.TokenHash(a) != hash.TokenHash(a) {
		t.Error("同一令牌的哈希应稳定")
	}
	if hash.TokenHash(a) == hash.TokenHash(b) {
		t.Error("不同令牌的哈希不应相同")
	}
	// 哈希不应包含原文。
	if strings.Contains(hash.TokenHash(a), a) {
		t.Error("哈希结果不应包含令牌原文")
	}
}

// TestSecureEqual 验证常数时间比较的行为。
func TestSecureEqual(t *testing.T) {
	if !hash.SecureEqual("abc", "abc") {
		t.Error("相同字符串应相等")
	}
	if hash.SecureEqual("abc", "abd") {
		t.Error("不同字符串不应相等")
	}
	if hash.SecureEqual("abc", "abcd") {
		t.Error("长度不同的字符串不应相等")
	}
	if !hash.SecureEqual("", "") {
		t.Error("两个空字符串应相等")
	}
}
