package workflow

import (
	"github.com/xywf221/xuliu/internal/acl"
	"github.com/xywf221/xuliu/internal/model"
	"github.com/xywf221/xuliu/internal/pkg/apperr"
)

// BugMachine 缺陷状态机。
//
// 守卫条件一览（写这份表时拿禅道 module/bug/model.php 核过一遍，
// 免得漏掉它的边界情形；表本身与执行方式是自己的）：
//
//	confirm:  status == active && confirmed == 0
//	assignto: status != closed
//	resolve:  status == active
//	close:    status == resolved
//	activate: status != active          （这就是「重新激活」）
//	tostory:  status == active
//
// 注意 activate 的语义：它不叫 reopen，但承担了重新打开的全部职责 ——
// 把状态打回 active 并累加 ActivatedCount。缺陷的完整闭环是
// active → resolved → closed → （必要时）activate 回到 active。
var BugMachine = &Machine{
	ObjectType: model.ObjectTypeBug,
	ErrorCode:  apperr.BugStatusInvalid,
	Statuses: []string{
		model.BugStatusActive,
		model.BugStatusResolved,
		model.BugStatusClosed,
	},
	StatusLabels: map[string]string{
		model.BugStatusActive:   "激活",
		model.BugStatusResolved: "已解决",
		model.BugStatusClosed:   "已关闭",
	},
	Transitions: map[string]Transition{
		"create": {
			Action: "create", To: model.BugStatusActive,
			Perm: string(acl.PrivBugCreate), Label: "提交缺陷",
			Kind: KindCreate, LogAction: model.ActionOpened,
		},
		"edit": {
			Action: "edit",
			Perm:   string(acl.PrivBugEdit), Label: "编辑",
			Kind: KindEdit, LogAction: model.ActionEdited,
		},
		"confirm": {
			Action: "confirm",
			From:   []string{model.BugStatusActive},
			Perm:   string(acl.PrivBugConfirm), Label: "确认",
			Kind: KindStatus, LogAction: model.ActionConfirmed,
		},
		"assign": {
			Action: "assign",
			// 已关闭的缺陷不再流转，故不允许指派。
			From: []string{model.BugStatusActive, model.BugStatusResolved},
			Perm: string(acl.PrivBugAssign), Label: "指派",
			Kind: KindAssign, LogAction: model.ActionAssigned,
		},
		"resolve": {
			Action: "resolve",
			From:   []string{model.BugStatusActive},
			To:     model.BugStatusResolved,
			Perm:   string(acl.PrivBugResolve), Label: "解决",
			Kind: KindStatus, LogAction: model.ActionResolved,
		},
		"close": {
			Action: "close",
			From:   []string{model.BugStatusResolved},
			To:     model.BugStatusClosed,
			Perm:   string(acl.PrivBugClose), Label: "关闭",
			Kind: KindStatus, LogAction: model.ActionClosed,
		},
		"activate": {
			Action: "activate",
			From:   []string{model.BugStatusResolved, model.BugStatusClosed},
			To:     model.BugStatusActive,
			Perm:   string(acl.PrivBugActivate), Label: "重新激活",
			Kind: KindStatus, LogAction: model.ActionActivated,
		},
		"tostory": {
			Action: "tostory",
			From:   []string{model.BugStatusActive},
			Perm:   string(acl.PrivBugToStory), Label: "转需求",
			Kind: KindStatus, LogAction: model.ActionLinked,
		},
	},
}

