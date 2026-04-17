import { defineStore } from 'pinia'
import { ref } from 'vue'
import { workspaceApi, type Workspace } from '@/api/workspaces'

export const useWorkspaceStore = defineStore('workspaces', () => {
  const workspaces = ref<Workspace[]>([])
  const loading = ref(false)
  const error = ref<string | null>(null)

  async function fetchWorkspaces() {
    loading.value = true
    error.value = null
    try {
      const response = await workspaceApi.list()
      workspaces.value = response.workspaces
    } catch (e) {
      error.value = 'Failed to load workspaces'
      throw e
    } finally {
      loading.value = false
    }
  }

  async function createWorkspace(name: string, description: string) {
    const response = await workspaceApi.create(name, description)
    workspaces.value.unshift(response.workspace)
    return response.workspace
  }

  async function updateWorkspace(id: number, name: string, description: string) {
    const response = await workspaceApi.update(id, name, description)
    const index = workspaces.value.findIndex((w) => w.id === id)
    if (index !== -1) {
      workspaces.value[index] = response.workspace
    }
    return response.workspace
  }

  async function deleteWorkspace(id: number) {
    await workspaceApi.delete(id)
    workspaces.value = workspaces.value.filter((w) => w.id !== id)
  }

  async function startWorkspace(id: number) {
    const response = await workspaceApi.start(id)
    const index = workspaces.value.findIndex((w) => w.id === id)
    if (index !== -1) {
      workspaces.value[index] = response.workspace
    }
    return response.workspace
  }

  async function stopWorkspace(id: number) {
    const response = await workspaceApi.stop(id)
    const index = workspaces.value.findIndex((w) => w.id === id)
    if (index !== -1) {
      workspaces.value[index] = response.workspace
    }
    return response.workspace
  }

  return {
    workspaces,
    loading,
    error,
    fetchWorkspaces,
    createWorkspace,
    updateWorkspace,
    deleteWorkspace,
    startWorkspace,
    stopWorkspace,
  }
})
