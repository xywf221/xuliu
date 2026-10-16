import axios from 'axios'
import { ElMessage } from 'element-plus'

// HTTP 客户端。
//
// 后端的响应外壳是统一的：成功 {data} / 失败 {code,message,requestId}。
// 这里把它**拆掉**再交给上层 —— 于是业务代码里看到的永远是数据本身，
// 不必到处写 res.data.data。少一层解包就少一层写错的机会。

const client = axios.create({
  baseURL: '/api/v1',
  timeout: 30000,
})

// token 存在这里而不是各个 store 里：拦截器与 store 都要用，
// 放在一个模块级变量里，刷新页面后再由 store 从本地存储恢复。
let accessToken = ''
let onUnauthorized = null

export function setAccessToken(token) {
  accessToken = token || ''
}

// onUnauthorized 注册「登录失效」时的处理（跳登录页）。
// 用回调而不是在这里 import router，是为了避免 router ↔ client 循环依赖。
export function setUnauthorizedHandler(handler) {
  onUnauthorized = handler
}

client.interceptors.request.use((config) => {
  if (accessToken) {
    config.headers.Authorization = `Bearer ${accessToken}`
  }
  return config
})

client.interceptors.response.use(
  (res) => res.data?.data ?? res.data,
  (err) => {
    const res = err.response
    const body = res?.data || {}

    // 401：登录过期或没有令牌。交给上层跳登录页，不在这里硬跳 ——
    // 拦截器里拿不到「当前在哪个页面」，跳完就没法回到原地了。
    if (res?.status === 401) {
      if (onUnauthorized) onUnauthorized()
      return Promise.reject(new ApiError(body.message || '登录已失效，请重新登录', body))
    }

    const message = body.message || defaultMessage(res?.status, err)
    // 4xx 是用户操作或权限的问题，弹一句就够；5xx 带上 requestId，
    // 让用户报障时能直接把定位信息给出来。
    ElMessage.error(body.requestId ? `${message}（${body.requestId}）` : message)
    return Promise.reject(new ApiError(message, body))
  },
)

function defaultMessage(status, err) {
  if (status === 403) return '没有操作权限'
  if (status === 404) return '对象不存在或已被删除'
  if (err.code === 'ECONNABORTED') return '请求超时，请稍后重试'
  if (!status) return '无法连接服务器'
  return `请求失败（HTTP ${status}）`
}

// ApiError 保留后端的错误码，供调用方做分支判断（如「版本冲突」提示刷新）。
export class ApiError extends Error {
  constructor(message, body = {}) {
    super(message)
    this.name = 'ApiError'
    this.code = body.code || ''
    this.requestId = body.requestId || ''
  }
}

export default client
