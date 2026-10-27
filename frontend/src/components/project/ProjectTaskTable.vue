<script setup lang="ts">
import { computed, ref } from 'vue'
import type { Task } from '@/api/tasks'
import type { MergeRequest } from '@/api/mergeRequests'
import { useActiveProjectStore } from '@/stores/activeProject'
import { formatUsd } from '@/utils/cost'
import { fullTime, timeAgo } from '@/utils/time'
import MrIcon from './MrIcon.vue'
import NewTaskModal from './NewTaskModal.vue'
import TaskProgress from './TaskProgress.vue'
import { TASK_STATUS } from './taskStatus'

// The project's tasks as a table, one row each, on a surface of its own.
// A row opens the task; "New task" opens the full form.
const store = useActiveProjectStore()

type Filter = 'open' | 'todo' | 'agent' | 'review' | 'done' | 'all'
type SortKey = 'title' | 'stage' | 'cost' | 'updated'

const FILTERS: { id: Filter; label: string }[] = [
  { id: 'open', label: 'Open' },
  { id: 'todo', label: 'To do' },
  { id: 'agent', label: 'Agent' },
  { id: 'review', label: 'Review' },
  { id: 'done', label: 'Done' },
  { id: 'all', label: 'All' },
]

const filter = ref<Filter>('open')
const search = ref('')
const sortKey = ref<SortKey>('updated')
const sortDesc = ref(true)
const showNewTask = ref(false)
// The task just created, highlighted until the next one.
const createdId = ref<number | null>(null)

function stage(task: Task): number {
  return TASK_STATUS[task.status].step
}

function matchesFilter(task: Task, f: Filter): boolean {
  switch (f) {
    case 'open':
      return stage(task) < 3
    case 'todo':
      return stage(task) === 0
    case 'agent':
      return stage(task) === 1
    case 'review':
      return stage(task) === 2
    case 'done':
      return stage(task) === 3
    case 'all':
      return true
  }
}

const counts = computed(() => {
  const out = {} as Record<Filter, number>
  for (const f of FILTERS) out[f.id] = store.tasks.filter((t) => matchesFilter(t, f.id)).length
  return out
})

function matchesSearch(task: Task, query: string): boolean {
  if (!query) return true
  const q = query.toLowerCase()
  return (
    task.title.toLowerCase().includes(q) ||
    `#${task.id}` === q ||
    String(task.id) === q ||
    task.branch.toLowerCase().includes(q)
  )
}

function compare(a: Task, b: Task): number {
  switch (sortKey.value) {
    case 'title':
      return a.title.localeCompare(b.title)
    case 'stage':
      return stage(a) - stage(b) || a.updatedAt.localeCompare(b.updatedAt)
    case 'cost':
      return a.costUsd - b.costUsd
    case 'updated':
      return a.updatedAt.localeCompare(b.updatedAt)
  }
}

const rows = computed(() => {
  const query = search.value.trim()
  const dir = sortDesc.value ? -1 : 1
  return store.tasks
    .filter((t) => matchesFilter(t, filter.value) && matchesSearch(t, query))
    .sort((a, b) => dir * compare(a, b) || b.id - a.id)
})

const working = computed(() => store.tasks.filter((t) => t.status === 'in_progress').length)
const spent = computed(() => store.tasks.reduce((sum, t) => sum + t.costUsd, 0))

function sortBy(key: SortKey) {
  if (sortKey.value === key) {
    sortDesc.value = !sortDesc.value
    return
  }
  sortKey.value = key
  // Text reads best A to Z; numbers and times largest first.
  sortDesc.value = key !== 'title'
}

function ariaSort(key: SortKey): 'ascending' | 'descending' | 'none' {
  if (sortKey.value !== key) return 'none'
  return sortDesc.value ? 'descending' : 'ascending'
}

function select(task: Task) {
  store.selectedTaskId = task.id
}

// A warning or the failure, under the title, where one explains the row.
function note(task: Task): string {
  if (!task.error) return ''
  return task.status === 'failed' || task.status === 'queued' || task.status === 'in_review' ? task.error : ''
}

function overBudget(task: Task): boolean {
  return task.budgetUsd > 0 && task.costUsd >= task.budgetUsd
}

function budgetShare(task: Task): number {
  return task.budgetUsd > 0 ? Math.min(1, task.costUsd / task.budgetUsd) : 0
}

function mergeRequestIcon(mr: MergeRequest) {
  return mr.status === 'merged' ? 'merged' : mr.status === 'closed' ? 'closed' : 'open'
}

function created(task: Task) {
  store.upsertTask(task)
  showNewTask.value = false
  createdId.value = task.id
  search.value = ''
  if (!matchesFilter(task, filter.value)) filter.value = 'open'
  void store.refresh()
}
</script>

