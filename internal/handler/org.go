package handler

import (
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/xywf221/xuliu/internal/dto"
	"github.com/xywf221/xuliu/internal/pkg/apperr"
	"github.com/xywf221/xuliu/internal/pkg/response"
	"github.com/xywf221/xuliu/internal/service"
)

// DeptHandler 处理部门相关请求。
type DeptHandler struct {
	depts *service.DeptService
}

// NewDeptHandler 构造部门 handler。
func NewDeptHandler(depts *service.DeptService) *DeptHandler {
	return &DeptHandler{depts: depts}
}

// Tree 处理 GET /depts/tree，返回部门树。
//
// @Summary     部门树
// @Description 返回完整部门树，data 为嵌套的 dto.DeptResp 数组，子部门挂在各节点的 Children 字段上。登录即可访问，不要求部门浏览权限：各处选择部门时都要用它。
// @Tags        部门
// @Produce     json
// @Security    BearerAuth
// @Success     200 {object} response.Body{data=[]dto.DeptResp}
// @Failure     401 {object} response.Body "未登录或令牌已失效"
// @Router      /depts/tree [get]
func (h *DeptHandler) Tree(c *gin.Context) {
	tree, err := h.depts.Tree(c.Request.Context())
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, tree)
}

// Get 处理 GET /depts/:id。
//
// @Summary     部门详情
// @Description 按 id 查询单个部门。
// @Tags        部门
// @Produce     json
// @Param       id path int true "部门 id"
// @Security    BearerAuth
// @Success     200 {object} response.Body{data=dto.DeptResp}
// @Failure     400 {object} response.Body "路径参数不合法"
// @Failure     401 {object} response.Body "未登录或令牌已失效"
// @Failure     403 {object} response.Body "无 dept-browse 权限"
// @Failure     404 {object} response.Body "部门不存在"
// @Router      /depts/{id} [get]
func (h *DeptHandler) Get(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}

	dept, err := h.depts.Get(c.Request.Context(), id)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, dept)
}

// Create 处理 POST /depts。
//
// @Summary     新建部门
// @Description 创建部门；parentId 为 0 表示顶级部门。
// @Tags        部门
// @Accept      json
// @Produce     json
// @Param       req body dto.CreateDeptReq true "部门信息"
// @Security    BearerAuth
// @Success     201 {object} response.Body{data=dto.DeptResp}
// @Failure     400 {object} response.Body "参数不合法"
// @Failure     401 {object} response.Body "未登录或令牌已失效"
// @Failure     403 {object} response.Body "无 dept-manage 权限"
// @Failure     409 {object} response.Body "同级部门名称重复"
// @Router      /depts [post]
func (h *DeptHandler) Create(c *gin.Context) {
	req := dto.CreateDeptReq{}
	if !response.Bind(c, &req) {
		return
	}

	dept, err := h.depts.Create(c.Request.Context(), req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.Created(c, dept)
}

// Update 处理 PUT /depts/:id。
//
// @Summary     编辑部门
// @Description 更新部门信息。所有字段可选，未提供的字段不改动；改 parentId 即移动部门（会同步子树层级）。
// @Tags        部门
// @Accept      json
// @Produce     json
// @Param       id  path int                 true "部门 id"
// @Param       req body dto.UpdateDeptReq true "待更新字段"
// @Security    BearerAuth
// @Success     200 {object} response.Body{data=dto.DeptResp}
// @Failure     400 {object} response.Body "参数不合法，或把部门移到自己的子树下"
// @Failure     401 {object} response.Body "未登录或令牌已失效"
// @Failure     403 {object} response.Body "无 dept-manage 权限"
// @Failure     404 {object} response.Body "部门不存在"
// @Failure     409 {object} response.Body "同级部门名称重复"
// @Router      /depts/{id} [put]
func (h *DeptHandler) Update(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}

	req := dto.UpdateDeptReq{}
	if !response.Bind(c, &req) {
		return
	}

	dept, err := h.depts.Update(c.Request.Context(), id, req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, dept)
}

// Delete 处理 DELETE /depts/:id。
//
// @Summary     删除部门
// @Description 删除部门。存在子部门或直属用户时不允许删除。
// @Tags        部门
// @Produce     json
// @Param       id path int true "部门 id"
// @Security    BearerAuth
// @Success     204 {string} string "无内容"
// @Failure     400 {object} response.Body "路径参数不合法，或仍有子部门 / 用户"
// @Failure     401 {object} response.Body "未登录或令牌已失效"
// @Failure     403 {object} response.Body "无 dept-manage 权限"
// @Failure     404 {object} response.Body "部门不存在"
// @Router      /depts/{id} [delete]
func (h *DeptHandler) Delete(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}

	if err := h.depts.Delete(c.Request.Context(), id); err != nil {
		response.Fail(c, err)
		return
	}
	response.NoContent(c)
}

// GroupHandler 处理权限组相关请求。
type GroupHandler struct {
	groups *service.GroupService
}

// NewGroupHandler 构造权限组 handler。
func NewGroupHandler(groups *service.GroupService) *GroupHandler {
	return &GroupHandler{groups: groups}
}

