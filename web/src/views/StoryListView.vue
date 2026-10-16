<script setup>
// 需求列表。
//
// 需求的完整生命周期都在这里起头：新建（草稿）→ 提交评审 → 评审通过（激活）
// → 变更 → 关闭 → 激活。列表上只放「看」和「新建」，
// 状态流转一律进详情页 —— 那是有上下文、需要填理由的操作，
// 摆在列表上一排按钮只会让人误点。
import { computed, onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'

import { productApi, storyApi } from '@/api'
import StatusTag from '@/components/StatusTag.vue'
import UserSelect from '@/components/UserSelect.vue'
import { useList } from '@/composables/useList'
import { useAuthStore } from '@/stores/auth'
import {
  priLabel,
  priOptions,
  priTagType,
  storyCategories,
  storySources,
  storyTypes,
} from '@/utils/dict'
import { formatDate, hours } from '@/utils/format'

const route = useRoute()
const router = useRouter()
const auth = useAuthStore()

const canCreate = auth.can('story-create')

// 产品筛选默认取 URL 上的 productId：从产品页点「需求」跳过来时，
// 用户看到的就是那个产品的需求，而不是全站的需求。
const { list, total, page, size, loading, filters, reload, reset } = useList(storyApi.list, {
  productId: Number(route.query.productId) || 0,
  status: '',
  pri: 0,
  assignedTo: 0,
  keywords: '',
})

const products = ref([])
onMounted(async () => {
  products.value = await productApi.brief({})
})

// 需求状态：与后端 model.StoryStatus* 一致。
const statusOptions = [
  { value: 'draft', label: '草稿' },
  { value: 'reviewing', label: '评审中' },
  { value: 'active', label: '激活' },
  { value: 'changing', label: '变更中' },
  { value: 'closed', label: '已关闭' },
]

// ---- 新建 ----
const dialogVisible = ref(false)
const submitting = ref(false)
const formRef = ref(null)
const form = ref(emptyForm())

function emptyForm() {
  return {
    productId: Number(route.query.productId) || 0,
    moduleId: 0,
    title: '',
    spec: '',
    verify: '',
    type: 'story',
    category: 'feature',
    source: 'po',
    pri: 3,
    estimate: null,
    assignedTo: 0,
    reviewers: [],
  }
}

// 模块树随产品变化：换产品后原来选的模块就失效了，必须清掉，
// 否则会把 A 产品的模块挂到 B 产品的需求上（后端会拒绝，但那是白跑一趟）。
const modules = ref([])
async function loadModules(productId) {
  modules.value = []
  form.value.moduleId = 0
  if (!productId) return
  modules.value = await productApi.modules(productId)
}

const rules = {
  productId: [{ required: true, message: '请选择产品', trigger: 'change' }],
  title: [{ required: true, message: '请输入需求标题', trigger: 'blur' }],
}

function openCreate() {
  form.value = emptyForm()
  dialogVisible.value = true
  if (form.value.productId) loadModules(form.value.productId)
}

async function submit() {
  const ok = await formRef.value?.validate().catch(() => false)
  if (!ok) return

  submitting.value = true
  try {
    const payload = { ...form.value }
    if (!payload.estimate) delete payload.estimate
    const created = await storyApi.create(payload)
    ElMessage.success('已创建需求')
    dialogVisible.value = false
    router.push(`/stories/${created.id}`)
  } catch {
    // 提示由 HTTP 层负责（如产品开了评审却没配评审人）。
  } finally {
    submitting.value = false
  }
}

const moduleOptions = computed(() => flatten(modules.value))
function flatten(nodes, out = []) {
  for (const node of nodes || []) {
    out.push({ id: node.id, name: node.name })
    if (node.children?.length) flatten(node.children, out)
  }
  return out
}

function storyTypeLabel(row) {
  return storyTypes.find((t) => t.value === row.type)?.label || row.type
}
</script>

<template>
  <el-card shadow="never">
    <div class="xl-page-title">
      <h2>需求</h2>
      <el-button v-if="canCreate" type="primary" @click="openCreate">
        <el-icon><Plus /></el-icon>
        <span>新建需求</span>
      </el-button>
    </div>

    <div class="xl-filters">
      <el-select
        v-model="filters.productId"
        placeholder="全部产品"
        clearable
        filterable
        style="width: 170px"
        @change="reload"
      >
        <el-option v-for="p in products" :key="p.id" :value="p.id" :label="p.name" />
      </el-select>
      <el-select v-model="filters.status" placeholder="状态" clearable style="width: 130px" @change="reload">
        <el-option v-for="opt in statusOptions" :key="opt.value" :value="opt.value" :label="opt.label" />
      </el-select>
      <el-select v-model="filters.pri" placeholder="优先级" clearable style="width: 120px" @change="reload">
        <el-option v-for="opt in priOptions" :key="opt.value" :value="opt.value" :label="opt.label" />
      </el-select>
      <div style="width: 170px">
        <UserSelect v-model="filters.assignedTo" placeholder="负责人" clearable @change="reload" />
      </div>
      <el-input
        v-model="filters.keywords"
        placeholder="标题 / 关键词"
        clearable
        style="width: 200px"
        @keyup.enter="reload"
        @clear="reload"
      />
      <el-button @click="reload">查询</el-button>
      <el-button text @click="reset">重置</el-button>
    </div>

    <el-table v-loading="loading" :data="list" border stripe row-key="id">
      <el-table-column label="编号" width="110">
        <template #default="{ row }">
          <span class="xl-mono xl-dim">{{ row.code }}</span>
        </template>
      </el-table-column>
      <el-table-column label="标题" min-width="240" show-overflow-tooltip>
        <template #default="{ row }">
          <el-link type="primary" @click="router.push(`/stories/${row.id}`)">{{ row.title }}</el-link>
        </template>
      </el-table-column>
      <el-table-column label="产品" width="130" show-overflow-tooltip>
        <template #default="{ row }">{{ row.productName }}</template>
      </el-table-column>
      <el-table-column label="类型" width="90">
        <template #default="{ row }">{{ storyTypeLabel(row) }}</template>
      </el-table-column>
      <el-table-column label="状态" width="96">
        <template #default="{ row }">
          <StatusTag :status="row.status" :label="row.statusLabel" />
        </template>
      </el-table-column>
      <el-table-column label="阶段" width="96">
        <template #default="{ row }">
          <span class="xl-dim">{{ row.stageLabel }}</span>
        </template>
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
      <el-table-column label="预计" width="80">
        <template #default="{ row }">{{ hours(row.estimate) }}</template>
      </el-table-column>
      <el-table-column label="版本" width="70">
        <template #default="{ row }">
          <span>v{{ row.version }}</span>
          <el-tag v-if="row.storyChanged" size="small" type="warning" class="ml">已变更</el-tag>
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
        @size-change="reload"
      />
    </div>

    <el-dialog v-model="dialogVisible" title="新建需求" width="720px">
      <el-form ref="formRef" :model="form" :rules="rules" label-width="96px">
        <el-row :gutter="12">
          <el-col :span="12">
            <el-form-item label="所属产品" prop="productId">
              <el-select
                v-model="form.productId"
                filterable
                style="width: 100%"
                @change="loadModules"
              >
                <el-option v-for="p in products" :key="p.id" :value="p.id" :label="p.name" />
              </el-select>
            </el-form-item>
          </el-col>
          <el-col :span="12">
            <el-form-item label="模块">
              <el-select v-model="form.moduleId" clearable style="width: 100%">
                <el-option
                  v-for="m in moduleOptions"
                  :key="m.id"
                  :value="m.id"
                  :label="m.name"
                />
              </el-select>
            </el-form-item>
          </el-col>
        </el-row>

        <el-form-item label="标题" prop="title">
          <el-input v-model="form.title" maxlength="255" />
        </el-form-item>

        <el-row :gutter="12">
          <el-col :span="8">
            <el-form-item label="类型">
              <el-select v-model="form.type" style="width: 100%">
                <el-option v-for="opt in storyTypes" :key="opt.value" :value="opt.value" :label="opt.label" />
              </el-select>
            </el-form-item>
          </el-col>
          <el-col :span="8">
            <el-form-item label="分类">
              <el-select v-model="form.category" style="width: 100%">
                <el-option
                  v-for="opt in storyCategories"
                  :key="opt.value"
                  :value="opt.value"
                  :label="opt.label"
                />
              </el-select>
            </el-form-item>
          </el-col>
          <el-col :span="8">
            <el-form-item label="来源">
              <el-select v-model="form.source" style="width: 100%">
                <el-option v-for="opt in storySources" :key="opt.value" :value="opt.value" :label="opt.label" />
              </el-select>
            </el-form-item>
          </el-col>
        </el-row>

        <el-row :gutter="12">
          <el-col :span="8">
            <el-form-item label="优先级">
              <el-select v-model="form.pri" style="width: 100%">
                <el-option v-for="opt in priOptions" :key="opt.value" :value="opt.value" :label="opt.label" />
              </el-select>
            </el-form-item>
          </el-col>
          <el-col :span="8">
            <el-form-item label="预计工时">
              <el-input-number v-model="form.estimate" :min="0" :step="0.5" :precision="1" />
            </el-form-item>
          </el-col>
          <el-col :span="8">
            <el-form-item label="负责人">
              <UserSelect v-model="form.assignedTo" include-empty />
            </el-form-item>
          </el-col>
        </el-row>

        <el-form-item label="需求描述">
          <el-input v-model="form.spec" type="textarea" :rows="4" placeholder="要解决什么问题、给谁用" />
        </el-form-item>
        <el-form-item label="验收标准">
          <el-input v-model="form.verify" type="textarea" :rows="3" placeholder="怎么算做完了" />
        </el-form-item>

        <el-form-item label="评审人">
          <UserSelect v-model="form.reviewers" multiple placeholder="留空则用产品的默认评审人" />
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
.ml {
  margin-left: 6px;
}
</style>
