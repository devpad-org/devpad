<script setup lang="ts">
import DocLink from '@/components/DocLink.vue'
import { ref, onMounted, onUnmounted } from 'vue'
import { aiApi, type AIModel, type BuiltInAgentDefaults } from '@/api/ai'
import { useAiModelStore } from '@/stores/aiModels'
import { thinkingEffortLabel } from '@/utils/aiModels'
import SelectMenu, { type SelectMenuOption } from '@/components/SelectMenu.vue'
import { useAllAIModels } from './useAllAIModels'

// Every model, with whether the user has a key for its provider.
const { models } = useAllAIModels()

const aiModelStore = useAiModelStore()

const agents = ref<BuiltInAgentDefaults[]>([])
const forms = ref<Record<string, { model: string; effort: string }>>({})
// The saved summary model, and the one the menu shows.
const savedSummary = ref('')
const summaryModel = ref('')
const loading = ref(true)
const error = ref('')

onMounted(async () => {
  try {
    const [defaults, summary] = await Promise.all([aiApi.listAgentDefaults(), aiApi.getSummaryModel()])
    agents.value = defaults.agents
    for (const a of defaults.agents) forms.value[a.agentId] = { model: a.model, effort: a.effort }
    savedSummary.value = summary.model
    summaryModel.value = summary.model
  } catch {
    error.value = 'Failed to load default models'
  } finally {
    loading.value = false
  }
})

function findModel(id: string): AIModel | undefined {
  return models.value.find((m) => m.id === id)
}

// Models the user has a key for, plus the one already chosen if they have
// since removed its key, so the menu can still show it.
function modelMenu(chosen: string, emptyLabel: string): SelectMenuOption[] {
  return [
    { value: '', label: emptyLabel },
    ...models.value
      .filter((m) => m.configured || m.id === chosen)
      .map((m) => ({
        value: m.id,
        label: m.name,
        description: m.configured ? m.providerName : `${m.providerName} · no key`,
      })),
  ]
}

function effortOptions(agentId: string): string[] {
  const model = forms.value[agentId]?.model
  return (model && findModel(model)?.thinking.supportedEfforts) || []
}

function effortMenu(agentId: string): SelectMenuOption[] {
  return [
    { value: '', label: 'Model default' },
    ...effortOptions(agentId).map((e) => ({ value: e, label: thinkingEffortLabel(e) })),
  ]
}

// A chosen model the user has no key for, which runs skip until they add one.
function missingKey(model: string): boolean {
  const found = model ? findModel(model) : undefined
  return !!found && !found.configured
}

// Each card (an agent ID, or 'summary') saves as soon as one of its menus
// changes. Its saves run one at a time, and a change made while one is in
// flight is sent when it returns, so the last choice is the one kept.
type SaveStatus = { state: 'saving' } | { state: 'saved' } | { state: 'error'; message: string }
const status = ref<Record<string, SaveStatus>>({})
const saves: Record<string, { running: boolean; again: boolean; clear?: ReturnType<typeof setTimeout> }> = {}

const SAVED_SHOWN_MS = 2000

function statusLabel(id: string): string {
  const state = status.value[id]?.state
  return state === 'saving' ? 'Saving...' : state === 'saved' ? 'Saved' : ''
}

function saveError(id: string): string {
  const s = status.value[id]
  return s?.state === 'error' ? s.message : ''
}

// save sends the card's current choice and records what the server kept;
// settle then puts that back in the card's menus, so a choice that failed
// to save is undone.
async function autosave(id: string, save: () => Promise<void>, settle: () => void) {
  const card = (saves[id] ??= { running: false, again: false })
  clearTimeout(card.clear)
  if (card.running) {
    card.again = true
    return
  }
  card.running = true
  status.value[id] = { state: 'saving' }
  let failure: unknown = null
  do {
    card.again = false
    try {
      await save()
      failure = null
    } catch (e) {
      failure = e
    }
  } while (card.again)
  card.running = false
  settle()
  if (failure) {
    status.value[id] = {
      state: 'error',
      message: `Not saved: ${failure instanceof Error ? failure.message : 'the server did not answer'}`,
    }
    return
  }
  status.value[id] = { state: 'saved' }
  card.clear = setTimeout(() => delete status.value[id], SAVED_SHOWN_MS)
  // Pickers in this browser read the defaults from the models store.
  void aiModelStore.fetchModels()
}

function saveAgent(agentId: string) {
  void autosave(
    agentId,
    async () => {
      const form = forms.value[agentId]
      if (!form) return
      const res = await aiApi.setAgentDefaults(agentId, form.model, form.model ? form.effort : '')
      const index = agents.value.findIndex((a) => a.agentId === agentId)
      if (index !== -1) agents.value[index] = res.agent
    },
    () => {
      const agent = agents.value.find((a) => a.agentId === agentId)
      if (agent) forms.value[agentId] = { model: agent.model, effort: agent.effort }
    },
  )
}

