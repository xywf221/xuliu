// 展示层的格式化。
//
// 后端所有时间都是 UTC（见 model 的时间约定），展示时转成本地时区 ——
// 「谁在什么时候改的」这件事，用户读的是自己的钟表时间。

// formatDate 把 ISO 时间转成 YYYY-MM-DD。
export function formatDate(value) {
  if (!value) return ''
  const d = new Date(value)
  if (Number.isNaN(d.getTime())) return String(value)
  const pad = (n) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`
}

// formatDateTime 转成 YYYY-MM-DD HH:mm。
export function formatDateTime(value) {
  if (!value) return ''
  const d = new Date(value)
  if (Number.isNaN(d.getTime())) return String(value)
  const pad = (n) => String(n).padStart(2, '0')
  return `${formatDate(value)} ${pad(d.getHours())}:${pad(d.getMinutes())}`
}

// fromNow 给操作历史与通知用：近期显示「刚刚 / 5 分钟前」，
// 超过一天就退回绝对时间 —— 太久远的「N 天前」读者还得自己换算，不如直接给日期。
export function fromNow(value) {
  if (!value) return ''
  const d = new Date(value)
  if (Number.isNaN(d.getTime())) return String(value)
  const diff = Date.now() - d.getTime()
  if (diff < 60000) return '刚刚'
  if (diff < 3600000) return `${Math.floor(diff / 60000)} 分钟前`
  if (diff < 86400000) return `${Math.floor(diff / 3600000)} 小时前`
  return formatDateTime(value)
}

// hours 显示工时：整数不带小数点（8 而不是 8.0）。
export function hours(value) {
  if (value === null || value === undefined || value === '') return '0'
  const n = Number(value)
  if (Number.isNaN(n)) return String(value)
  return Number.isInteger(n) ? String(n) : n.toFixed(2).replace(/0+$/, '').replace(/\.$/, '')
}

// today 返回今天的 YYYY-MM-DD，供日期选择器默认值使用。
export function today() {
  return formatDate(new Date())
}

// daysAgo / daysLater 给燃尽图的默认区间用。
export function daysAgo(n) {
  const d = new Date()
  d.setDate(d.getDate() - n)
  return formatDate(d)
}

export function daysLater(n) {
  const d = new Date()
  d.setDate(d.getDate() + n)
  return formatDate(d)
}

// isOverdue 判断截止日期是否已过（用于列表里的红色标记）。
export function isOverdue(deadline) {
  if (!deadline) return false
  return new Date(deadline).getTime() < Date.now()
}
