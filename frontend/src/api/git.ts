import { apiClient } from './client'

export interface GitFileStatus {
  path: string
  statusCode: string
  status: string
  staged: boolean
}

export interface GitStatus {
  isRepo: boolean
  branch: string
  files: GitFileStatus[]
  ahead: number
  behind: number
  remotes: string[]
  userName: string
  userEmail: string
}

export interface GitCommit {
  hash: string
  shortHash: string
  author: string
  email: string
  timestamp: string
  message: string
}

export interface GitBranch {
  name: string
  hash: string
  upstream: string
  current: boolean
  remote: boolean
}

export interface GitBranches {
  branches: GitBranch[]
  current: string
}

export interface GitActionResult {
  success: boolean
  output: string
  error?: string
}

interface GitLogResponse {
  commits: GitCommit[]
}

interface GitDiffResponse {
  diff: string
}

export const gitApi = {
  status(workspaceId: number): Promise<GitStatus> {
    return apiClient.get<GitStatus>(`/api/workspaces/${workspaceId}/git/status`)
  },

  log(workspaceId: number, count = 50): Promise<GitLogResponse> {
    return apiClient.get<GitLogResponse>(
      `/api/workspaces/${workspaceId}/git/log?count=${count}`
    )
  },

  branches(workspaceId: number): Promise<GitBranches> {
    return apiClient.get<GitBranches>(`/api/workspaces/${workspaceId}/git/branches`)
  },

  diff(workspaceId: number, path?: string, staged = false): Promise<string> {
    const params = new URLSearchParams()
    if (path) params.set('path', path)
    if (staged) params.set('staged', 'true')
    return apiClient
      .get<GitDiffResponse>(`/api/workspaces/${workspaceId}/git/diff?${params}`)
      .then((r) => r.diff)
  },

  action(
    workspaceId: number,
    action: string,
    opts: { files?: string[]; message?: string; branch?: string; remote?: string; userName?: string; userEmail?: string } = {}
  ): Promise<GitActionResult> {
    return apiClient.post<GitActionResult>(
      `/api/workspaces/${workspaceId}/git/action`,
      {
        action,
        files: opts.files ?? [],
        message: opts.message ?? '',
        branch: opts.branch ?? '',
        remote: opts.remote ?? '',
        userName: opts.userName ?? '',
        userEmail: opts.userEmail ?? '',
      }
    )
  },
}
