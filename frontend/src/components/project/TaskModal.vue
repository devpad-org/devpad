<script setup lang="ts">
import { computed, inject, nextTick, onMounted, onUnmounted, ref, watch } from 'vue'
import { taskApi, type Task } from '@/api/tasks'
import { projectApi } from '@/api/projects'
import type { MergeRequest } from '@/api/mergeRequests'
import MarkdownMessage from '@/components/ide/MarkdownMessage.vue'
import { confirmDiscardKey } from '@/components/ide/ideShell'
import { useActiveProjectStore } from '@/stores/activeProject'
import { branchActionError } from './branchErrors'
import AgentModelPicker from './AgentModelPicker.vue'
import MrIcon, { type MrIconName } from './MrIcon.vue'
import StatusBadge from './StatusBadge.vue'
import { TASK_STAGES, TASK_STATUS } from './taskStatus'
import { formatUsd } from '@/utils/cost'
import { fullTime, timeAgo } from '@/utils/time'

const props = defineProps<{
  task: Task | null
  projectId: number
  mergeRequest?: MergeRequest
  // The project workspace is running, so the task's branch can be checked out.
  workspaceLive?: boolean
}>()

const emit = defineEmits<{
  close: []
  changed: [task: Task]
  deleted: [id: number]
  'checked-out': [branch: string]
}>()

// The merge request opens in its own modal, so this one gets out of the way.
function reviewMergeRequest(id: number) {
  projectStore.selectedMergeRequestId = id
  emit('close')
}

const STARTABLE = new Set(['todo', 'failed', 'changes_requested', 'in_review'])
const ACTIVE = new Set(['queued', 'in_progress'])

const editing = ref(false)
const title = ref('')
const body = ref('')
// The budget as typed, in USD; empty or zero means no limit. v-model on a
// number input stores a number once anything is typed.
const budget = ref<number | string>('')
const message = ref('')
const model = ref('')
const effort = ref('')
const busy = ref(false)
const confirmingDelete = ref(false)
const composerOpen = ref(true)
const error = ref('')
const titleInput = ref<HTMLInputElement | null>(null)
const budgetInput = ref<HTMLInputElement | null>(null)

const projectStore = useActiveProjectStore()
const confirmDiscard = inject(confirmDiscardKey, () => Promise.resolve(true))
const checkingOut = ref(false)
const checkoutNotice = ref('')

// The agent's branch reaches the project when a round of its work is
// collected, so it can only be checked out after that.
const branchPublished = computed(() => !!props.task?.branch && projectStore.branchNames.has(props.task.branch))

watch(() => props.task?.id, (id) => {
  checkoutNotice.value = ''
  if (id !== undefined) {
    projectStore.refreshBranches().catch(() => {
      // Keep the last known branches; checking out reports a missing one.
    })
  }
}, { immediate: true })

async function checkoutBranch() {
  const branch = props.task?.branch
  if (!branch || !props.workspaceLive || checkingOut.value) return
  if (!(await confirmDiscard('Checking out this branch reloads your open files from it, so their unsaved changes will be lost.'))) return
  checkingOut.value = true
  error.value = ''
  checkoutNotice.value = ''
  try {
    const res = await projectApi.checkoutBranch(props.projectId, branch)
    checkoutNotice.value = res.created
      ? `Checked out ${res.branch} in your workspace.`
      : res.sha === res.upstreamSha
        ? `Switched your workspace to ${res.branch}.`
        : `Switched to ${res.branch}. Your copy differs from the agent's; merge ${res.upstream} to pick up its latest work.`
    emit('checked-out', res.branch)
  } catch (e) {
    error.value = branchActionError(e, 'Failed to check out the branch')
  } finally {
    checkingOut.value = false
  }
}

const info = computed(() => TASK_STATUS[props.task?.status ?? 'todo'])
const canStart = computed(() => props.task !== null && STARTABLE.has(props.task.status))
const canCancel = computed(() => props.task !== null && props.task.status !== 'merged' && props.task.status !== 'cancelled')
const canEdit = computed(() => props.task !== null && !ACTIVE.has(props.task.status) && props.task.status !== 'merged')
// The task's agents have spent its budget, so it cannot start until it is raised.
const overBudget = computed(() => !!props.task && props.task.budgetUsd > 0 && props.task.costUsd >= props.task.budgetUsd)

