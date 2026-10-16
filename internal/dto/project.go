package dto

// 项目域请求与响应。
//
// 项目与迭代是两层实体（不做项目集 program）：项目下挂若干产品，
// 迭代挂在项目下并承载需求与任务。因此在出参里
// **迭代总是带 ProjectID，而任务带 ProjectID + ExecutionID** ——
// 前端面包屑与跳转链接靠这几个 id 拼出来，不需要二次查询。

// ---------------------------------------------------------------------------
// 项目
// ---------------------------------------------------------------------------

// ProjectReq 是新建/编辑项目的请求。
type ProjectReq struct {
	Name string `json:"name" validate:"required,max=90"`
	Code string `json:"code" validate:"required,max=45,alphanum"`
	// Model 取 scrum / waterfall / kanban / agileplus / waterfallplus。
	Model string `json:"model" validate:"omitempty,oneof=scrum waterfall kanban agileplus waterfallplus"`
	// Status 只在编辑时生效：新建时的状态由状态机决定（未开始）。
	Status string `json:"status" validate:"omitempty,oneof=wait doing suspended closed"`
	Desc   string `json:"desc"`

	// Begin/End 是计划起止日期，格式 YYYY-MM-DD。
	Begin string `json:"begin" validate:"omitempty,datetime=2006-01-02"`
	End   string `json:"end" validate:"omitempty,datetime=2006-01-02"`

	// ProductIDs 是关联的产品。至少一个 —— 没有产品的项目
	// 既排不进需求也建不了任务，等于一个空壳。
	ProductIDs []uint `json:"productIds" validate:"required,min=1"`

	PO uint `json:"po"`
	PM uint `json:"pm"`
	QD uint `json:"qd"`
	RD uint `json:"rd"`

	ACL       string `json:"acl" validate:"omitempty,oneof=open private"`
	Whitelist []uint `json:"whitelist"`
	// Multiple 为 true 表示项目下可有多个迭代。
	Multiple *bool `json:"multiple"`

	// Team 是初始团队。项目负责人会自动进团队，不必重复列出。
	Team  []TeamMemberReq `json:"team"`
	Order int             `json:"order"`
}

// ProjectFilterReq 是项目列表的查询参数。
type ProjectFilterReq struct {
	Status   string `form:"status"`
	Model    string `form:"model"`
	Keywords string `form:"keywords"`
	// ProductID 只看关联了该产品的项目。
	ProductID uint `form:"productId"`

	Page  int    `form:"page"`
	Size  int    `form:"size"`
	Order string `form:"order"`
}

// ProjectResp 是项目出参。
type ProjectResp struct {
	ID   uint   `json:"id"`
	Code string `json:"code"`
	// Number 是面向用户的编号，形如 PROJECT-3。
	Number string `json:"number"`

	Name  string `json:"name"`
	Model string `json:"model"`
	// ModelLabel 是模型中文名，前端直接显示。
	ModelLabel string `json:"modelLabel"`
	Status     string `json:"status"`
	// StatusLabel 是状态中文名。
	StatusLabel string `json:"statusLabel"`
	Desc        string `json:"desc"`

	Begin     string  `json:"begin"`
	End       string  `json:"end"`
	RealBegan string  `json:"realBegan"`
	RealEnd   string  `json:"realEnd"`
	Days      int     `json:"days"`
	Progress  int     `json:"progress"`
	Estimate  float64 `json:"estimate"`
	Consumed  float64 `json:"consumed"`
	Left      float64 `json:"left"`

	PO     uint   `json:"po"`
	PM     uint   `json:"pm"`
	QD     uint   `json:"qd"`
	RD     uint   `json:"rd"`
	POName string `json:"poName,omitempty"`
	PMName string `json:"pmName,omitempty"`
	QDName string `json:"qdName,omitempty"`
	RDName string `json:"rdName,omitempty"`

	ACL       string `json:"acl"`
	Whitelist []uint `json:"whitelist"`
	Multiple  bool   `json:"multiple"`

	OpenedBy   uint   `json:"openedBy"`
	OpenedName string `json:"openedByName,omitempty"`
	OpenedDate string `json:"openedDate"`

	ClosedBy     uint   `json:"closedBy"`
	ClosedDate   string `json:"closedDate"`
	ClosedReason string `json:"closedReason"`
	// SuspendedDate 记录最近一次挂起时间。
	SuspendedDate string `json:"suspendedDate"`

	Order int `json:"order"`

	// Products 是关联的产品，详情与列表都会填充。
	Products []ProjectProductResp `json:"products,omitempty"`
	// Team 是团队成员，仅详情填充。
	Team []TeamMemberResp `json:"team,omitempty"`
	// MemberCount 是团队人数，列表接口填充。
	MemberCount int `json:"memberCount"`

	// ExecutionCount/TaskCount/StoryCount 是统计字段，仅详情填充。
	ExecutionCount int64 `json:"executionCount"`
	TaskCount      int64 `json:"taskCount"`
	StoryCount     int64 `json:"storyCount"`

	// Actions 是当前状态下可执行的动作，前端据此渲染按钮。
	Actions []TransitionResp `json:"actions,omitempty"`
}

