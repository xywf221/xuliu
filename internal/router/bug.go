package router

import (
	"github.com/gin-gonic/gin"

	"github.com/xywf221/xuliu/internal/acl"
	"github.com/xywf221/xuliu/internal/middleware"
)

// registerBug 挂载缺陷路由。
func registerBug(api *gin.RouterGroup, deps Deps) {
	auth := middleware.Authenticate(deps.Authenticator)
	perm := func(p acl.Priv) gin.HandlerFunc {
		return middleware.RequirePerm(deps.Enforcer, string(p))
	}

	g := api.Group("/bugs", auth)

	g.GET("", perm(acl.PrivBugBrowse), deps.Bugs.List)
	// 状态计数与列表同权限：它只是同一份数据的另一种聚合。
	g.GET("/counts", perm(acl.PrivBugBrowse), deps.Bugs.Counts)
	g.GET("/:id", perm(acl.PrivBugBrowse), deps.Bugs.Get)
	g.POST("", perm(acl.PrivBugCreate), deps.Bugs.Create)
	g.PUT("/:id", perm(acl.PrivBugEdit), deps.Bugs.Update)
	g.DELETE("/:id", perm(acl.PrivBugDelete), deps.Bugs.Delete)

	// 每个动作各挂自己的权限点，而不是「一个 /actions/:action + 一个笼统权限」——
	// 后者会让「能确认缺陷」的人顺带获得关闭缺陷的权力。
	g.POST("/:id/confirm", perm(acl.PrivBugConfirm), deps.Bugs.Confirm)
	g.POST("/:id/assign", perm(acl.PrivBugAssign), deps.Bugs.Assign)
	g.POST("/:id/resolve", perm(acl.PrivBugResolve), deps.Bugs.Resolve)
	g.POST("/:id/close", perm(acl.PrivBugClose), deps.Bugs.Close)
	g.POST("/:id/activate", perm(acl.PrivBugActivate), deps.Bugs.Activate)

	// 「转需求」要两个权限点：它一半是缺陷动作（决定这条缺陷就此了结），
	// 一半是需求动作（真的建出一条需求）。只卡 bug-tostory 就等于给
	// 每个能转需求的人开了建需求的权限 —— 中间件会依次校验，缺一即拒。
	g.POST("/:id/tostory",
		perm(acl.PrivBugToStory), perm(acl.PrivStoryCreate), deps.Bugs.ToStory)
}
