package handler

import (
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/xywf221/xuliu/internal/dto"
	"github.com/xywf221/xuliu/internal/pkg/response"
	"github.com/xywf221/xuliu/internal/service"
)

// ProductlineHandler 处理产品线请求。
type ProductlineHandler struct {
	lines *service.ProductlineService
}

// NewProductlineHandler 构造产品线 handler。
func NewProductlineHandler(lines *service.ProductlineService) *ProductlineHandler {
	return &ProductlineHandler{lines: lines}
}

// Tree 处理 GET /productlines/tree。
//
// @Summary     产品线树
// @Description 返回全部产品线组成的树形结构，children 由服务层按 parentId 组装。登录即可读，用于各处选择产品线。
// @Tags        产品线
// @Produce     json
// @Security    BearerAuth
// @Success     200 {object} response.Body{data=[]dto.ProductlineResp}
// @Failure     401 {object} response.Body "未登录或令牌已失效"
// @Router      /productlines/tree [get]
func (h *ProductlineHandler) Tree(c *gin.Context) {
	tree, err := h.lines.Tree(c.Request.Context())
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, tree)
}

// Create 处理 POST /productlines。
//
// @Summary     新建产品线
// @Description 新建一条产品线。
//
// @Tags        产品线
// @Accept      json
// @Produce     json
// @Param       req body dto.ProductlineReq true "产品线信息"
// @Security    BearerAuth
// @Success     201 {object} response.Body{data=dto.ProductlineResp}
// @Failure     400 {object} response.Body "参数不合法"
// @Failure     401 {object} response.Body "未登录或令牌已失效"
// @Failure     403 {object} response.Body "无 productline-manage 权限"
// @Failure     409 {object} response.Body "同级下已存在同名产品线"
// @Router      /productlines [post]
func (h *ProductlineHandler) Create(c *gin.Context) {
	req := dto.ProductlineReq{}
	if !response.Bind(c, &req) {
		return
	}
	line, err := h.lines.Create(c.Request.Context(), req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.Created(c, line)
}

// Update 处理 PUT /productlines/:id。
//
// @Summary     编辑产品线
// @Description 修改产品线的名称、三位负责人与排序。不支持改父节点：移动子树需要重算 path/grade，是独立的操作。
// @Tags        产品线
// @Accept      json
// @Produce     json
// @Param       id  path int                  true "产品线 ID"
// @Param       req body dto.ProductlineReq   true "产品线信息"
// @Security    BearerAuth
// @Success     200 {object} response.Body{data=dto.ProductlineResp}
// @Failure     400 {object} response.Body "参数不合法"
// @Failure     401 {object} response.Body "未登录或令牌已失效"
// @Failure     403 {object} response.Body "无 productline-manage 权限"
// @Failure     404 {object} response.Body "产品线不存在"
// @Failure     409 {object} response.Body "同级下已存在同名产品线"
// @Router      /productlines/{id} [put]
func (h *ProductlineHandler) Update(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	req := dto.ProductlineReq{}
	if !response.Bind(c, &req) {
		return
	}
	line, err := h.lines.Update(c.Request.Context(), id, req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, line)
}

// Delete 处理 DELETE /productlines/:id。
//
// @Summary     删除产品线
// @Description 删除产品线。仍有子产品线或挂有产品时拒绝删除。
// @Tags        产品线
// @Produce     json
// @Param       id path int true "产品线 ID"
// @Security    BearerAuth
// @Success     204 {string} string "无内容"
// @Failure     401 {object} response.Body "未登录或令牌已失效"
// @Failure     403 {object} response.Body "无 productline-manage 权限"
// @Failure     404 {object} response.Body "产品线不存在"
// @Failure     409 {object} response.Body "该产品线下还有产品，无法删除"
// @Router      /productlines/{id} [delete]
func (h *ProductlineHandler) Delete(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	if err := h.lines.Delete(c.Request.Context(), id); err != nil {
		response.Fail(c, err)
		return
	}
	response.NoContent(c)
}

// ---------------------------------------------------------------------------
// 产品
// ---------------------------------------------------------------------------

// ProductHandler 处理产品请求。
type ProductHandler struct {
	products *service.ProductService
}

// NewProductHandler 构造产品 handler。
func NewProductHandler(products *service.ProductService) *ProductHandler {
	return &ProductHandler{products: products}
}

// List 处理 GET /products。
//
// @Summary     产品列表
// @Description 分页查询产品主信息，data 为分页对象 {list,total,page,size}，list 元素为 dto.ProductResp。所有筛选项都是「空值即不过滤」；可见性由服务层按 ACL 过滤。
// @Tags        产品
// @Produce     json
// @Param       productlineId query int    false "按产品线过滤"
// @Param       status        query string false "状态：normal/closed"
// @Param       type          query string false "类型：normal/branch/platform"
// @Param       keywords      query string false "名称 / 代号模糊匹配"
// @Param       page          query int    false "页码，默认 1"
// @Param       size          query int    false "每页条数，默认 20，最大 200"
// @Param       order         query string false "排序，如 id_desc"
// @Security    BearerAuth
// @Success     200 {object} response.Body "分页对象，data 为 {list,total,page,size}，list 元素为 dto.ProductResp"
// @Failure     400 {object} response.Body "参数不合法"
// @Failure     401 {object} response.Body "未登录或令牌已失效"
// @Failure     403 {object} response.Body "无 product-browse 权限"
// @Router      /products [get]
func (h *ProductHandler) List(c *gin.Context) {
	req := dto.ProductFilterReq{}
	if !response.Bind(c, &req) {
		return
	}
	result, err := h.products.List(c.Request.Context(), req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, result)
}

// Get 处理 GET /products/:id。
//
// @Summary     产品详情
// @Description 取单个产品，额外补全产品线名与三位负责人姓名，并带上需求数 storyCount 与计划数 planCount。
// @Tags        产品
// @Produce     json
// @Param       id path int true "产品 ID"
// @Security    BearerAuth
// @Success     200 {object} response.Body{data=dto.ProductResp}
// @Failure     401 {object} response.Body "未登录或令牌已失效"
// @Failure     403 {object} response.Body "无 product-browse 权限，或无权访问该私有产品"
// @Failure     404 {object} response.Body "产品不存在"
// @Router      /products/{id} [get]
func (h *ProductHandler) Get(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	product, err := h.products.Get(c.Request.Context(), id)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, product)
}

