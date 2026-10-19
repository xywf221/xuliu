// 枚举字典。
//
// 后端在列表/详情里已经回了不少 Label 字段（statusLabel、typeLabel…），
// 凡是有的就用后端的 —— 那是唯一的一份事实。这里只补两类它没有的：
//   1. 下拉选项（新建/编辑表单要选值，后端不提供「有哪些值可选」）；
//   2. 前端自己拼的小标签（如优先级 P1）。
//
// 值本身与后端常量一一对应，改后端枚举时必须同步这里 ——
// 所以每张表都标注了它对应哪个后端文件。

// 优先级：1 最高，4 最低。
export const priOptions = [
  { value: 1, label: 'P1 紧急' },
  { value: 2, label: 'P2 高' },
  { value: 3, label: 'P3 中' },
  { value: 4, label: 'P4 低' },
]

export function priLabel(pri) {
  const found = priOptions.find((o) => o.value === Number(pri))
  return found ? found.label : `P${pri}`
}

export function priTagType(pri) {
  switch (Number(pri)) {
    case 1:
      return 'danger'
    case 2:
      return 'warning'
    case 3:
      return 'primary'
    default:
      return 'info'
  }
}

// 需求 —— internal/model/story.go
export const storyTypes = [
  { value: 'story', label: '用户故事' },
  { value: 'requirement', label: '需求' },
  { value: 'epic', label: '史诗' },
]

export const storyCategories = [
  { value: 'feature', label: '功能' },
  { value: 'interface', label: '接口' },
  { value: 'performance', label: '性能' },
  { value: 'safe', label: '安全' },
  { value: 'experience', label: '体验' },
  { value: 'improve', label: '改进' },
  { value: 'other', label: '其他' },
]

export const storySources = [
  { value: 'customer', label: '客户' },
  { value: 'user', label: '用户' },
  { value: 'po', label: '产品经理' },
  { value: 'market', label: '市场' },
  { value: 'service', label: '客服' },
  { value: 'operation', label: '运营' },
  { value: 'support', label: '技术支持' },
  { value: 'competitor', label: '竞品' },
  { value: 'partner', label: '合作伙伴' },
  { value: 'dev', label: '开发' },
  { value: 'tester', label: '测试' },
  { value: 'bug', label: '缺陷转化' },
  { value: 'forum', label: '论坛' },
  { value: 'other', label: '其他' },
]

// 需求阶段：开发流程上的位置，与 status（生命周期）是两件事。
export const storyStages = [
  { value: 'wait', label: '未开始' },
  { value: 'planned', label: '已计划' },
  { value: 'projected', label: '已立项' },
  { value: 'designing', label: '设计中' },
  { value: 'designed', label: '已设计' },
  { value: 'developing', label: '开发中' },
  { value: 'developed', label: '已开发' },
  { value: 'testing', label: '测试中' },
  { value: 'tested', label: '已测试' },
  { value: 'verified', label: '已验收' },
  { value: 'rejected', label: '已拒绝' },
  { value: 'delivering', label: '交付中' },
  { value: 'delivered', label: '已交付' },
  { value: 'released', label: '已发布' },
  { value: 'closed', label: '已关闭' },
]

export const storyClosedReasons = [
  { value: 'done', label: '已完成' },
  { value: 'subdivided', label: '已细分' },
  { value: 'duplicate', label: '重复' },
  { value: 'postponed', label: '延期' },
  { value: 'willnotdo', label: '不做' },
  { value: 'cancel', label: '取消' },
  { value: 'bydesign', label: '设计如此' },
]

export const reviewResults = [
  { value: 'pass', label: '通过' },
  { value: 'revert', label: '撤销变更' },
  { value: 'clarify', label: '有待明确' },
  { value: 'reject', label: '拒绝' },
]

// 任务 —— internal/model/task.go
export const taskTypes = [
  { value: 'design', label: '设计' },
  { value: 'devel', label: '开发' },
  { value: 'request', label: '请求' },
  { value: 'test', label: '测试' },
  { value: 'study', label: '研究' },
  { value: 'discuss', label: '讨论' },
  { value: 'ui', label: '界面' },
  { value: 'affair', label: '事务' },
  { value: 'misc', label: '其他' },
]

export const taskModes = [
  { value: 'single', label: '单人' },
  { value: 'linear', label: '多人串行' },
  { value: 'multi', label: '多人并行' },
]

// 缺陷 —— internal/model/bug.go
export const bugTypes = [
  { value: 'codeerror', label: '代码错误' },
  { value: 'config', label: '配置相关' },
  { value: 'install', label: '安装部署' },
  { value: 'security', label: '安全相关' },
  { value: 'performance', label: '性能问题' },
  { value: 'standard', label: '标准规范' },
  { value: 'automation', label: '测试脚本' },
  { value: 'designdefect', label: '设计缺陷' },
  { value: 'codeimprovement', label: '代码改进' },
  { value: 'others', label: '其他' },
]

