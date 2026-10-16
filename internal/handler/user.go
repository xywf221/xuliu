package handler

import (
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/xywf221/xuliu/internal/dto"
	"github.com/xywf221/xuliu/internal/identity"
	"github.com/xywf221/xuliu/internal/pkg/apperr"
	"github.com/xywf221/xuliu/internal/pkg/pagination"
	"github.com/xywf221/xuliu/internal/pkg/response"
	"github.com/xywf221/xuliu/internal/service"
)

// UserHandler 处理用户相关请求。
type UserHandler struct {
	users *service.UserService
	auth  *service.AuthService
}

// NewUserHandler 构造用户 handler。
func NewUserHandler(users *service.UserService, auth *service.AuthService) *UserHandler {
	return &UserHandler{users: users, auth: auth}
}

// List 处理 GET /users。
//
// @Summary     用户列表
// @Description 分页查询用户，data 为分页对象 {list,total,page,size}，list 元素为 dto.UserResp。所有筛选项都是「空值即不过滤」。
// @Tags        用户
// @Produce     json
// @Param       deptId         query int    false "按部门过滤"
// @Param       includeSubDept query bool   false "是否包含子部门"
// @Param       jobRole        query string false "按职位过滤，如 dev/qa/pm"
// @Param       type           query string false "按人员类型过滤：inside/outside/guest"
// @Param       disabled       query bool   false "是否停用；留空表示不过滤"
// @Param       keywords       query string false "在账号、姓名、拼音、邮箱四列上做模糊匹配"
// @Param       page           query int    false "页码，默认 1"
// @Param       size           query int    false "每页条数，默认 20，最大 200"
// @Param       order          query string false "排序，如 id_desc"
// @Security    BearerAuth
// @Success     200 {object} response.Body "分页对象，data 为 {list,total,page,size}，list 元素为 dto.UserResp"
// @Failure     400 {object} response.Body "参数不合法"
// @Failure     401 {object} response.Body "未登录或令牌已失效"
// @Failure     403 {object} response.Body "无 user-browse 权限"
// @Router      /users [get]
func (h *UserHandler) List(c *gin.Context) {
	req := dto.UserFilterReq{}
	if !response.Bind(c, &req) {
		return
	}

	result, err := h.users.List(c.Request.Context(), req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, result)
}

// Get 处理 GET /users/:id。
//
// @Summary     取用户详情
// @Description 返回单个用户的完整信息（含所属权限组与上级姓名），data 为 dto.UserResp。
// @Tags        用户
// @Produce     json
// @Param       id path int true "用户 ID"
// @Security    BearerAuth
// @Success     200 {object} response.Body "用户详情，data 为 dto.UserResp"
// @Failure     400 {object} response.Body "路径参数 id 不合法"
// @Failure     401 {object} response.Body "未登录或令牌已失效"
// @Failure     403 {object} response.Body "无 user-browse 权限"
// @Failure     404 {object} response.Body "用户不存在"
// @Router      /users/{id} [get]
func (h *UserHandler) Get(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}

	user, err := h.users.Get(c.Request.Context(), id)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, user)
}

// Create 处理 POST /users。
//
// @Summary     新建用户
// @Description 创建用户并返回详情（data 为 dto.UserResp）。account 全局唯一；口令强度不足会直接拒绝。
// @Description Groups 传权限组 id 列表，留空表示不加入任何组。
// @Tags        用户
// @Accept      json
// @Produce     json
// @Param       body body dto.CreateUserReq true "新建用户请求"
// @Security    BearerAuth
// @Success     201 {object} response.Body "创建成功，data 为 dto.UserResp"
// @Failure     400 {object} response.Body "参数不合法（含口令强度不足：至少 8 位、需同时含字母与数字）"
// @Failure     401 {object} response.Body "未登录或令牌已失效"
// @Failure     403 {object} response.Body "无 user-create 权限"
// @Failure     404 {object} response.Body "所选部门不存在"
// @Failure     409 {object} response.Body "账号已被占用"
// @Router      /users [post]
func (h *UserHandler) Create(c *gin.Context) {
	req := dto.CreateUserReq{}
	if !response.Bind(c, &req) {
		return
	}

	user, err := h.users.Create(c.Request.Context(), req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.Created(c, user)
}

// Update 处理 PUT /users/:id。
//
// @Summary     编辑用户
// @Description 编辑用户并返回详情（data 为 dto.UserResp）。请求体字段全为指针：不传即不改，
// @Description 传空串是清空该字段（如清空手机号）。Groups 不传表示不动分组，传空数组表示全部移出。
// @Description 不能停用当前登录的账号；不能把自己设为直属上级。
// @Tags        用户
// @Accept      json
// @Produce     json
// @Param       id   path int              true "用户 ID"
// @Param       body body dto.UpdateUserReq true "编辑用户请求"
// @Security    BearerAuth
// @Success     200 {object} response.Body "编辑成功，data 为 dto.UserResp"
// @Failure     400 {object} response.Body "参数不合法（停用自身、上级为自己、入职日期格式错误等）"
// @Failure     401 {object} response.Body "未登录或令牌已失效"
// @Failure     403 {object} response.Body "无 user-edit 权限"
// @Failure     404 {object} response.Body "用户或所选部门不存在"
// @Router      /users/{id} [put]
func (h *UserHandler) Update(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}

	req := dto.UpdateUserReq{}
	if !response.Bind(c, &req) {
		return
	}

	user, err := h.users.Update(c.Request.Context(), id, req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, user)
}