// TaskMachine 任务状态机。
//
// 守卫条件一览（写这份表时拿禅道 module/task/model.php 核过一遍，
// 免得漏掉它的边界情形；表本身与执行方式是自己的）：
//
//	start:    status == wait（团队多人任务也允许从 doing 再开始）
//	pause:    status == doing
//	restart:  status == pause
//	finish:   status 不属于 {done, closed, cancel}
//	cancel:   status 不属于 {done, closed, cancel}
//	close:    status 属于 {done, cancel}
//	activate: status 属于 {done, closed, cancel} → doing
//
// 与缺陷不同，任务有真正的 pause/restart 往返，因为「暂停」在排期管理里
// 是常态而非异常。
var TaskMachine = &Machine{
	ObjectType: model.ObjectTypeTask,
	ErrorCode:  apperr.TaskStatusInvalid,
	Statuses: []string{
		model.TaskStatusWait,
		model.TaskStatusDoing,
		model.TaskStatusDone,
		model.TaskStatusPause,
		model.TaskStatusCancel,
		model.TaskStatusClosed,
	},
	StatusLabels: map[string]string{
		model.TaskStatusWait:   "未开始",
		model.TaskStatusDoing:  "进行中",
		model.TaskStatusDone:   "已完成",
		model.TaskStatusPause:  "已暂停",
		model.TaskStatusCancel: "已取消",
		model.TaskStatusClosed: "已关闭",
	},
	Transitions: map[string]Transition{
		"create": {
			Action: "create", To: model.TaskStatusWait,
			Perm: string(acl.PrivTaskCreate), Label: "新建任务",
			Kind: KindCreate, LogAction: model.ActionOpened,
		},
		"edit": {
			Action: "edit",
			Perm:   string(acl.PrivTaskEdit), Label: "编辑",
			Kind: KindEdit, LogAction: model.ActionEdited,
		},
		"assign": {
			Action: "assign",
			From:   []string{model.TaskStatusWait, model.TaskStatusDoing, model.TaskStatusPause, model.TaskStatusDone},
			Perm:   string(acl.PrivTaskAssign), Label: "指派",
			Kind: KindAssign, LogAction: model.ActionAssigned,
		},
		"start": {
			Action: "start",
			From:   []string{model.TaskStatusWait, model.TaskStatusPause, model.TaskStatusDoing},
			To:     model.TaskStatusDoing,
			Perm:   string(acl.PrivTaskStart), Label: "开始",
			Kind: KindStatus, LogAction: model.ActionStarted,
		},
		"pause": {
			Action: "pause",
			From:   []string{model.TaskStatusDoing},
			To:     model.TaskStatusPause,
			Perm:   string(acl.PrivTaskStart), Label: "暂停",
			Kind: KindStatus, LogAction: model.ActionPaused,
		},
		"restart": {
			Action: "restart",
			From:   []string{model.TaskStatusPause},
			To:     model.TaskStatusDoing,
			Perm:   string(acl.PrivTaskStart), Label: "继续",
			Kind: KindStatus, LogAction: model.ActionRestarted,
		},
		"finish": {
			Action: "finish",
			// 未开始、进行中、已暂停都可直接完成（剩余工时归零即算做完）。
			From: []string{model.TaskStatusWait, model.TaskStatusDoing, model.TaskStatusPause},
			To:   model.TaskStatusDone,
			Perm: string(acl.PrivTaskFinish), Label: "完成",
			Kind: KindStatus, LogAction: model.ActionFinished,
		},
		"cancel": {
			Action: "cancel",
			From:   []string{model.TaskStatusWait, model.TaskStatusDoing, model.TaskStatusPause},
			To:     model.TaskStatusCancel,
			Perm:   string(acl.PrivTaskCancel), Label: "取消",
			Kind: KindStatus, LogAction: model.ActionCanceled,
			Danger: true,
		},
		"close": {
			Action: "close",
			From:   []string{model.TaskStatusDone, model.TaskStatusCancel},
			To:     model.TaskStatusClosed,
			Perm:   string(acl.PrivTaskClose), Label: "关闭",
			Kind: KindStatus, LogAction: model.ActionClosed,
		},
		"activate": {
			Action: "activate",
			From:   []string{model.TaskStatusDone, model.TaskStatusClosed, model.TaskStatusCancel},
			To:     model.TaskStatusDoing,
			Perm:   string(acl.PrivTaskStart), Label: "重新激活",
			Kind: KindStatus, LogAction: model.ActionActivated,
		},
		"recordeffort": {
			Action: "recordeffort",
			// 只有进行中的任务能记工时 —— 未开始就报工时说明流程走错了。
			From: []string{model.TaskStatusDoing},
			Perm: string(acl.PrivEffortRecord), Label: "登记工时",
			Kind: KindComment, LogAction: model.ActionRecordWork,
		},
	},
}

