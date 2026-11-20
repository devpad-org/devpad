<script setup lang="ts">
import DocLink from '@/components/DocLink.vue'
import { ref, onMounted } from 'vue'
import { settingsApi } from '@/api/settings'

const loading = ref(true)
const publicKey = ref('')
const error = ref('')
const success = ref('')
const submitting = ref(false)
const showRegenerateConfirm = ref(false)
const copied = ref(false)

onMounted(async () => {
  try {
    publicKey.value = (await settingsApi.getSSHKey()).publicKey
  } catch {
    error.value = 'Failed to load SSH key'
  } finally {
    loading.value = false
  }
})

async function generate() {
  error.value = ''
  success.value = ''
  submitting.value = true
  try {
    const regenerating = showRegenerateConfirm.value
    publicKey.value = (await settingsApi.generateSSHKey()).publicKey
    success.value = regenerating ? 'SSH key regenerated successfully' : 'SSH key generated successfully'
    showRegenerateConfirm.value = false
  } catch (e) {
    error.value = e instanceof Error ? e.message : 'Failed to generate SSH key'
  } finally {
    submitting.value = false
  }
}

async function copy() {
  try {
    await navigator.clipboard.writeText(publicKey.value)
    copied.value = true
    setTimeout(() => {
      copied.value = false
    }, 2000)
  } catch {
    error.value = 'Failed to copy to clipboard'
  }
}

function openRegenerateConfirm() {
  error.value = ''
  success.value = ''
  showRegenerateConfirm.value = true
}
</script>

<template>
  <section class="card">
    <div class="card-header">
      <h3 class="card-name">SSH key<DocLink topic="sshKey" label="Your SSH key" /></h3>
      <template v-if="!loading">
        <span v-if="publicKey" class="badge success"><span class="badge-dot"></span>Configured</span>
        <span v-else class="badge"><span class="badge-dot"></span>Not generated</span>
      </template>
    </div>
    <p class="card-desc">
      The key devpad uses to reach Git hosting services (GitHub, GitLab, Bitbucket) for you. Add the public key below to
      your Git provider's SSH key settings.
    </p>

    <div v-if="error" class="alert alert-error">{{ error }}</div>
    <div v-if="success" class="alert alert-success">{{ success }}</div>

    <div v-if="loading" class="loading-state">Loading SSH key...</div>

    <div v-else-if="showRegenerateConfirm" class="confirm-box">
      <p class="confirm-text">
        Regenerating your SSH key will invalidate the current key. You will need to update your public key in any Git
        hosting services where it is configured. Existing workspaces will receive the new key on their next start.
      </p>
      <div class="card-buttons">
        <button class="btn-secondary" @click="showRegenerateConfirm = false">Cancel</button>
        <button class="btn-danger" :disabled="submitting" @click="generate">
          {{ submitting ? 'Regenerating...' : 'Regenerate SSH key' }}
        </button>
      </div>
    </div>

    <template v-else-if="publicKey">
      <code class="mono-value ssh-key">{{ publicKey }}</code>
      <div class="card-actions">
        <div class="card-buttons">
          <button class="btn-danger-outline" @click="openRegenerateConfirm">Regenerate</button>
          <button class="btn-primary" @click="copy">{{ copied ? 'Copied!' : 'Copy public key' }}</button>
        </div>
      </div>
    </template>

    <div v-else class="card-actions">
      <div class="card-buttons">
        <button class="btn-primary" :disabled="submitting" @click="generate">
          {{ submitting ? 'Generating...' : 'Generate SSH key' }}
        </button>
      </div>
    </div>
  </section>
</template>

<style scoped src="./settings.css"></style>

<style scoped>
.ssh-key {
  margin-bottom: var(--space-4);
  font-size: 0.75rem;
}
</style>
