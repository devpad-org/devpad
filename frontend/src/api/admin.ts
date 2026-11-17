import { apiClient } from './client'

export interface AdminUser {
  id: number
  username: string
  email: string
  isAdmin: boolean
  createdAt: string
  updatedAt: string
}

export interface AdminWorkspace {
  id: number
  userId: number
  name: string
  description: string
  status: 'creating' | 'running' | 'stopped'
  memoryLimit: number
  nanoCpus: number
  createdAt: string
  updatedAt: string
}

export interface AdminInvite {
  id: number
  /** Empty when the invitee picks their own email. */
  email: string
  isAdmin: boolean
  createdBy: string
  expiresAt: string
  createdAt: string
  expired: boolean
}

/** A new invite carries its token this once; the server keeps only a hash. */
export interface CreatedInvite extends AdminInvite {
  token: string
}

interface UsersResponse {
  users: AdminUser[]
}

interface UserResponse {
  user: AdminUser
}

interface WorkspacesResponse {
  workspaces: AdminWorkspace[]
}

interface WorkspaceResponse {
  workspace: AdminWorkspace
}

export const adminApi = {
  listUsers(): Promise<UsersResponse> {
    return apiClient.get<UsersResponse>('/api/admin/users')
  },

  createUser(data: { username: string; email: string; password: string; isAdmin: boolean }): Promise<UserResponse> {
    return apiClient.post<UserResponse>('/api/admin/users', data)
  },

  updateUser(id: number, data: { username: string; email: string; isAdmin: boolean }): Promise<UserResponse> {
    return apiClient.put<UserResponse>(`/api/admin/users/${id}`, data)
  },

  resetPassword(id: number, password: string): Promise<void> {
    return apiClient.post<void>(`/api/admin/users/${id}/reset-password`, { password })
  },

  deleteUser(id: number): Promise<void> {
    return apiClient.delete(`/api/admin/users/${id}`)
  },

  listInvites(): Promise<{ invites: AdminInvite[] }> {
    return apiClient.get<{ invites: AdminInvite[] }>('/api/admin/invites')
  },

  createInvite(data: { email: string; isAdmin: boolean; validDays: number }): Promise<{ invite: CreatedInvite }> {
    return apiClient.post<{ invite: CreatedInvite }>('/api/admin/invites', data)
  },

  revokeInvite(id: number): Promise<void> {
    return apiClient.delete(`/api/admin/invites/${id}`)
  },

  listWorkspaces(): Promise<WorkspacesResponse> {
    return apiClient.get<WorkspacesResponse>('/api/admin/workspaces')
  },

  updateWorkspaceLimits(id: number, data: { memoryLimit: number; nanoCpus: number }): Promise<WorkspaceResponse> {
    return apiClient.put<WorkspaceResponse>(`/api/admin/workspaces/${id}/limits`, data)
  },
}
