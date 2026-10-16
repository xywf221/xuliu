<script setup>
// 项目详情：概览 / 需求 / 迭代 / 团队 / 历史。
//
// 分页签而不是一屏堆完，是因为这五块的信息密度都很高，
// 挤在一起的结果是每块都看不清。
//
// 「排期」这件事的入口放在项目这一侧（而不是需求详情页）是有原因的：
// 校验条件全在项目域（项目得关联了该需求的产品、迭代得属于该项目），
// 从项目出发才有足够上下文做提示。
import { computed, onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ElMessage, ElMessageBox } from 'element-plus'

import { executionApi, productApi, projectApi, storyApi } from '@/api'
import ActionTimeline from '@/components/ActionTimeline.vue'
import CommentsPanel from '@/components/CommentsPanel.vue'
import ObjectActions from '@/components/ObjectActions.vue'
import StatusTag from '@/components/StatusTag.vue'
import UserSelect from '@/components/UserSelect.vue'
import { useAuthStore } from '@/stores/auth'
import { labelOf, projectModels } from '@/utils/dict'
import { formatDate, formatDateTime, hours } from '@/utils/format'

const route = useRoute()
const router = useRouter()
const auth = useAuthStore()

const id = computed(() => Number(route.params.id))
const loading = ref(false)
const detail = ref(null)
const activeTab = ref('overview')

const canTeamManage = auth.can('team-manage')
const canExecutionCreate = auth.can('execution-create')

async function load() {
  loading.value = true
  try {
    detail.value = await projectApi.get(id.value)
    products.value = detail.value.products || []
  } finally {
    loading.value = false
  }
}

onMounted(load)

const products = ref([])

// ---- 需求排期 ----
const links = ref([])
const linkLoading = ref(false)

async function loadLinks() {
  linkLoading.value = true
  try {
    links.value = await projectApi.stories(id.value)
  } finally {
    linkLoading.value = false
  }
}

const linkVisible = ref(false)
const linkSubmitting = ref(false)
const linkForm = ref({ storyId: 0, executionId: 0, order: 0 })
const storyOptions = ref([])
const storyLoading = ref(false)

async function searchStories(keywords) {
  storyLoading.value = true
  try {
    const data = await storyApi.list({ keywords, size: 50, status: 'active' })
    storyOptions.value = data.list || []
  } finally {
    storyLoading.value = false
  }
}

async function submitLink() {
  if (!linkForm.value.storyId) {
    ElMessage.warning('请选择要排入的需求')
    return
  }
  linkSubmitting.value = true
  try {
    await projectApi.linkStory(id.value, linkForm.value)
    ElMessage.success('已排入')
    linkVisible.value = false
    linkForm.value = { storyId: 0, executionId: 0, order: 0 }
    await Promise.all([loadLinks(), load()])
  } finally {
    linkSubmitting.value = false
  }
}

async function unlink(row) {
  try {
    await ElMessageBox.confirm(
      `把「${row.storyTitle}」移出本项目？任务不会一起删除，但它们会失去所属需求的上下文。`,
      '移出需求',
      { confirmButtonText: '移出', cancelButtonText: '取消', type: 'warning' },
    )
  } catch {
    return
  }
  await projectApi.unlinkStory(id.value, row.storyId)
  ElMessage.success('已移出')
  await Promise.all([loadLinks(), load()])
}

// ---- 迭代 ----
const executions = ref([])
const executionVisible = ref(false)
const executionSubmitting = ref(false)
const executionForm = ref({})

async function loadExecutions() {
  const data = await executionApi.list({ projectId: id.value, size: 100 })
  executions.value = data.list || []
}

function openExecutionCreate() {
  executionForm.value = {
    projectId: id.value,
    name: '',
    type: 'sprint',
    begin: detail.value?.begin || '',
    end: detail.value?.end || '',
    po: detail.value?.po || 0,
    pm: detail.value?.pm || 0,
  }
  executionVisible.value = true
}

async function submitExecution() {
  if (!executionForm.value.name.trim()) {
    ElMessage.warning('请输入迭代名称')
    return
  }
  executionSubmitting.value = true
  try {
    await executionApi.create(executionForm.value)
    ElMessage.success('已创建迭代')
    executionVisible.value = false
    await Promise.all([loadExecutions(), load()])
  } finally {
    executionSubmitting.value = false
  }
}

