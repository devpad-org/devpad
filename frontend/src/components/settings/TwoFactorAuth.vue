<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { settingsApi, type TOTPSetup } from '@/api/settings'

const loading = ref(true)
const enabled = ref(false)
const setup = ref<TOTPSetup | null>(null)
const code = ref('')
const error = ref('')
const success = ref('')
const submitting = ref(false)
const showDisableConfirm = ref(false)
const disablePassword = ref('')

onMounted(async () => {
  try {
    enabled.value = (await settingsApi.getMFAStatus()).totpEnabled
  } catch {
    error.value = 'Failed to load two-factor authentication status'
  } finally {
    loading.value = false
  }
})

function clearMessages() {
  error.value = ''
  success.value = ''
}

async function startSetup() {
  clearMessages()
  submitting.value = true
  try {
    setup.value = await settingsApi.setupTOTP()
  } catch (e) {
    error.value = e instanceof Error ? e.message : 'Failed to generate TOTP setup'
  } finally {
    submitting.value = false
  }
}

async function enable() {
  clearMessages()
  if (!code.value || code.value.length !== 6) {
    error.value = 'Enter a valid 6-digit code'
    return
  }

  submitting.value = true
  try {
    await settingsApi.enableTOTP(code.value)
    enabled.value = true
    setup.value = null
    code.value = ''
    success.value = 'Two-factor authentication enabled'
  } catch (e) {
    error.value = e instanceof Error ? e.message : 'Failed to enable TOTP'
  } finally {
    submitting.value = false
  }
}

function cancelSetup() {
  setup.value = null
  code.value = ''
  error.value = ''
}

function openDisableConfirm() {
  clearMessages()
  disablePassword.value = ''
  showDisableConfirm.value = true
}

async function disable() {
  clearMessages()
  if (!disablePassword.value) {
    error.value = 'Password is required to disable two-factor authentication'
    return
  }

  submitting.value = true
  try {
    await settingsApi.disableTOTP(disablePassword.value)
    enabled.value = false
    showDisableConfirm.value = false
    disablePassword.value = ''
    success.value = 'Two-factor authentication disabled'
  } catch (e) {
    error.value = e instanceof Error ? e.message : 'Failed to disable TOTP'
  } finally {
    submitting.value = false
  }
}
</script>

<template>
  <section class="card">
    <div class="card-header">
      <h3 class="card-name">Two-factor authentication</h3>
      <template v-if="!loading">
        <span v-if="enabled" class="badge success"><span class="badge-dot"></span>Enabled</span>
        <span v-else class="badge"><span class="badge-dot"></span>Not enabled</span>
      </template>
    </div>
    <p class="card-desc">
      Ask for a code from an authenticator app (such as Google Authenticator, Authy or 1Password) as well as your
      password when you sign in.
    </p>

    <div v-if="error" class="alert alert-error">{{ error }}</div>
    <div v-if="success" class="alert alert-success">{{ success }}</div>

    <div v-if="loading" class="loading-state">Loading...</div>

    <template v-else>
      <!-- Disable confirmation -->
      <div v-if="showDisableConfirm" class="confirm-box">
        <p class="confirm-text">
          Disabling two-factor authentication will make your account less secure. Enter your password to confirm.
        </p>
        <div class="form-field">
          <label for="disable-password">Password</label>
          <input
            id="disable-password"
            v-model="disablePassword"
            type="password"
            autocomplete="current-password"
            required
            autofocus
            placeholder="Enter your password"
            @keydown.enter.prevent="disable"
          />
        </div>
        <div class="card-buttons">
          <button class="btn-secondary" @click="showDisableConfirm = false">Cancel</button>
          <button class="btn-danger" :disabled="submitting" @click="disable">
            {{ submitting ? 'Disabling...' : 'Disable 2FA' }}
          </button>
        </div>
      </div>

      <!-- Setup flow -->
      <div v-else-if="setup" class="totp-setup">
        <div class="setup-step">
          <span class="step-number">1</span>
          <div>
            <p class="step-title">Scan the QR code</p>
            <p class="step-desc">
              Scan this QR code with your authenticator app, or enter the secret key below by hand.
            </p>
          </div>
        </div>

        <div class="qr-container">
          <img
            :src="'https://api.qrserver.com/v1/create-qr-code/?size=200x200&data=' + encodeURIComponent(setup.url)"
            alt="TOTP QR code"
            class="qr-code"
          />
        </div>

        <div class="form-field">
          <label>Secret key</label>
          <code class="mono-value">{{ setup.secret }}</code>
        </div>

        <div class="setup-step">
          <span class="step-number">2</span>
          <div>
            <p class="step-title">Verify the code</p>
            <p class="step-desc">Enter the 6-digit code from your authenticator app to finish.</p>
          </div>
        </div>

        <form @submit.prevent="enable">
          <div class="form-field">
            <input
              v-model="code"
              type="text"
              inputmode="numeric"
              autocomplete="one-time-code"
              placeholder="Enter 6-digit code"
              maxlength="6"
            />
          </div>
          <div class="card-buttons">
            <button type="button" class="btn-secondary" @click="cancelSetup">Cancel</button>
            <button type="submit" class="btn-primary" :disabled="submitting">
              {{ submitting ? 'Verifying...' : 'Enable 2FA' }}
            </button>
          </div>
        </form>
      </div>

      <div v-else class="card-actions">
        <div class="card-buttons">
          <button v-if="enabled" class="btn-danger-outline" @click="openDisableConfirm">Disable 2FA</button>
          <button v-else class="btn-primary" :disabled="submitting" @click="startSetup">
            {{ submitting ? 'Generating...' : 'Set up 2FA' }}
          </button>
        </div>
      </div>
    </template>
  </section>
</template>

<style scoped src="./settings.css"></style>

<style scoped>
.totp-setup {
  display: flex;
  flex-direction: column;
  gap: var(--space-4);
}

.totp-setup .form-field {
  margin-bottom: var(--space-3);
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
</style>
