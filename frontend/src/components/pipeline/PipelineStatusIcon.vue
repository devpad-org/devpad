<script setup lang="ts">
import MrIcon from '@/components/project/MrIcon.vue'
import type { StatusLook } from './pipelineFormat'

// A pipeline's or a job's status as a small round icon in its tone, with
// its label for screen readers and as a tooltip.
withDefaults(defineProps<{ look: StatusLook; size?: number; title?: string }>(), { size: 18, title: '' })
</script>

<template>
  <span
    class="status-icon"
    :class="`tone-${look.tone}`"
    :style="{ width: `${size}px`, height: `${size}px` }"
    role="img"
    :aria-label="title || look.label"
    :title="title || look.label"
  >
    <span v-if="look.spin" class="status-spin" aria-hidden="true" />
    <MrIcon v-else :name="look.icon" :size="Math.round(size * 0.6)" :stroke-width="2.4" />
  </span>
</template>

<style scoped>
.status-icon {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  flex-shrink: 0;
  border-radius: 50%;
  border: 0.5px solid var(--border-subtle);
  background: var(--bg-neutral);
  color: var(--text-tertiary);
}

.tone-success {
  color: var(--success);
  background: var(--success-bg);
  border-color: var(--success-border);
}

.tone-error {
  color: var(--error);
  background: var(--error-bg);
  border-color: var(--error-border);
}

.tone-warning {
  color: var(--warning);
  background: var(--warning-bg);
  border-color: var(--warning-border);
}

.tone-active {
  color: var(--hue-blue);
  background: color-mix(in srgb, var(--hue-blue) 12%, transparent);
  border-color: color-mix(in srgb, var(--hue-blue) 30%, transparent);
}

.status-spin {
  width: 60%;
  height: 60%;
  border-radius: 50%;
  border: 1.5px solid var(--spinner-track);
  border-top-color: currentColor;
  animation: spin 0.9s linear infinite;
}

@media (prefers-reduced-motion: reduce) {
  .status-spin {
    animation-duration: 2.4s;
  }
}
</style>
