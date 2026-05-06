<script setup lang="ts">
import { computed, onMounted, onUnmounted, watch } from 'vue'
import { type AgentRun, useAgentRunStore, isAgentRunActiveStatus } from '@/stores/agentRuns'

const props = defineProps<{
  workspaceId: number
  selectedRunId: number | null
}>()

const emit = defineEmits<{
  (e: 'focus', run: AgentRun): void
}>()

const runStore = useAgentRunStore()
let refreshTimer: number | null = null

const workspaceRuns = computed(() =>
  runStore.runs.filter((run) => run.workspaceId === props.workspaceId),
)

const activeCount = computed(() =>
  workspaceRuns.value.filter((run) => isAgentRunActiveStatus(run.status)).length,
)

async function refreshRuns(): Promise<void> {
  await runStore.fetchRuns(props.workspaceId)
}

function startRefreshTimer(): void {
  stopRefreshTimer()
  if (props.workspaceId <= 0) return

  void refreshRuns()
  refreshTimer = window.setInterval(() => {
    void refreshRuns()
  }, 5000)
}

function stopRefreshTimer(): void {
  if (refreshTimer === null) return
  window.clearInterval(refreshTimer)
  refreshTimer = null
}

function focusRun(run: AgentRun): void {
  emit('focus', run)
}

function formatStatus(status: AgentRun['status']): string {
  return status.replace('_', ' ')
}

function formatRunTitle(run: AgentRun): string {
  const promptPreview = run.promptPreview?.trim()
  if (promptPreview) return promptPreview
  return `Run #${run.id}`
}

function formatRelativeTime(dateStr: string): string {
  const date = new Date(dateStr)
  const now = new Date()
  const diffMs = now.getTime() - date.getTime()
  const diffMin = Math.floor(diffMs / 60000)
  if (diffMin < 1) return 'just now'
  if (diffMin < 60) return `${diffMin}m ago`
  const diffHr = Math.floor(diffMin / 60)
  if (diffHr < 24) return `${diffHr}h ago`
  const diffDay = Math.floor(diffHr / 24)
  if (diffDay < 7) return `${diffDay}d ago`
  return date.toLocaleDateString()
}

watch(() => props.workspaceId, startRefreshTimer)

onMounted(startRefreshTimer)
onUnmounted(stopRefreshTimer)
</script>

<template>
  <div class="agent-runs-panel">
    <header class="agent-runs-header">
      <div>
        <h2 class="agent-runs-title">Agent Runs</h2>
        <p class="agent-runs-subtitle">
          <span v-if="activeCount > 0">{{ activeCount }} active</span>
          <span v-else>No active runs</span>
        </p>
      </div>
      <span v-if="activeCount > 0" class="active-indicator" aria-label="Active agent run" />
    </header>

    <div v-if="runStore.error" class="agent-runs-error">
      {{ runStore.error }}
    </div>

    <div v-if="runStore.loading && workspaceRuns.length === 0" class="agent-runs-empty">
      Loading runs…
    </div>
    <div v-else-if="workspaceRuns.length === 0" class="agent-runs-empty">
      No agent runs yet
    </div>
    <div v-else class="agent-runs-list">
      <button
        v-for="run in workspaceRuns"
        :key="run.id"
        type="button"
        class="agent-run-item"
        :class="[
          `agent-run-item--${run.status}`,
          {
            selected: run.id === selectedRunId,
            active: isAgentRunActiveStatus(run.status),
          },
        ]"
        @click="focusRun(run)"
      >
        <span class="run-status-dot" />
        <span class="run-main">
          <span class="run-title">{{ formatRunTitle(run) }}</span>
          <span class="run-meta">
            <span>{{ run.model }}</span>
            <span class="run-meta-sep">&middot;</span>
            <span>{{ formatRelativeTime(run.updatedAt) }}</span>
          </span>
          <span v-if="run.error" class="run-error">{{ run.error }}</span>
        </span>
        <span class="run-status">{{ formatStatus(run.status) }}</span>
      </button>
    </div>
  </div>
</template>

