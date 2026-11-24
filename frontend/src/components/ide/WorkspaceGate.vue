<script setup lang="ts">
// Stands in for a whole workspace page while the project workspace is
// missing, stopped or on its way up, with what the page needs it for and the
// button that gets it there.
export type WorkspaceGateState = 'none' | 'stopped' | 'creating' | 'starting' | 'not-ready' | 'error'

defineProps<{
  state: WorkspaceGateState
  // What the page needs the workspace for, finishing "Start it to …".
  purpose: string
  // The gate is the whole page, rather than the surface beside a project
  // list that works without the workspace, so it says what else does.
  fullPage?: boolean
  error?: string
  defaultBranch?: string
}>()

const emit = defineEmits<{
  create: []
  start: []
}>()
</script>

<template>
  <div class="workspace-gate">
    <div class="gate-body">
      <div class="gate-icon" :class="`tone-${state === 'error' ? 'error' : state === 'not-ready' ? 'neutral' : 'accent'}`" aria-hidden="true">
        <div v-if="state === 'creating' || state === 'starting'" class="gate-spinner" />
        <svg v-else-if="state === 'stopped'" width="22" height="22" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round">
          <path d="M12 2v10" />
          <path d="M18.4 6.6a9 9 0 1 1-12.77.04" />
        </svg>
        <svg v-else-if="state === 'none'" width="22" height="22" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round">
          <path d="m7 11 2-2-2-2" />
          <path d="M11 13h4" />
          <rect width="18" height="18" x="3" y="3" rx="2" />
        </svg>
        <svg v-else-if="state === 'not-ready'" width="22" height="22" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round">
          <circle cx="12" cy="12" r="10" />
          <path d="M12 6v6l4 2" />
        </svg>
        <svg v-else width="22" height="22" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round">
          <path d="m21.73 18-8-14a2 2 0 0 0-3.48 0l-8 14A2 2 0 0 0 4 21h16a2 2 0 0 0 1.73-3" />
          <path d="M12 9v4" />
          <path d="M12 17h.01" />
        </svg>
      </div>

      <div class="gate-copy" role="status">
        <template v-if="state === 'creating'">
          <p class="gate-title">Creating your workspace…</p>
          <p class="gate-text">
            Devpad is building the container and checking out {{ defaultBranch || 'the default branch' }}.
          </p>
        </template>
        <template v-else-if="state === 'starting'">
          <p class="gate-title">Starting your workspace…</p>
          <p class="gate-text">This page opens as soon as it's running.</p>
        </template>
        <template v-else-if="state === 'not-ready'">
          <p class="gate-title">The project repository is still being set up</p>
          <p class="gate-text">You can create your workspace once it's ready.</p>
        </template>
        <template v-else-if="state === 'none'">
          <p class="gate-title">This project has no workspace yet</p>
          <p class="gate-text">
            Your workspace is where you and your assistant work on the code. It starts from
            {{ defaultBranch || 'the default branch' }} and can switch to any project branch.
          </p>
        </template>
        <template v-else-if="state === 'stopped'">
          <p class="gate-title">Your workspace is stopped</p>
          <p class="gate-text">Start it to {{ purpose }}.</p>
        </template>
        <template v-else>
          <p class="gate-title">The workspace is unavailable</p>
        </template>
      </div>

      <button v-if="state === 'none'" class="gate-btn" @click="emit('create')">
        <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
          <path d="M5 12h14" />
          <path d="M12 5v14" />
        </svg>
        Create workspace
      </button>
      <button v-else-if="state === 'stopped'" class="gate-btn" @click="emit('start')">
        <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
          <path d="M6 4.5v15a1 1 0 0 0 1.5.86l12-7.5a1 1 0 0 0 0-1.72l-12-7.5A1 1 0 0 0 6 4.5Z" />
        </svg>
        Start workspace
      </button>

      <p v-if="error" class="gate-error" role="alert">{{ error }}</p>

      <p v-if="fullPage && (state === 'none' || state === 'stopped')" class="gate-note">
        Tasks, merge requests, pipelines and agents work without it.
      </p>
    </div>
  </div>
</template>

<style scoped>
.workspace-gate {
  display: flex;
  align-items: center;
  justify-content: center;
  height: 100%;
  overflow-y: auto;
  padding: var(--space-6) var(--space-4);
  background: var(--ide-surface-bg);
}

.gate-body {
  display: flex;
  flex-direction: column;
  align-items: center;
  max-width: 400px;
  text-align: center;
}

.gate-icon {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 48px;
  height: 48px;
  margin-bottom: var(--space-4);
  border-radius: var(--radius-xl);
  border: 0.5px solid;
}

.gate-icon.tone-accent {
  background: var(--accent-glow);
  border-color: var(--accent-border);
  color: var(--accent);
}

.gate-icon.tone-neutral {
  background: var(--bg-raised);
  border-color: var(--border-subtle);
  color: var(--text-secondary);
}

.gate-icon.tone-error {
  background: var(--error-bg);
  border-color: var(--error-border);
  color: var(--error);
}

.gate-copy {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
}

.gate-title {
  font-size: 0.95rem;
  font-weight: 600;
  color: var(--text-primary);
}

.gate-text {
  font-size: 0.82rem;
  line-height: 1.5;
  color: var(--text-secondary);
}

.gate-btn {
  display: inline-flex;
  align-items: center;
  gap: var(--space-2);
  margin-top: var(--space-5);
  padding: var(--space-2) var(--space-4);
  background: var(--accent);
  color: var(--text-on-accent);
  border-radius: var(--radius-md);
  font-size: 0.82rem;
  font-weight: 500;
  transition: background var(--transition-fast);
}

.gate-btn:hover {
  background: color-mix(in srgb, var(--accent) 85%, var(--fill-hover-mix));
}

.gate-error {
  margin-top: var(--space-3);
  font-size: 0.78rem;
  color: var(--error);
  overflow-wrap: anywhere;
}

.gate-note {
  margin-top: var(--space-6);
  font-size: 0.75rem;
  color: var(--text-tertiary);
}

.gate-spinner {
  width: 20px;
  height: 20px;
  border: 2px solid var(--spinner-track);
  border-top-color: var(--accent);
  border-radius: 50%;
  animation: spin 0.8s linear infinite;
}
</style>
