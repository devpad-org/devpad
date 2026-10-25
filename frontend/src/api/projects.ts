import { ApiError, apiClient } from './client'

export type ProjectStatus = 'creating' | 'ready' | 'error'

export interface Project {
  id: number
  name: string
  description: string
  defaultBranch: string
  upstreamUrl: string
  setupScript: string
  maxSandboxes: number
  status: ProjectStatus
  error: string
  createdAt: string
  updatedAt: string
}

export interface ProjectInput {
  name: string
  description: string
  defaultBranch?: string
  upstreamUrl?: string
  setupScript: string
  maxSandboxes: number
}

// Sandbox is a project workspace an agent works in on one branch.
export interface Sandbox {
  id: number
  name: string
  status: 'creating' | 'running' | 'stopped'
  kind: 'task' | 'review'
  branch: string
  projectId: number
  createdAt: string
  updatedAt: string
}

// ProjectWorkspace is the user's own workspace for a project. It owns no
// branch and can switch between them.
export interface ProjectWorkspace {
  id: number
  name: string
  status: 'creating' | 'running' | 'stopped'
  kind: 'interactive'
  branch: ''
  projectId: number
  createdAt: string
  updatedAt: string
}

export interface RepoBranch {
  name: string
  sha: string
}

export interface PushResult {
  branch: string
  sha: string
}

// A branch moved in the canonical repo or a workspace.
export interface BranchResult {
  branch: string
  sha: string
}

// How a branch of the project workspace compares with the project's.
export interface WorkspaceBranchStatus {
  branch: string
  // Whether the project has the branch at all.
  published: boolean
  sha: string
  canonicalSha?: string
  // Local commits the project lacks, and the project's commits the local
  // branch lacks.
  ahead: number
  behind: number
  // An agent's sandbox owns the branch, so the workspace can't publish to it.
  sandboxOwned: boolean
}

export interface CheckoutResult {
  branch: string
  // The local branch's commit; it differs from upstreamSha when an existing
  // local branch has diverged from the canonical one.
  sha: string
  upstream: string
  upstreamSha: string
  created: boolean
}

export interface RepoDiffFile {
  path: string
  oldPath?: string
  status: string
  additions: number
  deletions: number
  binary: boolean
}

export interface RepoDiff {
  baseSha: string
  headSha: string
  mergeBase: string
  files: RepoDiffFile[]
}

// What is happening in a project, for the launcher.
export interface ProjectOverview {
  projectId: number
  runningAgents: number
  activeTasks: number
  openMergeRequests: number
  // Open merge requests waiting on the user: no agent is reviewing or
  // reworking them, and the user hasn't reviewed their current head.
  awaitingReview: number
  lastActivityAt: string
}

// Ties a project-linked workspace to its project and, for sandboxes, to the
// task or merge request it is working on (0 when none).
export interface WorkspaceLink {
  workspaceId: number
  projectId: number
  kind: 'interactive' | 'task' | 'review'
  taskId: number
  mergeRequestId: number
}

// A git repo found in a standalone workspace that could become a project.
export interface DiscoveredRepo {
  // Relative to /workspace; "." is /workspace itself.
  path: string
  branch: string
  branchCount: number
  // At most 200 of each are named.
  branches: string[]
  tagCount: number
  tags: string[]
  dirty: boolean
  // Any credentials embedded in origin's URL are removed.
  originUrl: string
  originHadCredentials: boolean
  originDefaultBranch: string
  // Only local branches are imported: these exist only on origin, or are
  // behind origin's copy.
  remoteOnlyBranches: string[]
  behindBranches: { name: string; behind: number }[]
  // devpad needs the "devpad" remote name for its copies of project branches.
  hasDevpadRemote: boolean
}

// A credential found in a workspace: what it is and where, never its value.
export interface CredentialItem {
  id: string
  kind: string
  location: string
}

