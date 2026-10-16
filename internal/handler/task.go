package handler

import (
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/xywf221/xuliu/internal/dto"
	"github.com/xywf221/xuliu/internal/pkg/apperr"
	"github.com/xywf221/xuliu/internal/pkg/response"
	"github.com/xywf221/xuliu/internal/service"
)

// TaskHandler 处理任务相关请求。
type TaskHandler struct {
	tasks *service.TaskService
}

// NewTaskHandler 构造任务 handler。
func NewTaskHandler(tasks *service.TaskService) *TaskHandler {
	return &TaskHandler{tasks: tasks}
}

// List 处理 GET /tasks。
//
// @Summary     任务列表
// @Description 分页查询任务，data 为 {list,total,page,size}，list 元素为 dto.TaskResp。所有筛选项都是「空值即不过滤」。
// @Tags        任务
// @Produce     json
// @Param       projectId   query int    false "按项目过滤"
// @Param       executionId query int    false "按迭代过滤"
// @Param       productId   query int    false "按产品过滤"
// @Param       storyId     query int    false "按关联需求过滤"
// @Param       moduleId    query int    false "按模块过滤，传 0 与不传都表示不过滤"
// @Param       status      query string false "状态：wait/doing/pause/done/cancel/closed"
// @Param       type        query string false "任务类型：design/devel/request/test/study/discuss/ui/affair/misc"
// @Param       pri         query int    false "优先级 1..4"
// @Param       assignedTo  query int    false "按指派人过滤"
// @Param       openedBy    query int    false "按创建人过滤"
// @Param       finishedBy  query int    false "按完成人过滤"
// @Param       topOnly     query bool   false "只看顶层任务，不显示子任务"
// @Param       unfinished  query bool   false "只看未完成（未开始/进行中/暂停）的任务"
// @Param       keywords    query string false "标题 / 关键词模糊匹配"
// @Param       deadlineFrom query string false "截止日期下限 YYYY-MM-DD"
// @Param       deadlineTo   query string false "截止日期上限 YYYY-MM-DD"
// @Param       page        query int    false "页码，默认 1"
// @Param       size        query int    false "每页条数，默认 20，最大 200"
// @Param       order       query string false "排序，如 id_desc"
// @Security    BearerAuth
// @Success     200 {object} response.Body "分页对象，data 为 {list,total,page,size}，list 元素为 dto.TaskResp"
// @Failure     400 {object} response.Body "参数不合法"
// @Failure     401 {object} response.Body "未登录或令牌已失效"
// @Failure     403 {object} response.Body "无 task-browse 权限"
// @Router      /tasks [get]
func (h *TaskHandler) List(c *gin.Context) {
	req := dto.TaskFilterReq{}
	if !response.Bind(c, &req) {
		return
	}
	result, err := h.tasks.List(c.Request.Context(), req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, result)
}

// Get 处理 GET /tasks/:id。
//
// @Summary     任务详情
// @Description 返回单个任务，含成员分工（team）、工时记录（efforts）与当前可执行动作（actions）。
// @Tags        任务
// @Produce     json
// @Param       id  path int true "任务 ID"
// @Security    BearerAuth
// @Success     200 {object} response.Body{data=dto.TaskResp}
// @Failure     401 {object} response.Body "未登录或令牌已失效"
// @Failure     403 {object} response.Body "无 task-browse 权限"
// @Failure     404 {object} response.Body "任务不存在或不可见"
// @Router      /tasks/{id} [get]
func (h *TaskHandler) Get(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	task, err := h.tasks.Get(c.Request.Context(), id)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, task)
}

