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

// task_agent is the agent working on the merge request's task, and
// assistant the assistant in the project's own workspace.
export type CommentAuthor = Review['author'] | 'task_agent' | 'assistant'

export interface ReviewComment {
  id: number
  reviewId: number
  // The first comment of the thread this one answers, or 0 when this one
  // starts a thread.
  replyTo: number
  author: CommentAuthor
  path: string
  line: number
  body: string
  headSha: string
  createdAt: string
  // Who resolved the thread this comment starts, and when; empty while it
  // is open, and always on a reply.
  resolvedBy: CommentAuthor | ''
  resolvedAt: string | null
}

// What moved a merge request's source branch: a task's agent finishing a
// round, a publish from the project workspace, or a merge into the branch.
export type PushSource = 'task' | 'workspace' | 'merge'

// The source branch moving from beforeSha to headSha, at the time devpad
// saw it.
export interface Push {
  id: number
  source: PushSource
  beforeSha: string
  headSha: string
  createdAt: string
}

// A commit the merge request proposes. authoredAt is the commit's own date,
// which whoever made it chose.
export interface Commit {
  sha: string
  parents: string[]
  author: string
  authoredAt: string
  subject: string
}

// The commits a merge request proposes, newest first. truncated says there
// were more than listed: the oldest are left out.
export interface CommitLog {
  commits: Commit[]
  truncated: boolean
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
  // The moves of the source branch since it was opened, oldest first.
  pushes: Push[]
  ahead: number
  behind: number
  task: MergeRequestTask | null
}

export interface CommentInput {
  path: string
  line: number
  body: string
}

// A comment left at once, outside a review: a reply in the thread of
// replyTo, else a new thread on a line of path, or on the whole change.
export interface NewComment {
  body: string
  replyTo?: number
  path?: string
  line?: number
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

  // Opens a merge request for a published branch into a protected branch,
  // the default branch unless targetBranch says otherwise.
  create(
    projectId: number,
    input: { title: string; description: string; sourceBranch: string; targetBranch?: string },
  ): Promise<{ mergeRequest: MergeRequest }> {
    return apiClient.post(`/api/projects/${projectId}/merge-requests`, input)
  },

  get(projectId: number, id: number): Promise<{ mergeRequest: MergeRequestDetail }> {
    return apiClient.get(`/api/projects/${projectId}/merge-requests/${id}`)
  },

  diff(projectId: number, id: number): Promise<RepoDiff> {
    return apiClient.get(`/api/projects/${projectId}/merge-requests/${id}/diff`)
  },

  commits(projectId: number, id: number): Promise<CommitLog> {
    return apiClient.get(`/api/projects/${projectId}/merge-requests/${id}/commits`)
  },

  review(projectId: number, id: number, input: { verdict: Verdict; body: string; comments: CommentInput[]; sendToAgent: boolean }): Promise<{ review: Review }> {
    return apiClient.post(`/api/projects/${projectId}/merge-requests/${id}/reviews`, input)
  },

  comment(projectId: number, id: number, input: NewComment): Promise<{ comment: ReviewComment }> {
    return apiClient.post(`/api/projects/${projectId}/merge-requests/${id}/comments`, input)
  },

  // Resolves the thread a comment is in, or opens it again, and returns the
  // thread's first comment.
  resolveThread(projectId: number, id: number, commentId: number, resolved: boolean): Promise<{ comment: ReviewComment }> {
    return apiClient.post(`/api/projects/${projectId}/merge-requests/${id}/comments/${commentId}/resolve`, { resolved })
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
