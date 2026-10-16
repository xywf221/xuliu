<script setup>
// 缺陷详情。
//
// 生命周期：激活 → 指派 → 解决（必填解决方案）→ 关闭（只能从已解决关闭）
// → 重新激活（不是新开一条缺陷：activatedCount +1，并清掉原来的解决方案，
//   让它回到「待处理」，历史仍在同一条上）。
// 动作按钮全部来自后端的 TransitionResp，前端不自己判断「这个状态能不能关」。
import { computed, onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'

import { bugApi, executionApi, productApi, projectApi, storyApi, taskApi } from '@/api'
import ActionTimeline from '@/components/ActionTimeline.vue'
import CommentsPanel from '@/components/CommentsPanel.vue'
import ObjectActions from '@/components/ObjectActions.vue'
import StatusTag from '@/components/StatusTag.vue'
import UserSelect from '@/components/UserSelect.vue'
import { useAuthStore } from '@/stores/auth'
import {
  browserOptions,
  bugResolutions,
  bugTypes,
  labelOf,
  osOptions,
  priLabel,
  priOptions,
  priTagType,
  severityOptions,
} from '@/utils/dict'
import { formatDate, formatDateTime, isOverdue } from '@/utils/format'

const route = useRoute()
const router = useRouter()
const auth = useAuthStore()

const id = computed(() => Number(route.params.id))
const loading = ref(false)
const detail = ref(null)

const canEdit = auth.can('bug-edit')

async function load() {
  loading.value = true
  try {
    detail.value = await bugApi.get(id.value)
  } finally {
    loading.value = false
  }
}

// 「人 · 时间」这类组合展示：任一缺失都退回「-」，
// 免得出现「张三 · 」或「 · 2026-01-01」这种半截内容。
function actorLine(name, date) {
  if (!name && !date) return '-'
  return [name, date ? formatDateTime(date) : ''].filter(Boolean).join(' · ')
}

const timeline = ref(null)
const comments = ref(null)

async function afterAction() {
  // 状态一变，详情、操作历史、评论都要重取：动作本身会落一条评论，
  // 只刷详情会看到「历史里没有刚才那一步」。
  await load()
  timeline.value?.reload()
  comments.value?.reload()
}

// ---- 下拉数据 ----
// 产品/项目是全集，进页面拉一次即可；模块/迭代/需求/任务随所选产品与项目而变。
const products = ref([])
const projects = ref([])
const executions = ref([])
const modules = ref([])
const stories = ref([])
const tasks = ref([])

// 缺陷只能挂产品下的**缺陷**模块树：混进需求/任务树的节点会被后端
// module_wrong_root 拒绝，所以这里递归剪掉非 bug 的枝。
const moduleOptions = computed(() => pickBugModules(modules.value))

function pickBugModules(nodes, out = []) {
  for (const node of nodes || []) {
    if (node.type === 'bug') out.push({ id: node.id, name: node.name })
    if (node.children?.length) pickBugModules(node.children, out)
  }
  return out
}

async function fetchModules(productId) {
  modules.value = productId ? await productApi.modules(productId) : []
}

async function fetchStories(productId) {
  stories.value = []
  if (!productId) return
  const data = await storyApi.list({ productId, size: 100 })
  stories.value = data.list || []
}

async function fetchExecutions(projectId) {
  executions.value = []
  if (!projectId) return
  const data = await executionApi.list({ projectId })
  executions.value = data.list || []
}

async function fetchTasks(projectId) {
  tasks.value = []
  if (!projectId) return
  const data = await taskApi.list({ projectId, size: 100 })
  tasks.value = data.list || []
}

// ---- 编辑 ----
const editVisible = ref(false)
const editSubmitting = ref(false)
const editFormRef = ref(null)
const editForm = ref({})

const editRules = {
  title: [{ required: true, message: '请输入缺陷标题', trigger: 'blur' }],
}

async function openEdit() {
  // 双保险：动作按钮本身已按权限置灰，这里再拦一道，
  // 免得将来多出一个入口（快捷键、深链）绕过按钮直接打开表单。
  if (!canEdit) return
  const d = detail.value
  // BugReq 是整体覆盖语义：编辑必须把完整对象交回去，
  // 少带一个字段后端就会把它写回零值（漏了 moduleId 就等于取消模块归属）。
  editForm.value = {
    productId: d.productId,
    projectId: d.projectId,
    executionId: d.executionId,
    moduleId: d.moduleId,
    storyId: d.storyId,
    taskId: d.taskId,
    title: d.title,
    keywords: d.keywords,
    steps: d.steps,
    type: d.type,
    severity: d.severity,
    pri: d.pri,
    os: d.os,
    browser: d.browser,
    hardware: d.hardware,
    openedBuild: d.openedBuild,
    deadline: d.deadline,
    assignedTo: d.assignedTo,
  }
  editVisible.value = true
  // 选项要在赋完值之后才取：fetch* 只填下拉，不会碰已选好的 id。
  await Promise.all([fetchModules(d.productId), fetchStories(d.productId)])
  await Promise.all([fetchExecutions(d.projectId), fetchTasks(d.projectId)])
}

// 换产品/项目时要清掉已选值 —— 旧选项已不在新列表里，
// 留着会提交一个「不属于该产品/项目」的 id，后端只好拒绝。
async function onEditProductChange(productId) {
  editForm.value.moduleId = 0
  editForm.value.storyId = 0
  await Promise.all([fetchModules(productId), fetchStories(productId)])
}

async function onEditProjectChange(projectId) {
  editForm.value.executionId = 0
  editForm.value.taskId = 0
  await Promise.all([fetchExecutions(projectId), fetchTasks(projectId)])
}

async function submitEdit() {
  const ok = await editFormRef.value?.validate().catch(() => false)
  if (!ok) return

  editSubmitting.value = true
  try {
    const payload = { ...editForm.value }
    // 空截止日期不能当字符串发出去：后端校验 datetime=2006-01-02，空串直接非法。
    if (!payload.deadline) delete payload.deadline
    await bugApi.update(id.value, payload)
    ElMessage.success('已保存')
    editVisible.value = false
    await load()
  } catch {
    // 提示由 HTTP 层负责（如模块挂错树、产品不可见）。
  } finally {
    editSubmitting.value = false
  }
}

onMounted(() => {
  load()
  loadOptions()
})

async function loadOptions() {
  const [p, j] = await Promise.all([productApi.brief({}), projectApi.brief({})])
  products.value = p
  projects.value = j
}
</script>

<template>
  <div v-loading="loading">
    <el-page-header :content="detail?.title || '缺陷详情'" @back="router.back()">
      <template #extra>
        <ObjectActions
          v-if="detail"
          object-type="bug"
          :object-id="detail.id"
          :actions="detail.actions || []"
          :version="0"
          @done="afterAction"
          @edit="openEdit"
        />
      </template>
    </el-page-header>

    <el-row v-if="detail" :gutter="16" class="mt">
      <el-col :md="16">
        <el-card shadow="never">
          <template #header>
            <div class="head">
              <span class="xl-mono xl-dim">{{ detail.code }}</span>
              <StatusTag :status="detail.status" :label="detail.statusLabel" />
              <el-tag v-if="detail.activatedCount > 0" type="warning" size="small" disable-transitions>
                已重新激活 {{ detail.activatedCount }} 次
              </el-tag>
            </div>
          </template>

          <div class="xl-fields">
            <div class="xl-field">
              <span class="label">编号</span>
              <span class="value xl-mono">{{ detail.code }}</span>
            </div>
            <div class="xl-field">
              <span class="label">产品</span>
              <span class="value">{{ detail.productName }}</span>
            </div>
            <div class="xl-field">
              <span class="label">项目</span>
              <span class="value">{{ detail.projectName || '-' }}</span>
            </div>
            <div class="xl-field">
              <span class="label">迭代</span>
              <span class="value">{{ detail.executionName || '-' }}</span>
            </div>
            <div class="xl-field">
              <span class="label">模块</span>
              <span class="value">{{ detail.moduleName || '-' }}</span>
            </div>
            <div class="xl-field">
              <span class="label">关联需求</span>
              <span class="value">
                <el-link
                  v-if="detail.storyId"
                  type="primary"
                  @click="router.push(`/stories/${detail.storyId}`)"
                >
                  {{ detail.storyTitle }}
                </el-link>
                <span v-else>-</span>
              </span>
            </div>
            <div class="xl-field">
              <span class="label">关联任务</span>
              <span class="value">
                <el-link
                  v-if="detail.taskId"
                  type="primary"
                  @click="router.push(`/tasks/${detail.taskId}`)"
                >
                  {{ detail.taskName }}
                </el-link>
                <span v-else>-</span>
              </span>
            </div>
            <div class="xl-field">
              <span class="label">类型</span>
              <span class="value">{{ labelOf(bugTypes, detail.type, detail.typeLabel) }}</span>
            </div>
            <div class="xl-field">
              <span class="label">严重程度</span>
              <span class="value">
                <el-tag
                  :type="Number(detail.severity) <= 2 ? 'danger' : 'info'"
                  size="small"
                  disable-transitions
                >
                  {{ detail.severityLabel }}
                </el-tag>
              </span>
            </div>
            <div class="xl-field">
              <span class="label">优先级</span>
              <span class="value">
                <el-tag :type="priTagType(detail.pri)" size="small" disable-transitions>
                  {{ priLabel(detail.pri) }}
                </el-tag>
              </span>
            </div>
            <div class="xl-field">
              <span class="label">状态</span>
              <span class="value">
                <StatusTag :status="detail.status" :label="detail.statusLabel" />
              </span>
            </div>
            <div class="xl-field">
              <span class="label">解决方案</span>
              <span class="value">
                {{ detail.resolution ? labelOf(bugResolutions, detail.resolution, detail.resolutionLabel) : '-' }}
              </span>
            </div>
            <div class="xl-field">
              <span class="label">影响版本</span>
              <span class="value">{{ detail.openedBuild || '-' }}</span>
            </div>
            <div class="xl-field">
              <span class="label">解决版本</span>
              <span class="value">{{ detail.resolvedBuild || '-' }}</span>
            </div>
            <div class="xl-field">
              <span class="label">操作系统</span>
              <span class="value">{{ detail.os ? labelOf(osOptions, detail.os, detail.os) : '-' }}</span>
            </div>
            <div class="xl-field">
              <span class="label">浏览器</span>
              <span class="value">{{ detail.browser ? labelOf(browserOptions, detail.browser, detail.browser) : '-' }}</span>
            </div>
            <div class="xl-field">
              <span class="label">硬件</span>
              <span class="value">{{ detail.hardware || '-' }}</span>
            </div>
            <div class="xl-field">
              <span class="label">指派给</span>
              <span class="value">{{ detail.assignedToName || '未指派' }}</span>
            </div>
            <div class="xl-field">
              <span class="label">创建</span>
              <span class="value">{{ actorLine(detail.openedByName, detail.openedDate) }}</span>
            </div>
            <div class="xl-field">
              <span class="label">解决</span>
              <span class="value">{{ actorLine(detail.resolvedByName, detail.resolvedDate) }}</span>
            </div>
            <div class="xl-field">
              <span class="label">关闭</span>
              <span class="value">{{ actorLine(detail.closedByName, detail.closedDate) }}</span>
            </div>
            <div class="xl-field">
              <span class="label">激活次数</span>
              <span class="value">{{ detail.activatedCount || 0 }}</span>
            </div>
            <div v-if="detail.toStoryId > 0" class="xl-field">
              <span class="label">转成的需求</span>
              <span class="value">
                <el-link type="primary" @click="router.push(`/stories/${detail.toStoryId}`)">
                  {{ detail.toStoryTitle }}
                </el-link>
              </span>
            </div>
            <div v-if="detail.duplicateBugId > 0" class="xl-field">
              <span class="label">重复缺陷</span>
              <span class="value">
                <el-link type="primary" @click="router.push(`/bugs/${detail.duplicateBugId}`)">
                  {{ detail.duplicateBugTitle }}
                </el-link>
              </span>
            </div>
            <div class="xl-field">
              <span class="label">截止日期</span>
              <!-- 已关闭的缺陷不再算「逾期」：它已经翻篇了，标红只会干扰视线 -->
              <span
                class="value"
                :class="{ overdue: isOverdue(detail.deadline) && detail.status !== 'closed' }"
              >
                {{ formatDate(detail.deadline) || '-' }}
              </span>
            </div>
          </div>
        </el-card>

        <el-card shadow="never" class="mt">
          <template #header><span>重现步骤</span></template>
          <div class="xl-prose">{{ detail.steps || '（未填写）' }}</div>
          <template v-if="detail.keywords">
            <el-divider content-position="left">关键词</el-divider>
            <div class="xl-prose">{{ detail.keywords }}</div>
          </template>
        </el-card>

        <el-card shadow="never" class="mt">
          <template #header>
            <div class="head">
              <span>评论</span>
              <span class="xl-dim">共 {{ detail.commentCount || 0 }} 条</span>
            </div>
          </template>
          <CommentsPanel ref="comments" object-type="bug" :object-id="detail.id" />
        </el-card>
      </el-col>

      <el-col :md="8">
        <el-card shadow="never">
          <template #header><span>操作历史</span></template>
          <ActionTimeline ref="timeline" object-type="bug" :object-id="detail.id" />
        </el-card>
      </el-col>
    </el-row>

    <!-- 编辑 -->
    <el-dialog v-model="editVisible" title="编辑缺陷" width="760px">
      <el-form ref="editFormRef" :model="editForm" :rules="editRules" label-width="96px">
        <el-row :gutter="12">
          <el-col :span="8">
            <el-form-item label="所属产品" prop="productId">
              <el-select v-model="editForm.productId" filterable style="width: 100%" @change="onEditProductChange">
                <el-option v-for="p in products" :key="p.id" :value="p.id" :label="p.name" />
              </el-select>
            </el-form-item>
          </el-col>
          <el-col :span="8">
            <el-form-item label="所属项目">
              <el-select v-model="editForm.projectId" clearable filterable style="width: 100%" @change="onEditProjectChange">
                <el-option v-for="p in projects" :key="p.id" :value="p.id" :label="p.name" />
              </el-select>
            </el-form-item>
          </el-col>
          <el-col :span="8">
            <el-form-item label="所属迭代">
              <el-select v-model="editForm.executionId" clearable style="width: 100%">
                <el-option v-for="e in executions" :key="e.id" :value="e.id" :label="e.name" />
              </el-select>
            </el-form-item>
          </el-col>
        </el-row>

        <el-row :gutter="12">
          <el-col :span="8">
            <el-form-item label="所属模块">
              <el-select v-model="editForm.moduleId" clearable filterable style="width: 100%">
                <el-option v-for="m in moduleOptions" :key="m.id" :value="m.id" :label="m.name" />
              </el-select>
            </el-form-item>
          </el-col>
          <el-col :span="8">
            <el-form-item label="关联需求">
              <el-select v-model="editForm.storyId" clearable filterable style="width: 100%">
                <el-option v-for="s in stories" :key="s.id" :value="s.id" :label="s.title" />
              </el-select>
            </el-form-item>
          </el-col>
          <el-col :span="8">
            <el-form-item label="关联任务">
              <el-select v-model="editForm.taskId" clearable filterable style="width: 100%">
                <el-option v-for="t in tasks" :key="t.id" :value="t.id" :label="t.name" />
              </el-select>
            </el-form-item>
          </el-col>
        </el-row>

        <el-form-item label="标题" prop="title">
          <el-input v-model="editForm.title" maxlength="255" />
        </el-form-item>

        <el-form-item label="重现步骤">
          <el-input
            v-model="editForm.steps"
            type="textarea"
            :rows="5"
            placeholder="环境（系统 / 浏览器 / 版本）+ 复现步骤 + 期望结果与实际结果"
          />
        </el-form-item>

        <el-row :gutter="12">
          <el-col :span="8">
            <el-form-item label="类型">
              <el-select v-model="editForm.type" style="width: 100%">
                <el-option v-for="opt in bugTypes" :key="opt.value" :value="opt.value" :label="opt.label" />
              </el-select>
            </el-form-item>
          </el-col>
          <el-col :span="8">
            <el-form-item label="严重程度">
              <el-select v-model="editForm.severity" style="width: 100%">
                <el-option v-for="opt in severityOptions" :key="opt.value" :value="opt.value" :label="opt.label" />
              </el-select>
            </el-form-item>
          </el-col>
          <el-col :span="8">
            <el-form-item label="优先级">
              <el-select v-model="editForm.pri" style="width: 100%">
                <el-option v-for="opt in priOptions" :key="opt.value" :value="opt.value" :label="opt.label" />
              </el-select>
            </el-form-item>
          </el-col>
        </el-row>

        <el-row :gutter="12">
          <el-col :span="8">
            <el-form-item label="操作系统">
              <el-select v-model="editForm.os" clearable style="width: 100%">
                <el-option v-for="opt in osOptions" :key="opt.value" :value="opt.value" :label="opt.label" />
              </el-select>
            </el-form-item>
          </el-col>
          <el-col :span="8">
            <el-form-item label="浏览器">
              <el-select v-model="editForm.browser" clearable style="width: 100%">
                <el-option v-for="opt in browserOptions" :key="opt.value" :value="opt.value" :label="opt.label" />
              </el-select>
            </el-form-item>
          </el-col>
          <el-col :span="8">
            <el-form-item label="硬件">
              <el-input v-model="editForm.hardware" maxlength="60" />
            </el-form-item>
          </el-col>
        </el-row>

        <el-row :gutter="12">
          <el-col :span="8">
            <el-form-item label="影响版本">
              <el-input v-model="editForm.openedBuild" maxlength="255" placeholder="如 v1.0.0" />
            </el-form-item>
          </el-col>
          <el-col :span="8">
            <el-form-item label="截止日期">
              <el-date-picker
                v-model="editForm.deadline"
                type="date"
                value-format="YYYY-MM-DD"
                placeholder="截止日期"
                style="width: 100%"
              />
            </el-form-item>
          </el-col>
          <el-col :span="8">
            <el-form-item label="指派给">
              <UserSelect v-model="editForm.assignedTo" include-empty />
            </el-form-item>
          </el-col>
        </el-row>
      </el-form>

      <template #footer>
        <el-button @click="editVisible = false">取消</el-button>
        <el-button type="primary" :loading="editSubmitting" @click="submitEdit">保存</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<style scoped>
.mt {
  margin-top: 16px;
}

.head {
  display: flex;
  align-items: center;
  gap: 10px;
}

.overdue {
  color: #f56c6c;
}
</style>
