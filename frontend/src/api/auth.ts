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

/** What an invite will make, as shown to the person holding its link. */
export interface InviteInfo {
  isAdmin: boolean
  invitedBy: string
  expiresAt: string
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

  // The token goes in the body, never the URL, so it stays out of logs.
  checkInvite(token: string): Promise<{ invite: InviteInfo }> {
    return apiClient.post<{ invite: InviteInfo }>('/api/auth/invites/check', { token })
  },

  acceptInvite(data: { token: string; username: string; email: string; password: string }): Promise<AuthResponse> {
    return apiClient.post<AuthResponse>('/api/auth/invites/accept', data)
  },

  me(): Promise<AuthResponse> {
    return apiClient.get<AuthResponse>('/api/auth/me')
  },
}
