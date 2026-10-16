package dto

// 需求相关的请求与响应。
//
// 需求是版本化实体：主表存当前内容，历史快照进 pm_storyspec。
// 出参里 Version 是关键字段 —— 前端拿它做乐观锁，提交变更时必须带回，
// 否则两个人同时编辑会互相覆盖。

// CreateStoryReq 是新建需求的请求。
type CreateStoryReq struct {
	ProductID uint `json:"productId" validate:"required"`
	ModuleID  uint `json:"moduleId"`
	PlanID    uint `json:"planId"`

	Title    string `json:"title" validate:"required,max=255"`
	Spec     string `json:"spec"`
	Verify   string `json:"verify"`
	Keywords string `json:"keywords" validate:"omitempty,max=255"`

	Type       string `json:"type" validate:"omitempty,oneof=story requirement epic"`
	Category   string `json:"category" validate:"omitempty,oneof=feature interface performance safe experience improve other"`
	Source     string `json:"source" validate:"omitempty,oneof=customer user po market service operation support competitor partner dev tester bug forum other"`
	SourceNote string `json:"sourceNote" validate:"omitempty,max=255"`

	Pri uint8 `json:"pri" validate:"omitempty,max=4"`
	// Estimate 是预计工时，nil 表示未估。
	Estimate *float64 `json:"estimate" validate:"omitempty,gte=0"`

	// ParentID 用于拆分子需求。
	ParentID uint `json:"parentId"`

	// Reviewers 是评审人；留空则用产品的默认评审人。
	Reviewers []uint `json:"reviewers"`
	// AssignedTo 是初始指派。
	AssignedTo uint `json:"assignedTo"`
	// Files 是随需求一起提交的附件 id（需先调用上传接口）。
	Files   []uint `json:"files"`
	Comment string `json:"comment"`
}

// UpdateStoryReq 是编辑需求的请求。
//
// 内容字段用指针：区分「没传该字段」（不改）与「传了空值」（清空）。
// Version 是必填的乐观锁版本号。
type UpdateStoryReq struct {
	Version int `json:"version" validate:"required"`

	Title    *string `json:"title" validate:"omitempty,max=255"`
	Spec     *string `json:"spec"`
	Verify   *string `json:"verify"`
	Keywords *string `json:"keywords" validate:"omitempty,max=255"`

	ModuleID   *uint    `json:"moduleId"`
	PlanID     *uint    `json:"planId"`
	Type       *string  `json:"type" validate:"omitempty,oneof=story requirement epic"`
	Category   *string  `json:"category" validate:"omitempty,oneof=feature interface performance safe experience improve other"`
	Source     *string  `json:"source" validate:"omitempty,oneof=customer user po market service operation support competitor partner dev tester bug forum other"`
	Pri        *uint8   `json:"pri" validate:"omitempty,max=4"`
	Estimate   *float64 `json:"estimate" validate:"omitempty,gte=0"`
	AssignedTo *uint    `json:"assignedTo"`
	Stage      *string  `json:"stage"`

	Comment string `json:"comment"`
	Files   []uint `json:"files"`
}

// StoryFilterReq 是需求列表的查询参数。
type StoryFilterReq struct {
	ProductID uint `form:"productId"`
	PlanID    uint `form:"planId"`
	// ModuleID 传 0 与不传都表示不过滤；要看「未分类」请用 Unclassified。
	ModuleID uint `form:"moduleId"`
	// Unclassified 为 true 时只看未归入任何模块的需求。
	Unclassified     bool `form:"unclassified"`
	IncludeSubModule bool `form:"includeSubModule"`

	Status   string `form:"status"`
	Stage    string `form:"stage"`
	Type     string `form:"type"`
	Category string `form:"category"`
	Source   string `form:"source"`
	Pri      *int   `form:"pri"`

	AssignedTo *uint `form:"assignedTo"`
	OpenedBy   uint  `form:"openedBy"`
	// TopOnly 为 true 时只看顶层需求（不显示子需求）。
	TopOnly bool `form:"topOnly"`
	// ChangedOnly 只看「已变更但未被下游确认」的需求。
	ChangedOnly bool `form:"changedOnly"`

	Keywords string `form:"keywords"`

	Page  int    `form:"page"`
	Size  int    `form:"size"`
	Order string `form:"order"`
}

