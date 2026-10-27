<script setup lang="ts">
import { computed } from 'vue'
import MrIcon from '@/components/project/MrIcon.vue'
import { fullTime, timeAgo } from '@/utils/time'
import MrSendBackPopover from './MrSendBackPopover.vue'
import { useMergeRequest } from './useMergeRequest'

// The modal's header: the merge request's title, state and branches, with
// the actions that move it along: review again, send back to the task's agent.
const emit = defineEmits<{ close: [] }>()

const { mr, isOpen, busy, statusIcon, pendingAgentReview, latestAgentReview, canSendBack, taskWorking, rereview } = useMergeRequest()

// The task's agent is about to change the branch, so a review now would be
// of a commit on its way out.
const rereviewHint = computed(() => {
  if (pendingAgentReview.value) return 'A review agent is reviewing it'
  if (taskWorking.value) return "Wait for the task's agent to finish its changes"
  return `Review the latest commit again with ${latestAgentReview.value?.model}`
})
</script>

<template>
  <header v-if="mr" class="modal-header">
    <div class="heading">
      <span class="eyebrow">
        <MrIcon name="open" :size="12" />
        Merge request !{{ mr.id }}
      </span>
      <h2 id="mr-modal-title" class="modal-title">{{ mr.title }}</h2>
      <div class="summary">
        <span class="state-pill" :class="`state-${mr.status}`">
          <MrIcon :name="statusIcon" :size="12" />
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
        <span class="summary-item" :title="fullTime(mr.createdAt)">
          <MrIcon name="clock" :size="12" />
          opened {{ timeAgo(mr.createdAt) }}
        </span>
      </div>
    </div>
    <div v-if="isOpen && (latestAgentReview || canSendBack)" class="header-actions">
      <button
        v-if="latestAgentReview"
        class="btn btn-secondary btn-sm"
        :disabled="busy || pendingAgentReview || taskWorking"
        :title="rereviewHint"
        @click="rereview"
      >
        <span v-if="pendingAgentReview" class="spinner spinner-sm" />
        <MrIcon v-else name="retry" :size="13" />
        Re-review
      </button>
      <MrSendBackPopover v-if="canSendBack" />
    </div>
    <button class="modal-close" aria-label="Close" title="Close (Esc)" @click="emit('close')">
      <MrIcon name="close" :size="16" />
    </button>
  </header>
</template>

<style scoped src="@/components/project/shared.css"></style>
<style scoped src="@/components/project/taskModal.css"></style>
<style scoped src="./mergerequest.css"></style>
<style scoped>
.modal-title {
  margin: var(--space-1) 0 var(--space-2);
  font-size: 1.1rem;
}

.summary {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: var(--space-2);
  font-size: 0.75rem;
  color: var(--text-secondary);
}

.summary-item {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  margin-left: var(--space-1);
}

.summary-item .mr-icon {
  color: var(--text-muted);
}

.state-pill {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  padding: 1px var(--space-2) 1px 6px;
  border-radius: 999px;
  border: 0.5px solid transparent;
  font-size: 0.72rem;
  font-weight: 600;
  text-transform: capitalize;
}

.state-open {
  color: var(--accent-blue);
  background: color-mix(in srgb, var(--accent-blue) 12%, transparent);
  border-color: color-mix(in srgb, var(--accent-blue) 30%, transparent);
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
  max-width: 280px;
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

.muted {
  color: var(--text-muted);
}

.header-actions {
  flex-wrap: nowrap;
  margin-top: 2px;
}

@container (max-width: 640px) {
  .modal-header {
    flex-wrap: wrap;
  }

  /* The actions take their own row under the title; close stays top right. */
  .header-actions {
    order: 1;
    flex-basis: 100%;
    flex-wrap: wrap;
  }
}
</style>
