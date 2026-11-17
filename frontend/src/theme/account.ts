// The theme is a preference of the user's account, so it follows them to
// other browsers; each browser also remembers the last one it showed, so it
// can paint in it before anyone has signed in.

import { settingsApi } from '@/api/settings'
import { currentTheme, isThemeId, setTheme, type ThemeId } from '.'

/** Switches to the signed-in user's theme, if this browser was showing another. */
export async function syncThemeFromAccount() {
  try {
    const { theme } = await settingsApi.getPreferences()
    if (isThemeId(theme) && theme !== currentTheme.value) setTheme(theme)
  } catch {
    // The browser's remembered theme stays.
  }
}

/** Switches theme now and saves it to the user's account; on failure, switches back and throws. */
export async function chooseTheme(id: ThemeId) {
  const previous = currentTheme.value
  setTheme(id)
  try {
    await settingsApi.updatePreferences({ theme: id })
  } catch (e) {
    // Unless the user has already picked another since.
    if (currentTheme.value === id) setTheme(previous)
    throw e
  }
}
