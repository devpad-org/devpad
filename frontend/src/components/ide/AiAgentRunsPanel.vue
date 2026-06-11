<script setup lang="ts">
import { computed, onMounted, onUnmounted, watch } from 'vue'
import { type AgentRun, useAgentRunStore, isAgentRunActiveStatus } from '@/stores/agentRuns'

const treeStepPx = 18
const treeNodeSizePx = 14

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
  hasChildren: boolean
  ancestorContinuations: boolean[]
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
  const appendRun = (run: AgentRun, depth: number, isLastChild: boolean, ancestorContinuations: boolean[]) => {
    const runChildren = children.get(run.id) ?? []
    items.push({ run, depth, isLastChild, hasChildren: runChildren.length > 0, ancestorContinuations })
    runChildren.forEach((child, index) => {
      const childIsLast = index === runChildren.length - 1
      appendRun(child, depth + 1, childIsLast, [...ancestorContinuations, !childIsLast])
    })
  }

  roots.forEach((run, index) => {
    appendRun(run, 0, index === roots.length - 1, [])
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

async function cancelRun(run: AgentRun): Promise<void> {
  if (!isAgentRunActiveStatus(run.status)) return

  await runStore.cancelRun(run.id)
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

function runItemStyle(item: RunListItem): Record<string, string> {
  return {
    '--run-depth': String(item.depth),
    '--run-tree-width': `${treeNodeSizePx + item.depth * treeStepPx}px`,
    '--run-node-offset': `${item.depth * treeStepPx}px`,
  }
}

function treeLaneStyle(lane: number): Record<string, string> {
  return {
    '--lane-offset': `${lane * treeStepPx}px`,
  }
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
      <div
        v-for="item in runListItems"
        :key="item.run.id"
        class="agent-run-item"
        :class="[
          `agent-run-item--${item.run.status}`,
          {
            selected: item.run.id === selectedRunId,
            active: isAgentRunActiveStatus(item.run.status),
            child: item.depth > 0,
            parent: item.hasChildren,
            'has-error': Boolean(item.run.error),
            'last-child': item.isLastChild,
          },
        ]"
        :style="runItemStyle(item)"
        role="button"
        tabindex="0"
        @click="focusRun(item.run)"
        @keydown.enter.prevent="focusRun(item.run)"
        @keydown.space.prevent="focusRun(item.run)"
      >
        <span class="run-tree" aria-hidden="true">
          <span
            v-for="(continues, lane) in item.ancestorContinuations"
            :key="lane"
            class="tree-lane"
            :class="{
              continues,
              branch: lane === item.depth - 1,
              'last-branch': lane === item.depth - 1 && item.isLastChild,
            }"
            :style="treeLaneStyle(lane)"
          />
          <span class="run-node">
            <span class="run-status-dot" />
          </span>
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
        <span class="run-actions">
          <span class="run-status">{{ formatStatus(item.run.status) }}</span>
          <button
            v-if="isAgentRunActiveStatus(item.run.status)"
            type="button"
            class="run-stop-button"
            :disabled="runStore.isRunCancelling(item.run.id)"
            :aria-label="`Cancel ${formatRunTitle(item.run)}`"
            title="Cancel run"
            @click.stop="cancelRun(item.run)"
            @keydown.stop
          >
            <svg aria-hidden="true" width="10" height="10" viewBox="0 0 10 10" fill="currentColor">
              <rect x="1.5" y="1.5" width="7" height="7" rx="1.5" />
            </svg>
          </button>
        </span>
      </div>
    </div>
  </div>
</template>

<style scoped>
.agent-runs-panel {
  --ide-header-icon: var(--accent);
  --tree-step: 18px;
  --tree-node-size: 14px;
  --tree-node-center: calc(var(--tree-node-size) / 2);
  --tree-line: color-mix(in srgb, var(--accent-purple) 42%, var(--border-strong));
  --tree-line-muted: color-mix(in srgb, var(--text-muted) 26%, transparent);
  display: flex;
  flex-direction: column;
  height: 100%;
  min-width: 0;
  background: var(--ide-panel-bg);
}

.agent-runs-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-2);
  padding: 0 var(--space-3);
  border-bottom: 0.5px solid var(--border-default);
  background: var(--ide-header-bg);
  height: 38px;
  flex-shrink: 0;
}

.agent-runs-title {
  display: flex;
  align-items: center;
  gap: 6px;
  font-size: var(--ide-header-title-size);
  font-weight: 600;
  color: var(--text-primary);
}

.panel-icon {
  color: var(--ide-header-icon);
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
  gap: 0;
  min-height: 0;
  overflow-y: auto;
  padding: var(--space-2) var(--space-2) var(--space-3);
}

.agent-run-item {
  display: grid;
  grid-template-columns: var(--run-tree-width) minmax(0, 1fr) auto;
  align-items: stretch;
  gap: 10px;
  position: relative;
  width: 100%;
  min-height: 48px;
  padding: 6px 8px;
  border: 0.5px solid transparent;
  border-radius: var(--radius-lg);
  background: transparent;
  color: var(--text-secondary);
  cursor: pointer;
  text-align: left;
  transition:
    border-color var(--transition-fast),
    background var(--transition-fast),
    box-shadow var(--transition-fast);
}