const steps = computed(() => {
  const t = props.task
  const last = t?.status === 'cancelled' ? 'Cancelled' : 'Merged'
  return [...TASK_STAGES.slice(0, -1), last].map((label, i) => {
    let state: 'done' | 'current' | 'upcoming' | 'skipped' = 'upcoming'
    if (i === info.value.step) state = 'current'
    else if (i < info.value.step) state = t?.status === 'cancelled' ? 'skipped' : 'done'
    return { label: state === 'current' ? info.value.label : label, state }
  })
})

// One line on what is happening and what to do next.
const statusHint = computed(() => {
  const t = props.task
  if (!t) return ''
  switch (t.status) {
    case 'todo':
      return 'Not started. Hand it to an agent once the description says what you want.'
    case 'queued':
      return t.error || 'Waiting for a free sandbox.'
    case 'in_progress':
      return 'An agent is working on this in its own sandbox.'
    case 'failed':
      return t.error || 'The agent run failed.'
    case 'in_review':
      return props.mergeRequest ? 'The agent is done. Review the merge request.' : 'The agent is done and waiting for review.'
    case 'changes_requested':
      return 'Changes were requested. Send the feedback back to the agent.'
    case 'merged':
      return props.mergeRequest ? `Merged into ${props.mergeRequest.targetBranch}.` : 'Merged.'
    case 'cancelled':
      return 'This task was cancelled.'
  }
  return ''
})

// Errors the banner doesn't already show, e.g. a warning left on a task in review.
const extraWarning = computed(() => {
  const t = props.task
  return t?.error && t.status !== 'failed' && t.status !== 'queued' ? t.error : ''
})

const composer = computed(() => {
  switch (props.task?.status) {
    case 'todo':
      return {
        title: 'Start an agent',
        hint: 'It works on its own branch in a sandbox and opens a merge request when done.',
        label: 'Extra instructions',
        action: 'Start agent',
        icon: 'play' as MrIconName,
      }
    case 'failed':
      return {
        title: 'Try again',
        hint: 'Start a new run, optionally with a different model.',
        label: 'Message to the agent',
        action: 'Retry',
        icon: 'retry' as MrIconName,
      }
    default:
      return {
        title: 'Send back to the agent',
        hint: 'The agent picks up where it left off on the same branch.',
        label: 'Message to the agent',
        action: 'Send back to agent',
        icon: 'send' as MrIconName,
      }
  }
})

watch(
  () => props.task?.id,
  () => {
    const t = props.task
    editing.value = false
    confirmingDelete.value = false
    error.value = ''
    message.value = ''
    title.value = t?.title ?? ''
    body.value = t?.body ?? ''
    budget.value = t && t.budgetUsd > 0 ? String(t.budgetUsd) : ''
    model.value = t?.model ?? ''
    effort.value = t?.effort ?? ''
  },
  { immediate: true },
)

// In review the merge request is the main path, so the composer starts folded.
watch(
  () => [props.task?.id, props.task?.status],
  () => {
    composerOpen.value = props.task?.status !== 'in_review'
  },
  { immediate: true },
)

