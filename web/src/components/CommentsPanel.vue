<script setup>
// 评论面板。
//
// 评论挂在「任意业务对象」上（objectType + objectID 多态双列），
// 所以这个组件对需求/任务/缺陷/项目一视同仁 —— 传什么类型就评论什么。
// 后端在读写前都会校验「你看得见这个对象」，看不到会直接 403。
import { onMounted, ref, watch } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'

import { commentApi } from '@/api'
import { useAuthStore } from '@/stores/auth'
import { fromNow } from '@/utils/format'

const props = defineProps({
  objectType: { type: String, required: true },
  objectId: { type: [Number, String], required: true },
})

const auth = useAuthStore()

const list = ref([])
const total = ref(0)
const page = ref(1)
const size = ref(10)
const loading = ref(false)
const submitting = ref(false)
const content = ref('')

async function load() {
  loading.value = true
  try {
    const data = await commentApi.list({
      objectType: props.objectType,
      objectID: props.objectId,
      page: page.value,
      size: size.value,
      order: 'id_desc',
    })
    list.value = data.list || []
    total.value = data.total || 0
  } finally {
    loading.value = false
  }
}

onMounted(load)
watch(
  () => [props.objectType, props.objectId],
  () => {
    page.value = 1
    load()
  },
)

async function submit() {
  const text = content.value.trim()
  if (!text) {
    ElMessage.warning('评论内容不能为空')
    return
  }
  submitting.value = true
  try {
    await commentApi.create({
      objectType: props.objectType,
      objectID: props.objectId,
      content: text,
    })
    content.value = ''
    ElMessage.success('已发表评论')
    page.value = 1
    await load()
  } catch {
    // 错误提示由 HTTP 层负责。
  } finally {
    submitting.value = false
  }
}

async function remove(item) {
  try {
    await ElMessageBox.confirm('确定要删除这条评论吗？', '删除评论', {
      confirmButtonText: '删除',
      cancelButtonText: '取消',
      type: 'warning',
    })
  } catch {
    return
  }
  await commentApi.remove(item.id)
  ElMessage.success('已删除')
  load()
}

// 只有作者本人能删自己的评论（后端的判定同此）。
function canDelete(item) {
  return item.userId === auth.profile?.id || auth.profile?.isSuper
}

defineExpose({ reload: load })
</script>

<template>
  <div class="comments">
    <div class="editor">
      <el-input
        v-model="content"
        type="textarea"
        :rows="3"
        maxlength="5000"
        show-word-limit
        placeholder="写点什么…… 支持 @ 提及（提醒会随评论一起发出）"
      />
      <div class="editor-actions">
        <el-button type="primary" :loading="submitting" @click="submit">发表评论</el-button>
      </div>
    </div>

    <div v-loading="loading" class="list">
      <el-empty v-if="!loading && list.length === 0" description="还没有评论" />
      <div v-for="item in list" :key="item.id" class="item">
        <el-avatar :size="30">{{ (item.userName || '?').slice(0, 1) }}</el-avatar>
        <div class="body">
          <div class="meta">
            <strong>{{ item.userName || `用户#${item.userId}` }}</strong>
            <span class="xl-dim">{{ fromNow(item.createdAt) }}</span>
            <el-button v-if="canDelete(item)" text type="danger" size="small" @click="remove(item)">
              删除
            </el-button>
          </div>
          <div class="text xl-prose">{{ item.content }}</div>
        </div>
      </div>

      <div v-if="total > size" class="xl-pager">
        <el-pagination
          v-model:current-page="page"
          :page-size="size"
          :total="total"
          layout="prev, pager, next"
          background
          @current-change="load"
        />
      </div>
    </div>
  </div>
</template>

<style scoped>
.editor-actions {
  display: flex;
  justify-content: flex-end;
  margin: 8px 0 16px;
}

.item {
  display: flex;
  gap: 10px;
  padding: 10px 0;
  border-bottom: 1px solid #f0f2f5;
}

.body {
  flex: 1;
  min-width: 0;
}

.meta {
  display: flex;
  align-items: center;
  gap: 10px;
  margin-bottom: 4px;
}

.meta .el-button {
  margin-left: auto;
}
</style>
