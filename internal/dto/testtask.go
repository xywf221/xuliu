package dto

// 测试单与执行记录域请求与响应。
//
// 测试单是「一批用例在某次构建上的一次测试活动」，执行记录是其中每条用例
// 每次跑的结果。这两者的请求形状决定了本文件的两个关键约定：
//
//   - 结单必须给结论（result），且结论只有 pass/fail/blocked 三种 ——
//     「测完了」这件事本身不足以描述一次测试的产出。
//   - 记录执行结果时，步骤的实际情况由客户端逐条提交，后端按用例当时的
//     版本存一份快照。事后用例被改坏或被删，历史记录仍要能自证。
//
// 人名的出参键一律是 `<动作>ByName`（assignedToName / openedByName /
// executedByName），与需求、任务、缺陷三个域一致。前端的「谁」列是
// 按这个后缀取值的，本域若写成 assignedName，那一列会静默显示空值 ——
// 不报错，只是没人看得见。

// ---------------------------------------------------------------------------
// 测试单
// ---------------------------------------------------------------------------

// TestTaskReq 是新建/编辑测试单的请求。
//
// 与缺陷一样，整体覆盖：没提交的字段会被写回零值。
type TestTaskReq struct {
	// ProductID 必填：测试总是围绕某个产品。
	ProductID uint `json:"productId" validate:"required"`
	// ProjectID/ExecutionID 可选（线上回归可能不属于任何项目）。
	ProjectID   uint `json:"projectId"`
	ExecutionID uint `json:"executionId"`
	BuildID     uint `json:"buildId"`

	Name string `json:"name" validate:"required,max=255"`
	Desc string `json:"desc"`
	Pri  int    `json:"pri" validate:"omitempty,max=4"`

	AssignedTo uint `json:"assignedTo"`

	// Begin/End 是计划起止，格式 YYYY-MM-DD。
	Begin string `json:"begin" validate:"omitempty,datetime=2006-01-02"`
	End   string `json:"end" validate:"omitempty,datetime=2006-01-02"`

	Order int `json:"order"`

	// CaseIDs 是新建/编辑时一并纳入的用例（可选）。
	// 与「先建单再加用例」两条路都通：前端从需求列表勾选建单更省事。
	CaseIDs []uint `json:"caseIds"`
}

// TestTaskFilterReq 是测试单列表的查询参数。
type TestTaskFilterReq struct {
	ProductID   uint `form:"productId"`
	ProjectID   uint `form:"projectId"`
	ExecutionID uint `form:"executionId"`
	BuildID     uint `form:"buildId"`

	Status string `form:"status"`
	Result string `form:"result"`
	Pri    *int   `form:"pri"`

	AssignedTo uint `form:"assignedTo"`
	OpenedBy   uint `form:"openedBy"`

	// Unfinished 只看没结单的。
	Unfinished bool `form:"unfinished"`
	// NoCase 只看还没纳入用例的单。
	NoCase bool `form:"noCase"`

	Keywords string

	Page  int    `form:"page"`
	Size  int    `form:"size"`
	Order string `form:"order"`
}

// TestTaskResp 是测试单出参。
type TestTaskResp struct {
	ID   uint   `json:"id"`
	Code string `json:"code"`

	ProductID   uint   `json:"productId"`
	ProductName string `json:"productName,omitempty"`
	ProjectID   uint   `json:"projectId"`
	ProjectName string `json:"projectName,omitempty"`
	ExecutionID uint   `json:"executionId"`
	BuildID     uint   `json:"buildId"`
	BuildName   string `json:"buildName,omitempty"`

	Name   string `json:"name"`
	Desc   string `json:"desc,omitempty"`
	Pri    int    `json:"pri"`
	Status string `json:"status"`
	// Result 只在已结单时有值：pass / fail / blocked。
	Result string `json:"result"`
	Report string `json:"report,omitempty"`

	AssignedTo   uint   `json:"assignedTo"`
	AssignedName string `json:"assignedToName,omitempty"`

	Begin       string `json:"begin,omitempty"`
	End         string `json:"end,omitempty"`
	StartedAt   string `json:"startedAt,omitempty"`
	FinishedAt  string `json:"finishedAt,omitempty"`
	LastRunDate string `json:"lastRunDate,omitempty"`

	OpenedBy   uint   `json:"openedBy"`
	OpenedName string `json:"openedByName,omitempty"`
	OpenedDate string `json:"openedDate,omitempty"`

	CaseCount int `json:"caseCount"`
	// Summary 是单内用例的逐结论计数（pass/fail/blocked/unrun 四种）。
	Summary TestTaskSummary `json:"summary"`

	Actions []TransitionResp `json:"actions,omitempty"`
}

// TestTaskSummary 是测试单的执行进度概览。
type TestTaskSummary struct {
	Pass    int64 `json:"pass"`
	Fail    int64 `json:"fail"`
	Blocked int64 `json:"blocked"`
	Unrun   int64 `json:"unrun"`
	// Total 是纳入的用例总数（上面四项之和）。
	Total int64 `json:"total"`
}