export const bugResolutions = [
  { value: 'bydesign', label: '设计如此' },
  { value: 'duplicate', label: '重复缺陷' },
  { value: 'external', label: '外部原因' },
  { value: 'fixed', label: '已修复' },
  { value: 'notrepro', label: '无法重现' },
  { value: 'postponed', label: '延期处理' },
  { value: 'willnotfix', label: '不予修复' },
  { value: 'tostory', label: '转为需求' },
]

// 严重程度与优先级同为 1..4，但语义不同：严重程度说的是「坏了多重」，
// 优先级说的是「先做哪个」，所以标签不能共用一套。
export const severityOptions = [
  { value: 1, label: '1 致命' },
  { value: 2, label: '2 严重' },
  { value: 3, label: '3 一般' },
  { value: 4, label: '4 轻微' },
]

export const osOptions = [
  { value: 'all', label: '全部' },
  { value: 'windows', label: 'Windows' },
  { value: 'win11', label: 'Windows 11' },
  { value: 'win10', label: 'Windows 10' },
  { value: 'osx', label: 'macOS' },
  { value: 'android', label: 'Android' },
  { value: 'ios', label: 'iOS' },
  { value: 'linux', label: 'Linux' },
  { value: 'others', label: '其他' },
]

export const browserOptions = [
  { value: 'all', label: '全部' },
  { value: 'chrome', label: 'Chrome' },
  { value: 'edge', label: 'Edge' },
  { value: 'firefox', label: 'Firefox' },
  { value: 'safari', label: 'Safari' },
  { value: 'ie11', label: 'IE 11' },
  { value: 'other', label: '其他' },
]

// 项目 —— internal/model/project.go
export const projectModels = [
  { value: 'scrum', label: 'Scrum' },
  { value: 'waterfall', label: '瀑布' },
  { value: 'kanban', label: '看板' },
  { value: 'agileplus', label: '敏捷+' },
  { value: 'waterfallplus', label: '瀑布+' },
]

export const executionTypes = [
  { value: 'sprint', label: '冲刺' },
  { value: 'stage', label: '阶段' },
  { value: 'kanban', label: '看板' },
]

export const aclOptions = [
  { value: 'open', label: '公开' },
  { value: 'private', label: '私有' },
]

// 产品 —— internal/model/product.go
export const productTypes = [
  { value: 'normal', label: '正常' },
  { value: 'branch', label: '分支' },
  { value: 'platform', label: '平台' },
]

export const productStatuses = [
  { value: 'normal', label: '正常' },
  { value: 'closed', label: '已关闭' },
]

// 测试用例 —— internal/model/test.go / internal/workflow/machines.go
//
// 用例有两个正交的枚举：stage 是评审阶段（能不能进测试单），
// status 是可执行性（这条用例本身还能不能跑）。
export const caseStages = [
  { value: 'wait', label: '待评审' },
  { value: 'reviewing', label: '评审中' },
  { value: 'confirmed', label: '已确认' },
  { value: 'rejected', label: '已驳回' },
]

export const caseStatuses = [
  { value: 'normal', label: '正常' },
  { value: 'blocked', label: '阻塞' },
  { value: 'investigate', label: '待调查' },
]

export const caseTypes = [
  { value: 'feature', label: '功能' },
  { value: 'interface', label: '接口' },
  { value: 'performance', label: '性能' },
  { value: 'config', label: '配置' },
  { value: 'security', label: '安全' },
  { value: 'others', label: '其他' },
]

// 测试单与执行结果 —— internal/workflow/machines.go / model/enums.go
export const testTaskStatuses = [
  { value: 'wait', label: '未开始' },
  { value: 'doing', label: '进行中' },
  { value: 'blocked', label: '已阻塞' },
  { value: 'closed', label: '已结单' },
]

export const testRunResults = [
  { value: 'pass', label: '通过' },
  { value: 'fail', label: '失败' },
  { value: 'blocked', label: '阻塞' },
]

// 状态色：后端每个实体都回了 statusLabel，但颜色属于展示口味，
// 不必为此多一次接口字段，前端用一张表定下来。
const statusColors = {
  // 通用
  wait: 'info',
  doing: 'primary',
  done: 'success',
  pause: 'warning',
  cancel: 'info',
  closed: 'info',
  // 需求
  draft: 'info',
  reviewing: 'warning',
  active: 'success',
  changing: 'warning',
  // 缺陷
  resolved: 'success',
  // 项目/迭代
  suspended: 'warning',
  // 产品
  normal: 'success',
  // 测试
  confirmed: 'success',
  rejected: 'danger',
  reviewing: 'warning',
  blocked: 'warning',
  pass: 'success',
  fail: 'danger',
}

export function statusColor(status) {
  return statusColors[status] || 'info'
}

// 动作按钮的样式：破坏性动作用 danger 提示一下（关闭、取消），
// 其余走默认。后端回的动作里没有「危险」这个信息，它是界面语义。
export function actionButtonType(action) {
  switch (action) {
    case 'close':
    case 'cancel':
    case 'reject':
      return 'danger'
    case 'finish':
    case 'resolve':
    case 'pass':
      return 'success'
    case 'activate':
      return 'warning'
    default:
      return 'primary'
  }
}

export function labelOf(options, value, fallback = '') {
  const found = options.find((o) => o.value === value)
  return found ? found.label : fallback || value || ''
}
