<script setup lang="ts">
import { computed } from 'vue'

const props = defineProps<{ status: string }>()

const TONES: Record<string, string> = {
  ready: 'success',
  running: 'success',
  merged: 'success',
  approve: 'success',
  submitted: 'success',
  in_progress: 'active',
  queued: 'active',
  creating: 'active',
  pending: 'active',
  open: 'active',
  in_review: 'review',
  comment: 'review',
  changes_requested: 'attention',
  request_changes: 'attention',
  failed: 'error',
  error: 'error',
}

const tone = computed(() => TONES[props.status] ?? 'neutral')
const LABELS: Record<string, string> = {
  approve: 'approved',
  comment: 'commented',
  request_changes: 'changes requested',
}

const label = computed(() => LABELS[props.status] ?? props.status.replace(/_/g, ' '))
</script>

<template>
  <span class="status-badge" :class="`tone-${tone}`">{{ label }}</span>
</template>

<style scoped>
.status-badge {
  display: inline-flex;
  align-items: center;
  padding: 1px var(--space-2);
  border-radius: var(--radius-sm);
  font-size: 0.7rem;
  font-weight: 500;
  white-space: nowrap;
  border: 0.5px solid var(--border-default);
  color: var(--text-secondary);
  background: var(--bg-neutral);
}

.tone-success {
  color: var(--success);
  background: var(--success-bg);
  border-color: var(--success-border);
}

.tone-active {
  color: var(--accent-blue);
  background: color-mix(in srgb, var(--accent-blue) 12%, transparent);
  border-color: color-mix(in srgb, var(--accent-blue) 30%, transparent);
}

.tone-review {
  color: var(--accent-purple);
  background: color-mix(in srgb, var(--accent-purple) 12%, transparent);
  border-color: color-mix(in srgb, var(--accent-purple) 30%, transparent);
}

.tone-attention {
  color: var(--attention);
  background: var(--attention-bg);
  border-color: var(--attention-border);
}

.tone-error {
  color: var(--error);
  background: var(--error-bg);
  border-color: var(--error-border);
}
</style>
