package handler

import (
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/xywf221/xuliu/internal/dto"
	"github.com/xywf221/xuliu/internal/pkg/response"
	"github.com/xywf221/xuliu/internal/service"
)

// ProjectHandler 处理项目相关请求。
//
// 项目与迭代的状态流转没有各自的 handler 方法（不像需求有 Close/Review 等
// 七八个），而是收敛到 Apply 一个入口：两者的动作入参都只有一个备注，
// 拆成四个方法只会得到四份一模一样的代码。
type ProjectHandler struct {
	projects *service.ProjectService
}

// NewProjectHandler 构造项目 handler。
func NewProjectHandler(projects *service.ProjectService) *ProjectHandler {
	return &ProjectHandler{projects: projects}
}

// List 处理 GET /projects。
//
// @Summary     项目列表
// @Description 分页查询项目列表。结果按当前用户可见范围过滤：ACL 为 private 的项目只有白名单内成员能看到，列表与详情口径一致。所有筛选项空值即不过滤。
// @Tags        项目
// @Produce     json
// @Param       status   query string false "状态：wait/doing/suspended/closed"
// @Param       model    query string false "模型：scrum/waterfall/kanban/agileplus/waterfallplus"
// @Param       keywords query string false "名称 / 代号模糊匹配"
// @Param       productId query uint  false "只看关联了该产品的项目"
// @Param       page     query int    false "页码，默认 1"
// @Param       size     query int    false "每页条数，默认 20，最大 200"
// @Param       order    query string false "排序，如 id_desc"
// @Security    BearerAuth
// @Success     200 {object} response.Body "分页对象，data 为 {list,total,page,size}，list 元素为 dto.ProjectResp"
// @Failure     400 {object} response.Body "参数不合法"
// @Failure     401 {object} response.Body "未登录或令牌已失效"
// @Failure     403 {object} response.Body "无 project-browse 权限"
// @Router      /projects [get]
func (h *ProjectHandler) List(c *gin.Context) {
	req := dto.ProjectFilterReq{}
	if !response.Bind(c, &req) {
		return
	}
	result, err := h.projects.List(c.Request.Context(), req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, result)
}

// Brief 处理 GET /projects/brief，供下拉选择使用（不分页）。
//
// @Summary     项目简要列表
// @Description 返回项目简要信息（id/名称/代号），不分页，供下拉与筛选栏使用。同样按当前用户可见范围过滤（private 项目仅白名单可见）。
// @Tags        项目
// @Produce     json
// @Param       limit query int false "返回条数上限"
// @Security    BearerAuth
// @Success     200 {object} response.Body{data=[]dto.ProjectBrief}
// @Failure     401 {object} response.Body "未登录或令牌已失效"
// @Failure     403 {object} response.Body "无 project-browse 权限"
// @Router      /projects/brief [get]
func (h *ProjectHandler) Brief(c *gin.Context) {
	limit, _ := strconv.Atoi(c.Query("limit"))
	list, err := h.projects.ListBrief(c.Request.Context(), limit)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, list)
}

// Get 处理 GET /projects/:id。
//
// @Summary     项目详情
// @Description 按 ID 查询项目详情，含关联产品、团队、迭代/任务/需求统计与当前可执行的状态动作（actions）。ACL 为 private 的项目仅白名单成员可见，否则返回 403。
// @Tags        项目
// @Produce     json
// @Param       id path int true "项目 ID"
// @Security    BearerAuth
// @Success     200 {object} response.Body{data=dto.ProjectResp}
// @Failure     401 {object} response.Body "未登录或令牌已失效"
// @Failure     403 {object} response.Body "无 project-browse 权限或不在可见范围"
// @Failure     404 {object} response.Body "项目不存在"
// @Router      /projects/{id} [get]
func (h *ProjectHandler) Get(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	project, err := h.projects.Get(c.Request.Context(), id)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, project)
}

