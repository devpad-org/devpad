import { ref } from 'vue'
import { settingsApi, type UserPreferences } from '@/api/settings'

// The theme is a preference too, but it is in use the moment it is chosen,
// so it lives in @/theme (currentTheme, chooseTheme) and not here.
type Preferences = Omit<UserPreferences, 'theme'>

const defaultPreferences: Preferences = {
  diffViewSideBySide: true,
}

function fromResponse(response: UserPreferences): Preferences {
  return { diffViewSideBySide: response.diffViewSideBySide }
}

export function useUserPreferences() {
  const preferences = ref<Preferences>({ ...defaultPreferences })
  const loading = ref(false)
  const saving = ref(false)
  const error = ref('')

  async function loadPreferences() {
    loading.value = true
    error.value = ''
    try {
      preferences.value = fromResponse(await settingsApi.getPreferences())
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
      preferences.value = fromResponse(await settingsApi.updatePreferences({ diffViewSideBySide: sideBySide }))
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
