import type { RepoDiffFile } from '@/api/projects'
import type { CommentAuthor, LookClosely, LookCloselyReason, LookCloselySummary, MarkedFile, MergeStrategy, Push, Review, Verdict } from '@/api/mergeRequests'
import type { MrIconName } from '@/components/project/MrIcon.vue'

// Presentation helpers shared by the merge request components.

export type Author = Review['author']
export type Tone = 'success' | 'active' | 'review' | 'attention' | 'error' | 'warning' | 'neutral'

export const VERDICTS: { value: Verdict; label: string; hint: string; icon: MrIconName; tone: Tone }[] = [
  { value: 'comment', label: 'Comment', hint: 'General feedback without a decision.', icon: 'comment', tone: 'review' },
  { value: 'approve', label: 'Approve', hint: 'The change is ready to merge.', icon: 'check', tone: 'success' },
  { value: 'request_changes', label: 'Request changes', hint: 'Feedback that must be addressed before merging.', icon: 'alert', tone: 'attention' },
]

export const STRATEGIES: { value: MergeStrategy; label: string; hint: string }[] = [
  { value: 'merge', label: 'Merge commit', hint: 'Keeps every commit and records a merge commit.' },
  { value: 'squash', label: 'Squash', hint: 'Combines the branch into a single commit.' },
  { value: 'fast-forward', label: 'Fast-forward', hint: 'Moves the target branch forward; no new commit.' },
]

// What a change does to the files the look-closely mark lists, by reason,
// in the order the mark shows them: the server's MarkReasons, which tell
// the review agent the same.
export const LOOK_CLOSELY: Record<LookCloselyReason, string> = {
  tests_deleted: 'Deletes tests',
  pipeline: 'Changes the pipeline file',
  job_file: "Changes files the pipeline's jobs run",
  ci: 'Changes CI configuration',
  checks: 'Changes lint, type-check or test settings',
  build: 'Changes build scripts, dependencies or toolchains',
}

// Why the mark is there, beside it.
export const LOOK_CLOSELY_HINT =
  "These files decide what the pipeline's jobs prove, which a passing pipeline can't vouch for: read them as closely as the code. It's a mark, not a gate."

// The files a mark lists, by reason, in the order the mark shows them,
// with how many it counts of each, which may be more than it names.
export function lookCloselyGroups(mark: LookClosely): { reason: LookCloselyReason; count: number; files: MarkedFile[] }[] {
  return mark.reasons.map((c) => ({ reason: c.reason, count: c.files, files: mark.files.filter((f) => f.reason === c.reason) }))
}

// What a mark says, in a line: "Deletes tests (2); changes build scripts,
// dependencies or toolchains (3)".
export function lookCloselySummary(mark: LookCloselySummary): string {
  return mark.reasons.map((c, i) => `${i === 0 ? LOOK_CLOSELY[c.reason] : LOOK_CLOSELY[c.reason].toLowerCase()} (${c.files})`).join('; ')
}

export function plural(count: number, noun: string): string {
  return `${count} ${noun}${count === 1 ? '' : 's'}`
}

export function dirName(path: string): string {
  const i = path.lastIndexOf('/')
  return i === -1 ? '' : path.slice(0, i)
}

export function baseName(path: string): string {
  return path.slice(path.lastIndexOf('/') + 1)
}

export function authorName(author: CommentAuthor): string {
  if (author === 'agent') return 'Review agent'
  if (author === 'task_agent') return 'Task agent'
  if (author === 'assistant') return 'Assistant'
  return 'You'
}

// Who resolved a thread, for the middle of a sentence.
export function resolverName(author: CommentAuthor | ''): string {
  return !author || author === 'user' ? 'you' : authorName(author).toLowerCase()
}

export function authorIcon(author: CommentAuthor): MrIconName {
  return author === 'user' ? 'user' : 'bot'
}

export function shortSha(sha: string): string {
  return sha ? sha.slice(0, 8) : ''
}

// Five blocks split between additions and deletions, like a diffstat.
export function statBlocks(additions: number, deletions: number): ('add' | 'del' | 'none')[] {
  const total = additions + deletions
  if (total === 0) return Array(5).fill('none')
  const add = Math.round((additions / total) * 5)
  return Array.from({ length: 5 }, (_, i) => (i < add ? 'add' : 'del'))
}

// What a push did, given how many of its commits are still on the branch;
// none means it only moved the branch, as a rewrite or a reset does.
export function pushSummary(push: Push, commits: number, branch: string): string {
  const head = shortSha(push.headSha)
  switch (push.source) {
    case 'task':
      return commits ? `Task agent added ${plural(commits, 'commit')}` : `Task agent moved ${branch} to ${head}`
    case 'workspace':
      return commits ? `Published ${plural(commits, 'commit')} from the project workspace` : `Published ${head} from the project workspace`
    case 'upstream':
      return commits ? `Fast-forwarded ${branch} by ${plural(commits, 'commit')} from the upstream` : `Fast-forwarded ${branch} to ${head} from the upstream`
    default:
      return commits ? `A merge into ${branch} added ${plural(commits, 'commit')}` : `A merge into ${branch} moved it to ${head}`
  }
}

export function reviewTone(review: Review): Tone {
  if (review.status === 'pending') return 'active'
  if (review.status === 'failed') return 'error'
  if (review.verdict === 'approve') return 'success'
  if (review.verdict === 'request_changes') return 'attention'
  return 'review'
}

export function reviewIcon(review: Review): MrIconName {
  if (review.status === 'pending') return 'clock'
  if (review.status === 'failed') return 'warning'
  if (review.verdict === 'approve') return 'check'
  if (review.verdict === 'request_changes') return 'alert'
  return 'comment'
}

// Why a review's approval did not merge the change automatically, ready to
// end a sentence with a full stop.
export function autoMergeReason(review: Review): string {
  return review.autoMergeError.trim().replace(/[.\s]+$/, '')
}

export function reviewAction(review: Review): string {
  if (review.status === 'pending') return review.workspaceId ? 'is reviewing the change' : 'is waiting for a sandbox'
  if (review.status === 'failed') return 'could not finish the review'
  if (review.verdict === 'approve') return 'approved these changes'
  if (review.verdict === 'request_changes') return 'requested changes'
  return 'left a review'
}

export function fileStatusLabel(file: RepoDiffFile): string {
  switch (file.status) {
    case 'A':
      return 'added'
    case 'D':
      return 'deleted'
    case 'R':
      return 'renamed'
    case 'C':
      return 'copied'
    default:
      return 'modified'
  }
}

export function errorText(e: unknown, fallback: string): string {
  return e instanceof Error ? e.message : fallback
}
