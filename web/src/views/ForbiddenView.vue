<script setup>
// 无权访问。
//
// 单独一页而不是「静默跳回首页」：用户点了一个菜单却什么都没发生，
// 只会以为是系统坏了；明确说一句「你没有这个权限」，他才知道该找谁开权限。
import { useRouter } from 'vue-router'

const router = useRouter()
</script>

<template>
  <el-result icon="warning" title="没有访问权限" sub-title="你的角色不包含该页面所需的权限点，请联系管理员调整权限组。">
    <template #extra>
      <el-button type="primary" @click="router.push({ name: 'workbench' })">回到工作台</el-button>
    </template>
  </el-result>
</template>
