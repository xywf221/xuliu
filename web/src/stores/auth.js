import { defineStore } from 'pinia'
import { computed, ref } from 'vue'

import { authApi } from '@/api'
import { setAccessToken } from '@/api/client'

// 登录态。
//
// 令牌放 localStorage：刷新页面不该把人踢出去。
// 但它只是「缓存一份凭证」，真正的权限事实始终来自后端 ——
// 前端的 rights 只用来决定菜单与按钮显不显示，
// 藏起来的按钮挡不住手写的请求，所以每个接口后端都另有一道校验。
const TOKEN_KEY = 'xuliu.accessToken'
const REFRESH_KEY = 'xuliu.refreshToken'

export const useAuthStore = defineStore('auth', () => {
  const token = ref(localStorage.getItem(TOKEN_KEY) || '')
  const refreshToken = ref(localStorage.getItem(REFRESH_KEY) || '')
  const profile = ref(null)

  const isLoggedIn = computed(() => !!token.value)
  const rights = computed(() => profile.value?.rights || [])
  const displayName = computed(
    () => profile.value?.displayName || profile.value?.realname || profile.value?.account || '',
  )

  // can 判定当前用户是否拥有某个权限点。
  //
  // 与后端同样的口径：超管恒真，否则看权限点集合。
  function can(priv) {
    if (!priv) return true
    if (profile.value?.isSuper) return true
    return rights.value.includes(priv)
  }

  function canAny(privs) {
    return privs.some((p) => can(p))
  }

  function persist(access, refresh) {
    token.value = access || ''
    if (access) {
      localStorage.setItem(TOKEN_KEY, access)
      setAccessToken(access)
    } else {
      localStorage.removeItem(TOKEN_KEY)
      setAccessToken('')
    }
    if (refresh !== undefined) {
      refreshToken.value = refresh || ''
      if (refresh) localStorage.setItem(REFRESH_KEY, refresh)
      else localStorage.removeItem(REFRESH_KEY)
    }
  }

  async function login(account, password, remember) {
    const data = await authApi.login({ account, password, remember })
    persist(data.accessToken, data.refreshToken)
    profile.value = data.profile || null
    if (!profile.value) await loadProfile()
    return profile.value
  }

  async function loadProfile() {
    profile.value = await authApi.profile()
    return profile.value
  }

  // ensureProfile 在应用启动时补一次用户信息（比如刷新了页面）。
  async function ensureProfile() {
    if (!token.value || profile.value) return profile.value
    try {
      return await loadProfile()
    } catch {
      // 令牌失效：拦截器已经处理跳转，这里只需清掉本地残留。
      persist('', '')
      return null
    }
  }

  // clear 只清本地登录态，不调后端。
  //
  // 与 logout 的区别在于用途：logout 是用户主动点的（要通知服务端注销令牌），
  // clear 是「令牌已经无效了」时收拾残局 —— 这时候再发一个带失效令牌的请求
  // 只会再失败一次。
  function clear() {
    persist('', '')
    profile.value = null
  }

  async function logout() {
    try {
      await authApi.logout()
    } catch {
      // 退出接口失败不该拦着用户离开 —— 本地清干净即可。
    }
    persist('', '')
    profile.value = null
  }

  // restore 在应用启动时把本地令牌装回 HTTP 客户端。
  function restore() {
    if (token.value) setAccessToken(token.value)
  }

  return {
    token,
    profile,
    isLoggedIn,
    rights,
    displayName,
    can,
    canAny,
    login,
    loadProfile,
    ensureProfile,
    logout,
    clear,
    restore,
  }
})
