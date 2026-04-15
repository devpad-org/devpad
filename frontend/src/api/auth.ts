import { apiClient } from './client'

export interface User {
  id: number
  username: string
  email: string
  isAdmin: boolean
  totpEnabled: boolean
}

interface AuthResponse {
  user: User
}

interface LoginResponse {
  user?: User
  totpRequired?: boolean
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

  login(username: string, password: string, totpCode?: string): Promise<LoginResponse> {
    return apiClient.post<LoginResponse>('/api/auth/login', { username, password, totpCode })
  },

  logout(): Promise<void> {
    return apiClient.post<void>('/api/auth/logout', {})
  },

  me(): Promise<AuthResponse> {
    return apiClient.get<AuthResponse>('/api/auth/me')
  },
}