// Create 处理 POST /products。
//
// @Summary     新建产品
// @Description 新建产品。名称与代号各自唯一；acl=private 时按 whitelist 控制可见性。
// @Tags        产品
// @Accept      json
// @Produce     json
// @Param       req body dto.ProductReq true "产品信息"
// @Security    BearerAuth
// @Success     201 {object} response.Body{data=dto.ProductResp}
// @Failure     400 {object} response.Body "参数不合法"
// @Failure     401 {object} response.Body "未登录或令牌已失效"
// @Failure     403 {object} response.Body "无 product-create 权限"
// @Failure     404 {object} response.Body "所属产品线不存在"
// @Failure     409 {object} response.Body "已存在同名产品或产品代号已被占用"
// @Router      /products [post]
func (h *ProductHandler) Create(c *gin.Context) {
	req := dto.ProductReq{}
	if !response.Bind(c, &req) {
		return
	}
	product, err := h.products.Create(c.Request.Context(), req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.Created(c, product)
}

// Update 处理 PUT /products/:id。
//
// @Summary     编辑产品
// @Description 修改产品信息，并校验名称与代号唯一性。
// @Tags        产品
// @Accept      json
// @Produce     json
// @Param       id  path int              true "产品 ID"
// @Param       req body dto.ProductReq   true "产品信息"
// @Security    BearerAuth
// @Success     200 {object} response.Body{data=dto.ProductResp}
// @Failure     400 {object} response.Body "参数不合法"
// @Failure     401 {object} response.Body "未登录或令牌已失效"
// @Failure     403 {object} response.Body "无 product-edit 权限，或无权访问该私有产品"
// @Failure     404 {object} response.Body "产品不存在"
// @Failure     409 {object} response.Body "已存在同名产品或产品代号已被占用"
// @Router      /products/{id} [put]
func (h *ProductHandler) Update(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	req := dto.ProductReq{}
	if !response.Bind(c, &req) {
		return
	}
	product, err := h.products.Update(c.Request.Context(), id, req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, product)
}

// Delete 处理 DELETE /products/:id。
//
// @Summary     删除产品
// @Description 删除产品。产品下仍有未关闭的需求时拒绝删除；模块树与产品计划会随产品一起删除。
// @Tags        产品
// @Produce     json
// @Param       id path int true "产品 ID"
// @Security    BearerAuth
// @Success     204 {string} string "无内容"
// @Failure     401 {object} response.Body "未登录或令牌已失效"
// @Failure     403 {object} response.Body "无 product-delete 权限，或无权访问该私有产品"
// @Failure     404 {object} response.Body "产品不存在"
// @Failure     409 {object} response.Body "该产品下还有需求，无法删除"
// @Router      /products/{id} [delete]
func (h *ProductHandler) Delete(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	if err := h.products.Delete(c.Request.Context(), id); err != nil {
		response.Fail(c, err)
		return
	}
	response.NoContent(c)
}

// Brief 处理 GET /products/brief。
//
// @Summary     产品简要列表
// @Description 返回当前用户可见产品的简要信息（id / name / code / acl），供下拉与筛选栏使用，最多 500 条；可见范围由服务层按 ACL 过滤。
// @Tags        产品
// @Produce     json
// @Security    BearerAuth
// @Success     200 {object} response.Body{data=[]dto.ProductBrief}
// @Failure     401 {object} response.Body "未登录或令牌已失效"
// @Router      /products/brief [get]
func (h *ProductHandler) Brief(c *gin.Context) {
	products, err := h.products.Brief(c.Request.Context())
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, products)
}

