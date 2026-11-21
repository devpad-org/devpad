import { apiClient } from './client'

export interface MFAStatus {
  totpEnabled: boolean
}

export interface TOTPSetup {
  secret: string
  url: string
  /** The provisioning URL as a QR code, a data URL devpad renders itself. */
  qrCode: string
}

export interface SSHKeyResponse {
  publicKey: string
}

// What the user commits as. An empty name or email means the account's own.
export interface GitIdentity {
  name: string
  email: string
  accountName: string
  accountEmail: string
  // Whether one has been saved yet, even an empty one.
  saved: boolean
}

export interface SavedGitIdentity extends GitIdentity {
  // Running workspaces now using it.
  workspacesUpdated: number
  // A running workspace couldn't be updated; it will be when it next starts.
  syncIncomplete: boolean
}

export interface UserPreferences {
  diffViewSideBySide: boolean
  // A theme id from @/theme.
  theme: string
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

  getGitIdentity(): Promise<GitIdentity> {
    return apiClient.get<GitIdentity>('/api/settings/git-identity')
  },

  saveGitIdentity(name: string, email: string): Promise<SavedGitIdentity> {
    return apiClient.put<SavedGitIdentity>('/api/settings/git-identity', { name, email })
  },

  getPreferences(): Promise<UserPreferences> {
    return apiClient.get<UserPreferences>('/api/settings/preferences')
  },

  // Changes only the preferences given and returns them all.
  updatePreferences(preferences: Partial<UserPreferences>): Promise<UserPreferences> {
    return apiClient.put<UserPreferences>('/api/settings/preferences', preferences)
  },
}
