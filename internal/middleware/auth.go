package middleware

import (
	"context"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/xywf221/xuliu/internal/identity"
	"github.com/xywf221/xuliu/internal/pkg/apperr"
	"github.com/xywf221/xuliu/internal/pkg/response"
)

// TokenAuthenticator 由 service.AuthService 实现。
//
// 定义在这里而不是直接依赖 service 包，是为了让中间件层不依赖业务层 ——
// 依赖方向始终是 handler → service → repository，中间件只依赖接口。
// 好处是中间件的单测可以用一个假实现替换掉真实的令牌校验。
type TokenAuthenticator interface {
	// Authenticate 校验访问令牌并返回登录用户。失败返回应用错误。
	Authenticate(ctx context.Context, accessToken string) (*identity.User, error)
}

// Authenticate 校验请求携带的访问令牌，并把登录用户挂到上下文上。
//
// 令牌从 Authorization 头读取（Bearer 形式）。未携带令牌直接拒绝 ——
// 需要匿名访问的路由应当挂在鉴权中间件之外，而不是让这里放行。
func Authenticate(authenticator TokenAuthenticator) gin.HandlerFunc {
	return func(c *gin.Context) {
		token, ok := bearerToken(c)
		if !ok {
			response.Abort(c, apperr.New(apperr.Unauthorized))
			return
		}

		user, err := authenticator.Authenticate(c.Request.Context(), token)
		if err != nil {
			response.Abort(c, err)
			return
		}

		// 把用户挂到请求上下文，后续 handler 与 service 通过
		// identity.CurrentUser 取用，不必逐层传参。
		ctx := identity.WithUser(c.Request.Context(), user)
		c.Request = c.Request.WithContext(ctx)

		c.Next()
	}
}

// bearerToken 从 Authorization 头提取令牌。
//
// 兼容不带 "Bearer " 前缀的裸令牌：有些客户端（以及 curl 手测）
// 会直接把令牌放进头里，为此拒绝请求没有实际安全收益。
func bearerToken(c *gin.Context) (string, bool) {
	raw := strings.TrimSpace(c.GetHeader("Authorization"))
	if raw == "" {
		// 允许通过查询参数传递，仅用于「浏览器直接打开附件下载链接」
		// 这类无法设置请求头的场景。
		if q := c.Query("access_token"); q != "" {
			return q, true
		}
		return "", false
	}

	const prefix = "Bearer "
	if len(raw) > len(prefix) && strings.EqualFold(raw[:len(prefix)], prefix) {
		return strings.TrimSpace(raw[len(prefix):]), true
	}
	// 不带前缀时按裸令牌处理。
	return raw, true
}