// Delete 处理 DELETE /users/:id。
//
// @Summary     删除用户
// @Description 软删除用户，同时解除其全部分组关系。已被需求/任务/缺陷/工时/项目/团队引用的用户
// @Description 不允许删除（否则历史数据的经办人会指向不存在的用户），只能改为停用。
// @Tags        用户
// @Produce     json
// @Param       id path int true "用户 ID"
// @Security    BearerAuth
// @Success     204 {string} string "无内容"
// @Failure     400 {object} response.Body "试图删除当前登录的账号，或路径参数不合法"
// @Failure     401 {object} response.Body "未登录或令牌已失效"
// @Failure     403 {object} response.Body "无 user-delete 权限"
// @Failure     404 {object} response.Body "用户不存在"
// @Failure     409 {object} response.Body "该用户已被业务数据引用，请改为停用"
// @Router      /users/{id} [delete]
func (h *UserHandler) Delete(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}

	if err := h.users.Delete(c.Request.Context(), id); err != nil {
		response.Fail(c, err)
		return
	}
	response.NoContent(c)
}

// ResetPassword 处理 POST /users/:id/password，由管理员重置他人口令。
//
// @Summary     管理员重置口令
// @Description 重置目标用户的登录口令，并顺带清零失败次数、解除锁定 ——
// @Description 管理员重置的常见场景正是「用户把自己锁住了」。本接口不需要目标用户的旧口令。
// @Tags        用户
// @Accept      json
// @Produce     json
// @Param       id   path int                 true "目标用户 ID"
// @Param       body body dto.ResetPasswordReq true "新口令"
// @Security    BearerAuth
// @Success     200 {object} response.Body "data 为对象，仅一个字段 reset=true"
// @Failure     400 {object} response.Body "参数不合法（口令强度不足：至少 8 位、需同时含字母与数字）"
// @Failure     401 {object} response.Body "未登录或令牌已失效"
// @Failure     403 {object} response.Body "无 user-edit 权限"
// @Failure     404 {object} response.Body "目标用户不存在"
// @Router      /users/{id}/password [post]
func (h *UserHandler) ResetPassword(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}

	req := dto.ResetPasswordReq{}
	if !response.Bind(c, &req) {
		return
	}

	operator := identity.MustUser(c.Request.Context())
	if err := h.auth.ResetPassword(c.Request.Context(), id, req.NewPassword, operator.ID); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, gin.H{"reset": true})
}

// Brief 处理 GET /users/brief，返回用户简要列表供下拉选择。
//
// @Summary     用户简要列表
// @Description 供「指派给谁」这类下拉使用：只含未停用的用户，按姓名升序，data 为 dto.UserBrief 数组。
// @Description 登录即可调用、不要求 user-browse —— 选经办人不该是管理权限。
// @Tags        用户
// @Produce     json
// @Param       keywords query string false "在账号、姓名、拼音、邮箱四列上做模糊匹配"
// @Param       limit    query int    false "返回条数上限，默认 50；小于等于 0 或大于 200 都回落到 50"
// @Security    BearerAuth
// @Success     200 {object} response.Body{data=[]dto.UserBrief} "用户简要列表"
// @Failure     401 {object} response.Body "未登录或令牌已失效"
// @Router      /users/brief [get]
func (h *UserHandler) Brief(c *gin.Context) {
	keywords := c.Query("keywords")
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))

	users, err := h.users.Brief(c.Request.Context(), keywords, limit)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, users)
}

// pathID 解析路径参数里的 id。
//
// 失败时已经写出了响应，调用方直接 return。
// 集中在这里是为了避免每个 handler 重复写一遍 strconv 的错误分支。
func pathID(c *gin.Context) (uint, bool) {
	raw := c.Param("id")
	if raw == "" {
		response.Fail(c, invalidID("id", raw))
		return 0, false
	}

	parsed, err := strconv.ParseUint(raw, 10, 64)
	if err != nil || parsed == 0 {
		response.Fail(c, invalidID("id", raw))
		return 0, false
	}
	return uint(parsed), true
}

// invalidID 构造「路径参数不合法」的错误。
//
// 与 pathID 分开是因为有些路由的主键不叫 id（如 /products/:id/modules 里的
// 产品 id），那些 handler 自己取值、自己报错，但文案要一致。
func invalidID(name, raw string) error {
	if raw == "" {
		return apperr.Newf(apperr.InvalidParam, "缺少路径参数 %s", name)
	}
	return apperr.Newf(apperr.InvalidParam, "路径参数 %s 不合法：%s", name, raw)
}

// queryParams 从查询字符串解析分页参数。
//
// 各列表接口共用同一套 page/size/order 语义，集中解析保证行为一致。
func queryParams(c *gin.Context) pagination.Params {
	page, _ := strconv.Atoi(c.Query("page"))
	size, _ := strconv.Atoi(c.Query("size"))
	return pagination.Params{
		Page:  page,
		Size:  size,
		Order: c.Query("order"),
	}
}
