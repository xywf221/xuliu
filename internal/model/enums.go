package model

// 本文件的枚举取值全部对照禅道 22.6 源码核对过：
//
//	db/standard/zentao22.6.sql        表与列定义
//	module/story/lang/zh-cn.php       需求状态/阶段/类别/来源字典
//	module/bug/lang/zh-cn.php         缺陷状态/解决方案/类型/系统/浏览器字典
//	module/task/lang/zh-cn.php        任务状态/类型/多人模式字典
//	module/project/lang/zh-cn.php     项目模型/状态/访问控制字典
//
// 取值本身是字符串（存 varchar），这样跨库一致、加值也不需要改表结构。
// 中文含义写在注释里，供前端字典与文案使用。
//
// 改动这里的值等同破坏性变更：库里可能已有存量数据。

// ---------------------------------------------------------------------------
// 需求
// ---------------------------------------------------------------------------

// StoryStatus 需求状态。
const (
	StoryStatusDraft     = "draft"     // 草稿
	StoryStatusReviewing = "reviewing" // 评审中
	StoryStatusActive    = "active"    // 激活（已通过评审，可被项目引用）
	StoryStatusChanging  = "changing"  // 变更中
	StoryStatusClosed    = "closed"    // 已关闭
)

// StoryStages 需求阶段，按推进顺序排列。
var StoryStages = []string{
	"wait",       // 未开始
	"planned",    // 已计划
	"projected",  // 已立项
	"designing",  // 设计中
	"designed",   // 设计完毕
	"developing", // 研发中
	"developed",  // 研发完毕
	"testing",    // 测试中
	"tested",     // 测试完毕
	"verified",   // 已验收
	"rejected",   // 验收失败
	"delivering", // 交付中
	"delivered",  // 已交付
	"released",   // 已发布
	"closed",     // 已关闭
}

// StoryStageLabels 是需求阶段的中文名。
//
// 和 StoryStages 分开：那个是顺序（前端画进度条用），
// 这个是展示文案。两者都需要，但用途不同。
var StoryStageLabels = map[string]string{
	"wait":       "未开始",
	"planned":    "已计划",
	"projected":  "已立项",
	"designing":  "设计中",
	"designed":   "设计完毕",
	"developing": "研发中",
	"developed":  "研发完毕",
	"testing":    "测试中",
	"tested":     "测试完毕",
	"verified":   "已验收",
	"rejected":   "验收失败",
	"delivering": "交付中",
	"delivered":  "已交付",
	"released":   "已发布",
	"closed":     "已关闭",
}

// 需求阶段的具名常量（供状态机与默认值使用）。
const (
	StoryStageWait       = "wait"
	StoryStagePlanned    = "planned"
	StoryStageProjected  = "projected"
	StoryStageDeveloping = "developing"
	StoryStageTesting    = "testing"
	StoryStageVerified   = "verified"
	StoryStageReleased   = "released"
	StoryStageClosed     = "closed"
)

// StoryType 需求类型。禅道默认只启用 story，其余按配置开启。
const (
	StoryTypeStory       = "story"       // 研发需求 SR
	StoryTypeRequirement = "requirement" // 用户需求 UR
	StoryTypeEpic        = "epic"        // 业务需求 ER
)

// StoryCategory 需求类别。
const (
	StoryCategoryFeature     = "feature"     // 功能
	StoryCategoryInterface   = "interface"   // 接口
	StoryCategoryPerformance = "performance" // 性能
	StoryCategorySafe        = "safe"        // 安全
	StoryCategoryExperience  = "experience"  // 体验
	StoryCategoryImprove     = "improve"     // 改进
	StoryCategoryOther       = "other"       // 其他
)

