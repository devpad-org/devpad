import { apiClient } from './client'

export interface User {
  id: number
  username: string
  email: string
  isAdmin: boolean
}

interface AuthResponse {
  user: User
}

interface SetupCheckResponse {
  needsSetup: boolean
}

export const authApi = {
  checkSetup(): Promise<SetupCheckResponse> {
    return apiClient.get<SetupCheckResponse>('/api/auth/setup')
  },

  setup(username: string, email: string, password: string): Promise<AuthResponse> {
    return apiClient.post<AuthResponse>('/api/auth/setup', { username, email, password })
  },

  login(username: string, password: string): Promise<AuthResponse> {
    return apiClient.post<AuthResponse>('/api/auth/login', { username, password })
  },

  logout(): Promise<void> {
    return apiClient.post<void>('/api/auth/logout', {})
  },

  me(): Promise<AuthResponse> {
    return apiClient.get<AuthResponse>('/api/auth/me')
  },
}
