<script setup lang="ts">
defineProps<{
  content: string
}>()
</script>

<template>
  <section class="thinking-content" aria-label="Thinking">
    <div class="thinking-summary">
      <span class="thinking-title">Thinking</span>
    </div>
    <div class="thinking-text">{{ content }}</div>
  </section>
</template>

<style scoped>
.thinking-content {
  max-width: min(100%, 620px);
  margin: 0 0 var(--space-3);
  padding: var(--space-2) var(--space-3);
  border: 0.5px solid var(--border-hairline);
  border-left: 2px solid var(--border-strong);
  border-radius: var(--radius-md);
  background: color-mix(in srgb, var(--bg-raised) 34%, transparent);
  color: var(--text-muted);
}

.thinking-summary {
  display: inline-flex;
  align-items: center;
  gap: var(--space-1);
  margin-bottom: var(--space-1);
  color: var(--text-tertiary);
  user-select: none;
  opacity: 0.78;
}

.thinking-summary::before {
  content: '';
  width: 5px;
  height: 5px;
  border-radius: 50%;
  background: var(--text-dim);
  opacity: 0.75;
}

.thinking-title {
  font-family: var(--font-mono);
  font-size: 0.67rem;
  font-weight: 600;
  letter-spacing: 0.04em;
  text-transform: uppercase;
}

.thinking-text {
  max-height: min(38vh, 280px);
  overflow: auto;
  color: var(--text-muted);
  font-family: var(--font-mono);
  font-size: 0.72rem;
  white-space: pre-wrap;
  word-break: break-word;
  line-height: 1.55;
  opacity: 0.82;
}
</style>
