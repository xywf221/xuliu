<script setup>
// 需求详情。
//
// 一屏之内回答四件事：这条需求是什么（内容）、处在什么状态（生命周期）、
// 谁在参与（评审与排期）、发生过什么（历史与评论）。
// 动作按钮全部来自后端的 TransitionResp，前端不自己判断「这个状态能不能关」。
import { computed, onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'

import { storyApi } from '@/api'
import ActionTimeline from '@/components/ActionTimeline.vue'
import CommentsPanel from '@/components/CommentsPanel.vue'
import ObjectActions from '@/components/ObjectActions.vue'
import StatusTag from '@/components/StatusTag.vue'
import UserSelect from '@/components/UserSelect.vue'
import { useAuthStore } from '@/stores/auth'
import {
  labelOf,
  priLabel,
  priOptions,
  priTagType,
  reviewResults,
  storyCategories,
  storyClosedReasons,
  storySources,
  storyStages,
  storyTypes,
} from '@/utils/dict'
import { formatDate, formatDateTime, hours } from '@/utils/format'

const route = useRoute()
const router = useRouter()
const auth = useAuthStore()

const id = computed(() => Number(route.params.id))
const loading = ref(false)
const detail = ref(null)

const canEdit = auth.can('story-edit')
const canChange = auth.can('story-change')

async function load() {
  loading.value = true
  try {
    detail.value = await storyApi.get(id.value)
  } finally {
    loading.value = false
  }
}

onMounted(load)

const versions = ref([])
async function loadVersions() {
  versions.value = await storyApi.versions(id.value)
}

// ---- 编辑 ----
const editVisible = ref(false)
const editSubmitting = ref(false)
const editForm = ref({})
const editRules = {
  title: [{ required: true, message: '请输入标题', trigger: 'blur' }],
}

function openEdit() {
  const d = detail.value
  editForm.value = {
    // 版本号是乐观锁：不带它，两个人同时改就会后写覆盖先写。
    version: d.version,
    title: d.title,
    spec: d.spec,
    verify: d.verify,
    type: d.type,
    category: d.category,
    source: d.source,
    pri: d.pri,
    estimate: d.estimate,
    assignedTo: d.assignedTo,
  }
  editVisible.value = true
}

async function submitEdit() {
  editSubmitting.value = true
  try {
    await storyApi.update(id.value, editForm.value)
    ElMessage.success('已保存')
    editVisible.value = false
    await load()
  } catch {
    // 版本冲突等错误由 HTTP 层提示。
  } finally {
    editSubmitting.value = false
  }
}

// ---- 变更（版本 +1）----
const changeVisible = ref(false)
const changeSubmitting = ref(false)
const changeForm = ref({})

function openChange() {
  const d = detail.value
  changeForm.value = {
    version: d.version,
    title: d.title,
    spec: d.spec,
    verify: d.verify,
    reason: '',
    comment: '',
  }
  changeVisible.value = true
}

async function submitChange() {
  if (!changeForm.value.reason.trim()) {
    ElMessage.warning('需求变更必须填写原因')
    return
  }
  changeSubmitting.value = true
  try {
    await storyApi.change(id.value, changeForm.value)
    ElMessage.success('已提交变更，进入变更中')
    changeVisible.value = false
    await load()
  } catch {
    // 提示由 HTTP 层负责。
  } finally {
    changeSubmitting.value = false
  }
}

// ObjectActions 里 kind=edit 的动作交回这里处理（表单太大，不适合塞进弹窗组件）。
function onAction(action) {
  if (action.action === 'edit') openEdit()
  else if (action.action === 'change') openChange()
}

const timeline = ref(null)
const comments = ref(null)

async function afterAction() {
  await load()
  timeline.value?.reload()
  comments.value?.reload()
}

// 评审行：谁、什么结论、什么时候。列表为空说明还没提交过评审。
const reviews = computed(() => detail.value?.reviews || [])

function reviewTagType(result) {
  switch (result) {
    case 'pass':
      return 'success'
    case 'reject':
      return 'danger'
    case 'revert':
      return 'warning'
    case 'clarify':
      return 'warning'
    default:
      return 'info'
  }
}
</script>

<template>
  <div v-loading="loading">
    <el-page-header :content="detail?.title || '需求详情'" @back="router.back()">
      <template #extra>
        <ObjectActions
          v-if="detail"
          object-type="story"
          :object-id="detail.id"
          :actions="detail.actions || []"
          :version="detail.version"
          @done="afterAction"
          @edit="onAction"
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
              <el-tag v-if="detail.storyChanged" type="warning" size="small" disable-transitions>
                内容已变更
              </el-tag>
              <span class="xl-dim">v{{ detail.version }}</span>
            </div>
          </template>

          <div class="xl-fields">
            <div class="xl-field">
              <span class="label">产品</span>
              <span class="value">{{ detail.productName }}</span>
            </div>
            <div class="xl-field">
              <span class="label">模块</span>
              <span class="value">{{ detail.moduleName || '-' }}</span>
            </div>
            <div class="xl-field">
              <span class="label">计划</span>
              <span class="value">{{ detail.planTitle || '-' }}</span>
            </div>
            <div class="xl-field">
              <span class="label">类型</span>
              <span class="value">{{ labelOf(storyTypes, detail.type) }}</span>
            </div>
            <div class="xl-field">
              <span class="label">分类</span>
              <span class="value">{{ labelOf(storyCategories, detail.category) }}</span>
            </div>
            <div class="xl-field">
              <span class="label">来源</span>
              <span class="value">{{ labelOf(storySources, detail.source) }}</span>
            </div>
            <div class="xl-field">
              <span class="label">阶段</span>
              <span class="value">{{ labelOf(storyStages, detail.stage, detail.stageLabel) }}</span>
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
              <span class="label">负责人</span>
              <span class="value">{{ detail.assignedToName || '未指派' }}</span>
            </div>
            <div class="xl-field">
              <span class="label">预计工时</span>
              <span class="value">{{ hours(detail.estimate) }} 小时</span>
            </div>
            <div class="xl-field">
              <span class="label">创建</span>
              <span class="value">
                {{ detail.openedByName }} · {{ formatDateTime(detail.openedDate) }}
              </span>
            </div>
            <div v-if="detail.closedReason" class="xl-field">
              <span class="label">关闭原因</span>
              <span class="value">{{ labelOf(storyClosedReasons, detail.closedReason) }}</span>
            </div>
          </div>

          <el-divider content-position="left">需求描述</el-divider>
          <div class="xl-prose">{{ detail.spec || '（未填写）' }}</div>

          <el-divider content-position="left">验收标准</el-divider>
          <div class="xl-prose">{{ detail.verify || '（未填写）' }}</div>
        </el-card>

        <el-card shadow="never" class="mt">
          <template #header>
            <div class="head">
              <span>评论</span>
              <span class="xl-dim">共 {{ detail.commentCount || 0 }} 条</span>
            </div>
          </template>
          <CommentsPanel ref="comments" object-type="story" :object-id="detail.id" />
        </el-card>
      </el-col>

      <el-col :md="8">
        <el-card shadow="never">
          <template #header><span>评审</span></template>
          <el-empty v-if="!reviews.length" description="还没有提交评审" :image-size="60" />
          <div v-for="(item, idx) in reviews" :key="idx" class="review">
            <div class="review-head">
              <strong>{{ item.reviewerName || `用户#${item.reviewer}` }}</strong>
              <el-tag :type="reviewTagType(item.result)" size="small" disable-transitions>
                {{ item.resultText || labelOf(reviewResults, item.result, '待评审') }}
              </el-tag>
            </div>
            <div class="xl-dim">{{ formatDateTime(item.reviewDate) }}</div>
          </div>
        </el-card>

        <el-card shadow="never" class="mt">
          <template #header>
            <div class="head">
              <span>排期</span>
              <span class="xl-dim">{{ detail.projects?.length || 0 }} 个项目</span>
            </div>
          </template>
          <el-empty v-if="!detail.projects?.length" description="还未排进任何项目" :image-size="60" />
          <div v-for="link in detail.projects || []" :key="link.id" class="review">
            <el-link type="primary" @click="router.push(`/projects/${link.projectId}`)">
              {{ link.projectName }}
            </el-link>
            <span class="xl-dim"> · {{ link.executionName || '未指定迭代' }}</span>
            <el-tag v-if="link.outdated" type="warning" size="small" class="ml" disable-transitions>
              排入版本 v{{ link.version }} 已过时
            </el-tag>
          </div>
        </el-card>

        <el-card shadow="never" class="mt">
          <template #header>
            <div class="head">
              <span>版本历史</span>
              <el-button text type="primary" size="small" @click="loadVersions">
                {{ versions.length ? '刷新' : '展开' }}
              </el-button>
            </div>
          </template>
          <el-empty v-if="versions.length === 0" description="点击展开查看历史版本" :image-size="60" />
          <div v-for="item in versions" :key="item.version" class="review">
            <strong>v{{ item.version }}</strong>
            <span class="xl-dim"> {{ formatDate(item.createdAt) }}</span>
            <div class="xl-dim">{{ item.title }}</div>
          </div>
        </el-card>

        <el-card shadow="never" class="mt">
          <template #header><span>操作历史</span></template>
          <ActionTimeline ref="timeline" object-type="story" :object-id="detail.id" />
        </el-card>
      </el-col>
    </el-row>

    <!-- 编辑 -->
    <el-dialog v-model="editVisible" title="编辑需求" width="640px">
      <el-form ref="editForm" :model="editForm" :rules="editRules" label-width="96px">
        <el-form-item label="标题" prop="title">
          <el-input v-model="editForm.title" maxlength="255" />
        </el-form-item>
        <el-row :gutter="12">
          <el-col :span="8">
            <el-form-item label="类型">
              <el-select v-model="editForm.type" style="width: 100%">
                <el-option v-for="opt in storyTypes" :key="opt.value" :value="opt.value" :label="opt.label" />
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
          <el-col :span="8">
            <el-form-item label="预计工时">
              <el-input-number v-model="editForm.estimate" :min="0" :step="0.5" :precision="1" />
            </el-form-item>
          </el-col>
        </el-row>
        <el-form-item label="负责人">
          <UserSelect v-model="editForm.assignedTo" include-empty />
        </el-form-item>
        <el-form-item label="需求描述">
          <el-input v-model="editForm.spec" type="textarea" :rows="5" />
        </el-form-item>
        <el-form-item label="验收标准">
          <el-input v-model="editForm.verify" type="textarea" :rows="3" />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="editVisible = false">取消</el-button>
        <el-button type="primary" :loading="editSubmitting" @click="submitEdit">保存</el-button>
      </template>
    </el-dialog>

    <!-- 变更 -->
    <el-dialog v-model="changeVisible" title="需求变更" width="640px">
      <el-alert
        type="info"
        :closable="false"
        show-icon
        title="变更会把版本号 +1，并保留旧版本内容"
        description="下游已排期的项目会看到「排入版本已过时」的提示，便于他们决定是否重新评估。"
        class="mb"
      />
      <el-form :model="changeForm" label-width="96px">
        <el-form-item label="变更原因" required>
          <el-input v-model="changeForm.reason" maxlength="255" placeholder="为什么改：需求变了、理解错了、还是漏了约束" />
        </el-form-item>
        <el-form-item label="标题">
          <el-input v-model="changeForm.title" maxlength="255" />
        </el-form-item>
        <el-form-item label="需求描述">
          <el-input v-model="changeForm.spec" type="textarea" :rows="5" />
        </el-form-item>
        <el-form-item label="验收标准">
          <el-input v-model="changeForm.verify" type="textarea" :rows="3" />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="changeVisible = false">取消</el-button>
        <el-button type="primary" :loading="changeSubmitting" @click="submitChange">
          提交变更
        </el-button>
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

.head {
  display: flex;
  align-items: center;
  gap: 10px;
}

.review {
  padding: 8px 0;
  border-bottom: 1px dashed #ebeef5;
}

.review:last-child {
  border-bottom: none;
}

.review-head {
  display: flex;
  align-items: center;
  gap: 8px;
}

.ml {
  margin-left: 6px;
}
</style>
