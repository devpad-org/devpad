<script setup lang="ts">
import type { Task } from '@/api/tasks'
import type { MergeRequest } from '@/api/mergeRequests'
import StatusBadge from './StatusBadge.vue'

defineProps<{
  task: Task
  mergeRequest?: MergeRequest
}>()

const emit = defineEmits<{ select: [task: Task] }>()
</script>

<template>
  <button type="button" class="task-card" @click="emit('select', task)">
    <div class="task-top">
      <span class="task-title">{{ task.title }}</span>
      <StatusBadge :status="task.status" />
    </div>
    <p v-if="task.status === 'failed' && task.error" class="task-error">{{ task.error }}</p>
    <p v-else-if="task.status === 'queued' && task.error" class="task-note">{{ task.error }}</p>
    <div class="task-meta">
      <span class="mono">#{{ task.id }}</span>
      <span v-if="task.branch" class="mono task-branch">{{ task.branch }}</span>
      <span v-if="mergeRequest" class="task-mr">MR !{{ mergeRequest.id }}</span>
      <span v-if="task.model" class="task-model">{{ task.model }}</span>
    </div>
  </button>
</template>

<style scoped>
.task-card {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
  width: 100%;
  text-align: left;
  padding: var(--space-3);
  background: var(--bg-base);
  border: 0.5px solid var(--border-default);
  border-radius: var(--radius-md);
  color: var(--text-primary);
  transition: border-color var(--transition-fast);
}

.task-card:hover {
  border-color: var(--border-active);
}

.task-top {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: var(--space-2);
}

.task-title {
  font-size: 0.8rem;
  font-weight: 500;
  overflow-wrap: anywhere;
}

.task-error,
.task-note {
  font-size: 0.72rem;
  overflow-wrap: anywhere;
  display: -webkit-box;
  -webkit-line-clamp: 3;
  -webkit-box-orient: vertical;
  overflow: hidden;
}

.task-error {
  color: var(--error);
}

.task-note {
  color: var(--text-muted);
}

.task-meta {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-2);
  font-size: 0.7rem;
  color: var(--text-muted);
}

.mono {
  font-family: var(--font-mono);
}

.task-branch {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.task-mr {
  color: var(--accent-purple);
}
</style>
