import { defineStore } from 'pinia'
import { ref, reactive } from 'vue'
import { workspaceApi, type Workspace } from '@/api/workspaces'

export type TransitionState = 'starting' | 'stopping'

export const useWorkspaceStore = defineStore('workspaces', () => {
  const workspaces = ref<Workspace[]>([])
  const error = ref<string | null>(null)
  const transitioning = reactive(new Map<number, TransitionState>())

  // A failed fetch keeps the last list and reports the error until a fetch
  // succeeds, so background polls don't flicker the page.
  async function fetchWorkspaces() {
    try {
      const response = await workspaceApi.list()
      workspaces.value = response.workspaces
      error.value = null
    } catch (e) {
      error.value = e instanceof Error ? e.message : 'Failed to load workspaces'
      throw e
    }
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
    error,
    transitioning,
    fetchWorkspaces,
    setDefaultAgent,
    deleteWorkspace,
    startWorkspace,
    stopWorkspace,
  }
})
