<script setup lang="ts">
import { computed, onMounted, onUnmounted, watch } from 'vue'
import { type AgentRun, useAgentRunStore, isAgentRunActiveStatus } from '@/stores/agentRuns'
import { useAiModelStore } from '@/stores/aiModels'

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
const modelStore = useAiModelStore()
let refreshTimer: number | null = null

const workspaceRuns = computed(() =>
  runStore.runs.filter((run) => run.workspaceId === props.workspaceId),
)

const activeCount = computed(() =>
  workspaceRuns.value.filter((run) => isAgentRunActiveStatus(run.status)).length,
)

const waitingCount = computed(() =>
  workspaceRuns.value.filter((run) => isAgentRunWaitingStatus(run.status)).length,
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

function isAgentRunWaitingStatus(status: AgentRun['status']): boolean {
  return status === 'waiting_approval' || status === 'waiting_user'
}

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

const statusLabels: Record<AgentRun['status'], string> = {
  queued: 'Queued',
  running: 'Running',
  waiting_approval: 'Needs approval',
  waiting_user: 'Needs input',
  completed: 'Completed',
  failed: 'Failed',
  cancelled: 'Cancelled',
}

function formatStatus(status: AgentRun['status']): string {
  return statusLabels[status] ?? status
}

function formatModel(modelId: string): string {
  return modelStore.models.find((model) => model.id === modelId)?.name ?? modelId
}

function formatRunTitle(run: AgentRun): string {
  const promptPreview = run.promptPreview?.trim()
  if (promptPreview) return promptPreview
  return `Run #${run.id}`
}

function formatAbsoluteTime(dateStr: string): string {
  const date = new Date(dateStr)
  return Number.isFinite(date.getTime()) ? date.toLocaleString() : ''
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

onMounted(() => {
  if (!modelStore.loaded) void modelStore.fetchModels()
  startRefreshTimer()
})
onUnmounted(stopRefreshTimer)
</script>

<template>
  <div class="agent-runs-panel">
    <header class="agent-runs-header">
      <span class="agent-runs-title">
        <svg class="panel-icon" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
          <path d="M21 16V8a2 2 0 0 0-1-1.73l-7-4a2 2 0 0 0-2 0l-7 4A2 2 0 0 0 3 8v8a2 2 0 0 0 1 1.73l7 4a2 2 0 0 0 2 0l7-4A2 2 0 0 0 21 16Z" />
          <path d="m3.3 7 8.7 5 8.7-5" />
          <path d="M12 22V12" />
        </svg>
        Agent Runs
      </span>
      <span class="agent-runs-summary">
        <span v-if="waitingCount > 0" class="summary-pill summary-pill--waiting">
          <span class="summary-dot" aria-hidden="true" />
          {{ waitingCount }} waiting
        </span>
        <span v-if="activeCount - waitingCount > 0" class="summary-pill summary-pill--active">
          <span class="summary-dot" aria-hidden="true" />
          {{ activeCount - waitingCount }} running
        </span>
        <span
          v-if="activeCount === 0 && workspaceRuns.length > 0"
          class="summary-count"
          :aria-label="`${workspaceRuns.length} agent runs`"
        >{{ workspaceRuns.length }}</span>
      </span>
    </header>

    <div v-if="runStore.error" class="agent-runs-error" role="alert">
      <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
        <circle cx="12" cy="12" r="10" />
        <path d="M12 8v4" />
        <path d="M12 16h.01" />
      </svg>
      <span>{{ runStore.error }}</span>
    </div>

    <div v-if="runStore.loading && workspaceRuns.length === 0" class="agent-runs-state">
      <span class="agent-runs-spinner" aria-hidden="true" />
      Loading runs…
    </div>
    <div v-else-if="workspaceRuns.length === 0 && !runStore.error" class="agent-runs-state">
      <span class="state-icon" aria-hidden="true">
        <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round">
          <path d="M21 16V8a2 2 0 0 0-1-1.73l-7-4a2 2 0 0 0-2 0l-7 4A2 2 0 0 0 3 8v8a2 2 0 0 0 1 1.73l7 4a2 2 0 0 0 2 0l7-4A2 2 0 0 0 21 16Z" />
          <path d="m3.3 7 8.7 5 8.7-5" />
          <path d="M12 22V12" />
        </svg>
      </span>
      <span class="state-title">No agent runs yet</span>
      <span class="state-hint">Runs you start from the agent chat, and the sub-agents they spawn, show up here.</span>
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
            waiting: isAgentRunWaitingStatus(item.run.status),
            child: item.depth > 0,
            parent: item.hasChildren,
            'last-child': item.isLastChild,
          },
        ]"
        :style="runItemStyle(item)"
        role="button"
        tabindex="0"
        :aria-current="item.run.id === selectedRunId ? 'true' : undefined"
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
          <span class="run-title" :title="formatRunTitle(item.run)">{{ formatRunTitle(item.run) }}</span>
          <span class="run-meta">
            <span class="run-status">{{ formatStatus(item.run.status) }}</span>
            <span class="run-meta-sep" aria-hidden="true">&middot;</span>
            <span class="run-model" :title="item.run.model">{{ formatModel(item.run.model) }}</span>
            <span
              v-if="!modelStore.isModelAvailable(item.run.model)"
              class="model-unavailable"
              title="This model is no longer available."
            >unavailable</span>
            <span class="run-meta-sep" aria-hidden="true">&middot;</span>
            <time class="run-time" :datetime="item.run.updatedAt" :title="formatAbsoluteTime(item.run.updatedAt)">
              {{ formatRelativeTime(item.run.updatedAt) }}
            </time>
          </span>
          <span v-if="item.run.error" class="run-error" :title="item.run.error">{{ item.run.error }}</span>
        </span>
        <span v-if="isAgentRunActiveStatus(item.run.status)" class="run-actions">
          <button
            type="button"
            class="run-stop-button"
            :disabled="runStore.isRunCancelling(item.run.id)"
            :aria-label="`Cancel ${formatRunTitle(item.run)}`"
            title="Cancel run"
            @click.stop="cancelRun(item.run)"
            @keydown.stop
          >
            <span v-if="runStore.isRunCancelling(item.run.id)" class="agent-runs-spinner" aria-hidden="true" />
            <svg v-else aria-hidden="true" width="10" height="10" viewBox="0 0 10 10" fill="currentColor">
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
  /* Opaque so lane segments from adjacent rows can overlap without visible seams. */
  --tree-line: color-mix(in srgb, var(--accent-purple) 30%, var(--bg-raised));
  --row-pad-y: 9px;
  --row-gap: 2px;
  --title-size: 0.82rem;
  --title-line: 17px;
  /* Vertical center of the status node, measured from the top of the row content. */
  --run-node-y: calc(var(--title-line) / 2);
  --stop-size: 24px;
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
  min-width: 0;
  font-size: var(--ide-header-title-size);
  font-weight: 600;
  color: var(--text-primary);
  white-space: nowrap;
}

