package handler

import (
	"github.com/gin-gonic/gin"

	"github.com/xywf221/xuliu/internal/dto"
	"github.com/xywf221/xuliu/internal/pkg/response"
	"github.com/xywf221/xuliu/internal/service"
)

// BugHandler 处理缺陷相关请求。
type BugHandler struct {
	bugs *service.BugService
}

// NewBugHandler 构造缺陷 handler。
func NewBugHandler(bugs *service.BugService) *BugHandler {
	return &BugHandler{bugs: bugs}
}

// List 处理 GET /bugs。
//
// @Summary     缺陷列表
// @Description 分页查询缺陷。所有筛选项都是「空值即不过滤」。
// @Tags        缺陷
// @Produce     json
// @Param       productId   query int    false "按产品过滤"
// @Param       projectId   query int    false "按项目过滤"
// @Param       executionId query int    false "按迭代过滤"
// @Param       moduleId    query int    false "按模块过滤"
// @Param       planId      query int    false "按产品计划过滤"
// @Param       storyId     query int    false "按关联需求过滤"
// @Param       taskId      query int    false "按关联任务过滤"
// @Param       status      query string false "状态：active/resolved/closed"
// @Param       resolution  query string false "解决方案"
// @Param       unresolved  query bool   false "只看还没给出解决方案的缺陷"
// @Param       type        query string false "缺陷类型"
// @Param       severity    query int    false "严重程度 1..4"
// @Param       pri         query int    false "优先级 1..4"
// @Param       confirmed   query int    false "是否已确认：0 未确认，1 已确认"
// @Param       assignedTo  query int    false "按指派人过滤"
// @Param       openedBy    query int    false "按创建人过滤"
// @Param       resolvedBy  query int    false "按解决人过滤"
// @Param       keywords    query string false "标题 / 关键词模糊匹配"
// @Param       deadlineFrom query string false "截止日期下限 YYYY-MM-DD"
// @Param       deadlineTo   query string false "截止日期上限 YYYY-MM-DD"
// @Param       page        query int    false "页码，默认 1"
// @Param       size        query int    false "每页条数，默认 20，最大 200"
// @Param       order       query string false "排序，如 id_desc"
// @Security    BearerAuth
// @Success     200 {object} response.Body{data=[]dto.BugResp}
// @Failure     400 {object} response.Body "参数不合法"
// @Failure     401 {object} response.Body "未登录或令牌已失效"
// @Failure     403 {object} response.Body "无 bug-browse 权限"
// @Router      /bugs [get]
func (h *BugHandler) List(c *gin.Context) {
	req := dto.BugFilterReq{}
	if !response.Bind(c, &req) {
		return
	}
	result, err := h.bugs.List(c.Request.Context(), req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, result)
}

// Counts 处理 GET /bugs/counts，返回各状态的缺陷条数。
//
// 单独一个接口而不是塞进列表响应：列表是分页的，前端手里的 20 条
// 数不出「全部激活缺陷有多少」；而标签值只在筛选条件变化时才需要刷新。
//
// @Summary     缺陷状态计数
// @Description 按同一套筛选条件返回各状态的缺陷条数，用于列表页的标签值。
// @Tags        缺陷
// @Produce     json
// @Param       productId query int    false "按产品过滤"
// @Param       projectId query int    false "按项目过滤"
// @Param       status    query string false "状态"
// @Param       assignedTo query int   false "按指派人过滤"
// @Param       keywords  query string false "标题 / 关键词模糊匹配"
// @Security    BearerAuth
// @Success     200 {object} response.Body{data=dto.BugStatusCountsResp}
// @Failure     401 {object} response.Body "未登录或令牌已失效"
// @Failure     403 {object} response.Body "无 bug-browse 权限"
// @Router      /bugs/counts [get]
func (h *BugHandler) Counts(c *gin.Context) {
	req := dto.BugFilterReq{}
	if !response.Bind(c, &req) {
		return
	}
	counts, err := h.bugs.Counts(c.Request.Context(), req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, counts)
}

// Get 处理 GET /bugs/:id。
//
// @Summary     缺陷详情
// @Description 按 ID 查询单个缺陷的详情。
// @Tags        缺陷
// @Produce     json
// @Param       id   path int true  "缺陷 ID"
// @Security    BearerAuth
// @Success     200 {object} response.Body{data=dto.BugResp}
// @Failure     401 {object} response.Body "未登录或令牌已失效"
// @Failure     403 {object} response.Body "无 bug-browse 权限"
// @Failure     404 {object} response.Body "缺陷不存在"
// @Router      /bugs/{id} [get]
func (h *BugHandler) Get(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	bug, err := h.bugs.Get(c.Request.Context(), id)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, bug)
}