// Create 处理 POST /tasks。
//
// @Summary     新建任务
// @Description 创建任务。多人任务（mode 为 linear/multi）在 team 里给出成员分工；left 留空时按 estimate 初始化。
// @Tags        任务
// @Accept      json
// @Produce     json
// @Param       req body dto.TaskReq true "任务内容"
// @Security    BearerAuth
// @Success     201 {object} response.Body{data=dto.TaskResp}
// @Failure     400 {object} response.Body "参数不合法，如迭代不属于该项目、需求不可见"
// @Failure     401 {object} response.Body "未登录或令牌已失效"
// @Failure     403 {object} response.Body "无 task-create 权限"
// @Router      /tasks [post]
func (h *TaskHandler) Create(c *gin.Context) {
	req := dto.TaskReq{}
	if !response.Bind(c, &req) {
		return
	}
	task, err := h.tasks.Create(c.Request.Context(), req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.Created(c, task)
}

// Update 处理 PUT /tasks/:id。
//
// @Summary     编辑任务
// @Description 整体更新任务。remaining（left）只在显式给出时改写，避免编辑顺手覆盖「还剩多少」；version 为乐观锁版本号，必须原样带回。
// @Tags        任务
// @Accept      json
// @Produce     json
// @Param       id  path int           true "任务 ID"
// @Param       req body dto.TaskReq   true "任务内容"
// @Security    BearerAuth
// @Success     200 {object} response.Body{data=dto.TaskResp}
// @Failure     400 {object} response.Body "参数不合法"
// @Failure     401 {object} response.Body "未登录或令牌已失效"
// @Failure     403 {object} response.Body "无 task-edit 权限"
// @Failure     404 {object} response.Body "任务不存在或不可见"
// @Failure     409 {object} response.Body "版本冲突，任务已被他人修改"
// @Router      /tasks/{id} [put]
func (h *TaskHandler) Update(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	req := dto.TaskReq{}
	if !response.Bind(c, &req) {
		return
	}
	task, err := h.tasks.Update(c.Request.Context(), id, req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, task)
}

// Delete 处理 DELETE /tasks/:id。
//
// @Summary     删除任务
// @Description 删除任务，成功无返回体。
// @Tags        任务
// @Produce     json
// @Param       id  path int true "任务 ID"
// @Security    BearerAuth
// @Success     204 {string} string "无内容"
// @Failure     401 {object} response.Body "未登录或令牌已失效"
// @Failure     403 {object} response.Body "无 task-delete 权限"
// @Failure     404 {object} response.Body "任务不存在或不可见"
// @Router      /tasks/{id} [delete]
func (h *TaskHandler) Delete(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	if err := h.tasks.Delete(c.Request.Context(), id); err != nil {
		response.Fail(c, err)
		return
	}
	response.NoContent(c)
}

// Action 返回某个状态动作的 handler（开始/暂停/继续/重新激活）。
//
// 与项目一样，动作名由路由表给出：每个动作挂自己的权限点，
// 而不是一个 /actions/:action 配一个笼统的权限。
//
// 这个闭包支撑 4 条路由，入参只有备注，响应都是 dto.TaskResp：
//
//	POST /tasks/{id}/start    开始
//	POST /tasks/{id}/pause    暂停
//	POST /tasks/{id}/restart  继续
//	POST /tasks/{id}/activate 重新激活
//
// @Summary     推进任务（开始/暂停/继续/重新激活）
// @Description 通用状态动作，具体动作由路由决定，请求体只有备注。四条路由同属「推进任务」，共用 task-start 权限点。
// @Tags        任务
// @Accept      json
// @Produce     json
// @Param       id  path int                  true "任务 ID"
// @Param       req body dto.StateActionReq   false "动作备注"
// @Security    BearerAuth
// @Success     200 {object} response.Body{data=dto.TaskResp}
// @Failure     400 {object} response.Body "参数不合法"
// @Failure     401 {object} response.Body "未登录或令牌已失效"
// @Failure     403 {object} response.Body "无 task-start 权限"
// @Failure     404 {object} response.Body "任务不存在或不可见"
// @Failure     409 {object} response.Body "当前状态不允许该动作"
// @Router      /tasks/{id}/start [post]
// @Router      /tasks/{id}/pause [post]
// @Router      /tasks/{id}/restart [post]
// @Router      /tasks/{id}/activate [post]
func (h *TaskHandler) Action(name string) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := pathID(c)
		if !ok {
			return
		}
		req := dto.StateActionReq{}
		if !response.Bind(c, &req) {
			return
		}
		h.apply(c, id, service.TaskActionInput{Action: name, Comment: req.Comment})
	}
}

// Assign 处理 POST /tasks/:id/assign。
//
// @Summary     指派任务
// @Description 把任务指派给某个成员；多人任务会按成员分工落成员状态。
// @Tags        任务
// @Accept      json
// @Produce     json
// @Param       id  path int                true "任务 ID"
// @Param       req body dto.TaskAssignReq  true "指派对象"
// @Security    BearerAuth
// @Success     200 {object} response.Body{data=dto.TaskResp}
// @Failure     400 {object} response.Body "参数不合法，如被指派人不在项目/迭代成员里"
// @Failure     401 {object} response.Body "未登录或令牌已失效"
// @Failure     403 {object} response.Body "无 task-assign 权限"
// @Failure     404 {object} response.Body "任务不存在或不可见"
// @Router      /tasks/{id}/assign [post]
func (h *TaskHandler) Assign(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	req := dto.TaskAssignReq{}
	if !response.Bind(c, &req) {
		return
	}
	h.apply(c, id, service.TaskActionInput{
		Action:     "assign",
		Comment:    req.Comment,
		AssignedTo: req.AssignedTo,
	})
}