.panel-icon {
  color: var(--ide-header-icon);
  flex-shrink: 0;
}

.agent-runs-summary {
  display: flex;
  align-items: center;
  gap: var(--space-1);
  flex-shrink: 0;
}

.summary-pill {
  --pill-color: var(--accent-green);
  display: inline-flex;
  align-items: center;
  gap: 5px;
  height: 20px;
  padding: 0 8px;
  border: 0.5px solid color-mix(in srgb, var(--pill-color) 30%, transparent);
  border-radius: 999px;
  background: color-mix(in srgb, var(--pill-color) 11%, transparent);
  color: var(--pill-color);
  font-size: 0.68rem;
  font-weight: 500;
  font-variant-numeric: tabular-nums;
  white-space: nowrap;
}

.summary-pill--waiting { --pill-color: var(--attention); }

.summary-dot {
  position: relative;
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: currentColor;
}

.summary-pill--active .summary-dot::after {
  content: '';
  position: absolute;
  inset: 0;
  border-radius: 50%;
  background: currentColor;
  animation: run-pulse 1.8s ease-out infinite;
}

.summary-count {
  min-width: 20px;
  height: 20px;
  padding: 0 6px;
  border: 0.5px solid var(--border-subtle);
  border-radius: 999px;
  background: var(--bg-raised);
  color: var(--text-muted);
  font-family: var(--font-mono);
  font-size: 0.68rem;
  line-height: 19px;
  text-align: center;
}

.agent-runs-error {
  display: flex;
  align-items: flex-start;
  gap: var(--space-2);
  margin: var(--space-3) var(--space-3) 0;
  padding: var(--space-2) var(--space-3);
  border: 0.5px solid var(--error-border);
  border-radius: var(--radius-md);
  background: var(--error-bg);
  color: var(--accent-rose);
  font-size: 0.75rem;
  line-height: 1.4;
  flex-shrink: 0;
}

.agent-runs-error svg {
  flex-shrink: 0;
  margin-top: 1px;
}

.agent-runs-state {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: var(--space-2);
  padding: var(--space-8) var(--space-6);
  color: var(--text-muted);
  font-size: 0.78rem;
  text-align: center;
}

.state-icon {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 44px;
  height: 44px;
  margin-bottom: var(--space-1);
  border: 0.5px solid var(--border-subtle);
  border-radius: var(--radius-xl);
  background: var(--bg-raised);
  color: var(--text-muted);
}

.state-title {
  color: var(--text-secondary);
  font-size: 0.82rem;
  font-weight: 600;
}