// StorySource 需求来源。
const (
	StorySourceCustomer   = "customer"   // 客户
	StorySourceUser       = "user"       // 用户
	StorySourcePO         = "po"         // 产品经理
	StorySourceMarket     = "market"     // 市场
	StorySourceService    = "service"    // 客服
	StorySourceOperation  = "operation"  // 运营
	StorySourceSupport    = "support"    // 技术支持
	StorySourceCompetitor = "competitor" // 竞争对手
	StorySourcePartner    = "partner"    // 合作伙伴
	StorySourceDev        = "dev"        // 开发人员
	StorySourceTester     = "tester"     // 测试人员
	StorySourceBug        = "bug"        // 由缺陷转入
	StorySourceForum      = "forum"      // 论坛
	StorySourceOther      = "other"      // 其他
)

// StoryClosedReason 需求关闭原因。
const (
	StoryCloseDone       = "done"       // 已完成
	StoryCloseSubdivided = "subdivided" // 已拆分
	StoryCloseDuplicate  = "duplicate"  // 重复
	StoryClosePostponed  = "postponed"  // 延期
	StoryCloseWillNotDo  = "willnotdo"  // 不做
	StoryCloseCancel     = "cancel"     // 已取消
	StoryCloseByDesign   = "bydesign"   // 设计如此
)

// StoryReviewResult 需求评审结论。
const (
	ReviewPass    = "pass"    // 确认通过
	ReviewRevert  = "revert"  // 撤销变更
	ReviewClarify = "clarify" // 有待明确
	ReviewReject  = "reject"  // 拒绝
)

// ---------------------------------------------------------------------------
// 缺陷
// ---------------------------------------------------------------------------

// BugStatus 缺陷状态。注意：重新激活复用 activate 动作，没有独立的 reopen。
const (
	BugStatusActive   = "active"   // 激活
	BugStatusResolved = "resolved" // 已解决
	BugStatusClosed   = "closed"   // 已关闭
)

// BugResolution 解决方案。空值表示尚未解决。
const (
	BugResolutionByDesign   = "bydesign"   // 设计如此
	BugResolutionDuplicate  = "duplicate"  // 重复缺陷
	BugResolutionExternal   = "external"   // 外部原因
	BugResolutionFixed      = "fixed"      // 已修复
	BugResolutionNotRepro   = "notrepro"   // 无法重现
	BugResolutionPostponed  = "postponed"  // 延期处理
	BugResolutionWillNotFix = "willnotfix" // 不予解决
	BugResolutionToStory    = "tostory"    // 转为需求
)

// BugResolutions 是全部合法的解决方案取值。
//
// 单独列一份切片（而不是只写常量）：service 要做「传进来的值是不是合法」
// 的校验，前端也要拿它渲染下拉框。三处各写一遍字符串数组必然漂移
// —— 加了新方案却漏了校验，等于给库里开了个能写任意值的口子。
// 注意不含空串：空表示「尚未解决」，不是一种解决方案。
var BugResolutions = []string{
	BugResolutionByDesign,
	BugResolutionDuplicate,
	BugResolutionExternal,
	BugResolutionFixed,
	BugResolutionNotRepro,
	BugResolutionPostponed,
	BugResolutionWillNotFix,
	BugResolutionToStory,
}

// BugTypes 是全部缺陷类型，同样供校验与下拉框共用。
var BugTypes = []string{
	BugTypeCodeError,
	BugTypeConfig,
	BugTypeInstall,
	BugTypeSecurity,
	BugTypePerformance,
	BugTypeStandard,
	BugTypeAutomation,
	BugTypeDesignDefect,
	BugTypeCodeImprovement,
	BugTypeOthers,
}

// BugType 缺陷类型。
const (
	BugTypeCodeError       = "codeerror"       // 代码错误
	BugTypeConfig          = "config"          // 配置相关
	BugTypeInstall         = "install"         // 安装部署
	BugTypeSecurity        = "security"        // 安全相关
	BugTypePerformance     = "performance"     // 性能问题
	BugTypeStandard        = "standard"        // 标准规范
	BugTypeAutomation      = "automation"      // 测试脚本
	BugTypeDesignDefect    = "designdefect"    // 设计缺陷
	BugTypeCodeImprovement = "codeimprovement" // 代码改进
	BugTypeOthers          = "others"          // 其他
)

