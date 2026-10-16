package middleware

import (
	"log/slog"
	"time"
	"uuid"

	"github.com/gin-gonic/gin"

	"github.com/xywf221/xuliu/internal/logging"
	"github.com/xywf221/xuliu/internal/pkg/response"
)

// HeaderRequestID 是客户端可自带的请求 ID 头。
const HeaderRequestID = "X-Request-Id"

// RequestID 为每个请求分配一个 ID，写回响应头，并挂一个带该 ID 的
// logger 到 context 上。
//
// 客户端若自带了 X-Request-Id 就沿用（便于跨服务串联链路），否则生成一个。
// 沿用前会做长度与字符检查，避免把任意内容写进日志与响应头。
func RequestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		id := sanitizeRequestID(c.GetHeader(HeaderRequestID))
		if id == "" {
			// Go 1.27 标准库 uuid，无需第三方依赖。
			id = uuid.NewV7().String()
		}

		c.Set(response.ContextKeyRequestID, id)
		c.Header(HeaderRequestID, id)

		// 把带 requestId 的 logger 挂到 context 上，后续所有业务日志
		// 都自动带上它，排查问题时按 ID 一搜就能捞出整个请求的轨迹。
		logger := logging.FromContext(c.Request.Context()).With(
			slog.String("requestId", id),
			slog.String("method", c.Request.Method),
			slog.String("path", c.Request.URL.Path),
		)
		ctx := logging.WithLogger(c.Request.Context(), logger)
		c.Request = c.Request.WithContext(ctx)

		c.Next()
	}
}

// sanitizeRequestID 校验客户端传入的请求 ID。
//
// 不合法就返回空串让调用方重新生成 —— 请求 ID 会被写进响应头和日志，
// 放任任意内容等于让调用方注入日志噪音。
func sanitizeRequestID(id string) string {
	const maxLen = 64
	if id == "" || len(id) > maxLen {
		return ""
	}
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z',
			r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9',
			r == '-', r == '_', r == '.':
		default:
			return ""
		}
	}
	return id
}

// ContextLogger 返回当前请求的 logger，供 handler 与 service 使用。
// service 层通过 context 拿，不必把 logger 一路当参数传。
func ContextLogger(c *gin.Context) *slog.Logger {
	return logging.FromContext(c.Request.Context())
}

// accessLog 记录一条访问日志所需的字段已由 RequestID 中间件注入到 logger，
// 这里只补充耗时与状态码。
func accessLog(c *gin.Context, started time.Time) {
	logger := ContextLogger(c)
	status := c.Writer.Status()

	attrs := []any{
		slog.Int("status", status),
		slog.Duration("latency", time.Since(started)),
		slog.Int("bytes", c.Writer.Size()),
	}
	if raw := c.Request.URL.RawQuery; raw != "" {
		attrs = append(attrs, slog.String("query", raw))
	}

	// 5xx 是我们自己的问题要显眼；4xx 多是调用方用错，降一级免得刷屏。
	switch {
	case status >= 500:
		logger.Error("请求处理异常", attrs...)
	case status >= 400:
		logger.Debug("请求被拒绝", attrs...)
	default:
		logger.Info("请求完成", attrs...)
	}
}
