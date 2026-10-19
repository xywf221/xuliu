package model

import "time"

// TestCase 测试用例。
//
// 它有两个各自独立的维度，这是刻意分开的：
//
//   - Stage  是**评审生命周期**：wait → reviewing → confirmed / rejected。
//     走状态机，因为「谁在什么时候认可了这条用例」是要卡权限、要留痕的。
//   - Status 是**可执行性**：normal / blocked / investigate。
//     它回答「现在能不能跑」，与「有没有被认可」是两件事 ——
//     「评审通过了，但依赖的环境还没好」是测试日常，硬塞进一个字段
//     就会出现 pass 但又 blocked 这种没法表达的中间态。
//
// Version 是用例自身的版本号，每次编辑 +1。执行记录会记下当时的版本号，
// 于是「那次跑的到底是哪一版步骤」永远可追 —— 这也是我们不给用例建历史表
// 的原因：历史由执行快照承担，用例这一侧只保留当前事实。
//
// 与需求的关系是「溯源」而非「归属」：StoryID 可空，指向需求是为了回答
// 「这条需求被哪些用例覆盖」，以及需求变更后把相关用例挑出来复核
// （StoryVersion 与需求当前版本不一致时，前端标「需求已变更」）。
type TestCase struct {
	BaseModel

	ProductID uint `gorm:"index;not null" json:"productId"`
	ModuleID  uint `gorm:"index;not null;default:0" json:"moduleId"`

	// 溯源需求。StoryID 为 0 表示这条用例不对应具体需求（如探索性测试）。
	StoryID      uint `gorm:"index" json:"storyId"`
	StoryVersion int  `gorm:"not null;default:0" json:"storyVersion"`

	Title        string `gorm:"size:255;not null;index" json:"title"`
	Precondition string `gorm:"type:text" json:"precondition"`
	Keywords     string `gorm:"size:255" json:"keywords"`

	// Type 取值见 TestCaseType* 常量。
	Type string `gorm:"size:20;not null;default:feature;index" json:"type"`
	// Pri 是 1–4，0 表示未设置（与需求/缺陷口径一致）。
	Pri int `gorm:"not null;default:0;index" json:"pri"`

	// Stage 取值见 TestCaseStage* 常量。
	Stage string `gorm:"size:20;not null;default:wait;index" json:"stage"`
	// Status 取值见 TestCaseStatus* 常量。
	Status string `gorm:"size:20;not null;default:normal;index" json:"status"`

	// Version 从 1 起，每次编辑 +1；执行记录按 (caseId, version) 定位当时步骤。
	Version int `gorm:"not null;default:1" json:"version"`

	OpenedBy     uint       `gorm:"index;not null" json:"openedBy"`
	OpenedDate   *time.Time `json:"openedDate"`
	ReviewedBy   uint       `gorm:"index" json:"reviewedBy"`
	ReviewedDate *time.Time `json:"reviewedDate"`

	LastEditedBy   uint       `json:"lastEditedBy"`
	LastEditedDate *time.Time `json:"lastEditedDate"`

	// Desc 是补充说明（为什么这么测、边界在哪），与步骤分开：
	// 步骤是要逐条执行的，说明不是。
	Desc  string `gorm:"type:text" json:"desc"`
	Order int    `gorm:"not null;default:0" json:"order"`
}

func (TestCase) TableName() string { return "pm_testcase" }

// TestCaseStep 用例步骤。
//
// 一步一个「操作」配一个「期望」：期望结果写在步骤里而不是用例末尾，
// 是因为执行时要逐步判定 —— 一条用例常常第 3 步就断了，
// 只有分步期望才能说清断在哪、断的那步实际发生了什么。
//
// 步骤**不单独版本化**：编辑用例时整个步骤集合被替换，同时用例 Version +1，
// 历史步骤由执行记录里的快照保存。这样避免「步骤改了三版，用例还是同一行」
// 这种对不上的状态。
type TestCaseStep struct {
	BaseModel

	CaseID uint `gorm:"index;not null" json:"caseId"`
	// Order 从 1 开始，执行与展示都按它排序。
	Order  int    `gorm:"not null;default:0;index" json:"order"`
	Step   string `gorm:"type:text" json:"step"`
	Expect string `gorm:"type:text" json:"expect"`
}

func (TestCaseStep) TableName() string { return "pm_testcasestep" }

