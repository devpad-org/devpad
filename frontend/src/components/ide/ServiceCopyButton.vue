<script setup lang="ts">
import { computed, onUnmounted, ref } from 'vue'
import { writeClipboardText } from '@/utils/clipboard'

const props = defineProps<{
  value: string | number
  label: string
}>()

const copied = ref(false)
const failed = ref(false)
let feedbackTimer: number | null = null

const title = computed(() => {
  if (copied.value) return `Copied ${props.label}`
  if (failed.value) return `Failed to copy ${props.label}`
  return `Copy ${props.label}`
})

onUnmounted(() => {
  if (feedbackTimer !== null) clearTimeout(feedbackTimer)
})

function clearFeedbackAfter(delay: number): void {
  if (feedbackTimer !== null) clearTimeout(feedbackTimer)

  feedbackTimer = window.setTimeout(() => {
    copied.value = false
    failed.value = false
    feedbackTimer = null
  }, delay)
}

async function copyValue(): Promise<void> {
  copied.value = false
  failed.value = false

  try {
    await writeClipboardText(String(props.value))
    copied.value = true
    clearFeedbackAfter(2000)
  } catch (error) {
    console.error(`Failed to copy ${props.label}:`, error)
    failed.value = true
    clearFeedbackAfter(3000)
  }
}
</script>

<template>
  <button
    type="button"
    class="service-copy-btn"
    :class="{ copied, failed }"
    :title="title"
    :aria-label="title"
    @click="copyValue"
  >
    <svg v-if="copied" width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round">
      <polyline points="20 6 9 17 4 12" />
    </svg>
    <svg v-else-if="failed" width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round">
      <line x1="18" y1="6" x2="6" y2="18" />
      <line x1="6" y1="6" x2="18" y2="18" />
    </svg>
    <svg v-else width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
      <rect x="9" y="9" width="13" height="13" rx="2" ry="2" />
      <path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1" />
    </svg>
  </button>
</template>

<style scoped>
.service-copy-btn {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 24px;
  height: 24px;
  flex-shrink: 0;
  border: 0.5px solid var(--border-subtle);
  border-radius: var(--radius-md);
  background: color-mix(in srgb, var(--bg-raised) 82%, transparent);
  color: var(--text-muted);
  opacity: 0;
  transition: opacity var(--transition-fast), border-color var(--transition-fast), background var(--transition-fast), color var(--transition-fast);
}

.service-copy-btn:hover,
.service-copy-btn:focus-visible {
  border-color: var(--border-strong);
  background: var(--bg-hover);
  color: var(--text-primary);
  opacity: 1;
}

.service-copy-btn.copied {
  border-color: var(--success-border);
  background: var(--success-bg);
  color: var(--accent-green);
  opacity: 1;
}

.service-copy-btn.failed {
  border-color: var(--error-border);
  background: var(--error-bg);
  color: var(--accent-rose);
  opacity: 1;
}
</style>

