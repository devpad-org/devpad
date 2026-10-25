<script setup lang="ts">
import { ref } from 'vue'
import type { Task } from '@/api/tasks'
import { useActiveProjectStore } from '@/stores/activeProject'
import NewTaskForm from './NewTaskForm.vue'
import TaskCard from './TaskCard.vue'

// The project's task board, shown as an editor tab.
const store = useActiveProjectStore()
const showNewTask = ref(false)

function created(task: Task) {
  store.upsertTask(task)
  showNewTask.value = false
  void store.refresh()
}
</script>

<template>
  <div class="board-tab">
    <div class="board-inner">
      <div class="section-header">
        <h2 class="section-title">Tasks</h2>
        <button v-if="store.ready" class="btn btn-primary btn-sm" @click="showNewTask = !showNewTask">
          {{ showNewTask ? 'Close' : 'New task' }}
        </button>
      </div>

      <p v-if="!store.ready" class="empty-text">Tasks appear once the project repository is ready.</p>

      <template v-else>
        <NewTaskForm
          v-if="showNewTask && store.projectId !== null"
          class="board-new-task"
          :project-id="store.projectId"
          @created="created"
        />

        <div class="board">
          <div v-for="column in store.columns" :key="column.title" class="column">
            <div class="column-header">
              <span>{{ column.title }}</span>
              <span class="column-count">{{ column.tasks.length }}</span>
            </div>
            <div class="column-body">
              <TaskCard
                v-for="task in column.tasks"
                :key="task.id"
                :task="task"
                :merge-request="store.mergeRequestByTask.get(task.id)"
                @select="store.selectedTaskId = $event.id"
              />
              <p v-if="column.tasks.length === 0" class="empty-text">Nothing here</p>
            </div>
          </div>
        </div>
      </template>
    </div>
  </div>
</template>

<style scoped src="./shared.css"></style>
<style scoped>
.board-tab {
  height: 100%;
  overflow-y: auto;
  container-type: inline-size;
}

.board-inner {
  padding: var(--space-4);
}

.board-new-task {
  margin-bottom: var(--space-4);
}

.board {
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  gap: var(--space-3);
}

@container (max-width: 860px) {
  .board {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
}

/* One column under another on a phone; an empty column needs no height. */
@container (max-width: 480px) {
  .board {
    grid-template-columns: minmax(0, 1fr);
  }

  .board .column {
    min-height: 0;
  }
}

.column {
  display: flex;
  flex-direction: column;
  background: var(--bg-surface);
  border: 0.5px solid var(--border-default);
  border-radius: var(--radius-lg);
  min-height: 160px;
}

.column-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: var(--space-2) var(--space-3);
  font-size: 0.75rem;
  font-weight: 500;
  color: var(--text-secondary);
  border-bottom: 0.5px solid var(--border-default);
}

.column-count {
  color: var(--text-muted);
}

.column-body {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
  padding: var(--space-2);
}

.column-body .empty-text {
  text-align: center;
}
</style>
