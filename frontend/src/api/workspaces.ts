import { apiClient } from './client'

export interface Workspace {
  id: number
  name: string
  description: string
  status: 'creating' | 'running' | 'stopped'
  containerId: string
  defaultAgentId: string
  createdAt: string
  updatedAt: string
}

export interface FileEntry {
  name: string
  path: string
  type: 'file' | 'directory'
  size: number
  modTime: string
}

export interface ContainerStats {
  cpuPercent: number
  memoryUsage: number
  memoryLimit: number
  memoryPercent: number
  networkRx: number
  networkTx: number
  blockRead: number
  blockWrite: number
  pids: number
}

export interface WorkspaceInfo {
  agentVersion: string
  expectedAgentVersion: string
  stats: ContainerStats | null
}

export interface ProcessInfo {
  pid: number
  ppid: number
  user: string
  state: string
  command: string
  memoryBytes: number
  cpuTimeSeconds: number
  killable: boolean
}

export interface ProcessListResponse {
  processes: ProcessInfo[]
}

interface WorkspaceResponse {
  workspace: Workspace
}

interface WorkspaceListResponse {
  workspaces: Workspace[]
}

interface FileListResponse {
  entries: FileEntry[]
  truncated?: boolean
}

interface UploadFileResponse {
  path: string
}

interface ListFilesOptions {
  recursive?: boolean
  maxDepth?: number
  maxEntries?: number
}

export const workspaceApi = {
  list(): Promise<WorkspaceListResponse> {
    return apiClient.get<WorkspaceListResponse>('/api/workspaces')
  },

  get(id: number): Promise<WorkspaceResponse> {
    return apiClient.get<WorkspaceResponse>(`/api/workspaces/${id}`)
  },

  create(name: string, description: string): Promise<WorkspaceResponse> {
    return apiClient.post<WorkspaceResponse>('/api/workspaces', { name, description })
  },

  update(id: number, name: string, description: string): Promise<WorkspaceResponse> {
    return apiClient.put<WorkspaceResponse>(`/api/workspaces/${id}`, { name, description })
  },

  setDefaultAgent(id: number, agentId: string): Promise<WorkspaceResponse> {
    return apiClient.put<WorkspaceResponse>(`/api/workspaces/${id}/default-agent`, { agentId })
  },

  delete(id: number): Promise<void> {
    return apiClient.delete(`/api/workspaces/${id}`)
  },

  start(id: number): Promise<WorkspaceResponse> {
    return apiClient.post<WorkspaceResponse>(`/api/workspaces/${id}/start`, {})
  },

  stop(id: number): Promise<WorkspaceResponse> {
    return apiClient.post<WorkspaceResponse>(`/api/workspaces/${id}/stop`, {})
  },

  listFiles(id: number, path: string, options: ListFilesOptions = {}): Promise<FileListResponse> {
    const params = new URLSearchParams({ path })
    if (options.recursive) {
      params.set('recursive', 'true')
    }
    if (options.maxDepth !== undefined) {
      params.set('max_depth', String(options.maxDepth))
    }
    if (options.maxEntries !== undefined) {
      params.set('max_entries', String(options.maxEntries))
    }

    return apiClient.get<FileListResponse>(
      `/api/workspaces/${id}/files?${params.toString()}`
    )
  },

  async readFile(id: number, path: string, signal?: AbortSignal): Promise<string> {
    const res = await fetch(`/api/workspaces/${id}/file?path=${encodeURIComponent(path)}`, { signal })
    if (!res.ok) {
      throw new Error(`Failed to read file: ${res.status}`)
    }
    return res.text()
  },

  writeFile(id: number, path: string, content: string): Promise<void> {
    return apiClient.put(`/api/workspaces/${id}/file`, { path, content })
  },

  async uploadFile(id: number, directoryPath: string, file: File): Promise<UploadFileResponse> {
    const form = new FormData()
    form.set('path', directoryPath)
    form.set('file', file)

    return apiClient.postForm<UploadFileResponse>(`/api/workspaces/${id}/file/upload`, form)
  },

  async downloadFile(id: number, path: string): Promise<Blob> {
    const res = await fetch(`/api/workspaces/${id}/file?path=${encodeURIComponent(path)}`)
    if (!res.ok) {
      throw new Error(`Failed to download file: ${res.status}`)
    }
    return res.blob()
  },

  deleteFile(id: number, path: string): Promise<void> {
    return apiClient.delete(`/api/workspaces/${id}/file?path=${encodeURIComponent(path)}`)
  },

  mkdir(id: number, path: string): Promise<void> {
    return apiClient.post(`/api/workspaces/${id}/file/mkdir`, { path })
  },

  rename(id: number, oldPath: string, newPath: string): Promise<void> {
    return apiClient.post(`/api/workspaces/${id}/file/rename`, { oldPath, newPath })
  },

  async getPreviewURL(id: number, port: number): Promise<string> {
    const res = await apiClient.post<{ url: string }>(`/api/workspaces/${id}/preview`, { port })
    return res.url
  },

  getInfo(id: number): Promise<WorkspaceInfo> {
    return apiClient.get<WorkspaceInfo>(`/api/workspaces/${id}/info`)
  },

  listProcesses(id: number): Promise<ProcessListResponse> {
    return apiClient.get<ProcessListResponse>(`/api/workspaces/${id}/processes`)
  },

  killProcess(id: number, pid: number): Promise<void> {
    return apiClient.post<void>(`/api/workspaces/${id}/processes/${pid}/kill`, {})
  },
}
