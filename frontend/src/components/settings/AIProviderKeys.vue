<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { aiApi, type AIProvider } from '@/api/ai'

// Emitted after a key is saved or removed, which changes the models the
// user's runs can use.
const emit = defineEmits<{ changed: [] }>()

const providers = ref<AIProvider[]>([])
const keys = ref<Record<string, string>>({})
const loading = ref(true)
const error = ref('')
const success = ref('')
const submitting = ref<string | null>(null)

onMounted(load)

async function load() {
  try {
    providers.value = (await aiApi.listProviders()).providers
    for (const p of providers.value) keys.value[p.id] = ''
  } catch {
    error.value = 'Failed to load AI providers'
  } finally {
    loading.value = false
  }
}

async function run(provider: AIProvider, action: () => Promise<void>, done: string, failed: string) {
  error.value = ''
  success.value = ''
  submitting.value = provider.id
  try {
    await action()
    success.value = done
    providers.value = (await aiApi.listProviders()).providers
    keys.value[provider.id] = ''
    emit('changed')
  } catch (e) {
    error.value = e instanceof Error ? e.message : failed
  } finally {
    submitting.value = null
  }
}

function save(provider: AIProvider) {
  const key = keys.value[provider.id]?.trim()
  if (!key) {
    error.value = `Enter a key for ${provider.name}`
    success.value = ''
    return
  }
  void run(provider, () => aiApi.setProviderKey(provider.id, key), `${provider.name} key saved`, 'Failed to save the key')
}

function remove(provider: AIProvider) {
  void run(provider, () => aiApi.removeProviderKey(provider.id), `${provider.name} key removed`, 'Failed to remove the key')
}
</script>

<template>
  <section class="settings-section">
    <h2 class="section-title">AI Provider Keys</h2>
    <p class="section-desc">
      Your own API keys, which every agent you run uses: the assistant, your tasks and their reviews. Each provider bills
      you for what your agents use. Keys are stored encrypted and never shown again.
    </p>

    <div v-if="error" class="alert alert-error">{{ error }}</div>
    <div v-if="success" class="alert alert-success">{{ success }}</div>

    <div v-if="loading" class="loading-state">Loading providers...</div>

    <div v-else class="card-list">
      <div v-for="provider in providers" :key="provider.id" class="card">
        <div class="card-header">
          <span class="card-name">{{ provider.name }}</span>
          <span v-if="provider.hasApiKey" class="badge active">Key saved</span>
          <span v-else-if="provider.keyUnreadable" class="badge warning">Enter again</span>
          <span v-else class="badge">No key</span>
        </div>
        <p v-if="provider.keyUnreadable" class="card-hint card-desc">
          The saved key can no longer be read, as happens when the server's encryption key changes. Enter it
          again to use {{ provider.name }}.
        </p>
        <form @submit.prevent="save(provider)">
          <div class="form-field">
            <label :for="'apikey-' + provider.id">API key</label>
            <input
              :id="'apikey-' + provider.id"
              v-model="keys[provider.id]"
              type="password"
              autocomplete="off"
              :placeholder="provider.hasApiKey ? '••••••••  (enter a new key to replace it)' : 'Enter API key'"
            />
          </div>
          <div class="card-actions">
            <div class="card-buttons">
              <button
                v-if="provider.hasApiKey || provider.keyUnreadable"
                type="button"
                class="btn-danger-outline"
                :disabled="submitting === provider.id"
                @click="remove(provider)"
              >
                Remove
              </button>
              <button type="submit" class="btn-primary" :disabled="submitting === provider.id">
                {{ submitting === provider.id ? 'Saving...' : 'Save' }}
              </button>
            </div>
          </div>
        </form>
      </div>
    </div>
  </section>
</template>

<style scoped src="./ai-settings.css"></style>
