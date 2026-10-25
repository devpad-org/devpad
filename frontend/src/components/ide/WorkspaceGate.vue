<script setup lang="ts">
// Stands in for a workspace panel while the project workspace is missing,
// stopped or on its way up.
export type WorkspaceGateState = 'none' | 'stopped' | 'creating' | 'starting' | 'not-ready' | 'error'

defineProps<{
  state: WorkspaceGateState
  error?: string
  defaultBranch?: string
  compact?: boolean
}>()

const emit = defineEmits<{
  create: []
  start: []
}>()
</script>

<template>
  <div class="workspace-gate" :class="{ compact }">
    <template v-if="state === 'creating' || state === 'starting'">
      <div class="gate-spinner" />
      <p class="gate-title">{{ state === 'creating' ? 'Creating your workspace…' : 'Starting your workspace…' }}</p>
      <p v-if="!compact && state === 'creating'" class="gate-text">
        Devpad is building the container and checking out {{ defaultBranch || 'the default branch' }}.
      </p>
    </template>
    <template v-else-if="state === 'not-ready'">
      <p class="gate-title">The project repository is still being set up.</p>
      <p v-if="!compact" class="gate-text">You can create a workspace once it's ready.</p>
    </template>
    <template v-else-if="state === 'none'">
      <p class="gate-title">This project has no workspace yet.</p>
      <p v-if="!compact" class="gate-text">
        Your workspace is where you and your assistant work on the code. It starts from
        {{ defaultBranch || 'the default branch' }} and can switch to any project branch.
      </p>
      <button class="gate-btn" @click="emit('create')">Create workspace</button>
    </template>
    <template v-else-if="state === 'stopped'">
      <p class="gate-title">Your workspace is stopped.</p>
      <button class="gate-btn" @click="emit('start')">Start workspace</button>
    </template>
    <template v-else>
      <p class="gate-title">The workspace is unavailable.</p>
    </template>
    <p v-if="error" class="gate-error">{{ error }}</p>
  </div>
</template>

<style scoped>
.workspace-gate {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: var(--space-3);
  height: 100%;
  padding: var(--space-6) var(--space-4);
  text-align: center;
  background: var(--ide-surface-bg);
}

.workspace-gate.compact {
  justify-content: flex-start;
  gap: var(--space-2);
  padding: var(--space-6) var(--space-3);
  background: var(--ide-panel-bg);
}

.gate-title {
  font-size: 0.85rem;
  font-weight: 500;
  color: var(--text-primary);
}

.compact .gate-title {
  font-size: 0.78rem;
}

.gate-text {
  max-width: 420px;
  font-size: 0.8rem;
  color: var(--text-secondary);
}

.gate-btn {
  padding: var(--space-2) var(--space-4);
  background: var(--accent-blue);
  color: var(--bg-primary);
  border-radius: var(--radius-md);
  font-size: 0.8rem;
  font-weight: 500;
  transition: background var(--transition-fast);
}

.gate-btn:hover {
  background: color-mix(in srgb, var(--accent-blue) 85%, var(--gruvbox-fg0));
}

.gate-error {
  max-width: 420px;
  font-size: 0.78rem;
  color: var(--accent-rose);
  overflow-wrap: anywhere;
}

.gate-spinner {
  width: 20px;
  height: 20px;
  border: 2px solid var(--border-default);
  border-top-color: var(--accent-blue);
  border-radius: 50%;
  animation: spin 0.8s linear infinite;
}
</style>