// Create 处理 POST /projects。
//
// @Summary     新建项目
// @Description 新建项目。必须至少关联一个产品（productIds 非空）—— 没有产品的项目排不进需求、也建不了任务；名称与代号全局唯一（重复返回 409）；新建后的状态由状态机决定（未开始），不受 status 字段影响。创建者与 PO/PM/QD/RD 自动进入团队。
// @Tags        项目
// @Produce     json
// @Accept      json
// @Param       req body dto.ProjectReq true "项目信息，productIds 至少一个"
// @Security    BearerAuth
// @Success     201 {object} response.Body{data=dto.ProjectResp}
// @Failure     400 {object} response.Body "参数不合法或未关联产品"
// @Failure     401 {object} response.Body "未登录或令牌已失效"
// @Failure     403 {object} response.Body "无 project-create 权限"
// @Failure     409 {object} response.Body "名称或代号已存在"
// @Router      /projects [post]
func (h *ProjectHandler) Create(c *gin.Context) {
	req := dto.ProjectReq{}
	if !response.Bind(c, &req) {
		return
	}
	project, err := h.projects.Create(c.Request.Context(), req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.Created(c, project)
}

// Update 处理 PUT /projects/:id。
//
// @Summary     编辑项目
// @Description 编辑项目基本信息。状态不在此修改 —— 开始/挂起/关闭/激活各有独立动作与权限点；productIds 同样至少一个；更新负责人时不会自动调整团队成员。
// @Tags        项目
// @Produce     json
// @Accept      json
// @Param       id  path int              true "项目 ID"
// @Param       req body dto.ProjectReq   true "项目信息，productIds 至少一个"
// @Security    BearerAuth
// @Success     200 {object} response.Body{data=dto.ProjectResp}
// @Failure     400 {object} response.Body "参数不合法或未关联产品"
// @Failure     401 {object} response.Body "未登录或令牌已失效"
// @Failure     403 {object} response.Body "无 project-edit 权限"
// @Failure     404 {object} response.Body "项目不存在"
// @Failure     409 {object} response.Body "名称或代号已存在"
// @Router      /projects/{id} [put]
func (h *ProjectHandler) Update(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	req := dto.ProjectReq{}
	if !response.Bind(c, &req) {
		return
	}
	project, err := h.projects.Update(c.Request.Context(), id, req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, project)
}

// Delete 处理 DELETE /projects/:id。
//
// @Summary     删除项目
// @Description 删除项目。项目下还有迭代、任务或已排入的需求时返回 409，需先清空；项目内的缺陷不随项目删除，而是退回产品级（project_id 与 execution_id 置 0）。
// @Tags        项目
// @Produce     json
// @Param       id path int true "项目 ID"
// @Security    BearerAuth
// @Success     204 {string} string "无内容"
// @Failure     401 {object} response.Body "未登录或令牌已失效"
// @Failure     403 {object} response.Body "无 project-delete 权限"
// @Failure     404 {object} response.Body "项目不存在"
// @Failure     409 {object} response.Body "项目下仍有迭代 / 任务 / 需求"
// @Router      /projects/{id} [delete]
func (h *ProjectHandler) Delete(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	if err := h.projects.Delete(c.Request.Context(), id); err != nil {
		response.Fail(c, err)
		return
	}
	response.NoContent(c)
}

// Action 返回某个状态动作的 handler。
//
// 动作名由路由表给出而不是从路径参数里读：路径参数意味着「动作」是个
// 变量，可它其实是一张封闭的清单 —— 每个动作要挂自己的权限点
// （开始用 project-start，关闭用 project-close），路由表上写死才能
// 一眼看出「谁能做哪个动作」，审计时不必读 handler 代码。
//
// @Summary     项目状态动作
// @Description 对项目执行状态动作（start / suspend / activate / close），入参仅一个备注。start / suspend / activate 共用 project-start 权限；close 使用独立权限点 project-close —— 关闭一个项目比挂起它影响大得多，不该是同一个权限。当前状态不允许该动作时返回 409。
// @Tags        项目
// @Produce     json
// @Accept      json
// @Param       id     path int    true  "项目 ID"
// @Param       req    body dto.StateActionReq false "备注"
// @Security    BearerAuth
// @Success     200 {object} response.Body{data=dto.ProjectResp}
// @Failure     400 {object} response.Body "参数不合法"
// @Failure     401 {object} response.Body "未登录或令牌已失效"
// @Failure     403 {object} response.Body "无对应动作的权限（project-start / project-close）"
// @Failure     404 {object} response.Body "项目不存在"
// @Failure     409 {object} response.Body "当前状态不允许该动作"
// @Router      /projects/{id}/start [post]
// @Router      /projects/{id}/suspend [post]
// @Router      /projects/{id}/close [post]
// @Router      /projects/{id}/activate [post]
func (h *ProjectHandler) Action(name string) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := pathID(c)
		if !ok {
			return
		}
		req := dto.StateActionReq{}
		if !response.Bind(c, &req) {
			return
		}
		project, err := h.projects.Apply(c.Request.Context(), id, name, req.Comment)
		if err != nil {
			response.Fail(c, err)
			return
		}
		response.OK(c, project)
	}
}

