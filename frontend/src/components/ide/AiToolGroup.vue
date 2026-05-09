<script setup lang="ts">
import {
  hasToolResult,
  isToolError,
  toolFriendlyName,
  toolGroupState,
  toolGroupSubtitle,
  toolGroupTitle,
  toolResultMeta,
  toolStatusLabel,
  toolTargetLabel,
  type ToolGroupDisplay,
} from '@/components/ide/aiToolDisplay'

defineProps<{
  group: ToolGroupDisplay
}>()
</script>

<template>
  <details class="tool-group" :class="`tool-group--${toolGroupState(group)}`">
    <summary class="tool-group-summary">
      <span class="tool-group-icon">
        <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.25" stroke-linecap="round" stroke-linejoin="round">
          <path d="M14.7 6.3a1 1 0 0 0 0 1.4l1.6 1.6a1 1 0 0 0 1.4 0l3.77-3.77a6 6 0 0 1-7.94 7.94l-6.91 6.91a2.12 2.12 0 0 1-3-3l6.91-6.91a6 6 0 0 1 7.94-7.94l-3.76 3.76z" />
        </svg>
      </span>
      <span class="tool-group-copy">
        <span class="tool-group-title">{{ toolGroupTitle(group) }}</span>
        <span class="tool-group-subtitle">{{ toolGroupSubtitle(group) }}</span>
      </span>
      <span class="tool-group-status">
        <span v-if="toolGroupState(group) === 'running'" class="tool-spinner" />
        <span v-else-if="toolGroupState(group) === 'error'" class="tool-error">&#x2718;</span>
        <span v-else class="tool-done">&#x2714;</span>
      </span>
      <svg class="tool-group-chevron" width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.25" stroke-linecap="round" stroke-linejoin="round">
        <polyline points="6 9 12 15 18 9" />
      </svg>
    </summary>
    <div class="tool-group-details">
      <div
        v-for="tool in group.tools"
        :key="tool.toolCallId"
        class="tool-row"
        :class="{ 'tool-row--error': isToolError(tool), 'tool-row--running': !hasToolResult(tool) }"
      >
        <div class="tool-row-main">
          <span class="tool-row-name">{{ toolFriendlyName(tool.name) }}</span>
          <span class="tool-row-target">{{ toolTargetLabel(tool) }}</span>
        </div>
        <span class="tool-row-meta">{{ toolResultMeta(tool) }}</span>
        <span class="tool-row-status">{{ toolStatusLabel(tool) }}</span>
        <pre v-if="isToolError(tool)" class="tool-row-error">{{ tool.result }}</pre>
      </div>
    </div>
  </details>
</template>

<style scoped>
.tool-group {
  display: block;
  width: fit-content;
  min-width: 220px;
  max-width: min(100%, 560px);
  margin: 5px 0;
  overflow: hidden;
  border: 0.5px solid var(--border-subtle);
  border-radius: var(--radius-lg);
  background: var(--bg-neutral);
}

.tool-group:first-child {
  margin-top: 0;
}

.tool-group-summary {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  min-height: 30px;
  padding: 5px 8px;
  cursor: pointer;
  user-select: none;
  list-style: none;
  transition: background var(--transition-fast);
}

.tool-group-summary::-webkit-details-marker {
  display: none;
}

.tool-group-summary:hover {
  background: var(--bg-hover);
}

.tool-group-icon {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 22px;
  height: 22px;
  flex-shrink: 0;
  border: 0.5px solid var(--border-subtle);
  border-radius: var(--radius-md);
  background: var(--bg-hover);
  color: var(--accent);
}

.tool-group-copy {
  display: grid;
  min-width: 0;
  gap: 1px;
}

.tool-group-title {
  overflow: hidden;
  color: var(--text-primary);
  font-size: 0.74rem;
  font-weight: 600;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.tool-group-subtitle {
  overflow: hidden;
  color: var(--text-muted);
  font-family: var(--font-mono);
  font-size: 0.67rem;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.tool-group-status {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  margin-left: auto;
  min-width: 18px;
  color: var(--text-muted);
}

.tool-group-chevron {
  flex-shrink: 0;
  color: var(--text-muted);
  transition: transform var(--transition-fast), color var(--transition-fast);
}

.tool-group[open] .tool-group-chevron {
  transform: rotate(180deg);
  color: var(--text-secondary);
}

.tool-group--running {
  border-color: var(--accent-border);
  background: var(--accent-glow);
}

.tool-group--error {
  border-color: var(--error-border);
  background: var(--error-bg);
}

.tool-group-details {
  display: grid;
  gap: 1px;
  padding: 0 6px 6px;
}

.tool-row {
  display: grid;
  grid-template-columns: minmax(0, 1fr) auto auto;
  gap: var(--space-2);
  align-items: center;
  padding: 6px 7px;
  border-radius: var(--radius-md);
  background: color-mix(in srgb, var(--bg-base) 46%, transparent);
  color: var(--text-secondary);
}

.tool-row--running {
  background: color-mix(in srgb, var(--accent-glow) 46%, transparent);
}

.tool-row--error {
  background: var(--error-bg);
}

.tool-row-main {
  display: flex;
  align-items: baseline;
  min-width: 0;
  gap: var(--space-2);
}

.tool-row-name {
  flex-shrink: 0;
  color: var(--text-secondary);
  font-size: 0.68rem;
  font-weight: 600;
}

.tool-row-target {
  overflow: hidden;
  color: var(--text-primary);
  font-family: var(--font-mono);
  font-size: 0.7rem;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.tool-row-meta {
  color: var(--text-muted);
  font-size: 0.68rem;
  white-space: nowrap;
}

.tool-row-status {
  min-width: 42px;
  color: var(--text-tertiary);
  font-family: var(--font-mono);
  font-size: 0.66rem;
  text-align: right;
  text-transform: uppercase;
}

.tool-row--error .tool-row-status,
.tool-row--error .tool-row-meta {
  color: var(--accent-rose);
}

.tool-row--running .tool-row-status,
.tool-row--running .tool-row-meta {
  color: var(--accent);
}

.tool-row-error {
  grid-column: 1 / -1;
  max-height: 132px;
  margin: 1px 0 0;
  padding: 7px 8px;
  overflow: auto;
  border: 0.5px solid var(--error-border);
  border-radius: var(--radius-md);
  background: var(--bg-void);
  color: var(--accent-rose);
  font-family: var(--font-mono);
  font-size: 0.7rem;
  line-height: 1.45;
  white-space: pre-wrap;
}

.tool-done {
  font-size: 0.75rem;
  color: var(--accent-green);
}

.tool-error {
  font-size: 0.75rem;
  color: var(--accent-rose);
}

.tool-spinner {
  width: 10px;
  height: 10px;
  border: 1.5px solid var(--border-subtle);
  border-top-color: var(--accent);
  border-radius: 50%;
  animation: spin 0.8s linear infinite;
}

@keyframes spin {
  to {
    transform: rotate(360deg);
  }
}
</style>