// ProjectProductResp 描述项目关联的一个产品。
type ProjectProductResp struct {
	ProductID   uint   `json:"productId"`
	ProductName string `json:"productName,omitempty"`
	ProductCode string `json:"productCode,omitempty"`
}

// ProjectBrief 是项目简要信息，供下拉与筛选栏使用。
type ProjectBrief struct {
	ID   uint   `json:"id"`
	Name string `json:"name"`
	Code string `json:"code"`
}

// ---------------------------------------------------------------------------
// 迭代
// ---------------------------------------------------------------------------

// ExecutionReq 是新建/编辑迭代的请求。
type ExecutionReq struct {
	ProjectID uint `json:"projectId" validate:"required"`
	// Type 取 sprint / stage / kanban。
	Type   string `json:"type" validate:"omitempty,oneof=sprint stage kanban"`
	Status string `json:"status" validate:"omitempty,oneof=wait doing suspended closed"`

	Name string `json:"name" validate:"required,max=90"`
	Code string `json:"code" validate:"omitempty,max=45"`
	Desc string `json:"desc"`

	// Begin/End 是计划起止日期，格式 YYYY-MM-DD。
	Begin string `json:"begin" validate:"omitempty,datetime=2006-01-02"`
	End   string `json:"end" validate:"omitempty,datetime=2006-01-02"`

	PO uint `json:"po"`
	PM uint `json:"pm"`
	QD uint `json:"qd"`
	RD uint `json:"rd"`

	ACL       string `json:"acl" validate:"omitempty,oneof=open private"`
	Whitelist []uint `json:"whitelist"`

	// Team 是该迭代独立的团队（可选）。留空则只有项目团队可用，
	// 也就是说迭代团队是「项目团队的子集」这一约束由服务层保证。
	Team  []TeamMemberReq `json:"team"`
	Order int             `json:"order"`
}

// ExecutionFilterReq 是迭代列表的查询参数。
type ExecutionFilterReq struct {
	ProjectID uint   `form:"projectId"`
	Type      string `form:"type"`
	Status    string `form:"status"`
	Keywords  string `form:"keywords"`

	Page  int    `form:"page"`
	Size  int    `form:"size"`
	Order string `form:"order"`
}