// List 处理 GET /groups。
//
// @Summary     权限组列表
// @Description 分页查询权限组，data 为分页对象 {list,total,page,size}，list 元素为 dto.GroupResp。keywords 对组名模糊匹配。
// @Tags        用户组
// @Produce     json
// @Param       keywords query string false "组名模糊匹配"
// @Param       page     query int    false "页码，默认 1"
// @Param       size     query int    false "每页条数，默认 20，最大 200"
// @Param       order    query string false "排序，如 id_desc"
// @Security    BearerAuth
// @Success     200 {object} response.Body "分页对象，data 为 {list,total,page,size}，list 元素为 dto.GroupResp"
// @Failure     400 {object} response.Body "参数不合法"
// @Failure     401 {object} response.Body "未登录或令牌已失效"
// @Failure     403 {object} response.Body "无 group-browse 权限"
// @Router      /groups [get]
func (h *GroupHandler) List(c *gin.Context) {
	result, err := h.groups.List(c.Request.Context(), c.Query("keywords"), queryParams(c))
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, result)
}

// Get 处理 GET /groups/:id。
//
// @Summary     权限组详情
// @Description 按 id 查询权限组，返回其权限点列表与组内成员。
// @Tags        用户组
// @Produce     json
// @Param       id path int true "权限组 id"
// @Security    BearerAuth
// @Success     200 {object} response.Body{data=dto.GroupResp}
// @Failure     400 {object} response.Body "路径参数不合法"
// @Failure     401 {object} response.Body "未登录或令牌已失效"
// @Failure     403 {object} response.Body "无 group-browse 权限"
// @Failure     404 {object} response.Body "权限组不存在"
// @Router      /groups/{id} [get]
func (h *GroupHandler) Get(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}

	group, err := h.groups.Get(c.Request.Context(), id)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, group)
}

// Create 处理 POST /groups。
//
// @Summary     新建权限组
// @Description 创建权限组，privs 是权限点字符串列表，可在创建时一并授权。
// @Tags        用户组
// @Accept      json
// @Produce     json
// @Param       req body dto.CreateGroupReq true "权限组信息"
// @Security    BearerAuth
// @Success     201 {object} response.Body{data=dto.GroupResp}
// @Failure     400 {object} response.Body "参数不合法"
// @Failure     401 {object} response.Body "未登录或令牌已失效"
// @Failure     403 {object} response.Body "无 group-manage 权限"
// @Failure     409 {object} response.Body "权限组名称重复"
// @Router      /groups [post]
func (h *GroupHandler) Create(c *gin.Context) {
	req := dto.CreateGroupReq{}
	if !response.Bind(c, &req) {
		return
	}

	group, err := h.groups.Create(c.Request.Context(), req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.Created(c, group)
}

// Update 处理 PUT /groups/:id。
//
// @Summary     编辑权限组
// @Description 更新权限组信息。字段可选，未提供不改动；privs 为 null 表示不改动权限点，传空数组表示清空全部权限。
// @Tags        用户组
// @Accept      json
// @Produce     json
// @Param       id  path int                  true "权限组 id"
// @Param       req body dto.UpdateGroupReq true "待更新字段"
// @Security    BearerAuth
// @Success     200 {object} response.Body{data=dto.GroupResp}
// @Failure     400 {object} response.Body "参数不合法"
// @Failure     401 {object} response.Body "未登录或令牌已失效"
// @Failure     403 {object} response.Body "无 group-manage 权限"
// @Failure     404 {object} response.Body "权限组不存在"
// @Failure     409 {object} response.Body "权限组名称重复"
// @Router      /groups/{id} [put]
func (h *GroupHandler) Update(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}

	req := dto.UpdateGroupReq{}
	if !response.Bind(c, &req) {
		return
	}

	group, err := h.groups.Update(c.Request.Context(), id, req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, group)
}

// Delete 处理 DELETE /groups/:id。
//
// @Summary     删除权限组
// @Description 删除权限组。组内仍有成员时不允许删除。
// @Tags        用户组
// @Produce     json
// @Param       id path int true "权限组 id"
// @Security    BearerAuth
// @Success     204 {string} string "无内容"
// @Failure     400 {object} response.Body "路径参数不合法，或组内仍有成员"
// @Failure     401 {object} response.Body "未登录或令牌已失效"
// @Failure     403 {object} response.Body "无 group-manage 权限"
// @Failure     404 {object} response.Body "权限组不存在"
// @Router      /groups/{id} [delete]
func (h *GroupHandler) Delete(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}

	if err := h.groups.Delete(c.Request.Context(), id); err != nil {
		response.Fail(c, err)
		return
	}
	response.NoContent(c)
}

// Privileges 处理 GET /groups/privileges，返回全部权限点供配置界面使用。
//
// 可用 ?groupId=1 标记某组已持有的权限点；不传则所有 Held 均为 false。
//
// @Summary     权限点字典
// @Description 返回全部权限点，按模块分组，data 元素为 dto.PrivGroupResp。传 groupId 时每个权限点的 Held 表示该组是否已持有，专供权限配置界面渲染。
// @Tags        用户组
// @Produce     json
// @Param       groupId query int false "权限组 id，用于标记已持有的权限点；不传则所有 Held 均为 false"
// @Security    BearerAuth
// @Success     200 {object} response.Body{data=[]dto.PrivGroupResp}
// @Failure     400 {object} response.Body "groupId 不合法"
// @Failure     401 {object} response.Body "未登录或令牌已失效"
// @Failure     403 {object} response.Body "无 group-browse 权限"
// @Router      /groups/privileges [get]
func (h *GroupHandler) Privileges(c *gin.Context) {
	var groupID uint
	if raw := c.Query("groupId"); raw != "" {
		parsed, err := strconv.ParseUint(raw, 10, 64)
		if err != nil {
			response.Fail(c, apperr.Newf(apperr.InvalidParam, "groupId 不合法：%s", raw))
			return
		}
		groupID = uint(parsed)
	}

	privs, err := h.groups.Privileges(c.Request.Context(), groupID)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, privs)
}
