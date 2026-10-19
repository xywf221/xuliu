package handler

import (
	"github.com/gin-gonic/gin"

	"github.com/xywf221/xuliu/internal/dto"
	"github.com/xywf221/xuliu/internal/pkg/response"
	"github.com/xywf221/xuliu/internal/service"
)

// TestTaskHandler 处理测试单与执行记录相关请求。
type TestTaskHandler struct {
	tasks *service.TestTaskService
}

// NewTestTaskHandler 构造测试单 handler。
func NewTestTaskHandler(tasks *service.TestTaskService) *TestTaskHandler {
	return &TestTaskHandler{tasks: tasks}
}

// List 处理 GET /testtasks。
//
// @Summary     测试单列表
// @Description 分页查询测试单，data 为分页对象 {list,total,page,size}，list 元素为 dto.TestTaskResp。
// @Description 每张单都带着单内用例的执行汇总（通过/失败/阻塞/未跑），列表页不必逐行再查。
// @Tags        测试单
// @Produce     json
// @Param       productId   query int    false "按产品过滤"
// @Param       projectId   query int    false "按项目过滤"
// @Param       executionId query int    false "按迭代过滤"
// @Param       buildId     query int    false "按被测构建过滤"
// @Param       status      query string false "状态：wait / doing / blocked / closed"
// @Param       result      query string false "结单结论：pass / fail / blocked"
// @Param       pri         query int    false "优先级 1-4"
// @Param       assignedTo  query int    false "负责人"
// @Param       openedBy    query int    false "提单人"
// @Param       unfinished  query bool   false "只看未结单"
// @Param       noCase      query bool   false "只看还没纳入用例的单"
// @Param       keywords    query string false "名称/说明模糊匹配"
// @Param       page        query int    false "页码，默认 1"
// @Param       size        query int    false "每页条数，默认 20，最大 200"
// @Param       order       query string false "排序，如 opened_desc"
// @Security    BearerAuth
// @Success     200 {object} response.Body "分页对象，data 为 {list,total,page,size}，list 元素为 dto.TestTaskResp"
// @Failure     400 {object} response.Body "参数不合法"
// @Failure     401 {object} response.Body "未登录或令牌已失效"
// @Failure     403 {object} response.Body "无 testtask-browse 权限"
// @Router      /testtasks [get]
func (h *TestTaskHandler) List(c *gin.Context) {
	req := dto.TestTaskFilterReq{}
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

// Get 处理 GET /testtasks/:id。
//
// @Summary     测试单详情
// @Description 返回测试单与单内用例清单（data 为 dto.TestTaskDetailResp）。
// @Description 每条用例会同时给出「纳入时锁定的版本」与「用例此刻的版本」，
// @Description 两者不一致说明纳入之后用例又改过，本单的结果是对着旧版跑的。
// @Tags        测试单
// @Produce     json
// @Param       id path int true "测试单 ID"
// @Security    BearerAuth
// @Success     200 {object} response.Body "测试单详情，data 为 dto.TestTaskDetailResp"
// @Failure     400 {object} response.Body "路径参数不合法"
// @Failure     401 {object} response.Body "未登录或令牌已失效"
// @Failure     403 {object} response.Body "无 testtask-browse 权限，或该单不可见"
// @Failure     404 {object} response.Body "测试单不存在"
// @Router      /testtasks/{id} [get]
func (h *TestTaskHandler) Get(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	detail, err := h.tasks.Get(c.Request.Context(), id)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, detail)
}

