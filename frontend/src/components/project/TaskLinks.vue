<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { taskApi, type Task, type TaskLinkKind } from '@/api/tasks'
import DocLink from '@/components/DocLink.vue'
import SelectMenu, { type SelectMenuOption } from '@/components/SelectMenu.vue'
import { useActiveProjectStore } from '@/stores/activeProject'
import MrIcon from './MrIcon.vue'
import { TASK_STATUS, canWaitFrom } from './taskStatus'

// A task's links to the project's other tasks, in the task modal's sidebar:
// the tasks it waits for, those waiting for it, and related ones. A row
// opens its task; the form adds a link, and removing one may let a task
// left to start when unblocked go ahead.
const props = defineProps<{
  task: Task
  projectId: number
}>()

const store = useActiveProjectStore()

const adding = ref(false)
const kind = ref<TaskLinkKind>('blocked_by')
const otherId = ref(0)
const busy = ref(false)
const error = ref('')

watch(
  () => props.task.id,
  () => {
    adding.value = false
    error.value = ''
  },
)

const byId = computed(() => new Map(store.tasks.map((t) => [t.id, t])))

const groups = computed(() =>
  [
    { kind: 'blocked_by' as const, label: 'Blocked by', ids: props.task.blockedBy },
    { kind: 'blocks' as const, label: 'Blocks', ids: props.task.blocks },
    { kind: 'related' as const, label: 'Related', ids: props.task.related },
  ]
    .filter((g) => g.ids.length > 0)
    .map((g) => ({ ...g, tasks: g.ids.map((id) => byId.value.get(id) ?? null) })),
)

const linked = computed(() => new Set([...props.task.blockedBy, ...props.task.blocks, ...props.task.related]))
const defaultBranch = computed(() => store.project?.defaultBranch ?? '')
// Only a task to do that starts from the branch tasks merge into can wait.
const waits = (t: Task) => t.status === 'todo' && canWaitFrom(t.baseBranch, defaultBranch.value)
const canWait = computed(() => waits(props.task))

const kindOptions = computed<SelectMenuOption<TaskLinkKind>[]>(() => [
  {
    value: 'blocked_by',
    label: 'Blocked by',
    description: canWait.value
      ? 'Its agent starts only once that task is merged'
      : props.task.status !== 'todo'
        ? 'Only a task that has not started can wait for another'
        : `Only a task that starts from ${defaultBranch.value}, which tasks merge into, can wait for another`,
    disabled: !canWait.value,
  },
  { value: 'blocks', label: 'Blocks', description: "That task's agent starts only once this one is merged" },
  { value: 'related', label: 'Related to', description: 'Shown on both, and to their agents; holds nothing back' },
])

// The tasks a link of the chosen kind can be made to.
const candidates = computed<SelectMenuOption<number>[]>(() =>
  store.tasks
    .filter((t) => t.id !== props.task.id && !linked.value.has(t.id))
    .filter((t) => {
      if (kind.value === 'blocked_by') return t.status !== 'cancelled'
      if (kind.value === 'blocks') return waits(t)
      return true
    })
    .sort((a, b) => b.id - a.id)
    .map((t) => ({ value: t.id, label: `#${t.id} ${t.title}`, description: TASK_STATUS[t.status].label })),
)

watch(kind, () => {
  if (!candidates.value.some((c) => c.value === otherId.value)) otherId.value = 0
})

function openForm() {
  kind.value = canWait.value ? 'blocked_by' : 'related'
  otherId.value = 0
  error.value = ''
  adding.value = true
}

function open(id: number) {
  store.selectedTaskId = id
}

async function act(action: () => Promise<void>) {
  if (busy.value) return
  busy.value = true
  error.value = ''
  try {
    await action()
  } catch (e) {
    error.value = e instanceof Error ? e.message : 'Request failed'
  } finally {
    busy.value = false
  }
}

function link() {
  if (!otherId.value) return
  const id = props.task.id
  void act(async () => {
    await taskApi.link(props.projectId, id, kind.value, otherId.value)
    adding.value = false
    // The other task's links changed too, so the whole board reloads.
    await store.refresh()
  })
}

function unlink(other: number) {
  const id = props.task.id
  void act(async () => {
    await taskApi.unlink(props.projectId, id, other)
    await store.refresh()
  })
}

function waitsOn(t: Task | null): boolean {
  return !!t && t.status !== 'merged'
}
</script>

