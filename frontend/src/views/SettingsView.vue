<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { settingsApi, type TOTPSetup } from '@/api/settings'

// Password change
const currentPassword = ref('')
const newPassword = ref('')
const confirmPassword = ref('')
const passwordError = ref('')
const passwordSuccess = ref('')
const passwordSubmitting = ref(false)

// MFA state
const mfaLoading = ref(true)
const totpEnabled = ref(false)
const totpSetup = ref<TOTPSetup | null>(null)
const totpCode = ref('')
const mfaError = ref('')
const mfaSuccess = ref('')
const mfaSubmitting = ref(false)
const showDisableConfirm = ref(false)
const disablePassword = ref('')

// SSH key state
const sshLoading = ref(true)
const sshPublicKey = ref('')
const sshError = ref('')
const sshSuccess = ref('')
const sshSubmitting = ref(false)
const showRegenerateConfirm = ref(false)
const sshCopied = ref(false)

onMounted(async () => {
  try {
    const status = await settingsApi.getMFAStatus()
    totpEnabled.value = status.totpEnabled
  } catch {
    mfaError.value = 'Failed to load MFA status'
  } finally {
    mfaLoading.value = false
  }

  try {
    const ssh = await settingsApi.getSSHKey()
    sshPublicKey.value = ssh.publicKey
  } catch {
    sshError.value = 'Failed to load SSH key'
  } finally {
    sshLoading.value = false
  }
})

async function handleChangePassword() {
  passwordError.value = ''
  passwordSuccess.value = ''

  if (!currentPassword.value || !newPassword.value || !confirmPassword.value) {
    passwordError.value = 'All fields are required'
    return
  }
  if (newPassword.value.length < 8) {
    passwordError.value = 'New password must be at least 8 characters'
    return
  }
  if (newPassword.value !== confirmPassword.value) {
    passwordError.value = 'Passwords do not match'
    return
  }

  passwordSubmitting.value = true
  try {
    await settingsApi.changePassword(currentPassword.value, newPassword.value)
    passwordSuccess.value = 'Password changed successfully'
    currentPassword.value = ''
    newPassword.value = ''
    confirmPassword.value = ''
  } catch (e) {
    passwordError.value = e instanceof Error ? e.message : 'Failed to change password'
  } finally {
    passwordSubmitting.value = false
  }
}

async function handleSetupTOTP() {
  mfaError.value = ''
  mfaSuccess.value = ''
  mfaSubmitting.value = true
  try {
    totpSetup.value = await settingsApi.setupTOTP()
  } catch (e) {
    mfaError.value = e instanceof Error ? e.message : 'Failed to generate TOTP setup'
  } finally {
    mfaSubmitting.value = false
  }
}

async function handleEnableTOTP() {
  mfaError.value = ''
  mfaSuccess.value = ''

  if (!totpCode.value || totpCode.value.length !== 6) {
    mfaError.value = 'Enter a valid 6-digit code'
    return
  }

  mfaSubmitting.value = true
  try {
    await settingsApi.enableTOTP(totpCode.value)
    totpEnabled.value = true
    totpSetup.value = null
    totpCode.value = ''
    mfaSuccess.value = 'Two-factor authentication enabled'
  } catch (e) {
    mfaError.value = e instanceof Error ? e.message : 'Failed to enable TOTP'
  } finally {
    mfaSubmitting.value = false
  }
}

function openDisableConfirm() {
  disablePassword.value = ''
  mfaError.value = ''
  mfaSuccess.value = ''
  showDisableConfirm.value = true
}

async function handleDisableTOTP() {
  mfaError.value = ''
  mfaSuccess.value = ''

  if (!disablePassword.value) {
    mfaError.value = 'Password is required to disable MFA'
    return
  }

  mfaSubmitting.value = true
  try {
    await settingsApi.disableTOTP(disablePassword.value)
    totpEnabled.value = false
    showDisableConfirm.value = false
    disablePassword.value = ''
    mfaSuccess.value = 'Two-factor authentication disabled'
  } catch (e) {
    mfaError.value = e instanceof Error ? e.message : 'Failed to disable TOTP'
  } finally {
    mfaSubmitting.value = false
  }
}

