<script setup lang="ts">
import { computed, ref } from 'vue'
import type { MergeRequest, MergeRequestStatus } from '@/api/mergeRequests'
import { useActiveProjectStore } from '@/stores/activeProject'
import { fullTime, timeAgo } from '@/utils/time'
import MrIcon, { type MrIconName } from './MrIcon.vue'
import NewMergeRequestModal from './NewMergeRequestModal.vue'
import { TASK_STATUS } from './taskStatus'

// The project's merge requests as a table, one row each, on a surface of its
// own like the tasks. A row opens the merge request in its modal.
const store = useActiveProjectStore()

type Filter = MergeRequestStatus | 'all'
type SortKey = 'title' | 'status' | 'updated'

const FILTERS: { id: Filter; label: string }[] = [
  { id: 'open', label: 'Open' },
  { id: 'merged', label: 'Merged' },
  { id: 'closed', label: 'Closed' },
  { id: 'all', label: 'All' },
]

const STATUS: Record<MergeRequestStatus, { order: number; icon: MrIconName; label: string }> = {
  open: { order: 0, icon: 'open', label: 'Open' },
  merged: { order: 1, icon: 'merged', label: 'Merged' },
  closed: { order: 2, icon: 'closed', label: 'Closed' },
}

const filter = ref<Filter>('open')
const search = ref('')
const sortKey = ref<SortKey>('updated')
const sortDesc = ref(true)
const showNew = ref(false)

const counts = computed(() => {
  const out = { open: 0, merged: 0, closed: 0, all: store.mergeRequests.length } as Record<Filter, number>
  for (const mr of store.mergeRequests) out[mr.status]++
  return out
})

const taskById = computed(() => new Map(store.tasks.map((t) => [t.id, t])))

function matchesSearch(mr: MergeRequest, query: string): boolean {
  if (!query) return true
  const q = query.toLowerCase()
  return (
    mr.title.toLowerCase().includes(q) ||
    `!${mr.id}` === q ||
    String(mr.id) === q ||
    (mr.taskId > 0 && `#${mr.taskId}` === q) ||
    mr.sourceBranch.toLowerCase().includes(q) ||
    mr.targetBranch.toLowerCase().includes(q)
  )
}

function compare(a: MergeRequest, b: MergeRequest): number {
  switch (sortKey.value) {
    case 'title':
      return a.title.localeCompare(b.title)
    case 'status':
      return STATUS[a.status].order - STATUS[b.status].order || a.updatedAt.localeCompare(b.updatedAt)
    case 'updated':
      return a.updatedAt.localeCompare(b.updatedAt)
  }
}

const rows = computed(() => {
  const query = search.value.trim()
  const dir = sortDesc.value ? -1 : 1
  return store.mergeRequests
    .filter((mr) => (filter.value === 'all' || mr.status === filter.value) && matchesSearch(mr, query))
    .sort((a, b) => dir * compare(a, b) || b.id - a.id)
})

// Open merge requests whose task's agent is working on them again.
const agentWorking = computed(
  () =>
    store.openMergeRequests.filter((mr) => {
      const status = taskById.value.get(mr.taskId)?.status
      return status === 'queued' || status === 'in_progress'
    }).length,
)

function sortBy(key: SortKey) {
  if (sortKey.value === key) {
    sortDesc.value = !sortDesc.value
    return
  }
  sortKey.value = key
  // Text and the status order read best from the top; times newest first.
  sortDesc.value = key === 'updated'
}

function ariaSort(key: SortKey): 'ascending' | 'descending' | 'none' {
  if (sortKey.value !== key) return 'none'
  return sortDesc.value ? 'descending' : 'ascending'
}

function select(mr: MergeRequest) {
  store.selectedMergeRequestId = mr.id
}
</script>

