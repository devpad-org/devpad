import { apiClient, ApiError } from './client'
import type { AgentSettings, PipelineRounds, TaskStatus } from './tasks'
import type { MergeGateState } from './pipelines'
import type { RepoDiff } from './projects'

export type MergeRequestStatus = 'open' | 'merged' | 'closed'
export type Verdict = 'approve' | 'request_changes' | 'comment'
export type MergeStrategy = 'merge' | 'squash' | 'fast-forward'

export interface MergeRequest {
  id: number
  projectId: number
  taskId: number
  // Who opened it: the user, devpad for a task, or the assistant with the
  // user's approval.
  openedBy: 'user' | 'task_agent' | 'assistant'
  title: string
  description: string
  sourceBranch: string
  targetBranch: string
  status: MergeRequestStatus
  headSha: string
  baseSha: string
  mergeSha: string
  mergedAt: string | null
  // Who merged it: the user, or devpad once a review agent approved it.
  // Empty until it is merged.
  mergedBy: 'user' | 'auto' | ''
  // You merged it although its pipeline hadn't let it through: how the
  // merge gate stood then, and the pipeline that stood. Null otherwise.
  pipelineOverride: PipelineOverride | null
  // The project's automatic review waits for the pipeline to pass.
  reviewAwaitsPipeline: boolean
  // How many files of its change devpad marks for a closer look, for each
  // reason; null while it hasn't worked them out for the head. The files
  // come with the change.
  lookClosely: LookCloselySummary | null
  createdAt: string
  updatedAt: string
  // A review agent is at work on it. Only the list sets it.
  agentReviewing: boolean
}

// Why devpad marks a file of a change for a closer look: it deletes a test,
// or changes the pipeline file, a file the target's jobs run, another CI
// system's configuration, a lint, type-check or test setting, or a build
// script, dependency list, lockfile or toolchain pin. In the order the mark
// shows them, as the server's MarkReasons.
export type LookCloselyReason = 'tests_deleted' | 'pipeline' | 'job_file' | 'ci' | 'checks' | 'build'

// How many files the mark lists for a reason.
export interface ReasonCount {
  reason: LookCloselyReason
  files: number
}

// A file the mark lists. Its paths are the change's, which its author chose.
export interface MarkedFile {
  path: string
  oldPath?: string
  reason: LookCloselyReason
}

// The look-closely mark: files a passing pipeline can't vouch for, at most
// 50 of them, those of the first reasons first, and how many there are of
// each reason, every reason present. It never keeps a merge request from
// merging.
export interface LookClosely {
  files: MarkedFile[]
  reasons: ReasonCount[]
}

// What a merge request keeps of its mark, for the list.
export interface LookCloselySummary {
  reasons: ReasonCount[]
}

// What a merge request changes, and the files of it devpad marks.
export interface MergeRequestChange extends RepoDiff {
  lookClosely: LookClosely
}

export interface PipelineOverride {
  state: MergeGateState
  pipelineId: number | null
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
  // round is which review of the code and review loop this is, from 1.
  round: number
  // What an agent review may spend, zero for no limit, and has spent. A
  // review of a task's merge request spends from the task's budget too.
  budgetUsd: number
  costUsd: number
  error: string
  // Why the review's approval did not merge the change into a branch the
  // project merges into automatically, in a few lowercase words.
  autoMergeError: string
  // The review agent's run has not ended, or devpad is still settling it,
  // so its approval may yet merge the change.
  finishing: boolean
  createdAt: string
  updatedAt: string
}

// REVIEW_ROUNDS is how many reviews an auto-fixing review agent makes, each
// of the task agent's fixes, before requested changes wait for the user.
// It matches maxReviewRounds on the server.
export const REVIEW_ROUNDS = 3

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
  // The thread this comment starts must be addressed before the change can
  // merge; always false on a reply.
  blocking: boolean
  createdAt: string
  // Who resolved the thread this comment starts, and when; empty while it
  // is open, and always on a reply.
  resolvedBy: CommentAuthor | ''
  resolvedAt: string | null
}

// What moved a merge request's source branch: a task's agent finishing a
// round, a publish from the project workspace, or a merge into the branch.
export type PushSource = 'task' | 'workspace' | 'merge' | 'upstream'

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
  // What the task's agent and the reviews of its merge request may spend in
  // all, zero for no limit, and have spent.
  budgetUsd: number
  costUsd: number
  pipelineRounds: PipelineRounds
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

  diff(projectId: number, id: number): Promise<MergeRequestChange> {
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

  // Starts an agent review that stopped before submitting again, raising
  // its own budget or its task's first when given. A review that ran picks
  // up where it stopped; one that never started starts as a new review.
  continueReview(
    projectId: number,
    id: number,
    reviewId: number,
    input: { budgetUsd?: number; taskBudgetUsd?: number },
  ): Promise<{ review: Review }> {
    return apiClient.post(`/api/projects/${projectId}/merge-requests/${id}/agent-reviews/${reviewId}/continue`, input)
  },

  // overridePipeline merges although the pipeline gate is shut: the gate
  // as you saw it when you confirmed merging over it. The server refuses
  // it, with the gate as it stands (pipelineGateFromError), when the state,
  // the pipeline, the head or the target has moved on.
  merge(
    projectId: number,
    id: number,
    input: { strategy: MergeStrategy; message?: string; push: boolean; overridePipeline?: GateConfirmation },
  ): Promise<MergeResult> {
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

// The pipeline gate as you saw it when you confirmed merging over it.
export interface GateConfirmation {
  state: MergeGateState
  pipelineId: number | null
  headSha: string
  targetSha: string
}

// How the pipeline gate stood when it refused a merge: what to ask the user
// to confirm merging over.
export interface PipelineGateRefusal extends GateConfirmation {
  reason: string
  overridable: boolean
  // You had confirmed merging over the gate as it stood before.
  changed: boolean
}

// What merging over the gate g confirms.
export function gateConfirmation(g: GateConfirmation): GateConfirmation {
  return { state: g.state, pipelineId: g.pipelineId, headSha: g.headSha, targetSha: g.targetSha }
}

// Returns how the pipeline gate stood when it refused a merge, or null.
export function pipelineGateFromError(err: unknown): PipelineGateRefusal | null {
  if (!(err instanceof ApiError) || err.status !== 409) return null
  const body = err.body
  if (typeof body !== 'object' || body === null || !('pipelineGate' in body)) return null
  const gate = (body as { pipelineGate: unknown }).pipelineGate
  if (typeof gate !== 'object' || gate === null || typeof (gate as { state?: unknown }).state !== 'string') return null
  return gate as PipelineGateRefusal
}