function cancelSetup() {
  totpSetup.value = null
  totpCode.value = ''
  mfaError.value = ''
}

async function handleGenerateSSHKey() {
  sshError.value = ''
  sshSuccess.value = ''
  sshSubmitting.value = true
  try {
    const result = await settingsApi.generateSSHKey()
    sshPublicKey.value = result.publicKey
    sshSuccess.value = showRegenerateConfirm.value ? 'SSH key regenerated successfully' : 'SSH key generated successfully'
    showRegenerateConfirm.value = false
  } catch (e) {
    sshError.value = e instanceof Error ? e.message : 'Failed to generate SSH key'
  } finally {
    sshSubmitting.value = false
  }
}

async function copySSHKey() {
  try {
    await navigator.clipboard.writeText(sshPublicKey.value)
    sshCopied.value = true
    setTimeout(() => { sshCopied.value = false }, 2000)
  } catch {
    sshError.value = 'Failed to copy to clipboard'
  }
}

function openRegenerateConfirm() {
  sshError.value = ''
  sshSuccess.value = ''
  showRegenerateConfirm.value = true
}

</script>

<template>
  <div class="settings-page">
    <div class="settings-container">
      <h1 class="settings-title">Settings</h1>
      <p class="settings-subtitle">Manage your account and security preferences</p>

      <!-- Password Section -->
      <section class="settings-section">
        <h2 class="section-title">Change Password</h2>
        <p class="section-desc">Update your account password. Must be at least 8 characters.</p>

        <div v-if="passwordError" class="alert alert-error">{{ passwordError }}</div>
        <div v-if="passwordSuccess" class="alert alert-success">{{ passwordSuccess }}</div>

        <form @submit.prevent="handleChangePassword">
          <div class="form-field">
            <label for="current-password">Current Password</label>
            <input
              id="current-password"
              v-model="currentPassword"
              type="password"
              autocomplete="current-password"
              required
              placeholder="Enter current password"
            />
          </div>
          <div class="form-field">
            <label for="new-password">New Password</label>
            <input
              id="new-password"
              v-model="newPassword"
              type="password"
              autocomplete="new-password"
              required
              placeholder="At least 8 characters"
            />
          </div>
          <div class="form-field">
            <label for="confirm-password">Confirm New Password</label>
            <input
              id="confirm-password"
              v-model="confirmPassword"
              type="password"
              autocomplete="new-password"
              required
              placeholder="Repeat new password"
            />
          </div>
          <button type="submit" class="btn-primary" :disabled="passwordSubmitting">
            {{ passwordSubmitting ? 'Changing...' : 'Change Password' }}
          </button>
        </form>
      </section>

      <!-- MFA Section -->
      <section class="settings-section">
        <h2 class="section-title">Two-Factor Authentication</h2>
        <p class="section-desc">
          Add an extra layer of security using a TOTP authenticator app
          (e.g. Google Authenticator, Authy, 1Password).
        </p>

        <div v-if="mfaError" class="alert alert-error">{{ mfaError }}</div>
        <div v-if="mfaSuccess" class="alert alert-success">{{ mfaSuccess }}</div>

        <div v-if="mfaLoading" class="loading-state">Loading MFA status...</div>

        <template v-else>
          <!-- MFA Enabled State -->
          <div v-if="totpEnabled && !showDisableConfirm" class="mfa-status">
            <div class="mfa-badge enabled">
              <span class="mfa-dot"></span>
              Enabled
            </div>
            <p class="mfa-info">Your account is protected with two-factor authentication.</p>
            <button class="btn-danger-outline" @click="openDisableConfirm">Disable 2FA</button>
          </div>

          <!-- Disable Confirmation -->
          <div v-if="showDisableConfirm" class="mfa-disable-form">
            <p class="mfa-warning">
              Disabling two-factor authentication will make your account less secure.
              Enter your password to confirm.
            </p>
            <div class="form-field">
              <label for="disable-password">Password</label>
              <input
                id="disable-password"
                v-model="disablePassword"
                type="password"
                required
                autofocus
                placeholder="Enter your password"
              />
            </div>
            <div class="mfa-actions">
              <button class="btn-secondary" @click="showDisableConfirm = false">Cancel</button>
              <button class="btn-danger" :disabled="mfaSubmitting" @click="handleDisableTOTP">
                {{ mfaSubmitting ? 'Disabling...' : 'Disable 2FA' }}
              </button>
            </div>
          </div>

          <!-- MFA Not Enabled & No Setup In Progress -->
          <div v-if="!totpEnabled && !totpSetup" class="mfa-status">
            <div class="mfa-badge disabled">
              <span class="mfa-dot"></span>
              Not Enabled
            </div>
            <p class="mfa-info">Protect your account with a time-based one-time password.</p>
            <button class="btn-primary" :disabled="mfaSubmitting" @click="handleSetupTOTP">
              {{ mfaSubmitting ? 'Generating...' : 'Set Up 2FA' }}
            </button>
          </div>

          <!-- TOTP Setup Flow -->
          <div v-if="totpSetup" class="totp-setup">
            <div class="setup-steps">
              <div class="setup-step">
                <span class="step-number">1</span>
                <div>
                  <p class="step-title">Scan QR Code</p>
                  <p class="step-desc">
                    Scan this QR code with your authenticator app, or manually enter the secret key below.
                  </p>
                </div>
              </div>

              <div class="qr-container">
                <img :src="'https://api.qrserver.com/v1/create-qr-code/?size=200x200&data=' + encodeURIComponent(totpSetup.url)" alt="TOTP QR Code" class="qr-code" />
              </div>

              <div class="secret-display">
                <label>Secret Key</label>
                <code class="secret-value">{{ totpSetup.secret }}</code>
              </div>

              <div class="setup-step">
                <span class="step-number">2</span>
                <div>
                  <p class="step-title">Verify Code</p>
                  <p class="step-desc">Enter the 6-digit code from your authenticator app to confirm setup.</p>
                </div>
              </div>

              <div class="form-field">
                <input
                  v-model="totpCode"
                  type="text"
                  inputmode="numeric"
                  autocomplete="one-time-code"
                  placeholder="Enter 6-digit code"
                  maxlength="6"
                />
              </div>

              <div class="mfa-actions">
                <button class="btn-secondary" @click="cancelSetup">Cancel</button>
                <button class="btn-primary" :disabled="mfaSubmitting" @click="handleEnableTOTP">
                  {{ mfaSubmitting ? 'Verifying...' : 'Enable 2FA' }}
                </button>
              </div>
            </div>
          </div>
        </template>
      </section>

      <!-- SSH Key Section -->
      <section class="settings-section">
        <h2 class="section-title">SSH Key</h2>
        <p class="section-desc">
          Your SSH key is used to authenticate with Git hosting services (GitHub, GitLab, Bitbucket).
          Copy the public key below and add it to your Git provider's SSH key settings.
        </p>

        <div v-if="sshError" class="alert alert-error">{{ sshError }}</div>
        <div v-if="sshSuccess" class="alert alert-success">{{ sshSuccess }}</div>

        <div v-if="sshLoading" class="loading-state">Loading SSH key...</div>

        <template v-else>
          <!-- No SSH key yet -->
          <div v-if="!sshPublicKey && !showRegenerateConfirm" class="ssh-status">
            <div class="mfa-badge disabled">
              <span class="mfa-dot"></span>
              Not Generated
            </div>
            <p class="mfa-info">Generate an SSH key to enable Git operations over SSH in your workspaces.</p>
            <button class="btn-primary" :disabled="sshSubmitting" @click="handleGenerateSSHKey">
              {{ sshSubmitting ? 'Generating...' : 'Generate SSH Key' }}
            </button>
          </div>

          <!-- SSH key exists -->
          <div v-if="sshPublicKey && !showRegenerateConfirm" class="ssh-key-display">
            <div class="mfa-badge enabled">
              <span class="mfa-dot"></span>
              Configured
            </div>

            <div class="ssh-key-box">
              <label>Public Key</label>
              <code class="ssh-key-value">{{ sshPublicKey }}</code>
            </div>

            <div class="ssh-key-actions">
              <button class="btn-primary" @click="copySSHKey">
                {{ sshCopied ? 'Copied!' : 'Copy Public Key' }}
              </button>
              <button class="btn-danger-outline" @click="openRegenerateConfirm">Regenerate</button>
            </div>
          </div>

          <!-- Regenerate Confirmation -->
          <div v-if="showRegenerateConfirm" class="mfa-disable-form">
            <p class="mfa-warning">
              Regenerating your SSH key will invalidate the current key. You will need to update your
              public key in any Git hosting services where it is configured. Existing workspaces will
              receive the new key on their next start.
            </p>
            <div class="mfa-actions">
              <button class="btn-secondary" @click="showRegenerateConfirm = false">Cancel</button>
              <button class="btn-danger" :disabled="sshSubmitting" @click="handleGenerateSSHKey">
                {{ sshSubmitting ? 'Regenerating...' : 'Regenerate SSH Key' }}
              </button>
            </div>
          </div>
        </template>
      </section>
    </div>
  </div>
