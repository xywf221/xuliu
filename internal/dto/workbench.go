package dto

// 通知与工作台的请求/响应。

// ---------------------------------------------------------------------------
// 通知
// ---------------------------------------------------------------------------

// NoticeFilterReq 是通知列表的查询参数。
//
// 没有 toUser 参数：通知列表永远只查「我自己」的，
// 让它可传等于开一个读别人提醒的口子。
type NoticeFilterReq struct {
	// Unread 传 1 只看未读、传 0 只看已读、不传全都看。
	Unread *bool `form:"unread"`

	ObjectType string `form:"objectType"`
	ObjectID   uint   `form:"objectId"`
	Keywords   string `form:"keywords"`

	Page  int    `form:"page"`
	Size  int    `form:"size"`
	Order string `form:"order"`
}

// NoticeResp 是通知出参。
type NoticeResp struct {
	ID uint `json:"id"`
	// FromUser 为 0 表示系统触发。
	FromUser   uint   `json:"fromUser"`
	FromName   string `json:"fromName,omitempty"`
	ObjectType string `json:"objectType"`
	ObjectID   uint   `json:"objectId"`
	// ObjectTitle 是被通知对象当时的标题，供列表直接显示。
	// 直接取不到（对象已删）时为空，此时前端只显示 Title。
	ObjectTitle string `json:"objectTitle,omitempty"`

	Title   string `json:"title"`
	Content string `json:"content"`
	// Link 是前端跳转路径，如 /bugs/12。
	Link string `json:"link"`

	Read      bool   `json:"read"`
	ReadAt    string `json:"readAt,omitempty"`
	CreatedAt string `json:"createdAt"`
}

// NoticeReadReq 是「标记已读」的请求。
//
// IDs 与 All 二选一：All 为 true 时忽略 IDs，表示「全部标记已读」。
type NoticeReadReq struct {
	IDs []uint `json:"ids"`
	All bool   `json:"all"`
}

// NoticeCountResp 是未读数的响应。
type NoticeCountResp struct {
	Unread int64 `json:"unread"`
}

// ---------------------------------------------------------------------------
// 工作台
// ---------------------------------------------------------------------------

// WorkbenchResp 是工作台首屏所需的全部数据。
//
// 做成一个聚合接口而不是让前端并发打五六个接口：工作台是登录后的
// 第一个页面，串行请求会让首屏变成「一块一块地长出来」；
// 而且这几块数据共用同一份可见性判定，分开算只是重复劳动。
type WorkbenchResp struct {
	Counts WorkbenchCounts `json:"counts"`

	// Tasks/Bugs 是「等我动手」的清单，按紧急程度排序。
	Tasks   []WorkbenchTaskResp   `json:"tasks"`
	Bugs    []WorkbenchBugResp    `json:"bugs"`
	Reviews []WorkbenchReviewResp `json:"reviews"`
	Notices []NoticeResp          `json:"notices"`

	// Efforts 是我自己的工时概览。
	Efforts WorkbenchEffortResp `json:"efforts"`
}

// WorkbenchCounts 是工作台各处的数量。
type WorkbenchCounts struct {
	// TaskDoing / TaskWait 是「派给我且未完成」的任务数。
	TaskDoing int `json:"taskDoing"`
	TaskWait  int `json:"taskWait"`
	// TaskOverdue 是其中已过截止日期还没完成的。
	TaskOverdue int `json:"taskOverdue"`
	// BugActive 是「派给我且未解决」的缺陷数。
	BugActive int `json:"bugActive"`
	// StoryReview 是等我评审的需求数。
	StoryReview int `json:"storyReview"`
	// NoticeUnread 是未读通知数。
	NoticeUnread int64 `json:"noticeUnread"`
}

// WorkbenchTaskResp 是工作台上的任务条目（只带列表要显示的字段）。
type WorkbenchTaskResp struct {
	ID          uint    `json:"id"`
	Name        string  `json:"name"`
	ProjectID   uint    `json:"projectId"`
	ProjectName string  `json:"projectName,omitempty"`
	ExecName    string  `json:"executionName,omitempty"`
	Status      string  `json:"status"`
	StatusLabel string  `json:"statusLabel"`
	Pri         int     `json:"pri"`
	Estimate    float64 `json:"estimate"`
	Consumed    float64 `json:"consumed"`
	Left        float64 `json:"left"`
	Progress    int     `json:"progress"`
	Deadline    string  `json:"deadline,omitempty"`
	// Overdue 由服务端算：客户端算「今天」会因为时区与本地时钟不一致而漂移。
	Overdue bool `json:"overdue"`
}