<template>
  <section class="list-surface mr-surface" aria-label="Merge requests">
    <header class="list-header">
      <div class="list-title">
        <span class="list-title-icon">
          <MrIcon name="open" :size="16" :stroke-width="1.8" />
        </span>
        <span class="list-title-text">Merge requests</span>
        <span v-if="store.ready" class="list-summary">
          <span>{{ counts.open }} open</span>
          <span v-if="agentWorking > 0" class="summary-working">
            {{ agentWorking }} {{ agentWorking === 1 ? 'agent' : 'agents' }} addressing feedback
          </span>
          <span>{{ counts.merged }} merged</span>
        </span>
      </div>
      <button class="new-btn" type="button" :disabled="!store.ready" @click="showNew = true">
        <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
          <path d="M12 5v14" />
          <path d="M5 12h14" />
        </svg>
        New merge request
      </button>
    </header>

    <p v-if="!store.ready" class="list-empty">Merge requests appear once the project repository is ready.</p>

    <template v-else>
      <div class="list-toolbar">
        <div class="filters" role="group" aria-label="Show merge requests">
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
          <span class="sr-only">Search merge requests</span>
          <input v-model="search" type="search" placeholder="Search title, !id or branch" autocomplete="off" />
        </label>
      </div>

      <div class="list-scroll">
        <table v-if="rows.length > 0" class="list-table">
          <thead>
            <tr>
              <th class="col-mr" :aria-sort="ariaSort('title')">
                <button type="button" class="sort" :class="{ sorted: sortKey === 'title' }" @click="sortBy('title')">
                  Merge request
                  <MrIcon name="chevron-down" :size="11" class="sort-icon" :class="{ up: sortKey === 'title' && !sortDesc }" />
                </button>
              </th>
              <th class="col-branches">Branches</th>
              <th class="col-task">Task</th>
              <th class="col-status" :aria-sort="ariaSort('status')">
                <button type="button" class="sort" :class="{ sorted: sortKey === 'status' }" @click="sortBy('status')">
                  Status
                  <MrIcon name="chevron-down" :size="11" class="sort-icon" :class="{ up: sortKey === 'status' && !sortDesc }" />
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
              v-for="mr in rows"
              :key="mr.id"
              class="list-row"
              :class="{ selected: store.selectedMergeRequestId === mr.id }"
              @click="select(mr)"
            >
              <td class="col-mr">
                <button type="button" class="row-title" @click.stop="select(mr)">{{ mr.title }}</button>
                <div class="row-sub">
                  <span class="mono">!{{ mr.id }}</span>
                  <span :title="fullTime(mr.createdAt)">opened {{ timeAgo(mr.createdAt) }}</span>
                  <span v-if="mr.status === 'merged' && mr.mergedAt" :title="fullTime(mr.mergedAt)">merged {{ timeAgo(mr.mergedAt) }}</span>
                </div>
              </td>
              <td class="col-branches">
                <span class="branches" :title="`${mr.sourceBranch} → ${mr.targetBranch}`">
                  <span class="mono ellipsis branch-source">{{ mr.sourceBranch }}</span>
                  <MrIcon name="arrow-right" :size="11" class="branch-arrow" />
                  <span class="mono ellipsis branch-target">{{ mr.targetBranch }}</span>
                </span>
              </td>
              <td class="col-task">
                <button
                  v-if="mr.taskId"
                  type="button"
                  class="task-link"
                  :class="taskById.get(mr.taskId) ? `tone-${TASK_STATUS[taskById.get(mr.taskId)!.status].tone}` : ''"
                  :title="taskById.get(mr.taskId) ? `Open task #${mr.taskId}: ${taskById.get(mr.taskId)!.title}` : `Open task #${mr.taskId}`"
                  @click.stop="store.selectedTaskId = mr.taskId"
                >
                  <span class="mono">#{{ mr.taskId }}</span>
                  <template v-if="taskById.get(mr.taskId)">
                    <MrIcon :name="TASK_STATUS[taskById.get(mr.taskId)!.status].icon" :size="12" class="task-icon" />
                    <span class="ellipsis">{{ TASK_STATUS[taskById.get(mr.taskId)!.status].label }}</span>
                  </template>
                </button>
                <span v-else class="none">&mdash;</span>
              </td>
              <td class="col-status">
                <span class="state-pill" :class="`state-${mr.status}`">
                  <MrIcon :name="STATUS[mr.status].icon" :size="12" />
                  {{ STATUS[mr.status].label }}
                </span>
              </td>
              <td class="col-updated">
                <span :title="`Updated ${fullTime(mr.updatedAt)}\nOpened ${fullTime(mr.createdAt)}`">{{ timeAgo(mr.updatedAt) }}</span>
              </td>
            </tr>
          </tbody>
        </table>

        <div v-else-if="store.mergeRequests.length === 0" class="list-empty list-empty-first">
          <p>No merge requests yet. An agent opens one when it finishes a task, or open one from a branch yourself.</p>
          <button type="button" class="btn-primary-sm" @click="showNew = true">New merge request</button>
        </div>
        <p v-else class="list-empty">No merge requests match.</p>
      </div>
    </template>

    <NewMergeRequestModal :show="showNew" @close="showNew = false" />
  </section>
</template>

<style scoped src="./listTable.css"></style>
<style scoped>
.mr-surface {
  --ide-header-icon: var(--accent-blue);
}

.summary-working {
  color: var(--accent-purple);
}

.col-branches {
  width: 280px;
}

.col-task {
  width: 190px;
}

.col-status {
  width: 110px;
}

.branches {
  display: flex;
  align-items: center;
  gap: 6px;
  min-width: 0;
  font-size: 0.72rem;
}

.branch-source {
  color: var(--text-primary);
}

.branch-target {
  flex-shrink: 0;
  max-width: 45%;
}

.branch-arrow {
  flex-shrink: 0;
  color: var(--text-dim);
}

.task-link {
  --tone: var(--text-secondary);
  display: inline-flex;
  align-items: center;
  gap: 5px;
  max-width: 100%;
  padding: 1px var(--space-2);
  border-radius: var(--radius-sm);
  border: 0.5px solid var(--border-default);
  background: var(--bg-neutral);
  color: var(--text-secondary);
  font-size: 0.72rem;
  transition: border-color var(--transition-fast);
}

.task-link:hover {
  border-color: var(--border-active);
}

.task-icon {
  flex-shrink: 0;
  color: var(--tone);
}

.tone-active {
  --tone: var(--accent-blue);
}

.tone-review {
  --tone: var(--accent-purple);
}

.tone-attention {
  --tone: var(--attention);
}

.tone-error {
  --tone: var(--error);
}

.tone-success {
  --tone: var(--success);
}

.state-pill {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  padding: 1px var(--space-2) 1px 6px;
  border-radius: 999px;
  border: 0.5px solid transparent;
  font-size: 0.72rem;
  font-weight: 500;
  white-space: nowrap;
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

/* Narrower panes drop the columns that matter least. */
@container (max-width: 900px) {
  .col-task {
    display: none;
  }
}

/* A phone, or a narrow pane: each merge request's title over its branches
   and status. */
@container (max-width: 540px) {
  .list-row {
    grid-template-columns: minmax(0, 1fr) auto auto;
    grid-template-areas:
      'mr mr mr'
      'branches task status';
  }

  .list-table td.col-mr {
    grid-area: mr;
  }

  .list-table td.col-branches {
    grid-area: branches;
    min-width: 0;
  }

  .list-table td.col-task {
    display: block;
    grid-area: task;
  }

  .list-table td.col-task:has(.none),
  .task-link .ellipsis {
    display: none;
  }

  .list-table td.col-status {
    grid-area: status;
  }
}
</style>
