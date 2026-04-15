<script setup lang="ts">
import { ref } from 'vue'
import { useRouter } from 'vue-router'
import { useAuthStore, TOTPRequiredError } from '@/stores/auth'

const auth = useAuthStore()
const router = useRouter()

const username = ref('')
const password = ref('')
const totpCode = ref('')
const error = ref('')
const submitting = ref(false)
const needsTOTP = ref(false)

async function handleSubmit() {
  error.value = ''
  submitting.value = true
  try {
    await auth.login(username.value, password.value, needsTOTP.value ? totpCode.value : undefined)
    router.push({ name: 'home' })
  } catch (e) {
    if (e instanceof TOTPRequiredError) {
      needsTOTP.value = true
    } else {
      error.value = e instanceof Error ? e.message : 'Login failed'
    }
  } finally {
    submitting.value = false
  }
}
</script>

<template>
  <div class="login-page">
    <form class="login-form" @submit.prevent="handleSubmit">
      <div class="form-header">
        <h1 class="form-title">Devpad</h1>
        <p class="form-subtitle">Sign in to your account</p>
      </div>

      <div v-if="error" class="form-error">{{ error }}</div>

      <template v-if="!needsTOTP">
        <div class="form-field">
          <label for="username">Username</label>
          <input
            id="username"
            v-model="username"
            type="text"
            autocomplete="username"
            required
            autofocus
            placeholder="Enter your username"
          />
        </div>

        <div class="form-field">
          <label for="password">Password</label>
          <input
            id="password"
            v-model="password"
            type="password"
            autocomplete="current-password"
            required
            placeholder="Enter your password"
          />
        </div>
      </template>

      <template v-else>
        <div class="form-field">
          <label for="totp">Authentication Code</label>
          <input
            id="totp"
            v-model="totpCode"
            type="text"
            inputmode="numeric"
            autocomplete="one-time-code"
            required
            autofocus
            placeholder="Enter 6-digit code"
            maxlength="6"
          />
          <p class="field-hint">Enter the code from your authenticator app</p>
        </div>
      </template>

      <button type="submit" class="btn-primary" :disabled="submitting">
        {{ submitting ? 'Signing in...' : 'Sign in' }}
      </button>

      <button v-if="needsTOTP" type="button" class="btn-back" @click="needsTOTP = false; totpCode = ''; error = ''">
        Back to login
      </button>
    </form>
  </div>
</template>

<style scoped>
.login-page {
  display: flex;
  align-items: center;
  justify-content: center;
  height: 100%;
}

.login-form {
  width: 100%;
  max-width: 380px;
  padding: var(--space-8);
  background: var(--bg-surface);
  border: 1px solid var(--border-default);
  border-radius: var(--radius-lg);
}

.form-header {
  text-align: center;
  margin-bottom: var(--space-6);
}

.form-title {
  font-size: 1.75rem;
  font-weight: 700;
  background: linear-gradient(135deg, var(--accent-blue), var(--accent-purple));
  -webkit-background-clip: text;
  background-clip: text;
  -webkit-text-fill-color: transparent;
  margin-bottom: var(--space-1);
}

.form-subtitle {
  color: var(--text-secondary);
  font-size: 0.9rem;
}

.form-error {
  padding: var(--space-3);
  margin-bottom: var(--space-4);
  background: rgba(244, 63, 94, 0.1);
  border: 1px solid rgba(244, 63, 94, 0.3);
  border-radius: var(--radius-md);
  color: var(--accent-rose);
  font-size: 0.85rem;
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

.form-field input {
  width: 100%;
  padding: var(--space-2) var(--space-3);
  background: var(--bg-primary);
  border: 1px solid var(--border-default);
  border-radius: var(--radius-md);
  color: var(--text-primary);
  font-family: inherit;
  font-size: 0.9rem;
  transition: border-color var(--transition-fast);
}

.form-field input:focus {
  outline: none;
  border-color: var(--accent-blue);
  box-shadow: 0 0 0 2px rgba(0, 212, 255, 0.15);
}

.form-field input::placeholder {
  color: var(--text-muted);
}

.btn-primary {
  width: 100%;
  padding: var(--space-2) var(--space-4);
  margin-top: var(--space-2);
  background: linear-gradient(135deg, var(--accent-blue), var(--accent-purple));
  color: white;
  font-weight: 600;
  font-size: 0.9rem;
  border-radius: var(--radius-md);
  transition: opacity var(--transition-fast);
}

.btn-primary:hover:not(:disabled) {
  opacity: 0.9;
}

.btn-primary:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

.btn-back {
  width: 100%;
  padding: var(--space-2) var(--space-4);
  margin-top: var(--space-2);
  font-size: 0.85rem;
  color: var(--text-secondary);
  border: 1px solid var(--border-default);
  border-radius: var(--radius-md);
  transition: all var(--transition-fast);
}

.btn-back:hover {
  color: var(--text-primary);
  border-color: var(--border-active);
  background: var(--bg-hover);
}

.field-hint {
  margin-top: var(--space-1);
  font-size: 0.75rem;
  color: var(--text-muted);
}
</style>
