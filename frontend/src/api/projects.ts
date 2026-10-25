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
