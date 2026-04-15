import { defineStore } from 'pinia'
import { ref, computed } from 'vue'
import { authApi, type User } from '@/api/auth'

export const useAuthStore = defineStore('auth', () => {
  const user = ref<User | null>(null)
  const needsSetup = ref(false)
  const loading = ref(true)

  const isAuthenticated = computed(() => user.value !== null)
  const isAdmin = computed(() => user.value?.isAdmin ?? false)

  async function checkStatus() {
    loading.value = true
    try {
      // Check if initial setup is needed
      const setupCheck = await authApi.checkSetup()
      needsSetup.value = setupCheck.needsSetup
      if (setupCheck.needsSetup) {
        return
      }

      // Try to restore session
      const response = await authApi.me()
      user.value = response.user
    } catch {
      user.value = null
    } finally {
      loading.value = false
    }
  }

  async function login(username: string, password: string) {
    const response = await authApi.login(username, password)
    user.value = response.user
    needsSetup.value = false
  }

  async function setup(username: string, email: string, password: string) {
    const response = await authApi.setup(username, email, password)
    user.value = response.user
    needsSetup.value = false
  }

  async function logout() {
    await authApi.logout()
    user.value = null
  }

  return {
    user,
    needsSetup,
    loading,
    isAuthenticated,
    isAdmin,
    checkStatus,
    login,
    setup,
    logout,
  }
})
