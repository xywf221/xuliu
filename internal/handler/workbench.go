package handler

import (
	"github.com/gin-gonic/gin"

	"github.com/xywf221/xuliu/internal/dto"
	"github.com/xywf221/xuliu/internal/pkg/response"
	"github.com/xywf221/xuliu/internal/service"
)

// NoticeHandler 处理站内通知相关请求。
type NoticeHandler struct {
	notices *service.NoticeService
}

// NewNoticeHandler 构造通知 handler。
func NewNoticeHandler(notices *service.NoticeService) *NoticeHandler {
	return &NoticeHandler{notices: notices}
}

// List 处理 GET /notices。
//
// @Summary     我的通知列表
// @Description 分页查询当前登录人的通知，data 为分页对象 {list,total,page,size}，list 元素为 dto.NoticeResp。
// @Description 这一组接口都不收 userId 参数：能查别人的提醒就等于开了一个读别人通知的口子。
// @Tags        通知
// @Produce     json
// @Param       unread     query bool   false "true 只看未读、false 只看已读、不传全都看"
// @Param       objectType query string false "按对象类型过滤，如 story/task/bug"
// @Param       objectId   query int    false "按对象 ID 过滤"
// @Param       keywords   query string false "标题/内容模糊匹配"
// @Param       page       query int    false "页码，默认 1"
// @Param       size       query int    false "每页条数，默认 20，最大 200"
// @Param       order      query string false "排序，如 id_desc"
// @Security    BearerAuth
// @Success     200 {object} response.Body "分页对象，data 为 {list,total,page,size}，list 元素为 dto.NoticeResp"
// @Failure     400 {object} response.Body "参数不合法"
// @Failure     401 {object} response.Body "未登录或令牌已失效"
// @Failure     403 {object} response.Body "无 my-work 权限"
// @Router      /notices [get]
func (h *NoticeHandler) List(c *gin.Context) {
	req := dto.NoticeFilterReq{}
	if !response.Bind(c, &req) {
		return
	}
	result, err := h.notices.List(c.Request.Context(), req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, result)
}

// UnreadCount 处理 GET /notices/unread-count。
//
// 单独一个接口是因为它是被轮询的那个：前端只想知道红点上的数字，
// 不该为此拉一遍通知列表。
//
// @Summary     我的未读数
// @Description 只返回未读通知数量（data 为 {unread: n}），供前端红点轮询。
// @Tags        通知
// @Produce     json
// @Security    BearerAuth
// @Success     200 {object} response.Body "data 为 dto.NoticeCountResp，字段 unread 是未读条数"
// @Failure     401 {object} response.Body "未登录或令牌已失效"
// @Failure     403 {object} response.Body "无 my-work 权限"
// @Router      /notices/unread-count [get]
func (h *NoticeHandler) UnreadCount(c *gin.Context) {
	count, err := h.notices.UnreadCount(c.Request.Context())
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, count)
}

// MarkRead 处理 POST /notices/read。
//
// 用 POST 而不是 PUT /notices/:id/read：这个动作用来「标记若干条」或
// 「全部标记」，前者在 URL 里表达不出来，后者根本没有 id。
// 返回剩下的未读数，前端拿到就能直接更新红点，不用再问一次。
//
// @Summary     标记通知已读
// @Description 把指定若干条（ids）或全部（all=true）标记为已读，只影响当前登录人自己的通知。
// @Description 返回标记后剩下的未读数，前端可直接刷新红点。ids 与 all 至少给一个。
// @Tags        通知
// @Accept      json
// @Produce     json
// @Param       body body dto.NoticeReadReq true "要标记的通知 id 列表，或 all=true"
// @Security    BearerAuth
// @Success     200 {object} response.Body "data 为 dto.NoticeCountResp，即标记后的剩余未读数"
// @Failure     400 {object} response.Body "ids 与 all 都没给，或参数不合法"
// @Failure     401 {object} response.Body "未登录或令牌已失效"
// @Failure     403 {object} response.Body "无 my-work 权限"
// @Router      /notices/read [post]
func (h *NoticeHandler) MarkRead(c *gin.Context) {
	req := dto.NoticeReadReq{}
	if !response.Bind(c, &req) {
		return
	}
	count, err := h.notices.MarkRead(c.Request.Context(), req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, count)
}

