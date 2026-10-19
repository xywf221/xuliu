package router

import (
	"github.com/gin-gonic/gin"

	"github.com/xywf221/xuliu/internal/acl"
	"github.com/xywf221/xuliu/internal/middleware"
)

// registerTest 挂载测试用例路由。
//
// 权限点写在路由上而不是 handler 内部，与其余模块一致：审计「谁能做什么」
// 时只需要读这张表。两个细节值得说明：
//
//   - 提交评审 / 撤回评审 挂的是 **testcase-edit**：推动评审的人是写用例的人，
//     出结论的才是评审人。把 submitreview 挂在 review 权限上，会让用例作者
//     必须求别人帮忙点一下「提交」。
//   - 评审结论走一个 /review 接口、由请求体给 pass 或 reject，
//     与需求的 /stories/:id/review 同形；拆成两条路由的话，
//     「谁评审的」这类审计问题要在两处分别看。
func registerTest(api *gin.RouterGroup, deps Deps) {
	auth := middleware.Authenticate(deps.Authenticator)
	perm := func(p acl.Priv) gin.HandlerFunc {
		return middleware.RequirePerm(deps.Enforcer, string(p))
	}

	g := api.Group("/testcases", auth)

	g.GET("", perm(acl.PrivTestCaseBrowse), deps.TestCases.List)
	g.GET("/:id", perm(acl.PrivTestCaseBrowse), deps.TestCases.Get)
	g.POST("", perm(acl.PrivTestCaseCreate), deps.TestCases.Create)
	g.PUT("/:id", perm(acl.PrivTestCaseEdit), deps.TestCases.Update)
	g.DELETE("/:id", perm(acl.PrivTestCaseDelete), deps.TestCases.Delete)

	for _, action := range []string{"submit-review", "recall"} {
		g.POST("/:id/"+action, perm(acl.PrivTestCaseEdit), deps.TestCases.Action(action))
	}
	g.POST("/:id/review", perm(acl.PrivTestCaseReview), deps.TestCases.Review)

	// ---- 测试单 ----
	t := api.Group("/testtasks", auth)

	t.GET("", perm(acl.PrivTestTaskBrowse), deps.TestTasks.List)
	t.GET("/:id", perm(acl.PrivTestTaskBrowse), deps.TestTasks.Get)
	t.GET("/:id/runs", perm(acl.PrivTestTaskBrowse), deps.TestTasks.ListRuns)
	t.POST("", perm(acl.PrivTestTaskCreate), deps.TestTasks.Create)
	t.PUT("/:id", perm(acl.PrivTestTaskEdit), deps.TestTasks.Update)
	t.DELETE("/:id", perm(acl.PrivTestTaskDelete), deps.TestTasks.Delete)

	t.POST("/:id/assign", perm(acl.PrivTestTaskAssign), deps.TestTasks.Assign)
	// 开始 / 阻塞 / 重新打开 同属「推进这张单」，共用一个权限点。
	for _, action := range []string{"start", "block", "activate"} {
		t.POST("/:id/"+action, perm(acl.PrivTestTaskStart), deps.TestTasks.Action(action))
	}
	// 结单给的是测试结论，比推进状态更敏感（它会变成测试报告的一部分），单独一个权限点。
	t.POST("/:id/finish", perm(acl.PrivTestTaskFinish), deps.TestTasks.Finish)

	// 单内用例的增删跟着「编辑这张单」的权限：纳入/移出用例就是改单的内容。
	t.POST("/:id/cases", perm(acl.PrivTestTaskEdit), deps.TestTasks.AddCases)
	t.DELETE("/:id/cases/:caseId", perm(acl.PrivTestTaskEdit), deps.TestTasks.RemoveCase)
	t.PUT("/:id/cases/:caseId/assign", perm(acl.PrivTestTaskAssign), deps.TestTasks.AssignCase)

	// 记录执行结果单独一个权限点（testrun-record）：能改测试单元信息的人
	// 不该自动有权给出「这条用例通过了」——后者才是测试报告可信度的来源。
	t.POST("/:id/cases/:caseId/run", perm(acl.PrivTestRunRecord), deps.TestTasks.RecordRun)
}
