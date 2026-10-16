package dto

// 产品域出参统一带 Code 字段（如 STORY-12），便于外部集成与全局搜索。
// 禅道把编号藏在 #12 里，对外接口没有稳定编号；这里两者都给。

// ---------------------------------------------------------------------------
// 产品线
// ---------------------------------------------------------------------------

// ProductlineReq 是新建/编辑产品线的请求。
type ProductlineReq struct {
	Name     string `json:"name" validate:"required,max=60"`
	ParentID uint   `json:"parentId"`
	// PO/QD/RD 是三位负责人的用户 id。
	PO uint `json:"po"`
	QD uint `json:"qd"`
	RD uint `json:"rd"`
	// Order 越小越靠前。
	Order int `json:"order"`
}

// ProductlineResp 是产品线出参。
type ProductlineResp struct {
	ID       uint   `json:"id"`
	Name     string `json:"name"`
	ParentID uint   `json:"parentId"`
	Path     string `json:"path"`
	Grade    int    `json:"grade"`
	Order    int    `json:"order"`

	PO uint `json:"po"`
	QD uint `json:"qd"`
	RD uint `json:"rd"`

	// Children 只在树接口里填充。
	Children []ProductlineResp `json:"children,omitempty"`
}

// ---------------------------------------------------------------------------
// 产品
// ---------------------------------------------------------------------------

// ProductReq 是新建/编辑产品的请求。
type ProductReq struct {
	Name          string `json:"name" validate:"required,max=110"`
	Code          string `json:"code" validate:"required,max=45,alphanum"`
	ProductlineID uint   `json:"productlineId"`
	Type          string `json:"type" validate:"omitempty,oneof=normal branch platform"`
	Status        string `json:"status" validate:"omitempty,oneof=normal closed"`
	Desc          string `json:"desc"`

	PO uint `json:"po"`
	QD uint `json:"qd"`
	RD uint `json:"rd"`

	ACL string `json:"acl" validate:"omitempty,oneof=open private"`
	// Whitelist 是私有产品的可见用户 id 列表。
	Whitelist []uint `json:"whitelist"`
	// Reviewer 是需求默认评审人 id 列表。
	Reviewer []uint `json:"reviewer"`
	// ReviewStory 为 false 时新建需求直接进入激活态，不走评审。
	ReviewStory *bool `json:"reviewStory"`

	Order int `json:"order"`
}

// ProductFilterReq 是产品列表的查询参数。
type ProductFilterReq struct {
	ProductlineID uint   `form:"productlineId"`
	Status        string `form:"status"`
	Type          string `form:"type"`
	Keywords      string `form:"keywords"`

	Page  int    `form:"page"`
	Size  int    `form:"size"`
	Order string `form:"order"`
}

// ProductResp 是产品出参。
type ProductResp struct {
	ID            uint   `json:"id"`
	Name          string `json:"name"`
	Code          string `json:"code"`
	ProductlineID uint   `json:"productlineId"`
	ProductlineNm string `json:"productlineName,omitempty"`
	Type          string `json:"type"`
	Status        string `json:"status"`
	Desc          string `json:"desc"`

	PO uint `json:"po"`
	QD uint `json:"qd"`
	RD uint `json:"rd"`
	// POName/QDName/RDName 是补全后的姓名，前端不必再查用户表。
	POName string `json:"poName,omitempty"`
	QDName string `json:"qdName,omitempty"`
	RDName string `json:"rdName,omitempty"`

	ACL         string `json:"acl"`
	Whitelist   []uint `json:"whitelist"`
	Reviewer    []uint `json:"reviewer"`
	ReviewStory bool   `json:"reviewStory"`

	Order int `json:"order"`

	// 统计字段，仅详情接口填充（列表接口留零以免 N+1）。
	StoryCount int64 `json:"storyCount"`
	PlanCount  int64 `json:"planCount"`
}

// ProductBrief 是产品简要信息，供下拉与筛选栏使用。
type ProductBrief struct {
	ID   uint   `json:"id"`
	Name string `json:"name"`
	Code string `json:"code"`
	// ACL 让前端能对私有产品加个标记。
	ACL string `json:"acl"`
}

// ---------------------------------------------------------------------------
// 模块树
// ---------------------------------------------------------------------------

// ModuleReq 是新建/编辑模块的请求。
type ModuleReq struct {
	// Root 是所属产品 id。
	Root uint `json:"root" validate:"required"`
	// Type 取 story / task / bug。
	Type     string `json:"type" validate:"required,oneof=story task bug"`
	ParentID uint   `json:"parentId"`

	Name  string `json:"name" validate:"required,max=60"`
	Short string `json:"short" validate:"omitempty,max=60"`
	Owner uint   `json:"owner"`
	Desc  string `json:"desc"`
	Order int    `json:"order"`
}

// ModuleResp 是模块出参，树形结构。
type ModuleResp struct {
	ID       uint   `json:"id"`
	Root     uint   `json:"root"`
	Type     string `json:"type"`
	Name     string `json:"name"`
	Short    string `json:"short"`
	ParentID uint   `json:"parentId"`
	Path     string `json:"path"`
	Grade    int    `json:"grade"`
	Order    int    `json:"order"`
	Owner    uint   `json:"owner"`
	Desc     string `json:"desc"`

	// Children 在树接口里填充，扁平接口里为空。
	Children []ModuleResp `json:"children,omitempty"`
}

// ---------------------------------------------------------------------------
// 产品计划
// ---------------------------------------------------------------------------

// PlanReq 是新建/编辑产品计划的请求。
type PlanReq struct {
	ProductID uint   `json:"productId" validate:"required"`
	Title     string `json:"title" validate:"required,max=90"`
	Status    string `json:"status" validate:"omitempty,oneof=wait doing done closed"`
	Desc      string `json:"desc"`
	// Begin/End 是计划起止日期，格式 YYYY-MM-DD。
	Begin string `json:"begin" validate:"omitempty,datetime=2006-01-02"`
	End   string `json:"end" validate:"omitempty,datetime=2006-01-02"`
	Order int    `json:"order"`
}

// PlanResp 是产品计划出参。
type PlanResp struct {
	ID        uint   `json:"id"`
	ProductID uint   `json:"productId"`
	Title     string `json:"title"`
	Status    string `json:"status"`
	Desc      string `json:"desc"`
	Begin     string `json:"begin"`
	End       string `json:"end"`

	OpenedBy uint   `json:"openedBy"`
	OpenedAt string `json:"openedDate"`
	Order    int    `json:"order"`

	// StoryCount 计划下的需求数，仅详情填充。
	StoryCount int64 `json:"storyCount"`
}
