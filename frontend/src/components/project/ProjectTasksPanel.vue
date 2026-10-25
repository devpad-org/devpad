<script setup lang="ts">
import { ref } from 'vue'
import type { Task } from '@/api/tasks'
import { useActiveProjectStore } from '@/stores/activeProject'
import NewTaskForm from './NewTaskForm.vue'
import StatusBadge from './StatusBadge.vue'

// The project's tasks in the IDE side bar, grouped like the board.
const emit = defineEmits<{ 'open-board': [] }>()

const store = useActiveProjectStore()
const showNewTask = ref(false)

function created(task: Task) {
  store.upsertTask(task)
  showNewTask.value = false
  void store.refresh()
}
</script>

<template>
  <div class="side-panel tasks-panel">
    <div class="side-header">
      <span class="side-title">
        <svg class="panel-icon" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
          <path d="m3 17 2 2 4-4" />
          <path d="m3 7 2 2 4-4" />
          <path d="M13 6h8" />
          <path d="M13 12h8" />
          <path d="M13 18h8" />
        </svg>
        Tasks
      </span>
      <div class="side-header-actions">
        <button class="icon-btn" title="Open board" aria-label="Open board" @click="emit('open-board')">
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
            <rect width="18" height="18" x="3" y="3" rx="2" />
            <path d="M9 3v18" />
            <path d="M15 3v18" />
          </svg>
        </button>
        <button
          class="icon-btn"
          :class="{ active: showNewTask }"
          :disabled="!store.ready"
          title="New task"
          aria-label="New task"
          @click="showNewTask = !showNewTask"
        >
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
            <path d="M12 5v14" />
            <path d="M5 12h14" />
          </svg>
        </button>
      </div>
    </div>

    <div class="side-body">
      <div v-if="showNewTask && store.projectId !== null" class="side-section">
        <NewTaskForm compact :project-id="store.projectId" @created="created" @cancel="showNewTask = false" />
      </div>

      <p v-if="!store.ready" class="side-section side-empty">Tasks appear once the project repository is ready.</p>

      <template v-else>
        <div v-for="column in store.columns" :key="column.title" class="side-section">
          <div class="side-section-title">
            <span>{{ column.title }}</span>
            <span class="side-count">{{ column.tasks.length }}</span>
          </div>
          <button
            v-for="task in column.tasks"
            :key="task.id"
            type="button"
            class="side-row"
            @click="store.selectedTaskId = task.id"
          >
            <span class="side-row-main">
              <span class="side-row-title">{{ task.title }}</span>
              <span class="side-row-meta">
                <span class="mono">#{{ task.id }}</span>
                <span v-if="store.mergeRequestByTask.get(task.id)">MR !{{ store.mergeRequestByTask.get(task.id)?.id }}</span>
                <span v-if="task.status === 'failed' && task.error" class="row-error">{{ task.error }}</span>
              </span>
            </span>
            <StatusBadge :status="task.status" />
          </button>
          <p v-if="column.tasks.length === 0" class="side-empty">Nothing here</p>
        </div>
      </template>
    </div>
  </div>
</template>

<style scoped src="./shared.css"></style>
<style scoped src="./panel.css"></style>
<style scoped>
.tasks-panel {
  --ide-header-icon: var(--accent-amber);
}

.row-error {
  color: var(--accent-rose);
  overflow: hidden;
  text-overflow: ellipsis;
}
</style>
