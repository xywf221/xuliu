package router

import (
	"github.com/gin-gonic/gin"

	"github.com/xywf221/xuliu/internal/acl"
	"github.com/xywf221/xuliu/internal/middleware"
)

// registerWorkbench 挂载通知与工作台路由。
//
// 两者的权限点是分开的：通知是每个人自己的东西（my-work），
// 燃尽图是看别人干活的报表（report-browse）——
// 有些人该有工作台但不该看到整个项目的进度曲线。
func registerWorkbench(api *gin.RouterGroup, deps Deps) {
	auth := middleware.Authenticate(deps.Authenticator)
	perm := func(p acl.Priv) gin.HandlerFunc {
		return middleware.RequirePerm(deps.Enforcer, string(p))
	}

	// 通知：这一组路由都不接 userId 参数，服务端一律取当前登录人。
	// 让 id 可传就等于开一个读别人提醒的口子。
	notices := api.Group("/notices", auth, perm(acl.PrivMyWork))
	notices.GET("", deps.Notices.List)
	notices.GET("/unread-count", deps.Notices.UnreadCount)
	notices.POST("/read", deps.Notices.MarkRead)
	notices.DELETE("/:id", deps.Notices.Delete)

	workbench := api.Group("/workbench", auth)
	workbench.GET("", perm(acl.PrivMyWork), deps.Workbench.Get)
	// 燃尽图单独卡报表权限：它画的是整个迭代的进度，
	// 而工作台只画「我的」。
	workbench.GET("/burndown", perm(acl.PrivReport), deps.Workbench.Burndown)
}