// 迭代的动作同样来自后端的状态机（每一行的 actions），
// 这里只把「可用的」列出来 —— 不能做的动作不显示，避免用户点了才被拒。
async function runExecutionAction(row, action) {
  try {
    await ElMessageBox.confirm(`确定要对迭代「${row.name}」执行「${action.label}」吗？`, '请确认', {
      confirmButtonText: '确定',
      cancelButtonText: '取消',
      type: action.danger ? 'warning' : 'info',
    })
  } catch {
    return
  }
  await executionApi.action(row.id, action.action)
  ElMessage.success('操作成功')
  await loadExecutions()
}

// ---- 团队 ----
const team = ref([])
const teamVisible = ref(false)
const teamSubmitting = ref(false)
const teamForm = ref({ userId: 0, role: 'dev', position: '' })

async function loadTeam() {
  team.value = await projectApi.team(id.value)
}

async function addMember() {
  if (!teamForm.value.userId) {
    ElMessage.warning('请选择成员')
    return
  }
  teamSubmitting.value = true
  try {
    await projectApi.addTeam(id.value, teamForm.value)
    ElMessage.success('已加入团队')
    teamVisible.value = false
    teamForm.value = { userId: 0, role: 'dev', position: '' }
    await Promise.all([loadTeam(), load()])
  } finally {
    teamSubmitting.value = false
  }
}

async function removeMember(row) {
  try {
    await ElMessageBox.confirm(`把「${row.realname}」移出项目团队？`, '移除成员', {
      confirmButtonText: '移除',
      cancelButtonText: '取消',
      type: 'warning',
    })
  } catch {
    return
  }
  await projectApi.removeTeam(id.value, row.userId)
  ElMessage.success('已移除')
  await Promise.all([loadTeam(), load()])
}

// ---- 按需加载分页签的数据 ----
//
// 只在切到某个页签时才查它要的数据：项目详情一进来就并发五个请求，
// 其中四个可能用户根本不看。
async function onTabChange(name) {
  if (name === 'stories' && links.value.length === 0) await loadLinks()
  if (name === 'executions' && executions.value.length === 0) await loadExecutions()
  if (name === 'team' && team.value.length === 0) await loadTeam()
  if (name === 'stories' || name === 'executions') await searchStories('')
}

const timeline = ref(null)
const comments = ref(null)

async function afterAction() {
  await load()
  timeline.value?.reload()
  comments.value?.reload()
}

const roleLabels = {
  PO: '产品负责',
  PM: '项目经理',
  QD: '测试负责',
  RD: '研发负责',
  dev: '开发',
  qa: '测试',
}
</script>

