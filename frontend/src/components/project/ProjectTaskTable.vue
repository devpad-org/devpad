<script setup lang="ts">
import { computed, ref } from 'vue'
import type { Task } from '@/api/tasks'
import type { MergeRequest } from '@/api/mergeRequests'
import { useOpenMergeRequest } from '@/components/ide/viewTabs'
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
const openMergeRequest = useOpenMergeRequest()

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
  <section class="task-table-surface" aria-label="Tasks">
    <header class="tasks-header">
      <div class="tasks-title">
        <span class="tasks-title-icon">
          <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
            <path d="m3 17 2 2 4-4" />
            <path d="m3 7 2 2 4-4" />
            <path d="M13 6h8" />
            <path d="M13 12h8" />
            <path d="M13 18h8" />
          </svg>
        </span>
        <span class="tasks-title-text">Tasks</span>
        <span v-if="store.ready" class="tasks-summary">
          <span>{{ counts.open }} open</span>
          <span v-if="working > 0" class="summary-working">{{ working }} {{ working === 1 ? 'agent' : 'agents' }} working</span>
          <span :title="'What agents have spent on this project\'s tasks, counting only models with known prices'">{{ formatUsd(spent) }} spent</span>
        </span>
      </div>
      <button class="new-task-btn" type="button" :disabled="!store.ready || store.projectId === null" @click="showNewTask = true">
        <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
          <path d="M12 5v14" />
          <path d="M5 12h14" />
        </svg>
        New task
      </button>
    </header>

    <p v-if="!store.ready" class="tasks-empty">Tasks appear once the project repository is ready.</p>

    <template v-else>
      <div class="tasks-toolbar">
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

      <div class="tasks-scroll">
        <table v-if="rows.length > 0" class="tasks-table">
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
              class="task-row"
              :class="{ selected: store.selectedTaskId === task.id, created: createdId === task.id }"
              @click="select(task)"
            >
              <td class="col-task">
                <button type="button" class="task-title" @click.stop="select(task)">{{ task.title }}</button>
                <div class="task-sub">
                  <span class="mono">#{{ task.id }}</span>
                  <span v-if="task.branch" class="mono ellipsis" :title="task.branch">{{ task.branch }}</span>
                  <span v-if="task.baseBranch" class="ellipsis" :title="`Started from ${task.baseBranch}`">from <span class="mono">{{ task.baseBranch }}</span></span>
                  <span v-if="task.handedOff">handed off</span>
                </div>
                <p v-if="note(task)" class="task-note" :class="{ 'task-note-error': task.status === 'failed' }" :title="task.error">
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
                  @click.stop="openMergeRequest(store.mergeRequestByTask.get(task.id)!.id)"
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

        <div v-else-if="store.tasks.length === 0" class="tasks-empty tasks-empty-first">
          <p>No tasks yet. Describe a change and hand it to an agent, or keep it here to do later.</p>
          <button type="button" class="btn-primary-sm" @click="showNewTask = true">New task</button>
        </div>
        <p v-else class="tasks-empty">No tasks match.</p>
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

<style scoped>
.task-table-surface {
  --ide-header-icon: var(--accent-amber);
  display: flex;
  flex-direction: column;
  height: 100%;
  overflow: hidden;
  background: var(--ide-surface-bg);
  container-type: inline-size;
}

/* Header, matching the Git Graph's */
.tasks-header {
  display: flex;
  align-items: center;
  gap: var(--space-3);
  height: 38px;
  padding: 0 var(--space-3);
  border-bottom: 0.5px solid var(--border-default);
  background: var(--ide-header-bg);
  flex-shrink: 0;
}

.tasks-title {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  flex: 1;
  min-width: 0;
  font-size: var(--ide-header-title-size);
  color: var(--text-primary);
}

.tasks-title-icon {
  color: var(--ide-header-icon);
  line-height: 0;
}

