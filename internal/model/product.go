package model

import "time"

// 本文件是产品域实体：产品线、产品、模块树、产品计划。

// Productline 产品线。
//
// 禅道 22.x 把产品线塞在 zt_module 表里以 type='line' 区分，
// 属于复用树表存扁平列表的历史包袱。这里独立成表：语义清晰，
// 且与需求/缺陷的模块树彻底解耦（否则查模块时要处处记得排除 line）。
type Productline struct {
	BaseModel
	Name     string `gorm:"size:60;not null;index" json:"name"`
	ParentID uint   `gorm:"index;not null;default:0" json:"parentId"`
	Path     string `gorm:"size:255;index" json:"path"`
	Grade    int    `gorm:"not null;default:1" json:"grade"`
	Order    int    `gorm:"not null;default:0" json:"order"`

	// 负责人（用户 id）。
	PO uint `json:"po"`
	QD uint `json:"qd"`
	RD uint `json:"rd"`
}

func (Productline) TableName() string { return "pm_productline" }

// Product 产品。需求的归属主体。
type Product struct {
	BaseModel
	// ProductlineID 为 0 表示不属于任何产品线。
	ProductlineID uint `gorm:"index" json:"productlineId"`

	Name string `gorm:"size:110;not null;index" json:"name"`
	// Code 是产品代号，全局唯一，用于需求编号前缀等。
	Code string `gorm:"size:45;uniqueIndex;not null" json:"code"`

	Type   string `gorm:"size:20;not null;default:normal" json:"type"`
	Status string `gorm:"size:20;not null;default:normal;index" json:"status"`

	Desc string `gorm:"type:text" json:"desc"`

	// 三个负责人。用用户 id 而非禅道的 account 字符串。
	PO uint `gorm:"index" json:"po"` // 产品经理
	QD uint `json:"qd"`              // 测试负责人
	RD uint `json:"rd"`              // 发布负责人

	// ACL 取 open / private；private 时仅白名单与关联项目团队可见。
	ACL string `gorm:"size:20;not null;default:open" json:"acl"`
	// Whitelist 是白名单用户 id 列表（JSON 数组），仅 private 时生效。
	Whitelist string `gorm:"type:text" json:"whitelist"`
	// Reviewer 是需求默认评审人 id 列表（JSON 数组）。
	Reviewer string `gorm:"type:text" json:"reviewer"`

	// 需求评审开关：关闭时新建需求直接进入 active，不走评审流程。
	//
	// 注意这里**没有** gorm 的 default 标签，默认值由 service 层显式赋。
	// 原因：带 default 标签时 GORM 会跳过零值字段，让 DB 默认值生效 ——
	// 于是「显式把产品的需求评审关掉」会被静默写成 true，
	// 用户看到的开关与库里的值不一致。现在插入时必须显式给值，
	// 漏给的话会直接报 not null 而不是静默取默认值。
	ReviewStory bool `gorm:"not null" json:"reviewStory"`

	Order int `gorm:"not null;default:0" json:"order"`
}

func (Product) TableName() string { return "pm_product" }

// Module 模块树。产品下的分类树，需求/任务/缺陷各自使用独立的树。
//
// Root 是所属产品的 id；Type 区分这棵树服务于哪类对象。
// 同一个产品下会有多棵同 Root 但不同 Type 的树。
//
// Path 存祖先 id 逗号串（物化路径），Grade 是深度。
// 查「某模块下所有子孙」用 Path 前缀匹配，一次查询搞定且三库一致。
type Module struct {
	BaseModel
	Root   uint   `gorm:"index:idx_module_root_type,priority:1;not null" json:"root"`
	Type   string `gorm:"size:20;index:idx_module_root_type,priority:2;not null" json:"type"`
	Branch uint   `gorm:"index;not null;default:0" json:"branch"`

	Name     string `gorm:"size:60;not null" json:"name"`
	ParentID uint   `gorm:"index;not null;default:0" json:"parentId"`
	Path     string `gorm:"size:255;index" json:"path"`
	Grade    int    `gorm:"not null;default:1" json:"grade"`
	Order    int    `gorm:"not null;default:0" json:"order"`

	// Short 是简称，用于列表里节省横向空间。
	Short string `gorm:"size:60" json:"short"`
	// Owner 是模块负责人（用户 id）。
	Owner uint   `gorm:"index" json:"owner"`
	Desc  string `gorm:"type:text" json:"desc"`
}

func (Module) TableName() string { return "pm_module" }

