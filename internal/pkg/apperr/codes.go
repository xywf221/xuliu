package apperr

import "net/http"

// 全站错误码目录。
//
// 集中登记的理由：错误码是对外 API 契约的一部分，必须能一眼看全、能查重
// （Define 会对重复码 panic）。各领域的常量按模块分段，文案写中文，
// 将来做多语言时替换 catalog 即可，业务代码不动。
const (
	// ---- 通用 ----
	Internal         Code = "internal"      // 未预期的内部错误
	InvalidParam     Code = "invalid_param" // 参数校验不通过
	NotFound         Code = "not_found"     // 资源不存在
	Unauthorized     Code = "unauthorized"  // 未登录
	Forbidden        Code = "forbidden"     // 已登录但无权限
	Conflict         Code = "conflict"      // 并发/状态冲突
	MethodNotAllowed Code = "method_not_allowed"
	TooManyRequests  Code = "too_many_requests"

	// ---- 认证与会话 ----
	TokenInvalid     Code = "token_invalid"    // 凭据格式错误/签名不符
	TokenExpired     Code = "token_expired"    // 已过期
	TokenRevoked     Code = "token_revoked"    // 已主动登出
	LoginFailed      Code = "login_failed"     // 账号或密码错误（不区分二者，防枚举）
	AccountDisabled  Code = "account_disabled" // 账号被禁用
	AccountLocked    Code = "account_locked"   // 连续失败被锁定
	OldPasswordWrong Code = "old_password_wrong"

	// ---- 用户 / 部门 ----
	UserNotFound      Code = "user_not_found"
	AccountTaken      Code = "account_taken"
	UserHasReferences Code = "user_has_references" // 已被需求/任务等引用，不能删
	DeptNotFound      Code = "dept_not_found"
	DeptNameTaken     Code = "dept_name_taken"
	DeptHasChildren   Code = "dept_has_children"
	DeptCycle         Code = "dept_cycle" // 把父部门设成自己的子孙
	CompanyNotFound   Code = "company_not_found"

	// ---- 权限组 ----
	GroupNotFound       Code = "group_not_found"
	GroupNameTaken      Code = "group_name_taken"
	GroupHasUsers       Code = "group_has_users" // 组内仍有成员
	PrivilegeUnknown    Code = "privilege_unknown"
	SuperAdminImmutable Code = "super_admin_immutable" // 超管权限不可回收

	// ---- 产品线 / 产品 ----
	ProductlineNotFound    Code = "productline_not_found"
	ProductlineCycle       Code = "productline_cycle"
	ProductlineHasProducts Code = "productline_has_products"
	ProductNotFound        Code = "product_not_found"
	ProductNameTaken       Code = "product_name_taken"
	ProductCodeTaken       Code = "product_code_taken"
	ProductClosed          Code = "product_closed"        // 已关闭的产品不允许再提需求
	ProductHasStories      Code = "product_has_stories"   // 仍有需求
	ProductAccessDenied    Code = "product_access_denied" // acl=private 且不在白名单

	// ---- 模块树 ----
	ModuleNotFound    Code = "module_not_found"
	ModuleNameTaken   Code = "module_name_taken"
	ModuleCycle       Code = "module_cycle"
	ModuleHasChildren Code = "module_has_children"
	ModuleWrongRoot   Code = "module_wrong_root"  // 父子模块不属于同一产品
	ModuleHasObjects  Code = "module_has_objects" // 模块下仍有需求/任务/缺陷

	// ---- 产品计划 ----
	PlanNotFound      Code = "plan_not_found"
	PlanDateInvalid   Code = "plan_date_invalid" // 结束日期早于开始日期
	PlanStatusInvalid Code = "plan_status_invalid"

	// ---- 需求 ----
	StoryNotFound               Code = "story_not_found"
	StoryTitleRequired          Code = "story_title_required"
	StoryStatusInvalid          Code = "story_status_invalid"   // 当前状态不允许该动作
	StoryVersionConflict        Code = "story_version_conflict" // 乐观锁：需求已被他人变更
	StoryNotChanged             Code = "story_not_changed"      // 内容没变，无需变更
	StoryReviewerRequired       Code = "story_reviewer_required"
	StoryReviewNotFound         Code = "story_review_not_found"
	StoryAlreadyReviewed        Code = "story_already_reviewed"
	StoryReviewResultInvalid    Code = "story_review_result_invalid"
	StoryClosedReasonRequired   Code = "story_closed_reason_required"
	StoryDuplicateTargetInvalid Code = "story_duplicate_target_invalid"
	StoryHasTasks               Code = "story_has_tasks"    // 需求下仍有未完成任务
	StoryHasChildren            Code = "story_has_children" // 需求下仍有子需求
	StoryStageInvalid           Code = "story_stage_invalid"
	StoryCannotDeleteReleased   Code = "story_cannot_delete_released"
	// StoryProductNotInProject 是「需求所属产品没关联到目标项目」。
	StoryProductNotInProject Code = "story_product_not_in_project"

	// ---- 项目 / 迭代 ----
	ProjectNotFound           Code = "project_not_found"
	ProjectNameTaken          Code = "project_name_taken"
	ProjectCodeTaken          Code = "project_code_taken"
	ProjectStatusInvalid      Code = "project_status_invalid"
	ProjectDateInvalid        Code = "project_date_invalid"
	ProjectNoProduct          Code = "project_no_product" // 项目未关联任何产品
	ExecutionNotFound         Code = "execution_not_found"
	ExecutionStatusInvalid    Code = "execution_status_invalid"
	ExecutionDateOutOfProject Code = "execution_date_out_of_project" // 迭代超出项目起止
	ExecutionDateInvalid      Code = "execution_date_invalid"
	// ProjectAccessDenied 是 acl=private 且不在白名单/团队成员里。
	// 与 ProductAccessDenied 分开：两者的可见性来源不同，
	// 前端要提示的是「加入这个项目」还是「加入这个产品」。
	ProjectAccessDenied    Code = "project_access_denied"
	TeamMemberNotFound     Code = "team_member_not_found"
	TeamMemberExists       Code = "team_member_exists"
	TeamMemberNotInProject Code = "team_member_not_in_project" // 任务负责人不在项目团队中

	// ---- 任务 ----
	TaskNotFound          Code = "task_not_found"
	TaskStatusInvalid     Code = "task_status_invalid"
	TaskLeftNotZero       Code = "task_left_not_zero" // 剩余工时不为 0，不允许关闭
	TaskEstimateInvalid   Code = "task_estimate_invalid"
	TaskHoursInvalid      Code = "task_hours_invalid"       // 工时数非法（负数等）
	TaskEffortExceedsLeft Code = "task_effort_exceeds_left" // 记录工时超过剩余工时
	TaskHasChildren       Code = "task_has_children"
	TaskStoryVersionStale Code = "task_story_version_stale" // 关联需求已变更
	TaskParentCycle       Code = "task_parent_cycle"
	TaskNotAssigned       Code = "task_not_assigned" // 未指派负责人

	// ---- 缺陷 ----
	BugNotFound                Code = "bug_not_found"
	BugStatusInvalid           Code = "bug_status_invalid"
	BugResolutionRequired      Code = "bug_resolution_required"
	BugResolutionInvalid       Code = "bug_resolution_invalid"
	BugAlreadyConfirmed        Code = "bug_already_confirmed"
	BugDuplicateOfSelf         Code = "bug_duplicate_of_self"
	BugDuplicateTargetRequired Code = "bug_duplicate_target_required"
	// BugHasDownstream 表示缺陷已经转成了需求或任务，此时不能删除。
	BugHasDownstream Code = "bug_has_downstream"
	// StoryFromBug 表示需求是由缺陷转入的，此时不能删除。
	StoryFromBug Code = "story_from_bug"

	// ---- 测试用例 ----
	TestCaseNotFound     Code = "testcase_not_found"
	TestCaseStageInvalid Code = "testcase_stage_invalid" // 当前评审阶段不允许该动作
	TestCaseStepRequired Code = "testcase_step_required" // 用例至少要有一步，否则无法逐步判定

	// ---- 测试单与执行 ----
	TestTaskNotFound         Code = "testtask_not_found"
	TestTaskStatusInvalid    Code = "testtask_status_invalid"
	TestTaskResultRequired   Code = "testtask_result_required"    // 结单必须给结论
	TestTaskCaseExists       Code = "testtask_case_exists"        // 用例已在单里
	TestTaskCaseNotFound     Code = "testtask_case_not_found"     // 单里没有这条用例
	TestTaskCaseNotConfirmed Code = "testtask_case_not_confirmed" // 未确认的用例不能入单
	TestRunNotFound          Code = "testrun_not_found"
	// TestTaskHasRuns 是「已经有执行记录的测试单不能删」。
	TestTaskHasRuns Code = "testtask_has_runs"

	// ---- 附件 ----
	FileNotFound       Code = "file_not_found"
	FileTooLarge       Code = "file_too_large"
	FileTypeNotAllowed Code = "file_type_not_allowed"
	FileStoreFailed    Code = "file_store_failed"
	FileEmpty          Code = "file_empty"

	// ---- 评论 / 操作历史 ----
	CommentNotFound Code = "comment_not_found"
	CommentEmpty    Code = "comment_empty"
	ActionNotFound  Code = "action_not_found"

	// ---- 存储后端 ----
	StorageBackendUnknown Code = "storage_backend_unknown"
	StorageDisabled       Code = "storage_disabled"
)

