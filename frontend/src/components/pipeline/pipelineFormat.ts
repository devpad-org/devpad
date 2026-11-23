import type { JobStatus, Pipeline, PipelineJob, PipelineStatus } from '@/api/pipelines'
import type { MrIconName } from '@/components/project/MrIcon.vue'
import { shortSha } from '@/components/mergerequest/format'

// Presentation helpers shared by the pipeline components.

// What the app says of pipelines starting only by hand and gating nothing,
// in one place for when devpad starts them on its own and merging waits on
// them. The docs say the same in docs/09-pipelines.md (the introduction,
// #branches and #merge-requests), docs/04-branches.md (the Run pipelines
// list) and docs/03-projects.md (the Pipelines surface).
export const PIPELINE_COPY = {
  // Beside where pipelines are run.
  startedByHand: "Devpad doesn't start pipelines on its own yet: run them here, or from a merge request.",
  // Under the Run pipelines ticks.
  tickedBranches:
    "The Project page shows each ticked branch's latest pipeline. Devpad doesn't start pipelines on its own yet: run them on any protected branch, or on a merge request into one, from the Pipelines surface or the merge request.",
  // On a merge request without a pipeline.
  mergeRequestNone: "No pipeline has run on this merge request yet. Run one here: devpad doesn't start them on its own yet.",
  // What a merge request's pipeline decides.
  notGating: "A pipeline's result doesn't block merging yet: it is there for you to weigh.",
} as const

// success passed; active is under way; error failed or could not run;
// warning stopped short; neutral says nothing of the commits.
export type PipelineTone = 'success' | 'active' | 'error' | 'warning' | 'neutral'

export interface StatusLook {
  label: string
  tone: PipelineTone
  icon: MrIconName
  // Shown as a spinner rather than the icon.
  spin?: boolean
}

export const PIPELINE_STATUS: Record<PipelineStatus, StatusLook> = {
  queued: { label: 'Queued', tone: 'active', icon: 'clock' },
  running: { label: 'Running', tone: 'active', icon: 'play', spin: true },
  passed: { label: 'Passed', tone: 'success', icon: 'check' },
  failed: { label: 'Failed', tone: 'error', icon: 'close' },
  error: { label: 'Error', tone: 'error', icon: 'warning' },
  invalid: { label: 'Invalid', tone: 'error', icon: 'warning' },
  conflict: { label: "Doesn't merge", tone: 'error', icon: 'warning' },
  not_configured: { label: 'No pipeline', tone: 'neutral', icon: 'minus' },
  cancelled: { label: 'Cancelled', tone: 'neutral', icon: 'ban' },
  superseded: { label: 'Replaced', tone: 'neutral', icon: 'ban' },
  interrupted: { label: 'Interrupted', tone: 'warning', icon: 'stop' },
}

export const JOB_STATUS: Record<JobStatus, StatusLook> = {
  queued: { label: 'Queued', tone: 'neutral', icon: 'clock' },
  running: { label: 'Running', tone: 'active', icon: 'play', spin: true },
  passed: { label: 'Passed', tone: 'success', icon: 'check' },
  failed: { label: 'Failed', tone: 'error', icon: 'close' },
  timed_out: { label: 'Timed out', tone: 'error', icon: 'clock' },
  out_of_memory: { label: 'Out of memory', tone: 'error', icon: 'warning' },
  error: { label: 'Error', tone: 'error', icon: 'warning' },
  cancelled: { label: 'Cancelled', tone: 'neutral', icon: 'ban' },
  skipped: { label: 'Skipped', tone: 'neutral', icon: 'minus' },
  interrupted: { label: 'Interrupted', tone: 'warning', icon: 'stop' },
}

// How a pipeline went, to follow "Pipeline #12" in a sentence.
export const PIPELINE_OUTCOME: Record<PipelineStatus, string> = {
  queued: 'is queued',
  running: 'is running',
  passed: 'passed',
  failed: 'failed',
  error: "couldn't run",
  invalid: "didn't parse",
  conflict: "found the branch doesn't merge",
  not_configured: 'found no pipeline configured',
  cancelled: 'was cancelled',
  superseded: 'was replaced',
  interrupted: 'was interrupted',
}

// What a pipeline tests, in a few words.
export function subjectLabel(p: Pipeline): string {
  return p.kind === 'branch' ? p.branch : `!${p.mergeRequestId} ${p.sourceBranch} → ${p.targetBranch}`
}

// Where a pipeline's jobs came from.
export function sourceLabel(p: Pipeline): string {
  switch (p.configSource) {
    case 'file':
      return `${p.configPath || '.devpad/pipeline.yml'} on ${p.kind === 'branch' ? p.branch : p.targetBranch}`
    case 'project':
      return "the project's pipeline setting"
    default:
      return 'no pipeline file or setting'
  }
}

// The commits a pipeline tested.
export function commitsLabel(p: Pipeline): string {
  if (p.kind === 'branch') return shortSha(p.headSha)
  return `${shortSha(p.headSha)} onto ${p.targetBranch} at ${shortSha(p.targetSha)}`
}

export function triggerLabel(p: Pipeline): string {
  switch (p.trigger) {
    case 'user':
      return 'Run by you'
    case 'restart':
      return p.rerunOf ? `Run again after devpad restarted, for #${p.rerunOf}` : 'Run again after devpad restarted'
    default:
      return `Started by ${p.trigger}`
  }
}

// A duration as "45s", "3m 12s" or "1h 05m".
export function formatDuration(ms: number): string {
  const s = Math.max(0, Math.round(ms / 1000))
  if (s < 60) return `${s}s`
  const m = Math.floor(s / 60)
  if (m < 60) return `${m}m ${String(s % 60).padStart(2, '0')}s`
  return `${Math.floor(m / 60)}h ${String(m % 60).padStart(2, '0')}m`
}

// How long something ran: until it finished, or until now while it runs.
// Empty when it never started.
export function runTime(item: { startedAt: string | null; finishedAt: string | null }, now: number): string {
  if (!item.startedAt) return ''
  const end = item.finishedAt ? Date.parse(item.finishedAt) : now
  return formatDuration(end - Date.parse(item.startedAt))
}

export function jobFinished(j: PipelineJob): boolean {
  return j.status !== 'queued' && j.status !== 'running'
}

// Why a job that didn't pass didn't, in a sentence, or empty.
// What its status already says, such as a failure's exit status, comes
// first; devpad's own error only for the rest.
export function jobOutcome(j: PipelineJob): string {
  switch (j.status) {
    case 'failed':
      if (j.exitCode !== null) return `Exited with status ${j.exitCode}.`
      break
    case 'timed_out':
      return `Ran past its timeout of ${formatDuration(j.timeoutMs)}.`
    case 'out_of_memory':
      return 'Its container ran out of memory.'
    case 'passed':
      return ''
  }
  return sentence(j.error)
}

// A message as a sentence: capitalised, ending in a full stop.
function sentence(text: string): string {
  const t = text.trim()
  if (!t) return ''
  const s = t.charAt(0).toUpperCase() + t.slice(1)
  return /[.!?]$/.test(s) ? s : `${s}.`
}

// The image without a registry path, which is long and says little.
export function imageLabel(image: string): string {
  return image.slice(image.lastIndexOf('/') + 1)
}

export function formatBytes(n: number): string {
  if (n < 1024) return `${n} B`
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`
  return `${(n / (1024 * 1024)).toFixed(1)} MB`
}
