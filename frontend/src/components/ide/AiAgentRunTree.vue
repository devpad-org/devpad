<script setup lang="ts">
import { computed } from 'vue'
import MrIcon, { type MrIconName } from '@/components/project/MrIcon.vue'
import { type AgentRun, useAgentRunStore, isAgentRunActiveStatus } from '@/stores/agentRuns'
import { useAiModelStore } from '@/stores/aiModels'
import { useAiAgentStore } from '@/stores/aiAgents'
import {
  type RunNode,
  formatAbsoluteTime,
  formatRelativeTime,
  formatRunStatus,
  formatUsage,
  formatUsageTitle,
  isSubAgentRun,
  plural,
  runTitle,
  usageTokenCount,
} from './agentRuns'

// One level of a conversation's runs: at depth 0 the messages sent in it, below
// them the sub-agents each run started. A row is drawn as what its run is, a
// message or a sub-agent, whatever level it sits at.

defineOptions({ name: 'AiAgentRunTree' })

const props = defineProps<{
  nodes: RunNode[]
  depth: number
  selectedRunId: number | null
  // Who sends a conversation's messages, and who answers them: you and the
  // assistant, or in an agent's sandbox, its task or review and the agent.
  sender: { label: string; icon: MrIconName }
  responder: string
  now: number
}>()

const emit = defineEmits<{
  (e: 'focus', run: AgentRun): void
}>()

const runStore = useAgentRunStore()
const modelStore = useAiModelStore()
const agentStore = useAiAgentStore()

const builtInAgentNames: Record<string, string> = {
  default: 'Coding Agent',
  review: 'Code Review Agent',
}

const treeClass = computed(() => {
  if (props.depth === 0) return 'run-tree--roots'
  return props.depth === 1 ? 'run-tree--spawned' : 'run-tree--nested'
})

function agentName(run: AgentRun): string {
  return agentStore.agents.find((agent) => agent.id === run.agentId)?.name
    ?? builtInAgentNames[run.agentId]
    ?? 'Agent'
}

function formatModel(modelId: string): string {
  return modelStore.models.find((model) => model.id === modelId)?.name ?? modelId
}

function starterName(node: RunNode): string {
  return isSubAgentRun(node.run) ? agentName(node.run) : props.responder
}

const statusIcons: Partial<Record<AgentRun['status'], MrIconName>> = {
  queued: 'clock',
  waiting_approval: 'alert',
  waiting_user: 'alert',
  completed: 'check',
  failed: 'close',
  cancelled: 'ban',
}

function rowLabel(run: AgentRun): string {
  const who = isSubAgentRun(run) ? `Sub-agent ${agentName(run)}` : props.sender.label
  return `${who}: ${runTitle(run)}, ${formatRunStatus(run)}`
}

function focusRun(run: AgentRun): void {
  emit('focus', run)
}

async function cancelRun(run: AgentRun): Promise<void> {
  if (!isAgentRunActiveStatus(run.status)) return
  await runStore.cancelRun(run.id)
}
</script>