// Finish 处理 POST /tasks/:id/finish。
//
// @Summary     完成任务
// @Description 完成与报工时是同一次调用：consumed 给出本次干完的工时，服务端在同一事务里先登记工时、再校验剩余工时是否归零，只允许剩余工时为 0 的任务完成。consumed 留空表示只改状态不额外报工，此时若任务还剩工时会被拒绝。
// @Tags        任务
// @Accept      json
// @Produce     json
// @Param       id  path int                true "任务 ID"
// @Param       req body dto.TaskFinishReq  true "本次消耗工时与备注"
// @Security    BearerAuth
// @Success     200 {object} response.Body{data=dto.TaskResp}
// @Failure     400 {object} response.Body "参数不合法，或本次消耗工时超过剩余工时"
// @Failure     401 {object} response.Body "未登录或令牌已失效"
// @Failure     403 {object} response.Body "无 task-finish 权限"
// @Failure     404 {object} response.Body "任务不存在或不可见"
// @Failure     409 {object} response.Body "task_left_not_zero：剩余工时不为 0，不允许完成，请先登记工时或把剩余工时改为 0"
// @Router      /tasks/{id}/finish [post]
func (h *TaskHandler) Finish(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	req := dto.TaskFinishReq{}
	if !response.Bind(c, &req) {
		return
	}
	h.apply(c, id, service.TaskActionInput{
		Action:   "finish",
		Comment:  req.Comment,
		Consumed: req.Consumed,
	})
}

// Cancel 处理 POST /tasks/:id/cancel。
//
// @Summary     取消任务
// @Description 取消任务并清零剩余工时，工时记录保留。
// @Tags        任务
// @Accept      json
// @Produce     json
// @Param       id  path int                true "任务 ID"
// @Param       req body dto.TaskCancelReq  true "取消备注"
// @Security    BearerAuth
// @Success     200 {object} response.Body{data=dto.TaskResp}
// @Failure     400 {object} response.Body "参数不合法"
// @Failure     401 {object} response.Body "未登录或令牌已失效"
// @Failure     403 {object} response.Body "无 task-cancel 权限"
// @Failure     404 {object} response.Body "任务不存在或不可见"
// @Router      /tasks/{id}/cancel [post]
func (h *TaskHandler) Cancel(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	req := dto.TaskCancelReq{}
	if !response.Bind(c, &req) {
		return
	}
	h.apply(c, id, service.TaskActionInput{Action: "cancel", Comment: req.Comment})
}

// Close 处理 POST /tasks/:id/close。
//
// @Summary     关闭任务
// @Description 关闭任务（终态）。已取消的任务也可以直接关闭。
// @Tags        任务
// @Accept      json
// @Produce     json
// @Param       id  path int               true "任务 ID"
// @Param       req body dto.TaskCloseReq  true "关闭备注"
// @Security    BearerAuth
// @Success     200 {object} response.Body{data=dto.TaskResp}
// @Failure     400 {object} response.Body "参数不合法"
// @Failure     401 {object} response.Body "未登录或令牌已失效"
// @Failure     403 {object} response.Body "无 task-close 权限"
// @Failure     404 {object} response.Body "任务不存在或不可见"
// @Failure     409 {object} response.Body "当前状态不允许关闭"
// @Router      /tasks/{id}/close [post]
func (h *TaskHandler) Close(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	req := dto.TaskCloseReq{}
	if !response.Bind(c, &req) {
		return
	}
	h.apply(c, id, service.TaskActionInput{Action: "close", Comment: req.Comment})
}

// apply 是所有任务动作的公共出口。
func (h *TaskHandler) apply(c *gin.Context, id uint, in service.TaskActionInput) {
	task, err := h.tasks.Apply(c.Request.Context(), id, in)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, task)
}

// Kanban 处理 GET /tasks/kanban?projectId=1&executionId=2。
//
// @Summary     任务看板
// @Description 按状态分列返回任务。看板是任务的另一种视图而非某个任务的子资源，故路径为 /tasks/kanban。data 为 dto.KanbanResp，含 scope（project/execution）与 columns（每列有 status、label、count、hours 工时三元组、tasks）。executionId 为 0 时看整个项目，否则看该迭代。
// @Tags        任务
// @Produce     json
// @Param       projectId   query int true  "项目 ID"
// @Param       executionId query int false "迭代 ID，为 0 或不传表示看整个项目"
// @Security    BearerAuth
// @Success     200 {object} response.Body{data=dto.KanbanResp}
// @Failure     400 {object} response.Body "参数不合法，如迭代不属于该项目"
// @Failure     401 {object} response.Body "未登录或令牌已失效"
// @Failure     403 {object} response.Body "无 task-browse 权限"
// @Failure     404 {object} response.Body "项目或迭代不存在或不可见"
// @Router      /tasks/kanban [get]
func (h *TaskHandler) Kanban(c *gin.Context) {
	projectID, ok := queryUint(c, "projectId")
	if !ok {
		return
	}
	executionID, ok := queryUint(c, "executionId")
	if !ok {
		return
	}
	board, err := h.tasks.Kanban(c.Request.Context(), projectID, executionID)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, board)
}

