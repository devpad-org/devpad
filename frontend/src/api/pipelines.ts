import { apiClient } from './client'
import type { Project } from './projects'

// Where a pipeline is, or how it ended. not_configured means the commit has
// no pipeline file and the project no setting, so nothing was tested: it is
// never a pass.
export type PipelineStatus =
  | 'queued'
  | 'running'
  | 'passed'
  | 'failed'
  | 'error'
  | 'invalid'
  | 'conflict'
  | 'not_configured'
  | 'cancelled'
  | 'superseded'
  | 'interrupted'

export type JobStatus =
  | 'queued'
  | 'running'
  | 'passed'
  | 'failed'
  | 'timed_out'
  | 'out_of_memory'
  | 'error'
  | 'cancelled'
  | 'skipped'
  | 'interrupted'

// user: started by hand; restart: devpad running again one its restart
// interrupted. The rest are devpad's own runs of ticked branches and merge
// requests into them: a merge request opening, its branch getting new
// commits, its target moving; a merge into the branch, a fast-forward from
// the upstream; the user ticking the branch, changing the pipeline setting;
// and devpad catching up as it starts. The server doesn't check triggers,
// so one the app doesn't know shows by its name (triggerLabel).
export type PipelineTrigger =
  | 'user'
  | 'restart'
  | 'opened'
  | 'source_moved'
  | 'target_moved'
  | 'merged'
  | 'upstream'
  | 'ticked'
  | 'setting'
  | 'catch_up'
  | (string & {})

// Where the jobs came from: a file at the commit read (a branch's own, or a
// merge request's target's), the project's setting, or neither.
export type PipelineSource = 'file' | 'project' | 'none'

// What a pipeline is for: official is a branch's or merge request's own,
// the result that counts; proposed runs the jobs a merge request's change
// proposes, from the pipeline file of its trial merge, for information
// only. As the server's Purposes.
export type PipelinePurpose = 'official' | 'proposed'

export interface PipelineJob {
  id: number
  name: string
  position: number
  timeoutMs: number
  services: string[]
  status: JobStatus
  exitCode: number | null
  error: string
  // How much the job printed, how much of the end of it is kept, and
  // whether that is still there.
  outputBytes: number
  keptBytes: number
  outputKept: boolean
  startedAt: string | null
  finishedAt: string | null
  // The job's script; only a single pipeline's read carries it.
  run?: string
}

export interface Pipeline {
  id: number
  projectId: number
  kind: 'branch' | 'merge_request'
  purpose: PipelinePurpose
  // The protected branch a branch pipeline tests.
  branch: string
  // The merge request a merge request pipeline tests, and its branches.
  mergeRequestId: number
  sourceBranch: string
  targetBranch: string
  // The commit tested: the branch's, or the merge request's source head.
  headSha: string
  // The target commit a merge request's head is merged onto, whose
  // pipeline it runs; empty for a branch.
  targetSha: string
  // What the jobs got: the commit, or the trial merge. Empty until pinned.
  testedSha: string
  // A proposed pipeline's jobs come from the trial merge it tests.
  configSource: PipelineSource
  configPath: string
  configHash: string
  // The commit the jobs were read from: the branch's, the target's, or a
  // proposed pipeline's trial merge, which is what it tests. Empty for
  // pipelines from before devpad kept it.
  configSha: string
  // The image the jobs ran on, set when the pipeline starts.
  image: string
  status: PipelineStatus
  // Why the pipeline itself ended as it did, when that was not its jobs.
  error: string
  conflictPaths: string[]
  trigger: PipelineTrigger
  // The interrupted pipeline this one runs again, or 0.
  rerunOf: number
  createdAt: string
  startedAt: string | null
  finishedAt: string | null
  jobs: PipelineJob[]
}

// One of devpad's own notes on a job: a setup step, or how it ended. Notes
// are never part of what the job printed.
export interface JobNote {
  at: string
  text: string
}

// A chunk of a job's output from offset. next is the offset to ask for
// next; dropped says output after the offset asked for is no longer kept.
export interface JobOutputChunk {
  offset: number
  data: string
  next: number
  done: boolean
  dropped: boolean
  notes: JobNote[]
}

