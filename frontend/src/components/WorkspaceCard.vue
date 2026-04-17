<script setup lang="ts">
import { ref } from 'vue'
import type { Workspace } from '@/api/workspaces'

const props = defineProps<{
  workspace: Workspace
}>()

const emit = defineEmits<{
  edit: [workspace: Workspace]
  delete: [workspace: Workspace]
  open: [workspace: Workspace]
  start: [workspace: Workspace]
  stop: [workspace: Workspace]
}>()

const toggling = ref(false)

async function togglePower() {
  if (toggling.value) return
  toggling.value = true
  try {
    if (props.workspace.status === 'running') {
      emit('stop', props.workspace)
    } else {
      emit('start', props.workspace)
    }
  } finally {
    toggling.value = false
  }
}

function statusColor(status: string) {
  switch (status) {
    case 'running':
      return 'status-running'
    case 'creating':
      return 'status-creating'
    default:
      return 'status-stopped'
  }
}

function statusLabel(status: string) {
  return status.charAt(0).toUpperCase() + status.slice(1)
}

function formatDate(dateStr: string) {
  return new Date(dateStr).toLocaleDateString(undefined, {
    month: 'short',
    day: 'numeric',
    year: 'numeric',
  })
}
</script>

<template>
  <div class="workspace-card">
    <div class="card-header">
      <div class="card-title-row">
        <h3 class="card-name">{{ workspace.name }}</h3>
        <span class="status-badge" :class="statusColor(workspace.status)">
          <span class="status-dot" />
          {{ statusLabel(workspace.status) }}
        </span>
      </div>
      <p v-if="workspace.description" class="card-desc">{{ workspace.description }}</p>
    </div>
    <div class="card-footer">
      <span class="card-date">Created {{ formatDate(workspace.createdAt) }}</span>
      <div class="card-actions">
        <!-- Primary CTA -->
        <button
          v-if="workspace.status === 'running'"
          class="btn-primary-action btn-open"
          @click="$emit('open', workspace)"
        >
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
            <path d="M18 13v6a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V8a2 2 0 0 1 2-2h6" />
            <polyline points="15 3 21 3 21 9" />
            <line x1="10" x2="21" y1="14" y2="3" />
          </svg>
          Open IDE
        </button>
        <button
          v-else-if="workspace.status === 'stopped'"
          class="btn-primary-action btn-start"
          :disabled="toggling"
          @click="togglePower"
        >
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
            <path d="M18.36 6.64a9 9 0 1 1-12.73 0" />
            <line x1="12" x2="12" y1="2" y2="12" />
          </svg>
          Start
        </button>
        <span v-else class="creating-label">
          <svg class="spinner" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
            <path d="M21 12a9 9 0 1 1-6.219-8.56" />
          </svg>
          Creating…
        </span>

        <!-- Secondary actions -->
        <div class="action-divider" />
        <button
          v-if="workspace.status === 'running'"
          class="action-btn action-power power-on"
          title="Stop"
          :disabled="toggling"
          @click="togglePower"
        >
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
            <path d="M18.36 6.64a9 9 0 1 1-12.73 0" />
            <line x1="12" x2="12" y1="2" y2="12" />
          </svg>
        </button>
        <button class="action-btn" title="Edit" @click="$emit('edit', workspace)">
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
            <path d="M17 3a2.85 2.83 0 1 1 4 4L7.5 20.5 2 22l1.5-5.5Z" />
            <path d="m15 5 4 4" />
          </svg>
        </button>
        <button class="action-btn action-danger" title="Delete" @click="$emit('delete', workspace)">
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
            <path d="M3 6h18" />
            <path d="M19 6v14c0 1-1 2-2 2H7c-1 0-2-1-2-2V6" />
            <path d="M8 6V4c0-1 1-2 2-2h4c1 0 2 1 2 2v2" />
          </svg>
        </button>
      </div>
    </div>
  </div>
</template>

