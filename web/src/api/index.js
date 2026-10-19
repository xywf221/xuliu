import client from '@/api/client'

// 接口清单。
//
// 分域按后端模块来，函数名用中文语境下的常用说法（提交评审、报工时……），
// 让调用处读起来就是业务动作 —— 前端最怕的就是一层层扒
// 「这到底打的是哪个接口」，所以这里不做通用化封装，宁可多写几行。

// ---- 身份 ----
export const authApi = {
  login: (data) => client.post('/auth/login', data),
  logout: () => client.post('/auth/logout'),
  profile: () => client.get('/auth/profile'),
  changePassword: (data) => client.post('/auth/password', data),
}

// ---- 组织 ----
export const orgApi = {
  users: (params) => client.get('/users', { params }),
  userBrief: (params) => client.get('/users/brief', { params }),
  createUser: (data) => client.post('/users', data),
  updateUser: (id, data) => client.put(`/users/${id}`, data),
  deptTree: () => client.get('/depts/tree'),
  groups: (params) => client.get('/groups', { params }),
}

// ---- 产品与需求 ----
export const productApi = {
  list: (params) => client.get('/products', { params }),
  brief: (params) => client.get('/products/brief', { params }),
  get: (id) => client.get(`/products/${id}`),
  create: (data) => client.post('/products', data),
  update: (id, data) => client.put(`/products/${id}`, data),
  remove: (id) => client.delete(`/products/${id}`),

  modules: (productId) => client.get(`/products/${productId}/modules`),
  createModule: (data) => client.post('/modules', data),
  plans: (productId) => client.get(`/products/${productId}/plans`),
}

export const storyApi = {
  list: (params) => client.get('/stories', { params }),
  get: (id) => client.get(`/stories/${id}`),
  versions: (id) => client.get(`/stories/${id}/versions`),
  create: (data) => client.post('/stories', data),
  update: (id, data) => client.put(`/stories/${id}`, data),

  submitReview: (id, data) => client.post(`/stories/${id}/submit-review`, data || {}),
  recall: (id, data) => client.post(`/stories/${id}/recall`, data || {}),
  review: (id, data) => client.post(`/stories/${id}/review`, data),
  change: (id, data) => client.post(`/stories/${id}/change`, data),
  close: (id, data) => client.post(`/stories/${id}/close`, data),
  activate: (id, data) => client.post(`/stories/${id}/activate`, data || {}),
  assign: (id, data) => client.post(`/stories/${id}/assign`, data),
  stage: (id, data) => client.post(`/stories/${id}/stage`, data),
}

// ---- 项目与迭代 ----
export const projectApi = {
  list: (params) => client.get('/projects', { params }),
  brief: (params) => client.get('/projects/brief', { params }),
  get: (id) => client.get(`/projects/${id}`),
  create: (data) => client.post('/projects', data),
  update: (id, data) => client.put(`/projects/${id}`, data),
  action: (id, action, data) => client.post(`/projects/${id}/${action}`, data || {}),

  stories: (id, params) => client.get(`/projects/${id}/stories`, { params }),
  linkStory: (id, data) => client.post(`/projects/${id}/stories`, data),
  unlinkStory: (id, storyId) => client.delete(`/projects/${id}/stories/${storyId}`),

  team: (id) => client.get(`/projects/${id}/team`),
  addTeam: (id, data) => client.post(`/projects/${id}/team`, data),
  removeTeam: (id, userId) => client.delete(`/projects/${id}/team/${userId}`),
}

export const executionApi = {
  list: (params) => client.get('/executions', { params }),
  get: (id) => client.get(`/executions/${id}`),
  create: (data) => client.post('/executions', data),
  update: (id, data) => client.put(`/executions/${id}`, data),
  action: (id, action, data) => client.post(`/executions/${id}/${action}`, data || {}),
  team: (id) => client.get(`/executions/${id}/team`),
  addTeam: (id, data) => client.post(`/executions/${id}/team`, data),
  removeTeam: (id, userId) => client.delete(`/executions/${id}/team/${userId}`),
}

// ---- 任务与工时 ----
export const taskApi = {
  list: (params) => client.get('/tasks', { params }),
  kanban: (params) => client.get('/tasks/kanban', { params }),
  get: (id) => client.get(`/tasks/${id}`),
  create: (data) => client.post('/tasks', data),
  update: (id, data) => client.put(`/tasks/${id}`, data),
  action: (id, action, data) => client.post(`/tasks/${id}/${action}`, data || {}),
  assign: (id, data) => client.post(`/tasks/${id}/assign`, data),
  finish: (id, data) => client.post(`/tasks/${id}/finish`, data || {}),
  cancel: (id, data) => client.post(`/tasks/${id}/cancel`, data || {}),
  close: (id, data) => client.post(`/tasks/${id}/close`, data || {}),

  efforts: (id) => client.get(`/tasks/${id}/efforts`),
  recordEffort: (id, data) => client.post(`/tasks/${id}/efforts`, data),
  allEfforts: (params) => client.get('/efforts', { params }),
}

