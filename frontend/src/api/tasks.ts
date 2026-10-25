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
  error: string
  createdAt: string
  updatedAt: string
}

export interface AgentSettings {
  agentId?: string
  model?: string
  effort?: string
}

export const taskApi = {
  list(projectId: number): Promise<{ tasks: Task[] }> {
    return apiClient.get(`/api/projects/${projectId}/tasks`)
  },

  create(projectId: number, input: { title: string; body: string; start: boolean } & AgentSettings): Promise<{ task: Task }> {
    return apiClient.post(`/api/projects/${projectId}/tasks`, input)
  },

  update(projectId: number, id: number, title: string, body: string): Promise<{ task: Task }> {
    return apiClient.put(`/api/projects/${projectId}/tasks/${id}`, { title, body })
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
}
