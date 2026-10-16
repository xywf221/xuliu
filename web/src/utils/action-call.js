import { bugApi, executionApi, projectApi, storyApi, taskApi } from '@/api'

// 状态动作 → 接口的映射。
//
// 后端把「当前状态能做什么」连同动作名一起下发了（TransitionResp），
// 前端要做的只剩「把动作名翻译成一次请求」。集中在这一张表里，
// 是为了让「双击一个按钮到底打了哪个接口」有唯一答案 ——
// 散在各处的 switch 迟早会出现同一个动作两处写法不一致。
//
// 需求是个例外：它的评审结论（pass/clarify/revert/reject）在状态机里是
// 四个独立动作，但对外只有一个接口 POST /stories/:id/review，
// 结论放在请求体的 result 里。表里如实写出来，免得看到四个动作以为有四个接口。

const storyReviewActions = ['pass', 'clarify', 'revert', 'reject']

// callAction 执行一个动作。payload 由调用方按动作补齐（见 ObjectActions.vue）。
export function callAction(objectType, id, action, payload = {}) {
  switch (objectType) {
    case 'story':
      return callStoryAction(id, action, payload)
    case 'task':
      return callTaskAction(id, action, payload)
    case 'bug':
      return callBugAction(id, action, payload)
    case 'project':
      return projectApi.action(id, action, { comment: payload.comment || '' })
    case 'execution':
      return executionApi.action(id, action, { comment: payload.comment || '' })
    default:
      return Promise.reject(new Error(`不支持的对象类型：${objectType}`))
  }
}

function callStoryAction(id, action, p) {
  if (storyReviewActions.includes(action)) {
    return storyApi.review(id, {
      version: p.version || 0,
      result: action,
      comment: p.comment || '',
      // 拒绝必须带上关闭原因（后端会校验），其余结论用不到这个字段。
      closedReason: action === 'reject' ? p.closedReason || 'willnotdo' : '',
    })
  }
  switch (action) {
    case 'submitreview':
      return storyApi.submitReview(id, {
        version: p.version || 0,
        reviewers: p.reviewers || [],
        comment: p.comment || '',
      })
    case 'recall':
      return storyApi.recall(id, { comment: p.comment || '' })
    case 'change':
      return storyApi.change(id, {
        version: p.version || 0,
        title: p.title,
        spec: p.spec || '',
        verify: p.verify || '',
        reason: p.reason,
        comment: p.comment || '',
      })
    case 'close':
      return storyApi.close(id, {
        closedReason: p.closedReason || 'done',
        duplicateStoryId: p.duplicateStoryId || 0,
        comment: p.comment || '',
      })
    case 'activate':
      return storyApi.activate(id, { assignedTo: p.assignedTo || 0, comment: p.comment || '' })
    case 'assign':
      return storyApi.assign(id, { assignedTo: p.assignedTo, comment: p.comment || '' })
    case 'stage':
      return storyApi.stage(id, { stage: p.stage, comment: p.comment || '' })
    default:
      return Promise.reject(new Error(`需求不支持的动作：${action}`))
  }
}

function callTaskAction(id, action, p) {
  switch (action) {
    case 'assign':
      return taskApi.assign(id, { assignedTo: p.assignedTo, comment: p.comment || '' })
    case 'finish':
      return taskApi.finish(id, { consumed: p.consumed || 0, comment: p.comment || '' })
    case 'cancel':
      return taskApi.cancel(id, { comment: p.comment || '' })
    case 'close':
      return taskApi.close(id, { comment: p.comment || '' })
    // start / pause / restart / activate 走同一个动作接口，动作名在 URL 上。
    case 'start':
    case 'pause':
    case 'restart':
    case 'activate':
      return taskApi.action(id, action, { comment: p.comment || '' })
    default:
      return Promise.reject(new Error(`任务不支持的动作：${action}`))
  }
}

function callBugAction(id, action, p) {
  switch (action) {
    case 'confirm':
      return bugApi.confirm(id, { comment: p.comment || '' })
    case 'assign':
      return bugApi.assign(id, { assignedTo: p.assignedTo, comment: p.comment || '' })
    case 'resolve':
      return bugApi.resolve(id, {
        resolution: p.resolution,
        resolvedBuild: p.resolvedBuild || '',
        duplicateBugId: p.duplicateBugId || 0,
        comment: p.comment || '',
      })
    case 'close':
      return bugApi.close(id, { comment: p.comment || '' })
    case 'activate':
      return bugApi.activate(id, { assignedTo: p.assignedTo || 0, comment: p.comment || '' })
    case 'tostory':
      return bugApi.toStory(id, {
        productId: p.productId || 0,
        title: p.title || '',
        assignedTo: p.assignedTo || 0,
      })
    default:
      return Promise.reject(new Error(`缺陷不支持的动作：${action}`))
  }
}

// needsAssignee 报告某个动作是否必须选人。
export function needsAssignee(action) {
  return action === 'assign'
}

// needsResolution 报告某个动作需要填解决方案。
export function needsResolution(action) {
  return action === 'resolve'
}

// needsClosedReason 报告某个动作需要填关闭原因。
// 需求关闭、需求评审拒绝这两个都要 —— 它们是同一个语义：以何种理由终结。
export function needsClosedReason(action) {
  return action === 'close'
}

// needsReviewResult 报告该动作是不是「给评审结论」。
export function isReviewAction(action) {
  return storyReviewActions.includes(action)
}
