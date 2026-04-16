import { apiClient } from './client'

export interface Workspace {
  id: number
  name: string
  description: string
  status: 'creating' | 'running' | 'stopped'
  containerId: string
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

interface WorkspaceResponse {
  workspace: Workspace
}

interface WorkspaceListResponse {
  workspaces: Workspace[]
}

interface FileListResponse {
  entries: FileEntry[]
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

  delete(id: number): Promise<void> {
    return apiClient.delete(`/api/workspaces/${id}`)
  },

  listFiles(id: number, path: string): Promise<FileListResponse> {
    return apiClient.get<FileListResponse>(
      `/api/workspaces/${id}/files?path=${encodeURIComponent(path)}`
    )
  },

  async readFile(id: number, path: string): Promise<string> {
    const res = await fetch(`/api/workspaces/${id}/file?path=${encodeURIComponent(path)}`)
    if (!res.ok) {
      throw new Error(`Failed to read file: ${res.status}`)
    }
    return res.text()
  },

  writeFile(id: number, path: string, content: string): Promise<void> {
    return apiClient.put(`/api/workspaces/${id}/file`, { path, content })
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
}
