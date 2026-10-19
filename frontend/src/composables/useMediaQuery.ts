import { ref, onMounted, onUnmounted } from 'vue'

// Layout breakpoint below which the UI switches to its single-column mobile layout.
export const MOBILE_MEDIA_QUERY = '(max-width: 768px)'

export function isMobileViewport(): boolean {
  return typeof window !== 'undefined' && window.matchMedia(MOBILE_MEDIA_QUERY).matches
}

export function useMediaQuery(query: string) {
  const mql = window.matchMedia(query)
  const matches = ref(mql.matches)

  function onChange(e: MediaQueryListEvent) {
    matches.value = e.matches
  }

  onMounted(() => {
    matches.value = mql.matches
    mql.addEventListener('change', onChange)
  })

  onUnmounted(() => {
    mql.removeEventListener('change', onChange)
  })

  return matches
}

export function useIsMobile() {
  return useMediaQuery(MOBILE_MEDIA_QUERY)
}