// ProductPlan 产品计划。需求的发布计划归属。
type ProductPlan struct {
	BaseModel
	ProductID uint `gorm:"index;not null" json:"productId"`
	Branch    uint `gorm:"index;not null;default:0" json:"branch"`

	Title  string `gorm:"size:90;not null;index" json:"title"`
	Status string `gorm:"size:20;not null;default:wait;index" json:"status"`
	Desc   string `gorm:"type:text" json:"desc"`

	Begin *time.Time `json:"begin"`
	End   *time.Time `json:"end"`

	FinishedDate *time.Time `json:"finishedDate"`
	ClosedDate   *time.Time `json:"closedDate"`

	OpenedBy   uint       `json:"openedBy"`
	OpenedDate *time.Time `json:"openedDate"`

	Order int `gorm:"not null;default:0" json:"order"`
}

func (ProductPlan) TableName() string { return "pm_productplan" }

// Story 需求。
//
// 需求是版本化的：主表只存当前内容（Title 与 zt_story 一致地放在主表，
// 便于列表查询与排序），历史快照进 StorySpec。
// 变更时 Version+1 并新增一条 StorySpec；评审时按 Version 关联 StoryReview。
type Story struct {
	BaseModel

	// ParentID 支持需求拆分；IsParent 标记有子需求。
	ParentID uint `gorm:"index;not null;default:0" json:"parentId"`
	IsParent bool `gorm:"not null;default:false" json:"isParent"`

	ProductID uint `gorm:"index;not null" json:"productId"`
	Branch    uint `gorm:"index;not null;default:0" json:"branch"`
	ModuleID  uint `gorm:"index;not null;default:0" json:"moduleId"`
	// PlanID 是所属产品计划。
	PlanID uint `gorm:"index" json:"planId"`

	Title    string `gorm:"size:255;not null;index" json:"title"`
	Keywords string `gorm:"size:255" json:"keywords"`

	Type     string `gorm:"size:20;not null;default:story;index" json:"type"`
	Category string `gorm:"size:20;not null;default:feature" json:"category"`
	Source   string `gorm:"size:20;not null;default:po" json:"source"`
	// SourceNote 是当来源为「其他」时的补充说明。
	SourceNote string `gorm:"size:255" json:"sourceNote"`
	// FromBugID 记录由哪个缺陷转入。
	FromBugID uint `gorm:"index" json:"fromBugId"`
	// FromStoryID 记录由哪条需求拆分而来。
	FromStoryID uint `gorm:"index" json:"fromStoryId"`

	Pri      int      `gorm:"not null;default:3;index" json:"pri"`
	Estimate *float64 `gorm:"type:decimal(10,2)" json:"estimate"`

	Status string `gorm:"size:20;not null;default:draft;index" json:"status"`
	Stage  string `gorm:"size:20;not null;default:wait;index" json:"stage"`
	// StagedBy 记录进入当前阶段的经办人。
	StagedBy uint `json:"stagedBy"`

	// Version 当前版本号，从 1 开始。任何内容变更都 +1。
	Version int `gorm:"not null;default:1" json:"version"`
	// StoryChanged 标记「已变更但未被下游确认」。
	StoryChanged bool `gorm:"not null;default:false" json:"storyChanged"`

	// 审计字段。
	OpenedBy     uint       `gorm:"index;not null" json:"openedBy"`
	OpenedDate   *time.Time `json:"openedDate"`
	AssignedTo   uint       `gorm:"index" json:"assignedTo"`
	AssignedDate *time.Time `json:"assignedDate"`
	// ChangedBy 记录变更发起人，用于 recall 时判断该回到 draft 还是 changing。
	ChangedBy   uint       `json:"changedBy"`
	ChangedDate *time.Time `json:"changedDate"`
	// ReviewedBy 是评审人 id 列表（JSON 数组）。
	ReviewedBy   string     `gorm:"type:text" json:"reviewedBy"`
	ReviewedDate *time.Time `json:"reviewedDate"`
	// ClosedBy/ClosedDate/ClosedReason 是关闭信息，
	// 内嵌 Actor 里的这三个字段在此单独声明以便加索引。
	ClosedBy     uint       `gorm:"index" json:"closedBy"`
	ClosedDate   *time.Time `json:"closedDate"`
	ClosedReason string     `gorm:"size:30;index" json:"closedReason"`

	ActivatedDate *time.Time `json:"activatedDate"`
	VerifiedDate  *time.Time `json:"verifiedDate"`
	ReleasedDate  *time.Time `json:"releasedDate"`

	// DuplicateStoryID 当关闭原因为「重复」时指向目标需求。
	DuplicateStoryID uint `gorm:"index" json:"duplicateStoryId"`
	// LinkStories 是关联需求 id 列表（JSON 数组）。
	LinkStories string `gorm:"type:text" json:"linkStories"`

	// LastEditedBy 记录最后一次修改人。
	LastEditedBy   uint       `json:"lastEditedBy"`
	LastEditedDate *time.Time `json:"lastEditedDate"`

	Order int `gorm:"not null;default:0" json:"order"`
}