// ---------------------------------------------------------------------------
// 工时
// ---------------------------------------------------------------------------

// RecordEffort 处理 POST /tasks/:id/efforts。
//
// @Summary     登记工时
// @Description 为任务登记一条工时记录，返回刷新后的任务详情。left 给出「登记之后」的剩余工时，留空时按「原剩余 − 本次消耗」推算；多人任务里 userId 留空表示算在当前登录用户头上。
// @Tags        工时
// @Accept      json
// @Produce     json
// @Param       id  path int           true "任务 ID"
// @Param       req body dto.EffortReq true "工时内容"
// @Security    BearerAuth
// @Success     201 {object} response.Body{data=dto.TaskResp}
// @Failure     400 {object} response.Body "参数不合法，或本次消耗工时超过剩余工时"
// @Failure     401 {object} response.Body "未登录或令牌已失效"
// @Failure     403 {object} response.Body "无 effort-record 权限"
// @Failure     404 {object} response.Body "任务不存在或不可见"
// @Router      /tasks/{id}/efforts [post]
func (h *TaskHandler) RecordEffort(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	req := dto.EffortReq{}
	if !response.Bind(c, &req) {
		return
	}
	task, err := h.tasks.RecordEffort(c.Request.Context(), id, req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.Created(c, task)
}

// ListEfforts 处理 GET /tasks/:id/efforts。
//
// @Summary     任务工时明细
// @Description 返回某个任务的全部工时记录，不分页，data 为 dto.EffortResp 数组。
// @Tags        工时
// @Produce     json
// @Param       id  path int true "任务 ID"
// @Security    BearerAuth
// @Success     200 {object} response.Body{data=[]dto.EffortResp}
// @Failure     401 {object} response.Body "未登录或令牌已失效"
// @Failure     403 {object} response.Body "无 task-browse 权限"
// @Failure     404 {object} response.Body "任务不存在或不可见"
// @Router      /tasks/{id}/efforts [get]
func (h *TaskHandler) ListEfforts(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	task, err := h.tasks.Get(c.Request.Context(), id)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, task.Efforts)
}

// AllEfforts 处理 GET /efforts，跨任务查询工时明细。
//
// @Summary     工时明细（跨任务）
// @Description 分页查询工时记录，用于「我的工时」与「项目工时」，data 为 {list,total,page,size}，list 元素为 dto.EffortResp。所有筛选项都是「空值即不过滤」。
// @Tags        工时
// @Produce     json
// @Param       objectType  query string false "对象类型，如 task"
// @Param       objectId    query int    false "对象 ID"
// @Param       projectId   query int    false "按项目过滤"
// @Param       executionId query int    false "按迭代过滤"
// @Param       userId      query int    false "按报工人过滤"
// @Param       dateFrom    query string false "报工日期下限 YYYY-MM-DD"
// @Param       dateTo      query string false "报工日期上限 YYYY-MM-DD"
// @Param       page        query int    false "页码，默认 1"
// @Param       size        query int    false "每页条数，默认 20，最大 200"
// @Param       order       query string false "排序，如 date_desc"
// @Security    BearerAuth
// @Success     200 {object} response.Body "分页对象，data 为 {list,total,page,size}，list 元素为 dto.EffortResp"
// @Failure     400 {object} response.Body "参数不合法"
// @Failure     401 {object} response.Body "未登录或令牌已失效"
// @Failure     403 {object} response.Body "无 effort-record 权限"
// @Router      /efforts [get]
func (h *TaskHandler) AllEfforts(c *gin.Context) {
	req := dto.EffortFilterReq{}
	if !response.Bind(c, &req) {
		return
	}
	result, err := h.tasks.ListEfforts(c.Request.Context(), req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, result)
}

// queryUint 解析可选的 uint 查询参数。
//
// 用指针而不是直接 c.Query()：调用方要能区分「没传」与「传了 0」，
// 前者是「不限」，后者是「未分类」这类真实取值。
func queryUint(c *gin.Context, name string) (uint, bool) {
	raw := c.Query(name)
	if raw == "" {
		return 0, true
	}
	parsed, err := strconv.ParseUint(raw, 10, 64)
	if err != nil {
		response.Fail(c, apperr.Newf(apperr.InvalidParam, "查询参数 %s 不合法：%s", name, raw))
		return 0, false
	}
	return uint(parsed), true
}
