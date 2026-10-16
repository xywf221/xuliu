package router

import (
	"github.com/gin-gonic/gin"

	"github.com/xywf221/xuliu/internal/acl"
	"github.com/xywf221/xuliu/internal/middleware"
)

// registerStory 挂载需求相关路由。
//
// 状态流转按动作拆成独立路由（而不是一个 /stories/:id/actions/:action），
// 这样权限点能精确到动作，审计时不必读 handler 就能回答
// 「关闭需求需要什么权限」。
func registerStory(api *gin.RouterGroup, deps Deps) {
	auth := middleware.Authenticate(deps.Authenticator)
	perm := func(p acl.Priv) gin.HandlerFunc {
		return middleware.RequirePerm(deps.Enforcer, string(p))
	}

	g := api.Group("/stories", auth)

	g.GET("", perm(acl.PrivStoryBrowse), deps.Stories.List)
	g.GET("/:id", perm(acl.PrivStoryBrowse), deps.Stories.Get)
	g.GET("/:id/versions", perm(acl.PrivStoryBrowse), deps.Stories.Versions)
	g.POST("", perm(acl.PrivStoryCreate), deps.Stories.Create)
	g.PUT("/:id", perm(acl.PrivStoryEdit), deps.Stories.Update)
	g.DELETE("/:id", perm(acl.PrivStoryDelete), deps.Stories.Delete)

	// 评审：提交与撤回用 story-review；给结论同样用 story-review，
	// 因为「谁能评审」是同一件事，换个结论不该换权限。
	g.POST("/:id/submit-review", perm(acl.PrivStoryReview), deps.Stories.SubmitReview)
	g.POST("/:id/recall", perm(acl.PrivStoryReview), deps.Stories.Recall)
	g.POST("/:id/review", perm(acl.PrivStoryReview), deps.Stories.Review)

	// 变更走单独的权限点：改需求内容会波及下游开发，比编辑更敏感。
	g.POST("/:id/change", perm(acl.PrivStoryChange), deps.Stories.Change)

	g.POST("/:id/close", perm(acl.PrivStoryClose), deps.Stories.Close)
	g.POST("/:id/activate", perm(acl.PrivStoryClose), deps.Stories.Activate)
	g.POST("/:id/assign", perm(acl.PrivStoryAssign), deps.Stories.Assign)

	// 阶段推进实质是编辑需求，只是它改的是 stage 而不是内容。
	g.POST("/:id/stage", perm(acl.PrivStoryEdit), deps.Stories.Stage)

}
