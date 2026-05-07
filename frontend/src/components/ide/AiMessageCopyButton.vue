<script setup lang="ts">
import { computed, onUnmounted, ref } from 'vue'
import { writeClipboardText } from '@/utils/clipboard'

const props = defineProps<{
  markdown: string
}>()

const copied = ref(false)
const failed = ref(false)
let feedbackTimer: number | null = null

const title = computed(() => {
  if (copied.value) return 'Copied Markdown'
  if (failed.value) return 'Copy failed'
  return 'Copy as Markdown'
})

onUnmounted(() => {
  if (feedbackTimer !== null) clearTimeout(feedbackTimer)
})

function clearFeedbackAfter(delay: number): void {
  if (feedbackTimer !== null) {
    clearTimeout(feedbackTimer)
  }
  feedbackTimer = window.setTimeout(() => {
    copied.value = false
    failed.value = false
    feedbackTimer = null
  }, delay)
}

async function copyMarkdown(): Promise<void> {
  if (!props.markdown) return

  copied.value = false
  failed.value = false

  try {
    await writeClipboardText(props.markdown)
    copied.value = true
    clearFeedbackAfter(2000)
  } catch (err) {
    console.error('Failed to copy chat message as Markdown:', err)
    failed.value = true
    clearFeedbackAfter(3000)
  }
}
</script>

<template>
  <button
    type="button"
    class="msg-copy-btn"
    :class="{ copied, failed }"
    :title="title"
    :aria-label="title"
    @click="copyMarkdown"
  >
    <svg
      v-if="copied"
      width="12"
      height="12"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      stroke-width="2.5"
      stroke-linecap="round"
      stroke-linejoin="round"
    >
      <polyline points="20 6 9 17 4 12" />
    </svg>
    <svg
      v-else-if="failed"
      width="12"
      height="12"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      stroke-width="2.5"
      stroke-linecap="round"
      stroke-linejoin="round"
    >
      <line x1="18" y1="6" x2="6" y2="18" />
      <line x1="6" y1="6" x2="18" y2="18" />
    </svg>
    <svg
      v-else
      width="12"
      height="12"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      stroke-width="2"
      stroke-linecap="round"
      stroke-linejoin="round"
    >
      <rect x="9" y="9" width="13" height="13" rx="2" ry="2" />
      <path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1" />
    </svg>
  </button>
</template>
