package service

import (
	"context"
	"strconv"

	"github.com/xywf221/xuliu/internal/history"
	"github.com/xywf221/xuliu/internal/model"
	"github.com/xywf221/xuliu/internal/repository"
	"github.com/xywf221/xuliu/internal/workflow"
)

// 本文件提供 history.Labeler 的实现：把字段名与取值翻成中文，
// 使操作历史里存的就是可读文本，历史页不必再反查字典表。
//
// history.Labeler 的接口不带 context（Value(field, raw string) string），
// 这是刻意的约束：它必须是一个纯函数。凡是需要查库的翻译（把用户 id
// 换成姓名）都不能放在这里 —— 那会让每写一条历史都触发若干次查询。
// 这类翻译由各 service 在事务外批量补齐，见 fillUserNames。

// priLabels 优先级的中文名。1 最高，数字越小越急。
var priLabels = map[string]string{
	"0": "未设置",
	"1": "最高",
	"2": "高",
	"3": "中",
	"4": "低",
}

// StoryLabeler 翻译需求相关字段。
type StoryLabeler struct{}

// FieldLabel 返回字段的中文名。
func (StoryLabeler) FieldLabel(field string) string {
	if l, ok := storyFieldLabels[field]; ok {
		return l
	}
	return ""
}

// Value 把原始值翻译为可读文本；无法翻译时原样返回。
func (StoryLabeler) Value(field, raw string) string {
	if raw == "" {
		return ""
	}
	switch field {
	case "Status":
		return workflow.StoryMachine.StatusLabel(raw)
	case "Stage":
		if l, ok := model.StoryStageLabels[raw]; ok {
			return l
		}
	case "Pri":
		if l, ok := priLabels[raw]; ok {
			return l
		}
	case "Type":
		if l, ok := storyTypeLabels[raw]; ok {
			return l
		}
	case "Category":
		if l, ok := storyCategoryLabels[raw]; ok {
			return l
		}
	case "Source":
		if l, ok := storySourceLabels[raw]; ok {
			return l
		}
	case "ClosedReason":
		if l, ok := storyCloseLabels[raw]; ok {
			return l
		}
	}
	return raw
}

// storyFieldLabels 是需求字段的中文名。
//
// 只有列在这里的字段才会被追溯（history.Diff 按这个映射决定追溯范围）。
// 「列进来」是有成本的决策：每多一个字段，时间线就多一类噪音，
// 所以只收用户真正会关心「谁改了什么」的字段。
var storyFieldLabels = map[string]string{
	"Title":            "标题",
	"Spec":             "需求描述",
	"Verify":           "验收标准",
	"Keywords":         "关键词",
	"ModuleID":         "模块",
	"PlanID":           "所属计划",
	"Type":             "类型",
	"Category":         "类别",
	"Source":           "来源",
	"SourceNote":       "来源备注",
	"Pri":              "优先级",
	"Estimate":         "预计工时",
	"Status":           "状态",
	"Stage":            "阶段",
	"AssignedTo":       "指派给",
	"ClosedBy":         "由谁关闭",
	"ClosedReason":     "关闭原因",
	"DuplicateStoryID": "重复需求",
	"Version":          "版本",
}

var storyTypeLabels = map[string]string{
	model.StoryTypeStory:       "研发需求",
	model.StoryTypeRequirement: "用户需求",
	model.StoryTypeEpic:        "业务需求",
}

var storyCategoryLabels = map[string]string{
	model.StoryCategoryFeature:     "功能",
	model.StoryCategoryInterface:   "接口",
	model.StoryCategoryPerformance: "性能",
	model.StoryCategorySafe:        "安全",
	model.StoryCategoryExperience:  "体验",
	model.StoryCategoryImprove:     "改进",
	model.StoryCategoryOther:       "其他",
}

var storySourceLabels = map[string]string{
	model.StorySourceCustomer:   "客户",
	model.StorySourceUser:       "用户",
	model.StorySourcePO:         "产品经理",
	model.StorySourceMarket:     "市场",
	model.StorySourceService:    "客服",
	model.StorySourceOperation:  "运营",
	model.StorySourceSupport:    "技术支持",
	model.StorySourceCompetitor: "竞争对手",
	model.StorySourcePartner:    "合作伙伴",
	model.StorySourceDev:        "开发人员",
	model.StorySourceTester:     "测试人员",
	model.StorySourceBug:        "由缺陷转入",
	model.StorySourceForum:      "论坛",
	model.StorySourceOther:      "其他",
}

