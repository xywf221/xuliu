<script setup>
// 产品列表。
//
// 产品是需求的容器：需求必须挂在某个产品下（缺陷也是）。
// 模块树随产品一起管理，所以「模块」放在这一页的行内抽屉里，
// 而不是单独开一个菜单 —— 模块脱离产品没有意义。
import { ref } from 'vue'
import { useRouter } from 'vue-router'
import { ElMessage, ElMessageBox } from 'element-plus'

import { productApi } from '@/api'
import StatusTag from '@/components/StatusTag.vue'
import UserSelect from '@/components/UserSelect.vue'
import { useList } from '@/composables/useList'
import { useAuthStore } from '@/stores/auth'
import { aclOptions, labelOf, productStatuses, productTypes } from '@/utils/dict'

const router = useRouter()
const auth = useAuthStore()

const { list, total, page, size, loading, filters, reload, reset } = useList(productApi.list, {
  keywords: '',
  type: '',
  acl: '',
})

const canCreate = auth.can('product-create')
const canEdit = auth.can('product-edit')
const canDelete = auth.can('product-delete')

// ---- 新建 / 编辑 ----
const dialogVisible = ref(false)
const dialogTitle = ref('新建产品')
const submitting = ref(false)
const formRef = ref(null)
const form = ref(emptyForm())

function emptyForm() {
  return {
    id: 0,
    name: '',
    code: '',
    type: 'normal',
    status: 'normal',
    po: 0,
    qd: 0,
    rd: 0,
    acl: 'open',
    desc: '',
    reviewStory: false,
    reviewer: [],
  }
}

const rules = {
  name: [{ required: true, message: '请输入产品名称', trigger: 'blur' }],
  code: [
    { required: true, message: '请输入产品代号', trigger: 'blur' },
    {
      pattern: /^[A-Za-z0-9]+$/,
      message: '代号只能是字母和数字',
      trigger: 'blur',
    },
  ],
}

function openCreate() {
  form.value = emptyForm()
  dialogTitle.value = '新建产品'
  dialogVisible.value = true
}

function openEdit(row) {
  form.value = {
    id: row.id,
    name: row.name,
    code: row.code,
    type: row.type,
    status: row.status,
    po: row.po,
    qd: row.qd,
    rd: row.rd,
    acl: row.acl,
    desc: row.desc,
    reviewStory: row.reviewStory,
    reviewer: row.reviewer || [],
  }
  dialogTitle.value = '编辑产品'
  dialogVisible.value = true
}

async function submit() {
  const ok = await formRef.value?.validate().catch(() => false)
  if (!ok) return

  submitting.value = true
  try {
    const payload = { ...form.value }
    delete payload.id
    if (form.value.id) {
      await productApi.update(form.value.id, payload)
      ElMessage.success('已保存')
    } else {
      await productApi.create(payload)
      ElMessage.success('已创建产品')
    }
    dialogVisible.value = false
    reload()
  } catch {
    // 提示由 HTTP 层负责（代号重复等）。
  } finally {
    submitting.value = false
  }
}

async function remove(row) {
  try {
    await ElMessageBox.confirm(
      `删除产品「${row.name}」后，其下需求将无法再按产品归类。确定删除吗？`,
      '删除产品',
      { confirmButtonText: '删除', cancelButtonText: '取消', type: 'warning' },
    )
  } catch {
    return
  }
  try {
    await productApi.remove(row.id)
    ElMessage.success('已删除')
    reload()
  } catch {
    // 提示由 HTTP 层负责（例如产品下还有需求、或没有删除权限）。
  }
}

// ---- 模块树 ----
const moduleDrawer = ref(false)
const moduleProduct = ref(null)
const modules = ref([])
const moduleLoading = ref(false)
const moduleForm = ref({ type: 'story', parentId: 0, name: '' })
const moduleSubmitting = ref(false)

async function openModules(row) {
  moduleProduct.value = row
  moduleDrawer.value = true
  await loadModules()
}

