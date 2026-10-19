package handler

import (
	"github.com/gin-gonic/gin"

	"github.com/xywf221/xuliu/internal/dto"
	"github.com/xywf221/xuliu/internal/pkg/apperr"
	"github.com/xywf221/xuliu/internal/pkg/response"
	"github.com/xywf221/xuliu/internal/service"
	"github.com/xywf221/xuliu/internal/workflow"
)

// TestCaseHandler 处理测试用例相关请求。
type TestCaseHandler struct {
	cases *service.TestCaseService
}

// NewTestCaseHandler 构造用例 handler。
func NewTestCaseHandler(cases *service.TestCaseService) *TestCaseHandler {
	return &TestCaseHandler{cases: cases}
}

// List 处理 GET /testcases。
//
// @Summary     用例列表
// @Description 分页查询测试用例，data 为分页对象 {list,total,page,size}，list 元素为 dto.TestCaseResp。
// @Description 只能看到自己有权限的产品下的用例（可见性按产品判定，与缺陷同一口径）。
// @Tags        测试用例
// @Produce     json
// @Param       productId  query int    false "按产品过滤"
// @Param       moduleId   query int    false "按模块过滤"
// @Param       storyId    query int    false "按溯源需求过滤"
// @Param       stage      query string false "评审阶段：wait / reviewing / confirmed / rejected"
// @Param       status     query string false "可执行性：normal / blocked / investigate"
// @Param       type       query string false "用例类型，如 feature / interface / performance"
// @Param       pri        query int    false "优先级 1-4"
// @Param       openedBy   query int    false "创建人"
// @Param       reviewedBy query int    false "评审人"
// @Param       keywords   query string false "在标题、关键词、编号上做模糊匹配"
// @Param       page       query int    false "页码，默认 1"
// @Param       size       query int    false "每页条数，默认 20，最大 200"
// @Param       order      query string false "排序，如 id_desc"
// @Security    BearerAuth
// @Success     200 {object} response.Body "分页对象，data 为 {list,total,page,size}，list 元素为 dto.TestCaseResp"
// @Failure     400 {object} response.Body "参数不合法"
// @Failure     401 {object} response.Body "未登录或令牌已失效"
// @Failure     403 {object} response.Body "无 testcase-browse 权限"
// @Router      /testcases [get]
func (h *TestCaseHandler) List(c *gin.Context) {
	req := dto.TestCaseFilterReq{}
	if !response.Bind(c, &req) {
		return
	}
	result, err := h.cases.List(c.Request.Context(), req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, result)
}

// Get 处理 GET /testcases/:id。
//
// @Summary     用例详情
// @Description 返回用例全文与全部步骤（data 为 dto.TestCaseDetailResp）。步骤按 order 升序。
// @Tags        测试用例
// @Produce     json
// @Param       id path int true "用例 ID"
// @Security    BearerAuth
// @Success     200 {object} response.Body "用例详情，data 为 dto.TestCaseDetailResp"
// @Failure     400 {object} response.Body "路径参数不合法"
// @Failure     401 {object} response.Body "未登录或令牌已失效"
// @Failure     403 {object} response.Body "无 testcase-browse 权限，或用例所属产品不可见"
// @Failure     404 {object} response.Body "用例不存在"
// @Router      /testcases/{id} [get]
func (h *TestCaseHandler) Get(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	detail, err := h.cases.Get(c.Request.Context(), id)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, detail)
}