<style scoped>
.agent-runs-panel {
  display: flex;
  flex-direction: column;
  height: 100%;
  min-width: 0;
  background: var(--bg-surface);
}

.agent-runs-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-3);
  padding: var(--space-3);
  border-bottom: 0.5px solid var(--border-default);
  background: var(--bg-surface);
}

.agent-runs-title {
  font-size: 0.78rem;
  line-height: 1.2;
  font-weight: 700;
  letter-spacing: 0.08em;
  color: var(--text-primary);
  text-transform: uppercase;
}

.agent-runs-subtitle { margin-top: 2px; color: var(--text-muted); font-size: 0.72rem; }

.active-indicator {
  width: 8px;
  height: 8px;
  flex-shrink: 0;
  border-radius: 50%;
  background: var(--accent-green);
  box-shadow: 0 0 10px var(--accent-green);
  animation: activePulse 1.3s ease-in-out infinite;
}

@keyframes activePulse {
  0%, 100% { opacity: 0.55; transform: scale(0.9); }
  50% { opacity: 1; transform: scale(1.15); }
}

.agent-runs-error {
  margin: var(--space-3);
  padding: var(--space-2);
  border: 0.5px solid var(--error-border);
  border-radius: var(--radius-md);
  background: var(--error-bg);
  color: var(--accent-rose);
  font-size: 0.75rem;
}

.agent-runs-empty { padding: var(--space-4); color: var(--text-muted); font-size: 0.78rem; }

.agent-runs-list {
  display: flex;
  flex-direction: column;
  gap: var(--space-1);
  min-height: 0;
  overflow-y: auto;
  padding: var(--space-2);
}

.agent-run-item {
  display: grid;
  grid-template-columns: auto minmax(0, 1fr) auto;
  align-items: start;
  gap: var(--space-2);
  width: 100%;
  padding: var(--space-2);
  border: 0.5px solid transparent;
  border-radius: var(--radius-md);
  color: var(--text-secondary);
  text-align: left;
  transition: all var(--transition-fast);
}

.agent-run-item:hover {
  border-color: var(--border-default);
  background: var(--bg-hover);
  color: var(--text-primary);
}

.agent-run-item.selected {
  border-color: var(--accent-border);
  background: var(--bg-selected);
}

.agent-run-item.active .run-title { color: var(--text-primary); }

.run-status-dot {
  width: 7px;
  height: 7px;
  border-radius: 50%;
  margin-top: 5px;
  background: var(--text-muted);
}

.agent-run-item--queued .run-status-dot,
.agent-run-item--running .run-status-dot,
.agent-run-item--waiting_approval .run-status-dot {
  background: var(--accent-green);
  box-shadow: 0 0 8px var(--accent-green);
}

.agent-run-item--completed .run-status-dot { background: var(--accent-blue); }

.agent-run-item--failed .run-status-dot,
.agent-run-item--cancelled .run-status-dot {
  background: var(--accent-rose);
}

.run-main { display: flex; flex-direction: column; min-width: 0; }

.run-title {
  overflow: hidden;
  font-size: 0.8rem;
  line-height: 1.25;
  font-weight: 600;
  color: var(--text-secondary);
  text-overflow: ellipsis;
  white-space: nowrap;
}

.run-meta {
  display: flex;
  align-items: center;
  gap: 5px;
  min-width: 0;
  margin-top: 2px;
  color: var(--text-muted);
  font-size: 0.7rem;
}

.run-meta span:first-child {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.run-meta-sep { color: var(--text-dim); }

.run-error {
  overflow: hidden;
  margin-top: 3px;
  color: var(--accent-rose);
  font-size: 0.7rem;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.run-status {
  align-self: start;
  padding: 1px 6px;
  border: 0.5px solid var(--border-default);
  border-radius: 999px;
  color: var(--text-muted);
  font-size: 0.64rem;
  line-height: 1.4;
  text-transform: capitalize;
  white-space: nowrap;
}

.agent-run-item.active .run-status {
  border-color: var(--success-border);
  background: var(--success-bg);
  color: var(--accent-green);
}
</style>