.state-hint {
  max-width: 240px;
  color: var(--text-muted);
  line-height: 1.5;
}

.agent-runs-spinner {
  width: 14px;
  height: 14px;
  border: 2px solid var(--spinner-track);
  border-top-color: var(--accent);
  border-radius: 50%;
  animation: spin 0.8s linear infinite;
}

.agent-runs-list {
  display: flex;
  flex-direction: column;
  min-height: 0;
  overflow-y: auto;
  overscroll-behavior: contain;
  padding: var(--space-2) var(--space-2) var(--space-3);
}

/* Each status sets one color that the node, ring, and label all share. */
.agent-run-item {
  --status-color: var(--text-muted);
  display: grid;
  grid-template-columns: var(--run-tree-width) minmax(0, 1fr) auto;
  align-items: start;
  column-gap: 10px;
  position: relative;
  width: 100%;
  padding: var(--row-pad-y) var(--space-2);
  border: 0.5px solid transparent;
  border-radius: var(--radius-lg);
  color: var(--text-secondary);
  cursor: pointer;
  text-align: left;
  -webkit-tap-highlight-color: transparent;
  transition:
    border-color var(--transition-fast),
    background var(--transition-fast);
}

.agent-run-item + .agent-run-item { margin-top: var(--row-gap); }

.agent-run-item--queued { --status-color: var(--text-secondary); }
.agent-run-item--running { --status-color: var(--accent-green); }
.agent-run-item--waiting_approval,
.agent-run-item--waiting_user { --status-color: var(--attention); }
.agent-run-item--completed { --status-color: var(--accent-blue); }
.agent-run-item--failed { --status-color: var(--accent-rose); }
.agent-run-item--cancelled { --status-color: var(--text-dim); }

.agent-run-item:hover {
  background: var(--bg-hover);
}

.agent-run-item:focus-visible {
  border-color: var(--accent-border);
  box-shadow: 0 0 0 3px var(--accent-glow);
}

.agent-run-item.selected {
  border-color: var(--accent-border);
  background: var(--bg-selected);
}

.agent-run-item.selected::before {
  content: '';
  position: absolute;
  left: -0.5px;
  top: 10px;
  bottom: 10px;
  width: 2px;
  border-radius: 0 2px 2px 0;
  background: var(--accent);
}

/* Tree connectors. Lanes overhang the row by its padding and gap so lines run unbroken between rows. */
.run-tree {
  position: relative;
  align-self: stretch;
  width: var(--run-tree-width);
  min-height: var(--title-line);
}

.tree-lane {
  position: absolute;
  top: calc(-1 * (var(--row-pad-y) + var(--row-gap) + 1px));
  bottom: calc(-1 * (var(--row-pad-y) + 1px));
  left: calc(var(--lane-offset) + var(--tree-node-center) - 0.75px);
  width: var(--tree-step);
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
  background: var(--tree-line);
}

.tree-lane.branch::after {
  content: '';
  position: absolute;
  left: 0;
  top: calc(var(--row-pad-y) + var(--row-gap) + 1px + var(--run-node-y) - 0.75px);
  width: calc(var(--tree-step) - var(--tree-node-center));
  height: 1.5px;
  background: var(--tree-line);
}

/* Last child: the vertical line stops short of the node and curves into it. */
.tree-lane.branch.last-branch::after {
  top: calc(var(--row-pad-y) + var(--row-gap) + 1px + var(--run-node-y) - 6px);
  height: 6px;
  border-bottom: 1.5px solid var(--tree-line);
  border-left: 1.5px solid var(--tree-line);
  border-bottom-left-radius: 6px;
  background: none;
}

.tree-lane.branch.last-branch::before {
  bottom: auto;
  height: calc(var(--row-pad-y) + var(--row-gap) + 1px + var(--run-node-y) - 5px);
}

.agent-run-item.parent .run-tree::after {
  content: '';
  position: absolute;
  left: calc(var(--run-node-offset) + var(--tree-node-center) - 0.75px);
  top: var(--run-node-y);
  bottom: calc(-1 * (var(--row-pad-y) + 1px));
  width: 1.5px;
  background: var(--tree-line);
  pointer-events: none;
}

.run-node {
  position: absolute;
  left: var(--run-node-offset);
  top: var(--run-node-y);
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: var(--tree-node-size);
  height: var(--tree-node-size);
  border: 0.5px solid color-mix(in srgb, var(--status-color) 40%, var(--border-subtle));
  border-radius: 50%;
  background: var(--bg-base);
  z-index: 1;
  transform: translateY(-50%);
}

.agent-run-item.selected .run-node { background: color-mix(in srgb, var(--bg-base) 88%, var(--accent)); }

.run-status-dot {
  position: relative;
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: var(--status-color);
}