<template>
  <component :is="depth === 0 ? 'ol' : 'ul'" class="run-tree" :class="treeClass">
    <li
      v-for="node in nodes"
      :key="node.run.id"
      class="run-node"
      :class="{
        'run-node--sub': isSubAgentRun(node.run),
        'has-children': node.children.length > 0,
      }"
    >
      <div
        class="run-row"
        :class="[
          `run-row--${node.run.status}`,
          isSubAgentRun(node.run) ? 'run-row--sub' : 'run-row--message',
          {
            selected: node.run.id === selectedRunId,
            active: isAgentRunActiveStatus(node.run.status),
          },
        ]"
        role="button"
        tabindex="0"
        :aria-label="rowLabel(node.run)"
        :aria-current="node.run.id === selectedRunId ? 'true' : undefined"
        @click="focusRun(node.run)"
        @keydown.enter.prevent="focusRun(node.run)"
        @keydown.space.prevent="focusRun(node.run)"
      >
        <span class="run-lead" aria-hidden="true">
          <span class="run-mark">
            <span v-if="isSubAgentRun(node.run)" class="run-glyph">
              <MrIcon name="bot" :size="12" :stroke-width="2" />
            </span>
            <span v-else class="run-avatar">
              <MrIcon :name="sender.icon" :size="12" :stroke-width="2" />
            </span>
            <span class="run-badge" />
          </span>
        </span>

        <span class="run-body">
          <span class="run-head">
            <span v-if="isSubAgentRun(node.run)" class="run-who run-who--agent">{{ agentName(node.run) }}</span>
            <template v-else>
              <span class="run-who">{{ sender.label }}</span>
              <time
                class="run-when"
                :datetime="node.run.createdAt"
                :title="formatAbsoluteTime(node.run.createdAt)"
              >{{ formatRelativeTime(node.run.createdAt, now) }}</time>
            </template>
            <span v-if="isAgentRunActiveStatus(node.run.status)" class="run-actions">
              <button
                type="button"
                class="run-stop-button"
                :disabled="runStore.isRunCancelling(node.run.id)"
                :aria-label="`Stop ${runTitle(node.run)}`"
                :title="isSubAgentRun(node.run) ? 'Stop sub-agent' : 'Stop run'"
                @click.stop="cancelRun(node.run)"
                @keydown.stop
              >
                <span v-if="runStore.isRunCancelling(node.run.id)" class="run-stop-spinner" aria-hidden="true" />
                <svg v-else aria-hidden="true" width="9" height="9" viewBox="0 0 10 10" fill="currentColor">
                  <rect x="1" y="1" width="8" height="8" rx="1.5" />
                </svg>
              </button>
            </span>
          </span>

          <span class="run-text" :title="runTitle(node.run)">{{ runTitle(node.run) }}</span>

          <span class="run-meta">
            <span class="run-status">
              <span v-if="node.run.status === 'running'" class="run-status-pulse" aria-hidden="true" />
              <MrIcon
                v-else-if="statusIcons[node.run.status]"
                :name="statusIcons[node.run.status]!"
                :size="10"
                :stroke-width="2.5"
              />
              {{ formatRunStatus(node.run) }}
            </span>
            <span class="run-sep" aria-hidden="true">&middot;</span>
            <span class="run-model" :title="node.run.model">{{ formatModel(node.run.model) }}</span>
            <span
              v-if="!modelStore.isModelAvailable(node.run.model)"
              class="model-unavailable"
              title="This model is no longer available."
            >unavailable</span>
            <span
              v-if="usageTokenCount(node.usage) > 0"
              class="run-cost"
              :title="formatUsageTitle(node.usage)"
            >{{ formatUsage(node.usage) }}</span>
          </span>

          <span v-if="node.run.error" class="run-error" :title="node.run.error">{{ node.run.error }}</span>
        </span>
      </div>

      <template v-if="node.children.length > 0">
        <div class="run-spawned">
          <svg class="run-spawned-icon" width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
            <path d="m12 3-1.912 5.813a2 2 0 0 1-1.275 1.275L3 12l5.813 1.912a2 2 0 0 1 1.275 1.275L12 21l1.912-5.813a2 2 0 0 1 1.275-1.275L21 12l-5.813-1.912a2 2 0 0 1-1.275-1.275L12 3Z" />
          </svg>
          <span>{{ starterName(node) }} started {{ plural(node.children.length, 'sub-agent', 'sub-agents') }}</span>
        </div>
        <AiAgentRunTree
          :nodes="node.children"
          :depth="depth + 1"
          :selected-run-id="selectedRunId"
          :sender="sender"
          :responder="responder"
          :now="now"
          @focus="focusRun"
        />
      </template>
    </li>
  </component>
</template>

