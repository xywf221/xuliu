package model

import "time"

// 本文件是项目域实体：项目、迭代、团队、任务、工时。
//
// 与禅道最大的结构差异：禅道把项目与迭代塞进同一张 zt_project 表，
// 靠 project/parent 列区分层级。在 Go 里那意味着每处都要做类型判断，
// 所以这里拆成 pm_project 与 pm_execution 两张表。

// Project 项目。
type Project struct {
	BaseModel

	Name string `gorm:"size:90;not null;index" json:"name"`
	Code string `gorm:"size:45;uniqueIndex" json:"code"`

	Model  string `gorm:"size:20;not null;default:scrum" json:"model"`
	Status string `gorm:"size:20;not null;default:wait;index" json:"status"`

	Begin *time.Time `json:"begin"`
	End   *time.Time `json:"end"`
	// RealBegan/RealEnd 是实际的起止时间，与计划值分开。
	RealBegan *time.Time `json:"realBegan"`
	RealEnd   *time.Time `json:"realEnd"`
	// Days 是总天数（冗余存储，避免每次按日期算）。
	Days int `gorm:"not null;default:0" json:"days"`

	// Progress 是项目进度百分比 0..100。
	Progress int     `gorm:"not null;default:0" json:"progress"`
	Estimate float64 `gorm:"type:decimal(10,2);not null;default:0" json:"estimate"`
	Consumed float64 `gorm:"type:decimal(10,2);not null;default:0" json:"consumed"`
	Left     float64 `gorm:"type:decimal(10,2);not null;default:0" json:"left"`

	Desc string `gorm:"type:text" json:"desc"`

	// 负责人。PO/PM/QD/RD 对应产品经理/项目经理/测试负责人/发布负责人。
	PO uint `gorm:"index" json:"po"`
	PM uint `gorm:"index" json:"pm"`
	QD uint `json:"qd"`
	RD uint `json:"rd"`

	// ACL 取 open / private；private 时仅团队成员与白名单可见。
	ACL       string `gorm:"size:20;not null;default:open" json:"acl"`
	Whitelist string `gorm:"type:text" json:"whitelist"`

	// Multiple 为 true 表示本项目下可有多个迭代（Scrum），否则只有一个。
	//
	// 同 Product.ReviewStory，没有 default 标签：带标签时 GORM 会跳过
	// 零值，「显式关掉多迭代」会被静默变回 true。
	Multiple bool `gorm:"not null" json:"multiple"`

	OpenedBy   uint       `gorm:"index;not null" json:"openedBy"`
	OpenedDate *time.Time `json:"openedDate"`

	ClosedBy     uint       `json:"closedBy"`
	ClosedDate   *time.Time `json:"closedDate"`
	ClosedReason string     `gorm:"size:30" json:"closedReason"`
	// SuspendedDate 记录挂起时间。
	SuspendedDate *time.Time `json:"suspendedDate"`

	LastEditedBy   uint       `json:"lastEditedBy"`
	LastEditedDate *time.Time `json:"lastEditedDate"`

	Order int `gorm:"not null;default:0" json:"order"`
}

func (Project) TableName() string { return "pm_project" }

// Execution 迭代/阶段/看板。
//
// 独立于项目：归属关系靠 ProjectID 表达，不再有「自己指自己」的父子列。
// 若将来需要「迭代下再分子阶段」，加 ParentID 即可，不影响现有语义。
type Execution struct {
	BaseModel

	ProjectID uint `gorm:"index;not null" json:"projectId"`
	// ParentID 支持迭代下的子阶段，0 表示直接挂在项目下。
	ParentID uint   `gorm:"index;not null;default:0" json:"parentId"`
	Path     string `gorm:"size:255" json:"path"`
	Grade    int    `gorm:"not null;default:1" json:"grade"`

	Name string `gorm:"size:90;not null;index" json:"name"`
	Code string `gorm:"size:45" json:"code"`

	// Type 取 sprint / stage / kanban。
	Type   string `gorm:"size:20;not null;default:sprint;index" json:"type"`
	Status string `gorm:"size:20;not null;default:wait;index" json:"status"`

	Begin *time.Time `json:"begin"`
	End   *time.Time `json:"end"`
	// RealBegan/RealEnd 是实际起止。
	RealBegan *time.Time `json:"realBegan"`
	RealEnd   *time.Time `json:"realEnd"`
	Days      int        `gorm:"not null;default:0" json:"days"`

	Progress int     `gorm:"not null;default:0" json:"progress"`
	Estimate float64 `gorm:"type:decimal(10,2);not null;default:0" json:"estimate"`
	Consumed float64 `gorm:"type:decimal(10,2);not null;default:0" json:"consumed"`
	Left     float64 `gorm:"type:decimal(10,2);not null;default:0" json:"left"`

	Desc string `gorm:"type:text" json:"desc"`

	// ACL 取 open / private，语义同项目。
	ACL       string `gorm:"size:20;not null;default:open" json:"acl"`
	Whitelist string `gorm:"type:text" json:"whitelist"`

	// 负责人。
	PO uint `gorm:"index" json:"po"`
	PM uint `gorm:"index" json:"pm"`
	QD uint `json:"qd"`
	RD uint `json:"rd"`

	OpenedBy   uint       `gorm:"index;not null" json:"openedBy"`
	OpenedDate *time.Time `json:"openedDate"`

	ClosedBy      uint       `json:"closedBy"`
	ClosedDate    *time.Time `json:"closedDate"`
	ClosedReason  string     `gorm:"size:30" json:"closedReason"`
	SuspendedDate *time.Time `json:"suspendedDate"`

	LastEditedBy   uint       `json:"lastEditedBy"`
	LastEditedDate *time.Time `json:"lastEditedDate"`

	Order int `gorm:"not null;default:0" json:"order"`
}