// Create 处理 POST /testtasks。
//
// @Summary     新建测试单
// @Description 建一张测试单，初始状态 wait；caseIds 可一并纳入用例（也可建完再加），data 为 dto.TestTaskResp。
// @Description 迭代必须属于所选项目；构建必须属于所选产品。只有评审通过（confirmed）的用例能被纳入。
// @Tags        测试单
// @Accept      json
// @Produce     json
// @Param       body body dto.TestTaskReq true "新建测试单请求"
// @Security    BearerAuth
// @Success     201 {object} response.Body "创建成功，data 为 dto.TestTaskResp"
// @Failure     400 {object} response.Body "参数不合法（含用例未评审通过、构建不属于该产品）"
// @Failure     401 {object} response.Body "未登录或令牌已失效"
// @Failure     403 {object} response.Body "无 testtask-create 权限，或所选对象不可见"
// @Failure     404 {object} response.Body "产品/用例/迭代不存在"
// @Router      /testtasks [post]
func (h *TestTaskHandler) Create(c *gin.Context) {
	req := dto.TestTaskReq{}
	if !response.Bind(c, &req) {
		return
	}
	created, err := h.tasks.Create(c.Request.Context(), req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.Created(c, created)
}

// Update 处理 PUT /testtasks/:id。
//
// @Summary     编辑测试单
// @Description 整体覆盖测试单的元信息，并可追加用例（caseIds 是「再纳入这些」，不是「只保留这些」）。
// @Description 已结单（closed）的单不能编辑，要先重新打开。data 为 dto.TestTaskResp。
// @Tags        测试单
// @Accept      json
// @Produce     json
// @Param       id   path int            true "测试单 ID"
// @Param       body body dto.TestTaskReq true "测试单全量内容"
// @Security    BearerAuth
// @Success     200 {object} response.Body "编辑成功，data 为 dto.TestTaskResp"
// @Failure     400 {object} response.Body "参数不合法"
// @Failure     401 {object} response.Body "未登录或令牌已失效"
// @Failure     403 {object} response.Body "无 testtask-edit 权限"
// @Failure     404 {object} response.Body "测试单不存在"
// @Failure     409 {object} response.Body "已结单不能编辑"
// @Router      /testtasks/{id} [put]
func (h *TestTaskHandler) Update(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	req := dto.TestTaskReq{}
	if !response.Bind(c, &req) {
		return
	}
	updated, err := h.tasks.Update(c.Request.Context(), id, req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, updated)
}

// Delete 处理 DELETE /testtasks/:id。
//
// @Summary     删除测试单
// @Description 删除测试单并移出其中全部用例关联。已有执行记录的单不允许删除 ——
// @Description 删除会毁掉测试证据，不再需要请改为结单。
// @Tags        测试单
// @Produce     json
// @Param       id path int true "测试单 ID"
// @Security    BearerAuth
// @Success     204 {string} string "无内容"
// @Failure     400 {object} response.Body "路径参数不合法"
// @Failure     401 {object} response.Body "未登录或令牌已失效"
// @Failure     403 {object} response.Body "无 testtask-delete 权限"
// @Failure     404 {object} response.Body "测试单不存在"
// @Failure     409 {object} response.Body "该单已有执行记录，不能删除"
// @Router      /testtasks/{id} [delete]
func (h *TestTaskHandler) Delete(c *gin.Context) {
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

// Assign 处理 POST /testtasks/:id/assign。
//
// @Summary     指派测试单负责人
// @Description 把测试单指派给某人，并通知他。data 为 dto.TestTaskResp。
// @Tags        测试单
// @Accept      json
// @Produce     json
// @Param       id   path int               true "测试单 ID"
// @Param       body body dto.AssignUserReq true "负责人"
// @Security    BearerAuth
// @Success     200 {object} response.Body "指派成功，data 为 dto.TestTaskResp"
// @Failure     400 {object} response.Body "参数不合法或用户不存在"
// @Failure     401 {object} response.Body "未登录或令牌已失效"
// @Failure     403 {object} response.Body "无 testtask-assign 权限"
// @Failure     404 {object} response.Body "测试单不存在"
// @Router      /testtasks/{id}/assign [post]
func (h *TestTaskHandler) Assign(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	req := dto.AssignUserReq{}
	if !response.Bind(c, &req) {
		return
	}
	updated, err := h.tasks.Assign(c.Request.Context(), id, req.AssignedTo, req.Comment)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, updated)
}

// Action 返回执行状态动作的 handler：开始 / 标记阻塞 / 重新打开。
//
// @Summary     推进测试单
// @Description start（wait/blocked → doing）、block（doing → blocked）、activate（closed → doing）。
// @Description 请求体只有备注。data 为 dto.TestTaskResp。
// @Tags        测试单
// @Accept      json
// @Produce     json
// @Param       id   path int                true "测试单 ID"
// @Param       body body dto.StateActionReq false "备注"
// @Security    BearerAuth
// @Success     200 {object} response.Body{data=dto.TestTaskResp}
// @Failure     400 {object} response.Body "参数不合法"
// @Failure     401 {object} response.Body "未登录或令牌已失效"
// @Failure     403 {object} response.Body "无 testtask-start 权限"
// @Failure     404 {object} response.Body "测试单不存在"
// @Failure     409 {object} response.Body "当前状态不允许该动作"
// @Router      /testtasks/{id}/start [post]
// @Router      /testtasks/{id}/block [post]
// @Router      /testtasks/{id}/activate [post]
func (h *TestTaskHandler) Action(name string) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := pathID(c)
		if !ok {
			return
		}
		req := dto.StateActionReq{}
		if !response.Bind(c, &req) {
			return
		}
		updated, err := h.tasks.Apply(c.Request.Context(), id, name, req.Comment)
		if err != nil {
			response.Fail(c, err)
			return
		}
		response.OK(c, updated)
	}
}