<template>
  <section class="list-surface task-surface" aria-label="Tasks">
    <header class="list-header">
      <div class="list-title">
        <span class="list-title-icon">
          <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
            <path d="m3 17 2 2 4-4" />
            <path d="m3 7 2 2 4-4" />
            <path d="M13 6h8" />
            <path d="M13 12h8" />
            <path d="M13 18h8" />
          </svg>
        </span>
        <span class="list-title-text">Tasks</span>
        <span v-if="store.ready" class="list-summary">
          <span>{{ counts.open }} open</span>
          <span v-if="working > 0" class="summary-working">{{ working }} {{ working === 1 ? 'agent' : 'agents' }} working</span>
          <span :title="'What agents have spent on this project\'s tasks, counting only models with known prices'">{{ formatUsd(spent) }} spent</span>
        </span>
      </div>
      <button class="new-btn" type="button" :disabled="!store.ready || store.projectId === null" @click="showNewTask = true">
        <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
          <path d="M12 5v14" />
          <path d="M5 12h14" />
        </svg>
        New task
      </button>
    </header>

    <p v-if="!store.ready" class="list-empty">Tasks appear once the project repository is ready.</p>

    <template v-else>
      <div class="list-toolbar">
        <div class="filters" role="group" aria-label="Show tasks">
          <button
            v-for="f in FILTERS"
            :key="f.id"
            type="button"
            class="filter"
            :class="{ active: filter === f.id }"
            :aria-pressed="filter === f.id"
            @click="filter = f.id"
          >
            {{ f.label }}
            <span class="filter-count">{{ counts[f.id] }}</span>
          </button>
        </div>
        <label class="search">
          <MrIcon name="search" :size="13" />
          <span class="sr-only">Search tasks</span>
          <input v-model="search" type="search" placeholder="Search title, #id or branch" autocomplete="off" />
        </label>
      </div>

      <div class="list-scroll">
        <table v-if="rows.length > 0" class="list-table">
          <thead>
            <tr>
              <th class="col-task" :aria-sort="ariaSort('title')">
                <button type="button" class="sort" :class="{ sorted: sortKey === 'title' }" @click="sortBy('title')">
                  Task
                  <MrIcon name="chevron-down" :size="11" class="sort-icon" :class="{ up: sortKey === 'title' && !sortDesc }" />
                </button>
              </th>
              <th class="col-progress" :aria-sort="ariaSort('stage')">
                <button type="button" class="sort" :class="{ sorted: sortKey === 'stage' }" @click="sortBy('stage')">
                  Progress
                  <MrIcon name="chevron-down" :size="11" class="sort-icon" :class="{ up: sortKey === 'stage' && !sortDesc }" />
                </button>
              </th>
              <th class="col-mr">Merge request</th>
              <th class="col-agent">Agent</th>
              <th class="col-cost" :aria-sort="ariaSort('cost')">
                <button type="button" class="sort" :class="{ sorted: sortKey === 'cost' }" @click="sortBy('cost')">
                  Cost
                  <MrIcon name="chevron-down" :size="11" class="sort-icon" :class="{ up: sortKey === 'cost' && !sortDesc }" />
                </button>
              </th>
              <th class="col-updated" :aria-sort="ariaSort('updated')">
                <button type="button" class="sort" :class="{ sorted: sortKey === 'updated' }" @click="sortBy('updated')">
                  Updated
                  <MrIcon name="chevron-down" :size="11" class="sort-icon" :class="{ up: sortKey === 'updated' && !sortDesc }" />
                </button>
              </th>
            </tr>
          </thead>
          <tbody>
            <tr
              v-for="task in rows"
              :key="task.id"
              class="list-row"
              :class="{ selected: store.selectedTaskId === task.id, created: createdId === task.id }"
              @click="select(task)"
            >
              <td class="col-task">
                <button type="button" class="row-title" @click.stop="select(task)">{{ task.title }}</button>
                <div class="row-sub">
                  <span class="mono">#{{ task.id }}</span>
                  <span v-if="task.branch" class="mono ellipsis" :title="task.branch">{{ task.branch }}</span>
                  <span v-if="task.baseBranch" class="ellipsis" :title="`Started from ${task.baseBranch}`">from <span class="mono">{{ task.baseBranch }}</span></span>
                  <span v-if="task.handedOff">handed off</span>
                </div>
                <p v-if="note(task)" class="row-note" :class="{ 'row-note-error': task.status === 'failed' }" :title="task.error">
                  {{ note(task) }}
                </p>
              </td>
              <td class="col-progress">
                <TaskProgress :status="task.status" />
              </td>
              <td class="col-mr">
                <button
                  v-if="store.mergeRequestByTask.get(task.id)"
                  type="button"
                  class="mr-link"
                  :class="`mr-${store.mergeRequestByTask.get(task.id)!.status}`"
                  :title="`Open merge request !${store.mergeRequestByTask.get(task.id)!.id}`"
                  @click.stop="store.selectedMergeRequestId = store.mergeRequestByTask.get(task.id)!.id"
                >
                  <MrIcon :name="mergeRequestIcon(store.mergeRequestByTask.get(task.id)!)" :size="12" />
                  !{{ store.mergeRequestByTask.get(task.id)!.id }}
                </button>
                <span v-else class="none">&mdash;</span>
              </td>
              <td class="col-agent">
                <span v-if="task.model" class="agent ellipsis" :title="task.effort ? `${task.model} · ${task.effort}` : task.model">
                  {{ task.model }}<span v-if="task.effort" class="muted"> &middot; {{ task.effort }}</span>
                </span>
                <span v-else class="none">&mdash;</span>
              </td>
              <td class="col-cost">
                <span class="cost" :class="{ 'over-budget': overBudget(task) }">
                  <span class="cost-spent">{{ formatUsd(task.costUsd) }}</span>
                  <span v-if="task.budgetUsd > 0" class="cost-budget">of {{ formatUsd(task.budgetUsd) }}</span>
                </span>
                <span v-if="task.budgetUsd > 0" class="meter" aria-hidden="true">
                  <span class="meter-fill" :style="{ width: `${budgetShare(task) * 100}%` }" />
                </span>
              </td>
              <td class="col-updated">
                <span :title="`Updated ${fullTime(task.updatedAt)}\nCreated ${fullTime(task.createdAt)}`">{{ timeAgo(task.updatedAt) }}</span>
              </td>
            </tr>
          </tbody>
        </table>

        <div v-else-if="store.tasks.length === 0" class="list-empty list-empty-first">
          <p>No tasks yet. Describe a change and hand it to an agent, or keep it here to do later.</p>
          <button type="button" class="btn-primary-sm" @click="showNewTask = true">New task</button>
        </div>
        <p v-else class="list-empty">No tasks match.</p>
      </div>
    </template>

    <NewTaskModal
      v-if="store.projectId !== null"
      :show="showNewTask"
      :project-id="store.projectId"
      @close="showNewTask = false"
      @created="created"
    />
  </section>
