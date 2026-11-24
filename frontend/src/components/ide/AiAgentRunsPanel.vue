<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { type AgentRun, useAgentRunStore, isAgentRunActiveStatus } from '@/stores/agentRuns'
import { useAiModelStore } from '@/stores/aiModels'
import type { Workspace } from '@/api/workspaces'
import MrIcon from '@/components/project/MrIcon.vue'
import DocLink from '@/components/DocLink.vue'
import AiAgentRunTree from './AiAgentRunTree.vue'
import {
  type RunThread,
  buildRunThreads,
  formatAbsoluteTime,
  formatRelativeTime,
  formatUsage,
  formatUsageTitle,
  isAgentRunWaitingStatus,
  plural,
  usageTokenCount,
} from './agentRuns'

const props = defineProps<{
  workspaceId: number
  selectedRunId: number | null
  workspaceKind?: Workspace['kind']
}>()

const emit = defineEmits<{
  (e: 'focus', run: AgentRun): void
}>()

const runStore = useAgentRunStore()
const modelStore = useAiModelStore()
let refreshTimer: number | null = null
let clockTimer: number | null = null

// Relative times and durations are read against this, ticked so that "just
// now" grows old even while no run changes.
const now = ref(Date.now())

const workspaceRuns = computed(() =>
  runStore.runs.filter((run) => run.workspaceId === props.workspaceId),
)

const activeCount = computed(() =>
  workspaceRuns.value.filter((run) => isAgentRunActiveStatus(run.status)).length,
)

const waitingCount = computed(() =>
  workspaceRuns.value.filter((run) => isAgentRunWaitingStatus(run.status)).length,
)

const threads = computed<RunThread[]>(() => buildRunThreads(workspaceRuns.value))

// In an agent's sandbox, a conversation's messages come from its task or
// review rather than from you typing them.
const sandbox = computed(() => props.workspaceKind === 'task' || props.workspaceKind === 'review')
const sender = computed(() => {
  if (props.workspaceKind === 'task') return { label: 'Task', icon: 'task' as const }
  if (props.workspaceKind === 'review') return { label: 'Review', icon: 'eye' as const }
  return { label: 'You', icon: 'user' as const }
})
const responder = computed(() => (sandbox.value ? 'Agent' : 'Assistant'))

// A conversation opens by itself while it is the latest, is working or holds
// the run on screen; the chevron overrides that either way.
const expandedOverrides = ref(new Map<string, boolean>())

function isExpanded(thread: RunThread, index: number): boolean {
  const override = expandedOverrides.value.get(thread.key)
  if (override !== undefined) return override
  return index === 0
    || thread.status === 'running'
    || thread.status === 'waiting'
    || (props.selectedRunId !== null && thread.runIds.has(props.selectedRunId))
}

function toggleThread(thread: RunThread, index: number): void {
  const next = new Map(expandedOverrides.value)
  next.set(thread.key, !isExpanded(thread, index))
  expandedOverrides.value = next
}

const threadStatusLabels: Record<RunThread['status'], string> = {
  waiting: 'Needs you',
  running: 'Running',
  failed: 'Failed',
  idle: '',
}

function messageCount(thread: RunThread): string {
  return sandbox.value
    ? plural(thread.messages, 'round', 'rounds')
    : plural(thread.messages, 'message', 'messages')
}

function threadCounts(thread: RunThread): string {
  const parts: string[] = []
  if (thread.messages > 0) parts.push(messageCount(thread))
  if (thread.subAgents > 0) parts.push(plural(thread.subAgents, 'sub-agent', 'sub-agents'))
  return parts.join(', ')
}

function threadLabel(thread: RunThread, index: number): string {
  const state = threadStatusLabels[thread.status]
  const expanded = isExpanded(thread, index)
  return [thread.title, threadCounts(thread), state].filter(Boolean).join(', ')
    + (expanded ? '' : '. Collapsed')
}

