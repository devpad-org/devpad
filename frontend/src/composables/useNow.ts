import { onScopeDispose, ref, watch, type Ref } from 'vue'

// useNow is the time, moved on every intervalMs while ticking is true, for
// showing how long something has been running.
export function useNow(ticking: Ref<boolean>, intervalMs = 1000) {
  const now = ref(Date.now())
  let timer: ReturnType<typeof setInterval> | null = null

  function stop() {
    if (timer !== null) {
      clearInterval(timer)
      timer = null
    }
  }

  watch(
    ticking,
    (on) => {
      stop()
      now.value = Date.now()
      if (on) timer = setInterval(() => (now.value = Date.now()), intervalMs)
    },
    { immediate: true },
  )
  onScopeDispose(stop)
  return now
}