var storyCloseLabels = map[string]string{
	model.StoryCloseDone:       "已完成",
	model.StoryCloseSubdivided: "已拆分",
	model.StoryCloseDuplicate:  "重复",
	model.StoryClosePostponed:  "延期",
	model.StoryCloseWillNotDo:  "不做",
	model.StoryCloseCancel:     "已取消",
	model.StoryCloseByDesign:   "设计如此",
}

// reviewResultLabels 评审结论的中文名。
var reviewResultLabels = map[string]string{
	model.ReviewPass:    "确认通过",
	model.ReviewRevert:  "撤销变更",
	model.ReviewClarify: "有待明确",
	model.ReviewReject:  "拒绝",
}

// 编译期断言：StoryLabeler 满足 history 的接口。
var _ history.Labeler = StoryLabeler{}

// userIDFields 是「值是一个用户 id」的字段名。
//
// 这些字段不能交给 Labeler 翻译（它拿不到 context），由 service
// 在写历史前用 fillUserNames 批量补齐姓名。
//
// 需求侧的字段名用 Go 风格大驼峰（标题、状态那几个），项目与任务侧
// 用数据库列名 —— 两边各自的变更比对函数决定了键名，这里只是登记。
var userIDFields = map[string]struct{}{
	"AssignedTo": {},
	"OpenedBy":   {},
	"ClosedBy":   {},
	"ChangedBy":  {},
	"StagedBy":   {},

	"assigned_to": {},
	"opened_by":   {},
	"closed_by":   {},
	"finished_by": {},
	"canceled_by": {},
	"po":          {},
	"pm":          {},
	"qd":          {},
	"rd":          {},
	"resolved_by": {},
}

// ---------------------------------------------------------------------------
// 项目 / 迭代
// ---------------------------------------------------------------------------

// ProjectLabeler 翻译项目与迭代相关字段。
//
// 两者字段同名同义（名称、状态、起止、负责人），一套标签就够，
// 不必为迭代再抄一份 —— 抄出来的那份迟早会漏改。
type ProjectLabeler struct{}

// FieldLabel 返回字段的中文名。
func (ProjectLabeler) FieldLabel(field string) string {
	if l, ok := projectFieldLabels[field]; ok {
		return l
	}
	if l, ok := executionFieldLabels[field]; ok {
		return l
	}
	return ""
}

// Value 把原始值翻译为可读文本；无法翻译时原样返回。
func (ProjectLabeler) Value(field, raw string) string {
	if raw == "" {
		return ""
	}
	switch field {
	case "status":
		if l, ok := workflow.ProjectMachine.StatusLabels[raw]; ok {
			return l
		}
		if l, ok := workflow.ExecutionMachine.StatusLabels[raw]; ok {
			return l
		}
	case "model":
		if l, ok := projectModelLabels[raw]; ok {
			return l
		}
	case "type":
		if l, ok := executionTypeLabels[raw]; ok {
			return l
		}
	}
	return raw
}

// projectFieldLabels 是项目字段的中文名。
var projectFieldLabels = map[string]string{
	"name":      "项目名称",
	"code":      "项目代号",
	"model":     "管理模型",
	"status":    "状态",
	"begin":     "开始日期",
	"end":       "结束日期",
	"days":      "工期天数",
	"desc":      "项目描述",
	"po":        "产品负责人",
	"pm":        "项目经理",
	"qd":        "测试负责人",
	"rd":        "发布负责人",
	"acl":       "访问控制",
	"whitelist": "白名单",
	"multiple":  "多迭代",
	"progress":  "进度",
	"estimate":  "预计工时",
	"consumed":  "已消耗工时",
	"left":      "剩余工时",
	"order":     "排序",
}

// executionFieldLabels 是迭代字段的中文名。
var executionFieldLabels = map[string]string{
	"name":      "迭代名称",
	"code":      "迭代代号",
	"type":      "迭代类型",
	"status":    "状态",
	"begin":     "开始日期",
	"end":       "结束日期",
	"days":      "工期天数",
	"desc":      "迭代描述",
	"po":        "产品负责人",
	"pm":        "项目经理",
	"qd":        "测试负责人",
	"rd":        "发布负责人",
	"acl":       "访问控制",
	"whitelist": "白名单",
	"progress":  "进度",
	"estimate":  "预计工时",
	"consumed":  "已消耗工时",
	"left":      "剩余工时",
	"order":     "排序",
}

