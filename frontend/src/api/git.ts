import { ApiError, apiClient } from './client'

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
  parents?: string[]
  author: string
  email: string
  timestamp: string
  message: string
}

export interface GitCommitFile {
  path: string
  oldPath?: string
  status: string
}

export interface GitFileDiff {
  path: string
  oldPath?: string
  status: string
  oldContent: string
  newContent: string
  oldFileName: string
  newFileName: string
}

export interface GitBranch {
  name: string
  hash: string
  upstream: string
  current: boolean
  remote: boolean
  remoteName?: string
  remoteBranch?: string
}

export interface GitBranches {
  branches: GitBranch[]
  current: string
}

export interface GitRemote {
  name: string
  fetchUrl: string
  pushUrl: string
}

export interface GitSshHostKey {
  host: string
  keyType: string
  fingerprint: string
  rawOutput?: string
}

export interface GitActionResult {
  success: boolean
  output: string
  error?: string
  sshHostKey?: GitSshHostKey
}

interface GitLogResponse {
  commits: GitCommit[]
}

interface GitDiffResponse {
  diff: string
}

interface GitCommitFilesResponse {
  files: GitCommitFile[]
}

interface GitRemotesResponse {
  remotes: GitRemote[]
}

export const gitApi = {
  status(workspaceId: number): Promise<GitStatus> {
    return apiClient.get<GitStatus>(`/api/workspaces/${workspaceId}/git/status`)
  },

  log(workspaceId: number, count = 50, opts: { allBranches?: boolean } = {}): Promise<GitLogResponse> {
    const params = new URLSearchParams({ count: String(count) })
    if (opts.allBranches) params.set('all', 'true')
    return apiClient.get<GitLogResponse>(
      `/api/workspaces/${workspaceId}/git/commits?${params}`
    )
  },

  branches(workspaceId: number): Promise<GitBranches> {
    return apiClient.get<GitBranches>(`/api/workspaces/${workspaceId}/git/branches`)
  },

  commitFiles(workspaceId: number, commit: string): Promise<GitCommitFile[]> {
    const params = new URLSearchParams({ commit })
    return apiClient
      .get<GitCommitFilesResponse>(`/api/workspaces/${workspaceId}/git/commit-files?${params}`)
      .then((r) => r.files)
  },

  commitFileDiff(
    workspaceId: number,
    commit: string,
    path: string,
    oldPath?: string
  ): Promise<GitFileDiff> {
    const params = new URLSearchParams({ commit, path })
    if (oldPath) params.set('oldPath', oldPath)
    return apiClient.get<GitFileDiff>(
      `/api/workspaces/${workspaceId}/git/commit-diff?${params}`
    )
  },

  fileDiff(workspaceId: number, path: string, staged = false): Promise<GitFileDiff> {
    const params = new URLSearchParams({ path })
    if (staged) params.set('staged', 'true')
    return apiClient.get<GitFileDiff>(
      `/api/workspaces/${workspaceId}/git/file-diff?${params}`
    )
  },

  remotes(workspaceId: number): Promise<GitRemote[]> {
    return apiClient
      .get<GitRemotesResponse>(`/api/workspaces/${workspaceId}/git/remotes`)
      .then((r) => r.remotes)
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
    opts: {
      files?: string[]
      message?: string
      branch?: string
      remote?: string
      url?: string
      newName?: string
      userName?: string
      userEmail?: string
      host?: string
      keyType?: string
      fingerprint?: string
    } = {}
  ): Promise<GitActionResult> {
    return apiClient.post<GitActionResult>(
      `/api/workspaces/${workspaceId}/git/action`,
      {
        action,
        files: opts.files ?? [],
        message: opts.message ?? '',
        branch: opts.branch ?? '',
        remote: opts.remote ?? '',
        url: opts.url ?? '',
        newName: opts.newName ?? '',
        userName: opts.userName ?? '',
        userEmail: opts.userEmail ?? '',
        host: opts.host ?? '',
        keyType: opts.keyType ?? '',
        fingerprint: opts.fingerprint ?? '',
      }
    )
  },

  acceptSshHostKey(workspaceId: number, hostKey: GitSshHostKey): Promise<GitActionResult> {
    return gitApi.action(workspaceId, 'accept-ssh-host-key', {
      host: hostKey.host,
      keyType: hostKey.keyType,
      fingerprint: hostKey.fingerprint,
    })
  },
}

export function gitSshHostKeyFromError(err: unknown): GitSshHostKey | null {
  if (!(err instanceof ApiError) || !isRecord(err.body)) return null
  const hostKey = err.body.sshHostKey
  return isGitSshHostKey(hostKey) ? hostKey : null
}

function isGitSshHostKey(value: unknown): value is GitSshHostKey {
  return isRecord(value) &&
    typeof value.host === 'string' &&
    typeof value.keyType === 'string' &&
    typeof value.fingerprint === 'string' &&
    (value.rawOutput === undefined || typeof value.rawOutput === 'string')
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null
}
