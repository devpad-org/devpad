<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { useAiAgentStore, type AIAgent } from '@/stores/aiAgents'

const props = defineProps<{
  workspaceId: number
  selectedAgentId: string
  defaultAgentId: string
  defaultAgentError?: string | null
  defaultAgentSavingId?: string | null
}>()

const emit = defineEmits<{
  (e: 'select', agentId: string): void
  (e: 'set-default', agentId: string): void
}>()

const store = useAiAgentStore()
const modalOpen = ref(false)
const editingAgent = ref<AIAgent | null>(null)
const saving = ref(false)

const form = reactive({
  name: '',
  purpose: '',
  instructions: '',
  isGlobal: false,
})

const modalTitle = computed(() => editingAgent.value ? 'Edit Agent' : 'Create Agent')
const panelError = computed(() => props.defaultAgentError || store.error)

watch(() => props.workspaceId, (workspaceId) => {
  if (workspaceId > 0) void store.fetchAgents(workspaceId)
}, { immediate: true })

watch(() => props.selectedAgentId, (agentId) => {
  if (agentId) store.setSelectedAgent(agentId)
}, { immediate: true })

function selectAgent(agent: AIAgent): void {
  store.setSelectedAgent(agent.id)
  emit('select', agent.id)
}

function setDefaultAgent(agent: AIAgent): void {
  if (props.defaultAgentSavingId || agent.id === props.defaultAgentId) return
  store.setSelectedAgent(agent.id)
  emit('set-default', agent.id)
}

function openCreateModal(): void {
  editingAgent.value = null
  form.name = ''
  form.purpose = ''
  form.instructions = ''
  form.isGlobal = false
  modalOpen.value = true
}

function openEditModal(agent: AIAgent): void {
  if (agent.isDefault) return
  editingAgent.value = agent
  form.name = agent.name
  form.purpose = agent.purpose
  form.instructions = agent.instructions
  form.isGlobal = agent.isGlobal
  modalOpen.value = true
}

function closeModal(): void {
  if (saving.value) return
  modalOpen.value = false
}

async function saveAgent(): Promise<void> {
  if (props.workspaceId <= 0 || saving.value) return
  saving.value = true
  const payload = {
    workspaceId: props.workspaceId,
    name: form.name,
    purpose: form.purpose,
    instructions: form.instructions,
    isGlobal: form.isGlobal,
  }
  const saved = editingAgent.value
    ? await store.updateAgent(editingAgent.value.id, payload)
    : await store.createAgent(payload)
  saving.value = false
  if (saved) {
    modalOpen.value = false
    emit('select', saved.id)
  }
}

async function deleteEditingAgent(): Promise<void> {
  if (!editingAgent.value || editingAgent.value.isDefault || saving.value) return
  saving.value = true
  const deleted = await store.deleteAgent(editingAgent.value.id)
  saving.value = false
  if (deleted) {
    const deletedDefaultAgent = editingAgent.value.id === props.defaultAgentId
    modalOpen.value = false
    if (deletedDefaultAgent) {
      emit('set-default', 'default')
      emit('select', 'default')
    } else {
      emit('select', store.selectedAgentId)
    }
  }
}
</script>

