import { defineStore } from 'pinia'
import { ref } from 'vue'
import { adminApi, type AdminUser, type AdminWorkspace } from '@/api/admin'

export const useAdminStore = defineStore('admin', () => {
  const users = ref<AdminUser[]>([])
  const workspaces = ref<AdminWorkspace[]>([])
  const loading = ref(false)
  const workspacesLoading = ref(false)
  const error = ref('')
  const workspacesError = ref('')

  async function fetchUsers() {
    loading.value = true
    error.value = ''
    try {
      const response = await adminApi.listUsers()
      users.value = response.users
    } catch (e) {
      error.value = e instanceof Error ? e.message : 'Failed to load users'
    } finally {
      loading.value = false
    }
  }

  async function createUser(data: { username: string; email: string; password: string; isAdmin: boolean }) {
    const response = await adminApi.createUser(data)
    users.value.push(response.user)
    return response.user
  }

  async function updateUser(id: number, data: { username: string; email: string; isAdmin: boolean }) {
    const response = await adminApi.updateUser(id, data)
    const index = users.value.findIndex((u) => u.id === id)
    if (index !== -1) {
      users.value[index] = response.user
    }
    return response.user
  }

  async function resetPassword(id: number, password: string) {
    await adminApi.resetPassword(id, password)
  }

  async function deleteUser(id: number) {
    await adminApi.deleteUser(id)
    users.value = users.value.filter((u) => u.id !== id)
  }

  async function fetchWorkspaces() {
    workspacesLoading.value = true
    workspacesError.value = ''
    try {
      const response = await adminApi.listWorkspaces()
      workspaces.value = response.workspaces
    } catch (e) {
      workspacesError.value = e instanceof Error ? e.message : 'Failed to load workspaces'
    } finally {
      workspacesLoading.value = false
    }
  }

  async function updateWorkspaceLimits(id: number, data: { memoryLimit: number; nanoCpus: number }) {
    const response = await adminApi.updateWorkspaceLimits(id, data)
    const index = workspaces.value.findIndex((w) => w.id === id)
    if (index !== -1) {
      workspaces.value[index] = response.workspace
    }
    return response.workspace
  }

  return {
    users,
    workspaces,
    loading,
    workspacesLoading,
    error,
    workspacesError,
    fetchUsers,
    createUser,
    updateUser,
    resetPassword,
    deleteUser,
    fetchWorkspaces,
    updateWorkspaceLimits,
  }
})
