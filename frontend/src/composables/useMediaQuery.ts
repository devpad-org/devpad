import { ref, onMounted, onUnmounted } from 'vue'

// Below this the UI switches to its single-column mobile layout: narrow screens,
// plus phones in landscape, which are wide but too short for side panels.
// Page-level CSS media queries repeat the 768px width; keep them in sync.
export const MOBILE_MEDIA_QUERY = '(max-width: 768px), (pointer: coarse) and (max-height: 500px)'

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
