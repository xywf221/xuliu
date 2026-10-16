package router

import (
	"github.com/gin-gonic/gin"

	"github.com/xywf221/xuliu/internal/acl"
	"github.com/xywf221/xuliu/internal/middleware"
)

// registerProject 挂载项目、迭代与团队相关路由。
//
// 状态动作按动作名拆成独立路由，每个动作挂自己的权限点：
// 「开始/挂起/激活」共用 project-start，而「关闭」用 project-close ——
// 关闭一个项目比挂起它影响大得多，不该是同一个权限。
func registerProject(api *gin.RouterGroup, deps Deps) {
	auth := middleware.Authenticate(deps.Authenticator)
	perm := func(p acl.Priv) gin.HandlerFunc {
		return middleware.RequirePerm(deps.Enforcer, string(p))
	}

	g := api.Group("/projects", auth)

	// /brief 要在 /:id 之前注册：gin 的路由树能区分静态与参数段，
	// 但保持「固定路径在前」的顺序读起来更清楚。
	g.GET("/brief", perm(acl.PrivProjectBrowse), deps.Projects.Brief)
	g.GET("", perm(acl.PrivProjectBrowse), deps.Projects.List)
	g.GET("/:id", perm(acl.PrivProjectBrowse), deps.Projects.Get)
	g.POST("", perm(acl.PrivProjectCreate), deps.Projects.Create)
	g.PUT("/:id", perm(acl.PrivProjectEdit), deps.Projects.Update)
	g.DELETE("/:id", perm(acl.PrivProjectDelete), deps.Projects.Delete)

	g.POST("/:id/start", perm(acl.PrivProjectStart), deps.Projects.Action("start"))
	g.POST("/:id/suspend", perm(acl.PrivProjectStart), deps.Projects.Action("suspend"))
	g.POST("/:id/activate", perm(acl.PrivProjectStart), deps.Projects.Action("activate"))
	g.POST("/:id/close", perm(acl.PrivProjectClose), deps.Projects.Action("close"))

	// 需求排期：把需求排进项目/迭代，或撤出来。
	// 写操作用 project-edit —— 排期改变了项目的范围，属于项目维护的动作。
	g.GET("/:id/stories", perm(acl.PrivProjectBrowse), deps.Projects.ListStories)
	g.POST("/:id/stories", perm(acl.PrivProjectEdit), deps.Projects.LinkStory)
	g.DELETE("/:id/stories/:storyId", perm(acl.PrivProjectEdit), deps.Projects.UnlinkStory)

	g.GET("/:id/team", perm(acl.PrivProjectBrowse), deps.Projects.ListTeam)
	g.POST("/:id/team", perm(acl.PrivTeamManage), deps.Projects.AddTeam)
	g.PUT("/:id/team/:userId", perm(acl.PrivTeamManage), deps.Projects.UpdateTeam)
	g.DELETE("/:id/team/:userId", perm(acl.PrivTeamManage), deps.Projects.RemoveTeam)

	// ---- 迭代 ----
	e := api.Group("/executions", auth)

	e.GET("", perm(acl.PrivProjectBrowse), deps.Executions.List)
	e.GET("/:id", perm(acl.PrivProjectBrowse), deps.Executions.Get)
	e.POST("", perm(acl.PrivExecutionCreate), deps.Executions.Create)
	e.PUT("/:id", perm(acl.PrivExecutionEdit), deps.Executions.Update)
	e.DELETE("/:id", perm(acl.PrivExecutionDelete), deps.Executions.Delete)

	for _, action := range []string{"start", "suspend", "close", "activate"} {
		e.POST("/:id/"+action, perm(acl.PrivExecutionEdit), deps.Executions.Action(action))
	}

	e.GET("/:id/team", perm(acl.PrivProjectBrowse), deps.Executions.ListTeam)
	e.POST("/:id/team", perm(acl.PrivTeamManage), deps.Executions.AddTeam)
	e.PUT("/:id/team/:userId", perm(acl.PrivTeamManage), deps.Executions.UpdateTeam)
	e.DELETE("/:id/team/:userId", perm(acl.PrivTeamManage), deps.Executions.RemoveTeam)
}
