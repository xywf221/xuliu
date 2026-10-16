<script setup>
// 状态标签。
//
// 文字一律用后端给的 statusLabel（唯一的事实来源），颜色由前端按状态值查表。
// label 缺席时退回状态值本身，至少不会显示空白。
import { computed } from 'vue'

import { statusColor } from '@/utils/dict'

const props = defineProps({
  status: { type: String, default: '' },
  label: { type: String, default: '' },
  size: { type: String, default: 'small' },
})

const text = computed(() => props.label || props.status || '-')
const type = computed(() => statusColor(props.status))
</script>

<template>
  <el-tag :type="type" :size="size" disable-transitions>{{ text }}</el-tag>
</template>
