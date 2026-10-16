<script setup>
// 选人下拉。
//
// 人员的极简列表在一个会话里不会变，缓存一次即可 ——
// 详情页上往往有三四个「负责人」字段，每个都发一次请求没必要。
import { onMounted, ref } from 'vue'

import { orgApi } from '@/api'
import { useAuthStore } from '@/stores/auth'

const props = defineProps({
  modelValue: { type: [Number, Array], default: null },
  multiple: { type: Boolean, default: false },
  placeholder: { type: String, default: '请选择用户' },
  clearable: { type: Boolean, default: true },
  disabled: { type: Boolean, default: false },
  // 是否允许选择「不指定」。指派场景通常允许，评审人则至少要一个。
  includeEmpty: { type: Boolean, default: false },
})

const emit = defineEmits(['update:modelValue', 'change'])

const auth = useAuthStore()
const users = ref([])
const loading = ref(false)

let cached = null

async function load() {
  if (cached) {
    users.value = cached
    return
  }
  loading.value = true
  try {
    const list = await orgApi.userBrief({})
    cached = list || []
    users.value = cached
  } finally {
    loading.value = false
  }
}

onMounted(load)

function onChange(value) {
  emit('update:modelValue', value)
  emit('change', value)
}

// label 优先用 realname：界面上认人不认账号。
function displayOf(user) {
  return user.displayName || user.realname || user.account
}

// 当前登录用户的 id：很多表单要默认填自己。
function currentUserId() {
  return auth.profile?.id
}

defineExpose({ currentUserId })
</script>

<template>
  <el-select
    :model-value="modelValue"
    :multiple="multiple"
    :placeholder="placeholder"
    :clearable="clearable"
    :disabled="disabled"
    :loading="loading"
    filterable
    collapse-tags
    collapse-tags-tooltip
    style="width: 100%"
    @change="onChange"
  >
    <el-option v-if="includeEmpty" :value="0" label="（不指定）" />
    <el-option
      v-for="user in users"
      :key="user.id"
      :value="user.id"
      :label="displayOf(user)"
    >
      <span>{{ displayOf(user) }}</span>
      <span class="xl-dim" style="margin-left: 8px; font-size: 12px">{{ user.account }}</span>
    </el-option>
  </el-select>
</template>