func init() {
	// 通用
	Define(Internal, http.StatusInternalServerError, "服务器内部错误")
	Define(InvalidParam, http.StatusBadRequest, "请求参数有误")
	Define(NotFound, http.StatusNotFound, "资源不存在")
	Define(Unauthorized, http.StatusUnauthorized, "请先登录")
	Define(Forbidden, http.StatusForbidden, "没有操作权限")
	Define(Conflict, http.StatusConflict, "操作冲突，请刷新后重试")
	Define(MethodNotAllowed, http.StatusMethodNotAllowed, "不支持的请求方法")
	Define(TooManyRequests, http.StatusTooManyRequests, "操作过于频繁，请稍后再试")

	// 认证与会话
	Define(TokenInvalid, http.StatusUnauthorized, "登录凭据无效")
	Define(TokenExpired, http.StatusUnauthorized, "登录已过期，请重新登录")
	Define(TokenRevoked, http.StatusUnauthorized, "登录已失效，请重新登录")
	Define(LoginFailed, http.StatusUnauthorized, "账号或密码错误")
	Define(AccountDisabled, http.StatusForbidden, "账号已被禁用")
	Define(AccountLocked, http.StatusForbidden, "账号已被锁定，请联系管理员")
	Define(OldPasswordWrong, http.StatusBadRequest, "原密码不正确")

	// 用户 / 部门
	Define(UserNotFound, http.StatusNotFound, "用户不存在")
	Define(AccountTaken, http.StatusConflict, "该账号已被占用")
	Define(UserHasReferences, http.StatusConflict, "该用户已被需求、任务等引用，无法删除，请改为禁用")
	Define(DeptNotFound, http.StatusNotFound, "部门不存在")
	Define(DeptNameTaken, http.StatusConflict, "同级下已存在同名部门")
	Define(DeptHasChildren, http.StatusConflict, "该部门下还有子部门，无法删除")
	Define(DeptCycle, http.StatusBadRequest, "不能把部门挂到它自己的下级下面")
	Define(CompanyNotFound, http.StatusNotFound, "公司信息不存在")

	// 权限组
	Define(GroupNotFound, http.StatusNotFound, "权限组不存在")
	Define(GroupNameTaken, http.StatusConflict, "已存在同名权限组")
	Define(GroupHasUsers, http.StatusConflict, "该权限组下还有成员，无法删除")
	Define(PrivilegeUnknown, http.StatusInternalServerError, "存在未知的权限点")
	Define(SuperAdminImmutable, http.StatusBadRequest, "超级管理员的权限不可回收")

	// 产品线 / 产品
	Define(ProductlineNotFound, http.StatusNotFound, "产品线不存在")
	Define(ProductlineCycle, http.StatusBadRequest, "不能把产品线挂到它自己的下级下面")
	Define(ProductlineHasProducts, http.StatusConflict, "该产品线下还有产品，无法删除")
	Define(ProductNotFound, http.StatusNotFound, "产品不存在")
	Define(ProductNameTaken, http.StatusConflict, "已存在同名产品")
	Define(ProductCodeTaken, http.StatusConflict, "产品代号已被占用")
	Define(ProductClosed, http.StatusConflict, "产品已关闭，不能继续提需求")
	Define(ProductHasStories, http.StatusConflict, "该产品下还有需求，无法删除")
	Define(ProductAccessDenied, http.StatusForbidden, "无权访问该私有产品")

	// 模块树
	Define(ModuleNotFound, http.StatusNotFound, "模块不存在")
	Define(ModuleNameTaken, http.StatusConflict, "同级下已存在同名模块")
	Define(ModuleCycle, http.StatusBadRequest, "不能把模块挂到它自己的下级下面")
	Define(ModuleHasChildren, http.StatusConflict, "该模块下还有子模块，无法删除")
	Define(ModuleWrongRoot, http.StatusBadRequest, "父子模块必须属于同一产品")
	Define(ModuleHasObjects, http.StatusConflict, "该模块下还有需求、任务或缺陷，无法删除")

	// 产品计划
	Define(PlanNotFound, http.StatusNotFound, "产品计划不存在")
	Define(PlanDateInvalid, http.StatusBadRequest, "计划结束日期不能早于开始日期")
	Define(PlanStatusInvalid, http.StatusConflict, "当前计划状态不允许该操作")

	// 需求
	Define(StoryNotFound, http.StatusNotFound, "需求不存在")
	Define(StoryTitleRequired, http.StatusBadRequest, "需求标题不能为空")
	Define(StoryStatusInvalid, http.StatusConflict, "需求当前状态不允许该操作")
	Define(StoryVersionConflict, http.StatusConflict, "需求已被他人修改，请刷新后重试")
	Define(StoryNotChanged, http.StatusBadRequest, "需求内容没有变化，无需变更")
	Define(StoryReviewerRequired, http.StatusBadRequest, "请至少指定一位评审人")
	Define(StoryReviewNotFound, http.StatusNotFound, "评审记录不存在")
	Define(StoryAlreadyReviewed, http.StatusConflict, "该需求已评审过，不能重复评审")
	Define(StoryReviewResultInvalid, http.StatusBadRequest, "评审结论不合法")
	Define(StoryClosedReasonRequired, http.StatusBadRequest, "关闭需求必须填写关闭原因")
	Define(StoryDuplicateTargetInvalid, http.StatusBadRequest, "重复需求必须指向另一条已存在的需求")
	Define(StoryHasTasks, http.StatusConflict, "该需求下还有未完成的任务，无法删除或撤出")
	Define(StoryHasChildren, http.StatusConflict, "该需求下还有子需求，无法删除")
	Define(StoryStageInvalid, http.StatusConflict, "需求当前阶段不允许该操作")
	Define(StoryCannotDeleteReleased, http.StatusConflict, "已发布的需求不能删除")
	Define(StoryProductNotInProject, http.StatusBadRequest, "该需求所属产品未关联到目标项目")

	// 项目 / 迭代
	Define(ProjectNotFound, http.StatusNotFound, "项目不存在")
	Define(ProjectNameTaken, http.StatusConflict, "已存在同名项目")
	Define(ProjectCodeTaken, http.StatusConflict, "项目代号已被占用")
	Define(ProjectStatusInvalid, http.StatusConflict, "项目当前状态不允许该操作")
	Define(ProjectDateInvalid, http.StatusBadRequest, "项目结束日期不能早于开始日期")
	Define(ProjectNoProduct, http.StatusBadRequest, "请先为项目关联至少一个产品")
	Define(ExecutionNotFound, http.StatusNotFound, "迭代不存在")
	Define(ExecutionStatusInvalid, http.StatusConflict, "迭代当前状态不允许该操作")
	Define(ExecutionDateOutOfProject, http.StatusBadRequest, "迭代起止日期必须落在项目周期内")
	Define(ExecutionDateInvalid, http.StatusBadRequest, "迭代结束日期不能早于开始日期")
	Define(ProjectAccessDenied, http.StatusForbidden, "无权访问该私有项目")
	Define(TeamMemberNotFound, http.StatusNotFound, "该成员不在项目团队中")
	Define(TeamMemberExists, http.StatusConflict, "该成员已在项目团队中")
	Define(TeamMemberNotInProject, http.StatusBadRequest, "负责人不是项目团队成员，请先加入团队")

	// 任务
	Define(TaskNotFound, http.StatusNotFound, "任务不存在")
	Define(TaskStatusInvalid, http.StatusConflict, "任务当前状态不允许该操作")
	Define(TaskLeftNotZero, http.StatusConflict, "任务剩余工时不为 0，请先结清工时再关闭")
	Define(TaskEstimateInvalid, http.StatusBadRequest, "预计工时必须大于 0")
	Define(TaskHoursInvalid, http.StatusBadRequest, "工时数不能为负数")
	Define(TaskEffortExceedsLeft, http.StatusBadRequest, "本次消耗工时超过剩余工时")
	Define(TaskHasChildren, http.StatusConflict, "该任务下还有子任务，无法删除")
	Define(TaskStoryVersionStale, http.StatusConflict, "关联需求已变更，请确认后再操作")
	Define(TaskParentCycle, http.StatusBadRequest, "不能把任务挂到它自己的下级下面")
	Define(TaskNotAssigned, http.StatusBadRequest, "请先指派任务负责人")

	// 缺陷
	Define(BugNotFound, http.StatusNotFound, "缺陷不存在")
	Define(BugStatusInvalid, http.StatusConflict, "缺陷当前状态不允许该操作")
	Define(BugResolutionRequired, http.StatusBadRequest, "解决缺陷必须选择解决方案")
	Define(BugResolutionInvalid, http.StatusBadRequest, "解决方案取值不合法")
	Define(BugAlreadyConfirmed, http.StatusConflict, "该缺陷已确认，无需重复确认")
	Define(BugDuplicateOfSelf, http.StatusBadRequest, "重复缺陷不能指向它自己")
	Define(BugDuplicateTargetRequired, http.StatusBadRequest, "解决方案为「重复」时必须指定重复的缺陷")
	Define(BugHasDownstream, http.StatusConflict, "该缺陷已转为需求或任务，无法删除；如需下线请改为关闭")
	Define(StoryFromBug, http.StatusConflict, "该需求是由缺陷转入的，无法删除；如需下线请改为关闭")

	// 测试用例
	Define(TestCaseNotFound, http.StatusNotFound, "测试用例不存在")
	Define(TestCaseStageInvalid, http.StatusConflict, "用例当前评审阶段不允许该操作")
	Define(TestCaseStepRequired, http.StatusBadRequest, "测试用例至少要有一个操作步骤")

	// 测试单与执行
	Define(TestTaskNotFound, http.StatusNotFound, "测试单不存在")
	Define(TestTaskStatusInvalid, http.StatusConflict, "测试单当前状态不允许该操作")
	Define(TestTaskResultRequired, http.StatusBadRequest, "结单必须给出测试结论")
	Define(TestTaskCaseExists, http.StatusConflict, "该用例已在测试单里")
	Define(TestTaskCaseNotFound, http.StatusNotFound, "测试单里没有这条用例")
	Define(TestTaskCaseNotConfirmed, http.StatusBadRequest, "只有评审通过的用例才能纳入测试单")
	Define(TestRunNotFound, http.StatusNotFound, "测试执行记录不存在")
	Define(TestTaskHasRuns, http.StatusConflict,
		"该测试单已有执行记录，删除会毁掉测试证据；如不再需要请改为结单")

	// 附件
	Define(FileNotFound, http.StatusNotFound, "附件不存在")
	Define(FileTooLarge, http.StatusRequestEntityTooLarge, "附件超出大小限制")
	Define(FileTypeNotAllowed, http.StatusBadRequest, "不支持该文件类型")
	Define(FileStoreFailed, http.StatusInternalServerError, "附件保存失败")
	Define(FileEmpty, http.StatusBadRequest, "上传的文件为空")

	// 评论 / 操作历史
	Define(CommentNotFound, http.StatusNotFound, "评论不存在")
	Define(CommentEmpty, http.StatusBadRequest, "评论内容不能为空")
	Define(ActionNotFound, http.StatusNotFound, "操作记录不存在")

	// 存储后端
	Define(StorageBackendUnknown, http.StatusInternalServerError, "未知的存储后端")
	Define(StorageDisabled, http.StatusServiceUnavailable, "附件存储未启用")
}
