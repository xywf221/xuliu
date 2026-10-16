package dto

// 任务域请求与响应。
//
// 任务与需求最大的区别：需求是版本化的、内容会变更；任务是可执行的
// 工作项，核心数据是**工时三元组**（预计/已消耗/剩余）与推进状态。
// 工时决定进度，而进度不单独存字段 —— 它随时可以按公式算出来，
// 存一份就多一个可能与工时不一致的地方。

// ---------------------------------------------------------------------------
// 任务
// ---------------------------------------------------------------------------

// TaskReq 是新建/编辑任务的请求。
type TaskReq struct {
	ProjectID   uint `json:"projectId" validate:"required"`
	ExecutionID uint `json:"executionId"`
	// ProductID 可选：不属于任何产品的任务（如内部事务）留空即可。
	ProductID uint `json:"productId"`
	ModuleID  uint `json:"moduleId"`
	StoryID   uint `json:"storyId"`

	Name     string `json:"name" validate:"required,max=255"`
	Keywords string `json:"keywords" validate:"omitempty,max=255"`
	Desc     string `json:"desc"`

	// Type 取 design/devel/request/test/study/discuss/ui/affair/misc。
	Type string `json:"type" validate:"omitempty,oneof=design devel request test study discuss ui affair misc"`
	// Mode 取 single/linear/multi，决定多人任务如何推进。
	Mode string `json:"mode" validate:"omitempty,oneof=single linear multi"`

	Pri      int      `json:"pri" validate:"omitempty,max=4"`
	Estimate float64  `json:"estimate" validate:"omitempty,gte=0"`
	Left     *float64 `json:"left" validate:"omitempty,gte=0"`
	// Deadline 是截止日期，格式 YYYY-MM-DD。
	Deadline string `json:"deadline" validate:"omitempty,datetime=2006-01-02"`
	// EstStarted 是计划开始日期。
	EstStarted string `json:"estStarted" validate:"omitempty,datetime=2006-01-02"`

	AssignedTo uint `json:"assignedTo"`
	// Team 是多人任务的成员分工（Mode 为 linear/multi 时使用）。
	Team []TaskTeamReq `json:"team"`

	Order int `json:"order"`
}

// TaskTeamReq 是任务成员分工。
type TaskTeamReq struct {
	UserID   uint    `json:"userId" validate:"required"`
	Estimate float64 `json:"estimate" validate:"omitempty,gte=0"`
	Left     float64 `json:"left" validate:"omitempty,gte=0"`
	Order    int     `json:"order"`
}

// TaskFilterReq 是任务列表的查询参数。
type TaskFilterReq struct {
	ProjectID   uint `form:"projectId"`
	ExecutionID uint `form:"executionId"`
	ProductID   uint `form:"productId"`
	StoryID     uint `form:"storyId"`
	// ModuleID 传 0 与不传都表示不过滤。
	ModuleID uint `form:"moduleId"`

	Status string `form:"status"`
	Type   string `form:"type"`
	Pri    *int   `form:"pri"`

	AssignedTo *uint `form:"assignedTo"`
	OpenedBy   uint  `form:"openedBy"`
	FinishedBy uint  `form:"finishedBy"`
	// TopOnly 只看顶层任务（不显示子任务）。
	TopOnly bool `form:"topOnly"`
	// Unfinished 只看未完成（未开始/进行中/暂停）的任务。
	Unfinished bool `form:"unfinished"`

	Keywords string `form:"keywords"`
	// DeadlineFrom/To 按截止日期过滤，格式 YYYY-MM-DD。
	DeadlineFrom string `form:"deadlineFrom"`
	DeadlineTo   string `form:"deadlineTo"`

	Page  int    `form:"page"`
	Size  int    `form:"size"`
	Order string `form:"order"`
}