// What a repo made from a workspace's files would hold.
export interface ContentsPreview {
  files: number
  bytes: number
  // Files that usually hold secrets, left out unless asked for.
  sensitiveFiles: string[]
  sensitiveCount: number
  sensitiveBytes: number
  junkPatterns: string[]
  sensitivePatterns: string[]
  maxBytes: number
}

// A credential devpad agreed to strip but could not, with why; the reason
// holds no secret.
export interface CredentialFailure extends CredentialItem {
  reason: string
}

export interface RepoDiscovery {
  repos: DiscoveredRepo[]
  // Only set when the workspace has no repo at all.
  contents?: ContentsPreview
  // What a workspace converted in place must not keep.
  credentials: CredentialItem[]
  // Git config includes that were searched too.
  credentialIncludes: string[]
  // The search for repos stopped at its limit, so some may be unchecked.
  credentialScanIncomplete: boolean
}

export interface ConvertInput extends ProjectInput {
  // A repo from discovery; empty makes a repo from the workspace's files.
  repoPath: string
  includeSensitive: boolean
  // IDs of the credentials the user agreed to strip; converting in place
  // must name every one the workspace holds.
  stripCredentials: string[]
}

// Which of devpad's SSH files were removed from a converted workspace. Kept
// files (known_hosts, config) differ from what devpad wrote, so they were
// left alone; devpad's key pair is always removed.
export interface SSHCleanup {
  removed: string[]
  kept: string[]
  absent: string[]
}

export interface ConvertResult {
  project: Project
  // The workspace became the project's workspace.
  inPlace: boolean
  // The converted workspace, now the project's; null when its repo was in a
  // subdirectory and the workspace was left as it was.
  workspace: ProjectWorkspace | null
  ssh: SSHCleanup | null
  // Set for a workspace converted in place.
  stripped: CredentialItem[]
  restartError: string
  // The devpad SSH key the workspace held was replaced by publicKey.
  keyRotated: boolean
  publicKey: string
  rotationError: string
  // Set when devpad could not record a step, which may then have run twice.
  progressError: string
}

// What converting a workspace in place did, kept on the project until the
// user dismisses it, so a lost response loses nothing. Steps still pending
// are finished by devpad.
export interface ConversionNotice {
  finished: boolean
  stripped: CredentialItem[]
  // SSH files left in place because they differ from what devpad wrote.
  sshKept: string[]
  restartPending: boolean
  restartError: string
  rotationPending: boolean
  keyRotated: boolean
  publicKey: string
  rotationError: string
  progressError: string
  createdAt: string
}

// A push made with `git push devpad` in the project workspace, by the user
// or the IDE assistant, published or refused.
export interface GitPushEvent {
  seq: number
  branch: string
  ok: boolean
  sha?: string
  error?: string
  at: string
}

export interface GitActivity {
  events: GitPushEvent[]
  latest: number
  // Changes when devpad restarts, which restarts the sequence numbers.
  epoch: string
}