</template>

<style scoped>
.settings-page {
  height: 100%;
  overflow-y: auto;
  padding: var(--space-6);
}

.settings-container {
  max-width: 600px;
  margin: 0 auto;
}

.settings-title {
  font-size: 1.5rem;
  font-weight: 500;
  color: var(--text-primary);
  margin-bottom: var(--space-1);
}

.settings-subtitle {
  color: var(--text-muted);
  font-size: 0.85rem;
  margin-bottom: var(--space-6);
}

.settings-section {
  padding: var(--space-6);
  background: var(--bg-surface);
  border: 0.5px solid var(--border-default);
  border-radius: var(--radius-lg);
  margin-bottom: var(--space-6);
}

.section-title {
  font-size: 1.1rem;
  font-weight: 600;
  color: var(--text-primary);
  margin-bottom: var(--space-1);
}

.section-desc {
  color: var(--text-muted);
  font-size: 0.85rem;
  margin-bottom: var(--space-5);
  line-height: 1.5;
}

.alert {
  padding: var(--space-3);
  margin-bottom: var(--space-4);
  border-radius: var(--radius-md);
  font-size: 0.85rem;
}

.alert-error {
  background: var(--error-bg);
  border: 0.5px solid var(--error-border);
  color: var(--accent-rose);
}