// TaskResp 是任务出参。
type TaskResp struct {
	ID uint `json:"id"`
	// Number 是面向用户的编号，形如 TASK-8。
	Number string `json:"number"`

	ProjectID     uint   `json:"projectId"`
	ProjectName   string `json:"projectName,omitempty"`
	ExecutionID   uint   `json:"executionId"`
	ExecutionName string `json:"executionName,omitempty"`
	ProductID     uint   `json:"productId"`
	ModuleID      uint   `json:"moduleId"`
	ModuleName    string `json:"moduleName,omitempty"`

	ParentID uint `json:"parentId"`
	IsParent bool `json:"isParent"`

	StoryID      uint   `json:"storyId"`
	StoryTitle   string `json:"storyTitle,omitempty"`
	StoryVersion int    `json:"storyVersion"`
	// StoryChanged 表示关联需求的版本已经比任务建的时候新了，
	// 前端据此提示「需求已变更，请确认」。
	StoryChanged bool `json:"storyChanged"`
	FromBugID    uint `json:"fromBugId"`

	Name     string `json:"name"`
	Keywords string `json:"keywords"`
	Desc     string `json:"desc"`

	Type      string `json:"type"`
	TypeLabel string `json:"typeLabel"`
	Mode      string `json:"mode"`
	ModeLabel string `json:"modeLabel"`

	Pri      int     `json:"pri"`
	Estimate float64 `json:"estimate"`
	Consumed float64 `json:"consumed"`
	Left     float64 `json:"left"`
	// Progress 是按工时算出的进度百分比 0..100，不落库。
	Progress int    `json:"progress"`
	Deadline string `json:"deadline"`

	Status      string `json:"status"`
	StatusLabel string `json:"statusLabel"`
	// Version 是乐观锁版本号，编辑时必须原样带回。
	Version int `json:"version"`

	OpenedBy      uint   `json:"openedBy"`
	OpenedName    string `json:"openedByName,omitempty"`
	OpenedDate    string `json:"openedDate"`
	AssignedTo    uint   `json:"assignedTo"`
	AssignName    string `json:"assignedToName,omitempty"`
	AssignedDate  string `json:"assignedDate"`
	EstStarted    string `json:"estStarted"`
	RealStarted   string `json:"realStarted"`
	FinishedBy    uint   `json:"finishedBy"`
	FinishedName  string `json:"finishedByName,omitempty"`
	FinishedDate  string `json:"finishedDate"`
	CanceledBy    uint   `json:"canceledBy"`
	CanceledDate  string `json:"canceledDate"`
	ClosedBy      uint   `json:"closedBy"`
	ClosedDate    string `json:"closedDate"`
	ClosedReason  string `json:"closedReason"`
	ActivatedDate string `json:"activatedDate"`

	Order int `json:"order"`

	// Team 是多人任务的成员分工，仅详情填充。
	Team []TaskTeamResp `json:"team,omitempty"`
	// Efforts 是工时记录，仅详情填充。
	Efforts []EffortResp `json:"efforts,omitempty"`
	// Actions 是当前状态下可执行的动作。
	Actions []TransitionResp `json:"actions,omitempty"`

	CommentCount int64 `json:"commentCount"`
	FileCount    int64 `json:"fileCount"`
	// EffortCount 是工时记录条数。
	EffortCount int64 `json:"effortCount"`
}

// TaskTeamResp 是任务成员分工出参。
type TaskTeamResp struct {
	UserID      uint    `json:"userId"`
	Realname    string  `json:"realname,omitempty"`
	Estimate    float64 `json:"estimate"`
	Consumed    float64 `json:"consumed"`
	Left        float64 `json:"left"`
	Progress    int     `json:"progress"`
	Status      string  `json:"status"`
	StatusLabel string  `json:"statusLabel"`
	Transfer    bool    `json:"transfer"`
	Order       int     `json:"order"`
}

// ---------------------------------------------------------------------------
// 工时
// ---------------------------------------------------------------------------