// Finish 处理 POST /testtasks/:id/finish。
//
// @Summary     测试单结单
// @Description 给出结论（pass / fail / blocked）并关闭测试单，report 可作为结单报告。
// @Description 结论必填：「测完了」本身不描述这次测试的产出。结单会通知提单人。
// @Tags        测试单
// @Accept      json
// @Produce     json
// @Param       id   path int                  true "测试单 ID"
// @Param       body body dto.TestTaskFinishReq true "结论与报告"
// @Security    BearerAuth
// @Success     200 {object} response.Body "已结单，data 为 dto.TestTaskResp"
// @Failure     400 {object} response.Body "缺少结论或取值不合法"
// @Failure     401 {object} response.Body "未登录或令牌已失效"
// @Failure     403 {object} response.Body "无 testtask-finish 权限"
// @Failure     404 {object} response.Body "测试单不存在"
// @Router      /testtasks/{id}/finish [post]
func (h *TestTaskHandler) Finish(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	req := dto.TestTaskFinishReq{}
	if !response.Bind(c, &req) {
		return
	}
	updated, err := h.tasks.Finish(c.Request.Context(), id, req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, updated)
}

// AddCases 处理 POST /testtasks/:id/cases。
//
// @Summary     纳入用例
// @Description 把评审通过的用例加入测试单，并锁定它们此刻的版本。已在单内的会被跳过（幂等）。
// @Description 未评审通过的用例会被拒绝：它的结论本身还没定，跑出来的结果不该进测试报告。
// @Tags        测试单
// @Accept      json
// @Produce     json
// @Param       id   path int                true "测试单 ID"
// @Param       body body dto.AddTaskCasesReq true "用例 id 列表"
// @Security    BearerAuth
// @Success     200 {object} response.Body "纳入后的测试单详情，data 为 dto.TestTaskDetailResp"
// @Failure     400 {object} response.Body "用例为空、不存在或未评审通过"
// @Failure     401 {object} response.Body "未登录或令牌已失效"
// @Failure     403 {object} response.Body "无 testtask-edit 权限"
// @Failure     404 {object} response.Body "测试单或用例不存在"
// @Failure     409 {object} response.Body "所选用例都已在单内"
// @Router      /testtasks/{id}/cases [post]
func (h *TestTaskHandler) AddCases(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	req := dto.AddTaskCasesReq{}
	if !response.Bind(c, &req) {
		return
	}
	detail, err := h.tasks.AddCases(c.Request.Context(), id, req.CaseIDs)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, detail)
}

// RemoveCase 处理 DELETE /testtasks/:id/cases/:caseId。
//
// @Summary     移出用例
// @Description 把用例从测试单移出，只删关联：用例本身与已有的执行记录都不动
// @Description （移出后仍要能回答「这条用例当初在这个单里跑过、结果是什么」）。
// @Tags        测试单
// @Produce     json
// @Param       id     path int true "测试单 ID"
// @Param       caseId path int true "用例 ID"
// @Security    BearerAuth
// @Success     200 {object} response.Body "移出后的测试单详情，data 为 dto.TestTaskDetailResp"
// @Failure     400 {object} response.Body "路径参数不合法"
// @Failure     401 {object} response.Body "未登录或令牌已失效"
// @Failure     403 {object} response.Body "无 testtask-edit 权限"
// @Failure     404 {object} response.Body "测试单不存在，或单内没有这条用例"
// @Router      /testtasks/{id}/cases/{caseId} [delete]
func (h *TestTaskHandler) RemoveCase(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	caseID, ok := uintParam(c, "caseId")
	if !ok {
		return
	}
	detail, err := h.tasks.RemoveCase(c.Request.Context(), id, caseID)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, detail)
}

