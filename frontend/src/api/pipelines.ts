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
// and devpad catching up as it starts. Later versions may add more.
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
  configSource: PipelineSource
  configPath: string
  configHash: string
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
  // Only pipelines older than this one, to page back.
  before?: number
  limit?: number
}

// What a pipeline tests: a protected branch's current commit, or an open
// merge request's head merged onto its target.
export type PipelineSubject = { branch: string } | { mergeRequestId: number }

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
  // You can merge anyway, which the merge request records.
  overridable: boolean
  // Why it is shut, as a lowercase clause; empty when it is clear.
  reason: string
  // The pipeline the state is about, if any.
  pipelineId: number | null
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
