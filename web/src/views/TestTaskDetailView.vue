<script setup>
// 测试单详情。
//
// 一个测试单里有若干用例，每条用例可以跑多次（每次一条执行记录），
// 所以这一页把「进度概览」放在最上面，「单内用例」放在中间：
// 每一行都能直接记录一次执行结果，历史记录单独查看。
//
// 状态动作（开始 / 标记阻塞 / 结单 / 重新打开 / 指派）全部来自后端的
// TransitionResp，按钮不写死；结单必须给结论，这个约束由后端强制。
import { computed, onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ElMessage, ElMessageBox } from 'element-plus'

import { caseApi, executionApi, projectApi, testTaskApi } from '@/api'
import ActionTimeline from '@/components/ActionTimeline.vue'
import CommentsPanel from '@/components/CommentsPanel.vue'
import ObjectActions from '@/components/ObjectActions.vue'
import StatusTag from '@/components/StatusTag.vue'
import UserSelect from '@/components/UserSelect.vue'
import { useAuthStore } from '@/stores/auth'
import {
  caseStages,
  labelOf,
  priLabel,
  priOptions,
  priTagType,
  testRunResults,
  testTaskStatuses,
} from '@/utils/dict'
import { formatDate, formatDateTime, hours } from '@/utils/format'

const route = useRoute()
const router = useRouter()
const auth = useAuthStore()

const id = computed(() => Number(route.params.id))
const loading = ref(false)
const detail = ref(null)

const canEdit = auth.can('testtask-edit')
const canRun = auth.can('testrun-record')
const canAssign = auth.can('testtask-assign')

async function load() {
  loading.value = true
  try {
    detail.value = await testTaskApi.get(id.value)
  } finally {
    loading.value = false
  }
}

function actorLine(name, date) {
  if (!name && !date) return '-'
  return [name, date ? formatDateTime(date) : ''].filter(Boolean).join(' · ')
}

const timeline = ref(null)
const comments = ref(null)

async function afterAction() {
  await load()
  timeline.value?.reload()
  comments.value?.reload()
}

const summaryCards = computed(() => {
  const s = detail.value?.summary || { pass: 0, fail: 0, blocked: 0, unrun: 0 }
  return [
    { key: 'unrun', label: '未执行', value: s.unrun, tone: 'info' },
    { key: 'pass', label: '已通过', value: s.pass, tone: 'success' },
    { key: 'fail', label: '已失败', value: s.fail, tone: 'danger' },
    { key: 'blocked', label: '被阻塞', value: s.blocked, tone: 'warning' },
  ]
})

// ---- 编辑 ----
const editVisible = ref(false)
const editSubmitting = ref(false)
const editFormRef = ref(null)
const editForm = ref({})
const editProjects = ref([])
const editExecutions = ref([])

const editRules = {
  name: [{ required: true, message: '请输入测试单名称', trigger: 'blur' }],
}

async function openEdit() {
  if (!canEdit) return
  const d = detail.value
  editForm.value = {
    productId: d.productId,
    projectId: d.projectId,
    executionId: d.executionId,
    buildId: d.buildId,
    name: d.name,
    desc: d.desc || '',
    pri: d.pri,
    assignedTo: d.assignedTo,
    begin: d.begin || '',
    end: d.end || '',
    order: d.order || 0,
  }
  editVisible.value = true
  // 选项在赋值之后取：load* 只填下拉，不会碰已选好的 id。
  editProjects.value = await projectApi.brief({})
  await loadEditExecutions(d.projectId, false)
}

async function loadEditExecutions(projectId, resetSelection = true) {
  editExecutions.value = []
  if (resetSelection) editForm.value.executionId = 0
  if (!projectId) return
  const data = await executionApi.list({ projectId, size: 200 })
  editExecutions.value = data.list || []
}

async function submitEdit() {
  const ok = await editFormRef.value?.validate().catch(() => false)
  if (!ok) return

  editSubmitting.value = true
  try {
    const payload = { ...editForm.value }
    // TestTaskReq 是整体覆盖语义，但 CaseIDs 留空表示「不动单内用例」——
    // 后端只在 len(caseIds) > 0 时才追加，所以这里不要传空数组。
    if (!payload.begin) delete payload.begin
    if (!payload.end) delete payload.end
    await testTaskApi.update(id.value, payload)
    ElMessage.success('已保存')
    editVisible.value = false
    await load()
  } catch {
    // 提示由 HTTP 层负责
  } finally {
    editSubmitting.value = false
  }
}

