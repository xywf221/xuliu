import { createRouter, createWebHistory } from 'vue-router'

import { useAuthStore } from '@/stores/auth'

// 路由表。
//
// meta.priv 是该页面要求的权限点，与后端路由上挂的是同一个字符串
// （见 internal/acl/privileges.go）。前端用它决定「菜单显不显示」和
// 「直接敲 URL 进不进得去」—— 这只是体验层的拦截，
// 真正的授权在服务端，不在这儿。
//
// meta.menu 为 true 的条目会出现在侧边栏，顺序即下面的书写顺序。
const routes = [
  {
    path: '/login',
    name: 'login',
    component: () => import('@/views/LoginView.vue'),
    meta: { public: true, title: '登录' },
  },
  {
    path: '/',
    component: () => import('@/layouts/AppLayout.vue'),
    redirect: '/workbench',
    children: [
      {
        path: 'workbench',
        name: 'workbench',
        component: () => import('@/views/WorkbenchView.vue'),
        meta: { title: '我的工作台', priv: 'my-work', menu: true, icon: 'Odometer' },
      },
      {
        path: 'products',
        name: 'products',
        component: () => import('@/views/ProductListView.vue'),
        meta: { title: '产品', priv: 'product-browse', menu: true, icon: 'Box' },
      },
      {
        path: 'stories',
        name: 'stories',
        component: () => import('@/views/StoryListView.vue'),
        meta: { title: '需求', priv: 'story-browse', menu: true, icon: 'Document' },
      },
      {
        path: 'stories/:id',
        name: 'story-detail',
        component: () => import('@/views/StoryDetailView.vue'),
        meta: { title: '需求详情', priv: 'story-browse', activeMenu: '/stories' },
      },
      {
        path: 'projects',
        name: 'projects',
        component: () => import('@/views/ProjectListView.vue'),
        meta: { title: '项目', priv: 'project-browse', menu: true, icon: 'Folder' },
      },
      {
        path: 'projects/:id',
        name: 'project-detail',
        component: () => import('@/views/ProjectDetailView.vue'),
        meta: { title: '项目详情', priv: 'project-browse', activeMenu: '/projects' },
      },
      {
        path: 'tasks',
        name: 'tasks',
        component: () => import('@/views/TaskListView.vue'),
        meta: { title: '任务', priv: 'task-browse', menu: true, icon: 'List' },
      },
      {
        path: 'tasks/:id',
        name: 'task-detail',
        component: () => import('@/views/TaskDetailView.vue'),
        meta: { title: '任务详情', priv: 'task-browse', activeMenu: '/tasks' },
      },
      {
        path: 'bugs',
        name: 'bugs',
        component: () => import('@/views/BugListView.vue'),
        meta: { title: '缺陷', priv: 'bug-browse', menu: true, icon: 'Warning' },
      },
      {
        path: 'bugs/:id',
        name: 'bug-detail',
        component: () => import('@/views/BugDetailView.vue'),
        meta: { title: '缺陷详情', priv: 'bug-browse', activeMenu: '/bugs' },
      },
      {
        path: 'notices',
        name: 'notices',
        component: () => import('@/views/NoticeListView.vue'),
        meta: { title: '我的通知', priv: 'my-work' },
      },
      {
        path: 'profile',
        name: 'profile',
        component: () => import('@/views/ProfileView.vue'),
        meta: { title: '个人中心' },
      },
      {
        path: 'forbidden',
        name: 'forbidden',
        component: () => import('@/views/ForbiddenView.vue'),
        meta: { title: '无权访问' },
      },
    ],
  },
  {
    // 兜底：地址敲错时不至于白屏。
    path: '/:pathMatch(.*)*',
    redirect: '/workbench',
  },
]

const router = createRouter({
  history: createWebHistory(),
  routes,
  scrollBehavior: () => ({ top: 0 }),
})

router.beforeEach(async (to) => {
  const auth = useAuthStore()
  auth.restore()

  if (to.meta.public) {
    return true
  }

  if (!auth.isLoggedIn) {
    // 带上原地址，登录后回到用户本来要去的地方。
    return { name: 'login', query: { redirect: to.fullPath } }
  }

  await auth.ensureProfile()

  // 权限不足就送去一个说明页，而不是静默跳回首页 ——
  // 「点了一下什么都没发生」比一句「你没有这个权限」更让人困惑。
  if (to.meta.priv && !auth.can(to.meta.priv)) {
    return { name: 'forbidden' }
  }

  return true
})

router.afterEach((to) => {
  document.title = to.meta.title ? `${to.meta.title} · 序流` : '序流'
})

export default router
