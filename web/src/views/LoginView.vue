<script setup>
import { reactive, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'

import { useAuthStore } from '@/stores/auth'

const route = useRoute()
const router = useRouter()
const auth = useAuthStore()

const formRef = ref(null)
const loading = ref(false)

const form = reactive({
  account: '',
  password: '',
  remember: true,
})

const rules = {
  account: [{ required: true, message: '请输入账号', trigger: 'blur' }],
  password: [{ required: true, message: '请输入口令', trigger: 'blur' }],
}

async function submit() {
  const ok = await formRef.value?.validate().catch(() => false)
  if (!ok) return

  loading.value = true
  try {
    await auth.login(form.account, form.password, form.remember)
    ElMessage.success(`欢迎回来，${auth.displayName}`)
    // 回到用户本来要去的页面；直接进来的话去工作台。
    const redirect = route.query.redirect
    router.replace(typeof redirect === 'string' && redirect ? redirect : { name: 'workbench' })
  } catch {
    // 账号或口令错的提示由 HTTP 层统一弹出。
  } finally {
    loading.value = false
  }
}
</script>

<template>
  <div class="login">
    <div class="panel">
      <div class="intro">
        <h1>序流</h1>
        <p class="slogan">让项目有序，让协作流动。</p>
        <ul>
          <li>需求、任务、迭代、缺陷一体化</li>
          <li>状态流转有据可查，每一步都留痕</li>
          <li>工作台只显示「等你动手」的事</li>
        </ul>
      </div>

      <el-card class="form-card" shadow="never">
        <h2>登录</h2>
        <el-form ref="formRef" :model="form" :rules="rules" size="large" @keyup.enter="submit">
          <el-form-item prop="account">
            <el-input v-model="form.account" placeholder="账号" autocomplete="username">
              <template #prefix><el-icon><User /></el-icon></template>
            </el-input>
          </el-form-item>

          <el-form-item prop="password">
            <el-input
              v-model="form.password"
              type="password"
              placeholder="口令"
              show-password
              autocomplete="current-password"
            >
              <template #prefix><el-icon><Lock /></el-icon></template>
            </el-input>
          </el-form-item>

          <el-form-item>
            <el-checkbox v-model="form.remember">记住我</el-checkbox>
          </el-form-item>

          <el-button type="primary" size="large" style="width: 100%" :loading="loading" @click="submit">
            登录
          </el-button>
        </el-form>

        <p class="hint xl-dim">
          首次部署的初始账号见 configs/config.yaml 的 app.bootstrap 段。
        </p>
      </el-card>
    </div>
  </div>
</template>

<style scoped>
.login {
  display: flex;
  align-items: center;
  justify-content: center;
  height: 100vh;
  background: linear-gradient(135deg, #1f2d3d 0%, #2c3e50 55%, #3a5169 100%);
}

.panel {
  display: flex;
  gap: 56px;
  align-items: center;
  padding: 24px;
}

.intro {
  color: #fff;
  max-width: 360px;
}

.intro h1 {
  margin: 0 0 8px;
  font-size: 44px;
  letter-spacing: 8px;
}

.slogan {
  margin: 0 0 24px;
  font-size: 16px;
  opacity: 0.85;
}

.intro ul {
  margin: 0;
  padding-left: 18px;
  line-height: 2;
  opacity: 0.75;
}

.form-card {
  width: 340px;
  border-radius: 10px;
}

.form-card h2 {
  margin: 0 0 18px;
  font-size: 18px;
}

.hint {
  margin: 12px 0 0;
  font-size: 12px;
  line-height: 1.6;
}

@media (max-width: 860px) {
  .intro {
    display: none;
  }
}
</style>
