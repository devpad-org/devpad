import { apiClient } from './client'

export interface MFAStatus {
  totpEnabled: boolean
}

export interface TOTPSetup {
  secret: string
  url: string
}

export interface SSHKeyResponse {
  publicKey: string
}

export interface UserPreferences {
  diffViewSideBySide: boolean
}

export const settingsApi = {
  changePassword(currentPassword: string, newPassword: string): Promise<void> {
    return apiClient.post<void>('/api/settings/password', { currentPassword, newPassword })
  },

  getMFAStatus(): Promise<MFAStatus> {
    return apiClient.get<MFAStatus>('/api/settings/mfa')
  },

  setupTOTP(): Promise<TOTPSetup> {
    return apiClient.post<TOTPSetup>('/api/settings/mfa/setup', {})
  },

  enableTOTP(code: string): Promise<void> {
    return apiClient.post<void>('/api/settings/mfa/enable', { code })
  },

  disableTOTP(password: string): Promise<void> {
    return apiClient.post<void>('/api/settings/mfa/disable', { password })
  },

  getSSHKey(): Promise<SSHKeyResponse> {
    return apiClient.get<SSHKeyResponse>('/api/settings/ssh-key')
  },

  generateSSHKey(): Promise<SSHKeyResponse> {
    return apiClient.post<SSHKeyResponse>('/api/settings/ssh-key/generate', {})
  },

  getPreferences(): Promise<UserPreferences> {
    return apiClient.get<UserPreferences>('/api/settings/preferences')
  },

  updatePreferences(preferences: UserPreferences): Promise<UserPreferences> {
    return apiClient.put<UserPreferences>('/api/settings/preferences', preferences)
  },
}