// StoryMachine 需求状态机。
//
// 守卫条件一览（写这份表时拿禅道 module/story/model.php 核过一遍，
// 免得漏掉它的边界情形；表本身与执行方式是自己的）：
//
//	submitreview: status ∈ {draft, changing}       → reviewing
//	recall:       status ∈ {reviewing, changing}   → changing（若已变更）否则 draft
//	review:       status == reviewing 且有评审人
//	              pass    → active
//	              clarify → draft / changing
//	              revert  → active（版本 −1，删掉最新 spec/review）
//	              reject  → closed
//	change:       status == active                 → changing（版本 +1）
//	close:        status != closed                 → closed
//	activate:     status == closed                 → 恢复到关闭前的状态
//
// 其中 review 的分支最复杂：四种评审结论各自导向不同状态，
// 所以这里把四种结论建模成四个独立动作，而不是一个带参数的 review。
// 好处是权限点、按钮文案、可用性判定都能按结论细分
// （比如「拒绝」比「通过」更敏感，可以配给不同角色）；
// 代价是 service 侧要按动作名分派，但那本来就是它该做的事。
var StoryMachine = &Machine{
	ObjectType: model.ObjectTypeStory,
	ErrorCode:  apperr.StoryStatusInvalid,
	Statuses: []string{
		model.StoryStatusDraft,
		model.StoryStatusReviewing,
		model.StoryStatusActive,
		model.StoryStatusChanging,
		model.StoryStatusClosed,
	},
	StatusLabels: map[string]string{
		model.StoryStatusDraft:     "草稿",
		model.StoryStatusReviewing: "评审中",
		model.StoryStatusActive:    "激活",
		model.StoryStatusChanging:  "变更中",
		model.StoryStatusClosed:    "已关闭",
	},
	Transitions: map[string]Transition{
		"create": {
			Action: "create", To: model.StoryStatusDraft,
			Perm: string(acl.PrivStoryCreate), Label: "新建需求",
			Kind: KindCreate, LogAction: model.ActionOpened,
		},
		"edit": {
			Action: "edit",
			// 已关闭的需求不可再改；评审中的要先撤回才能改。
			From: []string{model.StoryStatusDraft, model.StoryStatusActive, model.StoryStatusChanging},
			Perm: string(acl.PrivStoryEdit), Label: "编辑",
			Kind: KindEdit, LogAction: model.ActionEdited,
		},
		"assign": {
			Action: "assign",
			From:   []string{model.StoryStatusDraft, model.StoryStatusActive, model.StoryStatusChanging},
			Perm:   string(acl.PrivStoryAssign), Label: "指派",
			Kind: KindAssign, LogAction: model.ActionAssigned,
		},
		"submitreview": {
			Action: "submitreview",
			From:   []string{model.StoryStatusDraft, model.StoryStatusChanging},
			To:     model.StoryStatusReviewing,
			Perm:   string(acl.PrivStoryReview), Label: "提交评审",
			Kind: KindStatus, LogAction: model.ActionSubmitReview,
		},
		"recall": {
			Action: "recall",
			From:   []string{model.StoryStatusReviewing, model.StoryStatusChanging},
			// 目标状态取决于是否已有变更，由 service 决定写 draft 还是 changing，
			// 所以这里留空。禅道的 getActivateStatus 也是同样的动态判断。
			Perm: string(acl.PrivStoryReview), Label: "撤销评审",
			Kind: KindStatus, LogAction: model.ActionRecalled,
		},
		"pass": {
			Action: "pass",
			From:   []string{model.StoryStatusReviewing},
			To:     model.StoryStatusActive,
			Perm:   string(acl.PrivStoryReview), Label: "评审通过",
			Kind: KindStatus, LogAction: model.ActionReviewed,
		},
		"clarify": {
			Action: "clarify",
			From:   []string{model.StoryStatusReviewing},
			// 回到 draft 还是 changing 同样取决于是否已有变更。
			Perm: string(acl.PrivStoryReview), Label: "有待明确",
			Kind: KindStatus, LogAction: model.ActionReviewed,
		},
		"revert": {
			Action: "revert",
			From:   []string{model.StoryStatusReviewing},
			To:     model.StoryStatusActive,
			Perm:   string(acl.PrivStoryReview), Label: "撤销变更",
			Kind: KindStatus, LogAction: model.ActionReviewed,
			Danger: true,
		},
		"reject": {
			Action: "reject",
			From:   []string{model.StoryStatusReviewing},
			To:     model.StoryStatusClosed,
			Perm:   string(acl.PrivStoryReview), Label: "评审拒绝",
			Kind: KindStatus, LogAction: model.ActionReviewed,
			Danger: true,
		},
		"change": {
			Action: "change",
			From:   []string{model.StoryStatusActive},
			To:     model.StoryStatusChanging,
			Perm:   string(acl.PrivStoryChange), Label: "变更",
			Kind: KindStatus, LogAction: model.ActionEdited,
		},
		"close": {
			Action: "close",
			From: []string{
				model.StoryStatusDraft, model.StoryStatusActive,
				model.StoryStatusChanging, model.StoryStatusReviewing,
			},
			To:   model.StoryStatusClosed,
			Perm: string(acl.PrivStoryClose), Label: "关闭",
			Kind: KindStatus, LogAction: model.ActionClosed,
		},
		"activate": {
			Action: "activate",
			From:   []string{model.StoryStatusClosed},
			// 目标是恢复关闭前的状态，由 service 从操作历史里读取，故留空。
			Perm: string(acl.PrivStoryClose), Label: "激活",
			Kind: KindStatus, LogAction: model.ActionActivated,
		},
	},
}