// AssignCase 处理 PUT /testtasks/:id/cases/:caseId/assign。
//
// @Summary     指派单内用例
// @Description 把单内某条用例指派给具体执行人（一个单常常是几个人分头跑的），并通知他。
// @Tags        测试单
// @Accept      json
// @Produce     json
// @Param       id     path int               true "测试单 ID"
// @Param       caseId path int               true "用例 ID"
// @Param       body   body dto.AssignUserReq true "执行人"
// @Security    BearerAuth
// @Success     200 {object} response.Body "指派后的测试单详情，data 为 dto.TestTaskDetailResp"
// @Failure     400 {object} response.Body "参数不合法或用户不存在"
// @Failure     401 {object} response.Body "未登录或令牌已失效"
// @Failure     403 {object} response.Body "无 testtask-assign 权限"
// @Failure     404 {object} response.Body "测试单不存在，或单内没有这条用例"
// @Router      /testtasks/{id}/cases/{caseId}/assign [put]
func (h *TestTaskHandler) AssignCase(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	caseID, ok := uintParam(c, "caseId")
	if !ok {
		return
	}
	req := dto.AssignUserReq{}
	if !response.Bind(c, &req) {
		return
	}
	detail, err := h.tasks.AssignCase(c.Request.Context(), id, caseID, req.AssignedTo, req.Comment)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, detail)
}

// RecordRun 处理 POST /testtasks/:id/cases/:caseId/run。
//
// @Summary     记录一次执行
// @Description 记录某条用例本次的结果（pass / fail / blocked）。执行历史是追加式的：
// @Description 同一用例在同一单里可以跑很多次，每次一条记录。
// @Description 步骤的「操作/期望」由后端按用例内容回填并存成快照，不采信客户端传来的副本 ——
// @Description 否则一份被改过的步骤描述会伪装成「当时跑的就是这样」。
// @Tags        测试执行
// @Accept      json
// @Produce     json
// @Param       id     path int          true "测试单 ID"
// @Param       caseId path int          true "用例 ID"
// @Param       body   body dto.TestRunReq true "本次执行结果"
// @Security    BearerAuth
// @Success     201 {object} response.Body "执行记录，data 为 dto.TestRunResp"
// @Failure     400 {object} response.Body "参数不合法（含结论取值不合法）"
// @Failure     401 {object} response.Body "未登录或令牌已失效"
// @Failure     403 {object} response.Body "无 testrun-record 权限"
// @Failure     404 {object} response.Body "测试单不存在，或单内没有这条用例"
// @Failure     409 {object} response.Body "测试单已结单，不能继续记录结果"
// @Router      /testtasks/{id}/cases/{caseId}/run [post]
func (h *TestTaskHandler) RecordRun(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	caseID, ok := uintParam(c, "caseId")
	if !ok {
		return
	}
	req := dto.TestRunReq{}
	if !response.Bind(c, &req) {
		return
	}
	run, err := h.tasks.RecordRun(c.Request.Context(), id, caseID, req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.Created(c, run)
}

// ListRuns 处理 GET /testtasks/:id/runs。
//
// @Summary     执行历史
// @Description 查询测试单内的执行记录（data 为分页对象，list 元素为 dto.TestRunResp），
// @Description 可用 caseId 只看某条用例的历次结果。默认按时间倒序。
// @Tags        测试执行
// @Produce     json
// @Param       id     path int    true "测试单 ID"
// @Param       caseId query int  false "只看某条用例"
// @Param       result query string false "按结论过滤：pass / fail / blocked"
// @Param       page   query int  false "页码，默认 1"
// @Param       size   query int  false "每页条数，默认 20"
// @Security    BearerAuth
// @Success     200 {object} response.Body "分页对象，data 为 {list,total,page,size}，list 元素为 dto.TestRunResp"
// @Failure     400 {object} response.Body "参数不合法"
// @Failure     401 {object} response.Body "未登录或令牌已失效"
// @Failure     403 {object} response.Body "无 testtask-browse 权限"
// @Router      /testtasks/{id}/runs [get]
func (h *TestTaskHandler) ListRuns(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	req := dto.TestRunFilterReq{}
	if !response.Bind(c, &req) {
		return
	}
	req.TaskID = id
	result, err := h.tasks.ListRuns(c.Request.Context(), req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, result)
}