export interface PipelineListFilter {
  branch?: string
  mergeRequestId?: number
  // Only pipelines of this purpose; all when left out.
  purpose?: PipelinePurpose
  // Only pipelines older than this one, to page back.
  before?: number
  limit?: number
}

// What a pipeline tests: a protected branch's current commit, or an open
// merge request's head merged onto its target, with its own jobs or, as
// proposed, those its change proposes.
export type PipelineSubject = { branch: string } | { mergeRequestId: number; purpose?: PipelinePurpose }

// How many branches and merge requests one latest() names at most, as the
// server's MaxLatestSubjects.
export const MAX_LATEST_SUBJECTS = 100

// A pipeline of the commits and jobs a branch or merge request has now that
// ended without a result, cancelled or unable to run, when none of them
// counts: reason says why, and whether devpad runs them again (retrying).
export interface SettledPipeline {
  pipeline: Pipeline
  retrying: boolean
  reason: string
}

// How a branch stands for pipelines now: whether devpad runs it by itself,
// whether a pipeline is configured at its current commit (null when that
// couldn't be read), and its settled pipeline. Automatic and configured, a
// pipeline guards the merge requests into it.
export interface BranchPipelineStanding {
  automatic: boolean
  configured: boolean | null
  settled: SettledPipeline | null
}

// How the merge gate stands: unguarded when no pipeline guards the merge
// request (its target isn't ticked under Run pipelines, or has no
// pipeline), passed when its pipeline passed on it as it is now, closed
// when it can no longer merge, and otherwise why its pipeline keeps it from
// merging.
export type MergeGateState =
  | 'unguarded'
  | 'passed'
  | 'queued'
  | 'running'
  | 'not_run'
  | 'out_of_date'
  | 'failed'
  | 'conflict'
  | 'invalid'
  | 'error'
  | 'cancelled'
  | 'closed'

// The merge gate, as the server decides it: the one rule for whether a
// merge request's pipeline lets it merge.
export interface MergeGate {
  state: MergeGateState
  // A pipeline guards the merge request.
  guarded: boolean
  // The gate lets it merge.
  clear: boolean
  // A result that could clear it is still to come by itself.
  waits: boolean
  // You can merge anyway, which the merge request records: not when it
  // can no longer merge, nor when its branch doesn't merge onto the target.
  overridable: boolean
  // Why it is shut, as a lowercase clause; empty when it is clear.
  reason: string
  // The pipeline the state is about, if any, and the head and target tip
  // it is of: what a merge over the gate confirms, all of which must still
  // be so when it merges.
  pipelineId: number | null
  headSha: string
  targetSha: string
}

// How a merge request stands for pipelines now, as the server works it out:
// the head and target tip a pipeline must test to count, the jobs that
// would run there, and the pipeline that tested exactly that.
export interface MergeRequestPipelineStatus {
  // The merge request can still merge; when it can't, only latest is set.
  open: boolean
  headSha: string
  targetSha: string
  configHash: string
  // Where the jobs that would run now come from; 'none' is no pipeline
  // configured, so nothing guards the merge request.
  source: PipelineSource
  configPath: string
  // Why the pipeline that would run now doesn't parse, or empty.
  invalid: string
  configured: boolean
  // current passed: the result is of the commits and jobs as they are now.
  passed: boolean
  // The newest pipeline of exactly these commits and jobs that counts: one
  // under way, or one with a result. Null when none has run.
  current: Pipeline | null
  // The newest pipeline of the merge request that counts, whatever it
  // tested; an older result when it isn't current.
  latest: Pipeline | null
  // Devpad runs the merge request's pipelines by itself: it is open, into a
  // branch ticked to run them.
  automatic: boolean
  // When nothing current counts, the newest of these commits and jobs that
  // was cancelled or couldn't run, and why.
  settled: SettledPipeline | null
  // Whether the pipeline lets it merge.
  gate: MergeGate
  // When current failed, how the target's own pipeline went on the commit
  // it merged onto, with the same jobs: which of current's failing jobs fail
  // there too, or that it is still to say. Null otherwise.
  onTarget: TargetResult | null
  // The pipeline the change proposes, run for information only; null when
  // the change leaves the pipeline file alone. Nothing above ever counts
  // one.
  proposed: ProposedPipelineStatus | null
}