// ProjectMachine 项目状态机。
//
// 守卫条件一览（写这份表时拿禅道 module/project/model.php 核过一遍，
// 免得漏掉它的边界情形；表本身与执行方式是自己的）：
//
//	start:    status ∈ {wait, suspended}
//	suspend:  status ∈ {wait, doing}
//	close:    status != closed
var ProjectMachine = &Machine{
	ObjectType: model.ObjectTypeProject,
	ErrorCode:  apperr.ProjectStatusInvalid,
	Statuses: []string{
		model.ProjectStatusWait,
		model.ProjectStatusDoing,
		model.ProjectStatusSuspended,
		model.ProjectStatusClosed,
	},
	StatusLabels: map[string]string{
		model.ProjectStatusWait:      "未开始",
		model.ProjectStatusDoing:     "进行中",
		model.ProjectStatusSuspended: "已挂起",
		model.ProjectStatusClosed:    "已关闭",
	},
	Transitions: map[string]Transition{
		"create": {
			Action: "create", To: model.ProjectStatusWait,
			Perm: string(acl.PrivProjectCreate), Label: "新建项目",
			Kind: KindCreate, LogAction: model.ActionOpened,
		},
		"edit": {
			Action: "edit",
			From:   []string{model.ProjectStatusWait, model.ProjectStatusDoing, model.ProjectStatusSuspended},
			Perm:   string(acl.PrivProjectEdit), Label: "编辑",
			Kind: KindEdit, LogAction: model.ActionEdited,
		},
		"start": {
			Action: "start",
			From:   []string{model.ProjectStatusWait, model.ProjectStatusSuspended},
			To:     model.ProjectStatusDoing,
			Perm:   string(acl.PrivProjectStart), Label: "开始",
			Kind: KindStatus, LogAction: model.ActionStarted,
		},
		"suspend": {
			Action: "suspend",
			From:   []string{model.ProjectStatusWait, model.ProjectStatusDoing},
			To:     model.ProjectStatusSuspended,
			Perm:   string(acl.PrivProjectStart), Label: "挂起",
			Kind: KindStatus, LogAction: model.ActionPaused,
		},
		"close": {
			Action: "close",
			From:   []string{model.ProjectStatusWait, model.ProjectStatusDoing, model.ProjectStatusSuspended},
			To:     model.ProjectStatusClosed,
			Perm:   string(acl.PrivProjectClose), Label: "关闭",
			Kind: KindStatus, LogAction: model.ActionClosed,
			Danger: true,
		},
		"activate": {
			Action: "activate",
			From:   []string{model.ProjectStatusClosed},
			To:     model.ProjectStatusDoing,
			Perm:   string(acl.PrivProjectStart), Label: "激活",
			Kind: KindStatus, LogAction: model.ActionActivated,
		},
	},
}

