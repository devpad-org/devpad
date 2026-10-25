import { defineStore } from 'pinia'
import { computed, ref } from 'vue'
import { aiApi, type AttentionItem } from '@/api/ai'

const POLL_INTERVAL_MS = 5000

// A stable key for an item, across polls.
export function attentionItemKey(item: AttentionItem): string {
  return item.approval?.id ?? item.question?.id ?? `run-${item.run.id}`
}

// Agent runs, in any workspace, that are waiting for the user. Polled while
// anything is watching, so the header can show a count.
export const useAttentionStore = defineStore('attention', () => {
  const items = ref<AttentionItem[]>([])
  const loaded = ref(false)
  const count = computed(() => items.value.length)
  let timer: ReturnType<typeof setInterval> | null = null
  let watchers = 0

  async function refresh() {
    try {
      const response = await aiApi.listAttention()
      items.value = response.items ?? []
      loaded.value = true
    } catch {
      // keep the last known items
    }
  }

  function watch() {
    watchers++
    if (timer === null) {
      void refresh()
      timer = setInterval(refresh, POLL_INTERVAL_MS)
    }
  }

  function unwatch() {
    watchers = Math.max(0, watchers - 1)
    if (watchers === 0 && timer !== null) {
      clearInterval(timer)
      timer = null
    }
  }

  return { items, loaded, count, refresh, watch, unwatch }
})
