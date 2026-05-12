import { computed, ref } from 'vue'
import { defineStore } from 'pinia'
import { aiApi, type AgentRun, type AgentRunStatus } from '@/api/ai'

export { type AgentRun, type AgentRunStatus }

export function isAgentRunActiveStatus(status: AgentRunStatus): boolean {
  return status === 'queued' || status === 'running' || status === 'waiting_approval'
}

export const useAgentRunStore = defineStore('agentRuns', () => {
  const runs = ref<AgentRun[]>([])
  const loading = ref(false)
  const error = ref<string | null>(null)
  const cancellingRunIds = ref<Set<number>>(new Set())

  const activeRuns = computed(() => runs.value.filter((run) => isAgentRunActiveStatus(run.status)))

  function upsertRun(run: AgentRun): void {
    const index = runs.value.findIndex((existing) => existing.id === run.id)
    if (index >= 0) {
      runs.value[index] = run
      return
    }

    runs.value = [run, ...runs.value]
  }

  function isRunCancelling(runId: number): boolean {
    return cancellingRunIds.value.has(runId)
  }

  function setRunCancelling(runId: number, cancelling: boolean): void {
    const next = new Set(cancellingRunIds.value)
    if (cancelling) {
      next.add(runId)
    } else {
      next.delete(runId)
    }
    cancellingRunIds.value = next
  }

  function markRunCancelled(runId: number): void {
    const index = runs.value.findIndex((run) => run.id === runId)
    if (index < 0) return

    runs.value[index] = {
      ...runs.value[index],
      status: 'cancelled',
      updatedAt: new Date().toISOString(),
    }
  }

  async function fetchRuns(workspaceId: number): Promise<void> {
    if (workspaceId <= 0) {
      runs.value = []
      error.value = 'Workspace is required to load agent runs'
      return
    }

    loading.value = true
    try {
      const res = await aiApi.listAgentRuns(workspaceId)
      runs.value = res.runs
      error.value = null
    } catch (err) {
      error.value = err instanceof Error ? err.message : 'Failed to load agent runs'
    } finally {
      loading.value = false
    }
  }

  async function refreshRun(runId: number): Promise<AgentRun | null> {
    if (runId <= 0) {
      error.value = 'Agent run is required'
      return null
    }

    try {
      const res = await aiApi.getAgentRun(runId)
      upsertRun(res.run)
      error.value = null
      return res.run
    } catch (err) {
      error.value = err instanceof Error ? err.message : 'Failed to load agent run'
      return null
    }
  }

  async function cancelRun(runId: number): Promise<boolean> {
    if (runId <= 0) {
      error.value = 'Agent run is required'
      return false
    }
    if (isRunCancelling(runId)) return false

    setRunCancelling(runId, true)
    try {
      await aiApi.cancelAgentRun(runId)
      markRunCancelled(runId)
      error.value = null
      return true
    } catch (err) {
      error.value = err instanceof Error ? err.message : 'Failed to cancel agent run'
      return false
    } finally {
      setRunCancelling(runId, false)
    }
  }

  return {
    runs,
    loading,
    error,
    activeRuns,
    upsertRun,
    fetchRuns,
    refreshRun,
    cancelRun,
    isRunCancelling,
  }
})
