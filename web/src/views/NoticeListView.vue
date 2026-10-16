<script setup>
// 我的通知。
//
// 通知是**待办**不是流水：同一个对象上只留一条未读，新通知覆盖旧的
// （见后端 NoticeService.UpsertUnread）。所以这个列表读起来应该是
// 「现在有哪几件事等我动手」，而不是「历史上发生过什么」。
import { computed, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'

import { noticeApi } from '@/api'
import { fromNow } from '@/utils/format'

const router = useRouter()

const list = ref([])
const total = ref(0)
const page = ref(1)
const size = ref(20)
const loading = ref(false)
// 默认只看未读：这个页面的主要用途是清待办。
const filter = ref({ unread: true, keywords: '' })

const query = computed(() => {
  const params = { page: page.value, size: size.value, order: 'id_desc' }
  if (filter.value.unread) params.unread = 1
  if (filter.value.keywords) params.keywords = filter.value.keywords
  return params
})

async function load() {
  loading.value = true
  try {
    const data = await noticeApi.list(query.value)
    list.value = data.list || []
    total.value = data.total || 0
  } finally {
    loading.value = false
  }
}

function reload() {
  page.value = 1
  load()
}

onMounted(load)

// 点开一条通知：先标记已读，再跳到它指向的对象。
async function open(item) {
  if (!item.read) {
    await noticeApi.markRead({ ids: [item.id] })
  }
  if (item.link) {
    router.push(item.link)
  } else {
    load()
  }
}

async function markAll() {
  await noticeApi.markRead({ all: true })
  ElMessage.success('已全部标记为已读')
  reload()
}

async function remove(item) {
  await noticeApi.remove(item.id)
  ElMessage.success('已删除')
  load()
}
</script>

<template>
  <el-card shadow="never">
    <div class="xl-page-title">
      <h2>我的通知</h2>
      <div class="ops">
        <el-radio-group v-model="filter.unread" size="small" @change="reload">
          <el-radio-button :value="true">未读</el-radio-button>
          <el-radio-button :value="false">全部</el-radio-button>
        </el-radio-group>
        <el-input
          v-model="filter.keywords"
          placeholder="搜索标题"
          size="small"
          clearable
          style="width: 180px"
          @keyup.enter="reload"
          @clear="reload"
        />
        <el-button size="small" @click="markAll">全部已读</el-button>
      </div>
    </div>

    <div v-loading="loading">
      <el-empty v-if="!loading && list.length === 0" description="没有通知" />

      <div
        v-for="item in list"
        :key="item.id"
        class="item"
        :class="{ unread: !item.read }"
        @click="open(item)"
      >
        <div class="dot" />
        <div class="body">
          <div class="head">
            <strong>{{ item.title }}</strong>
            <span class="xl-dim">{{ fromNow(item.createdAt) }}</span>
          </div>
          <div class="content">{{ item.content }}</div>
          <div class="meta xl-dim">
            <span v-if="item.objectTitle">{{ item.objectTitle }}</span>
            <span v-if="item.fromName">来自 {{ item.fromName }}</span>
          </div>
        </div>
        <el-button text type="danger" size="small" @click.stop="remove(item)">删除</el-button>
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
  </el-card>
</template>

<style scoped>
.ops {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}

.item {
  display: flex;
  align-items: flex-start;
  gap: 10px;
  padding: 12px 8px;
  border-bottom: 1px solid #f0f2f5;
  cursor: pointer;
}

.item:hover {
  background: #fafafa;
}

.item.unread {
  background: #f5f9ff;
}

.dot {
  width: 8px;
  height: 8px;
  margin-top: 6px;
  border-radius: 50%;
  background: transparent;
  flex: 0 0 auto;
}

.item.unread .dot {
  background: #409eff;
}

.body {
  flex: 1;
  min-width: 0;
}

.head {
  display: flex;
  align-items: baseline;
  gap: 10px;
}

.content {
  color: #606266;
  margin-top: 2px;
}

.meta {
  display: flex;
  gap: 12px;
  font-size: 12px;
  margin-top: 4px;
}
</style>
