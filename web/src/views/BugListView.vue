<script setup>
// 缺陷列表。
//
// 缺陷与需求最大的不同：它不版本化、也不走评审，核心是「坏没坏、谁来修、
// 修完没」—— 所以这一页把状态计数摆在最上面，让人一眼看到还有多少没解决。
//
// 状态流转（确认 / 指派 / 解决 / 关闭 / 重新激活）一律进详情页：
// 每个动作都要填原因或选人，摆在列表上一排按钮只会让人误点。
import { computed, onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'

import { bugApi, executionApi, productApi, projectApi, storyApi, taskApi } from '@/api'
import StatusTag from '@/components/StatusTag.vue'
import UserSelect from '@/components/UserSelect.vue'
import { useList } from '@/composables/useList'
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
import { formatDate, isOverdue } from '@/utils/format'

const route = useRoute()
const router = useRouter()
const auth = useAuthStore()

const canCreate = auth.can('bug-create')

// 产品筛选默认取 URL 上的 productId：从产品页点「缺陷」跳过来时，
// 用户看到的就是那个产品的缺陷，而不是全站的缺陷。
const { list, total, page, size, loading, filters, load, reload, reset } = useList(bugApi.list, {
  productId: Number(route.query.productId) || 0,
  status: '',
  type: '',
  severity: 0,
  pri: 0,
  assignedTo: 0,
  keywords: '',
})

// 缺陷状态只有三个，与后端 model.BugStatus* 一致。
const statusOptions = [
  { value: 'active', label: '激活' },
  { value: 'resolved', label: '已解决' },
  { value: 'closed', label: '已关闭' },
]

// ---- 状态计数 ----
// 列表是分页的，手上这 20 条数不出「全部还有多少没解决」，所以计数另取一次。
const counts = ref({ unresolved: 0, active: 0, resolved: 0, closed: 0 })

async function loadCounts() {
  // 只按产品维度汇总：再叠上状态筛选就等于「用筛过的结果统计自己」，没有意义。
  // 空产品传 undefined，让查询串里干脆不出现这个参数。
  counts.value = await bugApi.counts({ productId: filters.productId || undefined })
}

// 产品一换，列表与计数两块都得重取。收口成一个 refresh，
// 免得某个触发点只记得刷表格、漏了卡片，数字和内容对不上。
function refresh() {
  return Promise.all([reload(), loadCounts()])
}

async function resetAll() {
  await reset()
  await loadCounts()
}

const statusCards = computed(() => [
  { key: '', label: '未解决', value: counts.value.unresolved, tone: 'danger' },
  { key: 'active', label: '激活', value: counts.value.active, tone: 'warning' },
  { key: 'resolved', label: '已解决', value: counts.value.resolved, tone: 'success' },
  { key: 'closed', label: '已关闭', value: counts.value.closed, tone: 'info' },
])

// 点卡片 = 按状态过滤。「未解决」不是一个状态，而是「还没给出解决方案」，
// 后端这条路径认的是空 status（不带状态过滤），所以它的 key 是空串。
function pickStatus(key) {
  filters.status = key
  refresh()
}

function goDetail(row) {
  router.push(`/bugs/${row.id}`)
}

// ---- 新建 ----
const dialogVisible = ref(false)
const submitting = ref(false)
const formRef = ref(null)
const form = ref(emptyForm())

function emptyForm() {
  return {
    productId: Number(route.query.productId) || 0,
    projectId: 0,
    executionId: 0,
    moduleId: 0,
    storyId: 0,
    taskId: 0,
    title: '',
    steps: '',
    type: 'codeerror',
    severity: 3,
    pri: 3,
    os: '',
    browser: '',
    hardware: '',
    openedBuild: '',
    deadline: '',
    assignedTo: 0,
  }
}

const rules = {
  productId: [{ required: true, message: '请选择产品', trigger: 'change' }],
  title: [{ required: true, message: '请输入缺陷标题', trigger: 'blur' }],
}

// 下拉数据源。产品和项目是全集，先拉一次放进内存；
// 迭代 / 模块 / 需求 / 任务都随所选产品与项目而变，按需再取。
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

async function loadModules(productId) {
  modules.value = []
  form.value.moduleId = 0
  if (!productId) return
  modules.value = await productApi.modules(productId)
}

async function loadStories(productId) {
  stories.value = []
  form.value.storyId = 0
  if (!productId) return
  const data = await storyApi.list({ productId, size: 100 })
  stories.value = data.list || []
}

async function loadExecutions(projectId) {
  executions.value = []
  form.value.executionId = 0
  if (!projectId) return
  const data = await executionApi.list({ projectId })
  executions.value = data.list || []
}

async function loadTasks(projectId) {
  tasks.value = []
  form.value.taskId = 0
  if (!projectId) return
  const data = await taskApi.list({ projectId, size: 100 })
  tasks.value = data.list || []
}

// 换产品：模块树与可选需求都变了，必须清掉已选值，
// 否则会把 A 产品的模块/需求挂到 B 产品的缺陷上（后端会拒绝，但那是白跑一趟）。
function onFormProductChange(productId) {
  return Promise.all([loadModules(productId), loadStories(productId)])
}

// 换项目：迭代与可选任务都变了，同理清掉。
function onFormProjectChange(projectId) {
  return Promise.all([loadExecutions(projectId), loadTasks(projectId)])
}

function openCreate() {
  form.value = emptyForm()
  dialogVisible.value = true
  if (form.value.productId) onFormProductChange(form.value.productId)
}

async function submit() {
  const ok = await formRef.value?.validate().catch(() => false)
  if (!ok) return

  submitting.value = true
  try {
    const payload = { ...form.value }
    // 空截止日期不能当字符串发给后端：它校验的是 datetime=2006-01-02，
    // 空串直接判非法，所以没填就干脆不带这个字段。
    if (!payload.deadline) delete payload.deadline
    const created = await bugApi.create(payload)
    ElMessage.success('已创建缺陷')
    dialogVisible.value = false
    router.push(`/bugs/${created.id}`)
  } catch {
    // 提示由 HTTP 层负责（如模块挂错树、产品不可见）。
  } finally {
    submitting.value = false
  }
}

onMounted(async () => {
  // 列表由 useList 自己在 onMounted 里取，这里只补计数与两个全集下拉。
  loadCounts()
  products.value = await productApi.brief({})
  projects.value = await projectApi.brief({})
})
</script>

<template>
  <el-card shadow="never">
    <div class="xl-page-title">
      <h2>缺陷</h2>
      <el-button v-if="canCreate" type="primary" @click="openCreate">
        <el-icon><Plus /></el-icon>
        <span>新建缺陷</span>
      </el-button>
    </div>

    <div class="stats">
      <div
        v-for="card in statusCards"
        :key="card.key"
        class="stat"
        :class="{ 'stat-on': filters.status === card.key }"
        @click="pickStatus(card.key)"
      >
        <div class="stat-num" :class="`tone-${card.tone}`">{{ card.value }}</div>
        <div class="stat-cap xl-dim">{{ card.label }}</div>
      </div>
    </div>

    <div class="xl-filters">
      <el-select
        v-model="filters.productId"
        placeholder="全部产品"
        clearable
        filterable
        style="width: 170px"
        @change="refresh"
      >
        <el-option v-for="p in products" :key="p.id" :value="p.id" :label="p.name" />
      </el-select>
      <el-select v-model="filters.status" placeholder="状态" clearable style="width: 120px" @change="refresh">
        <el-option v-for="opt in statusOptions" :key="opt.value" :value="opt.value" :label="opt.label" />
      </el-select>
      <el-select v-model="filters.type" placeholder="类型" clearable style="width: 140px" @change="refresh">
        <el-option v-for="opt in bugTypes" :key="opt.value" :value="opt.value" :label="opt.label" />
      </el-select>
      <el-select v-model="filters.severity" placeholder="严重" clearable style="width: 120px" @change="refresh">
        <el-option v-for="opt in severityOptions" :key="opt.value" :value="opt.value" :label="opt.label" />
      </el-select>
      <el-select v-model="filters.pri" placeholder="优先级" clearable style="width: 120px" @change="refresh">
        <el-option v-for="opt in priOptions" :key="opt.value" :value="opt.value" :label="opt.label" />
      </el-select>
      <div style="width: 170px">
        <UserSelect v-model="filters.assignedTo" placeholder="指派给" clearable @change="refresh" />
      </div>
      <el-input
        v-model="filters.keywords"
        placeholder="标题 / 编号 / 关键词"
        clearable
        style="width: 200px"
        @keyup.enter="refresh"
        @clear="refresh"
      />
      <el-button @click="refresh">查询</el-button>
      <el-button text @click="resetAll">重置</el-button>
    </div>

    <el-table
      v-loading="loading"
      :data="list"
      border
      stripe
      row-key="id"
      @row-click="goDetail"
    >
      <el-table-column label="编号" width="110">
        <template #default="{ row }">
          <span class="xl-mono xl-dim">{{ row.code }}</span>
        </template>
      </el-table-column>
      <el-table-column label="标题" min-width="240" show-overflow-tooltip>
        <template #default="{ row }">
          <el-link type="primary" @click="goDetail(row)">{{ row.title }}</el-link>
        </template>
      </el-table-column>
      <el-table-column label="产品" width="130" show-overflow-tooltip>
        <template #default="{ row }">{{ row.productName }}</template>
      </el-table-column>
      <el-table-column label="类型" width="110">
        <template #default="{ row }">{{ labelOf(bugTypes, row.type, row.typeLabel) }}</template>
      </el-table-column>
      <el-table-column label="严重" width="90">
        <template #default="{ row }">
          <!-- 致命/严重（1-2）用红色压一下，其余降级成灰色，避免整页都飘红 -->
          <el-tag
            :type="Number(row.severity) <= 2 ? 'danger' : 'info'"
            size="small"
            disable-transitions
          >
            {{ row.severityLabel }}
          </el-tag>
        </template>
      </el-table-column>
      <el-table-column label="优先级" width="92">
        <template #default="{ row }">
          <el-tag :type="priTagType(row.pri)" size="small" disable-transitions>
            {{ priLabel(row.pri) }}
          </el-tag>
        </template>
      </el-table-column>
      <el-table-column label="状态" width="96">
        <template #default="{ row }">
          <StatusTag :status="row.status" :label="row.statusLabel" />
        </template>
      </el-table-column>
      <el-table-column label="指派给" width="100">
        <template #default="{ row }">{{ row.assignedToName || '-' }}</template>
      </el-table-column>
      <el-table-column label="解决方案" width="100">
        <template #default="{ row }">
          <span v-if="row.resolution">
            {{ labelOf(bugResolutions, row.resolution, row.resolutionLabel) }}
          </span>
          <span v-else class="xl-dim">-</span>
        </template>
      </el-table-column>
      <el-table-column label="影响版本" width="110" show-overflow-tooltip>
        <template #default="{ row }">{{ row.openedBuild || '-' }}</template>
      </el-table-column>
      <el-table-column label="截止" width="110">
        <template #default="{ row }">
          <!-- 已关闭的缺陷不再算「逾期」：它已经翻篇了，标红只会干扰视线 -->
          <span :class="{ overdue: isOverdue(row.deadline) && row.status !== 'closed' }">
            {{ formatDate(row.deadline) || '-' }}
          </span>
        </template>
      </el-table-column>
      <el-table-column label="激活次数" width="90">
        <template #default="{ row }">
          <!-- 没被重新激活过就不显示次数：满屏的 0 是噪音 -->
          <span v-if="row.activatedCount > 0">{{ row.activatedCount }}</span>
          <span v-else class="xl-dim">-</span>
        </template>
      </el-table-column>
      <el-table-column label="创建" width="110">
        <template #default="{ row }">{{ formatDate(row.openedDate) }}</template>
      </el-table-column>
    </el-table>

    <div class="xl-pager">
      <el-pagination
        v-model:current-page="page"
        v-model:page-size="size"
        :total="total"
        :page-sizes="[20, 50, 100]"
        layout="total, sizes, prev, pager, next"
        background
        @current-change="load"
        @size-change="refresh"
      />
    </div>

    <el-dialog v-model="dialogVisible" title="新建缺陷" width="760px">
      <el-form ref="formRef" :model="form" :rules="rules" label-width="96px">
        <el-row :gutter="12">
          <el-col :span="8">
            <el-form-item label="所属产品" prop="productId">
              <el-select v-model="form.productId" filterable style="width: 100%" @change="onFormProductChange">
                <el-option v-for="p in products" :key="p.id" :value="p.id" :label="p.name" />
              </el-select>
            </el-form-item>
          </el-col>
          <el-col :span="8">
            <el-form-item label="所属项目">
              <el-select v-model="form.projectId" clearable filterable style="width: 100%" @change="onFormProjectChange">
                <el-option v-for="p in projects" :key="p.id" :value="p.id" :label="p.name" />
              </el-select>
            </el-form-item>
          </el-col>
          <el-col :span="8">
            <el-form-item label="所属迭代">
              <el-select v-model="form.executionId" clearable style="width: 100%">
                <el-option v-for="e in executions" :key="e.id" :value="e.id" :label="e.name" />
              </el-select>
            </el-form-item>
          </el-col>
        </el-row>

        <el-row :gutter="12">
          <el-col :span="8">
            <el-form-item label="所属模块">
              <el-select v-model="form.moduleId" clearable filterable style="width: 100%">
                <el-option v-for="m in moduleOptions" :key="m.id" :value="m.id" :label="m.name" />
              </el-select>
            </el-form-item>
          </el-col>
          <el-col :span="8">
            <el-form-item label="关联需求">
              <el-select v-model="form.storyId" clearable filterable style="width: 100%">
                <el-option v-for="s in stories" :key="s.id" :value="s.id" :label="s.title" />
              </el-select>
            </el-form-item>
          </el-col>
          <el-col :span="8">
            <el-form-item label="关联任务">
              <el-select v-model="form.taskId" clearable filterable style="width: 100%">
                <el-option v-for="t in tasks" :key="t.id" :value="t.id" :label="t.name" />
              </el-select>
            </el-form-item>
          </el-col>
        </el-row>

        <el-form-item label="标题" prop="title">
          <el-input v-model="form.title" maxlength="255" />
        </el-form-item>

        <el-form-item label="重现步骤">
          <el-input
            v-model="form.steps"
            type="textarea"
            :rows="4"
            placeholder="环境（系统 / 浏览器 / 版本）+ 复现步骤 + 期望结果与实际结果"
          />
        </el-form-item>

        <el-row :gutter="12">
          <el-col :span="8">
            <el-form-item label="类型">
              <el-select v-model="form.type" style="width: 100%">
                <el-option v-for="opt in bugTypes" :key="opt.value" :value="opt.value" :label="opt.label" />
              </el-select>
            </el-form-item>
          </el-col>
          <el-col :span="8">
            <el-form-item label="严重程度">
              <el-select v-model="form.severity" style="width: 100%">
                <el-option v-for="opt in severityOptions" :key="opt.value" :value="opt.value" :label="opt.label" />
              </el-select>
            </el-form-item>
          </el-col>
          <el-col :span="8">
            <el-form-item label="优先级">
              <el-select v-model="form.pri" style="width: 100%">
                <el-option v-for="opt in priOptions" :key="opt.value" :value="opt.value" :label="opt.label" />
              </el-select>
            </el-form-item>
          </el-col>
        </el-row>

        <el-row :gutter="12">
          <el-col :span="8">
            <el-form-item label="操作系统">
              <el-select v-model="form.os" clearable style="width: 100%">
                <el-option v-for="opt in osOptions" :key="opt.value" :value="opt.value" :label="opt.label" />
              </el-select>
            </el-form-item>
          </el-col>
          <el-col :span="8">
            <el-form-item label="浏览器">
              <el-select v-model="form.browser" clearable style="width: 100%">
                <el-option v-for="opt in browserOptions" :key="opt.value" :value="opt.value" :label="opt.label" />
              </el-select>
            </el-form-item>
          </el-col>
          <el-col :span="8">
            <el-form-item label="影响版本">
              <el-input v-model="form.openedBuild" maxlength="255" placeholder="如 v1.0.0" />
            </el-form-item>
          </el-col>
        </el-row>

        <el-row :gutter="12">
          <el-col :span="8">
            <el-form-item label="截止日期">
              <el-date-picker
                v-model="form.deadline"
                type="date"
                value-format="YYYY-MM-DD"
                placeholder="截止日期"
                style="width: 100%"
              />
            </el-form-item>
          </el-col>
          <el-col :span="8">
            <el-form-item label="指派给">
              <UserSelect v-model="form.assignedTo" include-empty />
            </el-form-item>
          </el-col>
        </el-row>
      </el-form>

      <template #footer>
        <el-button @click="dialogVisible = false">取消</el-button>
        <el-button type="primary" :loading="submitting" @click="submit">创建</el-button>
      </template>
    </el-dialog>
  </el-card>
</template>

<style scoped>
.stats {
  display: flex;
  gap: 12px;
  margin-bottom: 12px;
}

.stat {
  flex: 1;
  max-width: 190px;
  padding: 12px 16px;
  border: 1px solid #ebeef5;
  border-radius: var(--xl-card-radius);
  cursor: pointer;
  transition: border-color 0.2s;
}

.stat:hover {
  border-color: #c6d9f1;
}

.stat-on {
  border-color: #409eff;
  box-shadow: inset 0 0 0 1px #409eff;
}

.stat-num {
  font-size: 22px;
  font-weight: 600;
  line-height: 1.2;
}

.stat-cap {
  font-size: 12px;
  margin-top: 2px;
}

.tone-danger {
  color: #f56c6c;
}

.tone-warning {
  color: #e6a23c;
}

.tone-success {
  color: #67c23a;
}

.tone-info {
  color: #909399;
}

.overdue {
  color: #f56c6c;
}
</style>
