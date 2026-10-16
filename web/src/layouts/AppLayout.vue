<script setup>
// 主框架：左侧菜单 + 顶部栏 + 内容区。
//
// 菜单不是写死的列表，而是「路由表 ∩ 当前用户的权限点」——
// 后端在登录时把权限点整份给了前端，前端据此决定显示什么。
// 这样做的好处是：加了权限点、改了角色，前端一行都不用动。
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ElMessage, ElMessageBox } from 'element-plus'

import { noticeApi, workbenchApi } from '@/api'
import { useAuthStore } from '@/stores/auth'

const route = useRoute()
const router = useRouter()
const auth = useAuthStore()

const collapsed = ref(false)
const unread = ref(0)
let timer = null

// 菜单 = 路由表里标了 menu 且当前用户有权限的那些。
const menus = computed(() => {
  const root = router.options.routes.find((r) => r.children?.length)
  if (!root) return []
  return root.children
    .filter((r) => r.meta?.menu)
    .filter((r) => auth.can(r.meta.priv))
    .map((r) => ({
      path: `/${r.path}`,
      title: r.meta.title,
      icon: r.meta.icon,
    }))
})

const activeMenu = computed(() => route.meta.activeMenu || route.path)

// 未读红点：轮询而不是长连接。
//
// 站内通知不是即时性要求很高的东西，30 秒一次足够；
// 为它牵一条 WebSocket 会让部署多一层状态（尤其是在多实例下）。
async function refreshUnread() {
  try {
    const data = await noticeApi.unreadCount()
    unread.value = data?.unread || 0
  } catch {
    // 红点取不到不该打扰用户，静默即可。
  }
}

onMounted(() => {
  refreshUnread()
  timer = setInterval(refreshUnread, 30000)
})

onUnmounted(() => {
  if (timer) clearInterval(timer)
})

async function handleLogout() {
  await ElMessageBox.confirm('确定要退出登录吗？', '退出登录', {
    confirmButtonText: '退出',
    cancelButtonText: '取消',
    type: 'warning',
  })
    .then(async () => {
      await auth.logout()
      ElMessage.success('已退出登录')
      router.replace({ name: 'login' })
    })
    .catch(() => {})
}

// 点到红点时先刷新一次未读，省得用户还得手动刷新页面。
async function goNotices() {
  await refreshUnread()
  router.push({ name: 'notices' })
}
</script>

<template>
  <el-container class="layout">
    <el-aside :width="collapsed ? '64px' : '200px'" class="aside">
      <div class="brand" :class="{ mini: collapsed }">
        <span class="logo">序</span>
        <span v-if="!collapsed" class="name">序流</span>
      </div>
      <el-menu :default-active="activeMenu" :collapse="collapsed" router class="menu">
        <el-menu-item v-for="item in menus" :key="item.path" :index="item.path">
          <el-icon><component :is="item.icon" /></el-icon>
          <template #title>{{ item.title }}</template>
        </el-menu-item>
      </el-menu>
    </el-aside>

    <el-container>
      <el-header class="header">
        <div class="left">
          <el-button text @click="collapsed = !collapsed">
            <el-icon><Fold /></el-icon>
          </el-button>
          <span class="crumb">{{ route.meta.title }}</span>
        </div>
        <div class="right">
          <el-badge :value="unread" :hidden="!unread" class="badge">
            <el-button text @click="goNotices">
              <el-icon><Bell /></el-icon>
              <span>待办</span>
            </el-button>
          </el-badge>

          <el-dropdown>
            <span class="user">
              <el-avatar :size="26">{{ auth.displayName.slice(0, 1) }}</el-avatar>
              <span class="name">{{ auth.displayName }}</span>
              <el-icon><ArrowDown /></el-icon>
            </span>
            <template #dropdown>
              <el-dropdown-menu>
                <el-dropdown-item @click="router.push({ name: 'profile' })">
                  个人中心
                </el-dropdown-item>
                <el-dropdown-item divided @click="handleLogout">退出登录</el-dropdown-item>
              </el-dropdown-menu>
            </template>
          </el-dropdown>
        </div>
      </el-header>

      <el-main class="main">
        <!--
          不做 keep-alive：列表页的筛选条件会随 URL 变化
          （如从产品页点「需求」带着 productId 进来），缓存住组件反而会让
          查询条件与页面显示对不上。列表页重进重查一次，数据也更新鲜。
        -->
        <router-view />
      </el-main>
    </el-container>
  </el-container>
</template>

<style scoped>
.layout {
  height: 100vh;
}

.aside {
  background: #1f2d3d;
  transition: width 0.2s;
  overflow: hidden;
}

.brand {
  display: flex;
  align-items: center;
  gap: 8px;
  height: 56px;
  padding: 0 16px;
  color: #fff;
}

.brand.mini {
  justify-content: center;
  padding: 0;
}

.logo {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 28px;
  height: 28px;
  border-radius: 6px;
  background: #409eff;
  font-weight: 700;
}

.name {
  font-size: 16px;
  font-weight: 600;
  letter-spacing: 2px;
}

.menu {
  border-right: none;
  background: transparent;
}

.menu :deep(.el-menu-item) {
  color: #c0c4cc;
}

.menu :deep(.el-menu-item.is-active) {
  color: #fff;
  background: #409eff;
}

.menu :deep(.el-menu-item:hover) {
  background: #2c3e50;
}

.header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  background: #fff;
  border-bottom: 1px solid #e4e7ed;
}

.header .left,
.header .right {
  display: flex;
  align-items: center;
  gap: 12px;
}

.crumb {
  font-size: 15px;
  font-weight: 600;
}

.user {
  display: flex;
  align-items: center;
  gap: 6px;
  cursor: pointer;
  outline: none;
}

.main {
  background: var(--xl-bg);
  padding: var(--xl-gap);
}
</style>
