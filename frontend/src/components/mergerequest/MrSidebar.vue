<script setup lang="ts">
import { ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import AgentModelPicker from '@/components/project/AgentModelPicker.vue'
import MrIcon from '@/components/project/MrIcon.vue'
import StatusBadge from '@/components/project/StatusBadge.vue'
import { useActiveProjectStore } from '@/stores/activeProject'
import { REVIEW_AGENT_ID } from '@/utils/aiModels'
import { fullTime, timeAgo } from '@/utils/time'
import { authorName, plural, shortSha } from './format'
import { useMergeRequest } from './useMergeRequest'

// The merge request's details, its reviewers, the agent review controls and
// closing it.
const {
  mr,
  isOpen,
  busy,
  files,
  totals,
  section,
  latestReviews,
  pendingAgentReview,
  agentModel,
  agentEffort,
  autoFix,
  startAgentReview,
  close,
} = useMergeRequest()

const projectStore = useActiveProjectStore()
const route = useRoute()
const router = useRouter()
const confirmingClose = ref(false)

watch(() => mr.value?.id, () => {
  confirmingClose.value = false
})

// A real link to the task, so it can be copied or opened in a new tab.
function taskHref(id: number): string {
  return router.resolve({ query: { ...route.query, task: id } }).href
}

// A plain click opens the task over this modal without navigating.
function openTask(e: MouseEvent, id: number) {
  if (e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return
  e.preventDefault()
  projectStore.selectedTaskId = id
}

function closeMergeRequest() {
  confirmingClose.value = false
  close()
}
</script>

<template>
  <aside v-if="mr" class="side-col">
    <section class="side-section">
      <h3 class="side-title">Details</h3>
      <dl class="details">
        <dt>Source</dt>
        <dd><span class="mono ellipsis" :title="mr.sourceBranch">{{ mr.sourceBranch }}</span></dd>
        <dt>Target</dt>
        <dd><span class="mono ellipsis" :title="mr.targetBranch">{{ mr.targetBranch }}</span></dd>
        <dt>Head</dt>
        <dd><span class="mono" :title="mr.headSha">{{ shortSha(mr.headSha) }}</span></dd>
        <template v-if="isOpen">
          <dt>Commits</dt>
          <dd>
            <span>{{ mr.ahead }} ahead</span>
            <span class="muted">&middot;</span>
            <span :class="{ warn: mr.behind > 0 }">{{ mr.behind }} behind</span>
          </dd>
        </template>
        <dt>Changes</dt>
        <dd>
          <button class="text-button" @click="section = 'changes'">{{ plural(files.length, 'file') }}</button>
          <span class="add mono">+{{ totals.additions }}</span>
          <span class="del mono">&minus;{{ totals.deletions }}</span>
        </dd>
        <template v-if="mr.taskId">
          <dt>Task</dt>
          <dd>
            <a class="task-link" :href="taskHref(mr.taskId)" @click="openTask($event, mr.taskId)">
              <MrIcon name="task" :size="12" />
              #{{ mr.taskId }}
            </a>
          </dd>
        </template>
        <dt>Opened</dt>
        <dd :title="fullTime(mr.createdAt)">{{ timeAgo(mr.createdAt) }}</dd>
        <dt>Updated</dt>
        <dd :title="fullTime(mr.updatedAt)">{{ timeAgo(mr.updatedAt) }}</dd>
      </dl>
    </section>

    <section class="side-section">
      <h3 class="side-title">Reviewers</h3>
      <div v-for="author in (['user', 'agent'] as const)" :key="author" class="reviewer">
        <span class="avatar" :class="`avatar-${author}`">
          <MrIcon :name="author === 'agent' ? 'bot' : 'user'" :size="13" />
        </span>
        <span class="reviewer-name">{{ authorName(author) }}</span>
        <template v-if="latestReviews.get(author)">
          <StatusBadge
            :status="latestReviews.get(author)!.status === 'submitted' && latestReviews.get(author)!.verdict
              ? latestReviews.get(author)!.verdict
              : latestReviews.get(author)!.status"
          />
        </template>
        <span v-else class="muted reviewer-none">No review</span>
      </div>
    </section>

    <section v-if="isOpen" class="side-section">
      <h3 class="side-title">Agent review</h3>
      <p class="form-hint side-hint">
        An agent reads the change in its own sandbox and leaves line comments and a verdict.
      </p>
      <AgentModelPicker v-model:model="agentModel" v-model:effort="agentEffort" :agent-id="REVIEW_AGENT_ID" class="side-picker" />
      <label v-if="mr.taskId" class="form-check side-check">
        <input v-model="autoFix" type="checkbox" />
        Send requested changes to the task's agent
      </label>
      <button class="btn btn-secondary btn-sm side-button" :disabled="busy || pendingAgentReview || !agentModel" @click="startAgentReview">
        <span v-if="pendingAgentReview" class="spinner spinner-sm" />
        <MrIcon v-else name="bot" :size="13" />
        {{ pendingAgentReview ? 'Review in progress...' : 'Start agent review' }}
      </button>
    </section>

    <section v-if="isOpen" class="side-section">
      <h3 class="side-title">Actions</h3>
      <div v-if="confirmingClose" class="confirm">
        <p class="confirm-text">Close this merge request without merging? It can't be reopened; its branch stays.</p>
        <div class="confirm-actions">
          <button class="btn btn-secondary btn-sm" :disabled="busy" @click="confirmingClose = false">Keep open</button>
          <button class="btn btn-danger-solid btn-sm" :disabled="busy" @click="closeMergeRequest">Close</button>
        </div>
      </div>
      <div v-else class="action-list">
        <button class="action action-danger" :disabled="busy" @click="confirmingClose = true">
          <MrIcon name="closed" :size="13" />
          Close merge request
        </button>
      </div>
    </section>
  </aside>
</template>

<style scoped src="@/components/project/shared.css"></style>
<style scoped src="@/components/project/taskModal.css"></style>
<style scoped src="./mergerequest.css"></style>
<style scoped>
.side-col {
  overflow-y: auto;
}

.details {
  display: grid;
  grid-template-columns: auto minmax(0, 1fr);
  gap: var(--space-2) var(--space-3);
  font-size: 0.78rem;
}

.details dt {
  color: var(--text-muted);
}

.details dd {
  display: flex;
  align-items: center;
  gap: 6px;
  min-width: 0;
  color: var(--text-primary);
}

.mono {
  font-family: var(--font-mono);
}

.muted {
  color: var(--text-muted);
}

.warn {
  color: var(--warning);
}

.task-link {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  color: var(--accent-blue);
  font-size: inherit;
  text-decoration: none;
}

.task-link:hover {
  text-decoration: underline;
}

.reviewer {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  font-size: 0.8rem;
}

.reviewer + .reviewer {
  margin-top: var(--space-2);
}

.reviewer-name {
  flex: 1;
}

.reviewer-none {
  font-size: 0.72rem;
}

.side-section .side-hint {
  margin: 0 0 var(--space-3);
}

.side-picker {
  width: 100%;
  margin-bottom: var(--space-3);
}

.side-check {
  margin-bottom: var(--space-3);
}

.side-button {
  width: 100%;
}

.action-list {
  display: flex;
  flex-direction: column;
  gap: 2px;
  margin: 0 calc(var(--space-2) * -1);
}

.action {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  padding: 6px var(--space-2);
  border-radius: var(--radius-md);
  font-size: 0.78rem;
  color: var(--text-secondary);
  text-align: left;
  transition: all var(--transition-fast);
}

.action:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

.action-danger {
  color: var(--error);
}

.action-danger:hover:not(:disabled) {
  color: var(--error);
  background: var(--error-bg);
}

.confirm {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
  padding: var(--space-2) var(--space-3);
  border-radius: var(--radius-md);
  background: var(--error-bg);
  border: 0.5px solid var(--error-border);
}

.confirm-text {
  font-size: 0.75rem;
  color: var(--text-primary);
}

.confirm-actions {
  display: flex;
  justify-content: flex-end;
  gap: var(--space-2);
}

/* In one column the sidebar follows the main column, behind a hairline. */
@container (max-width: 900px) {
  .side-col {
    overflow: visible;
    border-left: none;
    border-top: 0.5px solid var(--border-default);
    padding-left: var(--space-4);
    padding-right: var(--space-4);
  }
}
</style>
