<script setup>
// 项目列表。
//
// 项目是「做事」的容器：需求排进来、任务拆出来、迭代切时间窗。
// 一个项目必须至少关联一个产品 —— 没有产品的项目排不进需求也建不了任务，
// 所以在新建表单里就把产品标成必填，而不是等用户建出一个空壳再报错。
import { onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'

import { productApi, projectApi } from '@/api'
import StatusTag from '@/components/StatusTag.vue'
import UserSelect from '@/components/UserSelect.vue'
import { useList } from '@/composables/useList'
import { useAuthStore } from '@/stores/auth'
import { aclOptions, labelOf, projectModels } from '@/utils/dict'
import { formatDate } from '@/utils/format'

const router = useRouter()
const auth = useAuthStore()

const canCreate = auth.can('project-create')

const { list, total, page, size, loading, filters, reload, reset } = useList(projectApi.list, {
  productId: 0,
  status: '',
  model: '',
  keywords: '',
})

const products = ref([])
onMounted(async () => {
  products.value = await productApi.brief({})
})

const statusOptions = [
  { value: 'wait', label: '未开始' },
  { value: 'doing', label: '进行中' },
  { value: 'suspended', label: '已挂起' },
  { value: 'closed', label: '已关闭' },
]

// ---- 新建 ----
const dialogVisible = ref(false)
const submitting = ref(false)
const formRef = ref(null)
const form = ref(emptyForm())

function emptyForm() {
  return {
    name: '',
    code: '',
    model: 'scrum',
    productIds: [],
    begin: '',
    end: '',
    po: 0,
    pm: 0,
    qd: 0,
    rd: 0,
    acl: 'open',
    multiple: true,
    desc: '',
    team: [],
  }
}

const rules = {
  name: [{ required: true, message: '请输入项目名称', trigger: 'blur' }],
  code: [
    { required: true, message: '请输入项目代号', trigger: 'blur' },
    { pattern: /^[A-Za-z0-9]+$/, message: '代号只能是字母和数字', trigger: 'blur' },
  ],
  productIds: [
    {
      type: 'array',
      required: true,
      min: 1,
      message: '至少关联一个产品：没有产品的项目排不进需求、建不了任务',
      trigger: 'change',
    },
  ],
}

async function submit() {
  const ok = await formRef.value?.validate().catch(() => false)
  if (!ok) return

  submitting.value = true
  try {
    const payload = { ...form.value }
    const created = await projectApi.create(payload)
    ElMessage.success('已创建项目')
    dialogVisible.value = false
    router.push(`/projects/${created.id}`)
  } catch {
    // 提示由 HTTP 层负责（代号重复等）。
  } finally {
    submitting.value = false
  }
}
</script>

<template>
  <el-card shadow="never">
    <div class="xl-page-title">
      <h2>项目</h2>
      <el-button v-if="canCreate" type="primary" @click="dialogVisible = true">
        <el-icon><Plus /></el-icon>
        <span>新建项目</span>
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
      <el-select v-model="filters.model" placeholder="管理模式" clearable style="width: 140px" @change="reload">
        <el-option v-for="opt in projectModels" :key="opt.value" :value="opt.value" :label="opt.label" />
      </el-select>
      <el-input
        v-model="filters.keywords"
        placeholder="名称 / 代号"
        clearable
        style="width: 200px"
        @keyup.enter="reload"
        @clear="reload"
      />
      <el-button @click="reload">查询</el-button>
      <el-button text @click="reset">重置</el-button>
    </div>

    <el-table v-loading="loading" :data="list" border stripe row-key="id">
      <el-table-column label="编号" width="120">
        <template #default="{ row }">
          <span class="xl-mono xl-dim">{{ row.number }}</span>
        </template>
      </el-table-column>
      <el-table-column label="项目" min-width="200">
        <template #default="{ row }">
          <el-link type="primary" @click="router.push(`/projects/${row.id}`)">{{ row.name }}</el-link>
          <el-tag v-if="row.acl === 'private'" size="small" type="warning" class="ml">私有</el-tag>
        </template>
      </el-table-column>
      <el-table-column label="模式" width="96">
        <template #default="{ row }">{{ row.modelLabel || labelOf(projectModels, row.model) }}</template>
      </el-table-column>
      <el-table-column label="状态" width="96">
        <template #default="{ row }">
          <StatusTag :status="row.status" :label="row.statusLabel" />
        </template>
      </el-table-column>
      <el-table-column label="进度" width="140">
        <template #default="{ row }">
          <el-progress :percentage="row.progress || 0" :stroke-width="10" />
        </template>
      </el-table-column>
      <el-table-column label="计划起止" width="190">
        <template #default="{ row }">
          <span class="xl-dim">{{ formatDate(row.begin) || '未定' }} ~ {{ formatDate(row.end) || '未定' }}</span>
        </template>
      </el-table-column>
      <el-table-column label="需求" width="70">
        <template #default="{ row }">{{ row.storyCount ?? 0 }}</template>
      </el-table-column>
      <el-table-column label="任务" width="70">
        <template #default="{ row }">{{ row.taskCount ?? 0 }}</template>
      </el-table-column>
      <el-table-column label="迭代" width="70">
        <template #default="{ row }">{{ row.executionCount ?? 0 }}</template>
      </el-table-column>
      <el-table-column label="成员" width="70">
        <template #default="{ row }">{{ row.memberCount ?? 0 }}</template>
      </el-table-column>
      <el-table-column label="负责人" width="120">
        <template #default="{ row }">{{ row.pmName || row.poName || '-' }}</template>
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

    <el-dialog v-model="dialogVisible" title="新建项目" width="720px">
      <el-form ref="formRef" :model="form" :rules="rules" label-width="96px">
        <el-row :gutter="12">
          <el-col :span="12">
            <el-form-item label="项目名称" prop="name">
              <el-input v-model="form.name" maxlength="90" />
            </el-form-item>
          </el-col>
          <el-col :span="12">
            <el-form-item label="项目代号" prop="code">
              <el-input v-model="form.code" maxlength="45" placeholder="字母数字，如 demo" />
            </el-form-item>
          </el-col>
        </el-row>

        <el-form-item label="关联产品" prop="productIds">
          <el-select v-model="form.productIds" multiple filterable style="width: 100%">
            <el-option v-for="p in products" :key="p.id" :value="p.id" :label="p.name" />
          </el-select>
        </el-form-item>

        <el-row :gutter="12">
          <el-col :span="8">
            <el-form-item label="管理模式">
              <el-select v-model="form.model" style="width: 100%">
                <el-option v-for="opt in projectModels" :key="opt.value" :value="opt.value" :label="opt.label" />
              </el-select>
            </el-form-item>
          </el-col>
          <el-col :span="8">
            <el-form-item label="访问控制">
              <el-select v-model="form.acl" style="width: 100%">
                <el-option v-for="opt in aclOptions" :key="opt.value" :value="opt.value" :label="opt.label" />
              </el-select>
            </el-form-item>
          </el-col>
          <el-col :span="8">
            <el-form-item label="多个迭代">
              <el-switch v-model="form.multiple" />
            </el-form-item>
          </el-col>
        </el-row>

        <el-row :gutter="12">
          <el-col :span="12">
            <el-form-item label="计划开始">
              <el-date-picker
                v-model="form.begin"
                type="date"
                value-format="YYYY-MM-DD"
                placeholder="YYYY-MM-DD"
                style="width: 100%"
              />
            </el-form-item>
          </el-col>
          <el-col :span="12">
            <el-form-item label="计划结束">
              <el-date-picker
                v-model="form.end"
                type="date"
                value-format="YYYY-MM-DD"
                placeholder="YYYY-MM-DD"
                style="width: 100%"
              />
            </el-form-item>
          </el-col>
        </el-row>

        <el-form-item label="PM 项目经理">
          <UserSelect v-model="form.pm" include-empty />
        </el-form-item>
        <el-form-item label="PO 产品负责">
          <UserSelect v-model="form.po" include-empty />
        </el-form-item>
        <el-form-item label="QD 测试负责">
          <UserSelect v-model="form.qd" include-empty />
        </el-form-item>

        <el-form-item label="备注">
          <el-input v-model="form.desc" type="textarea" :rows="2" />
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
