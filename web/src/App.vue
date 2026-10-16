<script setup>
// 应用根组件。
//
// 只做两件事：挂 router-view，以及把「登录失效」的回调接上路由。
// 后者放在这里是因为 client 不能直接 import router（循环依赖），
// 而根组件知道 router 的存在。
import { onMounted } from 'vue'
import { useRouter } from 'vue-router'

import { setUnauthorizedHandler } from '@/api/client'
import { useAuthStore } from '@/stores/auth'

const router = useRouter()
const auth = useAuthStore()

onMounted(() => {
  setUnauthorizedHandler(() => {
    auth.clear()
    const current = router.currentRoute.value
    if (current.name !== 'login') {
      router.replace({ name: 'login', query: { redirect: current.fullPath } })
    }
  })
})
</script>

<template>
  <router-view />
</template>
