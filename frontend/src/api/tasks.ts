import { apiClient } from './client'

export type TaskStatus =
  | 'todo'
  | 'queued'
  | 'in_progress'
  | 'in_review'
  | 'changes_requested'
  | 'merged'
  | 'cancelled'
  | 'failed'

export interface Task {
  id: number
  projectId: number
  title: string
  body: string
  status: TaskStatus
  branch: string
  workspaceId: number
  conversationId: number
  runId: number
  agentId: string
  model: string
  effort: string
  pendingMessage: string
  headSha: string
  // The project branch the task started from, when not the default branch.
  baseBranch: string
  // The task continues a conversation handed off from the project workspace.
  handedOff: boolean
  // What every round of the task's agent may spend in all, in USD; zero
  // means no limit. costUsd counts only models with known prices.
  budgetUsd: number
  costUsd: number
  // Links to the project's other tasks, by id. A task waits for those it is
  // blockedBy; blocked says one of them is not merged yet, and
  // startWhenUnblocked that its agent starts once none is.
  blockedBy: number[]
  blocks: number[]
  related: number[]
  blocked: boolean
  startWhenUnblocked: boolean
  error: string
  createdAt: string
  updatedAt: string
}

// How a task is linked to another, from the first task's side.
export type TaskLinkKind = 'blocked_by' | 'blocks' | 'related'

export interface AgentSettings {
  agentId?: string
  model?: string
  effort?: string
}

export const taskApi = {
  list(projectId: number): Promise<{ tasks: Task[] }> {
    return apiClient.get(`/api/projects/${projectId}/tasks`)
  },

  // Without budgetUsd the task gets the user's default budget; zero means no limit.
  create(
    projectId: number,
    input: {
      title: string
      body: string
      start: boolean
      baseBranch?: string
      budgetUsd?: number
      blockedBy?: number[]
      related?: number[]
    } & AgentSettings,
  ): Promise<{ task: Task }> {
    return apiClient.post(`/api/projects/${projectId}/tasks`, input)
  },

  // Hands a conversation from the project workspace off to a new task, which
  // starts at once on a branch made from baseBranch.
  handoff(
    projectId: number,
    input: { conversationId: number; title: string; instructions: string; baseBranch: string } & AgentSettings,
  ): Promise<{ task: Task }> {
    return apiClient.post(`/api/projects/${projectId}/tasks/handoff`, input)
  },

  update(projectId: number, id: number, input: { title: string; body: string; budgetUsd?: number }): Promise<{ task: Task }> {
    return apiClient.put(`/api/projects/${projectId}/tasks/${id}`, input)
  },

  // Replaces the task's budget alone; zero means no limit.
  setBudget(projectId: number, id: number, budgetUsd: number): Promise<{ task: Task }> {
    return apiClient.put(`/api/projects/${projectId}/tasks/${id}/budget`, { budgetUsd })
  },

  delete(projectId: number, id: number): Promise<void> {
    return apiClient.delete(`/api/projects/${projectId}/tasks/${id}`)
  },

  start(projectId: number, id: number, input: { message?: string } & AgentSettings = {}): Promise<{ task: Task }> {
    return apiClient.post(`/api/projects/${projectId}/tasks/${id}/start`, input)
  },

  cancel(projectId: number, id: number): Promise<{ task: Task }> {
    return apiClient.post(`/api/projects/${projectId}/tasks/${id}/cancel`, {})
  },

  // Drops a blocked task's request to start when unblocked.
  cancelStart(projectId: number, id: number): Promise<{ task: Task }> {
    return apiClient.post(`/api/projects/${projectId}/tasks/${id}/cancel-start`, {})
  },

  link(projectId: number, id: number, kind: TaskLinkKind, otherId: number): Promise<{ task: Task }> {
    return apiClient.post(`/api/projects/${projectId}/tasks/${id}/links`, { kind, taskId: otherId })
  },

  // Removes the link between two tasks, whichever kind it is. Both tasks'
  // links change, and a task the link held back may start, so reload the list.
  unlink(projectId: number, id: number, otherId: number): Promise<void> {
    return apiClient.delete(`/api/projects/${projectId}/tasks/${id}/links/${otherId}`)
  },
}