// projectModelLabels 是项目管理模型的中文名。
var projectModelLabels = map[string]string{
	model.ProjectModelScrum:         "敏捷（Scrum）",
	model.ProjectModelWaterfall:     "瀑布",
	model.ProjectModelKanban:        "看板",
	model.ProjectModelAgilePlus:     "敏捷加强",
	model.ProjectModelWaterfallPlus: "瀑布加强",
}

// executionTypeLabels 是迭代类型的中文名。
var executionTypeLabels = map[string]string{
	model.ExecutionTypeSprint: "迭代",
	model.ExecutionTypeStage:  "阶段",
	model.ExecutionTypeKanban: "看板",
}

// ---------------------------------------------------------------------------
// 任务
// ---------------------------------------------------------------------------

// TaskLabeler 翻译任务相关字段。
type TaskLabeler struct{}

// FieldLabel 返回字段的中文名。
func (TaskLabeler) FieldLabel(field string) string {
	if l, ok := taskFieldLabels[field]; ok {
		return l
	}
	return ""
}

// Value 把原始值翻译为可读文本。
func (TaskLabeler) Value(field, raw string) string {
	if raw == "" {
		return ""
	}
	switch field {
	case "status":
		return workflow.TaskMachine.StatusLabel(raw)
	case "type":
		if l, ok := taskTypeLabels[raw]; ok {
			return l
		}
	case "mode":
		if l, ok := taskModeLabels[raw]; ok {
			return l
		}
	case "pri":
		if l, ok := priLabels[raw]; ok {
			return l
		}
	case "closed_reason":
		if l, ok := taskCloseLabels[raw]; ok {
			return l
		}
	}
	return raw
}

// taskFieldLabels 是任务字段的中文名。
//
// 只有列进来的字段才会进时间线：任务的工时与状态是用户最关心的，
// 而 parent_id、product_id 这类结构性字段改了也不该刷屏。
var taskFieldLabels = map[string]string{
	"name":          "任务名称",
	"keywords":      "关键词",
	"desc":          "任务描述",
	"type":          "任务类型",
	"mode":          "多人模式",
	"pri":           "优先级",
	"estimate":      "预计工时",
	"consumed":      "已消耗工时",
	"left":          "剩余工时",
	"deadline":      "截止日期",
	"est_started":   "计划开始",
	"status":        "状态",
	"version":       "版本",
	"assigned_to":   "指派给",
	"module_id":     "模块",
	"story_id":      "关联需求",
	"execution_id":  "所属迭代",
	"parent_id":     "父任务",
	"closed_reason": "关闭原因",
}

var taskTypeLabels = map[string]string{
	model.TaskTypeDesign:  "设计",
	model.TaskTypeDevel:   "开发",
	model.TaskTypeRequest: "需求",
	model.TaskTypeTest:    "测试",
	model.TaskTypeStudy:   "研究",
	model.TaskTypeDiscuss: "讨论",
	model.TaskTypeUI:      "界面",
	model.TaskTypeAffair:  "事务",
	model.TaskTypeMisc:    "其他",
}

var taskModeLabels = map[string]string{
	model.TaskModeSingle: "单人",
	model.TaskModeLinear: "多人串行",
	model.TaskModeMulti:  "多人并行",
}

var taskCloseLabels = map[string]string{
	model.TaskCloseDone:   "已完成",
	model.TaskCloseCancel: "已取消",
}

// ---------------------------------------------------------------------------
// 缺陷
// ---------------------------------------------------------------------------

// BugLabeler 翻译缺陷相关字段。
//
// 缺陷的字段名用数据库列名（与项目、任务一致），因为它的变更比对
// 走的是「准备写入的字段映射」那条路径，键名天然就是列名。
type BugLabeler struct{}

// FieldLabel 返回字段的中文名。
func (BugLabeler) FieldLabel(field string) string {
	if l, ok := bugFieldLabels[field]; ok {
		return l
	}
	return ""
}