<template>
  <div class="agents-panel">
    <header class="agents-header">
      <span class="agents-title">
        <svg class="panel-icon" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
          <path d="m12 3-1.912 5.813a2 2 0 0 1-1.275 1.275L3 12l5.813 1.912a2 2 0 0 1 1.275 1.275L12 21l1.912-5.813a2 2 0 0 1 1.275-1.275L21 12l-5.813-1.912a2 2 0 0 1-1.275-1.275L12 3Z" />
        </svg>
        AI Agents
      </span>
      <button type="button" class="agents-add" title="Create agent" @click="openCreateModal">
        <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.25" stroke-linecap="round" stroke-linejoin="round">
          <path d="M12 5v14" />
          <path d="M5 12h14" />
        </svg>
      </button>
    </header>

    <div v-if="panelError" class="agents-error">{{ panelError }}</div>
    <div v-if="store.loading" class="agents-empty">Loading agents…</div>
    <div v-else class="agents-list">
      <div
        v-for="agent in store.agents"
        :key="agent.id"
        class="agent-card"
        :class="{ active: agent.id === store.selectedAgentId }"
      >
        <button type="button" class="agent-card-main" @click="selectAgent(agent)">
          <span class="agent-card-icon">
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
              <path d="m12 3-1.912 5.813a2 2 0 0 1-1.275 1.275L3 12l5.813 1.912a2 2 0 0 1 1.275 1.275L12 21l1.912-5.813a2 2 0 0 1 1.275-1.275L21 12l-5.813-1.912a2 2 0 0 1-1.275-1.275L12 3Z" />
            </svg>
          </span>
          <span class="agent-card-copy">
            <span class="agent-card-title">{{ agent.name }}</span>
            <span class="agent-card-purpose">{{ agent.purpose || 'Custom Devpad agent' }}</span>
            <span class="agent-card-scope">
              {{ agent.id === props.defaultAgentId ? 'Default' : agent.isDefault ? 'Baked in' : agent.isGlobal ? 'Global' : 'Workspace' }}
            </span>
          </span>
        </button>
        <button
          type="button"
          class="agent-default"
          :class="{ active: agent.id === props.defaultAgentId }"
          :title="agent.id === props.defaultAgentId ? 'Workspace default agent' : 'Set as workspace default'"
          :aria-pressed="agent.id === props.defaultAgentId"
          :disabled="Boolean(props.defaultAgentSavingId)"
          @click="setDefaultAgent(agent)"
        >
          <svg width="13" height="13" viewBox="0 0 24 24" :fill="agent.id === props.defaultAgentId ? 'currentColor' : 'none'" stroke="currentColor" stroke-width="2.1" stroke-linecap="round" stroke-linejoin="round">
            <polygon points="12 2 15.09 8.26 22 9.27 17 14.14 18.18 21.02 12 17.77 5.82 21.02 7 14.14 2 9.27 8.91 8.26 12 2" />
          </svg>
        </button>
        <button
          v-if="!agent.isDefault"
          type="button"
          class="agent-edit"
          title="Edit agent"
          @click="openEditModal(agent)"
        >
          <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.1" stroke-linecap="round" stroke-linejoin="round">
            <path d="M12 20h9" />
            <path d="M16.5 3.5a2.12 2.12 0 0 1 3 3L7 19l-4 1 1-4Z" />
          </svg>
        </button>
      </div>
    </div>

    <Teleport to="body">
      <div v-if="modalOpen" class="agent-modal-backdrop" @click.self="closeModal">
        <form class="agent-modal" @submit.prevent="saveAgent">
          <header class="agent-modal-header">
            <h3>{{ modalTitle }}</h3>
            <button type="button" class="agent-modal-close" @click="closeModal">×</button>
          </header>
          <label>
            <span>Name</span>
            <input v-model="form.name" required maxlength="80" placeholder="Code review" />
          </label>
          <label>
            <span>Purpose</span>
            <input v-model="form.purpose" maxlength="200" placeholder="Review diffs for correctness and risk" />
          </label>
          <label>
            <span>Instructions</span>
            <textarea v-model="form.instructions" required rows="8" placeholder="Describe how this agent should behave…" />
          </label>
          <label class="agent-global-toggle">
            <input v-model="form.isGlobal" type="checkbox" />
            <span>Make this agent global for my account</span>
          </label>
          <p class="agent-modal-error" v-if="store.error">{{ store.error }}</p>
          <footer class="agent-modal-actions">
            <button
              v-if="editingAgent"
              type="button"
              class="agent-delete-btn"
              :disabled="saving"
              @click="deleteEditingAgent"
            >
              Delete
            </button>
            <span class="agent-modal-spacer" />
            <button type="button" class="agent-secondary-btn" :disabled="saving" @click="closeModal">Cancel</button>
            <button type="submit" class="agent-primary-btn" :disabled="saving">
              {{ saving ? 'Saving…' : 'Save Agent' }}
            </button>
          </footer>
        </form>
      </div>
    </Teleport>
  </div>
</template>

<style scoped>
.agents-panel {
  --ide-header-icon: var(--accent);
  display: flex;
  flex-direction: column;
  height: 100%;
  background: var(--ide-panel-bg);
  color: var(--text-secondary);
}

.agents-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-2);
  padding: 0 var(--space-3);
  border-bottom: 0.5px solid var(--border-default);
  background: var(--ide-header-bg);
  height: 38px;
  flex-shrink: 0;
}

.agents-title {
  display: flex;
  align-items: center;
  gap: 6px;
  font-size: var(--ide-header-title-size);
  font-weight: 600;
  color: var(--text-primary);
}

.panel-icon {
  color: var(--ide-header-icon);
  flex-shrink: 0;
}

.agent-modal-header h3 {
  margin: 0;
  color: var(--text-primary);
  font-size: 0.86rem;
  font-weight: 650;
}

.agents-add,
.agent-default,
.agent-edit,
.agent-modal-close {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  border: 0.5px solid var(--border-default);
  border-radius: var(--radius-md);
  color: var(--text-secondary);
  transition: all var(--transition-fast);
}

.agents-add {
  width: 22px;
  height: 22px;
  border-radius: var(--radius-sm);
}

.agents-add:hover,
.agent-default:hover,
.agent-edit:hover,
.agent-modal-close:hover {
  border-color: var(--border-active);
  background: var(--bg-hover);
  color: var(--text-primary);
}

.agent-default.active {
  border-color: var(--accent-border);
  background: var(--accent-glow);
  color: var(--accent);
}

.agent-default:disabled {
  cursor: not-allowed;
  opacity: 0.65;
}

