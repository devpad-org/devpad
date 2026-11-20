import type { Router } from 'vue-router'

const RELOADED_AT_KEY = 'devpad:stale-build-reload'
// A chunk still failing this soon after a reload is not a stale build, so the
// page doesn't reload again.
const RELOAD_GUARD_MS = 10_000

// A deploy gives every chunk a new hashed name and the server drops the old
// ones, so a page loaded before it fails to load any view it had not fetched
// yet. When a navigation fails that way, this loads the new build at the page
// it was headed for. A chunk failing outside a navigation (a background
// prefetch, say) never reloads the page the user is on.
export function reloadOnStaleBuild(router: Router) {
  let navigatingTo: string | null = null
  router.beforeEach((to) => {
    navigatingTo = to.fullPath
  })
  router.afterEach(() => {
    navigatingTo = null
  })
  router.onError(() => {
    navigatingTo = null
  })

  window.addEventListener('vite:preloadError', () => {
    if (navigatingTo === null) return
    try {
      const last = Number(sessionStorage.getItem(RELOADED_AT_KEY)) || 0
      if (Date.now() - last < RELOAD_GUARD_MS) return
      sessionStorage.setItem(RELOADED_AT_KEY, String(Date.now()))
    } catch {
      // Without storage a reload can't be kept from repeating, so the error
      // surfaces as it would have.
      return
    }
    // The error still reaches the router, so if the user stays (an unsaved
    // file's prompt), the navigation fails as before.
    window.location.assign(router.resolve(navigatingTo).href)
  })
}
