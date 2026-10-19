<script setup>
// 用例列表。
//
// 用例与缺陷最大的不同：它走评审阶段（wait → reviewing → confirmed），
// 评审通过才能纳入测试单。所以这一页把「评审进度」放在首位——待评审几条、已通过几条，一目了然。
import { computed, onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'

import { caseApi, productApi, storyApi } from '@/api'
import StatusTag from '@/components/StatusTag.vue'
import UserSelect from '@/components/UserSelect.vue'
import { useList } from '@/composables/useList'
import { useAuthStore } from '@/stores/auth'
import {
  caseStages,
  caseStatuses,
  caseTypes,
  labelOf,
  priLabel,
  priOptions,
  priTagType,
} from '@/utils/dict'
import { formatDate } from '@/utils/format'

const route = useRoute()
const router = useRouter()
const auth = useAuthStore()

const canCreate = auth.can('testcase-create')

// 产品筛选默认取 URL 上的 productId：从产品页点「用例」跳过来时，
// 用户看到的就是那个产品的用例，而不是全站的用例。
const { list, total, page, size, loading, filters, load, reload, reset } = useList(caseApi.list, {
  productId: Number(route.query.productId) || 0,
  moduleId: 0,
  storyId: 0,
  stage: '',
  status: '',
  type: '',
  pri: 0,
  openedBy: 0,
  reviewedBy: 0,
  keywords: '',
})

// 用例阶段只有四种：待评审 → 评审中 → 已确认 / 已驳回。
const stageOptions = caseStages

// ---- 阶段统计 ----
// 列表是分页的，手上这 20 条数不出「还有多少在评审中」。
// 后端没有专门的分阶段计数接口，这里用 4 次 size=1 的查询各取一个 total ——
// 每次只搬 1 行，代价可以忽略；与其为它单开一个接口，不如等报表模块
// 把统计口径统一做掉。
const counts = ref({ wait: 0, reviewing: 0, confirmed: 0, rejected: 0 })

async function loadCounts() {
  const productId = filters.productId || undefined
  const stages = ['wait', 'reviewing', 'confirmed', 'rejected']
  const results = await Promise.all(
    stages.map((stage) => caseApi.list({ stage, productId, page: 1, size: 1 })),
  )
  const next = {}
  stages.forEach((stage, i) => {
    next[stage] = results[i]?.total || 0
  })
  counts.value = next
}

// 一换产品，列表与统计两块都得重取。收口成一个 refresh。
function refresh() {
  return Promise.all([reload(), loadCounts()])
}

async function resetAll() {
  await reset()
  await loadCounts()
}

const stageCards = computed(() => [
  { key: 'wait', label: '待评审', value: counts.value.wait, tone: 'info' },
  { key: 'reviewing', label: '评审中', value: counts.value.reviewing, tone: 'warning' },
  { key: 'confirmed', label: '已确认', value: counts.value.confirmed, tone: 'success' },
  { key: 'rejected', label: '已驳回', value: counts.value.rejected, tone: 'danger' },
])

function pickStage(key) {
  filters.stage = key
  refresh()
}

function goDetail(row) {
  router.push(`/testcases/${row.id}`)
}

// ---- 新建 ----
const dialogVisible = ref(false)
const submitting = ref(false)
const formRef = ref(null)
const form = ref(emptyForm())

function emptyForm() {
  return {
    productId: Number(route.query.productId) || 0,
    moduleId: 0,
    storyId: 0,
    title: '',
    precondition: '',
    keywords: '',
    desc: '',
    type: 'feature',
    pri: 3,
    steps: [],
    order: 0,
  }
}

const rules = {
  productId: [{ required: true, message: '请选择产品', trigger: 'change' }],
  title: [{ required: true, message: '请输入用例标题', trigger: 'blur' }],
  // 步骤是数组，校验要显式声明 type: 'array'，否则 required 只判断「非空」，
  // 空数组会被当成有值放过去，最后由后端回一句 400。
  steps: [
    { type: 'array', required: true, min: 1, message: '用例至少一步操作步骤', trigger: 'change' },
  ],
}

// 下拉数据源。产品和项目是全集，先拉一次放进内存；
// 迭代 / 模块 / 需求都随所选产品与项目而变，按需再取。
const products = ref([])
const modules = ref([])
const stories = ref([])

// 用例只能挂产品下**用例类型**的模块树。产品模块接口返回四棵树混在一起，
// 这里递归只留下 type === 'case' 的节点 —— 挑错树后端会拒绝，但那是白跑一趟。
async function loadModules(productId) {
  modules.value = []
  form.value.moduleId = 0
  if (!productId) return
  const tree = await productApi.modules(productId)
  modules.value = pickCaseModules(tree)
}

function pickCaseModules(nodes, out = []) {
  for (const node of nodes || []) {
    if (node.type === 'case') out.push({ id: node.id, name: node.name })
    if (node.children?.length) pickCaseModules(node.children, out)
  }
  return out
}

async function loadStories(productId) {
  stories.value = []
  form.value.storyId = 0
  if (!productId) return
  const data = await storyApi.list({ productId, size: 100 })
  stories.value = data.list || []
}

function onProductChange(productId) {
  return Promise.all([loadModules(productId), loadStories(productId)])
}

function openCreate() {
  form.value = emptyForm()
  dialogVisible.value = true
  if (form.value.productId) onProductChange(form.value.productId)
}

async function submit() {
  const ok = await formRef.value?.validate().catch(() => false)
  if (!ok) return

  submitting.value = true
  try {
    const payload = { ...form.value }
    // 步骤要转成后端能识别的数组格式。
    payload.steps = form.value.steps.map((s) => ({ step: s.step, expect: s.expect }))
    const created = await caseApi.create(payload)
    ElMessage.success('已创建用例')
    dialogVisible.value = false
    router.push(`/testcases/${created.id}`)
  } catch {
    // 提示由 HTTP 层负责（如模块挂错树）。
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
      <h2>用例</h2>
      <el-button v-if="canCreate" type="primary" @click="openCreate">
        <el-icon><Plus /></el-icon>
        <span>新建用例</span>
      </el-button>
    </div>

    <!-- 阶段卡片 -->
    <div class="stats">
      <div
        v-for="card in stageCards"
        :key="card.key"
        class="stat"
        :class="{ 'stat-on': filters.stage === card.key }"
        @click="pickStage(card.key)"
      >
        <div class="stat-num" :class="`tone-${card.tone}`">{{ card.value }}</div>
        <div class="stat-cap xl-dim">{{ card.label }}</div>
      </div>
    </div>

    <!-- 筛选区 -->
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
      <el-select v-model="filters.stage" placeholder="评审阶段" clearable style="width: 120px" @change="refresh">
        <el-option v-for="opt in stageOptions" :key="opt.value" :label="opt.label" :value="opt.value" />
      </el-select>
      <el-select v-model="filters.type" placeholder="类型" clearable style="width: 140px" @change="refresh">
        <el-option v-for="opt in caseTypes" :key="opt.value" :label="opt.label" :value="opt.value" />
      </el-select>
      <el-select v-model="filters.pri" placeholder="优先级" clearable style="width: 120px" @change="refresh">
        <el-option v-for="opt in priOptions" :key="opt.value" :label="opt.label" :value="opt.value" />
      </el-select>
      <UserSelect v-model="filters.openedBy" placeholder="创建人" clearable style="width: 140px" @change="refresh" />
      <UserSelect v-model="filters.reviewedBy" placeholder="评审人" clearable style="width: 140px" @change="refresh" />
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

    <!-- 表格 -->
    <el-table
      v-loading="loading"
      :data="list"
      border
      stripe
      row-key="id"
      @row-click="goDetail"
    >
      <el-table-column label="编号" width="110">
        <template #default="{ row }"><span class="xl-mono xl-dim">{{ row.code }}</span></template>
      </el-table-column>
      <el-table-column label="标题" min-width="240" show-overflow-tooltip>
        <template #default="{ row }"><el-link type="primary" @click="goDetail(row)">{{ row.title }}</el-link></template>
      </el-table-column>
      <el-table-column label="产品" width="130" show-overflow-tooltip>
        <template #default="{ row }">{{ row.productName }}</template>
      </el-table-column>
      <el-table-column label="模块" width="130" show-overflow-tooltip>
        <template #default="{ row }">{{ row.moduleName || '-' }}</template>
      </el-table-column>
      <el-table-column label="关联需求" width="150" show-overflow-tooltip>
        <template #default="{ row }">
          <span v-if="row.storyId"><el-link @click.stop="router.push(`/stories/${row.storyId}`)">{{ row.storyTitle }}</el-link></span>
          <span v-else class="xl-dim">-</span>
        </template>
      </el-table-column>
      <el-table-column label="类型" width="90">
        <template #default="{ row }">{{ labelOf(caseTypes, row.type) }}</template>
      </el-table-column>
      <el-table-column label="优先级" width="88">
        <template #default="{ row }">
          <el-tag :type="priTagType(row.pri)" size="small" disable-transitions>{{ priLabel(row.pri) }}</el-tag>
        </template>
      </el-table-column>
      <el-table-column label="阶段" width="100">
        <template #default="{ row }">
          <StatusTag :status="row.stage" :label="labelOf(caseStages, row.stage)" />
        </template>
      </el-table-column>
      <el-table-column label="可执行性" width="100">
        <template #default="{ row }">
          <StatusTag :status="row.status" :label="labelOf(caseStatuses, row.status)" />
        </template>
      </el-table-column>
      <el-table-column label="版本" width="70"><template #default="{ row }">{{ row.version }}</template></el-table-column>
      <el-table-column label="步骤数" width="80"><template #default="{ row }">{{ row.stepCount }}</template></el-table-column>
      <el-table-column label="创建人" width="100"><template #default="{ row }">{{ row.openedByName || '-' }}</template></el-table-column>
      <el-table-column label="创建时间" width="120"><template #default="{ row }">{{ formatDate(row.openedDate) }}</template></el-table-column>
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

    <!-- 新建弹窗 -->
    <el-dialog v-model="dialogVisible" title="新建用例" width="760px">
      <el-form ref="formRef" :model="form" :rules="rules" label-width="96px">
        <el-row :gutter="12">
          <el-col :span="8">
            <el-form-item label="所属产品" prop="productId">
              <el-select v-model="form.productId" filterable style="width: 100%" @change="onProductChange">
                <el-option v-for="p in products" :key="p.id" :label="p.name" :value="p.id" />
              </el-select>
            </el-form-item>
          </el-col>
          <el-col :span="8">
            <el-form-item label="所属模块">
              <el-select v-model="form.moduleId" clearable filterable style="width: 100%">
                <el-option v-for="m in modules" :key="m.id" :label="m.name" :value="m.id" />
              </el-select>
            </el-form-item>
          </el-col>
          <el-col :span="8">
            <el-form-item label="关联需求">
              <el-select v-model="form.storyId" clearable filterable style="width: 100%">
                <el-option v-for="s in stories" :key="s.id" :label="s.title" :value="s.id" />
              </el-select>
            </el-form-item>
          </el-col>
        </el-row>

        <el-form-item label="标题" prop="title">
          <el-input v-model="form.title" maxlength="255" />
        </el-form-item>
        <el-form-item label="前置条件">
          <el-input v-model="form.precondition" type="textarea" :rows="2" />
        </el-form-item>
        <el-form-item label="关键词">
          <el-input v-model="form.keywords" maxlength="255" />
        </el-form-item>
        <el-form-item label="用例描述">
          <el-input v-model="form.desc" type="textarea" :rows="3" />
        </el-form-item>

        <el-divider>操作步骤</el-divider>

        <el-space v-for="(step, idx) in form.steps" :key="idx" wrap>
          <el-input v-model="step.step" placeholder="操作内容" maxlength="2000" style="width: 300px" />
          <el-input v-model="step.expect" placeholder="期望结果" maxlength="2000" style="width: 200px" />
          <el-button @click="form.steps.splice(idx, 1)">删除</el-button>
        </el-space>

        <el-button style="margin-top: 8px" @click="form.steps.push({ step: '', expect: '' })">
          <el-icon><Plus /></el-icon> 添加步骤
        </el-button>

        <el-form-item label="排序号">
          <el-input-number v-model="form.order" :min="0" />
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
.stats { display: flex; gap: 12px; margin-bottom: 12px; }
.stat {
  flex: 1; max-width: 190px; padding: 12px 16px; border: 1px solid #ebeef5;
  border-radius: var(--xl-card-radius); cursor: pointer; transition: border-color 0.2s;
}
.stat:hover { border-color: #c6d9f1; }
.stat-on { border-color: #409eff; box-shadow: inset 0 0 0 1px #409eff; }
.stat-num { font-size: 22px; font-weight: 600; line-height: 1.2; }
.stat-cap { font-size: 12px; margin-top: 2px; }
.tone-info { color: #909399; }
.tone-warning { color: #e6a23c; }
.tone-success { color: #67c23a; }
</style>
