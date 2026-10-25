import { defineStore } from 'pinia'
import { ref, reactive } from 'vue'
import { workspaceApi, type Workspace } from '@/api/workspaces'

export type TransitionState = 'starting' | 'stopping'

export const useWorkspaceStore = defineStore('workspaces', () => {
  const workspaces = ref<Workspace[]>([])
  const loading = ref(false)
  const loaded = ref(false)
  const error = ref<string | null>(null)
  const transitioning = reactive(new Map<number, TransitionState>())

  // Only a fetch before the first successful load shows loading, and an
  // error stays until a fetch succeeds, so background polls don't flicker
  // the page between its states.
  async function fetchWorkspaces() {
    const initial = !loaded.value
    if (initial && !error.value) loading.value = true
    try {
      const response = await workspaceApi.list()
      workspaces.value = response.workspaces
      loaded.value = true
      error.value = null
    } catch (e) {
      if (initial) error.value = 'Failed to load workspaces'
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

  async function setDefaultAgent(id: number, agentId: string) {
    const response = await workspaceApi.setDefaultAgent(id, agentId)
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
    transitioning.set(id, 'starting')
    try {
      const response = await workspaceApi.start(id)
      const index = workspaces.value.findIndex((w) => w.id === id)
      if (index !== -1) {
        workspaces.value[index] = response.workspace
      }
      return response.workspace
    } finally {
      transitioning.delete(id)
    }
  }

  async function stopWorkspace(id: number) {
    transitioning.set(id, 'stopping')
    try {
      const response = await workspaceApi.stop(id)
      const index = workspaces.value.findIndex((w) => w.id === id)
      if (index !== -1) {
        workspaces.value[index] = response.workspace
      }
      return response.workspace
    } finally {
      transitioning.delete(id)
    }
  }

  return {
    workspaces,
    loading,
    loaded,
    error,
    transitioning,
    fetchWorkspaces,
    createWorkspace,
    updateWorkspace,
    setDefaultAgent,
    deleteWorkspace,
    startWorkspace,
    stopWorkspace,
  }
})