async function run(action: () => Promise<void>) {
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

function startEditing(focus: 'title' | 'budget' = 'title') {
  if (!props.task) return
  title.value = props.task.title
  body.value = props.task.body
  budget.value = props.task.budgetUsd > 0 ? String(props.task.budgetUsd) : ''
  error.value = ''
  editing.value = true
  void nextTick(() => (focus === 'budget' ? budgetInput.value : titleInput.value)?.focus())
}

function save() {
  if (!props.task) return
  if (!title.value.trim()) {
    error.value = 'Title is required'
    return
  }
  const budgetUsd = String(budget.value).trim() === '' ? 0 : Number(budget.value)
  if (!Number.isFinite(budgetUsd) || budgetUsd < 0) {
    error.value = 'The budget must be an amount in USD, or empty for no limit'
    return
  }
  const id = props.task.id
  void run(async () => {
    const res = await taskApi.update(props.projectId, id, { title: title.value.trim(), body: body.value, budgetUsd })
    editing.value = false
    emit('changed', res.task)
  })
}

function start() {
  if (!props.task || busy.value || !model.value || overBudget.value) return
  const id = props.task.id
  void run(async () => {
    const res = await taskApi.start(props.projectId, id, {
      message: message.value.trim() || undefined,
      model: model.value || undefined,
      effort: effort.value || undefined,
    })
    message.value = ''
    emit('changed', res.task)
  })
}

function cancel() {
  if (!props.task) return
  const id = props.task.id
  void run(async () => {
    const res = await taskApi.cancel(props.projectId, id)
    emit('changed', res.task)
  })
}

function remove() {
  if (!props.task) return
  const id = props.task.id
  void run(async () => {
    await taskApi.delete(props.projectId, id)
    emit('deleted', id)
  })
}

// Escape backs out of the innermost state first: delete confirm, edit, then the modal.
// Handling it marks it handled, so a merge request under the task stays open.
function onKeydown(e: KeyboardEvent) {
  if (e.key !== 'Escape' || !props.task || e.defaultPrevented) return
  e.preventDefault()
  if (confirmingDelete.value) confirmingDelete.value = false
  else if (editing.value) editing.value = false
  else emit('close')
}

onMounted(() => document.addEventListener('keydown', onKeydown))
onUnmounted(() => document.removeEventListener('keydown', onKeydown))
</script>

<template>
  <Teleport to="body">
    <div v-if="task" class="modal-overlay" @click.self="emit('close')">
      <div class="modal" role="dialog" aria-modal="true" aria-labelledby="task-modal-title">
        <header class="modal-header">
          <div class="heading">
            <span class="eyebrow">
              <MrIcon name="task" :size="12" />
              Task #{{ task.id }}
            </span>
            <h2 id="task-modal-title" class="modal-title">{{ task.title }}</h2>
            <div class="summary">
              <span v-if="task.branch" class="branch-chip" :title="task.branch">
                <MrIcon name="branch" :size="12" />
                {{ task.branch }}
              </span>
              <span v-if="task.model" class="summary-item">
                <MrIcon name="bot" :size="12" />
                {{ task.model }}<template v-if="task.effort"> &middot; {{ task.effort }}</template>
              </span>
              <span class="summary-item" :title="fullTime(task.updatedAt)">
                <MrIcon name="clock" :size="12" />
                updated {{ timeAgo(task.updatedAt) }}
              </span>
            </div>
          </div>
          <button class="modal-close" aria-label="Close" title="Close (Esc)" @click="emit('close')">
            <MrIcon name="close" :size="16" />
          </button>
        </header>

        <section class="lifecycle" :class="`tone-${info.tone}`" aria-label="Progress">
          <ol class="stepper">
            <li v-for="(step, i) in steps" :key="i" class="step" :class="`step-${step.state}`">
              <span class="step-node">
                <span v-if="step.state === 'current' && task.status === 'in_progress'" class="spinner spinner-sm" />
                <MrIcon v-else-if="step.state === 'current'" :name="info.icon" :size="13" />
                <MrIcon v-else-if="step.state === 'done'" name="check" :size="12" :stroke-width="2.5" />
                <span v-else class="step-number">{{ i + 1 }}</span>
              </span>
              <span class="step-label">{{ step.label }}</span>
            </li>
          </ol>
          <div class="status-banner">
            <MrIcon :name="info.icon" :size="15" class="banner-icon" />
            <p class="banner-text">{{ statusHint }}</p>
            <div class="banner-actions">
              <RouterLink
                v-if="task.workspaceId && ACTIVE.has(task.status)"
                class="btn btn-secondary btn-sm"
                :to="{ name: 'ide', params: { id: task.workspaceId } }"
              >
                <MrIcon name="terminal" :size="13" />
                Watch in sandbox
              </RouterLink>
              <button
                v-if="mergeRequest && (task.status === 'in_review' || task.status === 'changes_requested')"
                class="btn btn-primary btn-sm"
                @click="reviewMergeRequest(mergeRequest.id)"
              >
                <MrIcon name="open" :size="13" />
                Review !{{ mergeRequest.id }}
              </button>
            </div>
          </div>
        </section>

        <div class="modal-body">
          <div class="main-col">
            <p v-if="extraWarning" class="notice notice-warning notice-row">
              <MrIcon name="warning" :size="14" />
              {{ extraWarning }}
            </p>

            <form v-if="editing" class="edit-form" @submit.prevent="save">
              <div class="field">
                <label class="form-label" for="task-title">Title</label>
                <input id="task-title" ref="titleInput" v-model="title" class="form-input" type="text" autocomplete="off" />
              </div>
              <div class="field">
                <label class="form-label" for="task-body">Description</label>
                <textarea
                  id="task-body"
                  v-model="body"
                  class="form-input form-textarea"
                  rows="10"
                  placeholder="What should the agent do? Markdown works."
                  @keydown.meta.enter.prevent="save"
                  @keydown.ctrl.enter.prevent="save"
                />
              </div>
              <div class="field">
                <label class="form-label" for="task-budget">Budget (USD)</label>
                <input
                  id="task-budget"
                  ref="budgetInput"
                  v-model="budget"
                  class="form-input budget-input"
                  type="number"
                  min="0"
                  step="any"
                  inputmode="decimal"
                  placeholder="No limit"
                />
                <span class="form-hint">
                  What the agent may spend on this task across all its runs, counting only models with known prices.
                  Spent so far: {{ formatUsd(task.costUsd) }}.
                </span>
              </div>
              <div class="edit-actions">
                <span class="form-hint"><kbd>Ctrl</kbd> <kbd>Enter</kbd> to save &middot; <kbd>Esc</kbd> to cancel</span>
                <button type="button" class="btn btn-secondary btn-sm" :disabled="busy" @click="editing = false">Cancel</button>
                <button type="submit" class="btn btn-primary btn-sm" :disabled="busy">Save</button>
              </div>
            </form>

            <section v-else class="field">
              <div class="field-head">
                <h3 class="form-label">Description</h3>
                <span class="field-head-end">
                  <button v-if="canEdit" class="icon-button" aria-label="Edit task" title="Edit" @click="startEditing()">
                    <MrIcon name="pencil" :size="13" />
                  </button>
                </span>
              </div>
              <div class="description">
                <MarkdownMessage v-if="task.body" :content="task.body" />
                <div v-else class="empty-description">
                  <p class="muted">No description yet.</p>
                  <button v-if="canEdit" class="text-button" @click="startEditing()">Add one so the agent knows what to do</button>
                </div>
              </div>
            </section>

            <section v-if="task.pendingMessage && !editing" class="field">
              <div class="field-head">
                <MrIcon name="comment" :size="13" class="attention-icon" />
                <h3 class="form-label">Feedback for the next run</h3>
              </div>
              <div class="feedback">
                <MarkdownMessage :content="task.pendingMessage" />
              </div>
            </section>

            <p v-if="canStart && overBudget && !editing" class="notice notice-warning notice-row">
              <MrIcon name="warning" :size="14" />
              <span class="budget-notice-text">
                The agent has spent this task's {{ formatUsd(task.budgetUsd) }} budget. Raise it to start the agent again.
              </span>
              <button class="btn btn-secondary btn-sm" @click="startEditing('budget')">Raise budget</button>
            </p>

            <section v-if="canStart && !editing" class="composer">
              <button v-if="!composerOpen" class="composer-toggle" @click="composerOpen = true">
                <span class="avatar avatar-agent"><MrIcon name="bot" :size="13" /></span>
                <span class="composer-toggle-text">
                  <strong>Send back to the agent</strong>
                  <span class="muted">Skip the review and give the agent more instructions</span>
                </span>
                <MrIcon name="chevron-down" :size="14" class="muted" />
              </button>
              <template v-else>
                <div class="composer-head">
                  <span class="avatar avatar-agent"><MrIcon name="bot" :size="13" /></span>
                  <span class="composer-head-text">
                    <strong>{{ composer.title }}</strong>
                    <span class="muted">{{ composer.hint }}</span>
                  </span>
                </div>
                <label class="sr-only" for="task-message">{{ composer.label }}</label>
                <textarea
                  id="task-message"
                  v-model="message"
                  class="form-input form-textarea composer-input"
                  rows="3"
                  :placeholder="task.pendingMessage
                    ? `${composer.label} (optional). Replaces the feedback above.`
                    : `${composer.label} (optional)`"
                  @keydown.meta.enter.prevent="start"
                  @keydown.ctrl.enter.prevent="start"
                />
                <div class="composer-footer">
                  <AgentModelPicker v-model:model="model" v-model:effort="effort" :agent-id="task.agentId || undefined" />
                  <button class="btn btn-primary" :disabled="busy || !model || overBudget" @click="start">
                    <span v-if="busy" class="spinner spinner-sm spinner-on-accent" />
                    <MrIcon v-else :name="composer.icon" :size="13" />
                    {{ composer.action }}
                  </button>
                </div>
              </template>
            </section>

            <p v-if="error" class="notice notice-error notice-row">
              <MrIcon name="alert" :size="14" />
              {{ error }}
            </p>
          </div>

          <aside class="side-col">
            <section class="side-section">
              <h3 class="side-title">Details</h3>
              <dl class="details">
                <dt>Status</dt>
                <dd><StatusBadge :status="task.status" /></dd>
                <dt>Branch</dt>
                <dd>
                  <span v-if="task.branch" class="mono ellipsis" :title="task.branch">{{ task.branch }}</span>
                  <span v-else class="muted">Not created yet</span>
                </dd>
                <template v-if="task.baseBranch">
                  <dt>From</dt>
                  <dd><span class="mono ellipsis" :title="task.baseBranch">{{ task.baseBranch }}</span></dd>
                </template>
                <template v-if="task.headSha">
                  <dt>Head</dt>
                  <dd><span class="mono" :title="task.headSha">{{ task.headSha.slice(0, 8) }}</span></dd>
                </template>
                <dt>Agent</dt>
                <dd>
                  <span v-if="task.model" class="ellipsis" :title="task.model">{{ task.model }}</span>
                  <span v-else class="muted">Not run yet</span>
                </dd>
                <template v-if="task.effort">
                  <dt>Effort</dt>
                  <dd>{{ task.effort }}</dd>
                </template>
                <dt>Spent</dt>
                <dd class="spend" :class="{ 'over-budget': overBudget }">
                  {{ formatUsd(task.costUsd) }}
                  <span v-if="task.budgetUsd > 0" :class="{ muted: !overBudget }">of {{ formatUsd(task.budgetUsd) }}</span>
                  <span v-else class="muted">&middot; no limit</span>
                </dd>
                <dt>Created</dt>
                <dd :title="fullTime(task.createdAt)">{{ timeAgo(task.createdAt) }}</dd>
                <dt>Updated</dt>
                <dd :title="fullTime(task.updatedAt)">{{ timeAgo(task.updatedAt) }}</dd>
              </dl>
            </section>

            <section v-if="task.workspaceId || mergeRequest || branchPublished" class="side-section">
              <h3 class="side-title">Linked</h3>
              <div class="links">
                <button
                  v-if="branchPublished"
                  type="button"
                  class="link-row"
                  :disabled="!workspaceLive || checkingOut"
                  :title="workspaceLive ? `Check out ${task.branch} in your workspace` : 'Start your workspace to check out branches'"
                  @click="checkoutBranch"
                >
                  <span class="link-icon"><MrIcon name="branch" :size="13" /></span>
                  <span class="link-text">
                    <span class="link-title">{{ checkingOut ? 'Checking out…' : 'Check out branch' }}</span>
                    <span class="link-sub">{{ workspaceLive ? 'Run and change it in your workspace' : 'Start your workspace first' }}</span>
                  </span>
                  <MrIcon name="chevron-right" :size="13" class="link-go" />
                </button>
                <p v-if="checkoutNotice" class="checkout-notice">{{ checkoutNotice }}</p>
                <RouterLink v-if="task.workspaceId" class="link-row" :to="{ name: 'ide', params: { id: task.workspaceId } }">
                  <span class="link-icon"><MrIcon name="terminal" :size="13" /></span>
                  <span class="link-text">
                    <span class="link-title">Sandbox</span>
                    <span class="link-sub">Open the agent's workspace</span>
                  </span>
                  <MrIcon name="chevron-right" :size="13" class="link-go" />
                </RouterLink>
                <button
                  v-if="mergeRequest"
                  type="button"
                  class="link-row"
                  @click="reviewMergeRequest(mergeRequest.id)"
                >
                  <span class="link-icon link-icon-mr" :class="`mr-${mergeRequest.status}`">
                    <MrIcon :name="mergeRequest.status === 'merged' ? 'merged' : mergeRequest.status === 'closed' ? 'closed' : 'open'" :size="13" />
                  </span>
                  <span class="link-text">
                    <span class="link-title">Merge request !{{ mergeRequest.id }}</span>
                    <span class="link-sub">{{ mergeRequest.status }} &middot; into {{ mergeRequest.targetBranch }}</span>
                  </span>
                  <MrIcon name="chevron-right" :size="13" class="link-go" />
                </button>
              </div>
            </section>

            <section class="side-section">
              <h3 class="side-title">Actions</h3>
              <div v-if="confirmingDelete" class="confirm">
                <p class="confirm-text">Delete this task and its sandbox? This can't be undone.</p>
                <div class="confirm-actions">
                  <button class="btn btn-secondary btn-sm" :disabled="busy" @click="confirmingDelete = false">Keep</button>
                  <button class="btn btn-danger-solid btn-sm" :disabled="busy" @click="remove">Delete</button>
                </div>
              </div>
              <div v-else class="action-list">
                <button v-if="canEdit && !editing" class="action" :disabled="busy" @click="startEditing()">
                  <MrIcon name="pencil" :size="13" />
                  Edit task
                </button>
                <button v-if="canCancel" class="action" :disabled="busy" @click="cancel">
                  <MrIcon name="stop" :size="13" />
                  Cancel task
                </button>
                <button class="action action-danger" :disabled="busy" @click="confirmingDelete = true">
                  <MrIcon name="trash" :size="13" />
                  Delete task
                </button>
              </div>
            </section>
          </aside>
        </div>
      </div>
    </div>
  </Teleport>
</template>

<style scoped src="./shared.css"></style>
<style scoped src="./taskModal.css"></style>
<style scoped>
.modal-title {
  margin: var(--space-1) 0 var(--space-2);
  font-size: 1.1rem;
}

.summary {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: var(--space-2) var(--space-3);
  font-size: 0.75rem;
  color: var(--text-secondary);
}

.summary-item {
  display: inline-flex;
  align-items: center;
  gap: 5px;
}

.summary-item .mr-icon {
  color: var(--text-muted);
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

/* Lifecycle: stepper plus a banner with what's next */
.lifecycle {
  --tone: var(--text-secondary);
  --tone-bg: var(--bg-neutral);
  --tone-border: var(--border-default);
  padding: var(--space-4) var(--space-5);
  border-bottom: 0.5px solid var(--border-default);
}

.tone-active {
  --tone: var(--accent-blue);
  --tone-bg: color-mix(in srgb, var(--accent-blue) 12%, transparent);
  --tone-border: color-mix(in srgb, var(--accent-blue) 30%, transparent);
}

.tone-review {
  --tone: var(--accent-purple);
  --tone-bg: color-mix(in srgb, var(--accent-purple) 12%, transparent);
  --tone-border: color-mix(in srgb, var(--accent-purple) 30%, transparent);
}

.tone-attention {
  --tone: var(--attention);
  --tone-bg: var(--attention-bg);
  --tone-border: var(--attention-border);
}

.tone-error {
  --tone: var(--error);
  --tone-bg: var(--error-bg);
  --tone-border: var(--error-border);
}

.tone-success {
  --tone: var(--success);
  --tone-bg: var(--success-bg);
  --tone-border: var(--success-border);
}

.stepper {
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  list-style: none;
  margin-bottom: var(--space-3);
}

.step {
  position: relative;
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 6px;
  text-align: center;
  min-width: 0;
}

/* Connector to the previous step, drawn behind the nodes. */
.step + .step::before {
  content: '';
  position: absolute;
  top: 13px;
  right: calc(50% + 18px);
  left: calc(-50% + 18px);
  height: 2px;
  border-radius: 1px;
  background: var(--border-default);
}

.step-done + .step::before {
  background: var(--success-border);
}

.step-node {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 28px;
  height: 28px;
  border-radius: 50%;
  border: 1px solid var(--border-default);
  background: var(--bg-base);
  color: var(--text-dim);
  transition: all var(--transition-normal);
}

.step-number {
  font-size: 0.7rem;
  font-weight: 600;
  font-family: var(--font-mono);
}

.step-done .step-node {
  color: var(--success);
  background: var(--success-bg);
  border-color: var(--success-border);
}

.step-skipped .step-node {
  border-style: dashed;
}

.step-current .step-node {
  color: var(--tone);
  background: var(--tone-bg);
  border-color: var(--tone);
  box-shadow: 0 0 0 4px var(--tone-bg);
}

.step-current .spinner {
  border-top-color: var(--tone);
}

.step-label {
  max-width: 100%;
  font-size: 0.72rem;
  color: var(--text-muted);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.step-done .step-label {
  color: var(--text-secondary);
}

.step-current .step-label {
  color: var(--tone);
  font-weight: 600;
}

.status-banner {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: var(--space-2) var(--space-3);
  padding: var(--space-2) var(--space-3);
  border-radius: var(--radius-md);
  background: var(--tone-bg);
  border: 0.5px solid var(--tone-border);
}

.banner-icon {
  color: var(--tone);
}

.banner-text {
  flex: 1;
  min-width: 180px;
  font-size: 0.8rem;
  color: var(--text-primary);
  overflow-wrap: anywhere;
}

.tone-error .banner-text {
  color: var(--error);
}

.banner-actions {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-2);
}

.banner-actions:empty {
  display: none;
}

@media (max-width: 760px) {
  .lifecycle {
    padding-left: var(--space-4);
    padding-right: var(--space-4);
  }
}

.description {
  max-height: 45vh;
  overflow-y: auto;
  font-size: 0.85rem;
}

.empty-description {
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  gap: var(--space-1);
  font-size: 0.85rem;
}

.text-button {
  color: var(--accent-blue);
  font-size: 0.8rem;
}

.text-button:hover {
  text-decoration: underline;
}

.edit-form {
  display: flex;
  flex-direction: column;
  gap: var(--space-4);
}

.edit-form .form-hint {
  margin-top: var(--space-2);
}

.edit-actions {
  display: flex;
  align-items: center;
  justify-content: flex-end;
  flex-wrap: wrap;
  gap: var(--space-2);
}

.edit-actions .form-hint {
  margin: 0 auto 0 0;
}

.attention-icon {
  color: var(--attention);
}

/* The feedback reads as a quote of what the reviewer asked for. */
.feedback {
  padding: var(--space-1) 0 var(--space-1) var(--space-3);
  border-left: 2px solid var(--attention-border);
  font-size: 0.8rem;
}

/* Agent composer, set off from the task above by a hairline. */
.composer {
  display: flex;
  flex-direction: column;
  gap: var(--space-3);
  padding-top: var(--space-5);
  border-top: 0.5px solid var(--border-hairline);
}

.avatar {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 26px;
  height: 26px;
  border-radius: 50%;
  flex-shrink: 0;
}

.avatar-agent {
  color: var(--accent-purple);
  background: color-mix(in srgb, var(--accent-purple) 16%, transparent);
}

.composer-head {
  display: flex;
  align-items: center;
  gap: var(--space-3);
}

.composer-head-text,
.composer-toggle-text {
  display: flex;
  flex-direction: column;
  flex: 1;
  min-width: 0;
  font-size: 0.78rem;
  line-height: 1.4;
}

.composer-head-text strong,
.composer-toggle-text strong {
  font-size: 0.85rem;
  font-weight: 500;
}

.composer-input {
  min-height: 72px;
}

.composer-footer {
  display: flex;
  align-items: center;
  justify-content: space-between;
  flex-wrap: wrap;
  gap: var(--space-2);
}

.composer-toggle {
  display: flex;
  align-items: center;
  gap: var(--space-3);
  width: calc(100% + var(--space-4));
  margin: calc(var(--space-2) * -1);
  padding: var(--space-2);
  text-align: left;
  border-radius: var(--radius-md);
  transition: background var(--transition-fast);
}

.composer-toggle:hover {
  background: var(--bg-hover);
}

.details {
  display: grid;
  grid-template-columns: auto minmax(0, 1fr);
  gap: var(--space-2) var(--space-3);
  font-size: 0.78rem;
}

.details dt {
  color: var(--text-muted);
}

.details dd {
  display: flex;
  align-items: center;
  min-width: 0;
  color: var(--text-primary);
}

.details dd.spend {
  gap: var(--space-1);
}

.details dd.over-budget {
  color: var(--error);
}

.budget-notice-text {
  flex: 1;
}

.budget-input {
  max-width: 10rem;
}

.ellipsis {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.links {
  display: flex;
  flex-direction: column;
  gap: 2px;
  margin: 0 calc(var(--space-2) * -1);
}

.link-row {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  padding: 6px var(--space-2);
  border-radius: var(--radius-md);
  color: var(--text-primary);
  transition: background var(--transition-fast);
}

button.link-row {
  width: 100%;
  text-align: left;
  font-size: inherit;
}

button.link-row:disabled {
  opacity: 0.55;
  cursor: not-allowed;
}

.checkout-notice {
  margin: 2px var(--space-2) 4px;
  font-size: 0.75rem;
  color: var(--text-secondary);
}

.link-row:hover {
  color: var(--text-primary);
  background: var(--bg-hover);
}

.link-icon {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 26px;
  height: 26px;
  border-radius: var(--radius-md);
  background: var(--bg-raised);
  color: var(--text-secondary);
  flex-shrink: 0;
}

.mr-open {
  color: var(--accent-blue);
  background: color-mix(in srgb, var(--accent-blue) 14%, transparent);
}

.mr-merged {
  color: var(--success);
  background: var(--success-bg);
}

.link-text {
  display: flex;
  flex-direction: column;
  flex: 1;
  min-width: 0;
}

.link-title {
  font-size: 0.78rem;
  font-weight: 500;
}

.link-sub {
  font-size: 0.7rem;
  color: var(--text-muted);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.link-go {
  color: var(--text-dim);
  transition: transform var(--transition-fast), color var(--transition-fast);
}

.link-row:hover .link-go {
  color: var(--text-secondary);
  transform: translateX(2px);
}

.action-list {
  display: flex;
  flex-direction: column;
  gap: 2px;
  margin: 0 calc(var(--space-2) * -1);
}

.action {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  padding: 6px var(--space-2);
  border-radius: var(--radius-md);
  font-size: 0.78rem;
  color: var(--text-secondary);
  text-align: left;
  transition: all var(--transition-fast);
}

.action:hover:not(:disabled) {
  color: var(--text-primary);
  background: var(--bg-hover);
}

.action:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

.action-danger {
  color: var(--error);
}

.action-danger:hover:not(:disabled) {
  color: var(--error);
  background: var(--error-bg);
}

.confirm {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
  padding: var(--space-2) var(--space-3);
  border-radius: var(--radius-md);
  background: var(--error-bg);
  border: 0.5px solid var(--error-border);
}

.confirm-text {
  font-size: 0.75rem;
  color: var(--text-primary);
}

.confirm-actions {
  display: flex;
  justify-content: flex-end;
  gap: var(--space-2);
}
</style>
