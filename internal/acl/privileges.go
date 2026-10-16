// Package acl 实现权限判定。
//
// 权限点是 "module-method" 形式的字符串（如 "bug-resolve"）。沿用这个命名
// 是为了跟既有的禅道文档与老用户的说法对得上，机制本身是我们自己的：
// 权限组持有权限点集合，用户通过所属组获得权限，判定就是集合包含。
//
// 没有引入 Casbin：规则简单到「集合包含 + 一个超管短路 + 数据级白名单」，
// 引入策略引擎只会多一层需要学习和调试的抽象，而不会让规则更清晰。
package acl

import "sort"

// Priv 是权限点标识，形如 "bug-resolve"。
type Priv string

// 权限点目录。命名沿用禅道的 module-method，便于与禅道文档对照。
const (
	// ---- 用户与组织 ----
	PrivUserBrowse    Priv = "user-browse"
	PrivUserCreate    Priv = "user-create"
	PrivUserEdit      Priv = "user-edit"
	PrivUserDelete    Priv = "user-delete"
	PrivDeptBrowse    Priv = "dept-browse"
	PrivDeptManage    Priv = "dept-manage"
	PrivGroupBrowse   Priv = "group-browse"
	PrivGroupManage   Priv = "group-manage"
	PrivCompanyManage Priv = "company-manage"

	// ---- 产品 ----
	PrivProductBrowse     Priv = "product-browse"
	PrivProductCreate     Priv = "product-create"
	PrivProductEdit       Priv = "product-edit"
	PrivProductDelete     Priv = "product-delete"
	PrivProductlineManage Priv = "productline-manage"
	PrivModuleManage      Priv = "module-manage"
	PrivPlanManage        Priv = "plan-manage"
	PrivReleaseManage     Priv = "release-manage"
	PrivBuildManage       Priv = "build-manage"

	// ---- 需求 ----
	PrivStoryBrowse Priv = "story-browse"
	PrivStoryCreate Priv = "story-create"
	PrivStoryEdit   Priv = "story-edit"
	PrivStoryDelete Priv = "story-delete"
	PrivStoryChange Priv = "story-change"
	PrivStoryReview Priv = "story-review"
	PrivStoryClose  Priv = "story-close"
	PrivStoryAssign Priv = "story-assign"

	// ---- 项目 / 迭代 ----
	PrivProjectBrowse   Priv = "project-browse"
	PrivProjectCreate   Priv = "project-create"
	PrivProjectEdit     Priv = "project-edit"
	PrivProjectDelete   Priv = "project-delete"
	PrivProjectStart    Priv = "project-start"
	PrivProjectClose    Priv = "project-close"
	PrivExecutionCreate Priv = "execution-create"
	PrivExecutionEdit   Priv = "execution-edit"
	PrivExecutionDelete Priv = "execution-delete"
	PrivTeamManage      Priv = "team-manage"

	// ---- 任务 ----
	PrivTaskBrowse   Priv = "task-browse"
	PrivTaskCreate   Priv = "task-create"
	PrivTaskEdit     Priv = "task-edit"
	PrivTaskDelete   Priv = "task-delete"
	PrivTaskAssign   Priv = "task-assign"
	PrivTaskStart    Priv = "task-start"
	PrivTaskFinish   Priv = "task-finish"
	PrivTaskClose    Priv = "task-close"
	PrivTaskCancel   Priv = "task-cancel"
	PrivEffortRecord Priv = "effort-record"

	// ---- 缺陷 ----
	PrivBugBrowse   Priv = "bug-browse"
	PrivBugCreate   Priv = "bug-create"
	PrivBugEdit     Priv = "bug-edit"
	PrivBugDelete   Priv = "bug-delete"
	PrivBugAssign   Priv = "bug-assign"
	PrivBugConfirm  Priv = "bug-confirm"
	PrivBugResolve  Priv = "bug-resolve"
	PrivBugClose    Priv = "bug-close"
	PrivBugActivate Priv = "bug-activate"
	PrivBugToStory  Priv = "bug-tostory"

	// ---- 工作台 ----
	PrivMyWork     Priv = "my-work"
	PrivReport     Priv = "report-browse"
	PrivAdminTools Priv = "admin-tools"
)

// Meta 描述一个权限点，供前端渲染菜单与按钮、供权限组配置界面分组勾选。
type Meta struct {
	Priv   Priv   `json:"priv"`
	Module string `json:"module"` // 所属模块，用于分组展示
	Method string `json:"method"`
	Name   string `json:"name"` // 中文名，前端直接显示
}

// registry 保存全部权限点。用 map 便于 O(1) 校验合法性。
var registry = map[Priv]Meta{}