// ---- 纳入用例 ----
const addVisible = ref(false)
const addSubmitting = ref(false)
const addCandidates = ref([])
const addSelected = ref([])
const addLoading = ref(false)

async function openAddCases() {
  addVisible.value = true
  addSelected.value = []
  addCandidates.value = []
  addLoading.value = true
  try {
    // 只有「已确认」的用例才能进测试单，所以直接按 stage 过滤，
    // 免得多点几下才被后端一句 400 挡回来。
    const data = await caseApi.list({
      productId: detail.value.productId,
      stage: 'confirmed',
      page: 1,
      size: 200,
    })
    const included = new Set((detail.value.cases || []).map((c) => c.caseId))
    addCandidates.value = (data.list || []).filter((c) => !included.has(c.id))
  } finally {
    addLoading.value = false
  }
}

async function submitAddCases() {
  if (addSelected.value.length === 0) {
    ElMessage.warning('请选择要纳入的用例')
    return
  }
  addSubmitting.value = true
  try {
    detail.value = await testTaskApi.addCases(id.value, { caseIds: addSelected.value })
    ElMessage.success('已纳入')
    addVisible.value = false
    timeline.value?.reload()
  } catch {
    // 提示由 HTTP 层负责
  } finally {
    addSubmitting.value = false
  }
}

async function removeCase(row) {
  try {
    await ElMessageBox.confirm(`确定把「${row.caseTitle}」移出本测试单吗？`, '请确认', {
      confirmButtonText: '移出',
      cancelButtonText: '取消',
      type: 'warning',
    })
  } catch {
    return
  }
  try {
    detail.value = await testTaskApi.removeCase(id.value, row.caseId)
    ElMessage.success('已移出')
    timeline.value?.reload()
  } catch {
    // 提示由 HTTP 层负责
  }
}

// ---- 指派单内用例 ----
const assignVisible = ref(false)
const assignSubmitting = ref(false)
const assignTarget = ref(null)
const assignTo = ref(0)

function openAssignCase(row) {
  assignTarget.value = row
  assignTo.value = row.assignedTo || 0
  assignVisible.value = true
}

async function submitAssignCase() {
  if (!assignTo.value) {
    ElMessage.warning('请选择执行人')
    return
  }
  assignSubmitting.value = true
  try {
    detail.value = await testTaskApi.assignCase(id.value, assignTarget.value.caseId, {
      assignedTo: assignTo.value,
    })
    ElMessage.success('已指派')
    assignVisible.value = false
  } catch {
    // 提示由 HTTP 层负责
  } finally {
    assignSubmitting.value = false
  }
}

// ---- 记录执行 ----
// 步骤的「操作 / 期望」从用例当前内容取，只让填「实际结果」。
// 提交时后端仍会按用例版本重新回填一份快照，客户端传的文字不作数 ——
// 这正是要的：历史记录不能由提交者重新定义。
const runVisible = ref(false)
const runSubmitting = ref(false)
const runLoading = ref(false)
const runTarget = ref(null)
const runForm = ref({ result: 'pass', note: '', duration: 0, steps: [] })

async function openRun(row) {
  if (!canRun) return
  runTarget.value = row
  runForm.value = { result: 'pass', note: '', duration: 0, steps: [] }
  runVisible.value = true
  runLoading.value = true
  try {
    const caseDetail = await caseApi.get(row.caseId)
    runForm.value.steps = (caseDetail.steps || []).map((s) => ({
      order: s.order,
      step: s.step,
      expect: s.expect,
      actual: '',
    }))
  } finally {
    runLoading.value = false
  }
}

async function submitRun() {
  runSubmitting.value = true
  try {
    await testTaskApi.run(id.value, runTarget.value.caseId, {
      result: runForm.value.result,
      note: runForm.value.note,
      duration: runForm.value.duration,
      // 只提交实际结果；step/expect 由后端按用例版本回填成快照。
      steps: runForm.value.steps.map((s) => ({ order: s.order, actual: s.actual })),
    })
    ElMessage.success('已记录执行结果')
    runVisible.value = false
    await afterAction()
  } catch {
    // 提示由 HTTP 层负责（如测试单已结单）
  } finally {
    runSubmitting.value = false
  }
}