func (Story) TableName() string { return "pm_story" }

// StorySpec 需求内容的版本快照。
//
// Spec（需求描述）与 Verify（验收标准）放在这里而不是主表，因为它们是
// 大文本且需要按版本保留历史。主表只保留可变的小字段，列表查询不用拖着大文本。
//
// 变更流程：Version+1 后插入一条新 Spec；revert 时删除最新一条并 Version-1。
type StorySpec struct {
	ID      uint `gorm:"primaryKey" json:"id"`
	StoryID uint `gorm:"index;not null;uniqueIndex:uk_spec_story_version" json:"storyId"`
	// Version 与 Story.Version 对应。
	Version int `gorm:"not null;uniqueIndex:uk_spec_story_version" json:"version"`
	// Title 冗余一份，便于查历史版本标题。
	Title string `gorm:"size:255;not null" json:"title"`

	Spec   string `gorm:"type:text" json:"spec"`   // 需求描述
	Verify string `gorm:"type:text" json:"verify"` // 验收标准
	// Files 是附加在说明里的附件 id 列表（JSON 数组）。
	Files string `gorm:"type:text" json:"files"`

	CreatedAt time.Time `json:"createdAt"`
}

func (StorySpec) TableName() string { return "pm_storyspec" }

// StoryReview 需求评审记录。每位评审人一行。
type StoryReview struct {
	ID       uint `gorm:"primaryKey" json:"id"`
	StoryID  uint `gorm:"index;not null;uniqueIndex:uk_review_story_version_reviewer,priority:1" json:"storyId"`
	Version  int  `gorm:"not null;uniqueIndex:uk_review_story_version_reviewer,priority:2" json:"version"`
	Reviewer uint `gorm:"not null;uniqueIndex:uk_review_story_version_reviewer,priority:3" json:"reviewer"`

	// Result 取 pass / revert / clarify / reject；空表示尚未评审。
	Result     string     `gorm:"size:20" json:"result"`
	ReviewDate *time.Time `json:"reviewDate"`

	CreatedAt time.Time `json:"createdAt"`
}

func (StoryReview) TableName() string { return "pm_storyreview" }

// ProjectStory 需求与项目/迭代的关联（多对多）。
//
// ProjectID 与 ExecutionID 是两列而不是一个「可指向项目或迭代」的 id：
// 我们的项目与迭代本来就是两张表，用两列表达粒度不必靠 id 空间去猜，
// 外键也才有意义（排进项目时 ExecutionID 为 0，排进迭代时两列都有值）。
//
// Version 记录关联时需求所处的版本。需求后续变更后，通过比较它与
// Story.Version 即可知道「这个需求在本项目里的内容已经旧了」。
type ProjectStory struct {
	ID          uint `gorm:"primaryKey" json:"id"`
	ProjectID   uint `gorm:"index;not null;uniqueIndex:uk_project_story,priority:1" json:"projectId"`
	ExecutionID uint `gorm:"index;not null;default:0" json:"executionId"`
	ProductID   uint `gorm:"index;not null" json:"productId"`
	StoryID     uint `gorm:"index;not null;uniqueIndex:uk_project_story,priority:2" json:"storyId"`

	// Version 是关联时的需求版本。
	Version int `gorm:"not null;default:1" json:"version"`
	Order   int `gorm:"not null;default:0" json:"order"`

	CreatedAt time.Time `json:"createdAt"`
}

func (ProjectStory) TableName() string { return "pm_projectstory" }

// ProjectProduct 项目与产品的关联（多对多）。
type ProjectProduct struct {
	ID        uint `gorm:"primaryKey" json:"id"`
	ProjectID uint `gorm:"index;not null;uniqueIndex:uk_project_product,priority:1" json:"projectId"`
	ProductID uint `gorm:"index;not null;uniqueIndex:uk_project_product,priority:2" json:"productId"`
	Branch    uint `gorm:"not null;default:0;uniqueIndex:uk_project_product,priority:3" json:"branch"`
	// PlanID 是本项目关注的产品计划，0 表示不限。
	PlanID uint `gorm:"index" json:"planId"`

	CreatedAt time.Time `json:"createdAt"`
}

func (ProjectProduct) TableName() string { return "pm_projectproduct" }