// EffortReq 是登记工时的请求。
//
//	consumed  本次消耗的工时（必填，> 0）
//	left      记录**之后**的剩余工时；留空时按「原剩余 − 本次消耗」推算
//	date      报工日期，留空取今天
//
// 之所以允许 left 留空又保留显式填写的能力：绝大多数报工是
// 「我今天干了 3 小时，还剩 5 小时」，让用户只填一个数字最省事；
// 而一旦工作量重估（本来剩 8 小时，现在发现只剩 2 小时），
// 就必须能直接改写剩余值，否则只能靠凭空多报工时来「抹平」，
// 那会把工时数据污染掉。
type EffortReq struct {
	Consumed float64  `json:"consumed" validate:"required,gt=0"`
	Left     *float64 `json:"left" validate:"omitempty,gte=0"`
	Work     string   `json:"work" validate:"omitempty,max=1000"`
	// Date 是报工日期，格式 YYYY-MM-DD。
	Date string `json:"date" validate:"omitempty,datetime=2006-01-02"`
	// Begin/End 是当日工作时段，形如 0900 / 1800。
	Begin string `json:"begin" validate:"omitempty,len=4"`
	End   string `json:"end" validate:"omitempty,len=4"`
	// UserID 是给哪个成员报工；多人任务里留空表示「算在当前登录用户头上」。
	UserID  uint   `json:"userId"`
	Comment string `json:"comment" validate:"omitempty,max=255"`
}

// EffortFilterReq 是工时列表的查询参数。
type EffortFilterReq struct {
	ObjectType  string `form:"objectType"`
	ObjectID    uint   `form:"objectId"`
	ProjectID   uint   `form:"projectId"`
	ExecutionID uint   `form:"executionId"`
	UserID      uint   `form:"userId"`
	DateFrom    string `form:"dateFrom"`
	DateTo      string `form:"dateTo"`

	Page  int    `form:"page"`
	Size  int    `form:"size"`
	Order string `form:"order"`
}

// EffortResp 是工时记录出参。
type EffortResp struct {
	ID          uint    `json:"id"`
	ObjectType  string  `json:"objectType"`
	ObjectID    uint    `json:"objectId"`
	ProjectID   uint    `json:"projectId"`
	ExecutionID uint    `json:"executionId"`
	UserID      uint    `json:"userId"`
	Realname    string  `json:"realname,omitempty"`
	Work        string  `json:"work"`
	Date        string  `json:"date"`
	Consumed    float64 `json:"consumed"`
	Left        float64 `json:"left"`
	Begin       string  `json:"begin"`
	End         string  `json:"end"`
	CreatedAt   string  `json:"createdAt"`
}

// ---------------------------------------------------------------------------
// 动作请求
// ---------------------------------------------------------------------------

// TaskAssignReq 是指派任务。
type TaskAssignReq struct {
	AssignedTo uint   `json:"assignedTo" validate:"required"`
	Comment    string `json:"comment" validate:"omitempty,max=255"`
}

// TaskFinishReq 是完成任务。
type TaskFinishReq struct {
	// Consumed 可选：顺手把这次干完的工时一起报掉，省一次操作。
	Consumed float64 `json:"consumed" validate:"omitempty,gte=0"`
	Comment  string  `json:"comment" validate:"omitempty,max=255"`
}

// TaskCancelReq 是取消任务。
type TaskCancelReq struct {
	Comment string `json:"comment" validate:"omitempty,max=255"`
}

// TaskCloseReq 是关闭任务。
type TaskCloseReq struct {
	Comment string `json:"comment" validate:"omitempty,max=255"`
}

// ---------------------------------------------------------------------------
// 看板
// ---------------------------------------------------------------------------

// KanbanResp 是看板视图：按状态分列的任务。
//
// 后端直接给出「列」而不是让前端拿一堆任务自己分组：列的顺序、
// 列的中文名、每列的条数都属于展示规则，放在后端就与状态机的
// 定义同源，前端不必再维护一份状态顺序。
type KanbanResp struct {
	// Scope 说明看板的范围（project / execution）。
	Scope       string         `json:"scope"`
	ProjectID   uint           `json:"projectId"`
	ExecutionID uint           `json:"executionId"`
	Columns     []KanbanColumn `json:"columns"`
}

// KanbanColumn 是看板的一列。
type KanbanColumn struct {
	Status string     `json:"status"`
	Label  string     `json:"label"`
	Count  int64      `json:"count"`
	Hours  HoursResp  `json:"hours"`
	Tasks  []TaskResp `json:"tasks"`
}