.tasks-title-text {
  font-weight: 600;
}

.tasks-summary {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  min-width: 0;
  padding-left: var(--space-2);
  border-left: 0.5px solid var(--border-default);
  color: var(--text-secondary);
  overflow: hidden;
  white-space: nowrap;
}

.tasks-summary > span + span::before {
  content: '·';
  margin-right: var(--space-2);
  color: var(--text-dim);
}

.summary-working {
  color: var(--accent-blue);
}

.new-task-btn,
.btn-primary-sm {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  height: 24px;
  padding: 0 var(--space-2);
  border-radius: var(--radius-md);
  background: var(--accent-blue);
  color: var(--bg-primary);
  font-size: 0.72rem;
  font-weight: 500;
  white-space: nowrap;
  transition: background var(--transition-fast);
}

.new-task-btn:hover:not(:disabled),
.btn-primary-sm:hover {
  background: color-mix(in srgb, var(--accent-blue) 85%, var(--gruvbox-fg0));
}

.new-task-btn:disabled {
  opacity: 0.45;
  cursor: default;
}

/* Filters and search */
.tasks-toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  flex-wrap: wrap;
  gap: var(--space-2) var(--space-3);
  padding: var(--space-2) var(--space-3);
  border-bottom: 0.5px solid var(--border-hairline);
  flex-shrink: 0;
}

.filters {
  display: flex;
  flex-wrap: wrap;
  gap: 2px;
}

.filter {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  height: 26px;
  padding: 0 var(--space-2);
  border-radius: var(--radius-md);
  font-size: 0.75rem;
  color: var(--text-secondary);
  transition: all var(--transition-fast);
}

.filter:hover {
  color: var(--text-primary);
  background: var(--bg-hover);
}

.filter.active {
  color: var(--text-primary);
  background: var(--bg-raised);
}

.filter-count {
  min-width: 16px;
  padding: 0 4px;
  border-radius: 8px;
  background: var(--bg-neutral);
  color: var(--text-muted);
  font-size: 0.65rem;
  line-height: 16px;
  text-align: center;
}

.filter.active .filter-count {
  color: var(--text-secondary);
}

.search {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  width: 240px;
  max-width: 100%;
  height: 26px;
  padding: 0 var(--space-2);
  border: 0.5px solid var(--border-default);
  border-radius: var(--radius-md);
  background: var(--bg-primary);
  color: var(--text-muted);
  transition: border-color var(--transition-fast);
}

.search:focus-within {
  border-color: var(--accent-blue);
}

.search input {
  flex: 1;
  min-width: 0;
  background: transparent;
  border: none;
  color: var(--text-primary);
  font-family: inherit;
  font-size: 0.78rem;
}

.search input:focus {
  outline: none;
}

/* Table */
.tasks-scroll {
  flex: 1;
  overflow: auto;
  min-height: 0;
}

.tasks-table {
  width: 100%;
  border-collapse: collapse;
  table-layout: fixed;
  font-size: 0.8rem;
}

.tasks-table th {
  position: sticky;
  top: 0;
  z-index: 1;
  padding: 0 var(--space-3);
  height: 30px;
  background: var(--ide-surface-bg);
  border-bottom: 0.5px solid var(--border-default);
  font-size: 0.7rem;
  font-weight: 500;
  color: var(--text-muted);
  text-align: left;
  white-space: nowrap;
}

.sort {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  color: inherit;
  font: inherit;
}

.sort:hover,
.sort.sorted {
  color: var(--text-primary);
}

.sort-icon {
  opacity: 0;
  transition: transform var(--transition-fast), opacity var(--transition-fast);
}

.sort:hover .sort-icon,
.sort.sorted .sort-icon {
  opacity: 1;
}