// ---- 缺陷 ----
export const bugApi = {
  list: (params) => client.get('/bugs', { params }),
  counts: (params) => client.get('/bugs/counts', { params }),
  get: (id) => client.get(`/bugs/${id}`),
  create: (data) => client.post('/bugs', data),
  update: (id, data) => client.put(`/bugs/${id}`, data),

  confirm: (id, data) => client.post(`/bugs/${id}/confirm`, data || {}),
  assign: (id, data) => client.post(`/bugs/${id}/assign`, data),
  resolve: (id, data) => client.post(`/bugs/${id}/resolve`, data),
  close: (id, data) => client.post(`/bugs/${id}/close`, data || {}),
  activate: (id, data) => client.post(`/bugs/${id}/activate`, data || {}),
  toStory: (id, data) => client.post(`/bugs/${id}/tostory`, data),
}

// ---- 工作台与通知 ----
export const workbenchApi = {
  get: () => client.get('/workbench'),
  burndown: (params) => client.get('/workbench/burndown', { params }),
}

export const noticeApi = {
  list: (params) => client.get('/notices', { params }),
  unreadCount: () => client.get('/notices/unread-count'),
  markRead: (data) => client.post('/notices/read', data),
  remove: (id) => client.delete(`/notices/${id}`),
}

// ---- 通用：评论 / 附件 / 操作历史 ----
export const commentApi = {
  list: (params) => client.get('/comments', { params }),
  create: (data) => client.post('/comments', data),
  remove: (id) => client.delete(`/comments/${id}`),
}

export const fileApi = {
  list: (params) => client.get('/files', { params }),
  upload: (formData) =>
    client.post('/files', formData, { headers: { 'Content-Type': 'multipart/form-data' } }),
  remove: (id) => client.delete(`/files/${id}`),
  downloadUrl: (id) => `/api/v1/files/${id}/download`,
}

export const actionApi = {
  list: (params) => client.get('/actions', { params }),
}

// ---- 测试用例 ----
export const caseApi = {
  list: (params) => client.get('/testcases', { params }),
  get: (id) => client.get(`/testcases/${id}`),
  create: (data) => client.post('/testcases', data),
  update: (id, data) => client.put(`/testcases/${id}`, data),
  remove: (id) => client.delete(`/testcases/${id}`),

  // 提交评审 / 撤回评审走通用动作接口（请求体只有备注）；
  // 评审结论走 /review，结论放在 result 里。
  submitReview: (id, data) => client.post(`/testcases/${id}/submit-review`, data || {}),
  recall: (id, data) => client.post(`/testcases/${id}/recall`, data || {}),
  review: (id, data) => client.post(`/testcases/${id}/review`, data),
}

// ---- 测试单 ----
export const testTaskApi = {
  list: (params) => client.get('/testtasks', { params }),
  get: (id) => client.get(`/testtasks/${id}`),
  runs: (id, params) => client.get(`/testtasks/${id}/runs`, { params }),
  create: (data) => client.post('/testtasks', data),
  update: (id, data) => client.put(`/testtasks/${id}`, data),
  remove: (id) => client.delete(`/testtasks/${id}`),

  // 整单指派负责人
  assign: (id, data) => client.post(`/testtasks/${id}/assign`, data),
  start: (id, data) => client.post(`/testtasks/${id}/start`, data || {}),
  block: (id, data) => client.post(`/testtasks/${id}/block`, data || {}),
  activate: (id, data) => client.post(`/testtasks/${id}/activate`, data || {}),
  // 结单必须给结论（pass / fail / blocked），后端会校验。
  finish: (id, data) => client.post(`/testtasks/${id}/finish`, data),

  // 单内用例管理
  addCases: (id, data) => client.post(`/testtasks/${id}/cases`, data),
  removeCase: (id, caseId) => client.delete(`/testtasks/${id}/cases/${caseId}`),
  assignCase: (id, caseId, data) => client.put(`/testtasks/${id}/cases/${caseId}/assign`, data),
  // 记录一次执行：同一用例在同一单里可以跑多次，每次追加一条记录。
  run: (id, caseId, data) => client.post(`/testtasks/${id}/cases/${caseId}/run`, data),
}
