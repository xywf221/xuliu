<script setup>
// 个人中心：我是谁、我有哪些权限、改口令。
//
// 把权限点原样列出来，是为了让「为什么我看不到某个菜单」这类问题
// 用户自己能查 —— 权限点是与后端判定同源的那一份字符串。
import { onMounted, reactive, ref } from 'vue'
import { ElMessage } from 'element-plus'

import { authApi } from '@/api'
import { useAuthStore } from '@/stores/auth'
import { formatDateTime } from '@/utils/format'

const auth = useAuthStore()

const loading = ref(false)
const formRef = ref(null)
const submitting = ref(false)

const passwordForm = reactive({ oldPassword: '', newPassword: '', confirm: '' })

const rules = {
  oldPassword: [{ required: true, message: '请输入当前口令', trigger: 'blur' }],
  newPassword: [
    { required: true, message: '请输入新口令', trigger: 'blur' },
    { min: 8, message: '新口令至少 8 位', trigger: 'blur' },
  ],
  confirm: [
    {
      validator: (_rule, value, callback) => {
        if (value !== passwordForm.newPassword) {
          callback(new Error('两次输入的口令不一致'))
          return
        }
        callback()
      },
      trigger: 'blur',
    },
  ],
}

onMounted(async () => {
  loading.value = true
  try {
    await auth.loadProfile()
  } finally {
    loading.value = false
  }
})

async function changePassword() {
  const ok = await formRef.value?.validate().catch(() => false)
  if (!ok) return

  submitting.value = true
  try {
    await authApi.changePassword({
      oldPassword: passwordForm.oldPassword,
      newPassword: passwordForm.newPassword,
    })
    ElMessage.success('口令已修改')
    passwordForm.oldPassword = ''
    passwordForm.newPassword = ''
    passwordForm.confirm = ''
    formRef.value?.clearValidate()
  } catch {
    // 提示由 HTTP 层负责（口令过弱、原口令不符等都会带回原因）。
  } finally {
    submitting.value = false
  }
}
</script>

<template>
  <el-row v-loading="loading" :gutter="16">
    <el-col :md="14">
      <el-card shadow="never">
        <template #header><span>我的信息</span></template>

        <div class="fields xl-fields">
          <div class="xl-field">
            <span class="label">账号</span>
            <span class="value xl-mono">{{ auth.profile?.account }}</span>
          </div>
          <div class="xl-field">
            <span class="label">姓名</span>
            <span class="value">{{ auth.profile?.realname || '-' }}</span>
          </div>
          <div class="xl-field">
            <span class="label">部门</span>
            <span class="value">{{ auth.profile?.deptName || '-' }}</span>
          </div>
          <div class="xl-field">
            <span class="label">岗位</span>
            <span class="value">{{ auth.profile?.jobRole || '-' }}</span>
          </div>
          <div class="xl-field">
            <span class="label">邮箱</span>
            <span class="value">{{ auth.profile?.email || '-' }}</span>
          </div>
          <div class="xl-field">
            <span class="label">手机</span>
            <span class="value">{{ auth.profile?.mobile || '-' }}</span>
          </div>
          <div class="xl-field">
            <span class="label">角色</span>
            <span class="value">
              <el-tag v-if="auth.profile?.isSuper" type="danger" size="small" disable-transitions>
                超级管理员
              </el-tag>
              <template v-else>{{ auth.profile?.groups?.join('、') || '未分组' }}</template>
            </span>
          </div>
          <div class="xl-field">
            <span class="label">上次登录</span>
            <span class="value">{{ formatDateTime(auth.profile?.last) || '-' }}</span>
          </div>
        </div>
      </el-card>

      <el-card shadow="never" class="mt">
        <template #header><span>我的权限点（{{ auth.rights.length }} 项）</span></template>
        <div v-if="auth.profile?.isSuper" class="xl-dim">
          超级管理员拥有全部权限，不受权限点限制。
        </div>
        <div v-else class="privs">
          <el-tag v-for="perm in auth.rights" :key="perm" size="small" type="info" disable-transitions>
            {{ perm }}
          </el-tag>
        </div>
      </el-card>
    </el-col>

    <el-col :md="10">
      <el-card shadow="never">
        <template #header><span>修改口令</span></template>
        <el-form ref="formRef" :model="passwordForm" :rules="rules" label-width="88px">
          <el-form-item label="当前口令" prop="oldPassword">
            <el-input v-model="passwordForm.oldPassword" type="password" show-password />
          </el-form-item>
          <el-form-item label="新口令" prop="newPassword">
            <el-input v-model="passwordForm.newPassword" type="password" show-password />
          </el-form-item>
          <el-form-item label="确认新口令" prop="confirm">
            <el-input v-model="passwordForm.confirm" type="password" show-password />
          </el-form-item>
          <el-form-item>
            <el-button type="primary" :loading="submitting" @click="changePassword">
              提交
            </el-button>
          </el-form-item>
        </el-form>
        <div class="xl-dim tip">
          口令强度由后端校验：过短或过于常见的口令会被拒绝，并给出原因。
        </div>
      </el-card>
    </el-col>
  </el-row>
</template>

<style scoped>
.privs {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
}

.mt {
  margin-top: 16px;
}

.tip {
  font-size: 12px;
  line-height: 1.6;
}
</style>
