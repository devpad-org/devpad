import { onScopeDispose, watch, type Ref } from 'vue'
import { ApiError } from '@/api/client'

// What a call of a poll says of the next: go on (nothing, or 'ok'), try
// again later after a failure ('failed'), or stop until polling turns on
// again or is asked for, after a failure that lasts ('stop').
export type PollOutcome = 'ok' | 'failed' | 'stop' | void

// The longest a failing poll waits between calls.
const MAX_BACKOFF_MS = 30_000

// pollFailure is what an error from the API means for a poll: the thing
// polled is gone or not the user's (404, 403, and 401 once signed out), so
// asking again won't help, or anything else, which may pass.
export function pollFailure(e: unknown): PollOutcome {
  return e instanceof ApiError && [401, 403, 404].includes(e.status) ? 'stop' : 'failed'
}

// usePolling calls fn every intervalMs while enabled is true and the page
// is visible, at once when it turns on, and again as the page comes back
// into view. After each failure in a row it waits twice as long, up to 30
// seconds, and it stops on a failure that lasts. It stops with the
// component or scope that called it. Calls never overlap: one asked for
// while another runs follows it.
export function usePolling(fn: () => Promise<PollOutcome> | PollOutcome, intervalMs: number, enabled: Ref<boolean>) {
  let timer: ReturnType<typeof setTimeout> | null = null
  let running = false
  // Asked for while a call ran, so another follows it at once.
  let again = false
  let stopped = false
  // Stopped by a failure that lasts, until asked again.
  let halted = false
  let failures = 0

  function clear() {
    if (timer !== null) {
      clearTimeout(timer)
      timer = null
    }
  }

  function schedule() {
    clear()
    if (stopped || halted || !enabled.value) return
    const delay = Math.min(MAX_BACKOFF_MS, intervalMs * 2 ** failures)
    timer = setTimeout(() => void tick(), delay)
  }

  async function tick() {
    clear()
    if (stopped || halted || !enabled.value) return
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
      const outcome = await fn()
      if (outcome === 'stop') halted = true
      failures = outcome === 'failed' ? Math.min(failures + 1, 10) : 0
    } catch {
      failures = Math.min(failures + 1, 10)
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

  // Calls fn now, outside the schedule, such as after an action, and
  // resumes polling a failure stopped.
  function poll() {
    halted = false
    failures = 0
    return tick()
  }

  function onVisibility() {
    if (document.visibilityState === 'visible' && enabled.value && timer === null && !running) void tick()
  }

  watch(
    enabled,
    (on) => {
      if (on) void poll()
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

  return { poll }
}
