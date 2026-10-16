// Package apperr 定义全站统一的应用错误。
//
// 设计要点：
//
//   - **错误码稳定、文案可变**。对外只暴露 Code（如 story_not_found），
//     文案由本包的文案目录解析。将来要做多语言时，只需替换文案目录，
//     业务代码里散落的 apperr.New(...) 一行都不用改。
//   - **内部原因不外泄**。cause 只进日志，不发给客户端，避免把 SQL、
//     文件路径之类的东西漏给前端。
//   - HTTP 状态由码表统一决定，handler 层不再逐个 case 判断。
package apperr

import (
	"errors"
	"fmt"
	"net/http"
)

// Code 是稳定的错误码标识。对外 API 契约的一部分，改动等同破坏性变更。
type Code string

// Error 是携带错误码的应用错误。
type Error struct {
	code  Code
	msg   string // 非空时覆盖码表文案
	cause error  // 内部原因，仅进日志
}

// Error 实现 error。刻意把 cause 拼进来，方便日志与调试。
func (e *Error) Error() string {
	if e.cause != nil {
		return fmt.Sprintf("%s: %v", e.code, e.cause)
	}
	return string(e.code)
}

// Unwrap 支持 errors.Is / errors.As 沿链查找。
func (e *Error) Unwrap() error { return e.cause }

// Code 返回稳定错误码。
func (e *Error) Code() Code { return e.code }

// Status 返回该错误应映射的 HTTP 状态码。
func (e *Error) Status() int { return StatusOf(e.code) }

// Message 返回面向用户的文案：优先用覆盖文案，否则查码表。
func (e *Error) Message() string {
	if e.msg != "" {
		return e.msg
	}
	return MessageOf(e.code)
}

// New 用错误码构造错误。
func New(code Code) *Error { return &Error{code: code} }

// Wrap 用错误码包裹一个内部原因。cause 只进日志。
func Wrap(code Code, cause error) *Error { return &Error{code: code, cause: cause} }

// Newf 用错误码构造错误，并覆盖默认文案。
//
// 只在文案需要带上下文（如具体字段名）时使用；能复用码表就别用，
// 否则多语言改造时这些散落文案会漏掉。
func Newf(code Code, format string, args ...any) *Error {
	return &Error{code: code, msg: fmt.Sprintf(format, args...)}
}

// Wrapf 用错误码包裹内部原因，并覆盖默认文案。
func Wrapf(code Code, cause error, format string, args ...any) *Error {
	return &Error{code: code, msg: fmt.Sprintf(format, args...), cause: cause}
}

// As 把任意 error 归一化成 *Error。
//
// 已经是应用错误的原样返回；其余一律视为内部错误（对外只给通用文案，
// 真实原因留在 cause 里进日志）。
func As(err error) *Error {
	if err == nil {
		return nil
	}
	var appErr *Error
	if errors.As(err, &appErr) {
		return appErr
	}
	return Wrap(Internal, err)
}

// ---------------------------------------------------------------------------
// 码表
// ---------------------------------------------------------------------------

// spec 描述一个错误码的 HTTP 状态与默认文案。
type spec struct {
	status  int
	message string
}

// catalog 是错误码到状态/文案的映射。各领域在 init 阶段用 Define 注册。
var catalog = map[Code]spec{}

// Define 注册一个错误码。重复注册会 panic —— 宁可在启动时炸掉，
// 也不要让两个模块悄悄用同一个码却给出不同文案。
func Define(code Code, status int, message string) {
	if _, dup := catalog[code]; dup {
		panic(fmt.Sprintf("apperr: 错误码 %q 重复注册", code))
	}
	catalog[code] = spec{status: status, message: message}
}

// StatusOf 返回错误码对应的 HTTP 状态码；未注册的按 500 处理。
func StatusOf(code Code) int {
	if s, ok := catalog[code]; ok {
		return s.status
	}
	return http.StatusInternalServerError
}

// MessageOf 返回错误码的默认文案；未注册的给一句兜底。
func MessageOf(code Code) string {
	if s, ok := catalog[code]; ok {
		return s.message
	}
	return "服务器内部错误"
}

// Codes 返回已注册的全部错误码，供测试与文档生成使用。
func Codes() []Code {
	out := make([]Code, 0, len(catalog))
	for c := range catalog {
		out = append(out, c)
	}
	return out
}