// ---------------------------------------------------------------------------
// 模块树
// ---------------------------------------------------------------------------

// ModuleHandler 处理模块树请求。
type ModuleHandler struct {
	modules *service.ModuleService
}

// NewModuleHandler 构造模块 handler。
func NewModuleHandler(modules *service.ModuleService) *ModuleHandler {
	return &ModuleHandler{modules: modules}
}

// Tree 处理 GET /products/:id/modules?type=story。
//
// @Summary     模块树
// @Description 返回指定产品的模块树，按类型分树。需求的模块不能挂到任务上 —— 传错 type 会返回 module_wrong_root。
// @Tags        模块
// @Produce     json
// @Param       id   path  int    true  "产品 ID"
// @Param       type query string false "模块类型：story（默认）/ task / bug"
// @Security    BearerAuth
// @Success     200 {object} response.Body{data=[]dto.ModuleResp} "树形数组，子节点在 children 里"
// @Failure     400 {object} response.Body "产品 ID 不合法"
// @Failure     401 {object} response.Body "未登录或令牌已失效"
// @Failure     403 {object} response.Body "无 product-browse 权限"
// @Router      /products/{id}/modules [get]
func (h *ModuleHandler) Tree(c *gin.Context) {
	root, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || root == 0 {
		response.Fail(c, invalidID("id", c.Param("id")))
		return
	}
	// 默认取需求模块树：产品详情页最常看的就是它。
	moduleType := c.DefaultQuery("type", "story")

	tree, err := h.modules.Tree(c.Request.Context(), uint(root), moduleType)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, tree)
}

// Get 处理 GET /modules/:id。
//
// @Summary     模块详情
// @Tags        模块
// @Produce     json
// @Param       id path int true "模块 ID"
// @Security    BearerAuth
// @Success     200 {object} response.Body{data=dto.ModuleResp}
// @Failure     401 {object} response.Body "未登录或令牌已失效"
// @Failure     403 {object} response.Body "无 product-browse 权限"
// @Failure     404 {object} response.Body "模块不存在"
// @Router      /modules/{id} [get]
func (h *ModuleHandler) Get(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	module, err := h.modules.Get(c.Request.Context(), id)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, module)
}