async function refreshRuns(): Promise<void> {
  await runStore.fetchRuns(props.workspaceId)
  now.value = Date.now()
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

watch(() => props.workspaceId, () => {
  expandedOverrides.value = new Map()
  startRefreshTimer()
})

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
  clockTimer = window.setInterval(() => {
    now.value = Date.now()
  }, 30000)
})

onUnmounted(() => {
  stopRefreshTimer()
  if (clockTimer !== null) window.clearInterval(clockTimer)
})
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
        <DocLink topic="agentRuns" label="Agent runs" />
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
          v-if="activeCount === 0 && threads.length > 0"
          class="summary-count"
          :title="`${plural(threads.length, 'conversation', 'conversations')}, ${plural(workspaceRuns.length, 'run', 'runs')}`"
          :aria-label="plural(threads.length, 'conversation', 'conversations')"
        >{{ threads.length }}</span>
      </span>
    </header>

    <div v-if="runStore.error" class="agent-runs-error" role="alert">
      <MrIcon name="alert" :size="14" />
      <span>{{ runStore.error }}</span>
    </div>

    <div v-if="runStore.loading && workspaceRuns.length === 0" class="agent-runs-state">
      <span class="agent-runs-spinner" aria-hidden="true" />
      Loading runs…
    </div>
    <div v-else-if="workspaceRuns.length === 0 && !runStore.error" class="agent-runs-state">
      <span class="state-art" aria-hidden="true">
        <span class="state-art-avatar"><MrIcon name="user" :size="13" /></span>
        <span class="state-art-rail" />
        <span class="state-art-glyph"><MrIcon name="bot" :size="12" /></span>
      </span>
      <span class="state-title">No agent runs yet</span>
      <span class="state-hint">
        <template v-if="sandbox">Each round of this sandbox's agent, and the sub-agents it starts, show up here.</template>
        <template v-else>Each message you send the assistant shows up here, with the sub-agents it starts to help.</template>
      </span>
    </div>
    <div v-else class="agent-runs-list">
      <section
        v-for="(thread, index) in threads"
        :key="thread.key"
        class="run-thread"
        :class="[`run-thread--${thread.status}`, { expanded: isExpanded(thread, index) }]"
      >
        <button
          type="button"
          class="thread-header"
          :aria-expanded="isExpanded(thread, index)"
          :aria-label="threadLabel(thread, index)"
          @click="toggleThread(thread, index)"
        >
          <span class="thread-icon" aria-hidden="true">
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
              <path d="m12 3-1.912 5.813a2 2 0 0 1-1.275 1.275L3 12l5.813 1.912a2 2 0 0 1 1.275 1.275L12 21l1.912-5.813a2 2 0 0 1 1.275-1.275L21 12l-5.813-1.912a2 2 0 0 1-1.275-1.275L12 3Z" />
            </svg>
          </span>
          <span class="thread-main">
            <span class="thread-title" :title="thread.title">{{ thread.title }}</span>
            <span class="thread-meta">
              <span v-if="thread.status !== 'idle'" class="thread-status">
                <span class="thread-status-dot" aria-hidden="true" />
                {{ threadStatusLabels[thread.status] }}
              </span>
              <span v-if="thread.messages > 0" class="thread-count" :title="messageCount(thread)">
                <MrIcon :name="sender.icon" :size="10" :stroke-width="2.25" />{{ thread.messages }}
              </span>
              <span v-if="thread.subAgents > 0" class="thread-count thread-count--sub" :title="plural(thread.subAgents, 'sub-agent', 'sub-agents')">
                <MrIcon name="bot" :size="11" :stroke-width="2.25" />{{ thread.subAgents }}
              </span>
              <template v-if="thread.status !== 'running' && thread.status !== 'waiting'">
                <span class="thread-sep" aria-hidden="true">&middot;</span>
                <time
                  class="thread-time"
                  :datetime="new Date(thread.latestActivity).toISOString()"
                  :title="formatAbsoluteTime(thread.latestActivity)"
                >{{ formatRelativeTime(thread.latestActivity, now) }}</time>
              </template>
            </span>
          </span>
          <span class="thread-side">
            <span
              v-if="usageTokenCount(thread.usage) > 0"
              class="thread-cost"
              :title="formatUsageTitle(thread.usage)"
            >{{ formatUsage(thread.usage) }}</span>
            <MrIcon name="chevron-down" :size="14" class="thread-chevron" />
          </span>
        </button>

        <div v-if="isExpanded(thread, index)" class="thread-body">
          <AiAgentRunTree
            :nodes="thread.roots"
            :depth="0"
            :selected-run-id="selectedRunId"
            :sender="sender"
            :responder="responder"
            :now="now"
            @focus="focusRun"
          />
        </div>
      </section>
    </div>
  </div>