.sort-icon.up {
  transform: rotate(180deg);
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

.col-updated {
  width: 96px;
}

.tasks-table td {
  padding: var(--space-2) var(--space-3);
  border-bottom: 0.5px solid var(--border-hairline);
  vertical-align: middle;
  color: var(--text-secondary);
}

.task-row {
  cursor: pointer;
  transition: background var(--transition-fast);
}

.task-row:hover {
  background: var(--bg-hover);
}

.task-row.selected {
  background: color-mix(in srgb, var(--accent-blue) 8%, transparent);
}

.task-row.created {
  animation: created 2.4s ease-out;
}

@keyframes created {
  from {
    background: color-mix(in srgb, var(--accent-blue) 18%, transparent);
  }
}

@media (prefers-reduced-motion: reduce) {
  .task-row.created {
    animation: none;
  }
}

.task-title {
  display: block;
  max-width: 100%;
  color: var(--text-primary);
  font-weight: 500;
  font-size: 0.82rem;
  text-align: left;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.task-title:focus-visible {
  outline: 1px solid var(--accent-blue);
  outline-offset: 2px;
  border-radius: var(--radius-sm);
}

.task-sub {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  min-width: 0;
  margin-top: 2px;
  font-size: 0.7rem;
  color: var(--text-muted);
  white-space: nowrap;
  overflow: hidden;
}

.task-note {
  margin-top: 2px;
  font-size: 0.72rem;
  color: var(--text-muted);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.task-note-error {
  color: var(--error);
}

.mono {
  font-family: var(--font-mono);
}

.muted {
  color: var(--text-muted);
}

.ellipsis {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.none {
  color: var(--text-dim);
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

.col-updated {
  white-space: nowrap;
  font-size: 0.75rem;
}

.tasks-empty {
  padding: var(--space-6) var(--space-4);
  color: var(--text-muted);
  font-size: 0.8rem;
  text-align: center;
}

.tasks-empty-first {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: var(--space-3);
}

.sr-only {
  position: absolute;
  width: 1px;
  height: 1px;
  overflow: hidden;
  clip: rect(0 0 0 0);
  white-space: nowrap;
}

/* Narrower panes drop the columns that matter least. */
@container (max-width: 900px) {
  .col-agent {
    display: none;
  }
}

@container (max-width: 720px) {
  .col-updated {
    display: none;
  }

  .tasks-summary > span:not(:first-child) {
    display: none;
  }
}

/* A phone, or a narrow pane: each task is a two-line row, its title over
   its progress, merge request and cost. */
@container (max-width: 540px) {
  .tasks-table,
  .tasks-table tbody {
    display: block;
  }

  .tasks-table thead {
    display: none;
  }

  .task-row {
    display: grid;
    grid-template-columns: minmax(0, 1fr) auto auto;
    grid-template-areas:
      'task task task'
      'progress mr cost';
    align-items: center;
    column-gap: var(--space-3);
    row-gap: var(--space-2);
    padding: var(--space-3);
    border-bottom: 0.5px solid var(--border-hairline);
  }

  .tasks-table td {
    display: block;
    width: auto;
    padding: 0;
    border: none;
  }

  .tasks-table td.col-task {
    grid-area: task;
  }

  .tasks-table td.col-progress {
    grid-area: progress;
    min-width: 0;
  }

  .tasks-table td.col-mr {
    grid-area: mr;
  }

  .tasks-table td.col-mr:has(.none) {
    display: none;
  }

  .tasks-table td.col-cost {
    grid-area: cost;
  }

  .tasks-table td.col-cost .cost {
    justify-content: flex-end;
  }

  .tasks-table td.col-agent,
  .tasks-table td.col-updated {
    display: none;
  }

  .task-title {
    white-space: normal;
    overflow-wrap: anywhere;
  }

  .search {
    width: 100%;
  }

  .filters {
    flex-wrap: nowrap;
    width: 100%;
    overflow-x: auto;
    scrollbar-width: none;
  }

  .filter {
    height: 32px;
    flex-shrink: 0;
  }
}
</style>
