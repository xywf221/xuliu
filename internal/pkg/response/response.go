// Package response 定义全站统一的 HTTP 响应外壳与出参辅助函数。
//
// 约定：
//
//	成功  2xx  {"data": <载荷>, "requestId": "..."}
//	失败  4xx/5xx {"code": "story_not_found", "message": "需求不存在", "requestId": "..."}
//
// 客户端只需要判断 HTTP 状态码是否为 2xx，不需要在成功响应里再判一次 code。
// requestId 两侧都有，便于用户报错时直接拿它去日志里定位。
package response

import (
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/xywf221/xuliu/internal/pkg/apperr"
)

// HeaderRequestID 是回给客户端的请求 ID 响应头。
const HeaderRequestID = "X-Request-Id"

// ContextKeyRequestID 是 requestId 在 gin.Context 里的键。
// 由 middleware.RequestID 写入，这里只读，避免包依赖倒挂。
const ContextKeyRequestID = "requestId"

// Body 是所有 JSON 响应的统一外壳。
type Body struct {
	Data      any    `json:"data,omitempty"`
	Code      string `json:"code,omitempty"`
	Message   string `json:"message,omitempty"`
	RequestID string `json:"requestId,omitempty"`
}

// requestID 从 gin.Context 取请求 ID（没有就留空，不阻断响应）。
func requestID(c *gin.Context) string {
	if c == nil {
		return ""
	}
	return c.GetString(ContextKeyRequestID)
}

// OK 返回 200 与载荷。
func OK(c *gin.Context, data any) {
	c.JSON(http.StatusOK, Body{Data: data, RequestID: requestID(c)})
}

// Created 返回 201 与新建资源。
func Created(c *gin.Context, data any) {
	c.JSON(http.StatusCreated, Body{Data: data, RequestID: requestID(c)})
}

// NoContent 返回 204。
func NoContent(c *gin.Context) {
	c.Status(http.StatusNoContent)
}

// Fail 把任意 error 归一化成应用错误后写出响应。
//
// 内部原因（cause）只写日志、不回客户端 —— 避免把 SQL 语句、文件路径
// 之类的实现细节漏给前端。
func Fail(c *gin.Context, err error) {
	if err == nil {
		err = apperr.New(apperr.Internal)
	}
	appErr := apperr.As(err)

	rid := requestID(c)
	attrs := []any{
		slog.String("code", string(appErr.Code())),
		slog.String("requestId", rid),
		slog.String("path", c.FullPath()),
	}
	// 5xx 才是我们自己的问题，用 Error；4xx 是调用方用错了，用 Info 不刷日志。
	if appErr.Status() >= http.StatusInternalServerError {
		slog.Error("请求处理失败", append(attrs, slog.Any("cause", appErr))...)
	} else {
		slog.Debug("请求被拒绝", append(attrs, slog.Any("cause", appErr))...)
	}

	c.AbortWithStatusJSON(appErr.Status(), Body{
		Code:      string(appErr.Code()),
		Message:   appErr.Message(),
		RequestID: rid,
	})
}

// Abort 与 Fail 相同，单独命名以便在中间件里表达「中断后续处理」的意图。
func Abort(c *gin.Context, err error) { Fail(c, err) }