</template>

<style scoped>
.agent-runs-panel {
  --ide-header-icon: var(--accent);
  --card-bg: var(--bg-elevated);
  /* Opaque, so rail segments meeting at a branch don't darken. */
  --rail-color: color-mix(in srgb, var(--hue-purple) 38%, var(--card-bg));
  --avatar-size: 22px;
  --glyph-size: 20px;
  --stop-size: 22px;
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
  border-bottom: 0.5px solid var(--border-subtle);
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

.agent-runs-title .doc-link {
  margin-left: 0;
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
  --pill-color: var(--success);
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
  color: var(--text-tertiary);
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
  color: var(--error);
  font-size: 0.75rem;
  line-height: 1.4;
  flex-shrink: 0;
}

.agent-runs-error .mr-icon {
  margin-top: 1px;
}

.agent-runs-state {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: var(--space-2);
  padding: var(--space-8) var(--space-6);
  color: var(--text-tertiary);
  font-size: 0.78rem;
  text-align: center;
}

/* A message and a sub-agent it started, as the list draws them. */
.state-art {
  display: grid;
  grid-template-columns: 26px 22px;
  grid-template-rows: 26px 14px 22px;
  margin-bottom: var(--space-2);
}

.state-art-avatar {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  grid-column: 1;
  grid-row: 1;
  width: 26px;
  height: 26px;
  border: 0.5px solid var(--border-subtle);
  border-radius: 50%;
  background: var(--bg-raised);
  color: var(--text-secondary);
}

.state-art-rail {
  grid-column: 1 / 3;
  grid-row: 2 / 4;
  width: 22px;
  height: 25px;
  margin-left: 12.25px;
  border-left: 1.5px solid var(--rail-color);
  border-bottom: 1.5px solid var(--rail-color);
  border-bottom-left-radius: 8px;
  transform: translateY(1px);
}

.state-art-glyph {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  grid-column: 2;
  grid-row: 3;
  justify-self: end;
  width: 22px;
  height: 22px;
  margin-right: -14px;
  border: 0.5px solid color-mix(in srgb, var(--hue-purple) 40%, transparent);
  border-radius: 6px;
  background: color-mix(in srgb, var(--hue-purple) 14%, var(--ide-panel-bg));
  color: var(--hue-purple);
}

.state-title {
  color: var(--text-secondary);
  font-size: 0.82rem;
  font-weight: 600;
}

.state-hint {
  max-width: 240px;
  color: var(--text-tertiary);
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
  gap: var(--space-2);
  min-height: 0;
  overflow-y: auto;
  overscroll-behavior: contain;
  padding: var(--space-2) var(--space-2) var(--space-3);
}

/* ── Conversation ────────────────────────────────────────────────────── */

.run-thread {
  flex-shrink: 0;
  border: 0.5px solid var(--border-subtle);
  border-radius: var(--radius-lg);
  background: var(--card-bg);
}

.run-thread--running { --thread-status: var(--success); }
.run-thread--waiting {
  --thread-status: var(--attention);
  border-color: var(--attention-border);
}
.run-thread--failed { --thread-status: var(--error); }

.thread-header {
  display: grid;
  grid-template-columns: 26px minmax(0, 1fr) auto;
  align-items: center;
  column-gap: 10px;
  width: 100%;
  padding: 9px 8px 9px 9px;
  border: none;
  border-radius: calc(var(--radius-lg) - 0.5px);
  background: none;
  color: inherit;
  font: inherit;
  text-align: left;
  cursor: pointer;
  transition: background var(--transition-fast);
}

.run-thread.expanded .thread-header {
  border-bottom-right-radius: 0;
  border-bottom-left-radius: 0;
}

.thread-header:hover {
  background: var(--bg-hover);
}

.thread-header:focus-visible {
  outline: none;
  box-shadow: 0 0 0 2px var(--accent-glow), inset 0 0 0 0.5px var(--accent-border);
}

.thread-icon {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 26px;
  height: 26px;
  border: 0.5px solid var(--accent-border);
  border-radius: var(--radius-md);
  background: color-mix(in srgb, var(--accent) 14%, var(--card-bg));
  color: var(--accent);
}

.thread-main {
  display: flex;
  flex-direction: column;
  gap: 2px;
  min-width: 0;
}

.thread-title {
  overflow: hidden;
  color: var(--text-primary);
  font-size: 0.8rem;
  font-weight: 600;
  line-height: 1.3;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.thread-meta {
  display: flex;
  align-items: center;
  gap: 5px;
  min-width: 0;
  overflow: hidden;
  color: var(--text-tertiary);
  font-size: 0.68rem;
  line-height: 1.3;
  white-space: nowrap;
}

.thread-status {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  flex-shrink: 0;
  padding: 0 6px 0 5px;
  border-radius: 999px;
  background: color-mix(in srgb, var(--thread-status) 13%, transparent);
  color: var(--thread-status);
  font-weight: 500;
  line-height: 16px;
}

.thread-status-dot {
  position: relative;
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: currentColor;
}

.run-thread--running .thread-status-dot::after {
  content: '';
  position: absolute;
  inset: 0;
  border-radius: 50%;
  background: currentColor;
  animation: run-pulse 1.8s ease-out infinite;
}

.thread-count {
  display: inline-flex;
  align-items: center;
  gap: 3px;
  flex-shrink: 0;
  font-variant-numeric: tabular-nums;
}

.thread-count + .thread-count {
  margin-left: 2px;
}

.thread-count--sub .mr-icon {
  color: var(--hue-purple);
}

.thread-sep {
  flex-shrink: 0;
  color: var(--text-dim);
}

.thread-time {
  flex-shrink: 0;
  font-variant-numeric: tabular-nums;
}

.thread-side {
  display: flex;
  align-items: center;
  gap: 6px;
  align-self: start;
  height: 17px;
}

.thread-cost {
  color: var(--text-secondary);
  font-size: 0.7rem;
  font-variant-numeric: tabular-nums;
}

.thread-chevron {
  color: var(--text-dim);
  transform: rotate(-90deg);
  transition: transform var(--transition-fast), color var(--transition-fast);
}

.run-thread.expanded .thread-chevron {
  transform: none;
}

.thread-header:hover .thread-chevron {
  color: var(--text-secondary);
}

.thread-body {
  padding: 4px 4px 5px;
  border-top: 0.5px solid var(--border-hairline);
}

@keyframes run-pulse {
  0% { opacity: 0.7; transform: scale(1); }
  100% { opacity: 0; transform: scale(2.6); }
}

/* Mobile drawer: larger type and comfortable touch targets. */
@media (max-width: 768px), (pointer: coarse) and (max-height: 500px) {
  .agent-runs-panel {
    --avatar-size: 24px;
    --glyph-size: 22px;
    --stop-size: 28px;
  }

  .agent-runs-header {
    height: 44px;
    padding: 0 var(--space-4);
  }

  .agent-runs-title {
    font-size: 0.85rem;
  }

  .thread-header {
    padding: 11px 10px;
  }

  .thread-title { font-size: 0.9rem; }
  .thread-meta,
  .thread-cost { font-size: 0.76rem; }
}

@media (prefers-reduced-motion: reduce) {
  .summary-pill--active .summary-dot::after,
  .run-thread--running .thread-status-dot::after {
    animation: none;
  }
}
</style>
