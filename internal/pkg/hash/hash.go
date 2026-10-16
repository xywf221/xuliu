// Package hash 负责口令哈希与令牌生成。
//
// 口令用 bcrypt（golang.org/x/crypto），不用禅道的 md5 —— md5 系列
// 已不应用于口令存储，加盐也救不了它的速度优势（对攻击者同样是优势）。
package hash

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"crypto/rand"

	"golang.org/x/crypto/bcrypt"
)

// bcryptMaxInput 是 bcrypt 算法的硬性输入上限。
//
// bcrypt 只取前 72 字节，超长口令的后半部分会被静默丢弃 ——
// 意味着两个前 72 字节相同的长口令可以互相登录。这是算法的性质，
// 不是实现缺陷，所以我们主动预哈希来绕过它。
const bcryptMaxInput = 72

// ErrMismatch 表示口令不匹配。
var ErrMismatch = errors.New("hash: 口令不匹配")

// Password 用 bcrypt 生成口令哈希。
//
// cost 取 10..31；12 在安全与登录延迟之间比较平衡
// （现代硬件上约 200~300ms，对暴力破解足够贵，对正常登录无感）。
func Password(plain string, cost int) (string, error) {
	if plain == "" {
		return "", errors.New("hash: 口令不能为空")
	}
	if cost < bcrypt.MinCost || cost > bcrypt.MaxCost {
		cost = 12
	}

	prepared, err := prepare(plain)
	if err != nil {
		return "", err
	}

	out, err := bcrypt.GenerateFromPassword([]byte(prepared), cost)
	if err != nil {
		return "", fmt.Errorf("hash: 生成口令哈希失败: %w", err)
	}
	return string(out), nil
}

// VerifyPassword 校验明文口令与哈希是否匹配。
//
// 返回 ErrMismatch 表示不匹配；其余错误表示哈希本身损坏。
// 调用方应把两种失败都当作登录失败处理，但日志里要能区分
// （哈希损坏是数据问题，需要运维介入）。
func VerifyPassword(hash, plain string) error {
	if hash == "" || plain == "" {
		return ErrMismatch
	}

	prepared, err := prepare(plain)
	if err != nil {
		return ErrMismatch
	}

	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(prepared)); err != nil {
		if errors.Is(err, bcrypt.ErrMismatchedHashAndPassword) {
			return ErrMismatch
		}
		return fmt.Errorf("hash: 校验口令失败: %w", err)
	}
	return nil
}

// prepare 在口令超过 bcrypt 上限时先做 sha256 预哈希。
//
// 预哈希结果是定长十六进制字符串（64 字节），必然落在上限内，
// 且保留了口令的全部熵。未超限时原样返回，避免无谓地改变既有行为
// （比如从禅道迁移过来的短口令）。
func prepare(plain string) (string, error) {
	if !utf8.ValidString(plain) {
		return "", errors.New("hash: 口令包含非法 UTF-8 字符")
	}
	if len(plain) <= bcryptMaxInput {
		return plain, nil
	}
	sum := sha256.Sum256([]byte(plain))
	return hex.EncodeToString(sum[:]), nil
}

// Token 生成一个密码学安全的随机令牌，用于口令重置等场景。
//
// crypto/rand.Text 是 Go 1.27 新增的，直接给出 ≥128 位熵的
// RFC 4648 base32 文本，不必自己拼 base64 再处理填充。
func Token() string { return rand.Text() }

// TokenHash 返回令牌的 sha256 十六进制值。
//
// 口令重置令牌在库里存哈希而不是原文：数据库泄露时，
// 攻击者拿到的是不可直接使用的摘要。
func TokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// SecureEqual 做常数时间比较，避免通过响应时间推断令牌。
func SecureEqual(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

// ValidatePasswordStrength 检查口令强度，返回面向用户的拒绝理由。
//
// 策略刻意保守（长度优先于复杂度）：强制大小写混合与特殊符号
// 会把用户逼向 "Passw0rd!" 这种可预测的形态，长度才是真正的门槛。
func ValidatePasswordStrength(plain string) error {
	// 按字符数而非字节数计长，否则一个中文字符会被算成 3 个。
	runeCount := utf8.RuneCountInString(plain)

	if runeCount < 8 {
		return errors.New("口令长度至少 8 位")
	}
	if runeCount > 128 {
		return errors.New("口令长度不能超过 128 位")
	}

	// 拒绝纯数字与纯字母，这两类在字典攻击下最脆弱。
	var hasDigit, hasLetter bool
	for _, r := range plain {
		switch {
		case r >= '0' && r <= '9':
			hasDigit = true
		case (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z'):
			hasLetter = true
		}
	}
	if !hasDigit || !hasLetter {
		return errors.New("口令需同时包含字母和数字")
	}

	// 拒绝长游程：连续出现同一字符超过 maxRuneRun 次。
	//
	// 判定「整串由同一字符组成」是不够的 —— "aaaaaaaa1" 看似有变化，
	// 实际熵依然极低。按游程判定能拦住它，同时不误伤 "aa11bb22"。
	if hasLongRun(plain) {
		return errors.New("口令中不能有某个字符连续重复 5 次以上")
	}

	// 拒绝常见弱口令。这份清单很短，只拦最典型的几个，
	// 不做完整字典匹配（那会让注册变得令人恼火且效果有限）。
	lower := strings.ToLower(plain)
	for _, weak := range []string{
		"password", "12345678", "qwerty", "abc12345",
		"admin123", "11111111", "passw0rd", "a1234567",
	} {
		if strings.Contains(lower, weak) {
			return errors.New("口令过于常见，请更换")
		}
	}
	return nil
}

// maxRuneRun 是允许的最长连续重复长度。
const maxRuneRun = 4

// hasLongRun 报告字符串里是否存在超过 maxRuneRun 的连续相同字符。
func hasLongRun(s string) bool {
	var prev rune
	run := 0
	for i, r := range s {
		if i > 0 && r == prev {
			run++
			if run > maxRuneRun {
				return true
			}
			continue
		}
		prev, run = r, 1
	}
	return false
}
