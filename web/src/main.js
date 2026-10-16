import { createApp } from 'vue'
import * as ElementPlusIcons from '@element-plus/icons-vue'
import ElementPlus from 'element-plus'
import zhCn from 'element-plus/es/locale/lang/zh-cn'
import { createPinia } from 'pinia'

import 'element-plus/dist/index.css'
import '@/styles/app.css'

import App from '@/App.vue'
import router from '@/router'

// 应用入口。
//
// 只做装配，不放任何业务判断 —— 谁登录、能看什么、跳哪里，
// 分别属于 store、router 与后端权限接口的职责。
const app = createApp(App)

app.use(createPinia())
// 语言包固定中文：本系统只做中文版，但错误码与文案是分开的（见后端 internal/i18n），
// 所以将来加语言时改的是这一行与文案表，不是散落各处的硬编码字符串。
app.use(ElementPlus, { locale: zhCn })

// 图标全量注册。
//
// 全量注册是为了让菜单能按路由表里的图标名字符串动态渲染
// （`<component :is="icon" />`）—— 按需引入就无法在运行时按名字取组件。
// 代价是图标会整体进包，但这套图标本就不大，换来的是「加一个菜单不用改三处」。
for (const [name, component] of Object.entries(ElementPlusIcons)) {
  app.component(name, component)
}

app.use(router)

app.mount('#app')
