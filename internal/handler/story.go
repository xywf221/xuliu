package handler

import (
	"github.com/gin-gonic/gin"

	"github.com/xywf221/xuliu/internal/dto"
	"github.com/xywf221/xuliu/internal/pkg/apperr"
	"github.com/xywf221/xuliu/internal/pkg/response"
	"github.com/xywf221/xuliu/internal/service"
)

// StoryHandler 处理需求相关请求。
//
// 状态流转拆成多个路由而不是一个 /actions/:action：
// 每个动作的入参形状差别很大（评审要结论、变更要新内容、关闭要原因），
// 拆开之后每个路由可以挂自己的权限点、用自己那份带校验的 DTO，
// 出问题时也能从路由表直接看出「谁能做哪个动作」。
type StoryHandler struct {
	stories *service.StoryService
}

// NewStoryHandler 构造需求 handler。
func NewStoryHandler(stories *service.StoryService) *StoryHandler {
	return &StoryHandler{stories: stories}
}

// List 处理 GET /stories。
//
// @Summary     需求列表
// @Description 分页查询需求。所有筛选项都是「空值即不过滤」；模块相关的
// @Description 过滤有 unclassified / includeSubModule 两个开关。
// @Tags        需求
// @Produce     json
// @Param       productId        query int    false "按产品过滤"
// @Param       planId           query int    false "按产品计划过滤"
// @Param       moduleId         query int    false "按模块过滤"
// @Param       unclassified     query bool   false "只看未归入任何模块的需求"
// @Param       includeSubModule query bool   false "模块过滤是否包含子模块"
// @Param       status           query string false "状态：draft/reviewing/active/closed/changing"
// @Param       stage            query string false "阶段"
// @Param       type             query string false "需求类型：story/requirement/epic"
// @Param       category         query string false "需求分类"
// @Param       source           query string false "需求来源"
// @Param       pri              query int    false "优先级 1..4"
// @Param       assignedTo       query int    false "按指派人过滤"
// @Param       openedBy         query int    false "按创建人过滤"
// @Param       topOnly          query bool   false "只看顶层需求，不显示子需求"
// @Param       changedOnly      query bool   false "只看已变更但下游未确认的需求"
// @Param       keywords         query string false "标题 / 关键词模糊匹配"
// @Param       page             query int    false "页码，默认 1"
// @Param       size             query int    false "每页条数，默认 20，最大 200"
// @Param       order            query string false "排序，如 id_desc"
// @Security    BearerAuth
// @Success     200 {object} response.Body "分页对象，data 为 {list,total,page,size}，list 元素类型 dto.StoryResp"
// @Failure     400 {object} response.Body "参数不合法"
// @Failure     401 {object} response.Body "未登录或令牌已失效"
// @Failure     403 {object} response.Body "无 story-browse 权限"
// @Router      /stories [get]
func (h *StoryHandler) List(c *gin.Context) {
	req := dto.StoryFilterReq{}
	if !response.Bind(c, &req) {
		return
	}
	result, err := h.stories.List(c.Request.Context(), req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, result)
}

// Get 处理 GET /stories/:id。
//
// @Summary     需求详情
// @Description 返回单个需求，比列表多出版本规格（spec/verify）、评审记录（reviews）、
// @Description 附件（files）、关联需求与所属项目（projects），以及当前状态可执行的动作（actions）。
// @Tags        需求
// @Produce     json
// @Param       id  path int true "需求 ID"
// @Security    BearerAuth
// @Success     200 {object} response.Body{data=dto.StoryDetailResp}
// @Failure     401 {object} response.Body "未登录或令牌已失效"
// @Failure     403 {object} response.Body "无 story-browse 权限"
// @Failure     404 {object} response.Body "需求不存在或不可见"
// @Router      /stories/{id} [get]
func (h *StoryHandler) Get(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	story, err := h.stories.Get(c.Request.Context(), id)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, story)
}

// Versions 处理 GET /stories/:id/versions，返回历史版本快照。
//
// @Summary     需求历史版本
// @Description 返回需求的所有历史版本快照（含标题、描述、验收说明与写入时间），
// @Description 按版本号升序，用于「变更历史」页面。
// @Tags        需求
// @Produce     json
// @Param       id  path int true "需求 ID"
// @Security    BearerAuth
// @Success     200 {object} response.Body{data=[]dto.StorySpecResp}
// @Failure     401 {object} response.Body "未登录或令牌已失效"
// @Failure     403 {object} response.Body "无 story-browse 权限"
// @Failure     404 {object} response.Body "需求不存在或不可见"
// @Router      /stories/{id}/versions [get]
func (h *StoryHandler) Versions(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	versions, err := h.stories.Versions(c.Request.Context(), id)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, versions)
}

