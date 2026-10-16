// Package logging 构建全局结构化日志器。
//
// 用 log/slog 而不是第三方日志库：标准库够用，且 slog.Logger 可以挂到
// context 上逐请求下传，天然支持「同一请求的日志都带同一个 requestId」。
package logging

import (
	"context"
	"log/slog"
	"os"
	"strings"

	"github.com/xywf221/xuliu/internal/config"
)

// loggerContextKey 是 logger 在 context 里的私有键。
// 用私有类型避免与其他包写入的 context 键冲突。
type loggerContextKey struct{}

// New 按配置构造 slog.Logger。
func New(cfg config.Log, env string) *slog.Logger {
	opts := &slog.HandlerOptions{
		Level:     parseLevel(cfg.Level),
		AddSource: cfg.AddSource,
	}

	var handler slog.Handler
	if cfg.Format == "json" {
		handler = slog.NewJSONHandler(os.Stdout, opts)
	} else {
		handler = slog.NewTextHandler(os.Stdout, opts)
	}

	logger := slog.New(handler).With(
		slog.String("app", "xuliu"),
		slog.String("env", env),
	)

	// 同时设为默认 logger，让未显式注入的第三方库（gorm 等）也走同一条输出。
	slog.SetDefault(logger)
	return logger
}

// parseLevel 把配置字符串映射为 slog 级别，未知值按 info。
func parseLevel(s string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// WithLogger 把 logger 放入 context。
func WithLogger(ctx context.Context, logger *slog.Logger) context.Context {
	return context.WithValue(ctx, loggerContextKey{}, logger)
}

// FromContext 取出 context 上的 logger；没有则回落到默认 logger。
//
// 回落到默认值（而不是返回 nil）让调用方无需判空，代价只是丢了
// 请求级字段，不至于因为漏注入而丢掉整条日志。
func FromContext(ctx context.Context) *slog.Logger {
	if ctx != nil {
		if logger, ok := ctx.Value(loggerContextKey{}).(*slog.Logger); ok && logger != nil {
			return logger
		}
	}
	return slog.Default()
}
