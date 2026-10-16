package router

import (
	"github.com/gin-gonic/gin"

	"github.com/xywf221/xuliu/internal/acl"
	"github.com/xywf221/xuliu/internal/middleware"
)

// registerSystem 挂载认证、用户、部门、权限组相关路由。
//
// 权限点直接写在路由上，而不是在 handler 内部判断：这样「某个接口需要
// 什么权限」在路由表里一目了然，审计权限时不必逐个读 handler。
func registerSystem(api *gin.RouterGroup, deps Deps) {
	auth := middleware.Authenticate(deps.Authenticator)
	perm := func(p acl.Priv) gin.HandlerFunc {
		return middleware.RequirePerm(deps.Enforcer, string(p))
	}

	// ---- 认证：匿名可见 ----
	{
		g := api.Group("/auth")
		g.POST("/login", deps.Auth.Login)
		// 刷新令牌自带凭据，不需要访问令牌即可调用 ——
		// 否则访问令牌一过期就无法续期，刷新机制形同虚设。
		g.POST("/refresh", deps.Auth.Refresh)
	}

	// ---- 认证：需登录 ----
	{
		g := api.Group("/auth", auth)
		g.POST("/logout", deps.Auth.Logout)
		g.GET("/profile", deps.Auth.Profile)
		g.POST("/password", deps.Auth.ChangePassword)
	}

	// ---- 用户 ----
	{
		g := api.Group("/users", auth)

		// brief 供下拉选择使用，任何登录用户都能访问：
		// 选「指派给谁」不该要求用户有「查看用户」的管理权限。
		g.GET("/brief", deps.Users.Brief)

		g.GET("", perm(acl.PrivUserBrowse), deps.Users.List)
		g.GET("/:id", perm(acl.PrivUserBrowse), deps.Users.Get)
		g.POST("", perm(acl.PrivUserCreate), deps.Users.Create)
		g.PUT("/:id", perm(acl.PrivUserEdit), deps.Users.Update)
		g.DELETE("/:id", perm(acl.PrivUserDelete), deps.Users.Delete)
		g.POST("/:id/password", perm(acl.PrivUserEdit), deps.Users.ResetPassword)
	}

	// ---- 部门 ----
	{
		g := api.Group("/depts", auth)

		// 部门树供各处选择部门时使用，登录即可读。
		g.GET("/tree", deps.Depts.Tree)

		g.GET("/:id", perm(acl.PrivDeptBrowse), deps.Depts.Get)
		g.POST("", perm(acl.PrivDeptManage), deps.Depts.Create)
		g.PUT("/:id", perm(acl.PrivDeptManage), deps.Depts.Update)
		g.DELETE("/:id", perm(acl.PrivDeptManage), deps.Depts.Delete)
	}

	// ---- 权限组 ----
	{
		g := api.Group("/groups", auth)

		// 权限点字典供权限配置界面渲染，需要组管理权限才可读。
		g.GET("/privileges", perm(acl.PrivGroupBrowse), deps.Groups.Privileges)

		g.GET("", perm(acl.PrivGroupBrowse), deps.Groups.List)
		g.GET("/:id", perm(acl.PrivGroupBrowse), deps.Groups.Get)
		g.POST("", perm(acl.PrivGroupManage), deps.Groups.Create)
		g.PUT("/:id", perm(acl.PrivGroupManage), deps.Groups.Update)
		g.DELETE("/:id", perm(acl.PrivGroupManage), deps.Groups.Delete)
	}
}
