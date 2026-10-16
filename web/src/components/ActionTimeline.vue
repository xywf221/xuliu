<script setup>
// 操作历史时间线。
//
// 后端把「谁、什么时候、做了什么、改了什么」都算好了（含中文动词与
// 字段级 diff 的可读文案），前端只管画 —— 这样时间线在 Web、导出、
// 将来的移动端看到的是同一份事实，不会各拼一套。
import { onMounted, ref, watch } from 'vue'

import { actionApi } from '@/api'
import { formatDateTime } from '@/utils/format'

const props = defineProps({
  objectType: { type: String, required: true },
  objectId: { type: [Number, String], required: true },
})

const list = ref([])
const total = ref(0)
const page = ref(1)
const size = ref(20)
const loading = ref(false)

async function load() {
  loading.value = true
  try {
    // 查询参数用的是 objectID（大写 ID）—— 与后端 dto.ActionFilterReq 对齐。
    const data = await actionApi.list({
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
watch(() => [props.objectType, props.objectId], () => {
  page.value = 1
  load()
})

defineExpose({ reload: load })
</script>

<template>
  <div v-loading="loading" class="timeline">
    <el-empty v-if="!loading && list.length === 0" description="还没有操作记录" />

    <el-timeline v-else>
      <el-timeline-item
        v-for="item in list"
        :key="item.id"
        :timestamp="formatDateTime(item.date)"
        placement="top"
        :type="item.action === 'closed' ? 'info' : 'primary'"
      >
        <div class="head">
          <strong>{{ item.actorName || `用户#${item.actorId}` }}</strong>
          <span class="verb">{{ item.actionText || item.action }}</span>
          <span v-if="item.extra && item.extra.includes('|')" class="xl-dim">
            （{{ item.extra.replace('|', ' → ') }}）
          </span>
        </div>

        <div v-if="item.comment" class="comment xl-prose">{{ item.comment }}</div>

        <ul v-if="item.changes?.length" class="changes">
          <li v-for="(change, idx) in item.changes" :key="idx">{{ change.text }}</li>
        </ul>
      </el-timeline-item>
    </el-timeline>

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
</template>

<style scoped>
.timeline {
  min-height: 80px;
}

.head {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 6px;
}

.verb {
  color: #409eff;
  font-weight: 500;
}

.comment {
  margin-top: 6px;
  padding: 6px 10px;
  background: #f7f8fa;
  border-radius: 4px;
}

.changes {
  margin: 6px 0 0;
  padding-left: 18px;
  color: #606266;
  font-size: 13px;
}
</style>