func (Execution) TableName() string { return "pm_execution" }

// Team 项目/迭代团队成员。
//
// RootID + RootType 表达归属：RootType 为 project 时 RootID 是项目 id，
// 为 execution 时是迭代 id。用一张表承载两种团队，因为成员结构完全一致，
// 拆两张表只会复制粘贴。
type Team struct {
	BaseModel
	RootID   uint   `gorm:"index:idx_team_root,priority:1;not null;uniqueIndex:uk_team,priority:1" json:"rootId"`
	RootType string `gorm:"size:20;index:idx_team_root,priority:2;not null;uniqueIndex:uk_team,priority:2" json:"rootType"`
	UserID   uint   `gorm:"index;not null;uniqueIndex:uk_team,priority:3" json:"userId"`

	// Role 取 PO/PM/QD/RD/dev/qa。
	Role string `gorm:"size:20" json:"role"`
	// Position 是岗位描述（自由文本）。
	Position string `gorm:"size:60" json:"position"`
	// Days 是本项目内的投入天数，Hours 是每日工时。
	Days  int     `gorm:"not null;default:0" json:"days"`
	Hours float64 `gorm:"type:decimal(10,2);not null;default:0" json:"hours"`

	// 成员在本项目内的工时汇总。
	Estimate float64 `gorm:"type:decimal(10,2);not null;default:0" json:"estimate"`
	Consumed float64 `gorm:"type:decimal(10,2);not null;default:0" json:"consumed"`
	Left     float64 `gorm:"type:decimal(10,2);not null;default:0" json:"left"`

	Join  *time.Time `json:"join"`
	Order int        `gorm:"not null;default:0" json:"order"`
}

func (Team) TableName() string { return "pm_team" }

// 团队归属类型。
const (
	TeamRootProject   = "project"
	TeamRootExecution = "execution"
)

// Task 任务。
//
// 工时三兄弟是任务的核心：Estimate 预计、Consumed 已消耗、Left 剩余。
// 每次记工时都会更新 Consumed 与 Left，并写一条 Effort。
// 进度由这三者算出（见 service 层的 progress 公式），不单独存字段。
type Task struct {
	BaseModel

	ProjectID   uint `gorm:"index;not null" json:"projectId"`
	ExecutionID uint `gorm:"index;not null;default:0" json:"executionId"`
	ProductID   uint `gorm:"index" json:"productId"`
	ModuleID    uint `gorm:"index" json:"moduleId"`

	// ParentID 支持子任务；IsParent 便于列表快速判断有无子任务。
	ParentID uint   `gorm:"index;not null;default:0" json:"parentId"`
	IsParent bool   `gorm:"not null;default:false" json:"isParent"`
	Path     string `gorm:"size:255" json:"path"`
	Grade    int    `gorm:"not null;default:1" json:"grade"`

	// StoryID 关联需求；StoryVersion 是建立关联时的需求版本。
	// 二者不一致说明需求已变更，前端应提示确认。
	StoryID      uint `gorm:"index" json:"storyId"`
	StoryVersion int  `gorm:"not null;default:0" json:"storyVersion"`
	// FromBugID 记录由哪个缺陷转入。
	FromBugID uint `gorm:"index" json:"fromBugId"`

	Name     string `gorm:"size:255;not null;index" json:"name"`
	Keywords string `gorm:"size:255" json:"keywords"`
	Desc     string `gorm:"type:text" json:"desc"`

	Type string `gorm:"size:20;not null;default:devel;index" json:"type"`
	// Mode 取 single / linear / multi，决定多人任务如何推进。
	Mode string `gorm:"size:20;not null;default:single" json:"mode"`

	Pri      int        `gorm:"not null;default:0;index" json:"pri"`
	Estimate float64    `gorm:"type:decimal(10,2);not null;default:0" json:"estimate"`
	Consumed float64    `gorm:"type:decimal(10,2);not null;default:0" json:"consumed"`
	Left     float64    `gorm:"type:decimal(10,2);not null;default:0" json:"left"`
	Deadline *time.Time `json:"deadline"`

	Status string `gorm:"size:20;not null;default:wait;index" json:"status"`
	// Version 用于乐观锁，每次编辑 +1。
	Version int `gorm:"not null;default:1" json:"version"`

	// 审计字段。
	OpenedBy      uint       `gorm:"index;not null" json:"openedBy"`
	OpenedDate    *time.Time `json:"openedDate"`
	AssignedTo    uint       `gorm:"index" json:"assignedTo"`
	AssignedDate  *time.Time `json:"assignedDate"`
	EstStarted    *time.Time `json:"estStarted"`
	RealStarted   *time.Time `json:"realStarted"`
	FinishedBy    uint       `json:"finishedBy"`
	FinishedDate  *time.Time `json:"finishedDate"`
	CanceledBy    uint       `json:"canceledBy"`
	CanceledDate  *time.Time `json:"canceledDate"`
	ClosedBy      uint       `json:"closedBy"`
	ClosedDate    *time.Time `json:"closedDate"`
	ClosedReason  string     `gorm:"size:30" json:"closedReason"`
	ActivatedDate *time.Time `json:"activatedDate"`

	LastEditedBy   uint       `json:"lastEditedBy"`
	LastEditedDate *time.Time `json:"lastEditedDate"`

	Mailto string `gorm:"type:text" json:"mailto"`
	Order  int    `gorm:"not null;default:0" json:"order"`
}

