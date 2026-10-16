package model

import "time"

// Bug 缺陷。
//
// 生命周期比需求简单，但有两点容易搞错，是我们刻意的约定：
//
//  1. 没有独立的 reopen 动作。「重新激活」走 activate，它把状态打回 active
//     并累加 ActivatedCount。所以「这个缺陷被反复激活过几次」可以直接读出来。
//  2. Resolution 只在 resolve 时写入，close 不再改它。因此一个已关闭的缺陷
//     仍保留着当初为什么被解决（是修复、设计如此还是无法重现）。
//
// OpenedBuild / ResolvedBuild 存的是版本号的文本而非外键：「影响版本」常是
// 自由填写的一串，且历史缺陷指向的构建可能早已被删，做成外键会在删构建时卡住。
type Bug struct {
	BaseModel

	ProjectID   uint `gorm:"index" json:"projectId"`
	ProductID   uint `gorm:"index;not null" json:"productId"`
	ExecutionID uint `gorm:"index" json:"executionId"`
	ModuleID    uint `gorm:"index;not null;default:0" json:"moduleId"`
	PlanID      uint `gorm:"index" json:"planId"`

	// 与需求/任务的关联。
	StoryID      uint `gorm:"index" json:"storyId"`
	StoryVersion int  `gorm:"not null;default:0" json:"storyVersion"`
	TaskID       uint `gorm:"index" json:"taskId"`
	// CaseID 指向测试用例（二阶段启用）。
	CaseID uint `gorm:"index" json:"caseId"`

	// 反向转换：本缺陷被转成了哪条需求/任务。
	ToStoryID uint `gorm:"index" json:"toStoryId"`
	ToTaskID  uint `gorm:"index" json:"toTaskId"`

	Title    string `gorm:"size:255;not null;index" json:"title"`
	Keywords string `gorm:"size:255" json:"keywords"`
	// Steps 是重现步骤，大文本。
	Steps string `gorm:"type:text" json:"steps"`

	// 分类属性。
	Type     string `gorm:"size:20;not null;default:codeerror;index" json:"type"`
	Severity int    `gorm:"not null;default:0;index" json:"severity"`
	Pri      int    `gorm:"not null;default:0;index" json:"pri"`

	// 环境信息。
	OS       string `gorm:"size:255" json:"os"`
	Browser  string `gorm:"size:255" json:"browser"`
	Hardware string `gorm:"size:60" json:"hardware"`

	// 状态机字段。
	Status string `gorm:"size:20;not null;default:active;index" json:"status"`
	// Resolution 仅在 resolve 时写入，取值见 BugResolution* 常量。
	Resolution string `gorm:"size:30;index" json:"resolution"`
	// Confirmed 取 BugUnconfirmed / BugConfirmed。
	Confirmed int `gorm:"not null;default:0;index" json:"confirmed"`

	// ActivatedCount 是被重新激活的次数，submitter 可直接看到反复程度。
	ActivatedCount int        `gorm:"not null;default:0" json:"activatedCount"`
	ActivatedDate  *time.Time `json:"activatedDate"`

	// OpenedBuild 是影响版本（可填多个），ResolvedBuild 是解决版本。
	OpenedBuild   string `gorm:"size:255" json:"openedBuild"`
	ResolvedBuild string `gorm:"size:255" json:"resolvedBuild"`

	// 审计字段。
	OpenedBy     uint       `gorm:"index;not null" json:"openedBy"`
	OpenedDate   *time.Time `json:"openedDate"`
	AssignedTo   uint       `gorm:"index" json:"assignedTo"`
	AssignedDate *time.Time `json:"assignedDate"`
	Deadline     *time.Time `json:"deadline"`

	ResolvedBy   uint       `gorm:"index" json:"resolvedBy"`
	ResolvedDate *time.Time `json:"resolvedDate"`

	ClosedBy   uint       `json:"closedBy"`
	ClosedDate *time.Time `json:"closedDate"`
	// ClosedReason 是关闭原因，与 Resolution 不同：前者说为什么关，
	// 后者说怎么解决的。
	ClosedReason string `gorm:"size:30" json:"closedReason"`

	// DuplicateBugID 当解决方式为「重复」时指向源缺陷。
	DuplicateBugID uint `gorm:"index" json:"duplicateBugId"`
	// RelatedBugIDs 是关联缺陷 id 列表（JSON 数组）。
	RelatedBugIDs string `gorm:"type:text" json:"relatedBugIds"`

	LastEditedBy   uint       `json:"lastEditedBy"`
	LastEditedDate *time.Time `json:"lastEditedDate"`

	Mailto string `gorm:"type:text" json:"mailto"`
	Order  int    `gorm:"not null;default:0" json:"order"`
}

func (Bug) TableName() string { return "pm_bug" }

// Release 发布。把一批需求与缺陷打包成一次对外交付。
//
// Stories/Bugs 存 id 列表快照（JSON）：发布是不可变的交付记录，
// 之后需求归属变了也不应改写历史发布内容。
type Release struct {
	BaseModel
	ProductID uint `gorm:"index;not null" json:"productId"`
	Branch    uint `gorm:"index;not null;default:0" json:"branch"`

	Name   string `gorm:"size:90;not null;index" json:"name"`
	Marker string `gorm:"size:45" json:"marker"` // 版本标记
	Status string `gorm:"size:20;not null;default:wait;index" json:"status"`
	Desc   string `gorm:"type:text" json:"desc"`

	Date      *time.Time `json:"date"`      // 发布日期
	BuildDate *time.Time `json:"buildDate"` // 构建日期

	// Stories/Bugs 是本次发布包含的需求/缺陷 id 列表（JSON 数组）。
	Stories string `gorm:"type:text" json:"stories"`
	Bugs    string `gorm:"type:text" json:"bugs"`

	OpenedBy   uint       `json:"openedBy"`
	OpenedDate *time.Time `json:"openedDate"`

	Order int `gorm:"not null;default:0" json:"order"`
}

func (Release) TableName() string { return "pm_release" }

// Build 构建版本。缺陷的「影响版本/解决版本」在 UI 上从这里取值。
type Build struct {
	BaseModel
	ProductID   uint `gorm:"index;not null" json:"productId"`
	Branch      uint `gorm:"index;not null;default:0" json:"branch"`
	ProjectID   uint `gorm:"index" json:"projectId"`
	ExecutionID uint `gorm:"index" json:"executionId"`

	Name string `gorm:"size:90;not null;index" json:"name"`
	Desc string `gorm:"type:text" json:"desc"`

	Date *time.Time `json:"date"`

	// Stories/Bugs 是本次构建包含的需求/缺陷 id 列表（JSON 数组）。
	Stories string `gorm:"type:text" json:"stories"`
	Bugs    string `gorm:"type:text" json:"bugs"`

	OpenedBy   uint       `json:"openedBy"`
	OpenedDate *time.Time `json:"openedDate"`

	Order int `gorm:"not null;default:0" json:"order"`
}

func (Build) TableName() string { return "pm_build" }