.alert-success {
  background: var(--success-bg);
  border: 0.5px solid var(--success-border);
  color: var(--accent-green);
}

.form-field {
  margin-bottom: var(--space-4);
}

.form-field label {
  display: block;
  margin-bottom: var(--space-1);
  font-size: 0.85rem;
  font-weight: 500;
  color: var(--text-secondary);
}

.form-field input[type='text'],
.form-field input[type='password'] {
  width: 100%;
  padding: var(--space-2) var(--space-3);
  background: var(--bg-primary);
  border: 0.5px solid var(--border-default);
  border-radius: var(--radius-md);
  color: var(--text-primary);
  font-family: inherit;
  font-size: 0.9rem;
  transition: border-color var(--transition-fast);
}

.form-field input:focus {
  outline: none;
  border-color: var(--accent-border);
  box-shadow: 0 0 0 3px var(--accent-glow);
}

.form-field input::placeholder {
  color: var(--text-muted);
}

.btn-primary {
  padding: var(--space-2) var(--space-4);
  background: var(--accent);
  color: var(--bg-base);
  font-weight: 500;
  font-size: 0.85rem;
  border-radius: var(--radius-lg);
  transition: opacity var(--transition-fast);
}

.btn-primary:hover:not(:disabled) {
  opacity: 0.9;
}

.btn-primary:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

.btn-secondary {
  padding: var(--space-2) var(--space-4);
  font-size: 0.85rem;
  color: var(--text-secondary);
  border: 0.5px solid var(--border-default);
  border-radius: var(--radius-md);
  transition: all var(--transition-fast);
}

.btn-secondary:hover {
  color: var(--text-primary);
  border-color: var(--border-active);
  background: var(--bg-hover);
}

.btn-danger-outline {
  padding: var(--space-2) var(--space-4);
  font-size: 0.85rem;
  font-weight: 500;
  color: var(--accent-rose);
  border: 0.5px solid var(--error-border);
  border-radius: var(--radius-md);
  background: transparent;
  transition: all var(--transition-fast);
}

.btn-danger-outline:hover {
  background: var(--error-bg);
}