// ListTeam 处理 GET /projects/:id/team。
//
// @Summary     项目团队列表
// @Description 返回项目的团队成员及其在本项目内的任务数。迭代团队必须是项目团队的子集，往迭代加人前需先在此项目团队中。
// @Tags        项目
// @Produce     json
// @Param       id path int true "项目 ID"
// @Security    BearerAuth
// @Success     200 {object} response.Body{data=[]dto.TeamMemberResp}
// @Failure     401 {object} response.Body "未登录或令牌已失效"
// @Failure     403 {object} response.Body "无 project-browse 权限"
// @Failure     404 {object} response.Body "项目不存在"
// @Router      /projects/{id}/team [get]
func (h *ProjectHandler) ListTeam(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	members, err := h.projects.ListTeam(c.Request.Context(), id)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, members)
}

// AddTeam 处理 POST /projects/:id/team。
//
// @Summary     加入项目团队
// @Description 往项目团队里加一名成员。用户必须已存在；重复加入返回 409（team_member_exists），不是幂等更新 —— 修改已有成员请用 PUT。先加入项目团队，该成员才能被加进其下的迭代团队。
// @Tags        项目
// @Produce     json
// @Accept      json
// @Param       id   path int            true "项目 ID"
// @Param       req  body dto.TeamMemberReq true "成员信息"
// @Security    BearerAuth
// @Success     201 {object} response.Body "已加入，data 为空"
// @Failure     400 {object} response.Body "参数不合法"
// @Failure     401 {object} response.Body "未登录或令牌已失效"
// @Failure     403 {object} response.Body "无 team-manage 权限"
// @Failure     404 {object} response.Body "项目或用户不存在"
// @Failure     409 {object} response.Body "该成员已在团队中"
// @Router      /projects/{id}/team [post]
func (h *ProjectHandler) AddTeam(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	req := dto.TeamMemberReq{}
	if !response.Bind(c, &req) {
		return
	}
	if err := h.projects.AddTeam(c.Request.Context(), id, req); err != nil {
		response.Fail(c, err)
		return
	}
	response.Created(c, nil)
}

