<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import DocLink from '@/components/DocLink.vue'
import { settingsApi, type GitIdentity, type SavedGitIdentity } from '@/api/settings'

const loading = ref(true)
const saving = ref(false)
const error = ref('')
const success = ref('')
const saved = ref<GitIdentity | null>(null)
const name = ref('')
const email = ref('')

const changed = computed(
  () => !!saved.value && (name.value.trim() !== saved.value.name || email.value.trim() !== saved.value.email),
)

function show(identity: GitIdentity) {
  saved.value = identity
  name.value = identity.name
  email.value = identity.email
}

onMounted(async () => {
  try {
    show(await settingsApi.getGitIdentity())
  } catch {
    error.value = 'Failed to load your Git identity'
  } finally {
    loading.value = false
  }
})

function savedMessage(result: SavedGitIdentity): string {
  if (result.syncIncomplete) {
    return "Saved, but some running workspaces couldn't be reached. They'll update when they next start."
  }
  const n = result.workspacesUpdated
  if (n === 0) return 'Saved. Your workspaces will use it when they next start.'
  const running = n === 1 ? '1 running workspace' : `${n} running workspaces`
  return `Saved and applied to ${running}. Stopped ones update when they next start.`
}

async function save() {
  error.value = ''
  success.value = ''
  saving.value = true
  try {
    const result = await settingsApi.saveGitIdentity(name.value, email.value)
    show(result)
    success.value = savedMessage(result)
  } catch (e) {
    error.value = e instanceof Error ? e.message : 'Failed to save your Git identity'
  } finally {
    saving.value = false
  }
}
</script>

<template>
  <section class="card">
    <div class="card-header">
      <h3 class="card-name">Identity<DocLink topic="gitIdentity" label="Your Git identity" /></h3>
    </div>
    <p class="card-desc">
      The name and email on your commits, your agents' commits and your merges. Anything pushed upstream shows them.
      Saving replaces any identity set by hand in your project workspaces and sandboxes.
    </p>

    <div v-if="error" class="alert alert-error" role="alert">{{ error }}</div>
    <div v-if="success" class="alert alert-success" role="status">{{ success }}</div>

    <div v-if="loading" class="loading-state">Loading Git identity...</div>

    <form v-else-if="saved" @submit.prevent="save">
      <div class="card-fields">
        <div class="form-field">
          <label for="git-name">Name</label>
          <input id="git-name" v-model="name" type="text" autocomplete="name" :placeholder="saved.accountName" />
          <p class="field-hint">Leave empty to use your username.</p>
        </div>
        <div class="form-field">
          <label for="git-email">Email</label>
          <input
            id="git-email"
            v-model="email"
            type="text"
            inputmode="email"
            autocomplete="email"
            spellcheck="false"
            :placeholder="saved.accountEmail"
          />
          <p class="field-hint">Leave empty to use your account email.</p>
        </div>
      </div>
      <div class="card-actions">
        <div class="card-buttons">
          <button type="submit" class="btn-primary" :disabled="saving || !changed">
            {{ saving ? 'Saving...' : 'Save' }}
          </button>
        </div>
      </div>
    </form>
  </section>
</template>

<style scoped src="./settings.css"></style>
