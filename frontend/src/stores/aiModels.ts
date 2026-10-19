import { ref } from 'vue'
import { defineStore } from 'pinia'
import { aiApi, type AIModel } from '@/api/ai'

export { type AIModel }

export const useAiModelStore = defineStore('aiModels', () => {
  // Only configured models: those a new run can actually be started with.
  const models = ref<AIModel[]>([])
  const loaded = ref(false)

  async function fetchModels(): Promise<void> {
    try {
      const res = await aiApi.listModels()
      models.value = res.models.filter((m) => m.configured)
      loaded.value = true
    } catch {
      // models will remain empty
    }
  }

  // Reports whether a model recorded on a past run can still be used. Until the
  // list has loaded every model counts as available, so nothing is flagged early.
  function isModelAvailable(modelId: string): boolean {
    return !loaded.value || models.value.some((m) => m.id === modelId)
  }

  return {
    models,
    loaded,
    fetchModels,
    isModelAvailable,
  }
})
