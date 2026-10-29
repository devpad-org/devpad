<script setup lang="ts">
import MrIcon from '@/components/project/MrIcon.vue'
import { fullTime, timeAgo } from '@/utils/time'
import { shortSha } from './format'
import { useMergeRequest } from './useMergeRequest'

// The merge widget for an open merge request: its checks, any conflicts, the
// strategy and the merge button. Merged and closed ones show the outcome.
const {
  mr,
  project,
  isOpen,
  busy,
  checks,
  conflicts,
  strategy,
  strategies,
  strategyHint,
  pushAfterMerge,
  mergeError,
  statusIcon,
  merge,
  resolveConflicts,
} = useMergeRequest()
</script>

<template>
  <template v-if="mr">
    <section v-if="isOpen" class="card merge-card" aria-label="Merge">
      <ul class="checks">
        <li v-for="check in checks" :key="check.key" class="check" :class="`tone-${check.tone}`">
          <span class="check-icon">
            <span v-if="check.spin" class="spinner spinner-sm" />
            <MrIcon v-else :name="check.icon" :size="14" />
          </span>
          <span class="check-text">
            <span class="check-title">{{ check.title }}</span>
            <span v-if="check.detail" class="check-detail">{{ check.detail }}</span>
          </span>
        </li>
      </ul>
      <div v-if="conflicts" class="conflicts">
        <ul class="conflict-list">
          <li v-for="file in conflicts" :key="file" class="mono">
            <MrIcon name="file" :size="12" />
            {{ file }}
          </li>
        </ul>
        <button v-if="mr.taskId" class="btn btn-secondary btn-sm" :disabled="busy" @click="resolveConflicts">
          <MrIcon name="bot" :size="13" />
          Ask the task's agent to resolve them
        </button>
      </div>
      <div class="merge-footer">
        <div class="merge-options">
          <div class="segmented" role="radiogroup" aria-label="Merge strategy">
            <button
              v-for="s in strategies"
              :key="s.value"
              type="button"
              role="radio"
              :aria-checked="strategy === s.value"
              class="segment"
              :class="{ active: strategy === s.value }"
              @click="strategy = s.value"
            >
              {{ s.label }}
            </button>
          </div>
          <span class="form-hint strategy-hint">{{ strategyHint }}</span>
        </div>
        <div class="merge-actions">
          <label v-if="project?.upstreamUrl" class="form-check">
            <input v-model="pushAfterMerge" type="checkbox" />
            Push {{ mr.targetBranch }} upstream
          </label>
          <button class="btn btn-success" :disabled="busy" @click="merge">
            <MrIcon name="merged" :size="14" />
            Merge into {{ mr.targetBranch }}
          </button>
        </div>
      </div>
      <p v-if="mergeError" class="form-error merge-error">{{ mergeError }}</p>
    </section>

    <section v-else class="card outcome-card" :class="`state-${mr.status}`">
      <span class="outcome-icon"><MrIcon :name="statusIcon" :size="18" /></span>
      <div v-if="mr.status === 'merged'">
        <strong>Merged into {{ mr.targetBranch }}</strong>
        <p class="muted outcome-detail">
          as <span class="mono">{{ shortSha(mr.mergeSha) }}</span>
          <span :title="fullTime(mr.mergedAt)"> &middot; {{ timeAgo(mr.mergedAt) }}</span>
        </p>
      </div>
      <div v-else>
        <strong>Closed without merging</strong>
        <p class="muted outcome-detail" :title="fullTime(mr.updatedAt)">{{ timeAgo(mr.updatedAt) }}</p>
      </div>
    </section>
  </template>
</template>

<style scoped src="@/components/project/shared.css"></style>
<style scoped src="@/assets/styles/segmented.css"></style>
<style scoped src="./mergerequest.css"></style>
<style scoped>
.merge-card {
  overflow: hidden;
}

.checks {
  list-style: none;
}

.check {
  display: flex;
  align-items: flex-start;
  gap: var(--space-3);
  padding: var(--space-3) var(--space-4);
}

.check + .check {
  border-top: 0.5px solid var(--border-hairline);
}

.check-icon {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 26px;
  height: 26px;
  border-radius: 50%;
  flex-shrink: 0;
  color: var(--text-secondary);
  background: var(--bg-neutral);
}

.check-text {
  display: flex;
  flex-direction: column;
  min-width: 0;
}

.check-title {
  font-size: 0.85rem;
  font-weight: 500;
}

.check-detail {
  font-size: 0.75rem;
  color: var(--text-muted);
}

.conflicts {
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  gap: var(--space-3);
  padding: 0 var(--space-4) var(--space-3) calc(var(--space-4) + 26px + var(--space-3));
}

.conflict-list {
  list-style: none;
  display: flex;
  flex-direction: column;
  gap: 2px;
  color: var(--text-secondary);
}

.conflict-list li {
  display: flex;
  align-items: center;
  gap: 6px;
}

.merge-footer {
  display: flex;
  align-items: center;
  justify-content: space-between;
  flex-wrap: wrap;
  gap: var(--space-3);
  padding: var(--space-3) var(--space-4);
  background: var(--bg-raised);
  border-top: 0.5px solid var(--border-default);
}

.merge-options {
  display: flex;
  flex-direction: column;
  gap: var(--space-1);
  min-width: 0;
}

.strategy-hint {
  margin: 0;
}

.merge-actions {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: var(--space-3);
}

.merge-error {
  margin: 0;
  padding: var(--space-2) var(--space-4);
  border-top: 0.5px solid var(--error-border);
  background: var(--error-bg);
}

.outcome-card {
  display: flex;
  align-items: center;
  gap: var(--space-3);
  padding: var(--space-3) var(--space-4);
  font-size: 0.85rem;
}

.outcome-card strong {
  font-weight: 500;
}

.outcome-icon {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 36px;
  height: 36px;
  border-radius: 50%;
  background: var(--bg-neutral);
}

.outcome-card.state-merged {
  border-color: var(--success-border);
  background: color-mix(in srgb, var(--success) 6%, var(--bg-surface));
}

.outcome-card.state-merged .outcome-icon {
  color: var(--success);
  background: var(--success-bg);
}

.outcome-detail {
  font-size: 0.75rem;
}
</style>