// Create 处理 POST /bugs。
//
// @Summary     创建缺陷
// @Description 新建缺陷，缺陷必属某产品。可以不指派处理人（assignedTo=0 表示待认领）；附件通过 files 关联已上传的文件 id。
// @Tags        缺陷
// @Produce     json
// @Param       body body dto.BugReq true "缺陷信息"
// @Security    BearerAuth
// @Success     201 {object} response.Body{data=dto.BugResp}
// @Failure     400 {object} response.Body "参数不合法"
// @Failure     401 {object} response.Body "未登录或令牌已失效"
// @Failure     403 {object} response.Body "无 bug-create 权限"
// @Router      /bugs [post]
func (h *BugHandler) Create(c *gin.Context) {
	req := dto.BugReq{}
	if !response.Bind(c, &req) {
		return
	}
	bug, err := h.bugs.Create(c.Request.Context(), req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.Created(c, bug)
}

// Update 处理 PUT /bugs/:id。
//
// @Summary     更新缺陷
// @Description 整体覆盖语义：请求里没带的字段会被写回零值（清空关联需求、取消指派等），前端必须提交完整对象。
// @Tags        缺陷
// @Produce     json
// @Param       id   path int true  "缺陷 ID"
// @Param       body body dto.BugReq true "完整缺陷信息"
// @Security    BearerAuth
// @Success     200 {object} response.Body{data=dto.BugResp}
// @Failure     400 {object} response.Body "参数不合法"
// @Failure     401 {object} response.Body "未登录或令牌已失效"
// @Failure     403 {object} response.Body "无 bug-edit 权限"
// @Failure     404 {object} response.Body "缺陷不存在"
// @Router      /bugs/{id} [put]
func (h *BugHandler) Update(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	req := dto.BugReq{}
	if !response.Bind(c, &req) {
		return
	}
	bug, err := h.bugs.Update(c.Request.Context(), id, req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, bug)
}

// Delete 处理 DELETE /bugs/:id。
//
// @Summary     删除缺陷
// @Description 按 ID 删除缺陷，成功返回 204 无响应体。
// @Tags        缺陷
// @Produce     json
// @Param       id   path int true  "缺陷 ID"
// @Security    BearerAuth
// @Success     204 {object} response.Body
// @Failure     401 {object} response.Body "未登录或令牌已失效"
// @Failure     403 {object} response.Body "无 bug-delete 权限"
// @Failure     404 {object} response.Body "缺陷不存在"
// @Router      /bugs/{id} [delete]
func (h *BugHandler) Delete(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	if err := h.bugs.Delete(c.Request.Context(), id); err != nil {
		response.Fail(c, err)
		return
	}
	response.NoContent(c)
}

// Confirm 处理 POST /bugs/:id/confirm。
//
// @Summary     确认缺陷
// @Description 确认未确认的激活缺陷（active 且 confirmed==0 时 confirmed=1）。只改确认标记，不改变状态。
// @Tags        缺陷
// @Produce     json
// @Param       id   path int true "缺陷 ID"
// @Param       body body dto.StateActionReq false "备注（可选）"
// @Security    BearerAuth
// @Success     200 {object} response.Body{data=dto.BugResp}
// @Failure     400 {object} response.Body "参数不合法"
// @Failure     401 {object} response.Body "未登录或令牌已失效"
// @Failure     403 {object} response.Body "无 bug-confirm 权限"
// @Failure     404 {object} response.Body "缺陷不存在"
// @Failure     409 {object} response.Body "当前状态不可确认"
// @Router      /bugs/{id}/confirm [post]
func (h *BugHandler) Confirm(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	req := dto.StateActionReq{}
	if !response.Bind(c, &req) {
		return
	}
	h.apply(c, id, service.BugActionInput{Action: "confirm", Comment: req.Comment})
}

// Assign 处理 POST /bugs/:id/assign。
//
// @Summary     指派缺陷
// @Description 非 closed 状态的缺陷可指派处理人。指派成功会通知被指派人，指派给自己则不通知。
// @Tags        缺陷
// @Produce     json
// @Param       id   path int true "缺陷 ID"
// @Param       body body dto.BugAssignReq true "指派信息"
// @Security    BearerAuth
// @Success     200 {object} response.Body{data=dto.BugResp}
// @Failure     400 {object} response.Body "参数不合法"
// @Failure     401 {object} response.Body "未登录或令牌已失效"
// @Failure     403 {object} response.Body "无 bug-assign 权限"
// @Failure     404 {object} response.Body "缺陷不存在"
// @Failure     409 {object} response.Body "当前状态不可指派"
// @Router      /bugs/{id}/assign [post]
func (h *BugHandler) Assign(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	req := dto.BugAssignReq{}
	if !response.Bind(c, &req) {
		return
	}
	h.apply(c, id, service.BugActionInput{
		Action:     "assign",
		Comment:    req.Comment,
		AssignedTo: req.AssignedTo,
	})
}

// Resolve 处理 POST /bugs/:id/resolve。
//
// @Summary     解决缺陷
// @Description 将 active 缺陷流转为 resolved，resolution 必填（bydesign/duplicate/external/fixed/notrepro/postponed/willnotfix/tostory）；选 duplicate 时 duplicateBugId 必填；同时写入 resolvedBy/resolvedDate/resolvedBuild。
// @Tags        缺陷
// @Produce     json
// @Param       id   path int true "缺陷 ID"
// @Param       body body dto.BugResolveReq true "解决方案"
// @Security    BearerAuth
// @Success     200 {object} response.Body{data=dto.BugResp}
// @Failure     400 {object} response.Body "参数不合法"
// @Failure     401 {object} response.Body "未登录或令牌已失效"
// @Failure     403 {object} response.Body "无 bug-resolve 权限"
// @Failure     404 {object} response.Body "缺陷不存在"
// @Failure     409 {object} response.Body "当前状态不可解决"
// @Router      /bugs/{id}/resolve [post]
func (h *BugHandler) Resolve(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	req := dto.BugResolveReq{}
	if !response.Bind(c, &req) {
		return
	}
	h.apply(c, id, service.BugActionInput{
		Action:         "resolve",
		Comment:        req.Comment,
		Resolution:     req.Resolution,
		ResolvedBuild:  req.ResolvedBuild,
		DuplicateBugID: req.DuplicateBugID,
	})
}

// Close 处理 POST /bugs/:id/close。
//
// @Summary     关闭缺陷
// @Description 仅 resolved 状态的缺陷可以关闭，关闭后缺陷不再流转。
// @Tags        缺陷
// @Produce     json
// @Param       id   path int true "缺陷 ID"
// @Param       body body dto.BugCloseReq false "备注（可选）"
// @Security    BearerAuth
// @Success     200 {object} response.Body{data=dto.BugResp}
// @Failure     400 {object} response.Body "参数不合法"
// @Failure     401 {object} response.Body "未登录或令牌已失效"
// @Failure     403 {object} response.Body "无 bug-close 权限"
// @Failure     404 {object} response.Body "缺陷不存在"
// @Failure     409 {object} response.Body "仅已解决的缺陷可关闭"
// @Router      /bugs/{id}/close [post]
func (h *BugHandler) Close(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	req := dto.BugCloseReq{}
	if !response.Bind(c, &req) {
		return
	}
	h.apply(c, id, service.BugActionInput{Action: "close", Comment: req.Comment})
}

// Activate 处理 POST /bugs/:id/activate。
//
// 这就是「重新激活」：缺陷没有独立的 reopen 动作，activate 承担了
// 回到激活态并累加激活次数的全部职责。
//
// @Summary     重新激活缺陷
// @Description 重新激活缺陷：缺陷没有独立的 reopen 动作，activate 会让 activatedCount++、状态回到 active，并可在同一次请求中完成指派。
// @Tags        缺陷
// @Produce     json
// @Param       id   path int true "缺陷 ID"
// @Param       body body dto.BugActivateReq true "重新激活信息"
// @Security    BearerAuth
// @Success     200 {object} response.Body{data=dto.BugResp}
// @Failure     400 {object} response.Body "参数不合法"
// @Failure     401 {object} response.Body "未登录或令牌已失效"
// @Failure     403 {object} response.Body "无 bug-activate 权限"
// @Failure     404 {object} response.Body "缺陷不存在"
// @Failure     409 {object} response.Body "当前状态不可激活"
// @Router      /bugs/{id}/activate [post]
func (h *BugHandler) Activate(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	req := dto.BugActivateReq{}
	if !response.Bind(c, &req) {
		return
	}
	h.apply(c, id, service.BugActionInput{
		Action:     "activate",
		Comment:    req.Comment,
		AssignedTo: req.AssignedTo,
	})
}

// ToStory 处理 POST /bugs/:id/tostory。
//
// @Summary     缺陷转需求
// @Description 把缺陷转成需求并回填 toStoryId。半是缺陷动作半是需求动作，故需同时具备 bug-tostory 与 story-create。files 是转移而非复制——转过去后缺陷详情页不再显示这些附件。产品开了评审又没给 reviewers 时会建不出需求。
// @Tags        缺陷
// @Produce     json
// @Param       id   path int true "缺陷 ID"
// @Param       body body dto.BugToStoryReq true "转成的需求信息"
// @Security    BearerAuth
// @Success     200 {object} response.Body{data=dto.BugResp}
// @Failure     400 {object} response.Body "参数不合法，如开了评审未提供 reviewers"
// @Failure     401 {object} response.Body "未登录或令牌已失效"
// @Failure     403 {object} response.Body "无 bug-tostory 或 story-create 权限"
// @Failure     404 {object} response.Body "缺陷不存在"
// @Router      /bugs/{id}/tostory [post]
func (h *BugHandler) ToStory(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	req := dto.BugToStoryReq{}
	if !response.Bind(c, &req) {
		return
	}
	h.apply(c, id, service.BugActionInput{
		Action:         "tostory",
		Comment:        req.Comment,
		AssignedTo:     req.AssignedTo,
		StoryProductID: req.ProductID,
		StoryTitle:     req.Title,
		StorySpec:      req.Spec,
		StoryVerify:    req.Verify,
		StoryFiles:     req.Files,
		StoryReviewers: req.Reviewers,
	})
}

// apply 是所有缺陷动作的公共出口。
func (h *BugHandler) apply(c *gin.Context, id uint, in service.BugActionInput) {
	bug, err := h.bugs.Apply(c.Request.Context(), id, in)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, bug)
}