// BugOS 操作系统。禅道的取值很细，这里保持原样以便与禅道数据互通。
const (
	BugOSAll      = "all"
	BugOSWindows  = "windows"
	BugOSWin11    = "win11"
	BugOSWin10    = "win10"
	BugOSWin8     = "win8"
	BugOSWin7     = "win7"
	BugOSWinXP    = "winxp"
	BugOSMac      = "osx"
	BugOSAndroid  = "android"
	BugOSIOS      = "ios"
	BugOSLinux    = "linux"
	BugOSUbuntu   = "ubuntu"
	BugOSChromeOS = "chromeos"
	BugOSFedora   = "fedora"
	BugOSUnix     = "unix"
	BugOSOthers   = "others"
)

// BugBrowser 浏览器。
const (
	BugBrowserAll     = "all"
	BugBrowserChrome  = "chrome"
	BugBrowserEdge    = "edge"
	BugBrowserIE      = "ie"
	BugBrowserIE11    = "ie11"
	BugBrowserIE10    = "ie10"
	BugBrowserIE9     = "ie9"
	BugBrowserIE8     = "ie8"
	BugBrowserFirefox = "firefox"
	BugBrowserOpera   = "opera"
	BugBrowserSafari  = "safari"
	BugBrowser360     = "360"
	BugBrowserQQ      = "qq"
	BugBrowserOther   = "other"
)

// 缺陷确认状态。
const (
	BugUnconfirmed = 0 // 未确认
	BugConfirmed   = 1 // 已确认
)

// ---------------------------------------------------------------------------
// 任务
// ---------------------------------------------------------------------------

// TaskStatus 任务状态。
const (
	TaskStatusWait   = "wait"   // 未开始
	TaskStatusDoing  = "doing"  // 进行中
	TaskStatusDone   = "done"   // 已完成
	TaskStatusPause  = "pause"  // 已暂停
	TaskStatusCancel = "cancel" // 已取消
	TaskStatusClosed = "closed" // 已关闭
)

// TaskType 任务类型。
const (
	TaskTypeDesign  = "design"  // 设计
	TaskTypeDevel   = "devel"   // 开发
	TaskTypeRequest = "request" // 需求
	TaskTypeTest    = "test"    // 测试
	TaskTypeStudy   = "study"   // 研究
	TaskTypeDiscuss = "discuss" // 讨论
	TaskTypeUI      = "ui"      // 界面
	TaskTypeAffair  = "affair"  // 事务
	TaskTypeMisc    = "misc"    // 其他
)

// TaskMode 多人任务模式。空值等同单人或 multi。
const (
	TaskModeSingle = "single" // 单人任务
	TaskModeLinear = "linear" // 多人串行
	TaskModeMulti  = "multi"  // 多人并行
)

// TaskClosedReason 任务关闭原因。
const (
	TaskCloseDone   = "done"   // 已完成
	TaskCloseCancel = "cancel" // 已取消
)

// TaskUnfinishedStatuses 是「还没收尾」的任务状态集合。
//
// 判据不是简单的「不等于已完成」：已取消和已关闭同样是收尾状态 ——
// 一条砍掉的任务不该继续阻塞需求的删除，也不该出现在燃尽图的剩余里。
var TaskUnfinishedStatuses = []string{TaskStatusWait, TaskStatusDoing, TaskStatusPause}

// StoryPlannableStatuses 是「可以排进项目/迭代」的需求状态。
//
// 只有评审通过的需求（active）和正在变更中的需求（changing）能被排期：
// 草稿与评审中的需求随时可能被改掉，排进项目等于给下游一个会变的承诺；
// 已关闭的需求则是做完了，不需要再排。
var StoryPlannableStatuses = []string{StoryStatusActive, StoryStatusChanging}