<style scoped>
/*
 * Geometry. A message's avatar and a sub-agent's glyph sit in a lead column
 * centred on the first line of the row. The rail from a run to the sub-agents
 * it started runs down from its mark, through the "started" line, and
 * branches into each sub-agent's glyph; the last one curves in.
 */
.run-tree {
  --row-px: 6px;
  --row-py: 7px;
  --head-line: 22px;
  --lead-gap: 9px;
  --elbow: 10px;
  --rail-w: 1.5px;
  --lead-y: calc(var(--row-py) + var(--head-line) / 2);
  display: flex;
  flex-direction: column;
  margin: 0;
  padding: 0;
  list-style: none;
}

.run-node {
  position: relative;
  min-width: 0;
  /* Where the rail to this run's own sub-agents runs, from this li's edge. */
  --child-rail-x: calc(var(--row-px) + var(--mark-size) / 2);
  --mark-size: var(--avatar-size);
}

.run-node--sub {
  --mark-size: var(--glyph-size);
}

.run-tree--roots > .run-node + .run-node {
  margin-top: 2px;
}

.run-tree--spawned,
.run-tree--nested {
  --rail-x: var(--child-rail-x);
}

.run-tree--spawned > .run-node,
.run-tree--nested > .run-node {
  padding-left: calc(var(--rail-x) + var(--elbow));
}

/* The rail: straight down past every sub-agent but the last, with a branch
   into each, and a curve into the last. */
.run-tree--spawned > .run-node::before,
.run-tree--nested > .run-node::before,
.run-tree--spawned > .run-node::after,
.run-tree--nested > .run-node::after {
  content: '';
  position: absolute;
  left: calc(var(--rail-x) - var(--rail-w) / 2);
  pointer-events: none;
}

.run-tree--spawned > .run-node::before,
.run-tree--nested > .run-node::before {
  top: 0;
  bottom: 0;
  width: var(--rail-w);
  background: var(--rail-color);
}

.run-tree--spawned > .run-node::after,
.run-tree--nested > .run-node::after {
  top: calc(var(--lead-y) - var(--rail-w) / 2);
  width: calc(var(--elbow) + var(--row-px));
  height: var(--rail-w);
  background: var(--rail-color);
}

.run-tree--spawned > .run-node:last-child::before,
.run-tree--nested > .run-node:last-child::before {
  display: none;
}

.run-tree--spawned > .run-node:last-child::after,
.run-tree--nested > .run-node:last-child::after {
  top: 0;
  height: calc(var(--lead-y) + var(--rail-w) / 2);
  border-left: var(--rail-w) solid var(--rail-color);
  border-bottom: var(--rail-w) solid var(--rail-color);
  border-bottom-left-radius: 8px;
  background: none;
}

/* ── Row ─────────────────────────────────────────────────────────────── */

.run-row {
  --status-color: var(--text-tertiary);
  position: relative;
  display: grid;
  grid-template-columns: var(--mark-size) minmax(0, 1fr);
  column-gap: var(--lead-gap);
  padding: var(--row-py) var(--row-px);
  border-radius: var(--radius-md);
  color: var(--text-secondary);
  cursor: pointer;
  -webkit-tap-highlight-color: transparent;
  transition: background var(--transition-fast);
}

.run-row--queued { --status-color: var(--text-tertiary); }
.run-row--running { --status-color: var(--success); }
.run-row--waiting_approval,
.run-row--waiting_user { --status-color: var(--attention); }
.run-row--completed { --status-color: var(--text-tertiary); }
.run-row--failed { --status-color: var(--error); }
.run-row--cancelled { --status-color: var(--text-dim); }

.run-row:hover {
  background: var(--bg-hover);
}

.run-row:focus-visible {
  outline: none;
  box-shadow: 0 0 0 2px var(--accent-glow), inset 0 0 0 0.5px var(--accent-border);
}

.run-row.selected {
  background: var(--bg-selected);
  box-shadow: inset 2px 0 0 var(--accent);
}

