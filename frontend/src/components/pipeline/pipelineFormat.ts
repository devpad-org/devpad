import type { JobStatus, MergeGateState, Pipeline, PipelineJob, PipelineStatus } from '@/api/pipelines'
import type { PipelineRounds } from '@/api/tasks'
import type { MrIconName } from '@/components/project/MrIcon.vue'
import { shortSha } from '@/components/mergerequest/format'

// Presentation helpers shared by the pipeline components.

// What the app says of when devpad starts pipelines by itself, of the
// merge gate and of the pipeline a change proposes, in one place. The docs
// say the same in docs/09-pipelines.md (the introduction, #automatic-runs,
// #branches, #merge-requests, #merge-gate, #failures and #proposed),
// docs/04-branches.md (the Run pipelines list, #reviews,
// #auto-fixing-reviews, #merging and #automatic-merges), docs/05-tasks.md
// (#how-a-task-runs) and docs/03-projects.md (the Pipelines surface and the
// pipeline setting).
export const PIPELINE_COPY = {
  // Beside where pipelines are run.
  automaticRuns: 'Devpad runs ticked branches and the merge requests into them by itself. Run any protected branch here, or a merge request from its page.',
  // Under the Run pipelines ticks.
  tickedBranches:
    "Devpad runs a ticked branch's pipeline on each new commit and when you tick it, and a merge request's into it when it opens, when its branch gets new commits and when the ticked branch moves. A merge request into a ticked branch merges only once its pipeline passes, unless you merge it anyway. Merge requests into other protected branches run only when you start them, and their results don't keep them from merging. The Project page shows each ticked branch's latest pipeline.",
  // On a merge request into a ticked branch without a pipeline.
  mergeRequestNoneAutomatic: (target: string) =>
    `No pipeline has run on this merge request yet. Devpad runs one by itself when it opens, when its branch gets new commits and when ${target} moves.`,
  // On a merge request into a branch not ticked to run pipelines.
  mergeRequestNone: (target: string) =>
    `No pipeline has run on this merge request yet. ${target} isn't ticked under Run pipelines, so devpad doesn't run its merge requests by itself: run one here.`,
  // On the Pipeline setting card, with the branches ticked, or none.
  settingTicked: (branches: string) =>
    `Devpad runs ${branches} and the merge requests into them by itself, and runs them again when this setting changes the jobs they get. Other protected branches, and merge requests into them, run by hand.`,
  settingUnticked:
    'Pipelines run by hand on any protected branch and on merge requests into one. Tick branches under Run pipelines in Manage branches for devpad to run them, and the merge requests into them, by itself.',
  // What a merge request's pipeline decides: whether the merge gate guards
  // it, and when it doesn't, why not.
  gateGuarded: (target: string) =>
    `${target} is ticked under Run pipelines, so this merges only once its pipeline passes on this branch merged onto ${target} as it is then, unless you merge it anyway.`,
  gateNotTicked: (target: string) =>
    `No pipeline guards ${target}: it isn't ticked under Run pipelines, so a result here doesn't keep this from merging.`,
  gateNotConfigured: (target: string) => `No pipeline guards ${target}: it has no .devpad/pipeline.yml and the project no pipeline setting.`,
  gateShut: 'Merging waits for it to pass, unless you merge anyway.',
  gateConflict: "It can't merge until the conflicts are resolved; its pipeline runs again then.",
  // Asking to merge over a shut gate, and what the merge request keeps.
  overrideTitle: 'Merge without a passing pipeline?',
  overrideChanged: 'The pipeline or the commits changed. Merge anyway?',
  overrideRecorded: 'The merge request records that you merged it anyway, and how its pipeline stood.',
  // A task's merge request into a branch that merges automatically.
  autoMergeGuarded: (reviewer: string, target: string) => `Once ${reviewer} approves it and its pipeline passes on ${target} as it is then.`,
  autoMergeUnguarded: (reviewer: string, target: string) =>
    `Once ${reviewer} approves it. No pipeline guards ${target}, so nothing but the review agent's judgment decides what merges.`,
  autoMergeWaiting: (target: string) => `The review agent approved it; devpad merges it into ${target} once its pipeline passes.`,
  autoMergePipelineFile: (file: string) => `It changes ${file}, which devpad never merges by itself: merge it yourself once you've looked at the change to its jobs.`,
  // In the Branches dialog, under Merge automatically.
  autoMergeUnguardedWarning: (branches: string) =>
    `No pipeline guards ${branches}, so nothing but the review agent's judgment decides what merges. A change can carry text that tries to talk the review agent into approving it. Tick a branch under Run pipelines, with a pipeline configured, for its merge requests to pass the pipeline first.`,
  autoMergeGuardedHint: (branches: string) =>
    `A pipeline guards ${branches}: a task's merge request merges only once its pipeline passes too. A pipeline can't tell a weakened test from a sound one, so the review agent's judgment still counts.`,
  // Beside a failing job that fails on the target too.
  alsoFailing: (target: string) => `also failing on ${target}`,
  alsoFailingHint: (target: string) => `This job fails in ${target}'s own pipeline of the same commit too, so it isn't sent back to the task's agent.`,
  targetPending: (target: string) => `Waiting for ${target}'s own pipeline of the same commit, to tell the failures this change brings from those already there.`,
  // The project's automatic review of a merge request a pipeline guards.
  reviewAwaitsPipeline: 'The automatic review starts once the pipeline passes.',
  // The pipeline a merge request's change proposes, on its own card.
  proposedNote: "from this change — doesn't gate merging",
  proposedExplain: (file: string, target: string) =>
    `This change touches ${file}, so devpad also runs the jobs it proposes on this branch merged onto ${target}, for you to see they work before they take effect. The result never keeps this from merging, never goes back to the task's agent and never holds a review: until it merges, ${target}'s own jobs are the ones that count. Its jobs run one at a time, after any job of the pipelines that count, each for at most as long as ${target}'s longest.`,
  proposedRemoved: (target: string) =>
    `This change removes the pipeline, so it proposes no jobs and nothing runs here. Once it merges, ${target}'s commits run the project's pipeline setting, or no pipeline when it has none.`,
  proposedConflict: (target: string) => `This branch doesn't merge onto ${target}, so the pipeline it proposes can't be read.`,
  proposedSameJobs: (target: string) =>
    `This change proposes the very jobs ${target}'s pipeline already runs, such as by changing only a comment, so they run once, on the Pipeline card above.`,
  proposedNoneAutomatic: "No proposed pipeline has run yet. Devpad runs it by itself whenever it runs this merge request's own.",
  proposedNone: (target: string) => `No proposed pipeline has run yet. ${target} isn't ticked under Run pipelines, so run it here.`,
} as const

// What a task's automatic pipeline rounds say, on the task and on its merge
// request, or '' when there is nothing to say: the task was sent back
// because its pipeline failed, or the failure waits for you.
export function pipelineRoundsText(rounds: PipelineRounds | undefined): string {
  if (!rounds) return ''
  switch (rounds.held) {
    case 'limit':
      return `Waiting for you: the pipeline still failed after ${rounds.limit} automatic rounds. Send the failure back, or look at the change.`
    case 'stuck':
      return "Waiting for you: stuck, the agent's last round left the same failures as before. Send the failure back with a hint, or look at the change."
    case 'not_started':
      return "Waiting for you: devpad couldn't start the agent to send the pipeline's failure back. Send it back once the project is ready."
    case 'budget':
      return ''
  }
  return rounds.count > 0 ? `Sent back after the pipeline failed (${rounds.count} of ${rounds.limit}).` : ''
}

// Where a commit keeps its pipeline, as the server's project.PipelineFiles.
export const PIPELINE_FILES = ['.devpad/pipeline.yml', '.devpad/pipeline.yaml']

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