// Create 处理 POST /modules。
//
// @Summary     新建模块
// @Description Root 是所属产品，Type 决定这是需求 / 任务 / 缺陷的模块树。ParentID 留空则建为根模块。
// @Tags        模块
// @Accept      json
// @Produce     json
// @Param       req body dto.ModuleReq true "模块内容"
// @Security    BearerAuth
// @Success     201 {object} response.Body{data=dto.ModuleResp}
// @Failure     400 {object} response.Body "参数不合法"
// @Failure     401 {object} response.Body "未登录或令牌已失效"
// @Failure     403 {object} response.Body "无 module-manage 权限"
// @Router      /modules [post]
func (h *ModuleHandler) Create(c *gin.Context) {
	req := dto.ModuleReq{}
	if !response.Bind(c, &req) {
		return
	}
	module, err := h.modules.Create(c.Request.Context(), req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.Created(c, module)
}

// Update 处理 PUT /modules/:id。
//
// @Summary     编辑模块
// @Description 修改模块名称、负责人与排序。不支持改父节点：移动子树要重算整条 path，是另一个动作。
// @Tags        模块
// @Accept      json
// @Produce     json
// @Param       id  path int           true "模块 ID"
// @Param       req body dto.ModuleReq true "模块内容（整体覆盖）"
// @Security    BearerAuth
// @Success     200 {object} response.Body{data=dto.ModuleResp}
// @Failure     400 {object} response.Body "参数不合法"
// @Failure     401 {object} response.Body "未登录或令牌已失效"
// @Failure     403 {object} response.Body "无 module-manage 权限"
// @Failure     404 {object} response.Body "模块不存在"
// @Router      /modules/{id} [put]
func (h *ModuleHandler) Update(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	req := dto.ModuleReq{}
	if !response.Bind(c, &req) {
		return
	}
	module, err := h.modules.Update(c.Request.Context(), id, req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, module)
}

// Delete 处理 DELETE /modules/:id。
//
// @Summary     删除模块
// @Description 模块下还有子模块或对象时拒绝删除，不会连带清空 —— 删掉一棵有内容的树难以挽回。
// @Tags        模块
// @Produce     json
// @Param       id path int true "模块 ID"
// @Security    BearerAuth
// @Success     204 {string} string "无内容"
// @Failure     401 {object} response.Body "未登录或令牌已失效"
// @Failure     403 {object} response.Body "无 module-manage 权限"
// @Failure     404 {object} response.Body "模块不存在"
// @Failure     409 {object} response.Body "模块非空，不允许删除"
// @Router      /modules/{id} [delete]
func (h *ModuleHandler) Delete(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	if err := h.modules.Delete(c.Request.Context(), id); err != nil {
		response.Fail(c, err)
		return
	}
	response.NoContent(c)
}

// ---------------------------------------------------------------------------
// 产品计划
// ---------------------------------------------------------------------------

// PlanHandler 处理产品计划请求。
type PlanHandler struct {
	plans *service.PlanService
}

// NewPlanHandler 构造产品计划 handler。
func NewPlanHandler(plans *service.PlanService) *PlanHandler {
	return &PlanHandler{plans: plans}
}

// List 处理 GET /products/:id/plans。
//
// @Summary     产品计划列表
// @Description 分页返回某产品下的计划。所有筛选项都是「空值即不过滤」。
// @Tags        产品计划
// @Produce     json
// @Param       id       path  int    true  "产品 ID"
// @Param       status   query string false "计划状态：wait/doing/done/closed"
// @Param       keywords query string false "标题模糊匹配"
// @Param       page     query int    false "页码，默认 1"
// @Param       size     query int    false "每页条数，默认 20，最大 200"
// @Param       order    query string false "排序，如 id_desc"
// @Security    BearerAuth
// @Success     200 {object} response.Body "分页对象，data 为 {list,total,page,size}，list 元素为 dto.PlanResp"
// @Failure     400 {object} response.Body "产品 ID 不合法"
// @Failure     401 {object} response.Body "未登录或令牌已失效"
// @Failure     403 {object} response.Body "无 product-browse 权限"
// @Router      /products/{id}/plans [get]
func (h *PlanHandler) List(c *gin.Context) {
	productID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || productID == 0 {
		response.Fail(c, invalidID("id", c.Param("id")))
		return
	}

	params := queryParams(c)
	result, err := h.plans.List(c.Request.Context(), uint(productID),
		c.Query("status"), c.Query("keywords"), params)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, result)
}

