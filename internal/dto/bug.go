package dto

// 缺陷域请求与响应。
//
// 缺陷是本平台里「字段最多、但语义最直白」的实体：它不版本化、不拆分工时，
// 核心就是「谁在什么环境里发现了什么问题，怎么解决的」。
// 因此出参的重点是把环境信息、状态轨迹与解决方式一次带全，
// 让列表页不必再为每一行补查。

// ---------------------------------------------------------------------------
// 缺陷
// ---------------------------------------------------------------------------

// BugReq 是提交/编辑缺陷的请求。
//
// 提交与编辑共用同一个结构，因此编辑是**整体覆盖**语义：
// 请求里没带的字段会被写回零值（清空关联需求、取消指派等）。
// 前端编辑表单必须提交完整对象；这也是 ProductID 在编辑时仍然必填的原因。
type BugReq struct {
	// ProductID 必填：缺陷总是属于某个产品（Bug 是针对产品的质量问题）。
	ProductID   uint `json:"productId" validate:"required"`
	ProjectID   uint `json:"projectId"`
	ExecutionID uint `json:"executionId"`
	ModuleID    uint `json:"moduleId"`
	PlanID      uint `json:"planId"`

	// StoryID/TaskID 关联到需求或任务（在哪个需求的验证里发现的）。
	StoryID uint `json:"storyId"`
	TaskID  uint `json:"taskId"`

	Title    string `json:"title" validate:"required,max=255"`
	Keywords string `json:"keywords" validate:"omitempty,max=255"`
	Steps    string `json:"steps"`

	// Type 取 codeerror/config/install/security/performance/standard/
	// automation/designdefect/codeimprovement/others。
	Type string `json:"type" validate:"omitempty,oneof=codeerror config install security performance standard automation designdefect codeimprovement others"`
	// Severity/Pri 都是 1..4（1 最高）。
	Severity int `json:"severity" validate:"omitempty,gte=1,lte=4"`
	Pri      int `json:"pri" validate:"omitempty,gte=1,lte=4"`

	OS       string `json:"os" validate:"omitempty,max=255"`
	Browser  string `json:"browser" validate:"omitempty,max=255"`
	Hardware string `json:"hardware" validate:"omitempty,max=60"`

	// OpenedBuild 是影响版本。
	OpenedBuild string `json:"openedBuild" validate:"omitempty,max=255"`
	Deadline    string `json:"deadline" validate:"omitempty,datetime=2006-01-02"`

	AssignedTo uint `json:"assignedTo"`
	// RelatedBugIDs 是相关缺陷。
	RelatedBugIDs []uint `json:"relatedBugIds"`
	// Files 是本次提交附带的附件。
	Files []uint `json:"files"`

	Order int `json:"order"`
}

// BugFilterReq 是缺陷列表的查询参数。
type BugFilterReq struct {
	ProductID   uint `form:"productId"`
	ProjectID   uint `form:"projectId"`
	ExecutionID uint `form:"executionId"`
	ModuleID    uint `form:"moduleId"`
	PlanID      uint `form:"planId"`
	StoryID     uint `form:"storyId"`
	TaskID      uint `form:"taskId"`

	Status     string `form:"status"`
	Resolution string `form:"resolution"`
	// Unresolved 只看还没给出解决方案的缺陷。
	Unresolved bool   `form:"unresolved"`
	Type       string `form:"type"`
	Severity   *int   `form:"severity"`
	Pri        *int   `form:"pri"`
	// Confirmed 为 nil 表示不过滤；0 未确认，1 已确认。
	Confirmed *int `form:"confirmed"`

	AssignedTo *uint `form:"assignedTo"`
	OpenedBy   uint  `form:"openedBy"`
	ResolvedBy uint  `form:"resolvedBy"`

	Keywords     string `form:"keywords"`
	DeadlineFrom string `form:"deadlineFrom"`
	DeadlineTo   string `form:"deadlineTo"`

	Page  int    `form:"page"`
	Size  int    `form:"size"`
	Order string `form:"order"`
}