// Create 处理 POST /testcases。
//
// @Summary     新建用例
// @Description 创建用例及其步骤，data 为 dto.TestCaseResp。初始评审阶段是 wait（待评审）、版本为 1。
// @Description 步骤至少一条；顺序由后端按提交顺序重排为 1..n，前端传来的序号不作数。
// @Description 模块与溯源需求都必须属于所选产品，否则报参数错误。
// @Tags        测试用例
// @Accept      json
// @Produce     json
// @Param       body body dto.TestCaseReq true "新建用例请求"
// @Security    BearerAuth
// @Success     201 {object} response.Body "创建成功，data 为 dto.TestCaseResp"
// @Failure     400 {object} response.Body "参数不合法（含步骤为空、取值不在枚举内）"
// @Failure     401 {object} response.Body "未登录或令牌已失效"
// @Failure     403 {object} response.Body "无 testcase-create 权限，或该产品不可见"
// @Failure     404 {object} response.Body "产品或所选需求不存在"
// @Failure     409 {object} response.Body "产品已关闭，不能再维护用例"
// @Router      /testcases [post]
func (h *TestCaseHandler) Create(c *gin.Context) {
	req := dto.TestCaseReq{}
	if !response.Bind(c, &req) {
		return
	}
	created, err := h.cases.Create(c.Request.Context(), req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.Created(c, created)
}

// Update 处理 PUT /testcases/:id。
//
// @Summary     编辑用例
// @Description 整体覆盖用例内容与步骤，data 为 dto.TestCaseResp。请求里没带的字段会被写回零值。
// @Description 内容真的发生变化时版本 +1；已确认（confirmed）的用例会被打回待评审 ——
// @Description 评审针对的是那份内容，内容换了旧结论不该继续生效。
// @Description 评审中（reviewing）不允许编辑，要先撤回评审。
// @Tags        测试用例
// @Accept      json
// @Produce     json
// @Param       id   path int             true "用例 ID"
// @Param       body body dto.TestCaseReq true "用例全量内容"
// @Security    BearerAuth
// @Success     200 {object} response.Body "编辑成功，data 为 dto.TestCaseResp"
// @Failure     400 {object} response.Body "参数不合法（含步骤为空）"
// @Failure     401 {object} response.Body "未登录或令牌已失效"
// @Failure     403 {object} response.Body "无 testcase-edit 权限"
// @Failure     404 {object} response.Body "用例不存在"
// @Failure     409 {object} response.Body "评审中的用例不能编辑"
// @Router      /testcases/{id} [put]
func (h *TestCaseHandler) Update(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	req := dto.TestCaseReq{}
	if !response.Bind(c, &req) {
		return
	}
	updated, err := h.cases.Update(c.Request.Context(), id, req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, updated)
}

// Delete 处理 DELETE /testcases/:id。
//
// @Summary     删除用例
// @Description 软删除用例，并连带删除它的步骤。data 无内容。
// @Tags        测试用例
// @Produce     json
// @Param       id path int true "用例 ID"
// @Security    BearerAuth
// @Success     204 {string} string "无内容"
// @Failure     400 {object} response.Body "路径参数不合法"
// @Failure     401 {object} response.Body "未登录或令牌已失效"
// @Failure     403 {object} response.Body "无 testcase-delete 权限"
// @Failure     404 {object} response.Body "用例不存在"
// @Router      /testcases/{id} [delete]
func (h *TestCaseHandler) Delete(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	if err := h.cases.Delete(c.Request.Context(), id); err != nil {
		response.Fail(c, err)
		return
	}
	response.NoContent(c)
}

// Action 返回执行指定评审动作的 handler（提交评审 / 撤回评审）。
//
// 动作名由路由决定而不是参数：每个动作要挂的权限点不同
// （提交要 testcase-edit、出结论要 testcase-review），写在路由表上才一眼可见。
//
// @Summary     提交或撤回用例评审
// @Description 把用例送进评审（submitreview：wait/rejected → reviewing）或撤回评审（recall：reviewing → wait）。
// @Description 请求体只有一个备注。data 为 dto.TestCaseResp。
// @Tags        测试用例
// @Accept      json
// @Produce     json
// @Param       id   path int                 true "用例 ID"
// @Param       body body dto.StateActionReq  false "备注"
// @Security    BearerAuth
// @Success     200 {object} response.Body{data=dto.TestCaseResp}
// @Failure     400 {object} response.Body "参数不合法"
// @Failure     401 {object} response.Body "未登录或令牌已失效"
// @Failure     403 {object} response.Body "无 testcase-edit 权限"
// @Failure     404 {object} response.Body "用例不存在"
// @Failure     409 {object} response.Body "当前评审阶段不允许该动作"
// @Router      /testcases/{id}/submit-review [post]
// @Router      /testcases/{id}/recall [post]
func (h *TestCaseHandler) Action(name string) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := pathID(c)
		if !ok {
			return
		}
		req := dto.StateActionReq{}
		if !response.Bind(c, &req) {
			return
		}
		out, err := h.cases.Apply(c.Request.Context(), id, name, req.Comment)
		if err != nil {
			response.Fail(c, err)
			return
		}
		response.OK(c, out)
	}
}

// Review 处理 POST /testcases/:id/review。
//
// @Summary     给出用例评审结论
// @Description 对评审中的用例出结论：pass → confirmed（可纳入测试单），reject → rejected。
// @Description 评审人与评审时间由服务端按当前登录人写入，不接受请求里指定。
// @Tags        测试用例
// @Accept      json
// @Produce     json
// @Param       id   path int              true "用例 ID"
// @Param       body body dto.CaseReviewReq true "评审结论"
// @Security    BearerAuth
// @Success     200 {object} response.Body "结论已记录，data 为 dto.TestCaseResp"
// @Failure     400 {object} response.Body "结论取值不合法（只接受 pass / reject）"
// @Failure     401 {object} response.Body "未登录或令牌已失效"
// @Failure     403 {object} response.Body "无 testcase-review 权限"
// @Failure     404 {object} response.Body "用例不存在"
// @Failure     409 {object} response.Body "用例不在评审中"
// @Router      /testcases/{id}/review [post]
func (h *TestCaseHandler) Review(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	req := dto.CaseReviewReq{}
	if !response.Bind(c, &req) {
		return
	}

	// 结论名交给状态机归一并校验：路由上的动作与机器里登记的必须一致，
	// 在这里再写一遍映射表的话，加一个结论就有两处要改。
	action, known := workflow.TestCaseMachine.Canonical(req.Result)
	if !known {
		// DTO 上的 oneof 已经挡过一次，这里再挡一道：状态机才是动作名的权威，
		// 万一以后加了新结论而忘了同步校验，这里会明确拒绝而不是静默走错分支。
		response.Fail(c, apperr.Newf(apperr.InvalidParam, "评审结论取值不合法：%s", req.Result))
		return
	}

	out, err := h.cases.Apply(c.Request.Context(), id, action, req.Comment)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, out)
}
