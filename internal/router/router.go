// Package router 负责把 HTTP 路由挂到 gin 引擎上。
//
// 分文件按模块组织（system.go 等），本文件只做引擎配置与顶层组装。
package router

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/xywf221/xuliu/internal/acl"
	"github.com/xywf221/xuliu/internal/config"
	"github.com/xywf221/xuliu/internal/handler"
	"github.com/xywf221/xuliu/internal/middleware"
	"github.com/xywf221/xuliu/internal/pkg/apperr"
	"github.com/xywf221/xuliu/internal/pkg/response"
	"github.com/xywf221/xuliu/internal/web"
)

// Deps 是路由所需的全部分依赖。
//
// 用结构体而不是长参数列表：模块增多后参数列表会变得难以阅读，
// 而结构体让每个模块的依赖一眼可见。
type Deps struct {
	Config *config.Config
	// Authenticator 校验访问令牌。
	Authenticator middleware.TokenAuthenticator
	// Enforcer 提供权限判定。
	Enforcer *acl.Enforcer

	Auth   *handler.AuthHandler
	Users  *handler.UserHandler
	Depts  *handler.DeptHandler
	Groups *handler.GroupHandler

	// 产品域
	Productlines *handler.ProductlineHandler
	Products     *handler.ProductHandler
	Modules      *handler.ModuleHandler
	Plans        *handler.PlanHandler
	Stories      *handler.StoryHandler

	// 项目域
	Projects   *handler.ProjectHandler
	Executions *handler.ExecutionHandler
	Tasks      *handler.TaskHandler

	// 缺陷域
	Bugs *handler.BugHandler

	// 通用对象
	Comments *handler.CommentHandler
	Files    *handler.FileHandler
	Actions  *handler.ActionHandler

	// 工作台与通知
	Notices   *handler.NoticeHandler
	Workbench *handler.WorkbenchHandler
}

// New 构造 gin 引擎并注册全部路由。
func New(deps Deps) (*gin.Engine, error) {
	cfg := deps.Config

	if cfg.IsProd() {
		gin.SetMode(gin.ReleaseMode)
	}

	// 用 gin.New() 而非 gin.Default()：后者会装上 gin 自带的
	// Logger 与 Recovery，而我们要用自己那套（带 requestId、走 slog）。
	engine := gin.New()

	// 关闭自动重定向，避免 API 请求被 301 到带斜杠的路径后
	// 客户端跟随时把 POST 变成 GET 而丢掉请求体。
	engine.RedirectTrailingSlash = false
	engine.RedirectFixedPath = false

	// gin 默认信任全部代理（0.0.0.0/0），意味着任何请求都可以靠
	// X-Forwarded-For 伪造 ClientIP()。这里改成按配置显式声明：
	// 默认不信任任何代理（RemoteAddr 即真实客户端），
	// 部署在反向代理后时才在配置里列出代理地址。
	if err := engine.SetTrustedProxies(cfg.Server.TrustedProxies); err != nil {
		return nil, fmt.Errorf("设置可信代理失败: %w", err)
	}

	engine.Use(
		middleware.RequestID(),
		middleware.Recovery(),
		middleware.Logger(),
		middleware.SecurityHeaders(),
	)

	// 跨站防护：只放行开发期前端的源。
	// 生产环境前后端同源部署，CORSOrigins 为空即可。
	cop, err := middleware.CrossOriginProtection(cfg.Server.CORSOrigins)
	if err != nil {
		return nil, err
	}
	engine.Use(cop)

	// 健康检查放在鉴权之外，供探针使用。
	engine.GET("/healthz", healthz)

	api := engine.Group("/api/v1")
	registerSystem(api, deps)
	registerProduct(api, deps)
	registerStory(api, deps)
	registerProject(api, deps)
	registerTask(api, deps)
	registerBug(api, deps)
	registerCommon(api, deps)
	registerWorkbench(api, deps)

	// 接口文档（仅非生产环境）。
	registerSwagger(engine, !cfg.IsProd())

	// 前端静态资源与 SPA 回退。
	registerWeb(engine)

	return engine, nil
}

// healthz 是健康检查。
//
// 只报告进程存活，不探数据库：探针若要等数据库就绪，
// 会在数据库短暂抖动时把健康的实例也摘掉。
func healthz(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// registerWeb 挂载前端静态资源，并处理 SPA 的路由回退。
func registerWeb(engine *gin.Engine) {
	assets := web.Handler()
	if assets == nil {
		// 前端尚未构建（开发期用 vite dev server），此时给出明确提示，
		// 而不是返回一个令人困惑的 404。
		engine.NoRoute(func(c *gin.Context) {
			if strings.HasPrefix(c.Request.URL.Path, "/api/") {
				response.Fail(c, apperr.New(apperr.NotFound))
				return
			}
			c.String(http.StatusNotFound,
				"前端资源尚未构建。开发时请访问 Vite 开发服务器；\n"+
					"构建请在 web/ 目录下执行 npm run build。")
		})
		return
	}

	engine.NoRoute(func(c *gin.Context) {
		path := c.Request.URL.Path

		// API 路径不参与 SPA 回退，否则前端路由会把 404 伪装成 200。
		if strings.HasPrefix(path, "/api/") {
			response.Fail(c, apperr.New(apperr.NotFound))
			return
		}

		// 静态资源命中则直接返回。
		if strings.HasPrefix(path, "/assets/") || hasStaticExtension(path) {
			assets.ServeHTTP(c.Writer, c.Request)
			return
		}

		// 其余路径交给前端路由：一律返回 index.html，
		// 让 vue-router 按 URL 渲染对应页面（刷新子页面才能正常工作）。
		assets.ServeIndex(c.Writer, c.Request)
	})
}

// staticExtensions 是需要直接按文件返回的扩展名。
var staticExtensions = []string{
	".js", ".css", ".png", ".jpg", ".jpeg", ".gif", ".svg", ".webp",
	".ico", ".woff", ".woff2", ".ttf", ".eot", ".map", ".json",
}

// hasStaticExtension 报告路径是否指向静态文件。
func hasStaticExtension(path string) bool {
	lower := strings.ToLower(path)
	for _, ext := range staticExtensions {
		if strings.HasSuffix(lower, ext) {
			return true
		}
	}
	return false
}
