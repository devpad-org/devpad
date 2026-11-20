<script setup lang="ts">
import DocLink from '@/components/DocLink.vue'
import type { DocTopic } from '@/utils/docs'
import type { CardTone } from './card'

// One card of the project overview: a titled box with an icon, a "?" into
// the docs, a status pill on the right, a body and a row of actions.

defineProps<{
  title: string
  doc?: DocTopic
  docLabel?: string
  // Shown as a pill in the header.
  status?: string
  tone?: CardTone
  // Shown in place of the icon when the card is a step in a sequence.
  step?: number
}>()
</script>

<template>
  <section class="pcard" :class="tone ? `pcard-${tone}` : ''">
    <header class="pcard-head">
      <span v-if="step" class="pcard-step" aria-hidden="true">{{ step }}</span>
      <span v-else class="pcard-icon" aria-hidden="true"><slot name="icon" /></span>
      <h3 class="pcard-title">{{ title }}</h3>
      <DocLink v-if="doc" :topic="doc" :label="docLabel ?? title" />
      <span v-if="status" class="pcard-status" :class="`tone-${tone ?? 'neutral'}`">
        <span class="pcard-dot" aria-hidden="true" />
        <span class="pcard-status-text">{{ status }}</span>
      </span>
    </header>
    <div class="pcard-body">
      <slot />
    </div>
    <footer v-if="$slots.actions" class="pcard-actions">
      <slot name="actions" />
    </footer>
  </section>
</template>

<style scoped>
.pcard {
  display: flex;
  flex-direction: column;
  min-width: 0;
  background: var(--bg-base);
  border: 0.5px solid var(--border-subtle);
  border-radius: var(--radius-lg);
}

.pcard-head {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  min-height: 44px;
  padding: var(--space-2) var(--space-4);
  border-bottom: 0.5px solid var(--border-hairline);
}

.pcard-icon {
  display: inline-flex;
  color: var(--text-tertiary);
  line-height: 0;
}

.pcard-step {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 20px;
  height: 20px;
  flex-shrink: 0;
  border-radius: 50%;
  background: var(--accent-glow);
  border: 0.5px solid var(--accent-border);
  color: var(--accent);
  font-size: 0.7rem;
  font-weight: 600;
}

.pcard-title {
  font-size: 0.85rem;
  font-weight: 600;
  color: var(--text-primary);
  white-space: nowrap;
}

.pcard-status {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  min-width: 0;
  margin-left: auto;
  padding: 2px var(--space-2);
  border-radius: 999px;
  border: 0.5px solid var(--border-subtle);
  background: var(--bg-neutral);
  color: var(--text-secondary);
  font-size: 0.72rem;
  font-weight: 500;
}

.pcard-status-text {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.pcard-dot {
  width: 6px;
  height: 6px;
  flex-shrink: 0;
  border-radius: 50%;
  background: currentColor;
}

.tone-ok {
  color: var(--success);
  background: var(--success-bg);
  border-color: var(--success-border);
}

.tone-warn {
  color: var(--warning);
  background: var(--warning-bg);
  border-color: var(--warning-border);
}

.tone-error {
  color: var(--error);
  background: var(--error-bg);
  border-color: var(--error-border);
}

.tone-busy {
  color: var(--hue-blue);
  background: color-mix(in srgb, var(--hue-blue) 12%, transparent);
  border-color: color-mix(in srgb, var(--hue-blue) 30%, transparent);
}

.tone-busy .pcard-dot {
  animation: pcard-pulse 1.4s ease-in-out infinite;
}

@keyframes pcard-pulse {
  50% { opacity: 0.3; }
}

@media (prefers-reduced-motion: reduce) {
  .tone-busy .pcard-dot {
    animation: none;
  }
}

.pcard-body {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
  flex: 1;
  padding: var(--space-3) var(--space-4);
  font-size: 0.8rem;
  color: var(--text-secondary);
  line-height: 1.45;
  overflow-wrap: anywhere;
}

.pcard-actions {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-2);
  padding: var(--space-2) var(--space-4) var(--space-3);
}
</style>
