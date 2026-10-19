<script setup>
// 用例详情。
//
// 生命周期：待编辑 → 提交评审 → 评审中 → (通过→已确认 / 驳回→已拒绝) → 可编辑
// （已确认和评审中都不能编辑，避免绕过评审）。动作按钮全部来自后端下发，
// 前端只管画。
import { computed, onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'

import { caseApi, productApi, storyApi } from '@/api'
import ActionTimeline from '@/components/ActionTimeline.vue'
import CommentsPanel from '@/components/CommentsPanel.vue'
import ObjectActions from '@/components/ObjectActions.vue'
import StatusTag from '@/components/StatusTag.vue'
import { useAuthStore } from '@/stores/auth'
import { caseStages, caseStatuses, caseTypes, labelOf, priLabel, priTagType } from '@/utils/dict'
import { formatDateTime } from '@/utils/format'

const route = useRoute()
const router = useRouter()
const auth = useAuthStore()

const id = computed(() => Number(route.params.id))
const loading = ref(false)
const detail = ref(null)

const canEdit = auth.can('testcase-edit')

async function load() {
  loading.value = true
  try {
    detail.value = await caseApi.get(id.value)
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

// ---- 下拉数据 ----
const products = ref([])
const modules = ref([])
const stories = ref([])

// 用例只能挂产品下**用例类型**的模块树，其余三棵树的节点要剪掉。
async function loadModules(productId) {
  if (!productId) {
    modules.value = []
    return
  }
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
  if (!productId) return
  const data = await storyApi.list({ productId, size: 100 })
  stories.value = data.list || []
}

// ---- 编辑 ----
const editVisible = ref(false)
const editSubmitting = ref(false)
const editFormRef = ref(null)
const editForm = ref({})

const editRules = {
  title: [{ required: true, message: '请输入用例标题', trigger: 'blur' }],
}

async function openEdit() {
  if (!canEdit) return
  const d = detail.value
  editForm.value = {
    productId: d.productId,
    moduleId: d.moduleId,
    storyId: d.storyId,
    title: d.title,
    precondition: d.precondition || '',
    keywords: d.keywords || '',
    desc: d.desc || '',
    type: d.type,
    pri: d.pri,
    steps: d.steps ? [...d.steps] : [],
    order: d.order || 0,
  }
  editVisible.value = true
  await Promise.all([loadModules(d.productId), loadStories(d.productId)])
}

async function onProductChange(productId) {
  editForm.value.moduleId = 0
  editForm.value.storyId = 0
  await Promise.all([loadModules(productId), loadStories(productId)])
}

async function submitEdit() {
  const ok = await editFormRef.value?.validate().catch(() => false)
  if (!ok) return

  editSubmitting.value = true
  try {
    const payload = { ...editForm.value }
    payload.steps = editForm.value.steps.map((s) => ({ step: s.step, expect: s.expect }))
    await caseApi.update(id.value, payload)
    ElMessage.success('已保存')
    editVisible.value = false
    await load()
  } catch {
    // 提示由 HTTP 层负责
  } finally {
    editSubmitting.value = false
  }
}

// ---- 步骤管理 ----
function addStep() {
  editForm.value.steps.push({ step: '', expect: '' })
}

function removeStep(idx) {
  editForm.value.steps.splice(idx, 1)
}

onMounted(() => {
  load()
  loadOptions()
})

async function loadOptions() {
  products.value = await productApi.brief({})
}
</script>

<template>
  <div v-loading="loading">
    <el-page-header :content="detail?.title || '用例详情'" @back="router.back()">
      <template #extra>
        <!-- objectType 用后端的对象类型 "case"：动作分发与评论/历史共用同一套命名，
             路由地址 testcases 只是页面路径，不是对象类型。 -->
        <ObjectActions
          v-if="detail"
          object-type="case"
          :object-id="detail.id"
          :actions="detail.actions || []"
          :version="detail.version"
          @done="afterAction"
          @edit="openEdit"
        />
      </template>
    </el-page-header>

    <el-row v-if="detail" :gutter="16" class="mt">
      <el-col :md="18">
        <el-card shadow="never">
          <template #header>
            <div class="head">
              <span class="xl-mono xl-dim">{{ detail.code }}</span>
              <StatusTag :status="detail.stage" :label="labelOf(caseStages, detail.stage)" />
              <el-tag type="info" size="small" disable-transitions>v{{ detail.version }}</el-tag>
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
              <span class="label">关联需求</span>
              <span class="value">
                <el-link v-if="detail.storyId" type="primary" @click="router.push(`/stories/${detail.storyId}`)">
                  {{ detail.storyTitle }}
                </el-link>
                <span v-else>-</span>
              </span>
            </div>
            <div class="xl-field">
              <span class="label">类型</span>
              <span class="value">{{ labelOf(caseTypes, detail.type) }}</span>
            </div>
            <div class="xl-field">
              <span class="label">优先级</span>
              <span class="value">
                <el-tag :type="priTagType(detail.pri)" size="small" disable-transitions>{{ priLabel(detail.pri) }}</el-tag>
              </span>
            </div>
            <div class="xl-field">
              <span class="label">可执行性</span>
              <span class="value">
                <StatusTag :status="detail.status" :label="labelOf(caseStatuses, detail.status)" />
              </span>
            </div>
            <div class="xl-field">
              <span class="label">步骤数</span>
              <span class="value">{{ detail.stepCount }}</span>
            </div>
            <div class="xl-field">
              <span class="label">创建人</span>
              <span class="value">{{ actorLine(detail.openedByName, detail.openedDate) }}</span>
            </div>
            <div class="xl-field" v-if="detail.reviewedBy">
              <span class="label">评审人</span>
              <span class="value">{{ actorLine(detail.reviewedByName, detail.reviewedDate) }}</span>
            </div>
          </div>
        </el-card>

        <el-card shadow="never" class="mt">
          <template #header><span>前置条件</span></template>
          <div class="xl-prose">{{ detail.precondition || '（未填写）' }}</div>
        </el-card>

        <el-card shadow="never" class="mt">
          <template #header><span>操作步骤</span></template>
          <div v-if="detail.steps && detail.steps.length" class="steps-list">
            <div v-for="(step, idx) in detail.steps" :key="idx" class="step-item">
              <div class="step-head">
                <span class="step-num">第{{ idx + 1 }}步</span>
                <span class="step-label xl-dim">操作</span>
              </div>
              <div class="step-body">{{ step.step }}</div>
              <div class="step-head" style="margin-top: 12px">
                <span class="step-num"></span>
                <span class="step-label xl-dim">期望结果</span>
              </div>
              <div class="step-body">{{ step.expect }}</div>
            </div>
          </div>
          <el-empty v-else description="暂无步骤" />
        </el-card>

        <el-card shadow="never" class="mt">
          <template #header><span>关键词</span></template>
          <div class="xl-prose">{{ detail.keywords || '（未填写）' }}</div>
        </el-card>

        <el-card shadow="never" class="mt">
          <template #header>
            <div class="head">
              <span>评论</span>
              <span class="xl-dim">共 {{ detail.commentCount || 0 }} 条</span>
            </div>
          </template>
          <CommentsPanel ref="comments" object-type="case" :object-id="detail.id" />
        </el-card>
      </el-col>

      <el-col :md="6">
        <el-card shadow="never">
          <template #header><span>操作历史</span></template>
          <ActionTimeline ref="timeline" object-type="case" :object-id="detail.id" />
        </el-card>
      </el-col>
    </el-row>

    <!-- 编辑弹窗 -->
    <el-dialog v-model="editVisible" title="编辑用例" width="760px">
      <el-form ref="editFormRef" :model="editForm" :rules="editRules" label-width="96px">
        <el-row :gutter="12">
          <el-col :span="8">
            <el-form-item label="所属产品" prop="productId">
              <el-select v-model="editForm.productId" filterable style="width: 100%" @change="onProductChange">
                <el-option v-for="p in products" :key="p.id" :label="p.name" :value="p.id" />
              </el-select>
            </el-form-item>
          </el-col>
          <el-col :span="8">
            <el-form-item label="所属模块">
              <el-select v-model="editForm.moduleId" clearable filterable style="width: 100%">
                <el-option v-for="m in modules" :key="m.id" :label="m.name" :value="m.id" />
              </el-select>
            </el-form-item>
          </el-col>
          <el-col :span="8">
            <el-form-item label="关联需求">
              <el-select v-model="editForm.storyId" clearable filterable style="width: 100%">
                <el-option v-for="s in stories" :key="s.id" :label="s.title" :value="s.id" />
              </el-select>
            </el-form-item>
          </el-col>
        </el-row>

        <el-form-item label="标题" prop="title">
          <el-input v-model="editForm.title" maxlength="255" />
        </el-form-item>
        <el-form-item label="前置条件">
          <el-input v-model="editForm.precondition" type="textarea" :rows="2" />
        </el-form-item>
        <el-form-item label="关键词">
          <el-input v-model="editForm.keywords" maxlength="255" />
        </el-form-item>
        <el-form-item label="用例描述">
          <el-input v-model="editForm.desc" type="textarea" :rows="3" />
        </el-form-item>

        <el-divider>操作步骤</el-divider>

        <el-space v-for="(step, idx) in editForm.steps" :key="idx" wrap>
          <el-input v-model="step.step" placeholder="操作内容" maxlength="2000" style="width: 300px" />
          <el-input v-model="step.expect" placeholder="期望结果" maxlength="2000" style="width: 200px" />
          <el-button @click="removeStep(idx)">删除</el-button>
        </el-space>

        <el-button style="margin-top: 8px" @click="addStep"><el-icon><Plus /></el-icon> 添加步骤</el-button>

        <el-form-item label="排序号">
          <el-input-number v-model="editForm.order" :min="0" />
        </el-form-item>
      </el-form>

      <template #footer>
        <el-button @click="editVisible = false">取消</el-button>
        <el-button type="primary" :loading="editSubmitting" @click="submitEdit">保存</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<style scoped>
.mt { margin-top: 16px; }
.head { display: flex; align-items: center; gap: 10px; }
.steps-list { display: flex; flex-direction: column; gap: 16px; }
.step-item { padding: 12px; border: 1px solid #ebeef5; border-radius: var(--xl-card-radius); }
.step-head { display: flex; gap: 12px; align-items: center; font-size: 12px; color: var(--xl-text-dim); }
.step-num { font-weight: 600; color: #409eff; min-width: 60px; }
.step-body { line-height: 1.7; white-space: pre-wrap; }
</style>
