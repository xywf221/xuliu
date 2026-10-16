package router

import (
	"github.com/gin-gonic/gin"

	"github.com/xywf221/xuliu/internal/middleware"
)

// registerCommon 挂载评论、附件、操作历史这些「挂在任意业务对象上」的路由。
//
// 它们不挂权限点，只要求登录 —— 因为「能不能看这条评论/这个附件」
// 取决于被挂载的那个业务对象，而不是用户有没有某个功能权限，
// 这类判定在服务层由 Gate 按对象所属产品的 ACL 完成。
func registerCommon(api *gin.RouterGroup, deps Deps) {
	auth := middleware.Authenticate(deps.Authenticator)

	{
		g := api.Group("/comments", auth)
		g.GET("", deps.Comments.List)
		g.POST("", deps.Comments.Create)
		g.DELETE("/:id", deps.Comments.Delete)
	}

	{
		g := api.Group("/files", auth)
		g.GET("", deps.Files.List)
		g.POST("", deps.Files.Upload)
		g.GET("/:id/download", deps.Files.Download)
		g.DELETE("/:id", deps.Files.Delete)
	}

	{
		g := api.Group("/actions", auth)
		g.GET("", deps.Actions.List)
	}
}