.btn-danger {
  padding: var(--space-2) var(--space-4);
  font-weight: 600;
  font-size: 0.85rem;
  border-radius: var(--radius-md);
  background: var(--error-bg);
  color: var(--accent-rose);
  border: 0.5px solid var(--error-border);
  transition: all var(--transition-fast);
}

.btn-danger:hover:not(:disabled) {
  background: var(--error-bg);
}

.btn-danger:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

.loading-state {
  text-align: center;
  padding: var(--space-6);
  color: var(--text-muted);
}

/* MFA Status */
.mfa-status {
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  gap: var(--space-3);
}

.mfa-badge {
  display: inline-flex;
  align-items: center;
  gap: var(--space-2);
  padding: var(--space-1) var(--space-3);
  font-size: 0.8rem;
  font-weight: 600;
  border-radius: var(--radius-sm);
}

.mfa-badge.enabled {
  background: var(--success-bg);
  color: var(--accent-green);
  border: 0.5px solid var(--success-border);
}

.mfa-badge.disabled {
  background: var(--bg-neutral);
  color: var(--text-secondary);
  border: 0.5px solid var(--border-default);
}

.mfa-dot {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: currentColor;
}

.mfa-info {
  color: var(--text-muted);
  font-size: 0.85rem;
}

.mfa-warning {
  color: var(--accent-amber);
  font-size: 0.85rem;
  margin-bottom: var(--space-4);
  line-height: 1.5;
}

.mfa-actions {
  display: flex;
  gap: var(--space-2);
  margin-top: var(--space-2);
}

.mfa-disable-form {
  padding: var(--space-4);
  background: var(--error-bg);
  border: 0.5px solid var(--error-border);
  border-radius: var(--radius-md);
}

/* TOTP Setup */
.totp-setup {
  margin-top: var(--space-2);
}

.setup-steps {
  display: flex;
  flex-direction: column;
  gap: var(--space-4);
}

.setup-step {
  display: flex;
  align-items: flex-start;
  gap: var(--space-3);
}

.step-number {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 24px;
  height: 24px;
  flex-shrink: 0;
  background: var(--accent);
  color: var(--bg-base);
  font-size: 0.75rem;
  font-weight: 500;
  border-radius: 50%;
}

.step-title {
  font-weight: 600;
  font-size: 0.9rem;
  color: var(--text-primary);
  margin-bottom: var(--space-1);
}

.step-desc {
  color: var(--text-muted);
  font-size: 0.8rem;
  line-height: 1.5;
}

.qr-container {
  display: flex;
  justify-content: center;
  padding: var(--space-4);
  background: var(--qr-bg);
  border-radius: var(--radius-md);
  width: fit-content;
  margin: 0 auto;
}

.qr-code {
  width: 200px;
  height: 200px;
  display: block;
}

.secret-display {
  display: flex;
  flex-direction: column;
  gap: var(--space-1);
}

.secret-display label {
  font-size: 0.8rem;
  font-weight: 500;
  color: var(--text-secondary);
}

.secret-value {
  padding: var(--space-2) var(--space-3);
  background: var(--bg-primary);
  border: 0.5px solid var(--border-default);
  border-radius: var(--radius-md);
  font-family: var(--font-mono);
  font-size: 0.85rem;
  color: var(--accent-blue);
  word-break: break-all;
  user-select: all;
}

/* SSH Key */
.ssh-status {
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  gap: var(--space-3);
}

.ssh-key-display {
  display: flex;
  flex-direction: column;
  gap: var(--space-4);
}

.ssh-key-box {
  display: flex;
  flex-direction: column;
  gap: var(--space-1);
}

.ssh-key-box label {
  font-size: 0.8rem;
  font-weight: 500;
  color: var(--text-secondary);
}

.ssh-key-value {
  padding: var(--space-2) var(--space-3);
  background: var(--bg-primary);
  border: 0.5px solid var(--border-default);
  border-radius: var(--radius-md);
  font-family: var(--font-mono);
  font-size: 0.75rem;
  color: var(--accent-blue);
  word-break: break-all;
  user-select: all;
  line-height: 1.5;
}

.ssh-key-actions {
  display: flex;
  gap: var(--space-2);
}

</style>