// UpdateTeam 处理 PUT /projects/:id/team/:userId。
//
// @Summary     更新项目团队成员
// @Description 更新某成员在项目团队中的角色、岗位、工天 / 工时 / 预估与排序。req 中的 userId 被路径参数覆盖，以路径为准。
// @Tags        项目
// @Produce     json
// @Accept      json
// @Param       id      path int true "项目 ID"
// @Param       userId  path int true "成员用户 ID"
// @Param       req     body dto.TeamMemberReq true "更新后的成员信息"
// @Security    BearerAuth
// @Success     204 {string} string "无内容"
// @Failure     400 {object} response.Body "参数不合法"
// @Failure     401 {object} response.Body "未登录或令牌已失效"
// @Failure     403 {object} response.Body "无 team-manage 权限"
// @Failure     404 {object} response.Body "项目或成员不存在"
// @Router      /projects/{id}/team/{userId} [put]
func (h *ProjectHandler) UpdateTeam(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	userID, ok := uintParam(c, "userId")
	if !ok {
		return
	}
	req := dto.TeamMemberReq{}
	if !response.Bind(c, &req) {
		return
	}
	if err := h.projects.UpdateTeam(c.Request.Context(), id, userID, req); err != nil {
		response.Fail(c, err)
		return
	}
	response.NoContent(c)
}

// RemoveTeam 处理 DELETE /projects/:id/team/:userId。
//
// @Summary     移出项目团队
// @Description 把成员移出项目团队。该成员在本项目名下还有未完成任务时返回 409，需先转派或关闭这些任务 —— 否则任务会挂在不在团队里的人身上。
// @Tags        项目
// @Produce     json
// @Param       id     path int true "项目 ID"
// @Param       userId path int true "成员用户 ID"
// @Security    BearerAuth
// @Success     204 {string} string "无内容"
// @Failure     401 {object} response.Body "未登录或令牌已失效"
// @Failure     403 {object} response.Body "无 team-manage 权限"
// @Failure     404 {object} response.Body "项目或成员不存在"
// @Failure     409 {object} response.Body "该成员名下还有未完成的任务"
// @Router      /projects/{id}/team/{userId} [delete]
func (h *ProjectHandler) RemoveTeam(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	userID, ok := uintParam(c, "userId")
	if !ok {
		return
	}
	if err := h.projects.RemoveTeam(c.Request.Context(), id, userID); err != nil {
		response.Fail(c, err)
		return
	}
	response.NoContent(c)
}

// ListStories 处理 GET /projects/:id/stories，返回项目下已排入的需求。
//
// 带 ?executionId= 时只看某个迭代里的。
//
// @Summary     项目已排入的需求
// @Description 返回项目下已排入的需求列表。不传 executionId 时返回项目全部（含未分配到迭代的）；传了则只看该迭代内的，且校验迭代属于该项目。每条记录带排入时的版本号（version）与是否已过期（outdated），需求后续变更时 outdated 置 true。
// @Tags        项目
// @Produce     json
// @Param       id          path int true  "项目 ID"
// @Param       executionId query uint false "只看该迭代内已排入的需求"
// @Security    BearerAuth
// @Success     200 {object} response.Body{data=[]dto.StoryLinkResp}
// @Failure     401 {object} response.Body "未登录或令牌已失效"
// @Failure     403 {object} response.Body "无 project-browse 权限"
// @Failure     404 {object} response.Body "项目或迭代不存在"
// @Router      /projects/{id}/stories [get]
func (h *ProjectHandler) ListStories(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	executionID, ok := queryUint(c, "executionId")
	if !ok {
		return
	}
	links, err := h.projects.ListStories(c.Request.Context(), id, executionID)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, links)
}

