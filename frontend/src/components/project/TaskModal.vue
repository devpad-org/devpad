<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { taskApi, type Task } from '@/api/tasks'
import type { MergeRequest } from '@/api/mergeRequests'
import MarkdownMessage from '@/components/ide/MarkdownMessage.vue'
import AgentModelPicker from './AgentModelPicker.vue'
import StatusBadge from './StatusBadge.vue'

const props = defineProps<{
  task: Task | null
  projectId: number
  mergeRequest?: MergeRequest
}>()

const emit = defineEmits<{
  close: []
  changed: [task: Task]
  deleted: [id: number]
}>()

const STARTABLE = new Set(['todo', 'failed', 'changes_requested', 'in_review'])
const ACTIVE = new Set(['queued', 'in_progress'])

const editing = ref(false)
const title = ref('')
const body = ref('')
const message = ref('')
const model = ref('')
const effort = ref('')
const busy = ref(false)
const confirmingDelete = ref(false)
const error = ref('')

const canStart = computed(() => props.task !== null && STARTABLE.has(props.task.status))
const canCancel = computed(() => props.task !== null && props.task.status !== 'merged' && props.task.status !== 'cancelled')
const canEdit = computed(() => props.task !== null && !ACTIVE.has(props.task.status) && props.task.status !== 'merged')
const startLabel = computed(() => {
  switch (props.task?.status) {
    case 'todo':
      return 'Start agent'
    case 'failed':
      return 'Retry'
    default:
      return 'Send back to agent'
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
    model.value = t?.model ?? ''
    effort.value = t?.effort ?? ''
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

function save() {
  if (!props.task) return
  if (!title.value.trim()) {
    error.value = 'Title is required'
    return
  }
  const id = props.task.id
  void run(async () => {
    const res = await taskApi.update(props.projectId, id, title.value.trim(), body.value)
    editing.value = false
    emit('changed', res.task)
  })
}

function start() {
  if (!props.task) return
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
</script>

<template>
  <Teleport to="body">
    <div v-if="task" class="modal-overlay" @click.self="emit('close')">
      <div class="modal" role="dialog" aria-modal="true">
        <div class="modal-header">
          <div class="header-title">
            <StatusBadge :status="task.status" />
            <h2 class="modal-title">{{ task.title }}</h2>
          </div>
          <button class="modal-close" aria-label="Close" @click="emit('close')">&times;</button>
        </div>

        <div class="modal-body">
          <div class="meta-row">
            <span class="mono">#{{ task.id }}</span>
            <span v-if="task.branch" class="mono">{{ task.branch }}</span>
            <span v-if="task.model">{{ task.model }}<template v-if="task.effort"> &middot; {{ task.effort }}</template></span>
          </div>

          <p v-if="task.error" class="notice" :class="task.status === 'failed' ? 'notice-error' : 'notice-warning'">{{ task.error }}</p>

          <div class="links">
            <RouterLink v-if="task.workspaceId" class="btn btn-secondary btn-sm" :to="{ name: 'ide', params: { id: task.workspaceId } }">
              Open sandbox
            </RouterLink>
            <RouterLink
              v-if="mergeRequest"
              class="btn btn-secondary btn-sm"
              :to="{ name: 'merge-request', params: { id: projectId, mrId: mergeRequest.id } }"
            >
              Merge request !{{ mergeRequest.id }}
            </RouterLink>
          </div>

          <template v-if="editing">
            <div class="form-group">
              <label class="form-label" for="task-title">Title</label>
              <input id="task-title" v-model="title" class="form-input" type="text" autocomplete="off" />
            </div>
            <div class="form-group">
              <label class="form-label" for="task-body">Description</label>
              <textarea id="task-body" v-model="body" class="form-input form-textarea" rows="8" />
            </div>
            <div class="actions">
              <button class="btn btn-secondary" :disabled="busy" @click="editing = false">Cancel</button>
              <button class="btn btn-primary" :disabled="busy" @click="save">Save</button>
            </div>
          </template>
          <template v-else>
            <div class="task-body">
              <MarkdownMessage v-if="task.body" :content="task.body" />
              <p v-else class="muted">No description.</p>
            </div>
            <div v-if="task.pendingMessage" class="pending">
              <span class="form-label">Feedback for the next run</span>
              <MarkdownMessage :content="task.pendingMessage" />
            </div>
          </template>

          <div v-if="canStart && !editing" class="start-box">
            <label class="form-label" for="task-message">{{ task.status === 'todo' ? 'Extra instructions' : 'Message to the agent' }}</label>
            <textarea
              id="task-message"
              v-model="message"
              class="form-input form-textarea"
              rows="3"
              :placeholder="task.pendingMessage ? 'Optional. Replaces the feedback above.' : 'Optional.'"
            />
            <div class="start-row">
              <AgentModelPicker v-model:model="model" v-model:effort="effort" />
              <button class="btn btn-primary" :disabled="busy || !model" @click="start">{{ startLabel }}</button>
            </div>
          </div>

          <p v-if="error" class="form-error">{{ error }}</p>

          <div v-if="!editing" class="footer">
            <div class="footer-left">
              <button v-if="canEdit" class="btn btn-secondary btn-sm" :disabled="busy" @click="editing = true">Edit</button>
              <button v-if="canCancel" class="btn btn-secondary btn-sm" :disabled="busy" @click="cancel">Cancel task</button>
            </div>
            <div class="footer-right">
              <template v-if="confirmingDelete">
                <span class="muted confirm-text">Delete the task and its sandbox?</span>
                <button class="btn btn-secondary btn-sm" :disabled="busy" @click="confirmingDelete = false">Keep</button>
                <button class="btn btn-danger btn-sm" :disabled="busy" @click="remove">Delete</button>
              </template>
              <button v-else class="btn btn-danger btn-sm" :disabled="busy" @click="confirmingDelete = true">Delete</button>
            </div>
          </div>
        </div>
      </div>
    </div>
  </Teleport>
</template>

<style scoped src="./shared.css"></style>
<style scoped>
.modal {
  background: var(--bg-surface);
  border: 0.5px solid var(--border-default);
  border-radius: var(--radius-lg);
  width: 100%;
  max-width: 720px;
  box-shadow: var(--shadow-strong);
}

.modal-header {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: var(--space-3);
  padding: var(--space-4) var(--space-5);
  border-bottom: 0.5px solid var(--border-default);
}

.header-title {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  min-width: 0;
}

.modal-title {
  font-size: 1rem;
  font-weight: 600;
  overflow-wrap: anywhere;
}

.modal-close {
  font-size: 1.25rem;
  color: var(--text-muted);
  padding: var(--space-1);
  transition: color var(--transition-fast);
}

.modal-close:hover {
  color: var(--text-primary);
}

.modal-body {
  display: flex;
  flex-direction: column;
  gap: var(--space-4);
  padding: var(--space-5);
}

.modal-body > .notice,
.modal-body > .form-group,
.modal-body > .form-error {
  margin-bottom: 0;
}

.links,
.actions,
.footer-left,
.footer-right {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-2);
}

.actions {
  justify-content: flex-end;
}

.task-body {
  font-size: 0.85rem;
  max-height: 40vh;
  overflow-y: auto;
}

.pending {
  padding: var(--space-3);
  border-left: 2px solid var(--attention);
  background: var(--attention-bg);
  border-radius: var(--radius-sm);
  font-size: 0.8rem;
}

.start-box {
  padding-top: var(--space-3);
  border-top: 0.5px solid var(--border-default);
}

.start-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  flex-wrap: wrap;
  gap: var(--space-2);
  margin-top: var(--space-2);
}

.footer {
  display: flex;
  align-items: center;
  justify-content: space-between;
  flex-wrap: wrap;
  gap: var(--space-2);
  padding-top: var(--space-3);
  border-top: 0.5px solid var(--border-default);
}

.confirm-text {
  font-size: 0.75rem;
}
</style>
