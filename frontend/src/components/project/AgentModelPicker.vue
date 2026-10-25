<script setup lang="ts">
import { computed, onMounted, watch } from 'vue'
import { useAiModelStore } from '@/stores/aiModels'
import { defaultAIModel, defaultThinkingEffort, thinkingEffortLabel } from '@/utils/aiModels'

// Picks the model and reasoning effort agents run with.
const model = defineModel<string>('model', { required: true })
const effort = defineModel<string>('effort', { default: '' })

const store = useAiModelStore()
const current = computed(() => store.models.find((m) => m.id === model.value) ?? null)
const efforts = computed(() => current.value?.thinking.supportedEfforts ?? [])

function applyDefaults() {
  if (!current.value) {
    const fallback = defaultAIModel(store.models)
    if (fallback) model.value = fallback.id
  }
  if (current.value && efforts.value.length > 0 && !efforts.value.includes(effort.value)) {
    effort.value = defaultThinkingEffort(current.value)
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
      <select v-model="model" class="picker-select" aria-label="Model">
        <option v-for="m in store.models" :key="m.id" :value="m.id">{{ m.name }}</option>
      </select>
      <select v-if="efforts.length > 0" v-model="effort" class="picker-select" aria-label="Reasoning effort">
        <option v-for="e in efforts" :key="e" :value="e">{{ thinkingEffortLabel(e) }}</option>
      </select>
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
  padding: var(--space-1) var(--space-2);
  background: var(--bg-base);
  border: 0.5px solid var(--border-default);
  border-radius: var(--radius-md);
  color: var(--text-primary);
  font-family: inherit;
  font-size: 0.8rem;
  max-width: 220px;
}

.picker-select:focus {
  outline: none;
  border-color: var(--accent-blue);
}

.picker-empty {
  color: var(--text-muted);
  font-size: 0.8rem;
}
</style>