// moduleNames 是模块的展示名，用于权限配置界面分组标题。
var moduleNames = map[string]string{
	"user":        "用户",
	"dept":        "部门",
	"group":       "权限组",
	"company":     "公司",
	"product":     "产品",
	"productline": "产品线",
	"module":      "模块",
	"plan":        "产品计划",
	"release":     "发布",
	"build":       "构建",
	"story":       "需求",
	"project":     "项目",
	"execution":   "迭代",
	"team":        "团队",
	"task":        "任务",
	"effort":      "工时",
	"bug":         "缺陷",
	"my":          "工作台",
	"report":      "报表",
	"admin":       "系统",
}

// ModuleName 返回模块的展示名，未知模块回退为模块名本身。
func ModuleName(module string) string {
	if n, ok := moduleNames[module]; ok {
		return n
	}
	return module
}

// register 登记权限点。重复登记会 panic —— 宁可在启动时炸掉，
// 也不要让两个地方用同一个字符串表达不同权限。
func register(priv Priv, name string) {
	module, method, ok := split(priv)
	if !ok {
		panic("acl: 权限点命名必须为 module-method 形式：" + string(priv))
	}
	if _, dup := registry[priv]; dup {
		panic("acl: 权限点重复登记：" + string(priv))
	}
	registry[priv] = Meta{Priv: priv, Module: module, Method: method, Name: name}
}

// split 把 "bug-resolve" 拆成 ("bug", "resolve")。
//
// 从第一个 - 处切分：模块名不含 -，而方法名可能含（如 bug-tostory），
// 所以必须按首个分隔符拆，不能按最后一个。
func split(priv Priv) (module, method string, ok bool) {
	s := string(priv)
	for i := 0; i < len(s); i++ {
		if s[i] == '-' {
			if i == 0 || i == len(s)-1 {
				return "", "", false
			}
			return s[:i], s[i+1:], true
		}
	}
	return "", "", false
}

// All 返回全部权限点，按模块与方法排序，供权限组配置界面使用。
func All() []Meta {
	out := make([]Meta, 0, len(registry))
	for _, m := range registry {
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Module != out[j].Module {
			return out[i].Module < out[j].Module
		}
		return out[i].Method < out[j].Method
	})
	return out
}

// IsValid 报告权限点是否已登记。
func IsValid(priv Priv) bool {
	_, ok := registry[priv]
	return ok
}

// FilterValid 过滤出已登记的权限点，并去重。
//
// 用于「保存权限组」时清洗前端提交的权限点列表：前端可能带着过期的
// 权限点（比如我们删掉了某个功能），静默忽略比报错更合适。
func FilterValid(privs []Priv) []Priv {
	seen := make(map[Priv]struct{}, len(privs))
	out := make([]Priv, 0, len(privs))
	for _, p := range privs {
		if !IsValid(p) {
			continue
		}
		if _, dup := seen[p]; dup {
			continue
		}
		seen[p] = struct{}{}
		out = append(out, p)
	}
	return out
}

// ModulePrivs 返回按模块分组的全部权限点，供权限配置界面按组渲染。
func ModulePrivs() []ModuleGroup {
	all := All()
	order := make([]string, 0)
	grouped := make(map[string][]Meta)

	for _, m := range all {
		if _, seen := grouped[m.Module]; !seen {
			order = append(order, m.Module)
		}
		grouped[m.Module] = append(grouped[m.Module], m)
	}

	out := make([]ModuleGroup, 0, len(order))
	for _, mod := range order {
		out = append(out, ModuleGroup{
			Module: mod,
			Name:   ModuleName(mod),
			Privs:  grouped[mod],
		})
	}
	return out
}

// ModuleGroup 是权限点的模块分组。
type ModuleGroup struct {
	Module string `json:"module"`
	Name   string `json:"name"`
	Privs  []Meta `json:"privs"`
}

