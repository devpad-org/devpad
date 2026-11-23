import type { JobStatus, MergeGateState, NoResult, Pipeline, PipelineJob, PipelineStanding, PipelineStatus } from '@/api/pipelines'
import type { MrIconName } from '@/components/project/MrIcon.vue'
import { shortSha } from '@/components/mergerequest/format'

// Presentation helpers shared by the pipeline components. What the app
// says that the docs say too is in pipelineCopy.ts.

// How the merge gate stood when you merged over it, to follow "merged
// although".
export const GATE_OVERRIDDEN: Record<MergeGateState, string> = {
  unguarded: 'no pipeline guarded it',
  passed: 'its pipeline passed',
  queued: 'its pipeline was still queued',
  running: 'its pipeline was still running',
  not_run: 'no pipeline had run on it',
  out_of_date: 'its pipeline was out of date',
  failed: 'its pipeline failed',
  conflict: "its pipeline found it doesn't merge",
  invalid: "its pipeline didn't parse",
  error: "its pipeline couldn't run",
  cancelled: 'its pipeline was cancelled',
  closed: 'it could no longer merge',
}

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

// A badge's look for a result of older commits or jobs than its subject
// has now, on a branch and in the list of merge requests alike.
export const PIPELINE_OUT_OF_DATE: StatusLook = { label: 'Out of date', tone: 'neutral', icon: 'clock' }

// A badge's look for the pipeline of a subject's commits and jobs that
// ended without a result: cancelled, or couldn't run.
export function noResultLook(n: NoResult): StatusLook {
  return n.pipeline.status === 'cancelled' ? PIPELINE_STATUS.cancelled : { ...PIPELINE_STATUS.error, label: "Couldn't run" }
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

// The pipeline a card or badge shows of how a subject's pipelines stand:
// the one of its commits and jobs as they are now, else the newest of them
// that ended without a result, else its latest, which is then out of date.
export function shownOf(st: PipelineStanding | null | undefined): Pipeline | null {
  return st?.current ?? st?.noResult?.pipeline ?? st?.latest ?? null
}

// Whether pipeline id, shown for a standing, is of other commits or jobs
// than the subject has now: neither its current pipeline nor the one of
// them that ended without a result.
export function staleIn(st: PipelineStanding, id: number): boolean {
  return id !== st.current?.id && id !== st.noResult?.pipeline.id
}

// What a pipeline tests, in a few words, and whether it runs the jobs a
// change proposes.
export function subjectLabel(p: Pipeline): string {
  if (p.kind === 'branch') return p.branch
  const subject = `!${p.mergeRequestId} ${p.sourceBranch} → ${p.targetBranch}`
  return p.purpose === 'proposed' ? `${subject}, proposed` : subject
}

// Where a pipeline's jobs came from.
export function sourceLabel(p: Pipeline): string {
  if (p.purpose === 'proposed') return `${p.configPath || '.devpad/pipeline.yml'} as ${p.sourceBranch} proposes it`
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

// What started a pipeline: the user, or what devpad ran it by itself for.
export function triggerLabel(p: Pipeline): string {
  const ticked = p.kind === 'branch' ? p.branch : p.targetBranch
  switch (p.trigger) {
    case 'user':
      return 'Run by you'
    case 'restart':
      return p.rerunOf ? `Run again after devpad restarted, for #${p.rerunOf}` : 'Run again after devpad restarted'
    case 'opened':
      return 'Started when the merge request opened'
    case 'source_moved':
      return `Started when ${p.sourceBranch} got new commits`
    case 'target_moved':
      return `Started when ${p.targetBranch} moved`
    case 'merged':
      return `Started by a merge into ${p.branch}`
    case 'upstream':
      return `Started when ${p.branch} was fast-forwarded from the upstream`
    case 'ticked':
      return `Started when you ticked ${ticked} to run pipelines`
    case 'setting':
      return "Started when the project's pipeline setting changed"
    case 'catch_up':
      return 'Started when devpad caught up on commits nothing had tested'
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

// Whether a job ended because its own code failed: it exited with another
// status than 0, ran out of time or out of memory, as the server's
// JobStatus.Failed.
export function jobFailed(j: PipelineJob): boolean {
  return j.status === 'failed' || j.status === 'timed_out' || j.status === 'out_of_memory'
}

// The names of a pipeline's jobs whose code failed.
export function failedJobNames(p: Pipeline): string[] {
  return p.jobs.filter(jobFailed).map((j) => j.name)
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
export function sentence(text: string): string {
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