/* Lead: the avatar or glyph, centred on the first line. */
.run-lead {
  position: relative;
  align-self: stretch;
}

.run-mark {
  position: relative;
  display: flex;
  align-items: center;
  justify-content: center;
  height: var(--head-line);
}

.run-avatar {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: var(--avatar-size);
  height: var(--avatar-size);
  border: 0.5px solid var(--border-subtle);
  border-radius: 50%;
  background: var(--bg-raised);
  color: var(--text-secondary);
}

.run-glyph {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: var(--glyph-size);
  height: var(--glyph-size);
  border: 0.5px solid color-mix(in srgb, var(--hue-purple) 40%, transparent);
  border-radius: 6px;
  background: color-mix(in srgb, var(--hue-purple) 14%, var(--card-bg));
  color: var(--hue-purple);
}

/* A dot on the mark for a run that is going or needs a look. */
.run-badge {
  position: absolute;
  right: -2px;
  bottom: calc((var(--head-line) - var(--mark-size)) / 2 - 2px);
  display: none;
  width: 8px;
  height: 8px;
  border-radius: 50%;
  background: var(--status-color);
  box-shadow: 0 0 0 2px var(--card-bg);
}

.run-row.active .run-badge,
.run-row--failed .run-badge {
  display: block;
}

.run-row--queued .run-badge {
  background: var(--card-bg);
  box-shadow: 0 0 0 2px var(--card-bg), inset 0 0 0 1.5px var(--status-color);
}

.run-row--running .run-badge::after,
.run-status-pulse::after {
  content: '';
  position: absolute;
  inset: 0;
  border-radius: 50%;
  background: var(--status-color);
  animation: run-pulse 1.8s ease-out infinite;
}

/* The rail down from a run's mark to the sub-agents it started. */
.run-node.has-children > .run-row .run-lead::after {
  content: '';
  position: absolute;
  left: calc(50% - var(--rail-w) / 2);
  top: calc(var(--head-line) / 2 + var(--mark-size) / 2 + 3px);
  bottom: calc(-1 * var(--row-py));
  width: var(--rail-w);
  background: var(--rail-color);
  pointer-events: none;
}

/* Body */
.run-body {
  display: flex;
  flex-direction: column;
  min-width: 0;
}

.run-head {
  display: flex;
  align-items: center;
  gap: 6px;
  min-width: 0;
  height: var(--head-line);
  font-size: 0.74rem;
  white-space: nowrap;
}

.run-who {
  flex-shrink: 0;
  color: var(--text-primary);
  font-weight: 600;
}

.run-who--agent {
  flex-shrink: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
}

.run-when {
  flex-shrink: 0;
  color: var(--text-tertiary);
  font-size: 0.68rem;
  font-variant-numeric: tabular-nums;
}

.run-cost {
  flex-shrink: 0;
  margin-left: auto;
  padding-left: 6px;
  color: var(--text-tertiary);
  font-variant-numeric: tabular-nums;
}

.run-text {
  display: -webkit-box;
  overflow: hidden;
  -webkit-box-orient: vertical;
  -webkit-line-clamp: 2;
  line-clamp: 2;
  color: var(--text-secondary);
  font-size: 0.79rem;
  line-height: 1.4;
  overflow-wrap: anywhere;
}

.run-row--message .run-text,
.run-row.selected .run-text,
.run-row:hover .run-text {
  color: var(--text-primary);
}

.run-row--sub .run-text {
  font-size: 0.76rem;
}

.run-row--cancelled .run-text {
  color: var(--text-tertiary);
}

.run-meta {
  display: flex;
  align-items: center;
  gap: 5px;
  min-width: 0;
  margin-top: 3px;
  color: var(--text-tertiary);
  font-size: 0.68rem;
  line-height: 1.3;
  white-space: nowrap;
}

.run-status {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  flex-shrink: 0;
  color: var(--status-color);
  font-weight: 500;
}

.run-row--completed .run-status .mr-icon {
  color: var(--success);
}

