import { ref } from 'vue'
import { settingsApi, type UserPreferences } from '@/api/settings'

const defaultPreferences: UserPreferences = {
  diffViewSideBySide: true,
}

export function useUserPreferences() {
  const preferences = ref<UserPreferences>({ ...defaultPreferences })
  const loading = ref(false)
  const saving = ref(false)
  const error = ref('')

  async function loadPreferences() {
    loading.value = true
    error.value = ''
    try {
      preferences.value = await settingsApi.getPreferences()
    } catch (e) {
      error.value = e instanceof Error ? e.message : 'Failed to load preferences'
    } finally {
      loading.value = false
    }
  }

  async function setDiffViewSideBySide(sideBySide: boolean) {
    const previous = preferences.value
    preferences.value = { ...preferences.value, diffViewSideBySide: sideBySide }
    saving.value = true
    error.value = ''
    try {
      preferences.value = await settingsApi.updatePreferences(preferences.value)
    } catch (e) {
      preferences.value = previous
      error.value = e instanceof Error ? e.message : 'Failed to save preferences'
    } finally {
      saving.value = false
    }
  }

  return {
    preferences,
    loading,
    saving,
    error,
    loadPreferences,
    setDiffViewSideBySide,
  }
}