// TestTask 测试单：一批用例在某次构建上的执行活动。
//
// 禅道把结论塞进状态里（status 取值含 pass/fail），于是「测完了但结论是失败」
// 与「还没测完」共用同一个字段，统计时要拆开解释。这里分开两列：
//
//   - Status 只表达流程位置：wait → doing →（blocked ↔ doing）→ closed；
//   - Result 是结单时给出的结论（pass / fail / blocked），只有 closed 才有意义。
//
// 一个测试单必须属于一个产品：测的是这个产品的东西。项目/迭代可选 ——
// 缺陷可以只挂产品，测试单同理（线上问题的回归测试往往不属于任何项目）。
type TestTask struct {
	BaseModel

	ProductID   uint `gorm:"index;not null" json:"productId"`
	ProjectID   uint `gorm:"index" json:"projectId"`
	ExecutionID uint `gorm:"index" json:"executionId"`
	// BuildID 是被测构建。存 id 而非名称快照：构建列表本来就长，
	// 且「同一个构建复测」是要按构建查的。
	BuildID uint `gorm:"index" json:"buildId"`

	Name string `gorm:"size:255;not null;index" json:"name"`
	Desc string `gorm:"type:text" json:"desc"`

	// Status 取值见 TestTaskStatus* 常量；Result 见 TestTaskResult*。
	Status string `gorm:"size:20;not null;default:wait;index" json:"status"`
	Result string `gorm:"size:20;index" json:"result"`
	Pri    int    `gorm:"not null;default:0;index" json:"pri"`

	AssignedTo   uint       `gorm:"index" json:"assignedTo"`
	AssignedDate *time.Time `json:"assignedDate"`

	// Begin/End 是计划起止，StartedAt/FinishedAt 是实际发生时间。
	// 分开存：计划永远是计划，改了计划不该伪造出「其实早就开工了」。
	Begin      *time.Time `json:"begin"`
	End        *time.Time `json:"end"`
	StartedAt  *time.Time `json:"startedAt"`
	FinishedAt *time.Time `json:"finishedAt"`
	// LastRunDate 是最后一次记录执行结果的时间，列表按它排「最近有动静的单」。
	LastRunDate *time.Time `json:"lastRunDate"`

	OpenedBy   uint       `gorm:"index;not null" json:"openedBy"`
	OpenedDate *time.Time `json:"openedDate"`
	ClosedBy   uint       `json:"closedBy"`

	// Report 是结单时的总结（结论、遗留风险、环境说明）。
	Report string `gorm:"type:text" json:"report"`

	// Cases 是纳入的用例数快照。冗余一列而不是每次 COUNT 子表：
	// 列表页要显示这一列，而 COUNT 子表会让每页多一次全表扫。
	CaseCount int `gorm:"not null;default:0" json:"caseCount"`

	LastEditedBy   uint       `json:"lastEditedBy"`
	LastEditedDate *time.Time `json:"lastEditedDate"`
	Order          int        `gorm:"not null;default:0" json:"order"`
}

func (TestTask) TableName() string { return "pm_testtask" }

// TestTaskCase 测试单与用例的关联（M:N）。
//
// 除了两个 id，还带三样各自有用的东西：
//
//   - CaseVersion：纳入时锁定的用例版本。用例后来改了，单里的这条
//     仍记录「当时用的是第几版」，否则事后无法解释结果为什么对不上。
//   - LastResult/LastRunDate/RunCount：该用例在本单内的最新状态。
//     跑过多次时完整历史在 pm_testrun，这里只是让列表不必逐行 join。
type TestTaskCase struct {
	BaseModel

	TaskID uint `gorm:"index;not null" json:"taskId"`
	CaseID uint `gorm:"index;not null" json:"caseId"`
	// ProductID/StoryID 冗余自用例，让「按产品/需求筛测试单里的用例」
	// 不必再回到用例表。
	ProductID   uint `gorm:"index" json:"productId"`
	StoryID     uint `gorm:"index" json:"storyId"`
	CaseVersion int  `gorm:"not null;default:1" json:"caseVersion"`

	LastResult  string     `gorm:"size:20;index" json:"lastResult"`
	LastRunDate *time.Time `json:"lastRunDate"`
	RunCount    int        `gorm:"not null;default:0" json:"runCount"`

	// AssignedTo 是这条用例在本单内的执行人；为 0 表示未单独指派，
	// 由测试单负责人统一安排。
	AssignedTo uint `gorm:"index" json:"assignedTo"`

	Order int `gorm:"not null;default:0" json:"order"`
}

func (TestTaskCase) TableName() string { return "pm_testtaskcase" }

// TestRun 一次用例执行记录。
//
// 同一用例在同一单里可以跑很多次（修一版跑一版），所以这里是追加式历史，
// 而不是「更新那条用例的状态」。跑过几次、每次什么结果、失败时实际看到什么，
// 都问这张表。
//
// Steps 存执行时的步骤快照（JSON 文本，含每步的期望与实际）：
// 用例后来被改动甚至删除，这条记录仍要能自证当时跑的是什么。
// 这也是不给用例建历史表的原因 —— 历史由执行记录承担。
type TestRun struct {
	BaseModel

	TaskID      uint `gorm:"index;not null" json:"taskId"`
	CaseID      uint `gorm:"index;not null" json:"caseId"`
	CaseVersion int  `gorm:"not null;default:1" json:"caseVersion"`

	ProductID   uint `gorm:"index" json:"productId"`
	ProjectID   uint `gorm:"index" json:"projectId"`
	ExecutionID uint `gorm:"index" json:"executionId"`
	BuildID     uint `gorm:"index" json:"buildId"`

	// Result 取值见 TestRunResult* 常量。
	Result string `gorm:"size:20;not null;index" json:"result"`
	// Steps 是步骤快照：[{step,expect,actual,result}]。
	Steps string `gorm:"type:text" json:"steps"`
	Note  string `gorm:"type:text" json:"note"`

	// BugIDs 是本次执行发现/关联的缺陷 id（JSON 数组）。
	// 存快照而不是建关联表：一次失败可能牵出几个缺陷，且缺陷后续被删
	// 也不该改写「当时发现了什么」这条历史。
	BugIDs string `gorm:"type:text" json:"bugIds"`

	ExecutedBy   uint       `gorm:"index;not null" json:"executedBy"`
	ExecutedDate *time.Time `json:"executedDate"`
	// Duration 是本次执行耗时（小时），与工时同一精度口径。
	Duration float64 `gorm:"type:decimal(12,4);not null;default:0" json:"duration"`
}

func (TestRun) TableName() string { return "pm_testrun" }