.run-status-pulse {
  position: relative;
  width: 6px;
  height: 6px;
  margin: 0 2px;
  border-radius: 50%;
  background: var(--status-color);
}

.run-sep {
  flex-shrink: 0;
  color: var(--text-dim);
}

.run-model {
  min-width: 3ch;
  overflow: hidden;
  text-overflow: ellipsis;
}

.model-unavailable {
  flex-shrink: 0;
  padding: 0 5px;
  border: 0.5px solid var(--warning-border);
  border-radius: 999px;
  background: var(--warning-bg);
  color: var(--warning);
  font-size: 0.62rem;
  line-height: 14px;
}

.run-error {
  display: -webkit-box;
  overflow: hidden;
  -webkit-box-orient: vertical;
  -webkit-line-clamp: 2;
  line-clamp: 2;
  margin-top: 5px;
  padding: 4px 8px;
  border-left: 2px solid var(--error-border);
  border-radius: 0 var(--radius-sm) var(--radius-sm) 0;
  background: color-mix(in srgb, var(--error-bg) 70%, transparent);
  color: var(--error);
  font-family: var(--font-mono);
  font-size: 0.65rem;
  line-height: 1.45;
  overflow-wrap: anywhere;
}

/* The line between a run and the sub-agents it started. */
.run-spawned {
  position: relative;
  display: flex;
  align-items: center;
  gap: 5px;
  min-width: 0;
  padding: 1px var(--row-px) 3px calc(var(--row-px) + var(--mark-size) + var(--lead-gap));
  color: var(--text-tertiary);
  font-size: 0.66rem;
  line-height: 16px;
  white-space: nowrap;
}

.run-spawned > span {
  overflow: hidden;
  text-overflow: ellipsis;
}

.run-spawned::before {
  content: '';
  position: absolute;
  left: calc(var(--child-rail-x) - var(--rail-w) / 2);
  top: 0;
  bottom: 0;
  width: var(--rail-w);
  background: var(--rail-color);
}

.run-spawned-icon {
  flex-shrink: 0;
  color: var(--hue-purple);
}

/* Stop */
.run-actions {
  display: flex;
  align-items: center;
  flex-shrink: 0;
  margin-left: auto;
  margin-right: -2px;
}

.run-stop-button {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: var(--stop-size);
  height: var(--stop-size);
  border: 0.5px solid transparent;
  border-radius: var(--radius-md);
  color: var(--text-tertiary);
  transition:
    background var(--transition-fast),
    border-color var(--transition-fast),
    color var(--transition-fast);
}

.run-row:hover .run-stop-button,
.run-row:focus-within .run-stop-button {
  color: var(--error);
}

.run-stop-button:hover:not(:disabled),
.run-stop-button:focus-visible {
  border-color: var(--error-border);
  background: var(--error-bg);
  color: var(--error);
}

.run-stop-button:disabled {
  cursor: wait;
}

.run-stop-spinner {
  width: 11px;
  height: 11px;
  border: 1.5px solid var(--spinner-track);
  border-top-color: var(--error);
  border-radius: 50%;
  animation: spin 0.8s linear infinite;
}

@keyframes run-pulse {
  0% { opacity: 0.7; transform: scale(1); }
  100% { opacity: 0; transform: scale(2.6); }
}

@media (max-width: 768px), (pointer: coarse) and (max-height: 500px) {
  .run-tree {
    --row-py: 8px;
    --head-line: 28px;
  }

  .run-head { font-size: 0.82rem; }
  .run-text { font-size: 0.88rem; }
  .run-row--sub .run-text { font-size: 0.85rem; }
  .run-meta,
  .run-when { font-size: 0.76rem; }

  .run-stop-button {
    border-color: var(--border-subtle);
    color: var(--error);
  }
}

@media (prefers-reduced-motion: reduce) {
  .run-row--running .run-badge::after,
  .run-status-pulse::after {
    animation: none;
  }
}
</style>
