import { onScopeDispose, watch, type Ref } from 'vue'

// usePolling calls fn every intervalMs while enabled is true and the page
// is visible, once at once when it turns on, and again as the page comes
// back into view. It stops with the component or scope that called it.
// Calls never overlap: one asked for while another runs follows it.
export function usePolling(fn: () => Promise<void> | void, intervalMs: number, enabled: Ref<boolean>) {
  let timer: ReturnType<typeof setTimeout> | null = null
  let running = false
  // Asked for while a call ran, so another follows it at once.
  let again = false
  let stopped = false

  function clear() {
    if (timer !== null) {
      clearTimeout(timer)
      timer = null
    }
  }

  function schedule() {
    clear()
    if (stopped || !enabled.value) return
    timer = setTimeout(() => void tick(), intervalMs)
  }

  async function tick() {
    clear()
    if (stopped || !enabled.value) return
    if (document.visibilityState !== 'visible') {
      // Visibility coming back calls again.
      return
    }
    if (running) {
      again = true
      return
    }
    running = true
    try {
      await fn()
    } finally {
      running = false
      if (again) {
        again = false
        void tick()
      } else {
        schedule()
      }
    }
  }

  function onVisibility() {
    if (document.visibilityState === 'visible' && enabled.value && timer === null && !running) void tick()
  }

  watch(
    enabled,
    (on) => {
      if (on) void tick()
      else clear()
    },
    { immediate: true },
  )
  document.addEventListener('visibilitychange', onVisibility)

  onScopeDispose(() => {
    stopped = true
    clear()
    document.removeEventListener('visibilitychange', onVisibility)
  })

  // Calls fn now, outside the schedule, such as after an action.
  return { poll: tick }
}