// WorkbenchBugResp 是工作台上的缺陷条目。
type WorkbenchBugResp struct {
	ID          uint   `json:"id"`
	Code        string `json:"code"`
	Title       string `json:"title"`
	ProductID   uint   `json:"productId"`
	ProductName string `json:"productName,omitempty"`
	Type        string `json:"type"`
	TypeLabel   string `json:"typeLabel"`
	Severity    int    `json:"severity"`
	Pri         int    `json:"pri"`
	Status      string `json:"status"`
	StatusLabel string `json:"statusLabel"`
	Deadline    string `json:"deadline,omitempty"`
	Overdue     bool   `json:"overdue"`
}

// WorkbenchReviewResp 是等我评审的需求条目。
type WorkbenchReviewResp struct {
	StoryID     uint   `json:"storyId"`
	Title       string `json:"title"`
	ProductID   uint   `json:"productId"`
	ProductName string `json:"productName,omitempty"`
	Version     int    `json:"version"`
	Pri         int    `json:"pri"`
	OpenedBy    uint   `json:"openedBy"`
	OpenedName  string `json:"openedByName,omitempty"`
	OpenedDate  string `json:"openedDate,omitempty"`
}

// WorkbenchEffortResp 是我的工时概览。
type WorkbenchEffortResp struct {
	// Today / Week 是今天与本周（周一为起点）的已消耗工时。
	Today float64 `json:"today"`
	Week  float64 `json:"week"`
	// Total 是有记录以来的累计工时。
	Total float64 `json:"total"`
}

// ---------------------------------------------------------------------------
// 燃尽图
// ---------------------------------------------------------------------------

// BurndownReq 是燃尽图的查询参数。
//
// ExecutionID 与 ProjectID 二选一：给了迭代就画迭代的曲线，否则画项目的。
// 迭代本身已经属于某个项目，所以不再要求同时给出两者（给了也只用来校验
// 迭代确实在该项目下）。
type BurndownReq struct {
	ProjectID   uint `form:"projectId"`
	ExecutionID uint `form:"executionId"`
	// Begin/End 显式指定曲线的起止日期，形如 2026-10-01。
	// 不传时用迭代/项目自身的起止日期。
	Begin string `form:"begin"`
	End   string `form:"end"`
}

// Scope 说明这条曲线画在哪一层：给了迭代就是迭代，否则是项目。
func (r BurndownReq) Scope() string {
	if r.ExecutionID > 0 {
		return "execution"
	}
	return "project"
}

// ScopeID 是曲线所属对象的 id，与 Scope 配套。
func (r BurndownReq) ScopeID() uint {
	if r.ExecutionID > 0 {
		return r.ExecutionID
	}
	return r.ProjectID
}

// BurndownResp 是燃尽图数据。
type BurndownResp struct {
	// Scope 取 execution / project，说明这条曲线画的是哪一层。
	Scope         string  `json:"scope"`
	ScopeID       uint    `json:"scopeId"`
	Begin         string  `json:"begin"`
	End           string  `json:"end"`
	TotalEstimate float64 `json:"totalEstimate"`
	TotalConsumed float64 `json:"totalConsumed"`
	// Days 从 Begin 到 End 逐日一点，缺数据的日期也会补 0，
	// 这样前端画折线时不必自己补齐时间轴。
	Days []BurndownDayResp `json:"days"`
	// TaskCount 是计入曲线的任务数。
	TaskCount int `json:"taskCount"`
}

// BurndownDayResp 是燃尽图上的一天。
type BurndownDayResp struct {
	Date string `json:"date"`
	// Remaining 是当天结束时的剩余工时。
	Remaining float64 `json:"remaining"`
	// Consumed 是当天消耗的工时。
	Consumed float64 `json:"consumed"`
	// Added 是当天新增任务带来的工时（范围变化）。
	Added float64 `json:"added"`
	// Ideal 是理想剩余：按总工时从开始到结束线性递减。
	Ideal float64 `json:"ideal"`
}