.agent-run-item--queued .run-status-dot {
  background: transparent;
  box-shadow: inset 0 0 0 1.5px var(--status-color);
}

.agent-run-item--running .run-status-dot::after {
  content: '';
  position: absolute;
  inset: 0;
  border-radius: 50%;
  background: var(--status-color);
  animation: run-pulse 1.8s ease-out infinite;
}

.agent-run-item.waiting .run-node {
  box-shadow: 0 0 0 3px color-mix(in srgb, var(--status-color) 18%, transparent);
}

.run-main {
  display: flex;
  flex-direction: column;
  gap: 3px;
  min-width: 0;
}

.run-title {
  display: -webkit-box;
  overflow: hidden;
  -webkit-box-orient: vertical;
  -webkit-line-clamp: 2;
  line-clamp: 2;
  color: var(--text-secondary);
  font-size: var(--title-size);
  font-weight: 500;
  line-height: var(--title-line);
  overflow-wrap: anywhere;
}

.agent-run-item.active .run-title,
.agent-run-item.selected .run-title,
.agent-run-item:hover .run-title {
  color: var(--text-primary);
}

.agent-run-item--cancelled .run-title { color: var(--text-muted); }

.run-meta {
  display: flex;
  align-items: center;
  gap: 5px;
  min-width: 0;
  color: var(--text-muted);
  font-size: 0.7rem;
  line-height: 1.3;
  white-space: nowrap;
}

.run-status {
  flex-shrink: 0;
  color: var(--status-color);
  font-weight: 500;
}

.agent-run-item--cancelled .run-status { color: var(--text-muted); }

.run-model {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
}

.run-time {
  flex-shrink: 0;
  font-variant-numeric: tabular-nums;
}

.run-meta-sep {
  flex-shrink: 0;
  color: var(--text-dim);
}

.model-unavailable {
  flex-shrink: 0;
  padding: 0 5px;
  border: 0.5px solid var(--warning-border);
  border-radius: 999px;
  background: var(--warning-bg);
  color: var(--accent-amber);
  font-size: 0.62rem;
  line-height: 14px;
}

.run-error {
  display: -webkit-box;
  overflow: hidden;
  -webkit-box-orient: vertical;
  -webkit-line-clamp: 2;
  line-clamp: 2;
  margin-top: 2px;
  padding: 4px 8px;
  border-left: 2px solid var(--error-border);
  border-radius: 0 var(--radius-sm) var(--radius-sm) 0;
  background: color-mix(in srgb, var(--error-bg) 60%, transparent);
  color: color-mix(in srgb, var(--accent-rose) 80%, var(--text-primary));
  font-family: var(--font-mono);
  font-size: 0.66rem;
  line-height: 1.45;
  overflow-wrap: anywhere;
}

.run-actions {
  display: flex;
  align-items: center;
  height: var(--title-line);
}

.run-stop-button {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: var(--stop-size);
  height: var(--stop-size);
  border: 0.5px solid transparent;
  border-radius: var(--radius-md);
  color: var(--text-muted);
  transition:
    background var(--transition-fast),
    border-color var(--transition-fast),
    color var(--transition-fast);
}

.agent-run-item:hover .run-stop-button,
.agent-run-item:focus-within .run-stop-button {
  color: var(--accent-rose);
}

.run-stop-button:hover:not(:disabled),
.run-stop-button:focus-visible {
  border-color: var(--error-border);
  background: var(--error-bg);
  color: var(--accent-rose);
}

.run-stop-button:disabled {
  cursor: wait;
}

.run-stop-button .agent-runs-spinner {
  width: 11px;
  height: 11px;
  border-width: 1.5px;
  border-top-color: var(--accent-rose);
}

@keyframes run-pulse {
  0% { opacity: 0.7; transform: scale(1); }
  100% { opacity: 0; transform: scale(2.6); }
}

/* Mobile drawer: larger type and comfortable touch targets. */
@media (max-width: 768px), (pointer: coarse) and (max-height: 500px) {
  .agent-runs-panel {
    --row-pad-y: 11px;
    --title-size: 0.9rem;
    --title-line: 19px;
    --stop-size: 34px;
  }

  .agent-runs-header {
    height: 44px;
    padding: 0 var(--space-4);
  }

  .agent-runs-title {
    font-size: 0.85rem;
  }

  .run-meta { font-size: 0.76rem; }

  .run-actions {
    align-self: center;
    height: auto;
    margin-right: -4px;
  }

  .run-stop-button {
    border-color: var(--border-subtle);
    color: var(--accent-rose);
  }
}

@media (prefers-reduced-motion: reduce) {
  .summary-pill--active .summary-dot::after,
  .agent-run-item--running .run-status-dot::after {
    animation: none;
  }
}
</style>
