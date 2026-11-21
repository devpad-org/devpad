import type { TaskStatus } from '@/api/tasks'
import type { MrIconName } from './MrIcon.vue'

export type TaskTone = 'neutral' | 'active' | 'review' | 'attention' | 'error' | 'success'

// The stages every task moves through, in order.
export const TASK_STAGES = ['To do', 'Agent', 'Review', 'Done'] as const

// Where each status sits on the to do → agent → review → done track.
export const TASK_STATUS: Record<TaskStatus, { step: number; tone: TaskTone; icon: MrIconName; label: string }> = {
  todo: { step: 0, tone: 'neutral', icon: 'circle', label: 'To do' },
  queued: { step: 1, tone: 'active', icon: 'clock', label: 'Queued' },
  in_progress: { step: 1, tone: 'active', icon: 'bot', label: 'Agent working' },
  failed: { step: 1, tone: 'error', icon: 'alert', label: 'Failed' },
  in_review: { step: 2, tone: 'review', icon: 'eye', label: 'In review' },
  changes_requested: { step: 2, tone: 'attention', icon: 'comment', label: 'Changes requested' },
  merged: { step: 3, tone: 'success', icon: 'merged', label: 'Merged' },
  cancelled: { step: 3, tone: 'neutral', icon: 'ban', label: 'Cancelled' },
}

// Whether a task starting from base can wait for other tasks. A task's merge
// request merges into the project's default branch, so only a task that
// starts from it would find their work there; the server refuses the rest.
// A task's baseBranch is '' when it starts from the default branch.
export function canWaitFrom(base: string, defaultBranch: string): boolean {
  return base === '' || base === defaultBranch
}
