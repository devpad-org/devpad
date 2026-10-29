<script setup lang="ts">
import { computed, onMounted, watch } from 'vue'
import SelectMenu, { type SelectMenuOption } from '@/components/SelectMenu.vue'
import { useAiModelStore } from '@/stores/aiModels'
import { CODING_AGENT_ID, defaultAIModel, defaultThinkingEffort, thinkingEffortLabel } from '@/utils/aiModels'

// Picks the model and reasoning effort agents run with, starting on the
// defaults an admin set for the agent.
const props = withDefaults(defineProps<{ agentId?: string }>(), { agentId: CODING_AGENT_ID })
const model = defineModel<string>('model', { required: true })
const effort = defineModel<string>('effort', { default: '' })

const store = useAiModelStore()
const current = computed(() => store.models.find((m) => m.id === model.value) ?? null)
const efforts = computed(() => current.value?.thinking.supportedEfforts ?? [])
const agentDefaults = computed(() => store.agentDefaults[props.agentId])

const modelOptions = computed<SelectMenuOption[]>(() =>
  store.models.map((m) => ({ value: m.id, label: m.name, description: m.providerName })),
)
const effortOptions = computed<SelectMenuOption[]>(() =>
  efforts.value.map((e) => ({ value: e, label: thinkingEffortLabel(e) })),
)

function applyDefaults() {
  if (!current.value) {
    const fallback = defaultAIModel(store.models, agentDefaults.value)
    if (fallback) model.value = fallback.id
  }
  if (current.value && efforts.value.length > 0 && !efforts.value.includes(effort.value)) {
    effort.value = defaultThinkingEffort(current.value, agentDefaults.value)
  } else if (efforts.value.length === 0) {
    effort.value = ''
  }
}

watch(() => [store.models, model.value], applyDefaults)

onMounted(async () => {
  if (!store.loaded) await store.fetchModels()
  applyDefaults()
})
</script>

<template>
  <div class="agent-picker">
    <span v-if="store.loaded && store.models.length === 0" class="picker-empty">
      No AI models configured. Add a provider key in Settings.
    </span>
    <template v-else>
      <SelectMenu v-model="model" class="picker-select" :options="modelOptions" title="Model" />
      <SelectMenu
        v-if="efforts.length > 0"
        v-model="effort"
        class="picker-select"
        :options="effortOptions"
        title="Reasoning effort"
        prefix="Effort"
      />
    </template>
  </div>
</template>

<style scoped>
.agent-picker {
  display: inline-flex;
  align-items: center;
  gap: var(--space-2);
  flex-wrap: wrap;
}

.picker-select {
  max-width: 220px;
}

.picker-empty {
  color: var(--text-muted);
  font-size: 0.8rem;
}
</style>