// ---------------------------------------------------------------------------
// 项目 / 迭代
// ---------------------------------------------------------------------------

// ProjectModel 项目管理模型。
const (
	ProjectModelScrum         = "scrum"
	ProjectModelWaterfall     = "waterfall"
	ProjectModelKanban        = "kanban"
	ProjectModelAgilePlus     = "agileplus"
	ProjectModelWaterfallPlus = "waterfallplus"
)

// ProjectStatus 项目状态。
const (
	ProjectStatusWait      = "wait"      // 未开始
	ProjectStatusDoing     = "doing"     // 进行中
	ProjectStatusSuspended = "suspended" // 已挂起
	ProjectStatusClosed    = "closed"    // 已关闭
)

// ExecutionType 迭代类型。注意：我们把迭代拆成了独立表，
// 不再像禅道那样与项目共用一张表靠列区分。
const (
	ExecutionTypeSprint = "sprint" // 迭代
	ExecutionTypeStage  = "stage"  // 阶段
	ExecutionTypeKanban = "kanban" // 看板
)

// ExecutionStatus 迭代状态。
const (
	ExecutionStatusWait      = "wait"      // 未开始
	ExecutionStatusDoing     = "doing"     // 进行中
	ExecutionStatusSuspended = "suspended" // 已挂起
	ExecutionStatusClosed    = "closed"    // 已关闭
)

// 项目/迭代成员角色。
const (
	TeamRolePO  = "PO"  // 产品经理
	TeamRolePM  = "PM"  // 项目经理
	TeamRoleQD  = "QD"  // 测试负责人
	TeamRoleRD  = "RD"  // 发布负责人
	TeamRoleDev = "dev" // 研发
	TeamRoleQA  = "qa"  // 测试
)

// ---------------------------------------------------------------------------
// 产品
// ---------------------------------------------------------------------------

// ProductStatus 产品状态。
const (
	ProductStatusNormal = "normal" // 正常
	ProductStatusClosed = "closed" // 已关闭
)

// ProductType 产品类型。
const (
	ProductTypeNormal   = "normal"   // 正常产品
	ProductTypeBranch   = "branch"   // 分支产品
	ProductTypePlatform = "platform" // 平台产品
)

// PlanStatus 产品计划状态。
const (
	PlanStatusWait   = "wait"   // 未开始
	PlanStatusDoing  = "doing"  // 进行中
	PlanStatusDone   = "done"   // 已完成
	PlanStatusClosed = "closed" // 已关闭
)

// ---------------------------------------------------------------------------
// 模块树
// ---------------------------------------------------------------------------

// ModuleType 模块树类型。同一棵树按用途分开，需求/任务/缺陷各用一套。
const (
	ModuleTypeStory = "story" // 需求模块
	ModuleTypeTask  = "task"  // 任务模块
	ModuleTypeBug   = "bug"   // 缺陷模块
	ModuleTypeCase  = "case"  // 用例模块（二阶段）
)

// ---------------------------------------------------------------------------
// 用户
// ---------------------------------------------------------------------------

// UserType 用户类型。
const (
	UserTypeInside  = "inside"  // 内部用户
	UserTypeOutside = "outside" // 外部用户
	UserTypeGuest   = "guest"   // 访客
)

// UserJobRole 职位。对应禅道的 roleList。
const (
	JobRoleDev    = "dev"    // 研发
	JobRoleQA     = "qa"     // 测试
	JobRolePM     = "pm"     // 项目经理
	JobRolePO     = "po"     // 产品经理
	JobRoleTD     = "td"     // 研发主管
	JobRolePD     = "pd"     // 产品主管
	JobRoleQD     = "qd"     // 测试主管
	JobRoleTop    = "top"    // 高层管理
	JobRoleOthers = "others" // 其他
)

