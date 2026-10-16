<script setup>
// 任务列表。
//
// 任务有两种读法：一条条查（列表），按状态摊开看整体（看板）。
// 两者的数据源不同 —— 列表走分页 + 筛选，看板走一次性聚合（每列自带工时统计），
// 所以这里没有硬把它们塞进同一份数据，而是按当前视图分别请求。
// 状态流转一律进详情页：那是要填工时、写理由的操作，摆一排按钮在列表上只会误点。
import { computed, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'

import { executionApi, productApi, projectApi, storyApi, taskApi } from '@/api'
import StatusTag from '@/components/StatusTag.vue'
import UserSelect from '@/components/UserSelect.vue'
import { useList } from '@/composables/useList'
import { useAuthStore } from '@/stores/auth'
import { labelOf, priLabel, priOptions, priTagType, taskModes, taskTypes } from '@/utils/dict'
import { formatDate, hours, isOverdue } from '@/utils/format'

const route = useRoute()
const router = useRouter()
const auth = useAuthStore()

const canCreate = auth.can('task-create')

// 列表 / 看板。默认列表：绝大多数人进来是找一条具体的任务。
const view = ref('list')

// 项目筛选默认取 URL 上的 projectId：从项目页点「任务」跳过来时，
// 用户看到的就是那个项目的任务，而不是全站的任务。
const { list, total, page, size, loading, filters, reload, reset } = useList(taskApi.list, {
  projectId: Number(route.query.projectId) || 0,
  executionId: Number(route.query.executionId) || 0,
  status: '',
  type: '',
  assignedTo: 0,
  keywords: '',
})

// 任务状态：与后端 model.TaskStatus* 一致，只给筛选下拉用。
// 表格里的状态一律显示后端下发的 statusLabel（唯一事实），不在这张表里取文字。
const statusOptions = [
  { value: 'wait', label: '未开始' },
  { value: 'doing', label: '进行中' },
  { value: 'done', label: '已完成' },
  { value: 'pause', label: '已暂停' },
  { value: 'cancel', label: '已取消' },
  { value: 'closed', label: '已关闭' },
]

// 终态：不再流转，也就不该再报工时。
const terminalStatuses = ['done', 'cancel', 'closed']
function isTerminal(status) {
  return terminalStatuses.includes(status)
}

const projects = ref([])
const executions = ref([])

onMounted(async () => {
  projects.value = await projectApi.brief({})
  if (filters.projectId) await loadExecutions()
})

// 迭代属于项目：没选项目就没有可选的迭代，选了项目才去拉。
async function loadExecutions() {
  executions.value = []
  if (!filters.projectId) return
  const data = await executionApi.list({ projectId: filters.projectId, size: 100 })
  executions.value = data.list || []
}

async function onProjectChange() {
  // 换了项目，原来的迭代就不再属于这个项目了，必须清掉，
  // 否则会把 A 项目的迭代当成 B 项目的条件发出去，一条都查不到。
  filters.executionId = 0
  await loadExecutions()
  if (view.value === 'list') reload()
}

// 只作用于列表的筛选（状态/类型/负责人/关键词）在看板下没有意义，
// 走这个入口可以少发一次注定被忽略的请求。
function onFilterChange() {
  if (view.value === 'list') reload()
}

// ---- 看板 ----
const kanban = ref(null)
const kanbanLoading = ref(false)

async function loadKanban() {
  if (!filters.projectId) {
    // 看板没有「全站」这一说：列是某个项目的状态列，没项目就无从谈起。
    kanban.value = null
    return
  }
  kanbanLoading.value = true
  try {
    // 看板按项目聚合；选了迭代就再收窄一层，没选就是整个项目。
    const params = { projectId: filters.projectId }
    if (filters.executionId) params.executionId = filters.executionId
    kanban.value = await taskApi.kanban(params)
  } finally {
    kanbanLoading.value = false
  }
}

// 切到看板、或看板的作用域（项目 / 迭代）变了，都要重新拉 ——
// 看板是一次性聚合，不像列表那样换个分页参数就能复用旧数据。
watch(
  () => [view.value, filters.projectId, filters.executionId],
  () => {
    if (view.value === 'kanban') loadKanban()
  },
)

// ---- 新建 ----
const dialogVisible = ref(false)
const submitting = ref(false)
const formRef = ref(null)
const form = ref(emptyForm())

function emptyForm() {
  return {
    projectId: filters.projectId || 0,
    executionId: 0,
    productId: 0,
    moduleId: 0,
    storyId: 0,
    name: '',
    type: 'devel',
    mode: 'single',
    pri: 3,
    estimate: null,
    deadline: '',
    estStarted: '',
    assignedTo: 0,
    desc: '',
  }
}

const formExecutions = ref([])
async function loadFormExecutions(projectId) {
  formExecutions.value = []
  form.value.executionId = 0
  if (!projectId) return
  const data = await executionApi.list({ projectId, size: 100 })
  formExecutions.value = data.list || []
}

// 模块树随产品变化：换产品后原来选的模块就失效了，必须清掉，
// 否则会把 A 产品的模块挂到 B 产品的任务上（后端会拒绝，但那是白跑一趟）。
const modules = ref([])
async function loadModules(productId) {
  modules.value = []
  form.value.moduleId = 0
  if (!productId) return
  modules.value = await productApi.modules(productId)
}

// 关联需求：产品下最近的一批，够选就行 —— 任务通常拆自眼前这条需求，
// 不为了「全量可搜」而拖慢弹窗打开。
const stories = ref([])
async function loadStories(productId) {
  stories.value = []
  form.value.storyId = 0
  if (!productId) return
  const data = await storyApi.list({ productId, size: 100 })
  stories.value = data.list || []
}

// 换产品要同时重载模块树与需求列表，并清掉旧选择 —— 两者都是按产品来的。
async function onProductChange(productId) {
  await loadModules(productId)
  await loadStories(productId)
}

// 任务的模块必须来自产品的「任务」模块树：挂到需求/缺陷树上会被后端拒（module_wrong_root）。
// 所以这里按 type 过滤，而不是把整棵树平铺出去。
const moduleOptions = computed(() => flatten(modules.value))
function flatten(nodes, out = []) {
  for (const node of nodes || []) {
    if (node.type === 'task') out.push({ id: node.id, name: node.name })
    if (node.children?.length) flatten(node.children, out)
  }
  return out
}

const rules = {
  projectId: [{ required: true, message: '请选择项目', trigger: 'change' }],
  name: [{ required: true, message: '请输入任务名称', trigger: 'blur' }],
}

function openCreate() {
  form.value = emptyForm()
  dialogVisible.value = true
  if (form.value.projectId) loadFormExecutions(form.value.projectId)
}

async function submit() {
  const ok = await formRef.value?.validate().catch(() => false)
  if (!ok) return

  submitting.value = true
  try {
    const payload = { ...form.value }
    // 空值不下发：0 / '' 到了后端会被当成一个真实条件（如 moduleId=0），反而添乱。
    for (const key of ['executionId', 'productId', 'moduleId', 'storyId', 'assignedTo', 'estimate']) {
      if (!payload[key]) delete payload[key]
    }
    if (!payload.deadline) delete payload.deadline
    if (!payload.estStarted) delete payload.estStarted

    const created = await taskApi.create(payload)
    ElMessage.success('已创建任务')
    dialogVisible.value = false
    router.push(`/tasks/${created.id}`)
  } catch {
    // 提示由 HTTP 层负责（如模块树挂错、缺少必填）。
  } finally {
    submitting.value = false
  }
}

function goDetail(row) {
  router.push(`/tasks/${row.id}`)
}

// 类型标签优先用后端给的 typeLabel（唯一事实），缺失时才退回本地表。
function taskTypeLabel(row) {
  return row.typeLabel || labelOf(taskTypes, row.type)
}
</script>

<template>
  <el-card shadow="never">
    <div class="xl-page-title">
      <h2>任务</h2>
      <div class="head-actions">
        <el-radio-group v-model="view">
          <el-radio-button :value="'list'">列表</el-radio-button>
          <el-radio-button :value="'kanban'">看板</el-radio-button>
        </el-radio-group>
        <el-button v-if="canCreate" type="primary" @click="openCreate">
          <el-icon><Plus /></el-icon>
          <span>新建任务</span>
        </el-button>
      </div>
    </div>

    <div class="xl-filters">
      <el-select
        v-model="filters.projectId"
        placeholder="全部项目"
        clearable
        filterable
        style="width: 170px"
        @change="onProjectChange"
      >
        <el-option v-for="p in projects" :key="p.id" :value="p.id" :label="p.name" />
      </el-select>
      <el-select
        v-model="filters.executionId"
        placeholder="全部迭代"
        clearable
        :disabled="!filters.projectId"
        style="width: 150px"
        @change="onFilterChange"
      >
        <el-option v-for="e in executions" :key="e.id" :value="e.id" :label="e.name" />
      </el-select>
      <el-select v-model="filters.status" placeholder="状态" clearable style="width: 120px" @change="onFilterChange">
        <el-option v-for="opt in statusOptions" :key="opt.value" :value="opt.value" :label="opt.label" />
      </el-select>
      <el-select v-model="filters.type" placeholder="类型" clearable style="width: 120px" @change="onFilterChange">
        <el-option v-for="opt in taskTypes" :key="opt.value" :value="opt.value" :label="opt.label" />
      </el-select>
      <div style="width: 160px">
        <UserSelect v-model="filters.assignedTo" placeholder="负责人" clearable @change="onFilterChange" />
      </div>
      <el-input
        v-model="filters.keywords"
        placeholder="名称 / 关键词"
        clearable
        style="width: 200px"
        @keyup.enter="onFilterChange"
        @clear="onFilterChange"
      />
      <el-button @click="onFilterChange">查询</el-button>
      <el-button text @click="reset">重置</el-button>
    </div>

    <!-- 列表 -->
    <template v-if="view === 'list'">
      <el-table
        v-loading="loading"
        :data="list"
        border
        stripe
        row-key="id"
        class="clickable"
        @row-click="goDetail"
      >
        <el-table-column label="编号" width="110">
          <template #default="{ row }">
            <span class="xl-mono xl-dim">{{ row.number }}</span>
          </template>
        </el-table-column>
        <el-table-column label="名称" min-width="220" show-overflow-tooltip>
          <template #default="{ row }">
            <el-link type="primary" @click.stop="goDetail(row)">{{ row.name }}</el-link>
          </template>
        </el-table-column>
        <el-table-column label="项目" width="130" show-overflow-tooltip>
          <template #default="{ row }">{{ row.projectName || '-' }}</template>
        </el-table-column>
        <el-table-column label="迭代" width="130" show-overflow-tooltip>
          <template #default="{ row }">{{ row.executionName || '-' }}</template>
        </el-table-column>
        <el-table-column label="状态" width="96">
          <template #default="{ row }">
            <StatusTag :status="row.status" :label="row.statusLabel" />
          </template>
        </el-table-column>
        <el-table-column label="类型" width="90">
          <template #default="{ row }">{{ taskTypeLabel(row) }}</template>
        </el-table-column>
        <el-table-column label="优先级" width="92">
          <template #default="{ row }">
            <el-tag :type="priTagType(row.pri)" size="small" disable-transitions>
              {{ priLabel(row.pri) }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column label="负责人" width="100">
          <template #default="{ row }">{{ row.assignedToName || '-' }}</template>
        </el-table-column>
        <el-table-column label="进度" width="140">
          <template #default="{ row }">
            <el-progress :percentage="row.progress || 0" :stroke-width="8" />
          </template>
        </el-table-column>
        <el-table-column label="截止" width="110">
          <template #default="{ row }">
            <span :class="{ overdue: row.deadline && isOverdue(row.deadline) && !isTerminal(row.status) }">
              {{ formatDate(row.deadline) || '-' }}
            </span>
          </template>
        </el-table-column>
        <el-table-column label="工时" width="100">
          <template #default="{ row }">
            <span class="xl-dim">{{ hours(row.consumed) }}/{{ hours(row.estimate) }}</span>
          </template>
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
          @size-change="reload"
        />
      </div>
    </template>

    <!-- 看板 -->
    <div v-else v-loading="kanbanLoading" class="kanban">
      <el-empty v-if="!filters.projectId" description="看板需要先选一个项目" />
      <el-empty v-else-if="!(kanban?.columns || []).length" description="这里还没有任务" />
      <div v-else class="kanban-cols">
        <div v-for="col in kanban.columns" :key="col.status" class="kanban-col">
          <div class="kanban-head">
            <span>{{ col.label }}</span>
            <span class="xl-dim">{{ col.count }}</span>
          </div>
          <div class="xl-dim kanban-hours">
            预计 {{ hours(col.hours?.estimate) }} · 消耗 {{ hours(col.hours?.consumed) }} · 剩余
            {{ hours(col.hours?.left) }}
          </div>
          <div class="kanban-cards">
            <div
              v-for="t in col.tasks || []"
              :key="t.id"
              class="kanban-card"
              @click="goDetail(t)"
            >
              <div class="kanban-name">{{ t.name }}</div>
              <div class="kanban-meta">
                <el-tag :type="priTagType(t.pri)" size="small" disable-transitions>
                  {{ priLabel(t.pri) }}
                </el-tag>
                <span class="xl-dim">{{ t.assignedToName || '未指派' }}</span>
              </div>
              <el-progress :percentage="t.progress || 0" :stroke-width="4" :show-text="false" />
            </div>
          </div>
        </div>
      </div>
    </div>

    <el-dialog v-model="dialogVisible" title="新建任务" width="720px">
      <el-form ref="formRef" :model="form" :rules="rules" label-width="96px">
        <el-row :gutter="12">
          <el-col :span="12">
            <el-form-item label="项目" prop="projectId">
              <el-select
                v-model="form.projectId"
                filterable
                style="width: 100%"
                @change="loadFormExecutions"
              >
                <el-option v-for="p in projects" :key="p.id" :value="p.id" :label="p.name" />
              </el-select>
            </el-form-item>
          </el-col>
          <el-col :span="12">
            <el-form-item label="迭代">
              <el-select
                v-model="form.executionId"
                clearable
                :disabled="!form.projectId"
                style="width: 100%"
              >
                <el-option v-for="e in formExecutions" :key="e.id" :value="e.id" :label="e.name" />
              </el-select>
            </el-form-item>
          </el-col>
        </el-row>

        <el-row :gutter="12">
          <el-col :span="12">
            <el-form-item label="所属产品">
              <el-select
                v-model="form.productId"
                filterable
                clearable
                style="width: 100%"
                @change="onProductChange"
              >
                <el-option v-for="p in products" :key="p.id" :value="p.id" :label="p.name" />
              </el-select>
            </el-form-item>
          </el-col>
          <el-col :span="12">
            <el-form-item label="模块">
              <el-select v-model="form.moduleId" clearable style="width: 100%">
                <el-option v-for="m in moduleOptions" :key="m.id" :value="m.id" :label="m.name" />
              </el-select>
            </el-form-item>
          </el-col>
        </el-row>

        <el-form-item label="关联需求">
          <el-select
            v-model="form.storyId"
            filterable
            clearable
            :disabled="!form.productId"
            placeholder="可选，拆自哪条需求"
            style="width: 100%"
          >
            <el-option v-for="s in stories" :key="s.id" :value="s.id" :label="s.title" />
          </el-select>
        </el-form-item>

        <el-form-item label="名称" prop="name">
          <el-input v-model="form.name" maxlength="255" />
        </el-form-item>

        <el-row :gutter="12">
          <el-col :span="8">
            <el-form-item label="类型">
              <el-select v-model="form.type" style="width: 100%">
                <el-option v-for="opt in taskTypes" :key="opt.value" :value="opt.value" :label="opt.label" />
              </el-select>
            </el-form-item>
          </el-col>
          <el-col :span="8">
            <el-form-item label="模式">
              <el-select v-model="form.mode" style="width: 100%">
                <el-option v-for="opt in taskModes" :key="opt.value" :value="opt.value" :label="opt.label" />
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
            <el-form-item label="预计工时">
              <el-input-number v-model="form.estimate" :min="0" :step="0.5" :precision="1" />
            </el-form-item>
          </el-col>
          <el-col :span="8">
            <el-form-item label="截止日期">
              <el-date-picker
                v-model="form.deadline"
                type="date"
                value-format="YYYY-MM-DD"
                style="width: 100%"
              />
            </el-form-item>
          </el-col>
          <el-col :span="8">
            <el-form-item label="计划开始">
              <el-date-picker
                v-model="form.estStarted"
                type="date"
                value-format="YYYY-MM-DD"
                style="width: 100%"
              />
            </el-form-item>
          </el-col>
        </el-row>

        <el-form-item label="负责人">
          <UserSelect v-model="form.assignedTo" include-empty />
        </el-form-item>

        <el-form-item label="描述">
          <el-input v-model="form.desc" type="textarea" :rows="4" placeholder="要做什么、怎么算做完" />
        </el-form-item>
      </el-form>

      <template #footer>
        <el-button @click="dialogVisible = false">取消</el-button>
        <el-button type="primary" :loading="submitting" @click="submit">创建</el-button>
      </template>
    </el-dialog>
  </el-card>
</template>

<style scoped>
.head-actions {
  display: flex;
  align-items: center;
  gap: 12px;
}

.overdue {
  color: #f56c6c;
  font-weight: 500;
}

/* 整行可点：让手不用精确落在名称上 */
.clickable :deep(.el-table__row) {
  cursor: pointer;
}

.kanban-cols {
  display: flex;
  gap: 12px;
  overflow-x: auto;
  padding-bottom: 4px;
}

.kanban-col {
  flex: 0 0 240px;
  background: #f7f8fa;
  border-radius: 6px;
  padding: 10px;
}

.kanban-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  font-weight: 600;
}

.kanban-hours {
  font-size: 12px;
  margin: 4px 0 8px;
}

.kanban-cards {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.kanban-card {
  background: #fff;
  border-radius: 6px;
  padding: 8px 10px;
  cursor: pointer;
  box-shadow: 0 1px 2px rgba(0, 0, 0, 0.06);
}

.kanban-card:hover {
  box-shadow: 0 1px 6px rgba(0, 0, 0, 0.14);
}

.kanban-name {
  font-size: 13px;
  margin-bottom: 6px;
  word-break: break-word;
}

.kanban-meta {
  display: flex;
  align-items: center;
  justify-content: space-between;
  font-size: 12px;
  margin-bottom: 6px;
}
</style>
