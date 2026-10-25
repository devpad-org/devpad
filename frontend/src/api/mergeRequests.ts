import { apiClient, ApiError } from './client'
import type { AgentSettings } from './tasks'
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
  autoFix: boolean
  error: string
  createdAt: string
  updatedAt: string
}

export interface ReviewComment {
  id: number
  reviewId: number
  author: 'user' | 'agent'
  path: string
  line: number
  body: string
  headSha: string
  createdAt: string
}

export interface MergeRequestDetail extends MergeRequest {
  reviews: Review[]
  comments: ReviewComment[]
  ahead: number
  behind: number
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
}

// Returns the conflicting files of a merge the repo refused, or null.
export function mergeConflictsFromError(err: unknown): string[] | null {
  if (!(err instanceof ApiError) || err.status !== 409) return null
  const body = err.body
  if (typeof body !== 'object' || body === null || !('conflicts' in body)) return null
  const conflicts = (body as { conflicts: unknown }).conflicts
  return Array.isArray(conflicts) ? conflicts.filter((c): c is string => typeof c === 'string') : null
}
