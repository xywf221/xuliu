<script setup>
// 测试单列表。
//
// 测试单是「一批用例在某次构建上的一次测试活动」，核心指标是执行结论：
// pass / fail / blocked 各有多少。列表上只做筛选与跳转，
// 开始、结单、纳入用例这些都要给结论或选人，一律进详情页做。
import { computed, onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'

import { executionApi, productApi, projectApi, testTaskApi } from '@/api'
import StatusTag from '@/components/StatusTag.vue'
import UserSelect from '@/components/UserSelect.vue'
import { useList } from '@/composables/useList'
import { useAuthStore } from '@/stores/auth'
import {
  labelOf,
  priLabel,
  priOptions,
  priTagType,
  testRunResults,
  testTaskStatuses,
} from '@/utils/dict'
import { formatDate } from '@/utils/format'

const route = useRoute()
const router = useRouter()
const auth = useAuthStore()

const canCreate = auth.can('testtask-create')

const { list, total, page, size, loading, filters, load, reload, reset } = useList(testTaskApi.list, {
  productId: Number(route.query.productId) || 0,
  status: '',
  result: '',
  pri: null,
  assignedTo: 0,
  openedBy: 0,
  keywords: '',
})

// ---- 状态统计 ----
// 后端没有跨测试单的汇总接口，这里用 4 次 size=1 的查询各取一个 total。
// 只搬 1 行数据，代价可以忽略；等报表模块统一统计口径时再收口。
const counts = ref({ wait: 0, doing: 0, blocked: 0, closed: 0 })

async function loadCounts() {
  const productId = filters.productId || undefined
  const statuses = ['wait', 'doing', 'blocked', 'closed']
  const results = await Promise.all(
    statuses.map((status) => testTaskApi.list({ status, productId, page: 1, size: 1 })),
  )
  const next = {}
  statuses.forEach((status, i) => {
    next[status] = results[i]?.total || 0
  })
  counts.value = next
}

function refresh() {
  return Promise.all([reload(), loadCounts()])
}

async function resetAll() {
  await reset()
  await loadCounts()
}

const statusCards = computed(() => [
  { key: 'wait', label: '未开始', value: counts.value.wait, tone: 'info' },
  { key: 'doing', label: '进行中', value: counts.value.doing, tone: 'primary' },
  { key: 'blocked', label: '已阻塞', value: counts.value.blocked, tone: 'warning' },
  { key: 'closed', label: '已结单', value: counts.value.closed, tone: 'success' },
])

function pickStatus(key) {
  filters.status = key
  refresh()
}

function goDetail(row) {
  router.push(`/testtasks/${row.id}`)
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
    name: '',
    desc: '',
    pri: 3,
    assignedTo: 0,
    begin: '',
    end: '',
    order: 0,
  }
}

const rules = {
  productId: [{ required: true, message: '请选择产品', trigger: 'change' }],
  name: [{ required: true, message: '请输入测试单名称', trigger: 'blur' }],
}

// 下拉数据源。产品/项目是全集；迭代随项目而变。
const products = ref([])
const projects = ref([])
const executions = ref([])

async function loadProjects() {
  form.value.projectId = 0
  form.value.executionId = 0
  executions.value = []
  // 项目是全局列表：测试单可以不挂项目（线上回归就不属于任何迭代）。
  projects.value = await projectApi.brief({})
}

async function loadExecutions(projectId) {
  form.value.executionId = 0
  executions.value = []
  if (!projectId) return
  const data = await executionApi.list({ projectId, size: 200 })
  executions.value = data.list || []
}

function openCreate() {
  form.value = emptyForm()
  dialogVisible.value = true
  loadProjects()
}

async function submit() {
  const ok = await formRef.value?.validate().catch(() => false)
  if (!ok) return

  submitting.value = true
  try {
    const payload = { ...form.value }
    // 空日期不能当字符串发：后端校验 datetime=2006-01-02，空串直接非法。
    if (!payload.begin) delete payload.begin
    if (!payload.end) delete payload.end
    const created = await testTaskApi.create(payload)
    ElMessage.success('已创建测试单')
    dialogVisible.value = false
    router.push(`/testtasks/${created.id}`)
  } catch {
    // 提示由 HTTP 层负责
  } finally {
    submitting.value = false
  }
}

onMounted(async () => {
  loadCounts()
  products.value = await productApi.brief({})
})
</script>