// BugResp 是缺陷出参。
type BugResp struct {
	ID uint `json:"id"`
	// Code 是稳定编号，形如 BUG-12。
	Code string `json:"code"`

	ProductID     uint   `json:"productId"`
	ProductName   string `json:"productName,omitempty"`
	ProjectID     uint   `json:"projectId"`
	ProjectName   string `json:"projectName,omitempty"`
	ExecutionID   uint   `json:"executionId"`
	ExecutionName string `json:"executionName,omitempty"`
	ModuleID      uint   `json:"moduleId"`
	ModuleName    string `json:"moduleName,omitempty"`
	PlanID        uint   `json:"planId"`
	PlanTitle     string `json:"planTitle,omitempty"`
	StoryID       uint   `json:"storyId"`
	StoryTitle    string `json:"storyTitle,omitempty"`
	StoryVersion  int    `json:"storyVersion"`
	TaskID        uint   `json:"taskId"`
	TaskName      string `json:"taskName,omitempty"`

	Title    string `json:"title"`
	Keywords string `json:"keywords"`
	Steps    string `json:"steps"`

	Type          string `json:"type"`
	TypeLabel     string `json:"typeLabel"`
	Severity      int    `json:"severity"`
	SeverityLabel string `json:"severityLabel"`
	Pri           int    `json:"pri"`
	PriLabel      string `json:"priLabel"`

	OS       string `json:"os"`
	Browser  string `json:"browser"`
	Hardware string `json:"hardware"`

	Status          string `json:"status"`
	StatusLabel     string `json:"statusLabel"`
	Resolution      string `json:"resolution"`
	ResolutionLabel string `json:"resolutionLabel"`
	Confirmed       int    `json:"confirmed"`
	// ActivatedCount 是被重新激活的次数。
	ActivatedCount int `json:"activatedCount"`

	OpenedBuild   string `json:"openedBuild"`
	ResolvedBuild string `json:"resolvedBuild"`

	OpenedBy     uint   `json:"openedBy"`
	OpenedName   string `json:"openedByName,omitempty"`
	OpenedDate   string `json:"openedDate"`
	AssignedTo   uint   `json:"assignedTo"`
	AssignName   string `json:"assignedToName,omitempty"`
	AssignedDate string `json:"assignedDate"`
	Deadline     string `json:"deadline"`

	ResolvedBy   uint   `json:"resolvedBy"`
	ResolvedName string `json:"resolvedByName,omitempty"`
	ResolvedDate string `json:"resolvedDate"`

	ClosedBy     uint   `json:"closedBy"`
	ClosedName   string `json:"closedByName,omitempty"`
	ClosedDate   string `json:"closedDate"`
	ClosedReason string `json:"closedReason"`

	ActivatedDate string `json:"activatedDate"`

	// DuplicateBugID 在解决方式为「重复」时指向源缺陷。
	DuplicateBugID    uint   `json:"duplicateBugId"`
	DuplicateBugTitle string `json:"duplicateBugTitle,omitempty"`
	// ToStoryID 是「已转需求」后的成果。
	ToStoryID     uint   `json:"toStoryId"`
	ToStoryTitle  string `json:"toStoryTitle,omitempty"`
	RelatedBugIDs []uint `json:"relatedBugIds"`

	Order int `json:"order"`

	// 详情专有字段。
	Files        []FileResp       `json:"files,omitempty"`
	Actions      []TransitionResp `json:"actions,omitempty"`
	CommentCount int64            `json:"commentCount"`
	FileCount    int64            `json:"fileCount"`
}

// BugStatusCountsResp 是列表页顶部按状态统计的标签值。
//
// 单独给一个结构而不是让前端自己数：列表是分页的，
// 前端手上的那 20 条数不出「全部激活缺陷有多少」。
type BugStatusCountsResp struct {
	Active   int64 `json:"active"`
	Resolved int64 `json:"resolved"`
	Closed   int64 `json:"closed"`
	// Unresolved 是尚未给出解决方案的（resolution 为空）数量。
	Unresolved int64 `json:"unresolved"`
}

// ---------------------------------------------------------------------------
// 动作请求
// ---------------------------------------------------------------------------

// BugAssignReq 是指派缺陷。
type BugAssignReq struct {
	AssignedTo uint   `json:"assignedTo" validate:"required"`
	Comment    string `json:"comment" validate:"omitempty,max=255"`
}

// BugResolveReq 是解决缺陷。
type BugResolveReq struct {
	// Resolution 必填，取 bydesign/duplicate/external/fixed/notrepro/
	// postponed/willnotfix/tostory。
	Resolution string `json:"resolution" validate:"required"`
	// ResolvedBuild 是解决版本。
	ResolvedBuild string `json:"resolvedBuild" validate:"omitempty,max=255"`
	// DuplicateBugID 在解决方案为重复时必填。
	DuplicateBugID uint   `json:"duplicateBugId"`
	Comment        string `json:"comment" validate:"omitempty,max=255"`
}

// BugCloseReq 是关闭缺陷。
type BugCloseReq struct {
	Comment string `json:"comment" validate:"omitempty,max=255"`
}

// BugActivateReq 是重新激活缺陷。
type BugActivateReq struct {
	// AssignedTo 重新激活时可以直接指派给某人。
	AssignedTo uint   `json:"assignedTo"`
	Comment    string `json:"comment" validate:"omitempty,max=255"`
}

// BugToStoryReq 是把缺陷转为需求。
type BugToStoryReq struct {
	// ProductID 是转入的产品，留空表示留在原产品。
	ProductID uint `json:"productId"`
	// Title/Spec 留空时用缺陷的标题与重现步骤兜底。
	Title  string `json:"title" validate:"omitempty,max=255"`
	Spec   string `json:"spec"`
	Verify string `json:"verify"`
	// AssignedTo 是新需求的负责人。
	AssignedTo uint `json:"assignedTo"`
	// Files 是随需求一起带过去的附件（常是缺陷的截图）。
	//
	// 注意是**转移**而非复制：附件只有一份归属，转过去之后缺陷详情页
	// 就不再显示它们了。不想让缺陷丢掉截图就别传这个字段。
	Files []uint `json:"files"`
	// Reviewers 是新需求的评审人；产品开了评审又没设默认评审人时，
	// 必须在这里给出，否则这条需求会建不出来（评审人缺失是硬错误）。
	Reviewers []uint `json:"reviewers"`
	Comment   string `json:"comment" validate:"omitempty,max=255"`
}