function setAgentModel(agentId: string, model: string) {
  const form = forms.value[agentId]
  if (!form) return
  form.model = model
  if (!effortOptions(agentId).includes(form.effort)) form.effort = ''
  saveAgent(agentId)
}

function setAgentEffort(agentId: string, effort: string) {
  const form = forms.value[agentId]
  if (!form) return
  form.effort = effort
  saveAgent(agentId)
}

function setSummaryModel(model: string) {
  summaryModel.value = model
  void autosave(
    'summary',
    async () => {
      savedSummary.value = (await aiApi.setSummaryModel(summaryModel.value)).model
    },
    () => {
      summaryModel.value = savedSummary.value
    },
  )
}

onUnmounted(() => {
  for (const card of Object.values(saves)) clearTimeout(card.clear)
})
</script>

<template>
  <div>
    <header class="page-header">
      <h2 class="page-title">Default Models<DocLink topic="aiModels" label="Default models" /></h2>
      <p class="page-desc">
        The model and reasoning effort each built-in agent runs with unless whoever starts it picks another, including
        agents started by other agents. Each choice is saved as soon as you make it.
      </p>
    </header>

    <div v-if="error" class="alert alert-error">{{ error }}</div>

    <div v-if="loading" class="loading-state">Loading agents...</div>

    <div v-else class="card-list">
      <div v-for="agent in agents" :key="agent.agentId" class="card">
        <div class="card-header">
          <span class="card-name">{{ agent.name }}</span>
          <span class="card-state">
            <span class="save-status" :class="status[agent.agentId]?.state" role="status">
              {{ statusLabel(agent.agentId) }}
            </span>
            <span v-if="agent.model" class="badge active">Custom</span>
            <span v-else class="badge">Devpad default</span>
          </span>
        </div>
        <p v-if="agent.purpose" class="card-desc">{{ agent.purpose }}</p>
        <template v-if="forms[agent.agentId]">
          <div class="card-fields">
            <div class="form-field">
              <label :for="'agent-model-' + agent.agentId">Model</label>
              <SelectMenu
                :id="'agent-model-' + agent.agentId"
                :model-value="forms[agent.agentId].model"
                :options="modelMenu(forms[agent.agentId].model, 'Devpad default')"
                title="Model"
                variant="field"
                @update:model-value="setAgentModel(agent.agentId, $event)"
              />
            </div>
            <div v-if="effortOptions(agent.agentId).length > 0" class="form-field">
              <label :for="'agent-effort-' + agent.agentId">Reasoning effort</label>
              <SelectMenu
                :id="'agent-effort-' + agent.agentId"
                :model-value="forms[agent.agentId].effort"
                :options="effortMenu(agent.agentId)"
                title="Reasoning effort"
                variant="field"
                @update:model-value="setAgentEffort(agent.agentId, $event)"
              />
            </div>
          </div>
          <p v-if="missingKey(forms[agent.agentId].model)" class="card-hint">
            You have no key for this model's provider, so the default is skipped until you add one.
          </p>
          <p v-if="saveError(agent.agentId)" class="save-error" role="alert">{{ saveError(agent.agentId) }}</p>
        </template>
      </div>

      <div class="card">
        <div class="card-header">
          <span class="card-name">File summaries</span>
          <span class="card-state">
            <span class="save-status" :class="status.summary?.state" role="status">{{ statusLabel('summary') }}</span>
            <span v-if="savedSummary" class="badge active">Custom</span>
            <span v-else class="badge">Automatic</span>
          </span>
        </div>
        <p class="card-desc">
          The model agents' summarize_file tool uses to read a file for them. Its cost counts toward the budget of the run
          that asked for it. Automatic picks the cheapest priced model you have a key for.
        </p>
        <div class="card-fields">
          <div class="form-field">
            <label for="summary-model">Model</label>
            <SelectMenu
              id="summary-model"
              :model-value="summaryModel"
              :options="modelMenu(summaryModel, 'Automatic (cheapest)')"
              title="Model"
              variant="field"
              @update:model-value="setSummaryModel"
            />
          </div>
        </div>
        <p v-if="missingKey(summaryModel)" class="card-hint">
          You have no key for this model's provider, so summaries use the cheapest model you have a key for.
        </p>
        <p v-if="saveError('summary')" class="save-error" role="alert">{{ saveError('summary') }}</p>
      </div>
    </div>
  </div>
</template>

<style scoped src="./settings.css"></style>
<style scoped>
/* With no Save button below them, the fields end the card. */
.card-fields {
  row-gap: var(--space-4);
}

.card-fields .form-field {
  margin-bottom: 0;
}

.card-state {
  display: inline-flex;
  align-items: center;
  gap: var(--space-3);
}

.save-status {
  font-size: 0.8rem;
  color: var(--text-tertiary);
}

.save-status.saved {
  color: var(--success);
}

.card-hint,
.save-error {
  margin: var(--space-3) 0 0;
  line-height: 1.5;
}

.save-error {
  font-size: 0.8rem;
  color: var(--error);
}
</style>