// Delete 处理 DELETE /notices/:id。
//
// @Summary     删除一条通知
// @Description 删除当前登录人自己的某条通知。「不存在」与「不属于你」一律报 404 ——
// @Description 区分开等于告诉别人这条通知存在，只是他看不到。
// @Tags        通知
// @Produce     json
// @Param       id path int true "通知 ID"
// @Security    BearerAuth
// @Success     204 {string} string "无内容"
// @Failure     400 {object} response.Body "路径参数不合法"
// @Failure     401 {object} response.Body "未登录或令牌已失效"
// @Failure     403 {object} response.Body "无 my-work 权限"
// @Failure     404 {object} response.Body "通知不存在，或不属于当前用户"
// @Router      /notices/{id} [delete]
func (h *NoticeHandler) Delete(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	if err := h.notices.Delete(c.Request.Context(), id); err != nil {
		response.Fail(c, err)
		return
	}
	response.NoContent(c)
}

// WorkbenchHandler 处理工作台与燃尽图请求。
type WorkbenchHandler struct {
	workbench *service.WorkbenchService
}

// NewWorkbenchHandler 构造工作台 handler。
func NewWorkbenchHandler(workbench *service.WorkbenchService) *WorkbenchHandler {
	return &WorkbenchHandler{workbench: workbench}
}

// Get 处理 GET /workbench。
//
// @Summary     工作台首屏
// @Description 一次返回首屏所需的全部数据（data 为 dto.WorkbenchResp）：counts 计数、
// @Description tasks/bugs/reviews 三份「等我动手」的清单、notices 最近未读、efforts 我的工时概览。
// @Description 做成聚合接口是因为这几块共用同一份可见性判定，且首屏不该一块一块地长出来。
// @Tags        工作台
// @Produce     json
// @Security    BearerAuth
// @Success     200 {object} response.Body "data 为 dto.WorkbenchResp"
// @Failure     401 {object} response.Body "未登录或令牌已失效"
// @Failure     403 {object} response.Body "无 my-work 权限"
// @Router      /workbench [get]
func (h *WorkbenchHandler) Get(c *gin.Context) {
	data, err := h.workbench.Get(c.Request.Context())
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, data)
}

// Burndown 处理 GET /workbench/burndown。
//
// 走表单参数而不是 JSON body：这是一个只读查询，参数应该留在 URL 里，
// 便于直接贴给别人、也便于浏览器刷新重放。
//
// @Summary     燃尽图
// @Description 画一条剩余工时随日期递减的曲线（data 为 dto.BurndownResp）。给了 executionId 就画迭代
// @Description （scope=execution），否则画项目（scope=project）；两者都不给会报参数错误。
// @Description 曲线取自 pm_effort 里逐日的 left 快照，不是任务的当前值；任务从它被创建那天起才计入，
// @Description 所以中途加活会表现为一次向上跳，而不是曲线整体变形。
// @Tags        工作台
// @Produce     json
// @Param       projectId   query int    false "项目 ID，与 executionId 二选一"
// @Param       executionId query int    false "迭代 ID，优先用它画曲线"
// @Param       begin       query string false "起始日期 YYYY-MM-DD，不传则用对象自身的起止日期"
// @Param       end         query string false "结束日期 YYYY-MM-DD"
// @Security    BearerAuth
// @Success     200 {object} response.Body "data 为 dto.BurndownResp，days 逐日一点、缺数据补 0"
// @Failure     400 {object} response.Body "projectId 与 executionId 都没给，或日期格式不合法"
// @Failure     401 {object} response.Body "未登录或令牌已失效"
// @Failure     403 {object} response.Body "无 report-browse 权限，或该私有项目/迭代不可见"
// @Failure     404 {object} response.Body "项目/迭代不存在，或所选迭代不属于该项目"
// @Router      /workbench/burndown [get]
func (h *WorkbenchHandler) Burndown(c *gin.Context) {
	req := dto.BurndownReq{}
	if !response.Bind(c, &req) {
		return
	}
	data, err := h.workbench.Burndown(c.Request.Context(), req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, data)
}
