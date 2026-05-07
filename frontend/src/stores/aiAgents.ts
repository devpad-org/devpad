import { computed, ref } from 'vue'
import { defineStore } from 'pinia'
import { aiApi, type AIAgent, type SaveAgentPayload } from '@/api/ai'

export { type AIAgent, type SaveAgentPayload }

export const useAiAgentStore = defineStore('aiAgents', () => {
  const agents = ref<AIAgent[]>([])
  const loading = ref(false)
  const error = ref<string | null>(null)
  const selectedAgentId = ref('default')

  const selectedAgent = computed(() => (
    agents.value.find((agent) => agent.id === selectedAgentId.value) ?? agents.value[0] ?? null
  ))

  async function fetchAgents(workspaceId: number): Promise<void> {
    if (workspaceId <= 0) {
      agents.value = []
      error.value = 'Workspace is required to load agents'
      return
    }
    loading.value = true
    try {
      const res = await aiApi.listAgents(workspaceId)
      agents.value = res.agents
      if (!agents.value.some((agent) => agent.id === selectedAgentId.value)) {
        selectedAgentId.value = agents.value[0]?.id ?? 'default'
      }
      error.value = null
    } catch (err) {
      error.value = err instanceof Error ? err.message : 'Failed to load agents'
    } finally {
      loading.value = false
    }
  }

  async function createAgent(payload: SaveAgentPayload): Promise<AIAgent | null> {
    try {
      const res = await aiApi.createAgent(payload)
      upsertAgent(res.agent)
      selectedAgentId.value = res.agent.id
      error.value = null
      return res.agent
    } catch (err) {
      error.value = err instanceof Error ? err.message : 'Failed to create agent'
      return null
    }
  }

  async function updateAgent(id: string, payload: SaveAgentPayload): Promise<AIAgent | null> {
    try {
      const res = await aiApi.updateAgent(id, payload)
      upsertAgent(res.agent)
      error.value = null
      return res.agent
    } catch (err) {
      error.value = err instanceof Error ? err.message : 'Failed to update agent'
      return null
    }
  }

  async function deleteAgent(id: string): Promise<boolean> {
    try {
      await aiApi.deleteAgent(id)
      agents.value = agents.value.filter((agent) => agent.id !== id)
      if (selectedAgentId.value === id) {
        selectedAgentId.value = agents.value[0]?.id ?? 'default'
      }
      error.value = null
      return true
    } catch (err) {
      error.value = err instanceof Error ? err.message : 'Failed to delete agent'
      return false
    }
  }

  function setSelectedAgent(id: string): void {
    selectedAgentId.value = id
  }

  function upsertAgent(agent: AIAgent): void {
    const index = agents.value.findIndex((existing) => existing.id === agent.id)
    if (index >= 0) {
      agents.value[index] = agent
    } else {
      agents.value = [...agents.value, agent]
    }
  }

  return {
    agents,
    loading,
    error,
    selectedAgentId,
    selectedAgent,
    fetchAgents,
    createAgent,
    updateAgent,
    deleteAgent,
    setSelectedAgent,
  }
})