// Gender 性别。
const (
	GenderMale   = "m"
	GenderFemale = "f"
)

// ---------------------------------------------------------------------------
// 测试用例
// ---------------------------------------------------------------------------

// TestCaseStage 用例的评审生命周期。走状态机（见 workflow.TestCaseMachine）。
const (
	TestCaseStageWait      = "wait"      // 待评审
	TestCaseStageReviewing = "reviewing" // 评审中
	TestCaseStageConfirmed = "confirmed" // 评审通过，可纳入测试单
	TestCaseStageRejected  = "rejected"  // 评审驳回
)

// TestCaseStatus 用例的可执行性。与 Stage 正交：
// 评审通过但环境没好，就是 confirmed + blocked。
const (
	TestCaseStatusNormal      = "normal"      // 正常可执行
	TestCaseStatusBlocked     = "blocked"     // 被阻塞，暂时跑不了
	TestCaseStatusInvestigate = "investigate" // 结果存疑，待查
)

// TestCaseType 用例类型。借鉴禅道的分类，但只留下我们真会用来筛选的几类：
// 禅道另有 experience / designChange / unittest 等，实际数据里几乎为空，
// 放进枚举只会让筛选列表变长而没人用。
const (
	TestCaseTypeFeature     = "feature"     // 功能
	TestCaseTypeInterface   = "interface"   // 接口
	TestCaseTypePerformance = "performance" // 性能
	TestCaseTypeConfig      = "config"      // 配置
	TestCaseTypeSecurity    = "security"    // 安全
	TestCaseTypeOthers      = "others"      // 其他
)

// TestCaseTypes 是全部用例类型，供服务层校验与前端下拉框共用。
//
// 服务层再校验一次是有意的：DTO 上的 oneof 只挡 HTTP 那条路，
// 而从别的入口（导入、将来的批量生成）调用服务时，没登记的类型
// 会一路写进库，最后在前端显示成一个空白标签。
var TestCaseTypes = []string{
	TestCaseTypeFeature,
	TestCaseTypeInterface,
	TestCaseTypePerformance,
	TestCaseTypeConfig,
	TestCaseTypeSecurity,
	TestCaseTypeOthers,
}

// TestCaseStatuses 是全部可执行性取值。
var TestCaseStatuses = []string{
	TestCaseStatusNormal,
	TestCaseStatusBlocked,
	TestCaseStatusInvestigate,
}

// ---------------------------------------------------------------------------
// 测试单与执行记录
// ---------------------------------------------------------------------------

// TestTaskStatus 测试单的流程位置。
//
// 注意结论不在这个维度里（见 TestTaskResult）：「测完了，结论是失败」
// 是 status=closed + result=fail 两列，而不是把 pass/fail 当成状态。
const (
	TestTaskStatusWait    = "wait"    // 未开始
	TestTaskStatusDoing   = "doing"   // 进行中
	TestTaskStatusBlocked = "blocked" // 被阻塞（环境/依赖没好）
	TestTaskStatusClosed  = "closed"  // 已结单，结论见 Result
)

// TestTaskResult 是结单时给出的结论。空串表示还没结单。
const (
	TestTaskResultPass    = "pass"    // 通过
	TestTaskResultFail    = "fail"    // 不通过
	TestTaskResultBlocked = "blocked" // 因故未能测完
)

var TestTaskResults = []string{
	TestTaskResultPass,
	TestTaskResultFail,
	TestTaskResultBlocked,
}

// TestRunResult 是一次执行的结论。
//
// 与结单结论用同一组字面量（pass/fail/blocked）：语义本来就是同一件事，
// 分成两套值只会让「用例失败」和「测试单失败」在统计里对不上。
const (
	TestRunResultPass    = "pass"
	TestRunResultFail    = "fail"
	TestRunResultBlocked = "blocked"
)

var TestRunResults = []string{
	TestRunResultPass,
	TestRunResultFail,
	TestRunResultBlocked,
}