// ExecutionResp 是迭代出参。
type ExecutionResp struct {
	ID   uint   `json:"id"`
	Code string `json:"code"`
	// Number 是面向用户的编号，形如 EXEC-5。
	Number string `json:"number"`

	ProjectID   uint   `json:"projectId"`
	ProjectName string `json:"projectName,omitempty"`

	Name        string `json:"name"`
	Type        string `json:"type"`
	TypeLabel   string `json:"typeLabel"`
	Status      string `json:"status"`
	StatusLabel string `json:"statusLabel"`
	Desc        string `json:"desc"`

	Begin     string  `json:"begin"`
	End       string  `json:"end"`
	RealBegan string  `json:"realBegan"`
	RealEnd   string  `json:"realEnd"`
	Days      int     `json:"days"`
	Progress  int     `json:"progress"`
	Estimate  float64 `json:"estimate"`
	Consumed  float64 `json:"consumed"`
	Left      float64 `json:"left"`

	PO uint `json:"po"`
	PM uint `json:"pm"`
	QD uint `json:"qd"`
	RD uint `json:"rd"`

	ACL       string `json:"acl"`
	Whitelist []uint `json:"whitelist"`

	OpenedBy   uint   `json:"openedBy"`
	OpenedDate string `json:"openedDate"`

	ClosedBy      uint   `json:"closedBy"`
	ClosedDate    string `json:"closedDate"`
	ClosedReason  string `json:"closedReason"`
	SuspendedDate string `json:"suspendedDate"`

	Order int `json:"order"`

	// Team 是迭代**自己**的团队，仅详情填充。
	//
	// 为空表示这个迭代沿用项目团队（而不是「没人可用」）。
	// 这里刻意不回落到项目团队的行：前端拿着这些行去增删改，
	// 一动就会改到项目团队上。
	Team []TeamMemberResp `json:"team,omitempty"`
	// MemberCount 是该迭代实际可用的人数：迭代团队为空时按项目团队算，
	// 与「任务能派给谁」的口径一致。
	MemberCount int `json:"memberCount"`

	// StoryCount/TaskCount 是统计字段，仅详情填充。
	StoryCount int64 `json:"storyCount"`
	TaskCount  int64 `json:"taskCount"`
	// Hours 是该迭代下任务的工时汇总（进度与燃尽图都用它）。
	TaskHours HoursResp `json:"taskHours"`

	Actions []TransitionResp `json:"actions,omitempty"`
}

// HoursResp 是工时三元组出参。
type HoursResp struct {
	Estimate float64 `json:"estimate"`
	Consumed float64 `json:"consumed"`
	Left     float64 `json:"left"`
}

// ---------------------------------------------------------------------------
// 团队成员
// ---------------------------------------------------------------------------

// TeamMemberReq 是加入/更新团队成员。
type TeamMemberReq struct {
	UserID uint `json:"userId" validate:"required"`
	// Role 取 PO/PM/QD/RD/dev/qa。
	Role     string  `json:"role" validate:"omitempty,oneof=PO PM QD RD dev qa"`
	Position string  `json:"position" validate:"omitempty,max=60"`
	Days     int     `json:"days" validate:"omitempty,gte=0"`
	Hours    float64 `json:"hours" validate:"omitempty,gte=0"`
	Estimate float64 `json:"estimate" validate:"omitempty,gte=0"`
	Order    int     `json:"order"`
}

// TeamMemberResp 是团队成员出参。
type TeamMemberResp struct {
	UserID   uint    `json:"userId"`
	Account  string  `json:"account,omitempty"`
	Realname string  `json:"realname,omitempty"`
	Role     string  `json:"role"`
	Position string  `json:"position"`
	Days     int     `json:"days"`
	Hours    float64 `json:"hours"`
	Estimate float64 `json:"estimate"`
	Consumed float64 `json:"consumed"`
	Left     float64 `json:"left"`
	Join     string  `json:"join"`
	Order    int     `json:"order"`
	// TaskCount 是该成员在本项目内的任务数，仅详情填充。
	TaskCount int64 `json:"taskCount"`
}

// ---------------------------------------------------------------------------
// 动作
// ---------------------------------------------------------------------------

// TransitionResp 描述一个可用的状态动作。
//
// 需求、项目、迭代、任务、缺陷共用这一个结构：动作集合都是
// 「动作名 + 中文名 + 所需权限 + 目标状态」，形状完全一致，
// 一个通用的 workflow.Action 视图就能表达，不必每个实体抄一份。
type TransitionResp struct {
	Action string `json:"action"`
	Label  string `json:"label"`
	Perm   string `json:"perm"`
	To     string `json:"to"`
	Kind   string `json:"kind"`
	Danger bool   `json:"danger"`
	// Enabled 为 false 表示当前用户没有该动作的权限 ——
	// 仍然下发，前端置灰并提示「需要 XX 权限」。
	Enabled bool `json:"enabled"`
}

// StateActionReq 是通用的「状态动作」请求。
//
// 项目的开始/挂起/关闭/激活、迭代的同类动作、任务的暂停/继续等，
// 入参都只有一个备注，因此共用一个 DTO。
type StateActionReq struct {
	Comment string `json:"comment" validate:"omitempty,max=255"`
}
