import { ref, onMounted } from 'vue'
import { aiApi, type AIModel } from '@/api/ai'

// Every model, with whether the user has a key for its provider, for the
// settings pages that list models the user can't use yet too. The models
// store holds only those with a key. Each page loads them when it opens, so
// a key saved on another page shows up.
export function useAllAIModels() {
  const models = ref<AIModel[]>([])
  const loading = ref(true)
  const error = ref('')

  onMounted(async () => {
    try {
      models.value = (await aiApi.listModels()).models
    } catch {
      error.value = 'Failed to load AI models'
    } finally {
      loading.value = false
    }
  })

  return { models, loading, error }
}