// How the pipeline a merge request's change proposes stands now. It never
// gates merging, goes back to a task's agent or holds a review.
export interface ProposedPipelineStatus {
  // The pipeline file the change adds, changes, renames or deletes.
  file: string
  headSha: string
  targetSha: string
  configHash: string
  // The file the proposed jobs come from at the trial merge.
  configPath: string
  // Devpad runs it by itself, with the merge request's own pipeline.
  automatic: boolean
  // The change doesn't merge onto the target, so the file it proposes
  // can't be read; it removes the pipeline file; or it proposes the very
  // jobs the merge request's own pipeline runs. Nothing runs for any.
  conflict: boolean
  removed: boolean
  sameJobs: boolean
  // Why the proposed file doesn't parse, or empty.
  invalid: string
  current: Pipeline | null
  latest: Pipeline | null
  settled: SettledPipeline | null
}

export interface TargetResult {
  branch: string
  // The target's pipeline with a result, null when none has one.
  pipelineId: number | null
  failing: string[]
  pending: boolean
}

function base(projectId: number): string {
  return `/api/projects/${projectId}/pipelines`
}

export const pipelineApi = {
  // Newest first, with jobs but not their scripts.
  list(projectId: number, filter: PipelineListFilter = {}): Promise<{ pipelines: Pipeline[] }> {
    const query = new URLSearchParams()
    if (filter.branch) query.set('branch', filter.branch)
    if (filter.mergeRequestId) query.set('mergeRequestId', String(filter.mergeRequestId))
    if (filter.purpose) query.set('purpose', filter.purpose)
    if (filter.before) query.set('before', String(filter.before))
    if (filter.limit) query.set('limit', String(filter.limit))
    const qs = query.toString()
    return apiClient.get(`${base(projectId)}${qs ? `?${qs}` : ''}`)
  },

  // The newest pipeline that counts (under way, or with a result) of each
  // branch and merge request named that has one, the commit each branch
  // named is at now (null when the repo could not be read), and how each
  // branch stands.
  latest(
    projectId: number,
    subjects: { branches: string[]; mergeRequestIds: number[] },
  ): Promise<{
    pipelines: Pipeline[]
    branchTips: Record<string, string> | null
    branches: Record<string, BranchPipelineStanding>
  }> {
    const query = new URLSearchParams()
    for (const b of subjects.branches) query.append('branch', b)
    for (const id of subjects.mergeRequestIds) query.append('mergeRequestId', String(id))
    return apiClient.get(`${base(projectId)}/latest?${query.toString()}`)
  },

  mergeRequestStatus(projectId: number, mergeRequestId: number): Promise<{ status: MergeRequestPipelineStatus }> {
    return apiClient.get(`/api/projects/${projectId}/merge-requests/${mergeRequestId}/pipeline`)
  },

  // Starts a pipeline, or returns one already queued or running that tests
  // the same commits with the same jobs.
  start(projectId: number, subject: PipelineSubject): Promise<{ pipeline: Pipeline }> {
    return apiClient.post(base(projectId), subject)
  },

  // With the jobs' scripts.
  get(projectId: number, pipelineId: number): Promise<{ pipeline: Pipeline }> {
    return apiClient.get(`${base(projectId)}/${pipelineId}`)
  },

  cancel(projectId: number, pipelineId: number): Promise<{ pipeline: Pipeline }> {
    return apiClient.post(`${base(projectId)}/${pipelineId}/cancel`, {})
  },

  // Ask again from next, never from offset plus the length of data, which
  // counts characters rather than the job's bytes.
  jobOutput(projectId: number, pipelineId: number, jobId: number, offset: number): Promise<JobOutputChunk> {
    return apiClient.get(`${base(projectId)}/${pipelineId}/jobs/${jobId}/output?offset=${offset}`)
  },

  // Sets the pipeline for commits without a pipeline file; empty clears
  // it. YAML that does not parse is refused with the line at fault.
  setConfig(projectId: number, config: string): Promise<{ project: Project }> {
    return apiClient.put(`/api/projects/${projectId}/pipeline`, { config })
  },
}