<style scoped>
.workspace-card {
  background: var(--bg-surface);
  border: 1px solid var(--border-default);
  border-radius: var(--radius-lg);
  padding: var(--space-4) var(--space-5);
  transition: border-color var(--transition-fast);
}

.workspace-card:hover {
  border-color: var(--border-active);
}

.card-header {
  margin-bottom: var(--space-4);
}

.card-title-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-3);
  margin-bottom: var(--space-1);
}

.card-name {
  font-size: 0.95rem;
  font-weight: 600;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.card-desc {
  color: var(--text-secondary);
  font-size: 0.8rem;
  line-height: 1.4;
  display: -webkit-box;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
  overflow: hidden;
}

.status-badge {
  display: inline-flex;
  align-items: center;
  gap: var(--space-1);
  padding: 2px var(--space-2);
  border-radius: var(--radius-sm);
  font-size: 0.7rem;
  font-weight: 500;
  white-space: nowrap;
  flex-shrink: 0;
}

.status-dot {
  width: 6px;
  height: 6px;
  border-radius: 50%;
}

.status-running {
  background: rgba(16, 185, 129, 0.1);
  color: var(--accent-green);
}

.status-running .status-dot {
  background: var(--accent-green);
  box-shadow: 0 0 4px var(--accent-green);
}

.status-creating {
  background: rgba(245, 158, 11, 0.1);
  color: var(--accent-amber);
}

.status-creating .status-dot {
  background: var(--accent-amber);
  box-shadow: 0 0 4px var(--accent-amber);
}

.status-stopped {
  background: rgba(113, 113, 122, 0.1);
  color: var(--text-muted);
}

.status-stopped .status-dot {
  background: var(--text-muted);
}

.card-footer {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding-top: var(--space-3);
  border-top: 1px solid var(--border-default);
}

.card-date {
  color: var(--text-muted);
  font-size: 0.75rem;
}

.card-actions {
  display: flex;
  align-items: center;
  gap: var(--space-1);
}

/* Primary CTA buttons */
.btn-primary-action {
  display: inline-flex;
  align-items: center;
  gap: var(--space-1);
  padding: var(--space-1) var(--space-3);
  font-size: 0.75rem;
  font-weight: 600;
  border-radius: var(--radius-md);
  transition: all var(--transition-fast);
  letter-spacing: 0.01em;
}

.btn-open {
  background: var(--accent-blue);
  color: var(--bg-primary);
  box-shadow: 0 0 12px rgba(0, 212, 255, 0.25);
}

.btn-open:hover {
  background: color-mix(in srgb, var(--accent-blue) 85%, white);
  box-shadow: 0 0 20px rgba(0, 212, 255, 0.4);
}

.btn-start {
  background: rgba(16, 185, 129, 0.12);
  color: var(--accent-green);
  border: 1px solid rgba(16, 185, 129, 0.25);
}

.btn-start:hover {
  background: rgba(16, 185, 129, 0.2);
  border-color: rgba(16, 185, 129, 0.4);
}

.btn-start:disabled {
  opacity: 0.4;
  cursor: not-allowed;
}

.creating-label {
  display: inline-flex;
  align-items: center;
  gap: var(--space-1);
  padding: var(--space-1) var(--space-3);
  font-size: 0.75rem;
  font-weight: 500;
  color: var(--accent-amber);
}

.spinner {
  animation: spin 1s linear infinite;
}

@keyframes spin {
  to { transform: rotate(360deg); }
}

/* Divider between primary and secondary actions */
.action-divider {
  width: 1px;
  height: 16px;
  background: var(--border-default);
  margin: 0 var(--space-1);
}

.action-btn {
  padding: var(--space-1);
  color: var(--text-muted);
  border-radius: var(--radius-sm);
  transition: all var(--transition-fast);
}

.action-btn:hover {
  color: var(--text-primary);
  background: var(--bg-hover);
}

.action-power:disabled {
  opacity: 0.3;
  cursor: not-allowed;
}

.action-power.power-on:hover {
  color: var(--accent-amber);
}

.action-danger:hover {
  color: var(--accent-rose);
}
</style>