// 登记全部权限点。文案简洁，因为前端会把它直接渲染成勾选项。
func init() {
	// 用户与组织
	register(PrivUserBrowse, "查看用户")
	register(PrivUserCreate, "新建用户")
	register(PrivUserEdit, "编辑用户")
	register(PrivUserDelete, "删除用户")
	register(PrivDeptBrowse, "查看部门")
	register(PrivDeptManage, "管理部门")
	register(PrivGroupBrowse, "查看权限组")
	register(PrivGroupManage, "管理权限组")
	register(PrivCompanyManage, "管理公司信息")

	// 产品
	register(PrivProductBrowse, "查看产品")
	register(PrivProductCreate, "新建产品")
	register(PrivProductEdit, "编辑产品")
	register(PrivProductDelete, "删除产品")
	register(PrivProductlineManage, "管理产品线")
	register(PrivModuleManage, "管理模块")
	register(PrivPlanManage, "管理产品计划")
	register(PrivReleaseManage, "管理发布")
	register(PrivBuildManage, "管理构建")

	// 需求
	register(PrivStoryBrowse, "查看需求")
	register(PrivStoryCreate, "新建需求")
	register(PrivStoryEdit, "编辑需求")
	register(PrivStoryDelete, "删除需求")
	register(PrivStoryChange, "变更需求")
	register(PrivStoryReview, "评审需求")
	register(PrivStoryClose, "关闭需求")
	register(PrivStoryAssign, "指派需求")

	// 项目 / 迭代
	register(PrivProjectBrowse, "查看项目")
	register(PrivProjectCreate, "新建项目")
	register(PrivProjectEdit, "编辑项目")
	register(PrivProjectDelete, "删除项目")
	register(PrivProjectStart, "开始/挂起项目")
	register(PrivProjectClose, "关闭项目")
	register(PrivExecutionCreate, "新建迭代")
	register(PrivExecutionEdit, "编辑迭代")
	register(PrivExecutionDelete, "删除迭代")
	register(PrivTeamManage, "管理团队")

	// 任务
	register(PrivTaskBrowse, "查看任务")
	register(PrivTaskCreate, "新建任务")
	register(PrivTaskEdit, "编辑任务")
	register(PrivTaskDelete, "删除任务")
	register(PrivTaskAssign, "指派任务")
	register(PrivTaskStart, "开始/暂停任务")
	register(PrivTaskFinish, "完成任务")
	register(PrivTaskClose, "关闭任务")
	register(PrivTaskCancel, "取消任务")
	register(PrivEffortRecord, "登记工时")

	// 缺陷
	register(PrivBugBrowse, "查看缺陷")
	register(PrivBugCreate, "提交缺陷")
	register(PrivBugEdit, "编辑缺陷")
	register(PrivBugDelete, "删除缺陷")
	register(PrivBugAssign, "指派缺陷")
	register(PrivBugConfirm, "确认缺陷")
	register(PrivBugResolve, "解决缺陷")
	register(PrivBugClose, "关闭缺陷")
	register(PrivBugActivate, "重新激活缺陷")
	register(PrivBugToStory, "缺陷转需求")

	// 工作台
	register(PrivMyWork, "我的工作台")
	register(PrivReport, "查看统计报表")
	register(PrivAdminTools, "系统管理工具")
}

// DefaultGroupPrivs 是内置角色的默认权限集合，供初始化种子数据使用。
//
// 按研发团队里最常见的四种分工划分：
//   - 产品经理：产品与需求全流程
//   - 项目经理：项目、迭代、任务与团队
//   - 研发：领任务、报工时、提交缺陷、解决缺陷
//   - 测试：提缺陷、验证关闭、查看需求与任务
//
// 这些只是「新建权限组时的模板」，用户可自行增删。
//
// 四个模板都给上了 my-work：工作台是每个人看自己待办的地方，
// 不该因为角色不同而被关掉 —— 关了之后新账号登进来是一片空白，
// 用户只会以为是系统坏了。
var DefaultGroupPrivs = map[string][]Priv{
	"产品经理": {
		PrivProductBrowse, PrivProductCreate, PrivProductEdit,
		PrivProductlineManage, PrivModuleManage, PrivPlanManage,
		PrivReleaseManage, PrivBuildManage,
		PrivStoryBrowse, PrivStoryCreate, PrivStoryEdit, PrivStoryChange,
		PrivStoryReview, PrivStoryClose, PrivStoryAssign, PrivStoryDelete,
		PrivProjectBrowse, PrivExecutionCreate,
		PrivTaskBrowse, PrivBugBrowse,
		PrivMyWork, PrivReport,
	},
	"项目经理": {
		PrivProductBrowse, PrivPlanManage,
		PrivStoryBrowse, PrivStoryCreate, PrivStoryEdit, PrivStoryClose,
		PrivProjectBrowse, PrivProjectCreate, PrivProjectEdit, PrivProjectStart,
		PrivProjectClose, PrivExecutionCreate, PrivExecutionEdit, PrivTeamManage,
		PrivTaskBrowse, PrivTaskCreate, PrivTaskEdit, PrivTaskAssign,
		PrivTaskStart, PrivTaskFinish, PrivTaskClose, PrivTaskCancel,
		PrivEffortRecord, PrivTaskDelete,
		PrivBugBrowse, PrivBugCreate, PrivBugAssign,
		PrivMyWork, PrivReport,
	},
	"研发": {
		PrivProductBrowse, PrivStoryBrowse, PrivPlanManage,
		PrivProjectBrowse, PrivTaskBrowse, PrivTaskCreate, PrivTaskEdit,
		PrivTaskStart, PrivTaskFinish, PrivEffortRecord,
		PrivBugBrowse, PrivBugCreate, PrivBugEdit, PrivBugResolve,
		PrivMyWork,
	},
	"测试": {
		PrivProductBrowse, PrivStoryBrowse,
		PrivProjectBrowse, PrivExecutionCreate, PrivTaskBrowse, PrivTaskCreate,
		PrivTaskStart, PrivTaskFinish, PrivEffortRecord,
		PrivBugBrowse, PrivBugCreate, PrivBugEdit, PrivBugConfirm,
		PrivBugClose, PrivBugActivate, PrivBugAssign,
		PrivMyWork, PrivReport,
	},
}