// StoryResp 是需求列表出参。
type StoryResp struct {
	ID uint `json:"id"`
	// Code 是稳定编号，形如 STORY-12。
	Code string `json:"code"`

	ProductID   uint   `json:"productId"`
	ProductName string `json:"productName,omitempty"`
	ModuleID    uint   `json:"moduleId"`
	ModuleName  string `json:"moduleName,omitempty"`
	PlanID      uint   `json:"planId"`
	PlanTitle   string `json:"planTitle,omitempty"`
	ParentID    uint   `json:"parentId"`
	IsParent    bool   `json:"isParent"`

	Title    string `json:"title"`
	Keywords string `json:"keywords"`

	Type       string `json:"type"`
	Category   string `json:"category"`
	Source     string `json:"source"`
	SourceNote string `json:"sourceNote"`

	// Pri 用 int 而不是 uint8：模型里就是 int，跨层转换只会多出
	// 一处需要留意的窄化，没有收益。取值 0..4，0 表示未设置。
	Pri      int      `json:"pri"`
	Estimate *float64 `json:"estimate"`

	Status string `json:"status"`
	// StatusLabel 是状态中文名，前端直接显示，避免前后端各存一份字典。
	StatusLabel string `json:"statusLabel"`
	Stage       string `json:"stage"`
	StageLabel  string `json:"stageLabel"`

	Version      int  `json:"version"`
	StoryChanged bool `json:"storyChanged"`

	OpenedBy     uint   `json:"openedBy"`
	OpenedByName string `json:"openedByName,omitempty"`
	OpenedDate   string `json:"openedDate"`
	AssignedTo   uint   `json:"assignedTo"`
	AssignName   string `json:"assignedToName,omitempty"`
	AssignedDate string `json:"assignedDate"`
	ChangedBy    uint   `json:"changedBy"`
	ChangedDate  string `json:"changedDate"`
	ClosedBy     uint   `json:"closedBy"`
	ClosedDate   string `json:"closedDate"`
	ClosedReason string `json:"closedReason"`

	DuplicateStoryID uint `json:"duplicateStoryId"`
	FromBugID        uint `json:"fromBugId"`
	FromStoryID      uint `json:"fromStoryId"`

	// 关联信息，仅详情填充。
	LinkStories []uint `json:"linkStories,omitempty"`
	// Projects 是需求被排入的项目，仅详情填充。
	Projects []StoryLinkResp `json:"projects,omitempty"`

	// Actions 是当前状态下可执行的动作，前端据此渲染按钮。
	//
	// 由后端下发而不是前端自己判断：前后端各写一套状态规则必然漂移
	// （前端显示能点、后端却拒绝），这套数据与后端判定同源。
	Actions []TransitionResp `json:"actions,omitempty"`

	// CommentCount 是评论数；列表接口批量填充。
	CommentCount int64 `json:"commentCount"`
	// FileCount 是附件数。
	FileCount int64 `json:"fileCount"`
}

// StoryDetailResp 是需求详情，比列表多出版本规格、评审记录与关联。
type StoryDetailResp struct {
	StoryResp
	// Spec/Verify 是当前版本的内容。
	Spec   string `json:"spec"`
	Verify string `json:"verify"`
	// SpecFiles 是当前版本描述里附带的附件 id。
	SpecFiles []uint `json:"specFiles,omitempty"`
	// Reviews 是当前版本的评审记录。
	Reviews []StoryReviewResp `json:"reviews,omitempty"`
	// Files 是需求上的全部附件。
	Files []FileResp `json:"files,omitempty"`
}

// StoryReviewResp 是评审记录出参。
type StoryReviewResp struct {
	Reviewer     uint   `json:"reviewer"`
	ReviewerName string `json:"reviewerName,omitempty"`
	// Result 取 pass/revert/clarify/reject，空表示尚未评审。
	Result     string `json:"result"`
	ResultText string `json:"resultText,omitempty"`
	ReviewDate string `json:"reviewDate"`
}