// TestTaskDetailResp 是测试单详情，带单内用例清单。
type TestTaskDetailResp struct {
	TestTaskResp

	// Cases 是单内用例，含每条的最新结论与执行次数。
	Cases []TaskCaseResp `json:"cases,omitempty"`
}

// TaskCaseResp 是「单内的一条用例」。
type TaskCaseResp struct {
	CaseID      uint   `json:"caseId"`
	CaseTitle   string `json:"caseTitle,omitempty"`
	CaseVersion int    `json:"caseVersion"`
	// CaseCurrentVersion 是用例此刻的版本；与 CaseVersion 不等说明
	// 纳入之后用例又改过，本单里的结果是对着旧版跑的。
	CaseCurrentVersion int    `json:"caseCurrentVersion"`
	ProductID          uint   `json:"productId"`
	StoryID            uint   `json:"storyId"`
	LastResult         string `json:"lastResult"`
	LastRunDate        string `json:"lastRunDate,omitempty"`
	RunCount           int    `json:"runCount"`
	AssignedTo         uint   `json:"assignedTo"`
	AssignedName       string `json:"assignedToName,omitempty"`
	Order              int    `json:"order"`
}

// TestTaskFinishReq 是结单请求。
//
// 结论必填由服务层校验（而不是只靠 DTO）：结单是本域最重要的动作，
// 「点了结单但没结论」比「没结单」更糟。
type TestTaskFinishReq struct {
	// Result 取 pass / fail / blocked。
	Result string `json:"result" validate:"required,oneof=pass fail blocked"`
	Report string `json:"report"`
}

// AssignUserReq 是「指派给某个人」的请求。
//
// 字段名与任务的 TaskAssignReq 保持一致（assignedTo），前端一处写的
// 指派弹窗可以直接复用到测试单上；不叫 userId 是为了让请求体里
// 「谁被指派」这件事在各类指派接口里长得一样。
type AssignUserReq struct {
	AssignedTo uint   `json:"assignedTo" validate:"required"`
	Comment    string `json:"comment" validate:"omitempty,max=255"`
}

// AddTaskCasesReq 是往单里纳入用例的请求。
type AddTaskCasesReq struct {
	CaseIDs []uint `json:"caseIds" validate:"required,min=1,dive"`
}

// 单内用例的指派不另开请求结构：它与整单指派字段一致
// （assignedTo + comment），复用 AssignUserReq 让前端一处写的指派弹窗
// 在两种指派上都成立。

// ---------------------------------------------------------------------------
// 执行记录
// ---------------------------------------------------------------------------

// TestRunStepReq 是一次执行里单个步骤的实际情况。
type TestRunStepReq struct {
	Order int `json:"order"`
	// Step/Expect 由后端按用例当时的版本回填，客户端不必传。
	Step   string `json:"step"`
	Expect string `json:"expect"`
	Actual string `json:"actual"`
	// Result 取 pass/fail/blocked；留空表示跟随整条用例的结论。
	Result string `json:"result" validate:"omitempty,oneof=pass fail blocked"`
}

// TestRunReq 是「记录一次用例执行」的请求。
type TestRunReq struct {
	// Result 是这条用例本次的结论。
	Result string `json:"result" validate:"required,oneof=pass fail blocked"`
	// Steps 是逐步骤的实际情况，用于「断在第几步」。
	Steps []TestRunStepReq `json:"steps"`
	Note  string           `json:"note"`

	// Duration 是本次执行耗时（小时）。
	Duration float64 `json:"duration" validate:"omitempty,gte=0"`

	// BugIDs 是本次发现/关联的缺陷。失败时不强制填：
	// 先记录结果、回头再建缺陷是常见顺序。
	BugIDs []uint `json:"bugIds"`

	// BuildID 是实际跑的构建；留空则沿用测试单上的构建。
	BuildID uint `json:"buildId"`
}

// TestRunFilterReq 是执行记录的查询参数。
type TestRunFilterReq struct {
	TaskID uint   `form:"taskID"`
	CaseID uint   `form:"caseId"`
	Result string `form:"result"`

	Page  int    `form:"page"`
	Size  int    `form:"size"`
	Order string `form:"order"`
}

// TestRunResp 是一次执行记录出参。
type TestRunResp struct {
	ID          uint   `json:"id"`
	TaskID      uint   `json:"taskId"`
	CaseID      uint   `json:"caseId"`
	CaseTitle   string `json:"caseTitle,omitempty"`
	CaseVersion int    `json:"caseVersion"`

	ProductID   uint   `json:"productId"`
	ProjectID   uint   `json:"projectId"`
	ExecutionID uint   `json:"executionId"`
	BuildID     uint   `json:"buildId"`
	BuildName   string `json:"buildName,omitempty"`

	Result string           `json:"result"`
	Steps  []TestRunStepReq `json:"steps,omitempty"`
	Note   string           `json:"note,omitempty"`

	BugIDs   []uint   `json:"bugIds,omitempty"`
	BugNames []string `json:"bugNames,omitempty"`

	ExecutedBy   uint    `json:"executedBy"`
	ExecutedName string  `json:"executedByName,omitempty"`
	ExecutedDate string  `json:"executedDate,omitempty"`
	Duration     float64 `json:"duration"`
}
