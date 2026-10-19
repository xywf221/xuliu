package dto

// 测试用例域请求与响应。
//
// 用例的核心是「一组可逐步判定的操作」，所以请求里 steps 是必填且非空的：
// 没有步骤的用例无法执行，也无法回答「断在第几步」。
//
// 与缺陷一样，新建与编辑共用一个请求结构，编辑是**整体覆盖**语义 ——
// 步骤集合按提交内容整体替换（并让用例 Version +1）。这样做而不是做增量，
// 是因为步骤顺序本身就是语义的一部分，增量更新要额外表达「插到第几位」，
// 前端反而更难写对。

// ---------------------------------------------------------------------------
// 测试用例
// ---------------------------------------------------------------------------

// TestCaseStepReq 是一条用例步骤。
//
// ID 供前端保持稳定 key（新增行留空即可）；后端按提交内容整体重建，
// 因此不会用 ID 去匹配旧行。
type TestCaseStepReq struct {
	ID     uint   `json:"id"`
	Order  int    `json:"order"`
	Step   string `json:"step" validate:"required,max=2000"`
	Expect string `json:"expect" validate:"omitempty,max=2000"`
}

// TestCaseReq 是新建/编辑用例的请求。
type TestCaseReq struct {
	ProductID uint `json:"productId" validate:"required"`
	ModuleID  uint `json:"moduleId"`
	// StoryID 是可追溯的需求来源；为 0 表示这条用例不对应具体需求。
	StoryID uint `json:"storyId"`

	Title        string `json:"title" validate:"required,max=255"`
	Precondition string `json:"precondition"`
	Keywords     string `json:"keywords" validate:"omitempty,max=255"`
	Desc         string `json:"desc"`

	// Type 取 feature/interface/performance/config/security/others。
	Type string `json:"type" validate:"omitempty,oneof=feature interface performance config security others"`
	Pri  int    `json:"pri" validate:"omitempty,max=4"`
	// Status 是可执行性：normal/blocked/investigate。评审阶段（stage）不在这里，
	// 它只能走状态机动作，普通编辑改不了 —— 否则就绕过了评审。
	Status string `json:"status" validate:"omitempty,oneof=normal blocked investigate"`

	// Steps 至少一步：这是用例能否被执行与判定的底线。
	Steps []TestCaseStepReq `json:"steps" validate:"required,min=1,dive"`

	Order int `json:"order"`
}

// TestCaseFilterReq 是用例列表的查询参数。
//
// 没有「按上一轮执行结果筛选」这类参数：执行记录（testrun）还没落地，
// 与其开一个永远返回空的筛选口，不如等它真有数据时再加。
type TestCaseFilterReq struct {
	ProductID uint `form:"productId"`
	ModuleID  uint `form:"moduleId"`
	StoryID   uint `form:"storyId"`

	// Stage 取 wait/reviewing/confirmed/rejected。
	Stage string `form:"stage"`
	// Status 取 normal/blocked/investigate。
	Status string `form:"status"`
	Type   string `form:"type"`
	Pri    *int   `form:"pri"`

	OpenedBy   uint `form:"openedBy"`
	ReviewedBy uint `form:"reviewedBy"`

	Keywords string `form:"keywords"`

	Page  int    `form:"page"`
	Size  int    `form:"size"`
	Order string `form:"order"`
}

// TestCaseStepResp 是一条步骤的出参。
type TestCaseStepResp struct {
	ID     uint   `json:"id"`
	Order  int    `json:"order"`
	Step   string `json:"step"`
	Expect string `json:"expect"`
}

// TestCaseResp 是用例列表与详情的公共出参。
type TestCaseResp struct {
	ID uint `json:"id"`
	// Code 是给人看的编号，如 CASE-12；主键仍是自增 ID。
	Code      string `json:"code"`
	ProductID uint   `json:"productId"`
	// ProductName/ModuleName 由 service 批量补全，列表不必逐行查。
	ProductName string `json:"productName,omitempty"`
	ModuleID    uint   `json:"moduleId"`
	ModuleName  string `json:"moduleName,omitempty"`

	StoryID    uint   `json:"storyId"`
	StoryTitle string `json:"storyTitle,omitempty"`
	// StoryVersion 是用例记录时锁定的需求版本；与需求当前版本不一致
	// 就说明需求变了、这条用例要复核。
	StoryVersion int `json:"storyVersion"`
	// StoryStale 就是上面那个比对的结论，由后端给：
	// 补齐需求标题时已经把需求整行查出来了，版本就在同一份结果里，
	// 顺手算掉比让前端再查一次需求更省，也更不容易各算各的。
	StoryStale bool `json:"storyStale"`

	Title        string `json:"title"`
	Keywords     string `json:"keywords"`
	Type         string `json:"type"`
	Pri          int    `json:"pri"`
	Stage        string `json:"stage"`
	Status       string `json:"status"`
	Version      int    `json:"version"`
	StepCount    int    `json:"stepCount"`
	Precondition string `json:"precondition,omitempty"`
	Desc         string `json:"desc,omitempty"`

	OpenedBy       uint   `json:"openedBy"`
	OpenedByName   string `json:"openedByName,omitempty"`
	OpenedDate     string `json:"openedDate,omitempty"`
	ReviewedBy     uint   `json:"reviewedBy"`
	ReviewedByName string `json:"reviewedByName,omitempty"`
	ReviewedDate   string `json:"reviewedDate,omitempty"`
	LastEditedBy   uint   `json:"lastEditedBy"`
	LastEditedDate string `json:"lastEditedDate,omitempty"`

	Order int `json:"order"`

	// Actions 来自状态机（按 stage 计算），前端据此渲染按钮。
	Actions []TransitionResp `json:"actions,omitempty"`

	CommentCount int64 `json:"commentCount"`
	FileCount    int64 `json:"fileCount"`
}

// TestCaseDetailResp 是用例详情，比列表多出步骤全集。
type TestCaseDetailResp struct {
	TestCaseResp

	// Steps 按 order 升序，执行时逐条判定。
	Steps []TestCaseStepResp `json:"steps"`
	Files []FileResp         `json:"files,omitempty"`
}

// 用例的其余评审动作（submitreview / recall）沿用通用的
// dto.StateActionReq —— 它只有一个 Comment 字段。
//
// 没有做「指派评审人」：谁有 testcase-review 权谁就能出结论，
// 实际谁评审了记在 reviewedBy 上。多加一层「指定评审人」只是把权限点
// 的判定挪到别处，并不更安全。

// CaseReviewReq 是给出评审结论的请求。
//
// 结果只有 pass / reject 两种：需求那边还有 clarify（有待明确）与 revert（撤销变更），
// 因为需求会版本化、要区分「结论是暂缓」与「结论是撤回上一版」；
// 用例的结论就是「这条用例能不能进测试单」，两种足够。
type CaseReviewReq struct {
	// Result 取 pass（确认可用）或 reject（驳回）。
	Result  string `json:"result" validate:"required,oneof=pass reject"`
	Comment string `json:"comment" validate:"omitempty,max=255"`
}
