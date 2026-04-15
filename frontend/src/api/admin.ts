import { apiClient } from './client'

export interface AdminUser {
  id: number
  username: string
  email: string
  isAdmin: boolean
  createdAt: string
  updatedAt: string
}

interface UsersResponse {
  users: AdminUser[]
}

interface UserResponse {
  user: AdminUser
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
}
