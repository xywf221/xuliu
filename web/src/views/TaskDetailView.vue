<script setup>
// 任务详情。
//
// 一屏之内回答四件事：这条任务是什么（内容）、处在什么状态（生命周期）、
// 工时花在哪（报工记录与成员分工）、发生过什么（历史与评论）。
// 动作按钮全部来自后端的 TransitionResp，前端不自己判断「这个状态能不能完成」。
import { computed, onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'

import { taskApi } from '@/api'
import ActionTimeline from '@/components/ActionTimeline.vue'
import CommentsPanel from '@/components/CommentsPanel.vue'
import ObjectActions from '@/components/ObjectActions.vue'
import StatusTag from '@/components/StatusTag.vue'
import UserSelect from '@/components/UserSelect.vue'
import { labelOf, priLabel, priOptions, priTagType, taskModes, taskTypes } from '@/utils/dict'
import { formatDate, formatDateTime, hours, isOverdue, today } from '@/utils/format'

const route = useRoute()
const router = useRouter()

const id = computed(() => Number(route.params.id))
const loading = ref(false)
const detail = ref(null)

// 终态：不再流转，也就不该再报工时。
const terminalStatuses = ['done', 'cancel', 'closed']
function isTerminal(status) {
  return terminalStatuses.includes(status)
}

async function load() {
  loading.value = true
  try {
    detail.value = await taskApi.get(id.value)
  } finally {
    loading.value = false
  }
}

onMounted(load)

// 逾期只在「还没完」的时候才刺眼：已完成的任务留着红字只是噪音。
const overdue = computed(
  () => !!detail.value?.deadline && isOverdue(detail.value.deadline) && !isTerminal(detail.value.status),
)

const timeline = ref(null)
const comments = ref(null)

async function afterAction() {
  await load()
  timeline.value?.reload()
  comments.value?.reload()
}

// ---- 编辑 ----
// ObjectActions 里 kind=edit 的动作交回这里处理：表单太大，不适合塞进弹窗组件。
const editVisible = ref(false)
const editSubmitting = ref(false)
const editFormRef = ref(null)
const editForm = ref({})
const editRules = {
  name: [{ required: true, message: '请输入任务名称', trigger: 'blur' }],
}

function openEdit() {
  const d = detail.value
  editForm.value = {
    // 版本号是乐观锁：不带它，两个人同时改就会后写覆盖先写。
    version: d.version,
    name: d.name,
    type: d.type,
    mode: d.mode,
    pri: d.pri,
    estimate: d.estimate,
    deadline: d.deadline,
    estStarted: d.estStarted,
    assignedTo: d.assignedTo,
    desc: d.desc,
  }
  editVisible.value = true
}

async function submitEdit() {
  const ok = await editFormRef.value?.validate().catch(() => false)
  if (!ok) return

  editSubmitting.value = true
  try {
    await taskApi.update(id.value, editForm.value)
    ElMessage.success('已保存')
    editVisible.value = false
    await load()
  } catch {
    // 版本冲突等错误由 HTTP 层提示。
  } finally {
    editSubmitting.value = false
  }
}

// ---- 报工时 ----
const effortVisible = ref(false)
const effortSubmitting = ref(false)
const effortForm = ref({ consumed: 0, left: null, date: today(), work: '' })

// 能不能报工时，问后端而不是查状态表。
//
// 状态机里 recordeffort 的来源只有 doing —— 未开始（wait）和已暂停（pause）
// 同样被拒。若前端自己列一张「终态」名单来禁用按钮，就会漏掉这两个，
// 让人填完整张表单再被 409 打回来。后端的 actions 里此刻有没有这一项，
// 就是唯一的答案；enabled 为假则表示状态允许但缺权限点。
const effortAction = computed(() =>
  (detail.value?.actions || []).find((a) => a.action === 'recordeffort'),
)
const effortBlocked = computed(() => !effortAction.value)
const effortPermHint = computed(() => {
  const a = effortAction.value
  return a && !a.enabled ? `需要权限：${a.perm}` : ''
})

function openEffort() {
  effortForm.value = { consumed: 0, left: null, date: today(), work: '' }
  effortVisible.value = true
}

async function submitEffort() {
  const consumed = Number(effortForm.value.consumed)
  if (!consumed || consumed <= 0) {
    ElMessage.warning('消耗工时必须大于 0')
    return
  }
  effortSubmitting.value = true
  try {
    const payload = { ...effortForm.value, consumed }
    // 剩余工时留空时交给后端按「原剩余 − 本次消耗」推算，前端不复制这段算法，
    // 免得两边口径不一致（后端还会拒绝算出来为负的情况）。
    if (payload.left === null || payload.left === '') delete payload.left
    // 多人并行任务只有团队成员能报工；这里刻意不传 userId，
    // 由服务端按当前登录用户归属 —— 前端选人容易把工时记到别人头上。
    await taskApi.recordEffort(id.value, payload)
    ElMessage.success('已记录工时')
    effortVisible.value = false
    await load()
  } catch {
    // 提示由 HTTP 层负责（如任务状态不允许报工、剩余工时算成负数）。
  } finally {
    effortSubmitting.value = false
  }
}
</script>

<template>
  <div v-loading="loading">
    <el-page-header :content="detail?.name || '任务详情'" @back="router.back()">
      <template #extra>
        <ObjectActions
          v-if="detail"
          object-type="task"
          :object-id="detail.id"
          :actions="detail.actions || []"
          :version="detail.version"
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
              <span class="xl-mono xl-dim">{{ detail.number }}</span>
              <StatusTag :status="detail.status" :label="detail.statusLabel" />
              <span class="xl-dim">v{{ detail.version }}</span>
            </div>
          </template>

          <div class="xl-fields">
            <div class="xl-field">
              <span class="label">项目</span>
              <span class="value">{{ detail.projectName || '-' }}</span>
            </div>
            <div class="xl-field">
              <span class="label">迭代</span>
              <span class="value">{{ detail.executionName || '-' }}</span>
            </div>
            <div class="xl-field">
              <span class="label">产品</span>
              <span class="value">{{ detail.productName || '-' }}</span>
            </div>
            <div class="xl-field">
              <span class="label">模块</span>
              <span class="value">{{ detail.moduleName || '-' }}</span>
            </div>
            <div class="xl-field">
              <span class="label">关联需求</span>
              <span class="value">
                <template v-if="detail.storyId">
                  <el-link type="primary" @click="router.push(`/stories/${detail.storyId}`)">
                    {{ detail.storyTitle || `需求#${detail.storyId}` }}
                  </el-link>
                  <el-tag
                    v-if="detail.storyChanged"
                    type="warning"
                    size="small"
                    class="ml"
                    disable-transitions
                  >
                    需求已变更
                  </el-tag>
                </template>
                <span v-else>-</span>
              </span>
            </div>
            <div class="xl-field">
              <span class="label">类型</span>
              <span class="value">{{ detail.typeLabel || labelOf(taskTypes, detail.type) }}</span>
            </div>
            <div class="xl-field">
              <span class="label">模式</span>
              <span class="value">{{ detail.modeLabel || labelOf(taskModes, detail.mode) }}</span>
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
              <span class="label">预计工时</span>
              <span class="value">{{ hours(detail.estimate) }} 小时</span>
            </div>
            <div class="xl-field">
              <span class="label">消耗工时</span>
              <span class="value">{{ hours(detail.consumed) }} 小时</span>
            </div>
            <div class="xl-field">
              <span class="label">剩余工时</span>
              <span class="value">{{ hours(detail.left) }} 小时</span>
            </div>
            <div class="xl-field">
              <span class="label">进度</span>
              <span class="value">
                <el-progress :percentage="detail.progress || 0" :stroke-width="8" />
              </span>
            </div>
            <div class="xl-field">
              <span class="label">截止日期</span>
              <span class="value">
                <span :class="{ overdue }">{{ formatDate(detail.deadline) || '-' }}</span>
              </span>
            </div>
            <div class="xl-field">
              <span class="label">负责人</span>
              <span class="value">{{ detail.assignedToName || '未指派' }}</span>
            </div>
            <div class="xl-field">
              <span class="label">创建</span>
              <span class="value">
                {{ detail.openedByName }} · {{ formatDateTime(detail.openedDate) }}
              </span>
            </div>
            <div class="xl-field">
              <span class="label">实际开始</span>
              <span class="value">{{ formatDateTime(detail.realStarted) || '-' }}</span>
            </div>
            <div class="xl-field">
              <span class="label">完成</span>
              <span class="value">
                <template v-if="detail.finishedDate">
                  {{ formatDateTime(detail.finishedDate) }}
                  <span class="xl-dim">· {{ detail.finishedByName }}</span>
                </template>
                <template v-else>-</template>
              </span>
            </div>
            <div v-if="detail.closedReason" class="xl-field">
              <span class="label">关闭原因</span>
              <span class="value">{{ detail.closedReason }}</span>
            </div>
          </div>

          <el-divider content-position="left">任务描述</el-divider>
          <div class="xl-prose">{{ detail.desc || '（未填写）' }}</div>
        </el-card>

        <el-card shadow="never" class="mt">
          <template #header>
            <div class="head">
              <span>工时</span>
              <span class="xl-dim">共 {{ detail.effortCount || 0 }} 条记录</span>
              <span class="spacer" />
              <el-tooltip
                :disabled="!effortBlocked && !effortPermHint"
                :content="effortBlocked ? '只有进行中的任务能记工时（未开始 / 已暂停 / 已结束都不行）' : effortPermHint"
              >
                <span>
                  <el-button
                    type="primary"
                    size="small"
                    :disabled="effortBlocked || (!!effortAction && !effortAction.enabled)"
                    @click="openEffort"
                  >
                    <el-icon><Plus /></el-icon>
                    <span>记工时</span>
                  </el-button>
                </span>
              </el-tooltip>
            </div>
          </template>

          <el-table :data="detail.efforts || []" size="small" border>
            <el-table-column label="日期" width="120">
              <template #default="{ row }">{{ formatDate(row.date) }}</template>
            </el-table-column>
            <el-table-column label="成员" width="100">
              <template #default="{ row }">{{ row.realname || `用户#${row.userId}` }}</template>
            </el-table-column>
            <el-table-column label="工作内容" min-width="180" show-overflow-tooltip>
              <template #default="{ row }">{{ row.work || '-' }}</template>
            </el-table-column>
            <el-table-column label="消耗" width="80">
              <template #default="{ row }">{{ hours(row.consumed) }}</template>
            </el-table-column>
            <el-table-column label="剩余" width="80">
              <template #default="{ row }">{{ hours(row.left) }}</template>
            </el-table-column>
          </el-table>
        </el-card>

        <el-card shadow="never" class="mt">
          <template #header>
            <div class="head">
              <span>评论</span>
              <span class="xl-dim">共 {{ detail.commentCount || 0 }} 条</span>
            </div>
          </template>
          <CommentsPanel ref="comments" object-type="task" :object-id="detail.id" />
        </el-card>
      </el-col>

      <el-col :md="8">
        <!-- 单人任务没有分工一说，成员卡只在多人任务且真有成员时才出现 -->
        <el-card v-if="detail.mode !== 'single' && detail.team?.length" shadow="never">
          <template #header><span>成员分工</span></template>
          <el-table :data="detail.team" size="small" border>
            <el-table-column label="成员" width="90">
              <template #default="{ row }">{{ row.realname || `用户#${row.userId}` }}</template>
            </el-table-column>
            <el-table-column label="预计" width="60">
              <template #default="{ row }">{{ hours(row.estimate) }}</template>
            </el-table-column>
            <el-table-column label="消耗" width="60">
              <template #default="{ row }">{{ hours(row.consumed) }}</template>
            </el-table-column>
            <el-table-column label="剩余" width="60">
              <template #default="{ row }">{{ hours(row.left) }}</template>
            </el-table-column>
            <el-table-column label="进度" width="70">
              <template #default="{ row }">{{ row.progress || 0 }}%</template>
            </el-table-column>
            <el-table-column label="状态" min-width="80">
              <template #default="{ row }">
                <StatusTag :status="row.status" :label="row.statusLabel" />
              </template>
            </el-table-column>
          </el-table>
        </el-card>

        <el-card shadow="never" :class="{ mt: detail.mode !== 'single' && detail.team?.length }">
          <template #header><span>操作历史</span></template>
          <ActionTimeline ref="timeline" object-type="task" :object-id="detail.id" />
        </el-card>
      </el-col>
    </el-row>

    <!-- 编辑 -->
    <el-dialog v-model="editVisible" title="编辑任务" width="640px">
      <el-form ref="editFormRef" :model="editForm" :rules="editRules" label-width="96px">
        <el-form-item label="名称" prop="name">
          <el-input v-model="editForm.name" maxlength="255" />
        </el-form-item>
        <el-row :gutter="12">
          <el-col :span="8">
            <el-form-item label="类型">
              <el-select v-model="editForm.type" style="width: 100%">
                <el-option v-for="opt in taskTypes" :key="opt.value" :value="opt.value" :label="opt.label" />
              </el-select>
            </el-form-item>
          </el-col>
          <el-col :span="8">
            <el-form-item label="模式">
              <el-select v-model="editForm.mode" style="width: 100%">
                <el-option v-for="opt in taskModes" :key="opt.value" :value="opt.value" :label="opt.label" />
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
            <el-form-item label="预计工时">
              <el-input-number v-model="editForm.estimate" :min="0" :step="0.5" :precision="1" />
            </el-form-item>
          </el-col>
          <el-col :span="8">
            <el-form-item label="截止日期">
              <el-date-picker
                v-model="editForm.deadline"
                type="date"
                value-format="YYYY-MM-DD"
                style="width: 100%"
              />
            </el-form-item>
          </el-col>
          <el-col :span="8">
            <el-form-item label="计划开始">
              <el-date-picker
                v-model="editForm.estStarted"
                type="date"
                value-format="YYYY-MM-DD"
                style="width: 100%"
              />
            </el-form-item>
          </el-col>
        </el-row>
        <el-form-item label="负责人">
          <UserSelect v-model="editForm.assignedTo" include-empty />
        </el-form-item>
        <el-form-item label="描述">
          <el-input v-model="editForm.desc" type="textarea" :rows="5" />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="editVisible = false">取消</el-button>
        <el-button type="primary" :loading="editSubmitting" @click="submitEdit">保存</el-button>
      </template>
    </el-dialog>

    <!-- 记工时 -->
    <el-dialog v-model="effortVisible" title="记工时" width="520px">
      <el-form :model="effortForm" label-width="120px">
        <el-form-item label="消耗工时" required>
          <el-input-number v-model="effortForm.consumed" :min="0.01" :step="0.5" :precision="1" />
          <span class="xl-dim hint">必须大于 0</span>
        </el-form-item>
        <el-form-item label="报工后的剩余">
          <el-input-number v-model="effortForm.left" :min="0" :step="0.5" :precision="1" />
          <div class="xl-dim hint">留空则由服务端按「原剩余 − 本次消耗」自动推算；填了以此为准。</div>
        </el-form-item>
        <el-form-item label="日期">
          <el-date-picker
            v-model="effortForm.date"
            type="date"
            value-format="YYYY-MM-DD"
            style="width: 100%"
          />
        </el-form-item>
        <el-form-item label="工作内容">
          <el-input
            v-model="effortForm.work"
            type="textarea"
            :rows="3"
            maxlength="255"
            show-word-limit
            placeholder="这次做了什么"
          />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="effortVisible = false">取消</el-button>
        <el-button type="primary" :loading="effortSubmitting" @click="submitEffort">提交</el-button>
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

.spacer {
  flex: 1;
}

.overdue {
  color: #f56c6c;
  font-weight: 500;
}

.ml {
  margin-left: 6px;
}

.hint {
  font-size: 12px;
  margin-left: 8px;
}
</style>
