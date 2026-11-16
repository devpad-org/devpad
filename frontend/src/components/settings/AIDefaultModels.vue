<script setup lang="ts">
import { ref, onMounted } from 'vue'
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
const summaryModel = ref('')
const loading = ref(true)
const error = ref('')
const success = ref('')
// The agent ID being saved, or 'summary'.
const submitting = ref<string | null>(null)

onMounted(async () => {
  try {
    const [defaults, summary] = await Promise.all([aiApi.listAgentDefaults(), aiApi.getSummaryModel()])
    agents.value = defaults.agents
    for (const a of defaults.agents) forms.value[a.agentId] = { model: a.model, effort: a.effort }
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

function setAgentModel(agentId: string, model: string) {
  const form = forms.value[agentId]
  if (!form) return
  form.model = model
  if (!effortOptions(agentId).includes(form.effort)) form.effort = ''
}

// A chosen model the user has no key for, which runs skip until they add one.
function missingKey(model: string): boolean {
  const found = model ? findModel(model) : undefined
  return !!found && !found.configured
}

async function save(id: string, action: () => Promise<string>) {
  error.value = ''
  success.value = ''
  submitting.value = id
  try {
    success.value = await action()
    // Pickers in this browser read the defaults from the models store.
    void aiModelStore.fetchModels()
  } catch (e) {
    error.value = e instanceof Error ? e.message : 'Failed to save the default model'
  } finally {
    submitting.value = null
  }
}

function saveAgent(agentId: string) {
  const form = forms.value[agentId]
  if (!form) return
  void save(agentId, async () => {
    const res = await aiApi.setAgentDefaults(agentId, form.model, form.model ? form.effort : '')
    const index = agents.value.findIndex((a) => a.agentId === agentId)
    if (index !== -1) agents.value[index] = res.agent
    forms.value[agentId] = { model: res.agent.model, effort: res.agent.effort }
    return `Defaults for the ${res.agent.name} saved`
  })
}

function saveSummary() {
  void save('summary', async () => {
    summaryModel.value = (await aiApi.setSummaryModel(summaryModel.value)).model
    return 'Summary model saved'
  })
}
</script>

<template>
  <div>
    <header class="page-header">
      <h2 class="page-title">Default Models</h2>
      <p class="page-desc">
        The model and reasoning effort each built-in agent runs with unless whoever starts it picks another, including
        agents started by other agents.
      </p>
    </header>

    <div v-if="error" class="alert alert-error">{{ error }}</div>
    <div v-if="success" class="alert alert-success">{{ success }}</div>

    <div v-if="loading" class="loading-state">Loading agents...</div>

    <div v-else class="card-list">
      <div v-for="agent in agents" :key="agent.agentId" class="card">
        <div class="card-header">
          <span class="card-name">{{ agent.name }}</span>
          <span v-if="agent.model" class="badge active">Custom</span>
          <span v-else class="badge">Devpad default</span>
        </div>
        <p v-if="agent.purpose" class="card-desc">{{ agent.purpose }}</p>
        <form v-if="forms[agent.agentId]" @submit.prevent="saveAgent(agent.agentId)">
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
                v-model="forms[agent.agentId].effort"
                :options="effortMenu(agent.agentId)"
                title="Reasoning effort"
                variant="field"
              />
            </div>
          </div>
          <div class="card-actions">
            <span v-if="missingKey(forms[agent.agentId].model)" class="card-hint">
              You have no key for this model's provider, so the default is skipped until you add one.
            </span>
            <div class="card-buttons">
              <button type="submit" class="btn-primary" :disabled="submitting === agent.agentId">
                {{ submitting === agent.agentId ? 'Saving...' : 'Save' }}
              </button>
            </div>
          </div>
        </form>
      </div>

      <div class="card">
        <div class="card-header">
          <span class="card-name">File summaries</span>
          <span v-if="summaryModel" class="badge active">Custom</span>
          <span v-else class="badge">Automatic</span>
        </div>
        <p class="card-desc">
          The model agents' summarize_file tool uses to read a file for them. Its cost counts toward the budget of the run
          that asked for it. Automatic picks the cheapest priced model you have a key for.
        </p>
        <form @submit.prevent="saveSummary">
          <div class="form-field">
            <label for="summary-model">Model</label>
            <SelectMenu
              id="summary-model"
              v-model="summaryModel"
              :options="modelMenu(summaryModel, 'Automatic (cheapest)')"
              title="Model"
              variant="field"
            />
          </div>
          <div class="card-actions">
            <span v-if="missingKey(summaryModel)" class="card-hint">
              You have no key for this model's provider, so summaries use the cheapest model you have a key for.
            </span>
            <div class="card-buttons">
              <button type="submit" class="btn-primary" :disabled="submitting === 'summary'">
                {{ submitting === 'summary' ? 'Saving...' : 'Save' }}
              </button>
            </div>
          </div>
        </form>
      </div>
    </div>
  </div>
</template>

<style scoped src="./settings.css"></style>