<template>
  <section class="side-section" aria-labelledby="task-links-title">
    <div class="links-head">
      <h3 id="task-links-title" class="side-title">
        Linked tasks
        <DocLink topic="taskLinks" label="Linked tasks" />
      </h3>
      <button
        v-if="!adding"
        type="button"
        class="icon-button"
        aria-label="Link a task"
        title="Link a task"
        :disabled="busy"
        @click="openForm"
      >
        <MrIcon name="plus" :size="13" />
      </button>
    </div>

    <div v-for="group in groups" :key="group.kind" class="group">
      <h4 class="group-label">{{ group.label }}</h4>
      <ul class="group-list">
        <li v-for="(other, i) in group.tasks" :key="group.ids[i]" class="linked">
          <button
            type="button"
            class="linked-open"
            :disabled="!other"
            :title="other ? `Open task #${other.id}` : undefined"
            @click="open(group.ids[i])"
          >
            <span
              class="linked-icon"
              :class="{ waiting: group.kind === 'blocked_by' && waitsOn(other) }"
            >
              <MrIcon
                :name="group.kind === 'blocked_by' && waitsOn(other) ? 'lock' : other ? TASK_STATUS[other.status].icon : 'task'"
                :size="12"
              />
            </span>
            <span class="linked-text">
              <span class="linked-title ellipsis">
                <span class="mono">#{{ group.ids[i] }}</span> {{ other?.title ?? '' }}
              </span>
              <span class="linked-sub">{{ other ? TASK_STATUS[other.status].label : 'Not found' }}</span>
            </span>
          </button>
          <button
            type="button"
            class="icon-button linked-remove"
            :aria-label="`Remove the link to task #${group.ids[i]}`"
            title="Remove link"
            :disabled="busy"
            @click="unlink(group.ids[i])"
          >
            <MrIcon name="close" :size="12" />
          </button>
        </li>
      </ul>
    </div>

    <p v-if="groups.length === 0 && !adding" class="form-hint links-empty">
      Make a task wait for the work it builds on, or point related tasks at each other.
    </p>

    <form v-if="adding" class="link-form" @submit.prevent="link">
      <SelectMenu v-model="kind" :options="kindOptions" title="Link kind" variant="field" />
      <SelectMenu
        v-model="otherId"
        :options="candidates"
        title="Task"
        variant="field"
        placeholder="Pick a task"
        :disabled="candidates.length === 0"
      />
      <p v-if="candidates.length === 0" class="form-hint">
        {{ kind === 'blocks' ? `No other task to do starts from ${defaultBranch}.` : 'No other task to link.' }}
      </p>
      <div class="link-form-actions">
        <button type="button" class="btn btn-secondary btn-sm" :disabled="busy" @click="adding = false">Cancel</button>
        <button type="submit" class="btn btn-primary btn-sm" :disabled="busy || !otherId">
          <span v-if="busy" class="spinner spinner-sm spinner-on-accent" />
          Link
        </button>
      </div>
    </form>

    <p v-if="error" class="notice notice-error notice-row" role="alert">
      <MrIcon name="alert" :size="14" />
      {{ error }}
    </p>
  </section>
</template>

<style scoped src="./shared.css"></style>
<style scoped src="./taskModal.css"></style>
<style scoped>
.links-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: var(--space-2);
}

.links-head .side-title {
  margin-bottom: 0;
}

.group + .group {
  margin-top: var(--space-3);
}

.group-label {
  margin-bottom: var(--space-1);
  font-size: 0.7rem;
  font-weight: 500;
  color: var(--text-secondary);
}

.group-list {
  display: flex;
  flex-direction: column;
  gap: 2px;
  margin: 0 calc(var(--space-2) * -1);
  list-style: none;
}

.linked {
  display: flex;
  align-items: center;
  gap: var(--space-1);
  border-radius: var(--radius-md);
  transition: background var(--transition-fast);
}

.linked:hover {
  background: var(--bg-hover);
}

.linked-open {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  flex: 1;
  min-width: 0;
  padding: 5px var(--space-2);
  text-align: left;
  font-size: inherit;
  color: var(--text-primary);
}

.linked-open:disabled {
  cursor: default;
}

.linked-icon {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 22px;
  height: 22px;
  border-radius: var(--radius-sm);
  background: var(--bg-raised);
  color: var(--text-secondary);
  flex-shrink: 0;
}

.linked-icon.waiting {
  color: var(--warning);
  background: var(--warning-bg);
}

.linked-text {
  display: flex;
  flex-direction: column;
  min-width: 0;
}

.linked-title {
  font-size: 0.78rem;
  font-weight: 500;
}

.linked-title .mono {
  color: var(--text-tertiary);
}

.linked-sub {
  font-size: 0.7rem;
  color: var(--text-tertiary);
}

.linked-remove {
  margin-right: var(--space-1);
  opacity: 0;
}

.linked:hover .linked-remove,
.linked-remove:focus-visible {
  opacity: 1;
}

/* Touch screens have no hover to reveal it. */
@media (hover: none) {
  .linked-remove {
    opacity: 1;
  }
}

.links-empty {
  margin-top: 0;
}

.link-form {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
  margin-top: var(--space-2);
}

.link-form-actions {
  display: flex;
  justify-content: flex-end;
  gap: var(--space-2);
}

.notice-row {
  margin-top: var(--space-2);
}
</style>