.agents-error,
.agent-modal-error {
  margin: var(--space-2) var(--space-3);
  color: var(--accent-rose);
  font-size: 0.72rem;
}

.agents-empty {
  padding: var(--space-4);
  color: var(--text-muted);
  font-size: 0.76rem;
}

.agents-list {
  display: grid;
  gap: var(--space-2);
  padding: var(--space-3);
  overflow-y: auto;
}

.agent-card {
  display: grid;
  grid-template-columns: minmax(0, 1fr) auto auto;
  align-items: start;
  gap: var(--space-2);
  width: 100%;
  padding: var(--space-2);
  border: 0.5px solid var(--border-subtle);
  border-radius: var(--radius-lg);
  background: var(--bg-elevated);
  color: var(--text-secondary);
  text-align: left;
  transition: border-color var(--transition-fast), background var(--transition-fast), transform var(--transition-fast);
}

.agent-card-main {
  display: grid;
  grid-template-columns: auto minmax(0, 1fr);
  align-items: start;
  gap: var(--space-2);
  min-width: 0;
  color: inherit;
  text-align: left;
}

.agent-card:hover,
.agent-card.active {
  border-color: var(--accent-border);
  background: var(--accent-glow);
}

.agent-card:hover {
  transform: translateY(-1px);
}

.agent-card-icon {
  display: inline-flex;
  width: 24px;
  height: 24px;
  align-items: center;
  justify-content: center;
  border-radius: var(--radius-md);
  background: var(--bg-hover);
  color: var(--accent);
}

.agent-card-copy {
  display: grid;
  min-width: 0;
  gap: 2px;
}

.agent-card-title {
  overflow: hidden;
  color: var(--text-primary);
  font-size: 0.78rem;
  font-weight: 650;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.agent-card-purpose,
.agent-card-scope {
  overflow: hidden;
  color: var(--text-muted);
  font-size: 0.68rem;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.agent-card-scope {
  color: var(--accent);
  font-family: var(--font-mono);
  text-transform: uppercase;
}

.agent-default,
.agent-edit {
  width: 24px;
  height: 24px;
}

.agent-modal-backdrop {
  position: fixed;
  inset: 0;
  z-index: 80;
  display: flex;
  align-items: center;
  justify-content: center;
  padding: var(--space-4);
  background: var(--bg-overlay);
}

.agent-modal {
  display: grid;
  gap: var(--space-3);
  width: min(560px, 100%);
  padding: var(--space-4);
  border: 0.5px solid var(--border-strong);
  border-radius: var(--radius-xl);
  background: var(--bg-surface);
  box-shadow: var(--shadow-strong);
}

.agent-modal-header,
.agent-modal-actions {
  display: flex;
  align-items: center;
  gap: var(--space-2);
}

.agent-modal-close {
  margin-left: auto;
  width: 26px;
  height: 26px;
  font-size: 1.1rem;
}

.agent-modal label {
  display: grid;
  gap: var(--space-1);
  color: var(--text-secondary);
  font-size: 0.74rem;
  font-weight: 600;
}

.agent-modal input,
.agent-modal textarea {
  width: 100%;
  border: 0.5px solid var(--border-default);
  border-radius: var(--radius-md);
  background: var(--bg-elevated);
  color: var(--text-primary);
  font: inherit;
  font-weight: 400;
  padding: var(--space-2);
  outline: none;
  transition: border-color var(--transition-fast), box-shadow var(--transition-fast);
}

.agent-modal textarea {
  resize: vertical;
  font-family: var(--font-mono);
  line-height: 1.5;
}

.agent-modal input:focus,
.agent-modal textarea:focus {
  border-color: var(--accent-border);
  box-shadow: 0 0 0 3px var(--accent-glow);
}

.agent-global-toggle {
  display: flex !important;
  grid-template-columns: none !important;
  align-items: center;
  gap: var(--space-2) !important;
  font-weight: 500 !important;
}

.agent-global-toggle input {
  width: auto;
}

.agent-modal-spacer {
  flex: 1;
}

.agent-primary-btn,
.agent-secondary-btn,
.agent-delete-btn {
  padding: var(--space-2) var(--space-3);
  border-radius: var(--radius-md);
  font-size: 0.76rem;
  font-weight: 650;
  transition: all var(--transition-fast);
}

.agent-primary-btn {
  border: 0.5px solid var(--accent-border);
  background: var(--accent);
  color: var(--bg-base);
}

.agent-secondary-btn {
  border: 0.5px solid var(--border-default);
  color: var(--text-secondary);
}

.agent-delete-btn {
  border: 0.5px solid var(--error-border);
  background: var(--error-bg);
  color: var(--accent-rose);
}

.agent-primary-btn:disabled,
.agent-secondary-btn:disabled,
.agent-delete-btn:disabled {
  cursor: not-allowed;
  opacity: 0.5;
}
</style>