// Create 处理 POST /stories。
//
// @Summary     新建需求
// @Description 创建需求，初始状态为 draft。parentId 用于拆分子需求；estimate 为预计工时，
// @Description 留空表示未估；reviewers 留空时用产品配置的默认评审人；files 需先调上传接口拿到附件 id。
// @Tags        需求
// @Accept      json
// @Produce     json
// @Param       req body dto.CreateStoryReq true "需求内容"
// @Security    BearerAuth
// @Success     201 {object} response.Body{data=dto.StoryDetailResp}
// @Failure     400 {object} response.Body "参数不合法，如产品/模块/计划不存在或计划不属于该产品"
// @Failure     401 {object} response.Body "未登录或令牌已失效"
// @Failure     403 {object} response.Body "无 story-create 权限"
// @Router      /stories [post]
func (h *StoryHandler) Create(c *gin.Context) {
	req := dto.CreateStoryReq{}
	if !response.Bind(c, &req) {
		return
	}
	story, err := h.stories.Create(c.Request.Context(), req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.Created(c, story)
}

// Update 处理 PUT /stories/:id。
//
// @Summary     编辑需求
// @Description 更新需求。内容字段用指针区分「没传」（不改）与「传了空值」（清空）；
// @Description version 为必填的乐观锁版本号，必须原样带回，否则返回 409。
// @Tags        需求
// @Accept      json
// @Produce     json
// @Param       id  path int              true "需求 ID"
// @Param       req body dto.UpdateStoryReq true "需求内容"
// @Security    BearerAuth
// @Success     200 {object} response.Body{data=dto.StoryDetailResp}
// @Failure     400 {object} response.Body "参数不合法，如模块/计划不存在或不属于该产品"
// @Failure     401 {object} response.Body "未登录或令牌已失效"
// @Failure     403 {object} response.Body "无 story-edit 权限"
// @Failure     404 {object} response.Body "需求不存在或不可见"
// @Failure     409 {object} response.Body "版本冲突，需求已被他人修改"
// @Router      /stories/{id} [put]
func (h *StoryHandler) Update(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	req := dto.UpdateStoryReq{}
	if !response.Bind(c, &req) {
		return
	}
	story, err := h.stories.Update(c.Request.Context(), id, req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, story)
}

// Delete 处理 DELETE /stories/:id。
//
// @Summary     删除需求
// @Description 删除需求，成功无返回体。
// @Tags        需求
// @Produce     json
// @Param       id  path int true "需求 ID"
// @Security    BearerAuth
// @Success     204 {string} string "无内容"
// @Failure     401 {object} response.Body "未登录或令牌已失效"
// @Failure     403 {object} response.Body "无 story-delete 权限"
// @Failure     404 {object} response.Body "需求不存在或不可见"
// @Failure     409 {object} response.Body "需求当前状态不允许删除，如已有子需求或已排入项目"
// @Router      /stories/{id} [delete]
func (h *StoryHandler) Delete(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	if err := h.stories.Delete(c.Request.Context(), id); err != nil {
		response.Fail(c, err)
		return
	}
	response.NoContent(c)
}

// SubmitReview 处理 POST /stories/:id/submit-review。
//
// @Summary     提交需求评审
// @Description 把草稿或变更中的需求送进评审（draft/changing → reviewing）。请求里不给 reviewers 就沿用上次的名单（建产品时的默认评审人，或上次提交时选的）；名单为空是硬错误，返回 400。评审人会各收到一条待办。
// @Tags        需求
// @Accept      json
// @Produce     json
// @Param       id  path int                  true "需求 ID"
// @Param       req body dto.StorySubmitReviewReq true "评审人"
// @Security    BearerAuth
// @Success     200 {object} response.Body{data=dto.StoryDetailResp}
// @Failure     400 {object} response.Body "参数不合法，或没有可用的评审人"
// @Failure     401 {object} response.Body "未登录或令牌已失效"
// @Failure     403 {object} response.Body "无 story-review 权限"
// @Failure     404 {object} response.Body "需求不存在"
// @Failure     409 {object} response.Body "当前状态不允许提交评审"
// @Router      /stories/{id}/submit-review [post]
func (h *StoryHandler) SubmitReview(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	req := dto.StorySubmitReviewReq{}
	if !response.Bind(c, &req) {
		return
	}
	h.apply(c, id, service.ActionInput{
		Action:    "submitreview",
		Comment:   req.Comment,
		Version:   req.Version,
		Reviewers: req.Reviewers,
	})
}

// Recall 处理 POST /stories/:id/recall。
//
// @Summary     撤销评审
// @Description 把评审中的需求收回来。曾经变更过的回到「变更中」，否则回到「草稿」。
// @Tags        需求
// @Accept      json
// @Produce     json
// @Param       id  path int              true "需求 ID"
// @Param       req body dto.StoryRecallReq true "备注"
// @Security    BearerAuth
// @Success     200 {object} response.Body{data=dto.StoryDetailResp}
// @Failure     400 {object} response.Body "参数不合法"
// @Failure     401 {object} response.Body "未登录或令牌已失效"
// @Failure     403 {object} response.Body "无 story-review 权限"
// @Failure     404 {object} response.Body "需求不存在"
// @Failure     409 {object} response.Body "当前状态不允许撤销"
// @Router      /stories/{id}/recall [post]
func (h *StoryHandler) Recall(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	req := dto.StoryRecallReq{}
	if !response.Bind(c, &req) {
		return
	}
	h.apply(c, id, service.ActionInput{Action: "recall", Comment: req.Comment})
}

// Review 处理 POST /stories/:id/review。
//
// @Summary     给出评审结论
// @Description 四种结论导向四种状态：pass 激活；clarify 退回草稿或变更中；revert 退回激活态并把版本 -1、删掉最新的规格与评审记录（版本 1 没有变更可撤，会被拒）；reject 关闭，此时 closedReason 必填。
// @Tags        需求
// @Accept      json
// @Produce     json
// @Param       id  path int            true "需求 ID"
// @Param       req body dto.StoryReviewReq true "评审结论"
// @Security    BearerAuth
// @Success     200 {object} response.Body{data=dto.StoryDetailResp}
// @Failure     400 {object} response.Body "参数不合法，或 reject 未给关闭原因"
// @Failure     401 {object} response.Body "未登录或令牌已失效"
// @Failure     403 {object} response.Body "无 story-review 权限，或无权访问该产品"
// @Failure     404 {object} response.Body "需求不存在"
// @Failure     409 {object} response.Body "状态不允许评审；或 revert 时版本 1 无变更可撤"
// @Router      /stories/{id}/review [post]
func (h *StoryHandler) Review(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	req := dto.StoryReviewReq{}
	if !response.Bind(c, &req) {
		return
	}
	// 评审结论与状态机动作同名（pass / revert / clarify / reject）：
	// 四种结论导向四种不同状态，把它们建模成四个动作而不是一个带参数的
	// 动作，是为了让权限点与按钮文案都能按结论细分。
	h.apply(c, id, service.ActionInput{
		Action:       req.Result,
		Comment:      req.Comment,
		Version:      req.Version,
		Result:       req.Result,
		ClosedReason: req.ClosedReason,
	})
}

// Change 处理 POST /stories/:id/change，即「需求变更」。
//
// @Summary     需求变更
// @Description 版本 +1 并写入新内容，需求进入「变更中」。旧版本快照进 pm_storyspec。reason 必填 —— 变更必须留下理由。已排期的项目会看到「排入版本已过时」。version 是乐观锁，与当前版本不一致时返回 409。
// @Tags        需求
// @Accept      json
// @Produce     json
// @Param       id  path int             true "需求 ID"
// @Param       req body dto.StoryChangeReq true "变更内容与原因"
// @Security    BearerAuth
// @Success     200 {object} response.Body{data=dto.StoryDetailResp}
// @Failure     400 {object} response.Body "参数不合法，或缺少变更原因"
// @Failure     401 {object} response.Body "未登录或令牌已失效"
// @Failure     403 {object} response.Body "无 story-change 权限"
// @Failure     404 {object} response.Body "需求不存在"
// @Failure     409 {object} response.Body "状态不允许变更，或版本冲突"
// @Router      /stories/{id}/change [post]
func (h *StoryHandler) Change(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	req := dto.StoryChangeReq{}
	if !response.Bind(c, &req) {
		return
	}
	h.apply(c, id, service.ActionInput{
		Action:  "change",
		Comment: req.Comment,
		Version: req.Version,
		Title:   req.Title,
		Spec:    req.Spec,
		Verify:  req.Verify,
		Reason:  req.Reason,
		Files:   req.Files,
	})
}

// Close 处理 POST /stories/:id/close。
//
// @Summary     关闭需求
// @Description closedReason 必填（done/subdivided/duplicate/postponed/willnotdo/cancel/bydesign）；选 duplicate 时必须给出 duplicateStoryId。
// @Tags        需求
// @Accept      json
// @Produce     json
// @Param       id  path int           true "需求 ID"
// @Param       req body dto.StoryCloseReq true "关闭原因"
// @Security    BearerAuth
// @Success     200 {object} response.Body{data=dto.StoryDetailResp}
// @Failure     400 {object} response.Body "参数不合法，或未给出关闭原因"
// @Failure     401 {object} response.Body "未登录或令牌已失效"
// @Failure     403 {object} response.Body "无 story-close 权限"
// @Failure     404 {object} response.Body "需求不存在"
// @Failure     409 {object} response.Body "需求已关闭"
// @Router      /stories/{id}/close [post]
func (h *StoryHandler) Close(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	req := dto.StoryCloseReq{}
	if !response.Bind(c, &req) {
		return
	}
	h.apply(c, id, service.ActionInput{
		Action:           "close",
		Comment:          req.Comment,
		ClosedReason:     req.ClosedReason,
		DuplicateStoryID: req.DuplicateStoryID,
	})
}

// Activate 处理 POST /stories/:id/activate。
//
// @Summary     激活已关闭的需求
// @Description 不是简单回到 active：服务层会读最近一条状态变更历史，恢复到「关闭之前」那个状态（历史异常时才退回 active）。关闭痕迹（closedBy/Date/Reason）会被清掉。可以顺带指派负责人。
// @Tags        需求
// @Accept      json
// @Produce     json
// @Param       id  path int               true "需求 ID"
// @Param       req body dto.StoryActivateReq true "激活时可选的指派"
// @Security    BearerAuth
// @Success     200 {object} response.Body{data=dto.StoryDetailResp}
// @Failure     400 {object} response.Body "指定的指派人不存在"
// @Failure     401 {object} response.Body "未登录或令牌已失效"
// @Failure     403 {object} response.Body "无 story-close 权限"
// @Failure     404 {object} response.Body "需求不存在"
// @Failure     409 {object} response.Body "需求未处于关闭态"
// @Router      /stories/{id}/activate [post]
func (h *StoryHandler) Activate(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	req := dto.StoryActivateReq{}
	if !response.Bind(c, &req) {
		return
	}
	h.apply(c, id, service.ActionInput{
		Action:     "activate",
		Comment:    req.Comment,
		AssignedTo: req.AssignedTo,
	})
}

// Assign 处理 POST /stories/:id/assign。
//
// @Summary     指派需求负责人
// @Description 只改负责人，不改状态。被指派人会收到一条待办；指派给自己不通知。
// @Tags        需求
// @Accept      json
// @Produce     json
// @Param       id  path int             true "需求 ID"
// @Param       req body dto.StoryAssignReq true "新的负责人"
// @Security    BearerAuth
// @Success     200 {object} response.Body{data=dto.StoryDetailResp}
// @Failure     400 {object} response.Body "参数不合法，或指派人不存在"
// @Failure     401 {object} response.Body "未登录或令牌已失效"
// @Failure     403 {object} response.Body "无 story-assign 权限"
// @Failure     404 {object} response.Body "需求不存在"
// @Failure     409 {object} response.Body "评审中与已关闭的需求不能指派"
// @Router      /stories/{id}/assign [post]
func (h *StoryHandler) Assign(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	req := dto.StoryAssignReq{}
	if !response.Bind(c, &req) {
		return
	}
	h.apply(c, id, service.ActionInput{
		Action:     "assign",
		Comment:    req.Comment,
		AssignedTo: req.AssignedTo,
	})
}

// Stage 处理 POST /stories/:id/stage，推进需求阶段。
//
// @Summary     推进需求阶段
// @Description 阶段（wait/planned/…/released/closed）表达「这条需求做到哪一步了」，与 status 的生命周期是两条独立的线，所以它不挂 story-change 权限而挂 story-edit。允许按研发流程回退。
// @Tags        需求
// @Accept      json
// @Produce     json
// @Param       id  path int            true "需求 ID"
// @Param       req body dto.StoryStageReq true "目标阶段"
// @Security    BearerAuth
// @Success     200 {object} response.Body{data=dto.StoryDetailResp}
// @Failure     400 {object} response.Body "阶段名不合法"
// @Failure     401 {object} response.Body "未登录或令牌已失效"
// @Failure     403 {object} response.Body "无 story-edit 权限"
// @Failure     404 {object} response.Body "需求不存在"
// @Router      /stories/{id}/stage [post]
func (h *StoryHandler) Stage(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	req := dto.StoryStageReq{}
	if !response.Bind(c, &req) {
		return
	}
	story, err := h.stories.ChangeStage(c.Request.Context(), id, req.Stage, req.Comment)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, story)
}

// apply 是所有动作 handler 的公共出口。
//
// 它们做的事完全一样：把 DTO 摊平成 ActionInput、调用服务、写响应。
// 集中一处可以保证错误处理与响应形状不会在某个动作上走样。
func (h *StoryHandler) apply(c *gin.Context, id uint, in service.ActionInput) {
	if in.Action == "" {
		response.Fail(c, apperr.Newf(apperr.Internal, "未指定动作"))
		return
	}
	story, err := h.stories.Apply(c.Request.Context(), id, in)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, story)
}
