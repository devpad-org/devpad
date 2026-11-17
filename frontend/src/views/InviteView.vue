<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useAuthStore } from '@/stores/auth'
import { authApi, type InviteInfo } from '@/api/auth'
import { ApiError } from '@/api/client'
import { passwordProblem } from '@/utils/password'

const auth = useAuthStore()
const route = useRoute()
const router = useRouter()

// The token rides in the fragment (/invite#<token>), which browsers never
// send to a server, so it stays out of logs and Referer headers.
const token = computed(() => route.hash.replace(/^#/, ''))

type State = 'loading' | 'signed-in' | 'invalid' | 'ready'
const state = ref<State>('loading')
const invite = ref<InviteInfo | null>(null)

const username = ref('')
const email = ref('')
const password = ref('')
const confirmPassword = ref('')
const error = ref('')
const submitting = ref(false)
const signingOut = ref(false)

const expiresOn = computed(() =>
  invite.value ? new Date(invite.value.expiresAt).toLocaleString(undefined, { dateStyle: 'medium', timeStyle: 'short' }) : '',
)

async function checkInvite() {
  if (auth.isAuthenticated) {
    state.value = 'signed-in'
    return
  }
  if (!token.value) {
    state.value = 'invalid'
    return
  }
  state.value = 'loading'
  try {
    const response = await authApi.checkInvite(token.value)
    invite.value = response.invite
    state.value = 'ready'
  } catch (e) {
    if (e instanceof ApiError && e.status === 404) {
      state.value = 'invalid'
    } else {
      state.value = 'ready'
      error.value = e instanceof Error ? e.message : 'Could not check this invite'
    }
  }
}

async function signOut() {
  signingOut.value = true
  error.value = ''
  try {
    await auth.logout()
    await checkInvite()
  } catch (e) {
    error.value = e instanceof Error ? e.message : 'Sign out failed'
  } finally {
    signingOut.value = false
  }
}

async function handleSubmit() {
  error.value = ''
  if (password.value !== confirmPassword.value) {
    error.value = 'Passwords do not match'
    return
  }
  const problem = passwordProblem(password.value)
  if (problem) {
    error.value = problem
    return
  }

  submitting.value = true
  try {
    await auth.acceptInvite({
      token: token.value,
      username: username.value,
      email: email.value,
      password: password.value,
    })
    router.replace({ name: 'home' })
  } catch (e) {
    if (e instanceof ApiError && e.status === 404) {
      state.value = 'invalid'
      return
    }
    error.value = e instanceof Error ? e.message : 'Could not create your account'
  } finally {
    submitting.value = false
  }
}

onMounted(checkInvite)
</script>

<template>
  <div class="invite-page">
    <div class="invite-card">
      <div class="card-header">
        <h1 class="card-title">Devpad</h1>
      </div>

      <div v-if="state === 'loading'" class="card-message">Checking your invite…</div>

      <template v-else-if="state === 'signed-in'">
        <p class="card-subtitle">You're already signed in</p>
        <p class="card-text">
          You're signed in as <strong>{{ auth.user?.username }}</strong>. Sign out to accept this invite and create a
          new account.
        </p>
        <div v-if="error" class="form-error">{{ error }}</div>
        <div class="card-actions">
          <RouterLink :to="{ name: 'home' }" class="btn-secondary">Go to Devpad</RouterLink>
          <button type="button" class="btn-primary" :disabled="signingOut" @click="signOut">
            {{ signingOut ? 'Signing out…' : 'Sign out' }}
          </button>
        </div>
      </template>

      <template v-else-if="state === 'invalid'">
        <p class="card-subtitle">This invite can't be used</p>
        <p class="card-text">
          The link is incomplete, has expired, was revoked, or has already been used. Ask whoever invited you for a new
          one.
        </p>
        <div class="card-actions">
          <RouterLink :to="{ name: 'login' }" class="btn-secondary">Sign in</RouterLink>
        </div>
      </template>

      <form v-else @submit.prevent="handleSubmit">
        <p class="card-subtitle">Create your account</p>
        <p v-if="invite" class="card-text">
          <strong>{{ invite.invitedBy || 'An administrator' }}</strong> invited you to Devpad<template
            v-if="invite.isAdmin"
          > as an administrator</template>. This invite works once and expires {{ expiresOn }}.
        </p>

        <div v-if="error" class="form-error">{{ error }}</div>

        <div class="form-field">
          <label for="invite-username">Username</label>
          <input
            id="invite-username"
            v-model="username"
            type="text"
            autocomplete="username"
            required
            autofocus
            placeholder="Choose a username"
          />
        </div>

        <div class="form-field">
          <label for="invite-email">Email</label>
          <input
            id="invite-email"
            v-model="email"
            type="email"
            autocomplete="email"
            required
            placeholder="you@example.com"
          />
        </div>

        <div class="form-field">
          <label for="invite-password">Password</label>
          <input
            id="invite-password"
            v-model="password"
            type="password"
            autocomplete="new-password"
            required
            placeholder="At least 8 characters"
          />
        </div>

        <div class="form-field">
          <label for="invite-confirm">Confirm Password</label>
          <input
            id="invite-confirm"
            v-model="confirmPassword"
            type="password"
            autocomplete="new-password"
            required
            placeholder="Repeat your password"
          />
        </div>

        <button type="submit" class="btn-primary btn-block" :disabled="submitting">
          {{ submitting ? 'Creating account…' : 'Create Account' }}
        </button>
      </form>
    </div>
  </div>
</template>

<style scoped>
.invite-page {
  display: flex;
  justify-content: center;
  min-height: 100%;
  padding: var(--space-4);
}

.invite-card {
  margin: auto 0;
  width: 100%;
  max-width: 420px;
  padding: var(--space-8);
  background: var(--bg-elevated);
  border: 0.5px solid var(--border-subtle);
  border-radius: var(--radius-lg);
}

.card-header {
  text-align: center;
  margin-bottom: var(--space-4);
}

.card-title {
  font-size: 1.5rem;
  font-weight: 500;
  color: var(--text-primary);
}

.card-subtitle {
  color: var(--text-primary);
  font-size: 1rem;
  font-weight: 500;
  text-align: center;
  margin-bottom: var(--space-2);
}

.card-text,
.card-message {
  color: var(--text-secondary);
  font-size: 0.85rem;
  line-height: 1.5;
  text-align: center;
  margin-bottom: var(--space-5);
}

.card-text strong {
  color: var(--text-primary);
}

.card-actions {
  display: flex;
  justify-content: center;
  gap: var(--space-2);
}

.form-error {
  padding: var(--space-3);
  margin-bottom: var(--space-4);
  background: var(--error-bg);
  border: 0.5px solid var(--error-border);
  border-radius: var(--radius-md);
  color: var(--error);
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
  background: var(--bg-base);
  border: 0.5px solid var(--border-subtle);
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
  color: var(--text-tertiary);
}

.btn-primary,
.btn-secondary {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  padding: var(--space-2) var(--space-4);
  font-size: 0.85rem;
  border-radius: var(--radius-lg);
  text-decoration: none;
  transition: opacity var(--transition-fast), background var(--transition-fast);
}

.btn-primary {
  background: var(--accent);
  color: var(--text-on-accent);
  font-weight: 500;
}

.btn-primary:hover:not(:disabled) {
  opacity: 0.9;
}

.btn-primary:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

.btn-block {
  width: 100%;
  margin-top: var(--space-2);
}

.btn-secondary {
  color: var(--text-secondary);
  border: 0.5px solid var(--border-subtle);
}

.btn-secondary:hover {
  color: var(--text-primary);
  border-color: var(--border-strong);
  background: var(--bg-hover);
}

@media (max-width: 480px) {
  .invite-card {
    padding: var(--space-6) var(--space-4);
  }
}
</style>