// Value 把原始值翻译为可读文本；无法翻译时原样返回。
func (BugLabeler) Value(field, raw string) string {
	if raw == "" {
		return ""
	}
	switch field {
	case "status":
		return workflow.BugMachine.StatusLabel(raw)
	case "type":
		if l, ok := bugTypeLabels[raw]; ok {
			return l
		}
	case "severity":
		if l, ok := bugSeverityLabels[raw]; ok {
			return l
		}
	case "pri":
		if l, ok := priLabels[raw]; ok {
			return l
		}
	case "resolution", "closed_reason":
		if l, ok := bugResolutionLabels[raw]; ok {
			return l
		}
	case "confirmed":
		if l, ok := bugConfirmedLabels[raw]; ok {
			return l
		}
	}
	return raw
}

// bugFieldLabels 是缺陷字段的中文名。
//
// 「列进来」的口径与任务一致：用户真正会关心的字段才进时间线。
// last_edited_by、duplicate_bug_id 这类要么是审计痕迹、要么已经由
// 动作本身表达过（「解决为重复」），再各占一行只是噪音。
var bugFieldLabels = map[string]string{
	"title":           "缺陷标题",
	"keywords":        "关键词",
	"steps":           "重现步骤",
	"type":            "缺陷类型",
	"severity":        "严重程度",
	"pri":             "优先级",
	"os":              "操作系统",
	"browser":         "浏览器",
	"hardware":        "硬件环境",
	"opened_build":    "影响版本",
	"resolved_build":  "解决版本",
	"module_id":       "模块",
	"plan_id":         "所属计划",
	"story_id":        "关联需求",
	"task_id":         "关联任务",
	"project_id":      "所属项目",
	"execution_id":    "所属迭代",
	"deadline":        "截止日期",
	"assigned_to":     "指派给",
	"status":          "状态",
	"resolution":      "解决方案",
	"confirmed":       "确认状态",
	"to_story_id":     "转为需求",
	"related_bug_ids": "相关缺陷",
	"closed_reason":   "关闭原因",
	"order":           "排序",
}

var bugTypeLabels = map[string]string{
	model.BugTypeCodeError:       "代码错误",
	model.BugTypeConfig:          "配置相关",
	model.BugTypeInstall:         "安装部署",
	model.BugTypeSecurity:        "安全相关",
	model.BugTypePerformance:     "性能问题",
	model.BugTypeStandard:        "标准规范",
	model.BugTypeAutomation:      "测试脚本",
	model.BugTypeDesignDefect:    "设计缺陷",
	model.BugTypeCodeImprovement: "代码改进",
	model.BugTypeOthers:          "其他",
}

// bugSeverityLabels 严重程度。数字越大越轻。
var bugSeverityLabels = map[string]string{
	"1": "1",
	"2": "2",
	"3": "3",
	"4": "4",
	"0": "未设置",
}

var bugResolutionLabels = map[string]string{
	model.BugResolutionByDesign:   "设计如此",
	model.BugResolutionDuplicate:  "重复缺陷",
	model.BugResolutionExternal:   "外部原因",
	model.BugResolutionFixed:      "已修复",
	model.BugResolutionNotRepro:   "无法重现",
	model.BugResolutionPostponed:  "延期处理",
	model.BugResolutionWillNotFix: "不予解决",
	model.BugResolutionToStory:    "转为需求",
}

var bugConfirmedLabels = map[string]string{
	"0": "未确认",
	"1": "已确认",
}

// CaseLabeler 把用例的字段与取值翻译成可读文本。
type CaseLabeler struct{}

// FieldLabel 返回字段的中文名。
func (CaseLabeler) FieldLabel(field string) string {
	if l, ok := caseFieldLabels[field]; ok {
		return l
	}
	return ""
}

// Value 把原始值翻译为可读文本。
//
// stage 交给状态机自己翻：标签就定义在那儿，这里再抄一份必然漂移。
func (CaseLabeler) Value(field, raw string) string {
	if raw == "" {
		return ""
	}
	switch field {
	case "stage":
		return workflow.TestCaseMachine.StatusLabel(raw)
	case "status":
		if l, ok := caseStatusLabels[raw]; ok {
			return l
		}
	case "type":
		if l, ok := caseTypeLabels[raw]; ok {
			return l
		}
	case "pri":
		if l, ok := priLabels[raw]; ok {
			return l
		}
	}
	return raw
}

