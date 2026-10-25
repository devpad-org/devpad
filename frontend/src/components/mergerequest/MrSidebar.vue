<script setup lang="ts">
import AgentModelPicker from '@/components/project/AgentModelPicker.vue'
import MrIcon from '@/components/project/MrIcon.vue'
import StatusBadge from '@/components/project/StatusBadge.vue'
import { useActiveProjectStore } from '@/stores/activeProject'
import { fullTime, timeAgo } from '@/utils/time'
import { authorName, plural, shortSha } from './format'
import { useMergeRequest } from './useMergeRequest'

// The merge request's details, its reviewers, and the agent review controls.
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
} = useMergeRequest()

const projectStore = useActiveProjectStore()

function openTask(id: number) {
  projectStore.selectedTaskId = id
}
</script>

<template>
  <aside v-if="mr" class="side-col">
    <section class="card side-card">
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
            <button class="task-link" @click="openTask(mr.taskId)">
              <MrIcon name="task" :size="12" />
              #{{ mr.taskId }}
            </button>
          </dd>
        </template>
        <dt>Opened</dt>
        <dd :title="fullTime(mr.createdAt)">{{ timeAgo(mr.createdAt) }}</dd>
        <dt>Updated</dt>
        <dd :title="fullTime(mr.updatedAt)">{{ timeAgo(mr.updatedAt) }}</dd>
      </dl>
    </section>

    <section class="card side-card">
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

    <section v-if="isOpen" class="card side-card">
      <h3 class="side-title">
        <MrIcon name="bot" :size="14" />
        Agent review
      </h3>
      <p class="form-hint side-hint">
        An agent reads the change in its own sandbox and leaves line comments and a verdict.
      </p>
      <AgentModelPicker v-model:model="agentModel" v-model:effort="agentEffort" class="side-picker" />
      <label v-if="mr.taskId" class="form-check side-check">
        <input v-model="autoFix" type="checkbox" />
        Send requested changes to the task's agent
      </label>
      <button class="btn btn-secondary side-button" :disabled="busy || pendingAgentReview || !agentModel" @click="startAgentReview">
        <span v-if="pendingAgentReview" class="spinner spinner-sm" />
        {{ pendingAgentReview ? 'Review in progress...' : 'Start agent review' }}
      </button>
    </section>
  </aside>
</template>

<style scoped src="@/components/project/shared.css"></style>
<style scoped src="./mergerequest.css"></style>
<style scoped>
.side-col {
  display: flex;
  flex-direction: column;
  gap: var(--space-3);
  position: sticky;
  top: var(--space-4);
}

.side-card {
  padding: var(--space-3) var(--space-4);
}

.side-title {
  display: flex;
  align-items: center;
  gap: 6px;
  font-size: 0.72rem;
  font-weight: 600;
  letter-spacing: 0.04em;
  text-transform: uppercase;
  color: var(--text-muted);
  margin-bottom: var(--space-3);
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

.warn {
  color: var(--warning);
}

.task-link {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  color: var(--accent-blue);
  font-size: inherit;
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

.side-hint {
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

@container (max-width: 900px) {
  .side-col {
    position: static;
  }
}
</style>