async function loadModules() {
  if (!moduleProduct.value) return
  moduleLoading.value = true
  try {
    modules.value = await productApi.modules(moduleProduct.value.id)
  } finally {
    moduleLoading.value = false
  }
}

async function addModule() {
  if (!moduleForm.value.name.trim()) {
    ElMessage.warning('请输入模块名称')
    return
  }
  moduleSubmitting.value = true
  try {
    await productApi.createModule({
      root: moduleProduct.value.id,
      type: moduleForm.value.type,
      parentId: moduleForm.value.parentId,
      name: moduleForm.value.name.trim(),
    })
    ElMessage.success('已添加模块')
    moduleForm.value.name = ''
    moduleForm.value.parentId = 0
    await loadModules()
  } finally {
    moduleSubmitting.value = false
  }
}

// ---- 跳转 ----
function goStories(row) {
  router.push({ name: 'stories', query: { productId: row.id } })
}

function goBugs(row) {
  router.push({ name: 'bugs', query: { productId: row.id } })
}
</script>

<template>
  <el-card shadow="never">
    <div class="xl-page-title">
      <h2>产品</h2>
      <el-button v-if="canCreate" type="primary" @click="openCreate">
        <el-icon><Plus /></el-icon>
        <span>新建产品</span>
      </el-button>
    </div>

    <div class="xl-filters">
      <el-input
        v-model="filters.keywords"
        placeholder="按名称 / 代号搜索"
        clearable
        style="width: 220px"
        @keyup.enter="reload"
        @clear="reload"
      />
      <el-select v-model="filters.type" placeholder="类型" clearable style="width: 130px" @change="reload">
        <el-option value="normal" label="正常" />
        <el-option value="branch" label="分支" />
        <el-option value="platform" label="平台" />
      </el-select>
      <el-select v-model="filters.acl" placeholder="访问控制" clearable style="width: 130px" @change="reload">
        <el-option v-for="opt in aclOptions" :key="opt.value" :value="opt.value" :label="opt.label" />
      </el-select>
      <el-button @click="reload">查询</el-button>
      <el-button text @click="reset">重置</el-button>
    </div>

    <el-table v-loading="loading" :data="list" border stripe row-key="id">
      <el-table-column prop="id" label="ID" width="60" />
      <el-table-column label="产品" min-width="200">
        <template #default="{ row }">
          <el-link type="primary" @click="goStories(row)">{{ row.name }}</el-link>
          <el-tag v-if="row.acl === 'private'" size="small" type="warning" class="ml">私有</el-tag>
        </template>
      </el-table-column>
      <el-table-column prop="code" label="代号" width="120" />
      <el-table-column label="类型" width="90">
        <template #default="{ row }">{{ labelOf(productTypes, row.type) }}</template>
      </el-table-column>
      <el-table-column label="状态" width="90">
        <template #default="{ row }">
          <StatusTag :status="row.status" :label="labelOf(productStatuses, row.status)" />
        </template>
      </el-table-column>
      <el-table-column label="需求" width="80">
        <template #default="{ row }">
          <el-link type="primary" @click="goStories(row)">{{ row.storyCount ?? 0 }}</el-link>
        </template>
      </el-table-column>
      <el-table-column label="计划" width="80">
        <template #default="{ row }">
          <span>{{ row.planCount ?? 0 }}</span>
        </template>
      </el-table-column>
      <el-table-column label="负责人" width="200">
        <template #default="{ row }">
          <span class="xl-dim">
            PO {{ row.poName || '-' }} · QD {{ row.qdName || '-' }} · RD {{ row.rdName || '-' }}
          </span>
        </template>
      </el-table-column>
      <el-table-column label="操作" width="200" fixed="right">
        <template #default="{ row }">
          <el-button text type="primary" size="small" @click="openModules(row)">模块</el-button>
          <el-button text type="primary" size="small" @click="goStories(row)">需求</el-button>
          <el-button text type="primary" size="small" @click="goBugs(row)">缺陷</el-button>
          <el-button v-if="canEdit" text size="small" @click="openEdit(row)">编辑</el-button>
          <el-button v-if="canDelete" text type="danger" size="small" @click="remove(row)">删除</el-button>
        </template>
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

    <!-- 新建 / 编辑 -->
    <el-dialog v-model="dialogVisible" :title="dialogTitle" width="640px">
      <el-form ref="formRef" :model="form" :rules="rules" label-width="96px">
        <el-row :gutter="12">
          <el-col :span="12">
            <el-form-item label="产品名称" prop="name">
              <el-input v-model="form.name" maxlength="110" />
            </el-form-item>
          </el-col>
          <el-col :span="12">
            <el-form-item label="产品代号" prop="code">
              <el-input v-model="form.code" maxlength="45" placeholder="字母数字，如 demo" />
            </el-form-item>
          </el-col>
        </el-row>

        <el-row :gutter="12">
          <el-col :span="8">
            <el-form-item label="类型">
              <el-select v-model="form.type" style="width: 100%">
                <el-option value="normal" label="正常" />
                <el-option value="branch" label="分支" />
                <el-option value="platform" label="平台" />
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
            <el-form-item label="需求评审">
              <el-switch v-model="form.reviewStory" />
            </el-form-item>
          </el-col>
        </el-row>

        <el-form-item label="PO 产品经理">
          <UserSelect v-model="form.po" include-empty />
        </el-form-item>
        <el-form-item label="QD 测试负责">
          <UserSelect v-model="form.qd" include-empty />
        </el-form-item>
        <el-form-item label="RD 研发负责">
          <UserSelect v-model="form.rd" include-empty />
        </el-form-item>

        <el-form-item label="默认评审人">
          <UserSelect v-model="form.reviewer" multiple placeholder="提交需求评审时默认走这些人" />
        </el-form-item>

        <el-form-item label="备注">
          <el-input v-model="form.desc" type="textarea" :rows="2" />
        </el-form-item>
      </el-form>

      <template #footer>
        <el-button @click="dialogVisible = false">取消</el-button>
        <el-button type="primary" :loading="submitting" @click="submit">保存</el-button>
      </template>
    </el-dialog>

    <!-- 模块树 -->
    <el-drawer v-model="moduleDrawer" :title="`模块 · ${moduleProduct?.name || ''}`" size="480px">
      <div class="module-form">
        <el-select v-model="moduleForm.type" style="width: 100px">
          <el-option value="story" label="需求" />
          <el-option value="task" label="任务" />
          <el-option value="bug" label="缺陷" />
        </el-select>
        <el-input v-model="moduleForm.name" placeholder="模块名称" @keyup.enter="addModule" />
        <el-button type="primary" :loading="moduleSubmitting" @click="addModule">添加</el-button>
      </div>
      <div class="xl-dim tip">
        模块树按用途分开：需求挂需求树、任务挂任务树，跨树挂载会被后端拒绝。
      </div>

      <el-tree
        v-loading="moduleLoading"
        :data="modules"
        :props="{ label: 'name', children: 'children' }"
        default-expand-all
        node-key="id"
        class="mt"
      >
        <template #default="{ data }">
          <span class="node">
            <span>{{ data.name }}</span>
            <span class="xl-dim">{{ data.grade > 1 ? `第 ${data.grade} 级` : '' }}</span>
            <el-button
              text
              size="small"
              type="primary"
              @click.stop="moduleForm.parentId = data.id; moduleForm.type = data.type"
            >
              设为父级
            </el-button>
          </span>
        </template>
      </el-tree>
      <div v-if="moduleForm.parentId" class="xl-dim mt">
        新建的模块将挂在 #{{ moduleForm.parentId }} 下
        <el-button text size="small" @click="moduleForm.parentId = 0">取消</el-button>
      </div>
    </el-drawer>
  </el-card>
</template>

<style scoped>
.ml {
  margin-left: 6px;
}

.module-form {
  display: flex;
  gap: 8px;
}

.tip {
  font-size: 12px;
  margin-top: 8px;
}

.mt {
  margin-top: 12px;
}

.node {
  display: flex;
  align-items: center;
  gap: 10px;
  width: 100%;
}
</style>