func (Task) TableName() string { return "pm_task" }

// TaskTeam 多人任务的成员分工。
//
// 每个成员有自己独立的预计/消耗/剩余与状态：串行模式下按顺序推进，
// 并行模式下同时推进。任务主表上的工时是这些成员的汇总值。
type TaskTeam struct {
	ID     uint `gorm:"primaryKey" json:"id"`
	TaskID uint `gorm:"index;not null;uniqueIndex:uk_taskteam,priority:1" json:"taskId"`
	UserID uint `gorm:"index;not null;uniqueIndex:uk_taskteam,priority:2" json:"userId"`

	Estimate float64 `gorm:"type:decimal(10,2);not null;default:0" json:"estimate"`
	Consumed float64 `gorm:"type:decimal(10,2);not null;default:0" json:"consumed"`
	Left     float64 `gorm:"type:decimal(10,2);not null;default:0" json:"left"`

	// Status 是该成员自己的推进状态。
	Status string `gorm:"size:20;not null;default:wait" json:"status"`
	// Transfer 标记该成员是否已把任务转交他人。
	Transfer bool `gorm:"not null;default:false" json:"transfer"`
	// StoryVersion 记录该成员开始时的需求版本。
	StoryVersion int `gorm:"not null;default:0" json:"storyVersion"`

	Order     int       `gorm:"not null;default:0" json:"order"`
	CreatedAt time.Time `json:"createdAt"`
}

func (TaskTeam) TableName() string { return "pm_taskteam" }

// Effort 工时记录。对象可以是任务、需求或缺陷。
//
// Left 的语义很关键：每条工时记录都带上「记完之后剩余多少」，
// 该对象最终的 Left 等于最后一条 Effort 的 Left。这样工时历史可完整回放，
// 也能算出任意时点的剩余工作量。
type Effort struct {
	BaseModel
	// ObjectType 取 task / story / bug。
	ObjectType string `gorm:"size:20;not null;index:idx_effort_object,priority:1" json:"objectType"`
	ObjectID   uint   `gorm:"not null;index:idx_effort_object,priority:2" json:"objectID"`

	ProductID   uint `gorm:"index" json:"productId"`
	ProjectID   uint `gorm:"index;not null" json:"projectId"`
	ExecutionID uint `gorm:"index" json:"executionId"`
	// TaskTeamID 记录这条工时属于哪个成员分工（多人任务用）。
	TaskTeamID uint `gorm:"index" json:"taskTeamId"`

	// Account 记录报工者。
	UserID uint `gorm:"index;not null" json:"userId"`

	// Work 是本次工作内容说明。
	Work string `gorm:"type:text" json:"work"`
	// Date 是报工日期（业务日期，不是记录创建时间）。
	Date time.Time `gorm:"index;not null" json:"date"`

	// Consumed 本次消耗工时；Left 记完后的剩余工时。
	Consumed float64 `gorm:"type:decimal(10,2);not null;default:0" json:"consumed"`
	Left     float64 `gorm:"type:decimal(10,2);not null;default:0" json:"left"`
	// Begin/End 是当日工作时段，形如 "0900"/"1800"。
	Begin string `gorm:"size:4" json:"begin"`
	End   string `gorm:"size:4" json:"end"`

	Order int `gorm:"not null;default:0" json:"order"`
}

func (Effort) TableName() string { return "pm_effort" }

// TaskRelation 任务依赖关系。
//
// Condition 描述前置条件，如「前置任务完成」；Action 描述满足条件后
// 对后置任务做什么（如自动开始）。禅道的 zt_relationoftasks 即此义。
type TaskRelation struct {
	ID        uint `gorm:"primaryKey" json:"id"`
	TaskID    uint `gorm:"index;not null" json:"taskId"`
	PreTaskID uint `gorm:"index;not null" json:"preTaskId"`
	// Condition 前置条件；Action 满足后动作。
	Condition string    `gorm:"size:30;not null;default:done" json:"condition"`
	Action    string    `gorm:"size:30;not null;default:start" json:"action"`
	CreatedAt time.Time `json:"createdAt"`
}

func (TaskRelation) TableName() string { return "pm_taskrelation" }