</template>

<style scoped src="./listTable.css"></style>
<style scoped>
.task-surface {
  --ide-header-icon: var(--accent-amber);
}

.summary-working {
  color: var(--accent-blue);
}

.col-progress {
  width: 190px;
}

.col-mr {
  width: 120px;
}

.col-agent {
  width: 170px;
}

.col-cost {
  width: 130px;
}

.mr-link {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  padding: 1px var(--space-2);
  border-radius: var(--radius-sm);
  font-family: var(--font-mono);
  font-size: 0.72rem;
  color: var(--text-secondary);
  background: var(--bg-neutral);
  border: 0.5px solid var(--border-default);
  transition: border-color var(--transition-fast);
}

.mr-link:hover {
  border-color: var(--border-active);
}

.mr-open {
  color: var(--accent-blue);
  background: color-mix(in srgb, var(--accent-blue) 12%, transparent);
  border-color: color-mix(in srgb, var(--accent-blue) 30%, transparent);
}

.mr-merged {
  color: var(--success);
  background: var(--success-bg);
  border-color: var(--success-border);
}

.agent {
  display: block;
  font-size: 0.75rem;
}

.cost {
  display: flex;
  align-items: baseline;
  gap: 4px;
  white-space: nowrap;
}

.cost-spent {
  color: var(--text-primary);
  font-variant-numeric: tabular-nums;
}

.cost-budget {
  font-size: 0.7rem;
  color: var(--text-muted);
}

.over-budget .cost-spent,
.over-budget .cost-budget {
  color: var(--error);
}

.meter {
  display: block;
  height: 3px;
  margin-top: 4px;
  border-radius: 2px;
  background: var(--bg-raised);
  overflow: hidden;
}

.meter-fill {
  display: block;
  height: 100%;
  border-radius: 2px;
  background: var(--accent-blue);
}

.over-budget + .meter .meter-fill {
  background: var(--error);
}

/* Narrower panes drop the columns that matter least. */
@container (max-width: 900px) {
  .col-agent {
    display: none;
  }
}

/* A phone, or a narrow pane: each task's title over its progress, merge
   request and cost. */
@container (max-width: 540px) {
  .list-row {
    grid-template-columns: minmax(0, 1fr) auto auto;
    grid-template-areas:
      'task task task'
      'progress mr cost';
  }

  .list-table td.col-task {
    grid-area: task;
  }

  .list-table td.col-progress {
    grid-area: progress;
    min-width: 0;
  }

  .list-table td.col-mr {
    grid-area: mr;
  }

  .list-table td.col-mr:has(.none) {
    display: none;
  }

  .list-table td.col-cost {
    grid-area: cost;
  }

  .list-table td.col-cost .cost {
    justify-content: flex-end;
  }

  .list-table td.col-agent {
    display: none;
  }
}
</style>
