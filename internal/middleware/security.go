package middleware

import (
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/xywf221/xuliu/internal/identity"
	"github.com/xywf221/xuliu/internal/pkg/apperr"
	"github.com/xywf221/xuliu/internal/pkg/response"
)

// SecurityHeaders 设置一组保守的响应头。
//
// 说明取舍：
//   - 不做 X-Frame-Options: DENY。将来若要支持「把看板嵌进企业门户」，
//     DENY 会直接挡死；这里用 SAMEORIGIN，同源嵌入可用，跨站仍被拒。
//   - 不开 HSTS。本服务默认跑在 HTTP 上（内网部署常见），
//     强制 HSTS 会让用户没法退回 http 访问。
func SecurityHeaders() gin.HandlerFunc {
	return func(c *gin.Context) {
		h := c.Writer.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "SAMEORIGIN")
		h.Set("Referrer-Policy", "same-origin")
		// 本服务不依赖任何第三方域名，直接全关。
		h.Set("Permissions-Policy", "geolocation=(), microphone=(), camera=()")
		c.Next()
	}
}

// CrossOriginProtection 用 Go 1.27 标准库内建的 CSRF 防护，
// 拒绝浏览器发起的跨站写请求。
//
// 判定依据是 Sec-Fetch-Site（2023 年后所有浏览器都支持）或 Origin 与 Host
// 的比较；不带头部的请求（curl、服务端调用）视为非浏览器请求放行。
// 这样我们不必引入第三方 CSRF 中间件，也不必自己发 token。
//
// trustedOrigins 用于前后端分离开发时（Vite 跑在另一个端口）放行前端源。
//
// 注意：安全方法（GET/HEAD/OPTIONS）永远放行，所以**任何会改数据的操作
// 都不能做成 GET**，否则这道防护形同虚设。
func CrossOriginProtection(trustedOrigins []string) (gin.HandlerFunc, error) {
	prot := http.NewCrossOriginProtection()

	for _, origin := range trustedOrigins {
		if origin == "" {
			continue
		}
		// 通配符等于关掉防护，显式拒绝，避免配置写错后静默失效。
		if origin == "*" {
			return nil, apperr.Newf(apperr.Internal, "CORS 通配符 * 会使跨站防护失效，请改为列出具体源")
		}
		if err := prot.AddTrustedOrigin(origin); err != nil {
			return nil, apperr.Wrapf(apperr.Internal, err, "跨站防护信任源格式不合法：%s", origin)
		}
	}

	return func(c *gin.Context) {
		// 用 Check 而不是 prot.Handler(...) 包装：
		//
		// gin 的处理链由引擎的循环驱动，中间件只要不显式 Abort，
		// 引擎就会继续往后执行链上的下一个 handler。而 prot.Handler 在拒绝时
		// 只是「不调用内层 handler」，那是给 net/http 的语义（不返回即终止），
		// 搬到 gin 里就成了「响应 403 但写操作照样落库」—— 防护形同虚设。
		if err := prot.Check(c.Request); err != nil {
			slog.Debug("跨站写请求被拒绝",
				slog.String("requestId", c.GetString(response.ContextKeyRequestID)),
				slog.String("method", c.Request.Method),
				slog.String("path", c.Request.URL.Path),
				slog.String("origin", c.Request.Header.Get("Origin")),
				slog.String("secFetchSite", c.Request.Header.Get("Sec-Fetch-Site")),
			)
			// 统一错误格式，而不是标准库默认的纯文本 403。
			response.Abort(c, apperr.Newf(apperr.Forbidden, "请求来源不被信任"))
			return
		}
		c.Next()
	}, nil
}

// RequirePerm 是权限校验中间件。
//
// 真正的判定逻辑在 acl 包，这里通过 identity.PermChecker 接口间接调用，
// 以避免 middleware → acl → middleware 的循环依赖。
// perm 形如 "bug-resolve"（禅道的 module-method 命名）。
func RequirePerm(checker identity.PermChecker, perm string) gin.HandlerFunc {
	return func(c *gin.Context) {
		user, ok := identity.CurrentUser(c.Request.Context())
		if !ok {
			response.Abort(c, apperr.New(apperr.Unauthorized))
			return
		}
		if !checker.Can(user, perm) {
			ContextLogger(c).Debug("缺少权限点",
				slog.Uint64("userId", uint64(user.ID)),
				slog.String("perm", perm),
			)
			response.Abort(c, apperr.New(apperr.Forbidden))
			return
		}
		c.Next()
	}
}