export const projectApi = {
  list(): Promise<{ projects: Project[] }> {
    return apiClient.get('/api/projects')
  },

  overview(): Promise<{ projects: ProjectOverview[]; workspaces: WorkspaceLink[] }> {
    return apiClient.get('/api/projects/overview')
  },

  get(id: number): Promise<{ project: Project }> {
    return apiClient.get(`/api/projects/${id}`)
  },

  create(input: ProjectInput): Promise<{ project: Project }> {
    return apiClient.post('/api/projects', input)
  },

  update(id: number, input: ProjectInput): Promise<{ project: Project }> {
    return apiClient.put(`/api/projects/${id}`, input)
  },

  delete(id: number): Promise<void> {
    return apiClient.delete(`/api/projects/${id}`)
  },

  branches(id: number): Promise<{ defaultBranch: string; branches: RepoBranch[] }> {
    return apiClient.get(`/api/projects/${id}/branches`)
  },

  sandboxes(id: number): Promise<{ sandboxes: Sandbox[] }> {
    return apiClient.get(`/api/projects/${id}/sandboxes`)
  },

  startSandbox(id: number, workspaceId: number): Promise<{ sandbox: Sandbox }> {
    return apiClient.post(`/api/projects/${id}/sandboxes/${workspaceId}/start`, {})
  },

  push(id: number): Promise<PushResult> {
    return apiClient.post(`/api/projects/${id}/push`, {})
  },

  // Returns the project workspace, or null before it is first opened.
  async getWorkspace(id: number): Promise<ProjectWorkspace | null> {
    try {
      const res = await apiClient.get<{ workspace: ProjectWorkspace }>(`/api/projects/${id}/workspace`)
      return res.workspace
    } catch (e) {
      if (e instanceof ApiError && e.status === 404) return null
      throw e
    }
  },

  // Returns the project workspace, creating it on first use.
  openWorkspace(id: number): Promise<{ workspace: ProjectWorkspace }> {
    return apiClient.post(`/api/projects/${id}/workspace`, {})
  },

  // Checks out a canonical branch in the project workspace.
  checkoutBranch(id: number, branch: string): Promise<CheckoutResult> {
    return apiClient.post(`/api/projects/${id}/workspace/checkout`, { branch })
  },

  // Publishes a branch of the project workspace; existing branches only
  // fast-forward.
  publishBranch(id: number, branch: string): Promise<BranchResult> {
    return apiClient.post(`/api/projects/${id}/workspace/publish`, { branch })
  },

  // Refreshes devpad/<default branch> in the project workspace.
  syncWorkspace(id: number): Promise<BranchResult> {
    return apiClient.post(`/api/projects/${id}/workspace/sync`, {})
  },

  // Compares a branch of the project workspace with the project's, after
  // refreshing the workspace's copy of the project's branch.
  workspaceBranchStatus(id: number, branch: string): Promise<WorkspaceBranchStatus> {
    return apiClient.get(`/api/projects/${id}/workspace/branch-status?branch=${encodeURIComponent(branch)}`)
  },

  // Pushes made through the devpad remote after `after` in `epoch`. Without
  // after, only the latest sequence number comes back, so old pushes are not
  // replayed; with an epoch from before a restart, everything since comes.
  gitActivity(id: number, after?: number, epoch?: string): Promise<GitActivity> {
    const query = new URLSearchParams()
    if (after !== undefined) query.set('after', String(after))
    if (epoch !== undefined) query.set('epoch', epoch)
    const qs = query.toString()
    return apiClient.get(`/api/projects/${id}/git-activity${qs ? `?${qs}` : ''}`)
  },

  // Lists the repos in a standalone workspace that could become a project.
  discoverConversion(workspaceId: number): Promise<RepoDiscovery> {
    return apiClient.get(`/api/workspaces/${workspaceId}/conversion`)
  },

  // Makes a project from a standalone workspace.
  convertWorkspace(workspaceId: number, input: ConvertInput): Promise<ConvertResult> {
    return apiClient.post(`/api/workspaces/${workspaceId}/convert`, input)
  },

  // What converting a workspace into the project did, until dismissed.
  conversionNotice(id: number): Promise<{ conversion: ConversionNotice | null }> {
    return apiClient.get(`/api/projects/${id}/conversion`)
  },

  dismissConversionNotice(id: number): Promise<void> {
    return apiClient.post(`/api/projects/${id}/conversion/dismiss`, {})
  },

  // Returns null for a file that does not exist at rev, such as one a
  // change adds or deletes.
  async fileAt(id: number, rev: string, path: string): Promise<string | null> {
    const params = new URLSearchParams({ rev, path })
    const res = await fetch(`/api/projects/${id}/file?${params.toString()}`)
    if (res.status === 404) return null
    if (!res.ok) {
      let message = `Failed to load ${path}: ${res.status}`
      try {
        const body = await res.json()
        if (typeof body?.error === 'string') message = body.error
      } catch {
        // keep the status message
      }
      throw new Error(message)
    }
    return res.text()
  },
}