.agent-run-item.has-error {
  min-height: 64px;
  padding-block: 8px;
}

.agent-run-item + .agent-run-item { margin-top: 1px; }

.agent-run-item:hover {
  border-color: var(--border-default);
  background: var(--bg-hover);
  color: var(--text-primary);
}

.agent-run-item:focus-visible {
  outline: 1px solid var(--accent-border);
  outline-offset: -1px;
}

.agent-run-item.selected {
  border-color: var(--accent-border);
  background: var(--bg-selected);
}

.agent-run-item.active .run-title { color: var(--text-primary); }

.run-tree {
  position: relative;
  align-self: stretch;
  width: var(--run-tree-width);
  min-height: 34px;
}

.tree-lane {
  position: absolute;
  top: -8px;
  bottom: -8px;
  left: calc(var(--lane-offset) + var(--tree-node-center));
  width: var(--tree-step);
  z-index: 0;
  pointer-events: none;
}

.tree-lane.continues::before,
.tree-lane.branch::before {
  content: '';
  position: absolute;
  left: 0;
  top: 0;
  bottom: 0;
  width: 1.5px;
  border-radius: 999px;
  background: var(--tree-line);
}

.tree-lane.branch.last-branch::before {
  bottom: 50%;
}

.tree-lane.branch::after {
  content: '';
  position: absolute;
  left: 0;
  top: 50%;
  width: var(--tree-step);
  height: 1.5px;
  border-radius: 999px;
  background: var(--tree-line);
}

.run-node {
  position: absolute;
  left: var(--run-node-offset);
  top: 50%;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: var(--tree-node-size);
  height: var(--tree-node-size);
  border: 0.5px solid color-mix(in srgb, var(--border-strong) 70%, transparent);
  border-radius: 50%;
  background: var(--bg-surface);
  box-shadow: 0 0 0 3px color-mix(in srgb, var(--bg-surface) 80%, transparent);
  z-index: 1;
  transform: translateY(-50%);
}

.agent-run-item.parent .run-tree::after {
  content: '';
  position: absolute;
  left: calc(var(--run-node-offset) + var(--tree-node-center));
  top: 50%;
  bottom: -8px;
  width: 1.5px;
  border-radius: 999px;
  background: var(--tree-line);
  z-index: 0;
  pointer-events: none;
}

.run-status-dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  background: var(--text-muted);
  box-shadow: 0 0 0 1px color-mix(in srgb, var(--bg-void) 70%, transparent);
}

.agent-run-item--queued .run-status-dot,
.agent-run-item--running .run-status-dot,
.agent-run-item--waiting_approval .run-status-dot,
.agent-run-item--waiting_user .run-status-dot {
  background: var(--accent-green);
  box-shadow:
    0 0 0 1px color-mix(in srgb, var(--bg-void) 70%, transparent),
    0 0 10px color-mix(in srgb, var(--accent-green) 72%, transparent);
}

.agent-run-item--completed .run-status-dot {
  background: var(--accent-blue);
}

.agent-run-item--failed .run-status-dot,
.agent-run-item--cancelled .run-status-dot {
  background: var(--accent-rose);
}

.run-main {
  display: flex;
  flex-direction: column;
  justify-content: center;
  min-width: 0;
}

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
  line-height: 1.25;
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
  line-height: 1.3;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.run-actions {
  align-self: center;
  display: flex;
  align-items: center;
  gap: 6px;
  justify-self: end;
  min-width: max-content;
}

.run-status {
  padding: 2px 7px;
  border: 0.5px solid var(--border-default);
  border-radius: 999px;
  background: color-mix(in srgb, var(--bg-void) 24%, transparent);
  color: var(--text-muted);
  font-size: 0.64rem;
  line-height: 1.4;
  text-transform: capitalize;
  white-space: nowrap;
}

.run-stop-button {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 22px;
  height: 22px;
  border: 0.5px solid var(--error-border);
  border-radius: var(--radius-md);
  background: var(--error-bg);
  color: var(--accent-rose);
  opacity: 0.72;
  transition:
    background var(--transition-fast),
    border-color var(--transition-fast),
    color var(--transition-fast),
    opacity var(--transition-fast);
}

.agent-run-item:hover .run-stop-button,
.agent-run-item:focus-within .run-stop-button,
.run-stop-button:focus-visible {
  opacity: 1;
}

.run-stop-button:hover:not(:disabled) {
  border-color: color-mix(in srgb, var(--accent-rose) 55%, var(--error-border));
  background: color-mix(in srgb, var(--error-bg) 72%, var(--accent-rose));
  color: var(--text-primary);
}

.run-stop-button:disabled {
  cursor: wait;
  opacity: 0.55;
}

.agent-run-item.active .run-status {
  border-color: var(--success-border);
  background: var(--success-bg);
  color: var(--accent-green);
}

.agent-run-item--completed .run-status {
  border-color: color-mix(in srgb, var(--accent-blue) 28%, var(--border-default));
  color: var(--accent-blue);
}

.agent-run-item--failed .run-status,
.agent-run-item--cancelled .run-status {
  border-color: var(--error-border);
  background: var(--error-bg);
  color: var(--accent-rose);
}
</style>