// LinkStory 处理 POST /projects/:id/stories，把需求排入项目/迭代。
//
// @Summary     需求排入项目/迭代
// @Description 把需求排入项目，可同时用 executionId 指定迭代。服务端校验：需求所属产品必须已关联到该项目（否则 400，跨产品需求排不进来）；只有已评审通过的需求可排入（否则 409）；项目已关闭不可排入（409）；executionId 非 0 时迭代必须属于该项目。排期记录会存下排入时的 story 版本号，需求后来变更时前端据此提示「排入版本已过时」。排期记录以 (project_id, story_id) 为唯一，重复排入不新增记录而是更新已有关联（executionId / version / order）。
// @Tags        项目
// @Produce     json
// @Accept      json
// @Param       id   path int               true "项目 ID"
// @Param       req  body dto.StoryLinkReq  true "排期信息：storyId 必填，executionId 可选"
// @Security    BearerAuth
// @Success     201 {object} response.Body{data=dto.StoryLinkResp}
// @Failure     400 {object} response.Body "参数不合法，或需求所属产品未关联到此项目"
// @Failure     401 {object} response.Body "未登录或令牌已失效"
// @Failure     403 {object} response.Body "无 project-edit 权限"
// @Failure     404 {object} response.Body "项目 / 需求 / 迭代不存在"
// @Failure     409 {object} response.Body "项目已关闭，或需求当前状态不可排入"
// @Router      /projects/{id}/stories [post]
func (h *ProjectHandler) LinkStory(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	req := dto.StoryLinkReq{}
	if !response.Bind(c, &req) {
		return
	}
	link, err := h.projects.LinkStory(c.Request.Context(), id, req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.Created(c, link)
}

// UnlinkStory 处理 DELETE /projects/:id/stories/:storyId，把需求撤出项目。
//
// @Summary     需求撤出项目
// @Description 把已排入的需求从项目（或其迭代）撤出。需求本身不删除；但该项目下仍有任务关联这条需求时返回 409，需先处理这些任务 —— 否则看板上会出现指向「不在本项目里的需求」的孤儿任务。需求未排入此项目时返回 404。
// @Tags        项目
// @Produce     json
// @Param       id      path int true "项目 ID"
// @Param       storyId path int true "需求 ID"
// @Security    BearerAuth
// @Success     204 {string} string "无内容"
// @Failure     401 {object} response.Body "未登录或令牌已失效"
// @Failure     403 {object} response.Body "无 project-edit 权限"
// @Failure     404 {object} response.Body "项目 / 需求不存在，或该需求未排入此项目"
// @Failure     409 {object} response.Body "该项目下仍有任务关联此需求"
// @Router      /projects/{id}/stories/{storyId} [delete]
func (h *ProjectHandler) UnlinkStory(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	storyID, ok := uintParam(c, "storyId")
	if !ok {
		return
	}
	if err := h.projects.UnlinkStory(c.Request.Context(), id, storyID); err != nil {
		response.Fail(c, err)
		return
	}
	response.NoContent(c)
}

// ---------------------------------------------------------------------------
// 迭代
// ---------------------------------------------------------------------------

// ExecutionHandler 处理迭代相关请求。
type ExecutionHandler struct {
	executions *service.ExecutionService
}

// NewExecutionHandler 构造迭代 handler。
func NewExecutionHandler(executions *service.ExecutionService) *ExecutionHandler {
	return &ExecutionHandler{executions: executions}
}

// List 处理 GET /executions。
//
// @Summary     迭代列表
// @Description 分页查询迭代，data 为 {list,total,page,size}，list 元素为 dto.ExecutionResp。所有筛选项都是「空值即不过滤」，结果只含当前用户可见的项目下的迭代。estimate / consumed / left / progress 由下属任务的工时聚合而来，不是手动维护的字段。
// @Tags        迭代
// @Produce     json
// @Param       projectId query int    false "按项目过滤"
// @Param       type      query string false "迭代类型：sprint/stage/kanban"
// @Param       status    query string false "状态：wait/doing/suspended/closed"
// @Param       keywords  query string false "名称 / 关键词模糊匹配"
// @Param       page      query int    false "页码，默认 1"
// @Param       size      query int    false "每页条数，默认 20，最大 200"
// @Param       order     query string false "排序，如 id_desc"
// @Security    BearerAuth
// @Success     200 {object} response.Body "分页对象，data 为 {list,total,page,size}，list 元素为 dto.ExecutionResp"
// @Failure     400 {object} response.Body "参数不合法"
// @Failure     401 {object} response.Body "未登录或令牌已失效"
// @Failure     403 {object} response.Body "无 project-browse 权限"
// @Router      /executions [get]
func (h *ExecutionHandler) List(c *gin.Context) {
	req := dto.ExecutionFilterReq{}
	if !response.Bind(c, &req) {
		return
	}
	result, err := h.executions.List(c.Request.Context(), req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, result)
}

// Get 处理 GET /executions/:id。
//
// @Summary     迭代详情
// @Description 按 ID 查询单个迭代。详情额外填充 storyCount / taskCount / taskHours、迭代自己的团队（team）与当前可执行动作（actions）；team 为空表示「沿用项目团队」，不是没有可用成员。
// @Tags        迭代
// @Produce     json
// @Param       id path int true "迭代 ID"
// @Security    BearerAuth
// @Success     200 {object} response.Body{data=dto.ExecutionResp}
// @Failure     401 {object} response.Body "未登录或令牌已失效"
// @Failure     403 {object} response.Body "无 project-browse 权限"
// @Failure     404 {object} response.Body "迭代不存在或不可见"
// @Router      /executions/{id} [get]
func (h *ExecutionHandler) Get(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	execution, err := h.executions.Get(c.Request.Context(), id)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, execution)
}