<template>
  <div v-loading="loading">
    <el-page-header :content="detail?.name || '项目详情'" @back="router.back()">
      <template #extra>
        <ObjectActions
          v-if="detail"
          object-type="project"
          :object-id="detail.id"
          :actions="detail.actions || []"
          @done="afterAction"
        />
      </template>
    </el-page-header>

    <el-card v-if="detail" shadow="never" class="mt">
      <el-tabs v-model="activeTab" @tab-change="onTabChange">
        <el-tab-pane label="概览" name="overview">
          <div class="xl-fields">
            <div class="xl-field">
              <span class="label">编号</span>
              <span class="value xl-mono">{{ detail.number }}</span>
            </div>
            <div class="xl-field">
              <span class="label">代号</span>
              <span class="value xl-mono">{{ detail.code }}</span>
            </div>
            <div class="xl-field">
              <span class="label">状态</span>
              <span class="value">
                <StatusTag :status="detail.status" :label="detail.statusLabel" />
              </span>
            </div>
            <div class="xl-field">
              <span class="label">管理模式</span>
              <span class="value">{{ detail.modelLabel || labelOf(projectModels, detail.model) }}</span>
            </div>
            <div class="xl-field">
              <span class="label">访问控制</span>
              <span class="value">{{ detail.acl === 'private' ? '私有' : '公开' }}</span>
            </div>
            <div class="xl-field">
              <span class="label">计划起止</span>
              <span class="value">
                {{ formatDate(detail.begin) || '未定' }} ~ {{ formatDate(detail.end) || '未定' }}
              </span>
            </div>
            <div class="xl-field">
              <span class="label">实际起止</span>
              <span class="value">
                {{ formatDate(detail.realBegan) || '未开始' }} ~ {{ formatDate(detail.realEnd) || '-' }}
              </span>
            </div>
            <div class="xl-field">
              <span class="label">工期</span>
              <span class="value">{{ detail.days }} 天</span>
            </div>
            <div class="xl-field">
              <span class="label">工时</span>
              <span class="value">
                预计 {{ hours(detail.estimate) }} / 消耗 {{ hours(detail.consumed) }} /
                剩余 {{ hours(detail.left) }}
              </span>
            </div>
            <div class="xl-field">
              <span class="label">进度</span>
              <span class="value">
                <el-progress :percentage="detail.progress || 0" :stroke-width="12" />
              </span>
            </div>
            <div class="xl-field">
              <span class="label">PM</span>
              <span class="value">{{ detail.pmName || '-' }}</span>
            </div>
            <div class="xl-field">
              <span class="label">PO</span>
              <span class="value">{{ detail.poName || '-' }}</span>
            </div>
            <div class="xl-field">
              <span class="label">创建</span>
              <span class="value">{{ detail.openedByName }} · {{ formatDate(detail.openedDate) }}</span>
            </div>
          </div>

          <el-divider content-position="left">关联产品</el-divider>
          <div class="chips">
            <el-tag v-for="p in products" :key="p.productId" size="large" disable-transitions>
              {{ p.productName }}
              <span class="xl-dim">{{ p.productCode }}</span>
            </el-tag>
          </div>

          <el-divider content-position="left">项目描述</el-divider>
          <div class="xl-prose">{{ detail.desc || '（未填写）' }}</div>
        </el-tab-pane>

        <el-tab-pane name="stories">
          <template #label>需求（{{ detail.storyCount ?? 0 }}）</template>
          <div class="tab-actions">
            <el-button type="primary" size="small" @click="linkVisible = true">
              <el-icon><Plus /></el-icon>
              <span>排入需求</span>
            </el-button>
          </div>
          <el-table v-loading="linkLoading" :data="links" border stripe>
            <el-table-column label="需求" min-width="240">
              <template #default="{ row }">
                <el-link type="primary" @click="router.push(`/stories/${row.storyId}`)">
                  {{ row.storyTitle }}
                </el-link>
                <el-tag v-if="row.outdated" type="warning" size="small" class="ml" disable-transitions>
                  排入版本 v{{ row.version }} 已过时
                </el-tag>
              </template>
            </el-table-column>
            <el-table-column label="迭代" width="160">
              <template #default="{ row }">{{ row.executionName || '未指定' }}</template>
            </el-table-column>
            <el-table-column label="排入版本" width="100">
              <template #default="{ row }">v{{ row.version }}</template>
            </el-table-column>
            <el-table-column label="排序" width="80">
              <template #default="{ row }">{{ row.order }}</template>
            </el-table-column>
            <el-table-column label="操作" width="100">
              <template #default="{ row }">
                <el-button text type="danger" size="small" @click="unlink(row)">移出</el-button>
              </template>
            </el-table-column>
          </el-table>
        </el-tab-pane>

        <el-tab-pane name="executions">
          <template #label>迭代（{{ detail.executionCount ?? 0 }}）</template>
          <div class="tab-actions">
            <el-button v-if="canExecutionCreate" type="primary" size="small" @click="openExecutionCreate">
              <el-icon><Plus /></el-icon>
              <span>新建迭代</span>
            </el-button>
          </div>
          <el-table :data="executions" border stripe>
            <el-table-column label="迭代" min-width="180">
              <template #default="{ row }">
                <el-link type="primary" @click="router.push({ name: 'tasks', query: { projectId: row.projectId, executionId: row.id } })">
                  {{ row.name }}
                </el-link>
              </template>
            </el-table-column>
            <el-table-column label="类型" width="90">
              <template #default="{ row }">{{ row.typeLabel }}</template>
            </el-table-column>
            <el-table-column label="状态" width="96">
              <template #default="{ row }">
                <StatusTag :status="row.status" :label="row.statusLabel" />
              </template>
            </el-table-column>
            <el-table-column label="起止" width="190">
              <template #default="{ row }">
                <span class="xl-dim">
                  {{ formatDate(row.begin) || '未定' }} ~ {{ formatDate(row.end) || '未定' }}
                </span>
              </template>
            </el-table-column>
            <el-table-column label="进度" width="130">
              <template #default="{ row }">
                <el-progress :percentage="row.progress || 0" :stroke-width="10" />
              </template>
            </el-table-column>
            <el-table-column label="任务" width="70">
              <template #default="{ row }">{{ row.taskCount ?? 0 }}</template>
            </el-table-column>
            <el-table-column label="工时" width="120">
              <template #default="{ row }">
                <span class="xl-dim">
                  {{ hours(row.consumed) }}/{{ hours(row.estimate) }}h
                </span>
              </template>
            </el-table-column>
            <el-table-column label="操作" width="120">
              <template #default="{ row }">
                <el-dropdown
                  v-if="(row.actions || []).filter((a) => a.enabled).length"
                  @command="(cmd) => runExecutionAction(row, cmd)"
                >
                  <el-button text type="primary" size="small">
                    动作
                    <el-icon><ArrowDown /></el-icon>
                  </el-button>
                  <template #dropdown>
                    <el-dropdown-menu>
                      <el-dropdown-item
                        v-for="action in (row.actions || []).filter((a) => a.enabled)"
                        :key="action.action"
                        :command="action"
                      >
                        {{ action.label }}
                      </el-dropdown-item>
                    </el-dropdown-menu>
                  </template>
                </el-dropdown>
              </template>
            </el-table-column>
          </el-table>
        </el-tab-pane>

        <el-tab-pane name="team">
          <template #label>团队（{{ detail.memberCount ?? 0 }}）</template>
          <div class="tab-actions">
            <el-button v-if="canTeamManage" type="primary" size="small" @click="teamVisible = true">
              <el-icon><Plus /></el-icon>
              <span>加入成员</span>
            </el-button>
          </div>
          <el-alert
            type="info"
            :closable="false"
            show-icon
            class="mb"
            title="迭代团队是项目团队的子集"
            description="往迭代里加人前，他必须先在这个项目的团队里。这样「能看到项目里哪些人」这一份名单就够用了。"
          />
          <el-table :data="team" border stripe>
            <el-table-column label="成员" min-width="160">
              <template #default="{ row }">
                {{ row.realname || `用户#${row.userId}` }}
                <span class="xl-dim xl-mono">{{ row.account }}</span>
              </template>
            </el-table-column>
            <el-table-column label="角色" width="110">
              <template #default="{ row }">{{ roleLabels[row.role] || row.role }}</template>
            </el-table-column>
            <el-table-column label="可用工时" width="100">
              <template #default="{ row }">{{ hours(row.hours) }}</template>
            </el-table-column>
            <el-table-column label="消耗" width="90">
              <template #default="{ row }">{{ hours(row.consumed) }}</template>
            </el-table-column>
            <el-table-column label="剩余" width="90">
              <template #default="{ row }">{{ hours(row.left) }}</template>
            </el-table-column>
            <el-table-column label="任务数" width="90">
              <template #default="{ row }">
                <el-link
                  type="primary"
                  @click="router.push({ name: 'tasks', query: { projectId: detail.id, assignedTo: row.userId } })"
                >
                  {{ row.taskCount ?? 0 }}
                </el-link>
              </template>
            </el-table-column>
            <el-table-column label="加入时间" width="120">
              <template #default="{ row }">{{ formatDate(row.join) || '-' }}</template>
            </el-table-column>
            <el-table-column v-if="canTeamManage" label="操作" width="90">
              <template #default="{ row }">
                <el-button text type="danger" size="small" @click="removeMember(row)">移除</el-button>
              </template>
            </el-table-column>
          </el-table>
        </el-tab-pane>

        <el-tab-pane label="历史" name="history">
          <el-row :gutter="16">
            <el-col :md="14">
              <ActionTimeline ref="timeline" object-type="project" :object-id="detail.id" />
            </el-col>
            <el-col :md="10">
              <CommentsPanel ref="comments" object-type="project" :object-id="detail.id" />
            </el-col>
          </el-row>
        </el-tab-pane>
      </el-tabs>
    </el-card>

    <!-- 排入需求 -->
    <el-dialog v-model="linkVisible" title="把需求排入项目" width="560px">
      <el-form :model="linkForm" label-width="88px">
        <el-form-item label="需求" required>
          <el-select
            v-model="linkForm.storyId"
            filterable
            remote
            :remote-method="searchStories"
            :loading="storyLoading"
            placeholder="输入标题搜索（默认列出最近的激活需求）"
            style="width: 100%"
          >
            <el-option
              v-for="s in storyOptions"
              :key="s.id"
              :value="s.id"
              :label="`${s.title}（${s.productName}）`"
            />
          </el-select>
        </el-form-item>
        <el-form-item label="排入迭代">
          <el-select v-model="linkForm.executionId" clearable placeholder="可稍后再排" style="width: 100%">
            <el-option v-for="e in executions" :key="e.id" :value="e.id" :label="e.name" />
          </el-select>
        </el-form-item>
        <el-form-item label="排序">
          <el-input-number v-model="linkForm.order" :min="0" />
        </el-form-item>
      </el-form>
      <div class="xl-dim tip">
        服务端会校验「项目是否关联了该需求的产品」—— 跨产品需求排不进来。
      </div>
      <template #footer>
        <el-button @click="linkVisible = false">取消</el-button>
        <el-button type="primary" :loading="linkSubmitting" @click="submitLink">排入</el-button>
      </template>
    </el-dialog>

    <!-- 新建迭代 -->
    <el-dialog v-model="executionVisible" title="新建迭代" width="560px">
      <el-form :model="executionForm" label-width="88px">
        <el-form-item label="名称" required>
          <el-input v-model="executionForm.name" maxlength="90" placeholder="如 迭代一 / 2026 Q1 冲刺" />
        </el-form-item>
        <el-form-item label="类型">
          <el-select v-model="executionForm.type" style="width: 100%">
            <el-option value="sprint" label="冲刺" />
            <el-option value="stage" label="阶段" />
            <el-option value="kanban" label="看板" />
          </el-select>
        </el-form-item>
        <el-form-item label="计划开始">
          <el-date-picker
            v-model="executionForm.begin"
            type="date"
            value-format="YYYY-MM-DD"
            style="width: 100%"
          />
        </el-form-item>
        <el-form-item label="计划结束">
          <el-date-picker
            v-model="executionForm.end"
            type="date"
            value-format="YYYY-MM-DD"
            style="width: 100%"
          />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="executionVisible = false">取消</el-button>
        <el-button type="primary" :loading="executionSubmitting" @click="submitExecution">创建</el-button>
      </template>
    </el-dialog>

    <!-- 加入成员 -->
    <el-dialog v-model="teamVisible" title="加入项目团队" width="480px">
      <el-form :model="teamForm" label-width="88px">
        <el-form-item label="成员" required>
          <UserSelect v-model="teamForm.userId" />
        </el-form-item>
        <el-form-item label="角色">
          <el-select v-model="teamForm.role" style="width: 100%">
            <el-option value="dev" label="开发" />
            <el-option value="qa" label="测试" />
            <el-option value="PO" label="产品负责" />
            <el-option value="PM" label="项目经理" />
            <el-option value="QD" label="测试负责" />
            <el-option value="RD" label="研发负责" />
          </el-select>
        </el-form-item>
        <el-form-item label="岗位">
          <el-input v-model="teamForm.position" maxlength="60" />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="teamVisible = false">取消</el-button>
        <el-button type="primary" :loading="teamSubmitting" @click="addMember">加入</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<style scoped>
.mt {
  margin-top: 16px;
}

.mb {
  margin-bottom: 12px;
}

.ml {
  margin-left: 6px;
}

.chips {
  display: flex;
  gap: 8px;
  flex-wrap: wrap;
}

.tab-actions {
  display: flex;
  justify-content: flex-end;
  margin-bottom: 12px;
}

.tip {
  font-size: 12px;
}
</style>
