package middleware

import (
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"runtime/debug"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/xywf221/xuliu/internal/pkg/apperr"
	"github.com/xywf221/xuliu/internal/pkg/response"
)

// Recovery 捕获 panic，记录完整堆栈，并回一个不泄露内部细节的 500。
//
// 与 gin 自带的 Recovery 的区别：
//   - 用 slog 记录，且携带 requestId（gin 自带的是打印到 stderr）
//   - 客户端破坏连接（EPIPE 之类）时不当成服务端错误刷告警
//   - 响应体走统一错误格式，不直接回 gin 的默认纯文本
func Recovery() gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			r := recover()
			if r == nil {
				return
			}

			// 客户端提前断开导致的写失败，不值得报错 —— 用户关了页面而已。
			if isBrokenPipe(r) {
				ContextLogger(c).Warn("客户端中断连接",
					slog.Any("panic", r))
				c.Abort()
				return
			}

			stack := debug.Stack()
			ContextLogger(c).Error("请求处理发生 panic",
				slog.Any("panic", r),
				slog.String("stack", string(stack)),
			)

			// 正在被回放/测试时把 panic 重新抛出，否则测试里
			// 只会看到一个 500，拿不到原始堆栈。
			if isTestEnv() {
				panic(r)
			}

			response.Fail(c, apperr.Wrapf(apperr.Internal, fmt.Errorf("panic: %v", r), "服务器内部错误"))
		}()

		c.Next()
	}
}

// Logger 记录访问日志。放在 Recovery 之后注册，
// 这样它仍能记录到 panic 时的最终状态码。
func Logger() gin.HandlerFunc {
	return func(c *gin.Context) {
		started := time.Now()
		c.Next()
		accessLog(c, started)
	}
}

// isBrokenPipe 判断 panic 是否源自客户端断开连接。
func isBrokenPipe(r any) bool {
	ne, ok := r.(*net.OpError)
	if !ok {
		return false
	}
	var se *os.SyscallError
	if !errors.As(ne.Err, &se) {
		return false
	}
	msg := strings.ToLower(se.Error())
	return strings.Contains(msg, "broken pipe") ||
		strings.Contains(msg, "connection reset by peer") ||
		strings.Contains(msg, "connection refused")
}

// isTestEnv 报告是否运行在 go test 中。
func isTestEnv() bool {
	return strings.HasSuffix(os.Args[0], ".test") ||
		strings.Contains(os.Args[0], "\\go-build")
}