// caseFieldLabels 是用例字段的中文名。
//
// 只有列进来的字段才会进时间线。步骤明细的变化不单独记录：
// 版本 +1 本身就是「内容变了」的信号，把每一步的文本摊开只会刷屏。
var caseFieldLabels = map[string]string{
	"title":        "用例标题",
	"precondition": "前置条件",
	"keywords":     "关键词",
	"desc":         "补充说明",
	"type":         "用例类型",
	"pri":          "优先级",
	"stage":        "评审阶段",
	"status":       "可执行性",
	"version":      "版本",
	"module_id":    "所属模块",
	"story_id":     "溯源需求",
	"reviewed_by":  "评审人",
}

var caseTypeLabels = map[string]string{
	model.TestCaseTypeFeature:     "功能",
	model.TestCaseTypeInterface:   "接口",
	model.TestCaseTypePerformance: "性能",
	model.TestCaseTypeConfig:      "配置",
	model.TestCaseTypeSecurity:    "安全",
	model.TestCaseTypeOthers:      "其他",
}

var caseStatusLabels = map[string]string{
	model.TestCaseStatusNormal:      "正常",
	model.TestCaseStatusBlocked:     "被阻塞",
	model.TestCaseStatusInvestigate: "待查",
}

// TestTaskLabeler 把测试单的字段与取值翻译成可读文本。
type TestTaskLabeler struct{}

// FieldLabel 返回字段的中文名。
func (TestTaskLabeler) FieldLabel(field string) string {
	if l, ok := testTaskFieldLabels[field]; ok {
		return l
	}
	return ""
}

// Value 把原始值翻译为可读文本。
func (TestTaskLabeler) Value(field, raw string) string {
	if raw == "" {
		return ""
	}
	switch field {
	case "status":
		return workflow.TestTaskMachine.StatusLabel(raw)
	case "result":
		return testTaskResultLabel(raw)
	case "pri":
		if l, ok := priLabels[raw]; ok {
			return l
		}
	}
	return raw
}

// testTaskFieldLabels 是测试单字段的中文名。
//
// product_id / project_id / execution_id 属于结构性归属，改它们的场景
// 几乎只发生在建单时（建完就定了），列进时间线只会刷屏。
var testTaskFieldLabels = map[string]string{
	"name":        "测试单名称",
	"desc":        "说明",
	"pri":         "优先级",
	"status":      "状态",
	"result":      "测试结论",
	"report":      "结单报告",
	"assigned_to": "负责人",
	"build_id":    "被测构建",
	"begin":       "计划开始",
	"end":         "计划结束",
	"case_count":  "用例数",
}

// 编译期断言：各 Labeler 满足 history 的接口。
var (
	_ history.Labeler = ProjectLabeler{}
	_ history.Labeler = TaskLabeler{}
	_ history.Labeler = BugLabeler{}
	_ history.Labeler = CaseLabeler{}
	_ history.Labeler = TestTaskLabeler{}
)

// fillUserNames 把变更里的用户 id 翻成姓名。
//
// 一次批量查询补齐所有涉及的 id —— 逐条查会让「编辑一次需求」
// 变成十几次查询，而历史只是附带产物，不该拖慢主流程。
//
// 放在事务之外调用：查用户姓名是只读的，失败也不该让业务回滚。
func fillUserNames(ctx context.Context, users *repository.UserRepo, changes []history.Change) []history.Change {
	if len(changes) == 0 {
		return changes
	}

	ids := make([]uint, 0, len(changes)*2)
	collect := func(raw string) {
		if raw == "" {
			return
		}
		if id, err := strconv.ParseUint(raw, 10, 64); err == nil && id > 0 {
			ids = append(ids, uint(id))
		}
	}
	for _, c := range changes {
		if _, ok := userIDFields[c.Field]; !ok {
			continue
		}
		collect(c.Old)
		collect(c.New)
	}
	if len(ids) == 0 {
		return changes
	}

	names, err := users.NameMap(ctx, ids)
	if err != nil {
		// 查不到姓名不该让整次操作失败：历史里留着 id 总比丢失一条记录好。
		return changes
	}
	for i := range changes {
		c := &changes[i]
		if _, ok := userIDFields[c.Field]; !ok {
			continue
		}
		if c.OldText == "" {
			if id, err := strconv.ParseUint(c.Old, 10, 64); err == nil {
				if name, ok := names[uint(id)]; ok {
					c.OldText = name
				}
			}
		}
		if c.NewText == "" {
			if id, err := strconv.ParseUint(c.New, 10, 64); err == nil {
				if name, ok := names[uint(id)]; ok {
					c.NewText = name
				}
			}
		}
	}
	return changes
}