// Create 处理 POST /executions。
//
// @Summary     新建迭代
// @Description 新建迭代，迭代必属某项目（projectId 必填）。服务端校验：项目已关闭不能新建；同项目下迭代名称唯一；begin/end 必须落在项目起止日期内。type 取 sprint/stage/kanban。team 留空表示沿用项目团队；填了则逐条校验用户存在，执行团队与项目团队的子集约束在后续加人接口（POST /executions/{id}/team）强制校验。estimate / consumed / left / progress 由下属任务的工时聚合而来，创建时不手填。
// @Tags        迭代
// @Produce     json
// @Param       body body dto.ExecutionReq true "迭代信息"
// @Security    BearerAuth
// @Success     201 {object} response.Body{data=dto.ExecutionResp}
// @Failure     400 {object} response.Body "参数不合法，如日期越界、同项目下同名迭代"
// @Failure     401 {object} response.Body "未登录或令牌已失效"
// @Failure     403 {object} response.Body "无 execution-create 权限"
// @Failure     409 {object} response.Body "项目已关闭，不能新建迭代"
// @Router      /executions [post]
func (h *ExecutionHandler) Create(c *gin.Context) {
	req := dto.ExecutionReq{}
	if !response.Bind(c, &req) {
		return
	}
	execution, err := h.executions.Create(c.Request.Context(), req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.Created(c, execution)
}

// Update 处理 PUT /executions/:id。
//
// @Summary     编辑迭代
// @Description 编辑迭代基础信息与迭代团队。已关闭（closed）的迭代不可编辑，需先 activate 激活；重名校验与日期范围校验同新建。status 只允许出现在编辑里做初值设定之外的合法状态转移请走状态动作接口。执行团队与项目团队的子集约束在后续加人接口强制校验。
// @Tags        迭代
// @Produce     json
// @Param       id   path int            true "迭代 ID"
// @Param       body body dto.ExecutionReq true "迭代信息"
// @Security    BearerAuth
// @Success     200 {object} response.Body{data=dto.ExecutionResp}
// @Failure     400 {object} response.Body "参数不合法，如日期越界、同项目下同名迭代"
// @Failure     401 {object} response.Body "未登录或令牌已失效"
// @Failure     403 {object} response.Body "无 execution-edit 权限"
// @Failure     404 {object} response.Body "迭代不存在"
// @Failure     409 {object} response.Body "当前状态不可编辑，或项目已关闭"
// @Router      /executions/{id} [put]
func (h *ExecutionHandler) Update(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	req := dto.ExecutionReq{}
	if !response.Bind(c, &req) {
		return
	}
	execution, err := h.executions.Update(c.Request.Context(), id, req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, execution)
}

// Delete 处理 DELETE /executions/:id。
//
// @Summary     删除迭代
// @Description 删除迭代，成功返回 204 无响应体。迭代下还有任务时返回 409，需先移出或删除这些任务；已排入的需求与缺陷会解除与该迭代的关联退回项目级（execution_id 置 0），需求与缺陷本身不删除。迭代团队一并删除。
// @Tags        迭代
// @Produce     json
// @Param       id path int true "迭代 ID"
// @Security    BearerAuth
// @Success     204 {string} string "无内容"
// @Failure     401 {object} response.Body "未登录或令牌已失效"
// @Failure     403 {object} response.Body "无 execution-delete 权限"
// @Failure     404 {object} response.Body "迭代不存在"
// @Failure     409 {object} response.Body "迭代下仍有任务，需先移出或删除"
// @Router      /executions/{id} [delete]
func (h *ExecutionHandler) Delete(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	if err := h.executions.Delete(c.Request.Context(), id); err != nil {
		response.Fail(c, err)
		return
	}
	response.NoContent(c)
}

// Action 返回某个状态动作的 handler，见 ProjectHandler.Action。
//
// @Summary     推进迭代（开始/挂起/关闭/激活）
// @Description 通用状态动作，具体动作由路由决定，请求体只有备注。四条路由（start / suspend / close / activate）同属「推进迭代」，共用 execution-edit 权限点。状态机：start 让 wait|suspended → doing 并首次写入 realBegan；suspend 让 doing → suspended；close 让 wait|doing|suspended → closed（不可逆，需 activate 才能回来）；activate 让 closed → doing。当前状态不允许该动作时返回 409。
// @Tags        迭代
// @Accept      json
// @Produce     json
// @Param       id     path int                true "迭代 ID"
// @Param       body   body dto.StateActionReq false "动作备注"
// @Security    BearerAuth
// @Success     200 {object} response.Body{data=dto.ExecutionResp}
// @Failure     400 {object} response.Body "参数不合法"
// @Failure     401 {object} response.Body "未登录或令牌已失效"
// @Failure     403 {object} response.Body "无 execution-edit 权限"
// @Failure     404 {object} response.Body "迭代不存在"
// @Failure     409 {object} response.Body "当前状态不允许该动作"
// @Router      /executions/{id}/start [post]
// @Router      /executions/{id}/suspend [post]
// @Router      /executions/{id}/close [post]
// @Router      /executions/{id}/activate [post]
func (h *ExecutionHandler) Action(name string) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := pathID(c)
		if !ok {
			return
		}
		req := dto.StateActionReq{}
		if !response.Bind(c, &req) {
			return
		}
		execution, err := h.executions.Apply(c.Request.Context(), id, name, req.Comment)
		if err != nil {
			response.Fail(c, err)
			return
		}
		response.OK(c, execution)
	}
}

