<script setup lang="ts">
defineProps<{
  content: string
}>()
</script>

<template>
  <details class="thinking-content">
    <summary class="thinking-summary">
      <svg class="thinking-chevron" width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.25" stroke-linecap="round" stroke-linejoin="round">
        <polyline points="6 9 12 15 18 9" />
      </svg>
      <span class="thinking-title">Thinking</span>
    </summary>
    <div class="thinking-text">{{ content }}</div>
  </details>
</template>

<style scoped>
.thinking-content {
  max-width: min(100%, 620px);
  margin: 1px 0 var(--space-2);
  color: var(--text-tertiary);
}

.thinking-summary {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  padding: 2px 0;
  color: var(--text-muted);
  cursor: pointer;
  list-style: none;
  user-select: none;
  opacity: 0.74;
  transition: color var(--transition-fast), opacity var(--transition-fast);
}

.thinking-summary:hover {
  color: var(--text-secondary);
  opacity: 1;
}

.thinking-summary::-webkit-details-marker {
  display: none;
}

.thinking-title {
  font-family: var(--font-mono);
  font-size: 0.68rem;
  font-weight: 500;
  letter-spacing: 0.01em;
}

.thinking-chevron {
  flex-shrink: 0;
  color: currentColor;
  transition: transform var(--transition-fast), color var(--transition-fast);
}

.thinking-content[open] .thinking-chevron {
  transform: rotate(180deg);
  color: var(--text-secondary);
}

.thinking-text {
  max-height: 180px;
  margin: 2px 0 0 5px;
  overflow: auto;
  padding: 4px 0 4px var(--space-3);
  border-left: 0.5px solid var(--border-subtle);
  color: var(--text-muted);
  font-family: var(--font-mono);
  font-size: 0.7rem;
  white-space: pre-wrap;
  word-break: break-word;
  line-height: 1.5;
  opacity: 0.84;
}
</style>
