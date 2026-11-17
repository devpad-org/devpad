import { defineStore } from 'pinia'
import { ref } from 'vue'
import { adminApi, type AdminInvite, type AdminUser, type AdminWorkspace } from '@/api/admin'

export const useAdminStore = defineStore('admin', () => {
  const users = ref<AdminUser[]>([])
  const workspaces = ref<AdminWorkspace[]>([])
  const loading = ref(false)
  const workspacesLoading = ref(false)
  const error = ref('')
  const workspacesError = ref('')
  const invites = ref<AdminInvite[]>([])
  const invitesLoading = ref(false)
  const invitesError = ref('')

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
    // Demoting an admin deletes their unused invites, and renaming one
    // renames them on theirs.
    void fetchInvites({ quiet: true })
    return response.user
  }

  async function resetPassword(id: number, password: string) {
    await adminApi.resetPassword(id, password)
  }

  async function deleteUser(id: number) {
    await adminApi.deleteUser(id)
    users.value = users.value.filter((u) => u.id !== id)
    // Deleting an admin deletes their unused invites.
    void fetchInvites({ quiet: true })
  }

  /** quiet keeps the current list on screen while the new one loads. */
  async function fetchInvites({ quiet = false } = {}) {
    if (!quiet) invitesLoading.value = true
    invitesError.value = ''
    try {
      const response = await adminApi.listInvites()
      invites.value = response.invites
    } catch (e) {
      invitesError.value = e instanceof Error ? e.message : 'Failed to load invites'
    } finally {
      invitesLoading.value = false
    }
  }

  async function createInvite(data: { isAdmin: boolean; validDays: number }) {
    const { invite } = await adminApi.createInvite(data)
    // The list never holds a token: it is shown once, then gone.
    const { token: _token, ...listed } = invite
    invites.value.unshift(listed)
    return invite
  }

  async function revokeInvite(id: number) {
    await adminApi.revokeInvite(id)
    invites.value = invites.value.filter((i) => i.id !== id)
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
    invites,
    invitesLoading,
    invitesError,
    fetchUsers,
    createUser,
    updateUser,
    resetPassword,
    deleteUser,
    fetchInvites,
    createInvite,
    revokeInvite,
    fetchWorkspaces,
    updateWorkspaceLimits,
  }
})
