import { apiClient, ApiError } from './client'
import type { AgentSettings, TaskStatus } from './tasks'
import type { RepoDiff } from './projects'

export type MergeRequestStatus = 'open' | 'merged' | 'closed'
export type Verdict = 'approve' | 'request_changes' | 'comment'
export type MergeStrategy = 'merge' | 'squash' | 'fast-forward'

export interface MergeRequest {
  id: number
  projectId: number
  taskId: number
  title: string
  description: string
  sourceBranch: string
  targetBranch: string
  status: MergeRequestStatus
  headSha: string
  baseSha: string
  mergeSha: string
  mergedAt: string | null
  createdAt: string
  updatedAt: string
}

export interface Review {
  id: number
  author: 'user' | 'agent'
  status: 'pending' | 'submitted' | 'failed'
  verdict: Verdict | ''
  body: string
  headSha: string
  workspaceId: number
  runId: number
  agentId: string
  model: string
  effort: string
  autoFix: boolean
  error: string
  createdAt: string
  updatedAt: string
}

// task_agent is the agent working on the merge request's task.
export type CommentAuthor = Review['author'] | 'task_agent'

export interface ReviewComment {
  id: number
  reviewId: number
  // The first comment of the thread this one answers, or 0.
  replyTo: number
  author: CommentAuthor
  path: string
  line: number
  body: string
  headSha: string
  createdAt: string
}

// What a merge request shows of its task.
export interface MergeRequestTask {
  id: number
  status: TaskStatus
  workspaceId: number
  error: string
}

export interface MergeRequestDetail extends MergeRequest {
  reviews: Review[]
  comments: ReviewComment[]
  ahead: number
  behind: number
  task: MergeRequestTask | null
}

export interface CommentInput {
  path: string
  line: number
  body: string
}

export interface MergeResult {
  mergeRequest: MergeRequest
  pushed: boolean
  pushError: string
}

export const mergeRequestApi = {
  list(projectId: number): Promise<{ mergeRequests: MergeRequest[] }> {
    return apiClient.get(`/api/projects/${projectId}/merge-requests`)
  },

  // Opens a merge request for a published branch into the default branch.
  create(projectId: number, input: { title: string; description: string; sourceBranch: string }): Promise<{ mergeRequest: MergeRequest }> {
    return apiClient.post(`/api/projects/${projectId}/merge-requests`, input)
  },

  get(projectId: number, id: number): Promise<{ mergeRequest: MergeRequestDetail }> {
    return apiClient.get(`/api/projects/${projectId}/merge-requests/${id}`)
  },

  diff(projectId: number, id: number): Promise<RepoDiff> {
    return apiClient.get(`/api/projects/${projectId}/merge-requests/${id}/diff`)
  },

  review(projectId: number, id: number, input: { verdict: Verdict; body: string; comments: CommentInput[]; sendToAgent: boolean }): Promise<{ review: Review }> {
    return apiClient.post(`/api/projects/${projectId}/merge-requests/${id}/reviews`, input)
  },

  startAgentReview(projectId: number, id: number, input: AgentSettings & { autoFix: boolean }): Promise<{ review: Review }> {
    return apiClient.post(`/api/projects/${projectId}/merge-requests/${id}/agent-reviews`, input)
  },

  merge(projectId: number, id: number, input: { strategy: MergeStrategy; message?: string; push: boolean }): Promise<MergeResult> {
    return apiClient.post(`/api/projects/${projectId}/merge-requests/${id}/merge`, input)
  },

  close(projectId: number, id: number): Promise<{ mergeRequest: MergeRequest }> {
    return apiClient.post(`/api/projects/${projectId}/merge-requests/${id}/close`, {})
  },

  resolveConflicts(projectId: number, id: number, files: string[]): Promise<void> {
    return apiClient.post(`/api/projects/${projectId}/merge-requests/${id}/resolve-conflicts`, { files })
  },

  // Starts the task's agent again on the feedback, with an optional note.
  sendBack(projectId: number, id: number, input: { message: string }): Promise<{ task: MergeRequestTask }> {
    return apiClient.post(`/api/projects/${projectId}/merge-requests/${id}/send-back`, input)
  },
}

// Returns the conflicting files of a merge the repo refused, or null.
export function mergeConflictsFromError(err: unknown): string[] | null {
  if (!(err instanceof ApiError) || err.status !== 409) return null
  const body = err.body
  if (typeof body !== 'object' || body === null || !('conflicts' in body)) return null
  const conflicts = (body as { conflicts: unknown }).conflicts
  return Array.isArray(conflicts) ? conflicts.filter((c): c is string => typeof c === 'string') : null
}
