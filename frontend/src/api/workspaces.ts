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

interface WorkspaceResponse {
  workspace: Workspace
}

interface WorkspaceListResponse {
  workspaces: Workspace[]
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
}
