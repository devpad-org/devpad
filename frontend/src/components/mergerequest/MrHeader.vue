<script setup lang="ts">
import { computed } from 'vue'
import MrIcon from '@/components/project/MrIcon.vue'
import { fullTime, timeAgo } from '@/utils/time'
import MrSendBackPopover from './MrSendBackPopover.vue'
import { useMergeRequest } from './useMergeRequest'

// The merge request's title, state and branches, with the actions that move
// it along: review again, send back to the task's agent, close.
const { mr, isOpen, busy, statusIcon, pendingAgentReview, latestAgentReview, canSendBack, taskWorking, close, rereview } = useMergeRequest()

// The task's agent is about to change the branch, so a review now would be
// of a commit on its way out.
const rereviewHint = computed(() => {
  if (pendingAgentReview.value) return 'A review agent is reviewing it'
  if (taskWorking.value) return "Wait for the task's agent to finish its changes"
  return `Review the latest commit again with ${latestAgentReview.value?.model}`
})
</script>

<template>
  <header v-if="mr" class="mr-header">
    <div class="mr-heading">
      <h1 class="page-title mr-title">
        {{ mr.title }}
        <span class="mr-number">!{{ mr.id }}</span>
      </h1>
      <div class="mr-summary">
        <span class="state-pill" :class="`state-${mr.status}`">
          <MrIcon :name="statusIcon" :size="13" />
          {{ mr.status }}
        </span>
        <span class="branch-chip" :title="mr.sourceBranch">
          <MrIcon name="branch" :size="12" />
          {{ mr.sourceBranch }}
        </span>
        <MrIcon name="arrow-right" :size="13" class="muted" />
        <span class="branch-chip" :title="mr.targetBranch">
          <MrIcon name="branch" :size="12" />
          {{ mr.targetBranch }}
        </span>
        <span class="summary-text" :title="fullTime(mr.createdAt)">opened {{ timeAgo(mr.createdAt) }}</span>
      </div>
    </div>
    <div v-if="isOpen" class="header-actions">
      <button
        v-if="latestAgentReview"
        class="btn btn-secondary"
        :disabled="busy || pendingAgentReview || taskWorking"
        :title="rereviewHint"
        @click="rereview"
      >
        <span v-if="pendingAgentReview" class="spinner spinner-sm" />
        <MrIcon v-else name="retry" :size="14" />
        Re-review
      </button>
      <MrSendBackPopover v-if="canSendBack" />
      <button class="btn btn-secondary" :disabled="busy" @click="close">Close merge request</button>
    </div>
  </header>
</template>

<style scoped src="@/components/project/shared.css"></style>
<style scoped src="./mergerequest.css"></style>
<style scoped>
.mr-header {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  flex-wrap: wrap;
  gap: var(--space-3);
  margin-bottom: var(--space-4);
}

.mr-heading {
  min-width: 0;
  flex: 1;
}

.mr-title {
  font-size: 1.25rem;
}

.mr-number {
  color: var(--text-muted);
  font-weight: 400;
}

.mr-summary {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: var(--space-2);
  margin-top: var(--space-2);
  font-size: 0.78rem;
  color: var(--text-secondary);
}

.state-pill {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  padding: 3px 10px 3px 8px;
  border-radius: 999px;
  font-size: 0.75rem;
  font-weight: 600;
  text-transform: capitalize;
  border: 0.5px solid transparent;
}

.state-open {
  color: var(--accent-blue);
  background: color-mix(in srgb, var(--accent-blue) 14%, transparent);
  border-color: color-mix(in srgb, var(--accent-blue) 32%, transparent);
}

.state-merged {
  color: var(--success);
  background: var(--success-bg);
  border-color: var(--success-border);
}

.state-closed {
  color: var(--text-secondary);
  background: var(--bg-neutral);
  border-color: var(--border-default);
}

.branch-chip {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  max-width: 260px;
  padding: 2px var(--space-2);
  border-radius: var(--radius-sm);
  background: var(--bg-raised);
  border: 0.5px solid var(--border-default);
  color: var(--text-primary);
  font-family: var(--font-mono);
  font-size: 0.72rem;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.branch-chip .mr-icon {
  color: var(--text-muted);
}

.summary-text {
  color: var(--text-muted);
}
</style>
