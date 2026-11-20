import { computed, ref } from 'vue'

// How many views a navigation is still waiting to download.
const waiting = ref(0)

// Whether a navigation is waiting on a view's code, for the progress bar.
export const viewLoading = computed(() => waiting.value > 0)

export interface LazyView<T> {
  // For the route's `component`: a navigation waiting on it counts as loading.
  load: () => Promise<T>
  // Downloads the view in the background, so a later navigation needn't wait.
  prefetch: () => void
}

// Splits a view into its own chunk. A prefetch and the router share one
// download; a failed one is tried again next time.
export function lazyView<T>(importer: () => Promise<T>): LazyView<T> {
  let download: Promise<T> | null = null
  let loaded = false

  function fetchView(): Promise<T> {
    download ??= importer().then(
      (view) => {
        loaded = true
        return view
      },
      (err: unknown) => {
        download = null
        throw err
      },
    )
    return download
  }

  return {
    load() {
      if (loaded) return fetchView()
      waiting.value++
      return fetchView().finally(() => waiting.value--)
    },
    prefetch() {
      fetchView().catch(() => {})
    },
  }
}

// Prefetches once the browser is idle, so it never competes with the page
// that is loading now.
export function prefetchWhenIdle(view: LazyView<unknown>) {
  if ('requestIdleCallback' in window) window.requestIdleCallback(() => view.prefetch(), { timeout: 3000 })
  else setTimeout(() => view.prefetch(), 1000)
}