// StoryLinkResp 描述需求与项目/迭代的关联。
//
// 需求侧与项目侧共用这一个结构：从需求详情看是「这条需求进了哪些项目」，
// 从项目详情看是「这个项目排了哪些需求」，两边要的信息是同一份
// （项目、迭代、排入时的版本、是否已过期），只是关注点不同。
type StoryLinkResp struct {
	ID          uint   `json:"id"`
	ProjectID   uint   `json:"projectId"`
	ProjectName string `json:"projectName,omitempty"`
	ExecutionID uint   `json:"executionId"`
	ExecName    string `json:"executionName,omitempty"`
	// StoryID/StoryTitle 在项目侧的列表里有值；需求侧冗余（就是需求自己）。
	StoryID    uint   `json:"storyId"`
	StoryTitle string `json:"storyTitle,omitempty"`
	// Version 是排入时的需求版本；与当前版本不一致说明需求已变更，
	// 前端据此标注「需求已变更，请确认」。
	Version  int  `json:"version"`
	Outdated bool `json:"outdated"`
	Order    int  `json:"order"`
}

// StorySpecResp 是历史版本快照出参。
type StorySpecResp struct {
	Version   int    `json:"version"`
	Title     string `json:"title"`
	Spec      string `json:"spec"`
	Verify    string `json:"verify"`
	CreatedAt string `json:"createdAt"`
}

// ---------------------------------------------------------------------------
// 动作请求
// ---------------------------------------------------------------------------

// StorySubmitReviewReq 是提交评审。
type StorySubmitReviewReq struct {
	Version   int    `json:"version"`
	Reviewers []uint `json:"reviewers"`
	Comment   string `json:"comment"`
}

// StoryReviewReq 是评审结论。
type StoryReviewReq struct {
	Version int `json:"version"`
	// Result 取 pass / revert / clarify / reject。
	Result  string `json:"result" validate:"required,oneof=pass revert clarify reject"`
	Comment string `json:"comment"`
	// ClosedReason 在 reject 时必填。
	ClosedReason string `json:"closedReason" validate:"omitempty,oneof=done subdivided duplicate postponed willnotdo cancel bydesign"`
}

// StoryChangeReq 是需求变更：版本 +1 并写入新内容。
type StoryChangeReq struct {
	// Version 是变更前的版本，用于乐观锁。
	Version int `json:"version" validate:"required"`

	Title  string `json:"title" validate:"required,max=255"`
	Spec   string `json:"spec"`
	Verify string `json:"verify"`
	// Reason 是变更原因，必填 —— 需求变更必须留下理由，
	// 否则下游看到「内容变了但不知道为什么」。
	Reason string `json:"reason" validate:"required,max=255"`

	Files   []uint `json:"files"`
	Comment string `json:"comment"`
}

// StoryCloseReq 是关闭需求。
type StoryCloseReq struct {
	ClosedReason string `json:"closedReason" validate:"required,oneof=done subdivided duplicate postponed willnotdo cancel bydesign"`
	Comment      string `json:"comment"`
	// DuplicateStoryID 在关闭原因为 duplicate 时必填。
	DuplicateStoryID uint `json:"duplicateStoryId"`
}

// StoryActivateReq 是激活已关闭的需求。
type StoryActivateReq struct {
	// AssignedTo 可选：激活时顺便指派给某人。
	AssignedTo uint   `json:"assignedTo"`
	Comment    string `json:"comment"`
}

// StoryAssignReq 是指派需求。
type StoryAssignReq struct {
	AssignedTo uint   `json:"assignedTo" validate:"required"`
	Comment    string `json:"comment"`
}

// StoryRecallReq 是撤销评审。
type StoryRecallReq struct {
	Comment string `json:"comment"`
}

// StoryLinkReq 是把需求排入项目/迭代。
//
// 路由只挂在项目下（POST /projects/:id/stories）：ProjectID 从路径取，
// StoryID 由请求体给出，需求侧不再另开一个 /stories/:id/link。
// 理由是排期的校验全都落在项目域（项目得关联了该需求的产品、
// 迭代得属于该项目），从需求侧进还要反向把项目的仓储再串一遍。
type StoryLinkReq struct {
	ProjectID   uint `json:"projectId"`
	StoryID     uint `json:"storyId" validate:"required"`
	ExecutionID uint `json:"executionId"`
	Order       int  `json:"order"`
}

// StoryStageReq 是推进需求阶段。
type StoryStageReq struct {
	Stage   string `json:"stage" validate:"required"`
	Comment string `json:"comment"`
}