// Get 处理 GET /plans/:id。
//
// @Summary     产品计划详情
// @Tags        产品计划
// @Produce     json
// @Param       id path int true "计划 ID"
// @Security    BearerAuth
// @Success     200 {object} response.Body{data=dto.PlanResp}
// @Failure     401 {object} response.Body "未登录或令牌已失效"
// @Failure     403 {object} response.Body "无 product-browse 权限"
// @Failure     404 {object} response.Body "计划不存在"
// @Router      /plans/{id} [get]
func (h *PlanHandler) Get(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	plan, err := h.plans.Get(c.Request.Context(), id)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, plan)
}

// Create 处理 POST /plans。
//
// @Summary     新建产品计划
// @Tags        产品计划
// @Accept      json
// @Produce     json
// @Param       req body dto.PlanReq true "计划内容"
// @Security    BearerAuth
// @Success     201 {object} response.Body{data=dto.PlanResp}
// @Failure     400 {object} response.Body "参数不合法"
// @Failure     401 {object} response.Body "未登录或令牌已失效"
// @Failure     403 {object} response.Body "无 plan-manage 权限"
// @Failure     404 {object} response.Body "所属产品不存在"
// @Router      /plans [post]
func (h *PlanHandler) Create(c *gin.Context) {
	req := dto.PlanReq{}
	if !response.Bind(c, &req) {
		return
	}
	plan, err := h.plans.Create(c.Request.Context(), req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.Created(c, plan)
}

// Update 处理 PUT /plans/:id。
//
// @Summary     编辑产品计划
// @Tags        产品计划
// @Accept      json
// @Produce     json
// @Param       id  path int         true "计划 ID"
// @Param       req body dto.PlanReq true "计划内容（整体覆盖）"
// @Security    BearerAuth
// @Success     200 {object} response.Body{data=dto.PlanResp}
// @Failure     400 {object} response.Body "参数不合法"
// @Failure     401 {object} response.Body "未登录或令牌已失效"
// @Failure     403 {object} response.Body "无 plan-manage 权限"
// @Failure     404 {object} response.Body "计划不存在"
// @Router      /plans/{id} [put]
func (h *PlanHandler) Update(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	req := dto.PlanReq{}
	if !response.Bind(c, &req) {
		return
	}
	plan, err := h.plans.Update(c.Request.Context(), id, req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, plan)
}

// Delete 处理 DELETE /plans/:id。
//
// @Summary     删除产品计划
// @Description 计划下还挂着需求时拒绝删除。
// @Tags        产品计划
// @Produce     json
// @Param       id path int true "计划 ID"
// @Security    BearerAuth
// @Success     204 {string} string "无内容"
// @Failure     401 {object} response.Body "未登录或令牌已失效"
// @Failure     403 {object} response.Body "无 plan-manage 权限"
// @Failure     404 {object} response.Body "计划不存在"
// @Failure     409 {object} response.Body "计划下还有需求，不允许删除"
// @Router      /plans/{id} [delete]
func (h *PlanHandler) Delete(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	if err := h.plans.Delete(c.Request.Context(), id); err != nil {
		response.Fail(c, err)
		return
	}
	response.NoContent(c)
}