// ListTeam 处理 GET /executions/:id/team。
//
// @Summary     迭代团队列表
// @Description 返回迭代自己的团队成员（含角色、工天 / 工时 / 预估与任务数）。列表为空表示该迭代「沿用项目团队」，不是没有可用成员——迭代团队是项目团队的子集。
// @Tags        迭代
// @Produce     json
// @Param       id path int true "迭代 ID"
// @Security    BearerAuth
// @Success     200 {object} response.Body{data=[]dto.TeamMemberResp}
// @Failure     401 {object} response.Body "未登录或令牌已失效"
// @Failure     403 {object} response.Body "无 project-browse 权限"
// @Failure     404 {object} response.Body "迭代不存在"
// @Router      /executions/{id}/team [get]
func (h *ExecutionHandler) ListTeam(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	members, err := h.executions.ListTeam(c.Request.Context(), id)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, members)
}

// AddTeam 处理 POST /executions/:id/team。
//
// @Summary     加入迭代团队
// @Description 往迭代团队里加一名成员。硬约束：该用户必须已在项目团队里，否则 400（TeamMemberNotInProject）——迭代团队必须是项目团队的子集，先入项目团队才能入迭代。重复加入返回 409，不是幂等更新——修改已有成员请用 PUT。
// @Tags        迭代
// @Produce     json
// @Accept      json
// @Param       id   path int               true "迭代 ID"
// @Param       body body dto.TeamMemberReq true "成员信息"
// @Security    BearerAuth
// @Success     201 {object} response.Body "已加入，data 为空"
// @Failure     400 {object} response.Body "参数不合法，或该成员不是项目团队成员"
// @Failure     401 {object} response.Body "未登录或令牌已失效"
// @Failure     403 {object} response.Body "无 team-manage 权限"
// @Failure     404 {object} response.Body "迭代或用户不存在"
// @Failure     409 {object} response.Body "该成员已在迭代团队中"
// @Router      /executions/{id}/team [post]
func (h *ExecutionHandler) AddTeam(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	req := dto.TeamMemberReq{}
	if !response.Bind(c, &req) {
		return
	}
	if err := h.executions.AddTeam(c.Request.Context(), id, req); err != nil {
		response.Fail(c, err)
		return
	}
	response.Created(c, nil)
}