// ExecutionMachine 迭代状态机。
//
// 迭代的动作集比项目小：没有 suspend 的往返（禅道用 putoff 表达延期，
// 但那本质上是改日期而非改状态，因此这里把它归入 edit）。
var ExecutionMachine = &Machine{
	ObjectType: model.ObjectTypeExecution,
	ErrorCode:  apperr.ExecutionStatusInvalid,
	Statuses: []string{
		model.ExecutionStatusWait,
		model.ExecutionStatusDoing,
		model.ExecutionStatusSuspended,
		model.ExecutionStatusClosed,
	},
	StatusLabels: map[string]string{
		model.ExecutionStatusWait:      "未开始",
		model.ExecutionStatusDoing:     "进行中",
		model.ExecutionStatusSuspended: "已挂起",
		model.ExecutionStatusClosed:    "已关闭",
	},
	Transitions: map[string]Transition{
		"create": {
			Action: "create", To: model.ExecutionStatusWait,
			Perm: string(acl.PrivExecutionCreate), Label: "新建迭代",
			Kind: KindCreate, LogAction: model.ActionOpened,
		},
		"edit": {
			Action: "edit",
			From:   []string{model.ExecutionStatusWait, model.ExecutionStatusDoing, model.ExecutionStatusSuspended},
			Perm:   string(acl.PrivExecutionEdit), Label: "编辑",
			Kind: KindEdit, LogAction: model.ActionEdited,
		},
		"start": {
			Action: "start",
			From:   []string{model.ExecutionStatusWait, model.ExecutionStatusSuspended},
			To:     model.ExecutionStatusDoing,
			Perm:   string(acl.PrivExecutionEdit), Label: "开始",
			Kind: KindStatus, LogAction: model.ActionStarted,
		},
		"suspend": {
			Action: "suspend",
			From:   []string{model.ExecutionStatusDoing},
			To:     model.ExecutionStatusSuspended,
			Perm:   string(acl.PrivExecutionEdit), Label: "挂起",
			Kind: KindStatus, LogAction: model.ActionPaused,
		},
		"close": {
			Action: "close",
			From:   []string{model.ExecutionStatusWait, model.ExecutionStatusDoing, model.ExecutionStatusSuspended},
			To:     model.ExecutionStatusClosed,
			Perm:   string(acl.PrivExecutionEdit), Label: "关闭",
			Kind: KindStatus, LogAction: model.ActionClosed,
			Danger: true,
		},
		"activate": {
			Action: "activate",
			From:   []string{model.ExecutionStatusClosed},
			To:     model.ExecutionStatusDoing,
			Perm:   string(acl.PrivExecutionEdit), Label: "激活",
			Kind: KindStatus, LogAction: model.ActionActivated,
		},
	},
}

// Machines 按对象类型索引全部状态机，供通用接口（如 /actions）查表。
var Machines = map[string]*Machine{
	model.ObjectTypeStory:     StoryMachine,
	model.ObjectTypeTask:      TaskMachine,
	model.ObjectTypeBug:       BugMachine,
	model.ObjectTypeProject:   ProjectMachine,
	model.ObjectTypeExecution: ExecutionMachine,
}

// Get 按对象类型取状态机。
func Get(objectType string) (*Machine, bool) {
	m, ok := Machines[objectType]
	return m, ok
}
