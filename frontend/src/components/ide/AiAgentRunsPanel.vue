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

interface RunListItem {
  run: AgentRun
  depth: number
  isLastChild: boolean
}

const runStore = useAgentRunStore()
let refreshTimer: number | null = null

const workspaceRuns = computed(() =>
  runStore.runs.filter((run) => run.workspaceId === props.workspaceId),
)

const activeCount = computed(() =>
  workspaceRuns.value.filter((run) => isAgentRunActiveStatus(run.status)).length,
)

const runListItems = computed<RunListItem[]>(() => {
  const byId = new Map(workspaceRuns.value.map((run) => [run.id, run]))
  const children = new Map<number, AgentRun[]>()
  const roots: AgentRun[] = []

  for (const run of workspaceRuns.value) {
    if (run.parentRunId && byId.has(run.parentRunId)) {
      const siblings = children.get(run.parentRunId) ?? []
      siblings.push(run)
      children.set(run.parentRunId, siblings)
    } else {
      roots.push(run)
    }
  }

  const runActivityTime = (run: AgentRun): number => {
    let latest = runTime(run.updatedAt, run.createdAt)
    for (const child of children.get(run.id) ?? []) {
      latest = Math.max(latest, runActivityTime(child))
    }
    return latest
  }

  const compareRuns = (a: AgentRun, b: AgentRun) =>
    runActivityTime(b) - runActivityTime(a) || b.id - a.id

  roots.sort(compareRuns)
  for (const siblings of children.values()) {
    siblings.sort(compareRuns)
  }

  const items: RunListItem[] = []
  const appendRun = (run: AgentRun, depth: number, isLastChild: boolean) => {
    items.push({ run, depth, isLastChild })
    const runChildren = children.get(run.id) ?? []
    runChildren.forEach((child, index) => {
      appendRun(child, depth + 1, index === runChildren.length - 1)
    })
  }

  roots.forEach((run, index) => {
    appendRun(run, 0, index === roots.length - 1)
  })
  return items
})

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

function runTime(updatedAt: string, createdAt: string): number {
  const updated = new Date(updatedAt).getTime()
  if (Number.isFinite(updated)) return updated
  const created = new Date(createdAt).getTime()
  return Number.isFinite(created) ? created : 0
}

watch(() => props.workspaceId, startRefreshTimer)

watch(activeCount, (count, prevCount) => {
  if (count === 0) {
    stopRefreshTimer()
  } else if (prevCount === 0) {
    startRefreshTimer()
  }
})

onMounted(startRefreshTimer)
onUnmounted(stopRefreshTimer)
</script>

<template>
  <div class="agent-runs-panel">
    <header class="agent-runs-header">
      <span class="agent-runs-title">
        <svg class="panel-icon" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
          <path d="M21 16V8a2 2 0 0 0-1-1.73l-7-4a2 2 0 0 0-2 0l-7 4A2 2 0 0 0 3 8v8a2 2 0 0 0 1 1.73l7 4a2 2 0 0 0 2 0l7-4A2 2 0 0 0 21 16Z" />
          <path d="m3.3 7 8.7 5 8.7-5" />
          <path d="M12 22V12" />
        </svg>
        Agent Runs
      </span>
      <span v-if="activeCount > 0" class="agent-runs-badge" aria-label="Active agent runs">{{ activeCount }}</span>
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
        v-for="item in runListItems"
        :key="item.run.id"
        type="button"
        class="agent-run-item"
        :class="[
          `agent-run-item--${item.run.status}`,
          {
            selected: item.run.id === selectedRunId,
            active: isAgentRunActiveStatus(item.run.status),
            child: item.depth > 0,
            'last-child': item.isLastChild,
          },
        ]"
        :style="{ '--run-depth': item.depth }"
        @click="focusRun(item.run)"
      >
        <span class="run-tree" :class="{ child: item.depth > 0, 'last-child': item.isLastChild }" aria-hidden="true">
          <span class="run-status-dot" />
        </span>
        <span class="run-main">
          <span class="run-title">{{ formatRunTitle(item.run) }}</span>
          <span class="run-meta">
            <span>{{ item.run.model }}</span>
            <span class="run-meta-sep">&middot;</span>
            <span>{{ formatRelativeTime(item.run.updatedAt) }}</span>
          </span>
          <span v-if="item.run.error" class="run-error">{{ item.run.error }}</span>
        </span>
        <span class="run-status">{{ formatStatus(item.run.status) }}</span>
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
  gap: var(--space-2);
  padding: 0 var(--space-3);
  border-bottom: 0.5px solid var(--border-default);
  height: 38px;
  flex-shrink: 0;
}

.agent-runs-title {
  display: flex;
  align-items: center;
  gap: 6px;
  font-size: 0.75rem;
  font-weight: 600;
  text-transform: uppercase;
  letter-spacing: 0.05em;
  color: var(--text-secondary);
}

.panel-icon {
  color: var(--accent-purple);
  opacity: 0.7;
  flex-shrink: 0;
}

.agent-runs-badge {
  min-width: 18px;
  height: 18px;
  padding: 0 6px;
  flex-shrink: 0;
  border: 0.5px solid var(--success-border);
  border-radius: 999px;
  background: var(--success-bg);
  color: var(--accent-green);
  font-family: var(--font-mono);
  font-size: 0.68rem;
  line-height: 17px;
  text-align: center;
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
  position: relative;
  width: 100%;
  padding: var(--space-2);
  padding-left: calc(var(--space-2) + (var(--run-depth, 0) * 16px));
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

.run-tree {
  position: relative;
  display: flex;
  align-items: flex-start;
  justify-content: center;
  width: 10px;
  min-height: 18px;
}

.run-tree.child::before {
  content: '';
  position: absolute;
  left: -9px;
  top: -11px;
  bottom: 9px;
  width: 1px;
  background: var(--border-default);
}

.run-tree.child:not(.last-child)::before {
  bottom: -17px;
}

.run-tree.child::after {
  content: '';
  position: absolute;
  left: -9px;
  top: 8px;
  width: 9px;
  height: 1px;
  background: var(--border-default);
}

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
