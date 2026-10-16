<script setup>
// 我的工作台。
//
// 设计取向就一句话：**只放「等你动手」的事**。
// 后端已经把「派给我的未完成任务」「派给我的未解决缺陷」「等我评审的需求」
// 「未读通知」算好了，前端不再自己拉全量列表来过滤 ——
// 工作台一旦开始塞「最近动态」之类的东西，真正该做的事就被淹了。
import { computed, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'

import { executionApi, projectApi, workbenchApi } from '@/api'
import BurndownChart from '@/components/BurndownChart.vue'
import StatusTag from '@/components/StatusTag.vue'
import { useAuthStore } from '@/stores/auth'
import { priLabel, priTagType } from '@/utils/dict'
import { daysAgo, daysLater, formatDate, fromNow, hours, isOverdue } from '@/utils/format'

const router = useRouter()
const auth = useAuthStore()

const loading = ref(false)
const data = ref(null)

// ---- 燃尽图 ----
//
// 范围只能选「我参与的项目/迭代」：接口本身按可见性过滤，
// 所以这里拿到的列表天然就是我能看的那些。
const canReport = computed(() => auth.can('report-browse'))
const projects = ref([])
const executions = ref([])
const burndown = ref(null)
const burndownLoading = ref(false)
const form = ref({
  projectId: 0,
  executionId: 0,
  begin: daysAgo(6),
  end: daysLater(1),
})

async function load() {
  loading.value = true
  try {
    data.value = await workbenchApi.get()
  } finally {
    loading.value = false
  }
}

async function loadProjects() {
  if (!canReport.value) return
  projects.value = await projectApi.brief({})
}

async function onProjectChange(projectId) {
  form.value.executionId = 0
  executions.value = []
  burndown.value = null
  if (!projectId) return
  const result = await executionApi.list({ projectId, size: 100 })
  executions.value = result.list || []
  await loadBurndown()
}

async function loadBurndown() {
  const scope = form.value.executionId
    ? { executionId: form.value.executionId }
    : form.value.projectId
      ? { projectId: form.value.projectId }
      : null
  if (!scope) {
    burndown.value = null
    return
  }
  burndownLoading.value = true
  try {
    burndown.value = await workbenchApi.burndown({
      ...scope,
      begin: form.value.begin,
      end: form.value.end,
    })
  } catch {
    burndown.value = null
  } finally {
    burndownLoading.value = false
  }
}

onMounted(async () => {
  await load()
  await loadProjects()
})
</script>

<template>
  <div v-loading="loading">
    <div class="xl-page-title">
      <h2>我的工作台</h2>
      <el-button @click="load">
        <el-icon><Refresh /></el-icon>
        <span>刷新</span>
      </el-button>
    </div>

    <el-row :gutter="16">
      <el-col :xs="12" :sm="8" :md="4">
        <el-card shadow="hover" class="stat" @click="router.push({ name: 'tasks' })">
          <div class="num">{{ data?.counts.taskWait ?? 0 }}</div>
          <div class="label">待开始的任务</div>
        </el-card>
      </el-col>
      <el-col :xs="12" :sm="8" :md="4">
        <el-card shadow="hover" class="stat" @click="router.push({ name: 'tasks' })">
          <div class="num">{{ data?.counts.taskDoing ?? 0 }}</div>
          <div class="label">进行中的任务</div>
        </el-card>
      </el-col>
      <el-col :xs="12" :sm="8" :md="4">
        <el-card shadow="hover" class="stat">
          <div class="num danger">{{ data?.counts.taskOverdue ?? 0 }}</div>
          <div class="label">已逾期</div>
        </el-card>
      </el-col>
      <el-col :xs="12" :sm="8" :md="4">
        <el-card shadow="hover" class="stat" @click="router.push({ name: 'bugs' })">
          <div class="num warning">{{ data?.counts.bugActive ?? 0 }}</div>
          <div class="label">待解决的缺陷</div>
        </el-card>
      </el-col>
      <el-col :xs="12" :sm="8" :md="4">
        <el-card shadow="hover" class="stat">
          <div class="num">{{ data?.counts.storyReview ?? 0 }}</div>
          <div class="label">等我评审的需求</div>
        </el-card>
      </el-col>
      <el-col :xs="12" :sm="8" :md="4">
        <el-card shadow="hover" class="stat" @click="router.push({ name: 'notices' })">
          <div class="num primary">{{ data?.counts.noticeUnread ?? 0 }}</div>
          <div class="label">未读通知</div>
        </el-card>
      </el-col>
    </el-row>

    <el-row :gutter="16" class="mt">
      <el-col :md="14">
        <el-card shadow="never">
          <template #header>
            <div class="card-head">
              <span>我的任务</span>
              <el-button text type="primary" @click="router.push({ name: 'tasks' })">全部</el-button>
            </div>
          </template>
          <el-empty v-if="!data?.tasks?.length" description="没有待办任务，难得清静" />
          <el-table
            v-else
            :data="data.tasks"
            size="small"
            @row-click="(row) => router.push(`/tasks/${row.id}`)"
          >
            <el-table-column prop="name" label="任务" min-width="200" show-overflow-tooltip />
            <el-table-column prop="projectName" label="项目" width="130" show-overflow-tooltip />
            <el-table-column label="状态" width="88">
              <template #default="{ row }">
                <StatusTag :status="row.status" :label="row.statusLabel" />
              </template>
            </el-table-column>
            <el-table-column label="优先级" width="92">
              <template #default="{ row }">
                <el-tag :type="priTagType(row.pri)" size="small" disable-transitions>
                  {{ priLabel(row.pri) }}
                </el-tag>
              </template>
            </el-table-column>
            <el-table-column label="进度" width="120">
              <template #default="{ row }">
                <el-progress :percentage="row.progress || 0" :stroke-width="10" />
              </template>
            </el-table-column>
            <el-table-column label="截止" width="100">
              <template #default="{ row }">
                <span :class="{ overdue: row.overdue || isOverdue(row.deadline) }">
                  {{ formatDate(row.deadline) || '-' }}
                </span>
              </template>
            </el-table-column>
            <el-table-column label="工时" width="92">
              <template #default="{ row }">
                <span class="xl-dim">{{ hours(row.consumed) }}/{{ hours(row.estimate) }}h</span>
              </template>
            </el-table-column>
          </el-table>
        </el-card>
      </el-col>

      <el-col :md="10">
        <el-card shadow="never" class="mb">
          <template #header>
            <div class="card-head"><span>待我评审的需求</span></div>
          </template>
          <el-empty v-if="!data?.reviews?.length" description="没有待评审的需求" />
          <div v-for="item in data?.reviews || []" :key="item.storyId" class="review-item">
            <el-link type="primary" @click="router.push(`/stories/${item.storyId}`)">
              {{ item.title }}
            </el-link>
            <div class="sub xl-dim">
              {{ item.productName }} · 版本 v{{ item.version }} ·
              {{ item.openedByName }} 于 {{ fromNow(item.openedDate) }} 提交
            </div>
          </div>
        </el-card>

        <el-card shadow="never">
          <template #header>
            <div class="card-head">
              <span>我的缺陷</span>
              <el-button text type="primary" @click="router.push({ name: 'bugs' })">全部</el-button>
            </div>
          </template>
          <el-empty v-if="!data?.bugs?.length" description="没有被派给你的缺陷" />
          <div v-for="item in data?.bugs || []" :key="item.id" class="review-item">
            <el-link type="primary" @click="router.push(`/bugs/${item.id}`)">
              <span class="xl-mono xl-dim">{{ item.code }}</span> {{ item.title }}
            </el-link>
            <div class="sub xl-dim">
              {{ item.productName }} · {{ item.typeLabel }} ·
              <span :class="{ overdue: item.overdue }">
                截止 {{ formatDate(item.deadline) || '未定' }}
              </span>
            </div>
          </div>
        </el-card>
      </el-col>
    </el-row>

    <el-card v-if="canReport" shadow="never" class="mt">
      <template #header>
        <div class="card-head">
          <span>燃尽图</span>
          <div class="burndown-form">
            <el-select
              v-model="form.projectId"
              placeholder="选择项目"
              size="small"
              style="width: 160px"
              @change="onProjectChange"
            >
              <el-option
                v-for="p in projects"
                :key="p.id"
                :value="p.id"
                :label="p.name"
              />
            </el-select>
            <el-select
              v-model="form.executionId"
              placeholder="整个项目"
              size="small"
              clearable
              style="width: 160px"
              @change="loadBurndown"
            >
              <el-option
                v-for="e in executions"
                :key="e.id"
                :value="e.id"
                :label="e.name"
              />
            </el-select>
            <el-date-picker
              v-model="form.begin"
              type="date"
              value-format="YYYY-MM-DD"
              placeholder="开始日期"
              size="small"
              style="width: 140px"
            />
            <span class="xl-dim">至</span>
            <el-date-picker
              v-model="form.end"
              type="date"
              value-format="YYYY-MM-DD"
              placeholder="结束日期"
              size="small"
              style="width: 140px"
            />
            <el-button size="small" type="primary" :loading="burndownLoading" @click="loadBurndown">
              查询
            </el-button>
          </div>
        </div>
      </template>
      <div v-loading="burndownLoading">
        <BurndownChart
          v-if="burndown"
          :days="burndown.days"
          :total-estimate="burndown.totalEstimate"
          :total-consumed="burndown.totalConsumed"
        />
        <div v-else class="xl-empty">选择项目（或再选一个迭代）查看燃尽曲线</div>
      </div>
    </el-card>
  </div>
</template>

<style scoped>
.stat {
  text-align: center;
  cursor: pointer;
  margin-bottom: 16px;
}

.stat .num {
  font-size: 26px;
  font-weight: 600;
  line-height: 1.2;
}

.stat .num.danger {
  color: #f56c6c;
}

.stat .num.warning {
  color: #e6a23c;
}

.stat .num.primary {
  color: #409eff;
}

.stat .label {
  color: var(--xl-text-dim);
  font-size: 13px;
}

.mt {
  margin-top: 8px;
}

.mb {
  margin-bottom: 16px;
}

.card-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
}

.review-item {
  padding: 8px 0;
  border-bottom: 1px dashed #ebeef5;
}

.review-item:last-child {
  border-bottom: none;
}

.sub {
  font-size: 12px;
  margin-top: 2px;
}

.overdue {
  color: #f56c6c;
}

.burndown-form {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}
</style>
