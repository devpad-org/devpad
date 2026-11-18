<script setup lang="ts">
import DocLink from '@/components/DocLink.vue'
import { ref, onMounted } from 'vue'
import { aiApi, type AIProvider } from '@/api/ai'
import { useAiModelStore } from '@/stores/aiModels'

// A key saved or removed changes which models the user's runs can use, which
// pickers elsewhere read from the models store.
const aiModelStore = useAiModelStore()

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
    void aiModelStore.fetchModels()
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
  <div>
    <header class="page-header">
      <h2 class="page-title">Provider Keys<DocLink topic="aiKeys" label="Provider keys" /></h2>
      <p class="page-desc">
        Your own API keys, which every agent you run uses: the assistant, your tasks and their reviews. Each provider bills
        you for what your agents use. Keys are stored encrypted and never shown again.
      </p>
    </header>

    <div v-if="error" class="alert alert-error">{{ error }}</div>
    <div v-if="success" class="alert alert-success">{{ success }}</div>

    <div v-if="loading" class="loading-state">Loading providers...</div>

    <div v-else class="card key-list">
      <form
        v-for="provider in providers"
        :key="provider.id"
        class="key-row"
        @submit.prevent="save(provider)"
      >
        <div class="key-provider">
          <label class="key-name" :for="'apikey-' + provider.id">{{ provider.name }}</label>
          <span v-if="provider.hasApiKey" class="key-status saved"><span class="badge-dot"></span>Key saved</span>
          <span v-else-if="provider.keyUnreadable" class="key-status warning">
            <span class="badge-dot"></span>Enter again
          </span>
          <span v-else class="key-status"><span class="badge-dot"></span>No key</span>
        </div>
        <input
          :id="'apikey-' + provider.id"
          v-model="keys[provider.id]"
          class="key-input"
          type="password"
          autocomplete="off"
          :placeholder="provider.hasApiKey ? 'Enter a new key to replace it' : 'Enter API key'"
        />
        <div class="key-buttons">
          <button
            v-if="provider.hasApiKey || provider.keyUnreadable"
            type="button"
            class="btn-danger-outline"
            :disabled="submitting === provider.id"
            @click="remove(provider)"
          >
            Remove
          </button>
          <!-- Quiet until there's a key to save, so the column of buttons
               doesn't compete with the fields. -->
          <button
            type="submit"
            :class="keys[provider.id]?.trim() ? 'btn-primary' : 'btn-secondary'"
            :disabled="submitting === provider.id || !keys[provider.id]?.trim()"
          >
            {{ submitting === provider.id ? 'Saving...' : 'Save' }}
          </button>
        </div>
        <p v-if="provider.keyUnreadable" class="key-hint card-hint">
          The saved key can no longer be read, as happens when the server's encryption key changes. Enter it again to
          use {{ provider.name }}.
        </p>
      </form>
    </div>
  </div>
</template>

<style scoped src="./settings.css"></style>

<style scoped>
/* One card with a row per provider: few enough fields that each provider
   doesn't need a card of its own. */
.key-list {
  padding: 0;
}

.key-row {
  display: grid;
  grid-template-columns: 150px minmax(0, 1fr) auto;
  align-items: center;
  column-gap: var(--space-4);
  padding: var(--space-3) var(--space-5);
}

.key-row + .key-row {
  border-top: 0.5px solid var(--border-default);
}

.key-provider {
  display: flex;
  flex-direction: column;
  gap: 2px;
  min-width: 0;
}

.key-name {
  font-weight: 600;
  font-size: 0.9rem;
  color: var(--text-primary);
}

.key-status {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  font-size: 0.75rem;
  color: var(--text-muted);
}

.key-status.saved {
  color: var(--accent-green);
}

.key-status.warning {
  color: var(--accent-amber);
}

.key-input {
  width: 100%;
  padding: var(--space-2) var(--space-3);
  background: var(--bg-primary);
  border: 0.5px solid var(--border-default);
  border-radius: var(--radius-md);
  color: var(--text-primary);
  font-family: inherit;
  font-size: 0.85rem;
  transition: border-color var(--transition-fast);
}

.key-input:focus {
  outline: none;
  border-color: var(--accent-border);
  box-shadow: 0 0 0 3px var(--accent-glow);
}

.key-input::placeholder {
  color: var(--text-muted);
}

.key-buttons {
  display: flex;
  justify-content: flex-end;
  gap: var(--space-2);
  /* Keeps Save in one column whether or not a row has Remove. */
  min-width: 152px;
}

.key-hint {
  grid-column: 2 / -1;
  margin-top: var(--space-2);
  line-height: 1.5;
}

@media (max-width: 768px) {
  .key-row {
    grid-template-columns: minmax(0, 1fr) auto;
    row-gap: var(--space-2);
    padding: var(--space-3) var(--space-4);
  }

  .key-provider {
    grid-column: 1 / -1;
    flex-direction: row;
    align-items: baseline;
    justify-content: space-between;
  }

  .key-buttons {
    min-width: 0;
  }

  .key-hint {
    grid-column: 1 / -1;
    margin-top: 0;
  }
}
</style>