// UpdateTeam 处理 PUT /executions/:id/team/:userId。
//
// @Summary     更新迭代团队成员
// @Description 更新某成员在迭代团队中的角色、岗位、工天 / 工时 / 预估与排序。req 中的 userId 被路径参数覆盖，以路径为准。不改 userId，故不重新校验子集约束。
// @Tags        迭代
// @Produce     json
// @Accept      json
// @Param       id     path int               true "迭代 ID"
// @Param       userId path int               true "成员用户 ID"
// @Param       body   body dto.TeamMemberReq true "更新后的成员信息"
// @Security    BearerAuth
// @Success     204 {string} string "无内容"
// @Failure     400 {object} response.Body "参数不合法"
// @Failure     401 {object} response.Body "未登录或令牌已失效"
// @Failure     403 {object} response.Body "无 team-manage 权限"
// @Failure     404 {object} response.Body "迭代或成员不存在"
// @Router      /executions/{id}/team/{userId} [put]
func (h *ExecutionHandler) UpdateTeam(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	userID, ok := uintParam(c, "userId")
	if !ok {
		return
	}
	req := dto.TeamMemberReq{}
	if !response.Bind(c, &req) {
		return
	}
	if err := h.executions.UpdateTeam(c.Request.Context(), id, userID, req); err != nil {
		response.Fail(c, err)
		return
	}
	response.NoContent(c)
}

// RemoveTeam 处理 DELETE /executions/:id/team/:userId。
//
// @Summary     移出迭代团队
// @Description 把成员移出迭代团队。该成员在本迭代名下还有未完成任务时返回 409，需先转派或关闭这些任务——否则任务会挂在不在团队里的人身上。
// @Tags        迭代
// @Produce     json
// @Param       id     path int true "迭代 ID"
// @Param       userId path int true "成员用户 ID"
// @Security    BearerAuth
// @Success     204 {string} string "无内容"
// @Failure     401 {object} response.Body "未登录或令牌已失效"
// @Failure     403 {object} response.Body "无 team-manage 权限"
// @Failure     404 {object} response.Body "迭代或成员不存在"
// @Failure     409 {object} response.Body "该成员名下还有未完成的任务"
// @Router      /executions/{id}/team/{userId} [delete]
func (h *ExecutionHandler) RemoveTeam(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	userID, ok := uintParam(c, "userId")
	if !ok {
		return
	}
	if err := h.executions.RemoveTeam(c.Request.Context(), id, userID); err != nil {
		response.Fail(c, err)
		return
	}
	response.NoContent(c)
}

// uintParam 解析非主键的路径参数（如 :userId）。
func uintParam(c *gin.Context, name string) (uint, bool) {
	raw := c.Param(name)
	if raw == "" {
		response.Fail(c, invalidID(name, raw))
		return 0, false
	}
	parsed, err := strconv.ParseUint(raw, 10, 64)
	if err != nil {
		response.Fail(c, invalidID(name, raw))
		return 0, false
	}
	return uint(parsed), true
}