// ---- 执行历史 ----
const historyVisible = ref(false)
const historyLoading = ref(false)
const historyCase = ref(null)
const historyRuns = ref([])

async function openHistory(row) {
  historyCase.value = row
  historyVisible.value = true
  historyLoading.value = true
  try {
    const data = await testTaskApi.runs(id.value, { caseId: row.caseId, page: 1, size: 50 })
    historyRuns.value = data.list || []
  } finally {
    historyLoading.value = false
  }
}

function runTagType(result) {
  if (result === 'pass') return 'success'
  if (result === 'fail') return 'danger'
  return 'warning'
}

onMounted(load)
</script>

<template>
  <div v-loading="loading">
    <el-page-header :content="detail?.name || '测试单详情'" @back="router.back()">
      <template #extra>
        <ObjectActions
          v-if="detail"
          object-type="testtask"
          :object-id="detail.id"
          :actions="detail.actions || []"
          :version="0"
          @done="afterAction"
          @edit="openEdit"
        />
      </template>
    </el-page-header>

    <el-row v-if="detail" :gutter="16" class="mt">
      <el-col :md="17">
        <el-card shadow="never">
          <template #header>
            <div class="head">
              <span class="xl-mono xl-dim">{{ detail.code }}</span>
              <StatusTag :status="detail.status" :label="labelOf(testTaskStatuses, detail.status)" />
              <el-tag
                v-if="detail.result"
                :type="detail.result === 'pass' ? 'success' : detail.result === 'fail' ? 'danger' : 'warning'"
                size="small"
                disable-transitions
              >
                结论：{{ labelOf(testRunResults, detail.result) }}
              </el-tag>
            </div>
          </template>

          <div class="xl-fields">
            <div class="xl-field"><span class="label">产品</span><span class="value">{{ detail.productName }}</span></div>
            <div class="xl-field"><span class="label">项目</span><span class="value">{{ detail.projectName || '-' }}</span></div>
            <div class="xl-field"><span class="label">迭代</span><span class="value">{{ detail.executionName || '-' }}</span></div>
            <div class="xl-field"><span class="label">构建版本</span><span class="value">{{ detail.buildName || '-' }}</span></div>
            <div class="xl-field">
              <span class="label">优先级</span>
              <span class="value"><el-tag :type="priTagType(detail.pri)" size="small" disable-transitions>{{ priLabel(detail.pri) }}</el-tag></span>
            </div>
            <div class="xl-field"><span class="label">负责人</span><span class="value">{{ detail.assignedToName || '未指派' }}</span></div>
            <div class="xl-field"><span class="label">计划开始</span><span class="value">{{ formatDate(detail.begin) || '-' }}</span></div>
            <div class="xl-field"><span class="label">计划结束</span><span class="value">{{ formatDate(detail.end) || '-' }}</span></div>
            <div class="xl-field"><span class="label">实际开始</span><span class="value">{{ actorLine('', detail.startedAt) }}</span></div>
            <div class="xl-field"><span class="label">实际结束</span><span class="value">{{ actorLine('', detail.finishedAt) }}</span></div>
            <div class="xl-field"><span class="label">最近执行</span><span class="value">{{ actorLine('', detail.lastRunDate) }}</span></div>
            <div class="xl-field"><span class="label">创建</span><span class="value">{{ actorLine(detail.openedByName, detail.openedDate) }}</span></div>
          </div>

          <template v-if="detail.report">
            <el-divider content-position="left">测试报告</el-divider>
            <div class="xl-prose">{{ detail.report }}</div>
          </template>
        </el-card>

        <!-- 进度 -->
        <el-card shadow="never" class="mt">
          <template #header><span>执行进度</span></template>
          <div class="stats">
            <div v-for="card in summaryCards" :key="card.key" class="stat">
              <div class="stat-num" :class="`tone-${card.tone}`">{{ card.value }}</div>
              <div class="stat-cap xl-dim">{{ card.label }}</div>
            </div>
          </div>
        </el-card>

        <!-- 单内用例 -->
        <el-card shadow="never" class="mt">
          <template #header>
            <div class="head between">
              <span>单内用例（{{ detail.caseCount }}）</span>
              <el-button v-if="canEdit" size="small" @click="openAddCases">
                <el-icon><Plus /></el-icon> 纳入用例
              </el-button>
            </div>
          </template>

          <el-table :data="detail.cases || []" border stripe row-key="caseId">
            <el-table-column prop="order" label="#" width="52" align="center" />
            <el-table-column label="用例" min-width="220" show-overflow-tooltip>
              <template #default="{ row }">
                <el-link type="primary" @click="router.push(`/testcases/${row.caseId}`)">
                  {{ row.caseTitle || `用例 #${row.caseId}` }}
                </el-link>
              </template>
            </el-table-column>
            <el-table-column label="版本" width="120">
              <template #default="{ row }">
                <span>v{{ row.caseVersion }}</span>
                <!-- 纳入之后用例又改过：单内的结果是对着旧版跑的 -->
                <el-tooltip
                  v-if="row.caseCurrentVersion > row.caseVersion"
                  content="用例已更新，本单结果针对旧版本"
                >
                  <el-tag type="warning" size="small" disable-transitions>已更新</el-tag>
                </el-tooltip>
              </template>
            </el-table-column>
            <el-table-column label="最新结论" width="96">
              <template #default="{ row }">
                <el-tag v-if="row.lastResult" :type="runTagType(row.lastResult)" size="small" disable-transitions>
                  {{ labelOf(testRunResults, row.lastResult) }}
                </el-tag>
                <span v-else class="xl-dim">未执行</span>
              </template>
            </el-table-column>
            <el-table-column label="执行次数" width="86" align="center">
              <template #default="{ row }">{{ row.runCount }}</template>
            </el-table-column>
            <el-table-column label="执行人" width="100">
              <template #default="{ row }">{{ row.assignedToName || '-' }}</template>
            </el-table-column>
            <el-table-column label="上次执行" width="120">
              <template #default="{ row }">{{ formatDate(row.lastRunDate) || '-' }}</template>
            </el-table-column>
            <el-table-column label="操作" width="230">
              <template #default="{ row }">
                <!-- 已结单不再接受结果（后端会 409），按钮也就不必摆出来。 -->
                <el-button
                  v-if="canRun && detail.status !== 'closed'"
                  size="small"
                  type="primary"
                  plain
                  @click="openRun(row)"
                >
                  记录执行
                </el-button>
                <el-button v-if="canAssign" size="small" plain @click="openAssignCase(row)">指派</el-button>
                <el-button size="small" plain @click="openHistory(row)">历史</el-button>
                <el-button v-if="canEdit" size="small" text type="danger" @click="removeCase(row)">移出</el-button>
              </template>
            </el-table-column>
          </el-table>
          <el-empty v-if="!detail.cases?.length" description="还没有纳入用例" :image-size="72">
            <el-button v-if="canEdit" type="primary" @click="openAddCases">纳入用例</el-button>
          </el-empty>
        </el-card>

        <!-- 描述 -->
        <el-card v-if="detail.desc" shadow="never" class="mt">
          <template #header><span>描述</span></template>
          <div class="xl-prose">{{ detail.desc }}</div>
        </el-card>

        <!-- 评论 -->
        <el-card shadow="never" class="mt">
          <template #header><span>评论</span></template>
          <CommentsPanel ref="comments" object-type="testtask" :object-id="detail.id" />
        </el-card>
      </el-col>

      <el-col :md="7">
        <el-card shadow="never">
          <template #header><span>操作历史</span></template>
          <ActionTimeline ref="timeline" object-type="testtask" :object-id="detail.id" />
        </el-card>
      </el-col>
    </el-row>

    <!-- 编辑 -->
    <el-dialog v-model="editVisible" title="编辑测试单" width="640px">
      <el-form ref="editFormRef" :model="editForm" :rules="editRules" label-width="96px">
        <el-form-item label="所属产品">
          <!-- 产品不在编辑里改：用例是挂在产品下的，换产品会让单内用例失去归属。 -->
          <el-input :model-value="detail?.productName" disabled />
        </el-form-item>
        <el-form-item label="所属项目">
          <el-select
            v-model="editForm.projectId"
            clearable
            filterable
            style="width: 100%"
            @change="loadEditExecutions"
          >
            <el-option v-for="p in editProjects" :key="p.id" :label="p.name" :value="p.id" />
          </el-select>
        </el-form-item>
        <el-form-item label="所属迭代">
          <el-select v-model="editForm.executionId" clearable style="width: 100%">
            <el-option v-for="e in editExecutions" :key="e.id" :label="e.name" :value="e.id" />
          </el-select>
        </el-form-item>
        <el-form-item label="单名" prop="name">
          <el-input v-model="editForm.name" maxlength="255" />
        </el-form-item>
        <el-form-item label="描述">
          <el-input v-model="editForm.desc" type="textarea" :rows="3" />
        </el-form-item>
        <el-form-item label="优先级">
          <el-select v-model="editForm.pri" style="width: 100%">
            <el-option v-for="opt in priOptions" :key="opt.value" :label="opt.label" :value="opt.value" />
          </el-select>
        </el-form-item>
        <el-form-item label="负责人">
          <UserSelect v-model="editForm.assignedTo" include-empty />
        </el-form-item>
        <el-row :gutter="12">
          <el-col :span="12">
            <el-form-item label="计划开始">
              <el-date-picker v-model="editForm.begin" type="date" value-format="YYYY-MM-DD" placeholder="可选" style="width: 100%" />
            </el-form-item>
          </el-col>
          <el-col :span="12">
            <el-form-item label="计划结束">
              <el-date-picker v-model="editForm.end" type="date" value-format="YYYY-MM-DD" placeholder="可选" style="width: 100%" />
            </el-form-item>
          </el-col>
        </el-row>
      </el-form>
      <template #footer>
        <el-button @click="editVisible = false">取消</el-button>
        <el-button type="primary" :loading="editSubmitting" @click="submitEdit">保存</el-button>
      </template>
    </el-dialog>

    <!-- 纳入用例 -->
    <el-dialog v-model="addVisible" title="纳入用例" width="720px">
      <el-alert
        type="info"
        :closable="false"
        show-icon
        title="只列出本产品下已确认、且尚未纳入本单的用例。"
      />
      <el-table
        v-loading="addLoading"
        :data="addCandidates"
        border
        max-height="380"
        row-key="id"
        style="margin-top: 12px"
        @selection-change="(rows) => (addSelected = rows.map((r) => r.id))"
      >
        <el-table-column type="selection" width="44" />
        <el-table-column label="编号" width="110">
          <template #default="{ row }"><span class="xl-mono xl-dim">{{ row.code }}</span></template>
        </el-table-column>
        <el-table-column label="标题" min-width="240" show-overflow-tooltip>
          <template #default="{ row }">{{ row.title }}</template>
        </el-table-column>
        <el-table-column label="阶段" width="100">
          <template #default="{ row }">{{ labelOf(caseStages, row.stage) }}</template>
        </el-table-column>
        <el-table-column label="步骤数" width="80" align="center">
          <template #default="{ row }">{{ row.stepCount }}</template>
        </el-table-column>
      </el-table>
      <el-empty v-if="!addLoading && addCandidates.length === 0" description="没有可纳入的用例" :image-size="64" />
      <template #footer>
        <el-button @click="addVisible = false">取消</el-button>
        <el-button type="primary" :loading="addSubmitting" @click="submitAddCases">纳入</el-button>
      </template>
    </el-dialog>

    <!-- 指派执行人 -->
    <el-dialog v-model="assignVisible" title="指派用例执行人" width="480px">
      <el-form label-width="88px">
        <el-form-item label="用例">
          <span>{{ assignTarget?.caseTitle }}</span>
        </el-form-item>
        <el-form-item label="执行人" required>
          <UserSelect v-model="assignTo" placeholder="请选择" include-empty />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="assignVisible = false">取消</el-button>
        <el-button type="primary" :loading="assignSubmitting" @click="submitAssignCase">确定</el-button>
      </template>
    </el-dialog>

    <!-- 记录执行 -->
    <el-dialog v-model="runVisible" title="记录执行结果" width="720px">
      <div class="run-head">
        <span class="xl-dim">用例</span>
        <span>{{ runTarget?.caseTitle }}</span>
      </div>
      <el-form label-width="88px" v-loading="runLoading">
        <el-form-item label="本次结论" required>
          <el-radio-group v-model="runForm.result">
            <el-radio-button value="pass">通过</el-radio-button>
            <el-radio-button value="fail">失败</el-radio-button>
            <el-radio-button value="blocked">阻塞</el-radio-button>
          </el-radio-group>
        </el-form-item>

        <el-form-item label="逐步骤">
          <div class="steps">
            <div v-for="(step, idx) in runForm.steps" :key="idx" class="step">
              <div class="step-title">{{ idx + 1 }}. {{ step.step }}</div>
              <div class="step-expect xl-dim">期望：{{ step.expect || '（未填写）' }}</div>
              <el-input
                v-model="step.actual"
                type="textarea"
                :rows="2"
                placeholder="实际结果（留空表示与期望一致）"
              />
            </div>
            <el-empty v-if="runForm.steps.length === 0" description="这条用例没有步骤" :image-size="56" />
          </div>
        </el-form-item>

        <el-form-item label="备注">
          <el-input v-model="runForm.note" type="textarea" :rows="2" maxlength="500" placeholder="复现环境、失败现象等" />
        </el-form-item>
        <el-form-item label="耗时(小时)">
          <el-input-number v-model="runForm.duration" :min="0" :step="0.5" :precision="1" />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="runVisible = false">取消</el-button>
        <el-button type="primary" :loading="runSubmitting" @click="submitRun">提交结果</el-button>
      </template>
    </el-dialog>

    <!-- 执行历史 -->
    <el-dialog v-model="historyVisible" :title="`执行历史 · ${historyCase?.caseTitle || ''}`" width="720px">
      <div v-loading="historyLoading">
        <el-empty v-if="!historyLoading && historyRuns.length === 0" description="还没有执行记录" :image-size="64" />
        <div v-for="run in historyRuns" :key="run.id" class="run-item">
          <div class="run-title">
            <el-tag :type="runTagType(run.result)" size="small" disable-transitions>
              {{ labelOf(testRunResults, run.result) }}
            </el-tag>
            <span class="xl-dim">{{ run.executedByName || '' }} · {{ formatDateTime(run.executedDate) }}</span>
            <span v-if="run.duration" class="xl-dim">耗时 {{ hours(run.duration) }}h</span>
            <span class="xl-dim">用例 v{{ run.caseVersion }}</span>
          </div>
          <div v-if="run.note" class="xl-prose">{{ run.note }}</div>
          <div v-for="(s, i) in run.steps || []" :key="i" class="run-step">
            <span class="xl-dim">{{ i + 1 }}.</span>
            <span>{{ s.step }}</span>
            <span v-if="s.actual" class="actual">实际：{{ s.actual }}</span>
          </div>
        </div>
      </div>
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

.head.between {
  justify-content: space-between;
}

.stats {
  display: flex;
  gap: 12px;
}

.stat {
  flex: 1;
  max-width: 140px;
  padding: 12px 16px;
  border: 1px solid #ebeef5;
  border-radius: var(--xl-card-radius);
  text-align: center;
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

.tone-success {
  color: #67c23a;
}

.tone-danger {
  color: #f56c6c;
}

.tone-warning {
  color: #e6a23c;
}

.run-head {
  display: flex;
  gap: 8px;
  margin-bottom: 12px;
}

.steps {
  width: 100%;
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.step {
  padding: 10px 12px;
  border: 1px solid #ebeef5;
  border-radius: var(--xl-card-radius);
}

.step-title {
  font-weight: 600;
  margin-bottom: 4px;
}

.step-expect {
  font-size: 12px;
  margin-bottom: 6px;
}

.run-item {
  padding: 10px 0;
  border-bottom: 1px dashed #ebeef5;
}

.run-title {
  display: flex;
  align-items: center;
  gap: 10px;
  margin-bottom: 4px;
}

.run-step {
  display: flex;
  gap: 8px;
  line-height: 1.7;
  font-size: 13px;
}

.run-step .actual {
  color: #e6a23c;
}
</style>
