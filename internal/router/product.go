package router

import (
	"github.com/gin-gonic/gin"

	"github.com/xywf221/xuliu/internal/acl"
	"github.com/xywf221/xuliu/internal/middleware"
)

// registerProduct 挂载产品线、产品、模块、产品计划相关路由。
func registerProduct(api *gin.RouterGroup, deps Deps) {
	auth := middleware.Authenticate(deps.Authenticator)
	perm := func(p acl.Priv) gin.HandlerFunc {
		return middleware.RequirePerm(deps.Enforcer, string(p))
	}

	// ---- 产品线 ----
	{
		g := api.Group("/productlines", auth)

		// 产品线树供各处选择产品线时使用，登录即可读。
		g.GET("/tree", deps.Productlines.Tree)

		g.POST("", perm(acl.PrivProductlineManage), deps.Productlines.Create)
		g.PUT("/:id", perm(acl.PrivProductlineManage), deps.Productlines.Update)
		g.DELETE("/:id", perm(acl.PrivProductlineManage), deps.Productlines.Delete)
	}

	// ---- 产品 ----
	{
		g := api.Group("/products", auth)

		// brief 供下拉与筛选栏使用：登录用户能看到的产品，
		// 就该出现在选择器里；能不能看由服务层按 ACL 过滤。
		g.GET("/brief", deps.Products.Brief)

		g.GET("", perm(acl.PrivProductBrowse), deps.Products.List)
		g.GET("/:id", perm(acl.PrivProductBrowse), deps.Products.Get)
		g.POST("", perm(acl.PrivProductCreate), deps.Products.Create)
		g.PUT("/:id", perm(acl.PrivProductEdit), deps.Products.Update)
		g.DELETE("/:id", perm(acl.PrivProductDelete), deps.Products.Delete)

		// 模块树与产品计划从属于产品，读权限沿用产品浏览权限。
		g.GET("/:id/modules", perm(acl.PrivProductBrowse), deps.Modules.Tree)
		g.GET("/:id/plans", perm(acl.PrivProductBrowse), deps.Plans.List)
	}

	// ---- 模块 ----
	{
		g := api.Group("/modules", auth)
		g.GET("/:id", perm(acl.PrivProductBrowse), deps.Modules.Get)
		g.POST("", perm(acl.PrivModuleManage), deps.Modules.Create)
		g.PUT("/:id", perm(acl.PrivModuleManage), deps.Modules.Update)
		g.DELETE("/:id", perm(acl.PrivModuleManage), deps.Modules.Delete)
	}

	// ---- 产品计划 ----
	{
		g := api.Group("/plans", auth)
		g.GET("/:id", perm(acl.PrivProductBrowse), deps.Plans.Get)
		g.POST("", perm(acl.PrivPlanManage), deps.Plans.Create)
		g.PUT("/:id", perm(acl.PrivPlanManage), deps.Plans.Update)
		g.DELETE("/:id", perm(acl.PrivPlanManage), deps.Plans.Delete)
	}
}