<template>
  <el-card shadow="never">
    <div class="xl-page-title">
      <h2>测试单</h2>
      <el-button v-if="canCreate" type="primary" @click="openCreate">
        <el-icon><Plus /></el-icon>
        <span>新建测试单</span>
      </el-button>
    </div>

    <!-- 状态卡片 -->
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
        style="width: 160px"
        @change="refresh"
      >
        <el-option v-for="p in products" :key="p.id" :label="p.name" :value="p.id" />
      </el-select>
      <el-select v-model="filters.status" placeholder="状态" clearable style="width: 110px" @change="refresh">
        <el-option v-for="opt in testTaskStatuses" :key="opt.value" :label="opt.label" :value="opt.value" />
      </el-select>
      <el-select v-model="filters.result" placeholder="结论" clearable style="width: 110px" @change="refresh">
        <el-option v-for="opt in testRunResults" :key="opt.value" :label="opt.label" :value="opt.value" />
      </el-select>
      <el-select v-model="filters.pri" placeholder="优先级" clearable style="width: 120px" @change="refresh">
        <el-option v-for="opt in priOptions" :key="opt.value" :label="opt.label" :value="opt.value" />
      </el-select>
      <div style="width: 150px">
        <UserSelect v-model="filters.assignedTo" placeholder="负责人" clearable @change="refresh" />
      </div>
      <div style="width: 150px">
        <UserSelect v-model="filters.openedBy" placeholder="创建人" clearable @change="refresh" />
      </div>
      <el-input
        v-model="filters.keywords"
        placeholder="单名 / 编号 / 关键词"
        clearable
        style="width: 200px"
        @keyup.enter="refresh"
        @clear="refresh"
      />
      <el-button @click="refresh">查询</el-button>
      <el-button text @click="resetAll">重置</el-button>
    </div>

    <el-table v-loading="loading" :data="list" border stripe row-key="id" @row-click="goDetail">
      <el-table-column label="编号" width="130">
        <template #default="{ row }"><span class="xl-mono xl-dim">{{ row.code }}</span></template>
      </el-table-column>
      <el-table-column label="单名" min-width="200" show-overflow-tooltip>
        <template #default="{ row }">
          <el-link type="primary" @click="goDetail(row)">{{ row.name }}</el-link>
        </template>
      </el-table-column>
      <el-table-column label="产品" width="130" show-overflow-tooltip>
        <template #default="{ row }">{{ row.productName || '-' }}</template>
      </el-table-column>
      <el-table-column label="项目" width="120" show-overflow-tooltip>
        <template #default="{ row }">{{ row.projectName || '-' }}</template>
      </el-table-column>
      <el-table-column label="优先级" width="88">
        <template #default="{ row }">
          <el-tag :type="priTagType(row.pri)" size="small" disable-transitions>{{ priLabel(row.pri) }}</el-tag>
        </template>
      </el-table-column>
      <el-table-column label="状态" width="96">
        <template #default="{ row }">
          <StatusTag :status="row.status" :label="labelOf(testTaskStatuses, row.status)" />
        </template>
      </el-table-column>
      <el-table-column label="结论" width="96">
        <template #default="{ row }">
          <el-tag
            v-if="row.result"
            :type="row.result === 'pass' ? 'success' : row.result === 'fail' ? 'danger' : 'warning'"
            size="small"
            disable-transitions
          >
            {{ labelOf(testRunResults, row.result) }}
          </el-tag>
          <span v-else class="xl-dim">-</span>
        </template>
      </el-table-column>
      <el-table-column label="进度" width="150">
        <template #default="{ row }">
          <!-- 单内用例的逐结论计数：通过/失败/阻塞/未执行 -->
          <span class="prog">
            <span class="ok">{{ row.summary?.pass || 0 }}</span>/<span class="bad">{{ row.summary?.fail || 0 }}</span>/<span class="warn">{{ row.summary?.blocked || 0 }}</span>/<span class="xl-dim">{{ row.summary?.unrun || 0 }}</span>
            <span class="xl-dim">（共 {{ row.caseCount }}）</span>
          </span>
        </template>
      </el-table-column>
      <el-table-column label="负责人" width="100">
        <template #default="{ row }">{{ row.assignedToName || '-' }}</template>
      </el-table-column>
      <el-table-column label="计划结束" width="110">
        <template #default="{ row }">{{ formatDate(row.end) || '-' }}</template>
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

    <el-dialog v-model="dialogVisible" title="新建测试单" width="640px">
      <el-form ref="formRef" :model="form" :rules="rules" label-width="96px">
        <el-form-item label="所属产品" prop="productId">
          <el-select v-model="form.productId" filterable style="width: 100%">
            <el-option v-for="p in products" :key="p.id" :label="p.name" :value="p.id" />
          </el-select>
        </el-form-item>
        <el-form-item label="所属项目">
          <el-select v-model="form.projectId" clearable filterable style="width: 100%" @change="loadExecutions">
            <el-option v-for="p in projects" :key="p.id" :label="p.name" :value="p.id" />
          </el-select>
        </el-form-item>
        <el-form-item label="所属迭代">
          <el-select v-model="form.executionId" clearable style="width: 100%">
            <el-option v-for="e in executions" :key="e.id" :label="e.name" :value="e.id" />
          </el-select>
        </el-form-item>

        <el-form-item label="单名" prop="name">
          <el-input v-model="form.name" maxlength="255" />
        </el-form-item>
        <el-form-item label="描述">
          <el-input v-model="form.desc" type="textarea" :rows="3" />
        </el-form-item>
        <el-form-item label="优先级">
          <el-select v-model="form.pri" style="width: 100%">
            <el-option v-for="opt in priOptions" :key="opt.value" :label="opt.label" :value="opt.value" />
          </el-select>
        </el-form-item>
        <el-form-item label="负责人">
          <UserSelect v-model="form.assignedTo" />
        </el-form-item>
        <el-row :gutter="12">
          <el-col :span="12">
            <el-form-item label="计划开始">
              <el-date-picker v-model="form.begin" type="date" value-format="YYYY-MM-DD" placeholder="可选" style="width: 100%" />
            </el-form-item>
          </el-col>
          <el-col :span="12">
            <el-form-item label="计划结束">
              <el-date-picker v-model="form.end" type="date" value-format="YYYY-MM-DD" placeholder="可选" style="width: 100%" />
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
  max-width: 170px;
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

.tone-info {
  color: #909399;
}

.tone-primary {
  color: #409eff;
}

.tone-success {
  color: #67c23a;
}

.tone-danger {
  color: #f56c6c;
}

.tone-warning {
  color: #e6a23c;
}

.prog .ok {
  color: #67c23a;
}

.prog .bad {
  color: #f56c6c;
}

.prog .warn {
  color: #e6a23c;
}
</style>
