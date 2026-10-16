package router

import (
	"github.com/gin-gonic/gin"

	"github.com/xywf221/xuliu/internal/acl"
	"github.com/xywf221/xuliu/internal/middleware"
)

// registerTask 挂载任务、工时与看板路由。
func registerTask(api *gin.RouterGroup, deps Deps) {
	auth := middleware.Authenticate(deps.Authenticator)
	perm := func(p acl.Priv) gin.HandlerFunc {
		return middleware.RequirePerm(deps.Enforcer, string(p))
	}

	g := api.Group("/tasks", auth)

	g.GET("", perm(acl.PrivTaskBrowse), deps.Tasks.List)
	// 看板是任务的另一种视图，不是某个任务的子资源，
	// 所以放在 /tasks/kanban 而不是 /tasks/:id/kanban。
	g.GET("/kanban", perm(acl.PrivTaskBrowse), deps.Tasks.Kanban)
	g.GET("/:id", perm(acl.PrivTaskBrowse), deps.Tasks.Get)
	g.POST("", perm(acl.PrivTaskCreate), deps.Tasks.Create)
	g.PUT("/:id", perm(acl.PrivTaskEdit), deps.Tasks.Update)
	g.DELETE("/:id", perm(acl.PrivTaskDelete), deps.Tasks.Delete)

	g.POST("/:id/assign", perm(acl.PrivTaskAssign), deps.Tasks.Assign)
	// 开始/暂停/继续/重新激活同属「推进任务」，共用一个权限点；
	// 完成、取消、关闭各自更敏感，分开。
	for _, action := range []string{"start", "pause", "restart", "activate"} {
		g.POST("/:id/"+action, perm(acl.PrivTaskStart), deps.Tasks.Action(action))
	}
	g.POST("/:id/finish", perm(acl.PrivTaskFinish), deps.Tasks.Finish)
	g.POST("/:id/cancel", perm(acl.PrivTaskCancel), deps.Tasks.Cancel)
	g.POST("/:id/close", perm(acl.PrivTaskClose), deps.Tasks.Close)

	g.GET("/:id/efforts", perm(acl.PrivTaskBrowse), deps.Tasks.ListEfforts)
	g.POST("/:id/efforts", perm(acl.PrivEffortRecord), deps.Tasks.RecordEffort)

	// 跨任务的工时明细（「我的工时」/「项目工时」）。
	// 权限用 effort-record：能报工的人就能看到工时明细，
	// 而工时属于业务内容，不额外开放给「只能看任务」的角色。
	f := api.Group("/efforts", auth)
	f.GET("", perm(acl.PrivEffortRecord), deps.Tasks.AllEfforts)
}
