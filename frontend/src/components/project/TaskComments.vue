<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref, watch } from 'vue'
import { taskApi, type Task, type TaskComment, type TaskCommentAuthor, type TaskCommentRevision } from '@/api/tasks'
import DocLink from '@/components/DocLink.vue'
import MarkdownMessage from '@/components/ide/MarkdownMessage.vue'
import MrIcon from './MrIcon.vue'
import { fullTime, timeAgo } from '@/utils/time'

// A task's comments, by the user, the task's agent and the assistant, with
// a box to add one. Each author edits only its own, and every version is
// kept: an edited comment says so and opens its history, so an agent cannot
// quietly take back what it once told another. Raw HTML shows as text, so
// nothing in a comment is hidden from the user that an agent would read.
const props = defineProps<{ task: Task; projectId: number }>()

// Agents comment while they work, so the list keeps up while it is open.
const POLL_INTERVAL_MS = 15_000

const comments = ref<TaskComment[]>([])
const loaded = ref(false)
const draft = ref('')
const posting = ref(false)
const error = ref('')

const editingId = ref<number | null>(null)
const editDraft = ref('')
const saving = ref(false)
const editInput = ref<HTMLTextAreaElement[]>([])

// The history shown for each comment, and the versions read for it, by the
// number of versions it had: a new edit reads them again.
const historyOpen = ref(new Set<number>())
const histories = ref(new Map<number, { count: number; revisions: TaskCommentRevision[] }>())
const historyError = ref(new Map<number, string>())

const AUTHORS: Record<TaskCommentAuthor, string> = {
  user: 'You',
  task_agent: 'Task agent',
  assistant: 'Assistant',
}

const countLabel = computed(() => (comments.value.length ? String(comments.value.length) : ''))

function errorText(e: unknown, fallback: string): string {
  return e instanceof Error && e.message ? e.message : fallback
}

async function load() {
  const id = props.task.id
  try {
    const res = await taskApi.comments(props.projectId, id)
    if (props.task.id !== id) return
    comments.value = res.comments
    loaded.value = true
    for (const c of res.comments) {
      if (historyOpen.value.has(c.id)) void loadHistory(c)
    }
  } catch (e) {
    if (props.task.id === id && !loaded.value) error.value = errorText(e, 'Failed to load comments')
  }
}

watch(
  () => props.task.id,
  () => {
    comments.value = []
    loaded.value = false
    draft.value = ''
    error.value = ''
    editingId.value = null
    historyOpen.value = new Set()
    histories.value = new Map()
    historyError.value = new Map()
    void load()
  },
)

let timer: ReturnType<typeof setInterval> | null = null
onMounted(() => {
  void load()
  timer = setInterval(() => {
    if (document.visibilityState === 'visible' && editingId.value === null) void load()
  }, POLL_INTERVAL_MS)
})
onUnmounted(() => {
  if (timer !== null) clearInterval(timer)
})

async function post() {
  const body = draft.value.trim()
  if (!body || posting.value) return
  posting.value = true
  error.value = ''
  try {
    const res = await taskApi.addComment(props.projectId, props.task.id, body)
    comments.value = [...comments.value, res.comment]
    draft.value = ''
  } catch (e) {
    error.value = errorText(e, 'Failed to add the comment')
  } finally {
    posting.value = false
  }
}

function startEdit(c: TaskComment) {
  editingId.value = c.id
  editDraft.value = c.body
  error.value = ''
  void nextTick(() => editInput.value[0]?.focus())
}

function cancelEdit() {
  editingId.value = null
}

async function saveEdit(c: TaskComment) {
  const body = editDraft.value.trim()
  if (!body || saving.value) return
  if (body === c.body) {
    editingId.value = null
    return
  }
  saving.value = true
  error.value = ''
  try {
    const res = await taskApi.editComment(props.projectId, props.task.id, c.id, body)
    comments.value = comments.value.map((other) => (other.id === c.id ? res.comment : other))
    editingId.value = null
    if (historyOpen.value.has(c.id)) void loadHistory(res.comment)
  } catch (e) {
    error.value = errorText(e, 'Failed to save the comment')
  } finally {
    saving.value = false
  }
}

async function loadHistory(c: TaskComment) {
  if (histories.value.get(c.id)?.count === c.revisions) return
  try {
    const res = await taskApi.commentRevisions(props.projectId, props.task.id, c.id)
    histories.value = new Map(histories.value).set(c.id, { count: c.revisions, revisions: res.revisions })
    const errors = new Map(historyError.value)
    errors.delete(c.id)
    historyError.value = errors
  } catch (e) {
    historyError.value = new Map(historyError.value).set(c.id, errorText(e, 'Failed to load the history'))
  }
}

function toggleHistory(c: TaskComment) {
  const open = new Set(historyOpen.value)
  if (open.has(c.id)) {
    open.delete(c.id)
  } else {
    open.add(c.id)
    void loadHistory(c)
  }
  historyOpen.value = open
}

// Versions newest first, each labelled by where it stands.
function versions(c: TaskComment) {
  const revisions = histories.value.get(c.id)?.revisions ?? []
  return revisions
    .map((v, i) => ({
      ...v,
      label: i === revisions.length - 1 ? 'Current' : i === 0 ? 'Original' : `Edit ${i}`,
    }))
    .reverse()
}

function editedTitle(c: TaskComment): string {
  const who = c.author === 'user' ? 'You' : AUTHORS[c.author]
  return `${who} changed this comment ${timeAgo(c.updatedAt)}. Show all ${c.revisions} versions.`
}
</script>

<template>
  <section class="field comments" aria-label="Comments">
    <div class="field-head">
      <h3 class="form-label">Comments</h3>
      <span v-if="countLabel" class="count">{{ countLabel }}</span>
      <DocLink topic="taskComments" label="Comments" />
    </div>

    <p v-if="loaded && comments.length === 0" class="empty muted">
      No comments yet. You, the task's agent and the assistant can comment here.
    </p>

    <ol v-if="comments.length" class="comment-list">
      <li v-for="c in comments" :key="c.id" class="comment">
        <div class="comment-meta">
          <span class="avatar" :class="`avatar-${c.author}`">
            <MrIcon :name="c.author === 'user' ? 'user' : 'bot'" :size="11" />
          </span>
          <strong class="comment-author">{{ AUTHORS[c.author] }}</strong>
          <span class="muted" :title="fullTime(c.createdAt)">{{ timeAgo(c.createdAt) }}</span>
          <button
            v-if="c.revisions > 1"
            type="button"
            class="tag tag-edited"
            :class="{ 'tag-agent': c.author !== 'user' }"
            :aria-expanded="historyOpen.has(c.id)"
            :title="editedTitle(c)"
            @click="toggleHistory(c)"
          >
            <MrIcon name="clock" :size="10" />
            edited &middot; {{ c.revisions }} versions
          </button>
          <span class="comment-actions">
            <button
              v-if="c.author === 'user' && editingId !== c.id"
              type="button"
              class="icon-button"
              aria-label="Edit comment"
              title="Edit"
              @click="startEdit(c)"
            >
              <MrIcon name="pencil" :size="12" />
            </button>
          </span>
        </div>

        <form v-if="editingId === c.id" class="edit-form" @submit.prevent="saveEdit(c)">
          <textarea
            ref="editInput"
            v-model="editDraft"
            class="form-input form-textarea"
            rows="3"
            aria-label="Edit comment"
            @keydown.meta.enter.prevent="saveEdit(c)"
            @keydown.ctrl.enter.prevent="saveEdit(c)"
            @keydown.esc.prevent="cancelEdit"
          />
          <div class="form-actions">
            <span class="form-hint">Earlier versions stay in the comment's history.</span>
            <button type="button" class="btn btn-secondary btn-sm" :disabled="saving" @click="cancelEdit">Cancel</button>
            <button type="submit" class="btn btn-primary btn-sm" :disabled="saving || !editDraft.trim()">Save</button>
          </div>
        </form>
        <MarkdownMessage v-else :content="c.body" literal-html class="comment-body" />

        <div v-if="historyOpen.has(c.id)" class="history">
          <p v-if="historyError.get(c.id)" class="form-error">{{ historyError.get(c.id) }}</p>
          <p v-else-if="!histories.get(c.id)" class="muted history-loading">Loading versions…</p>
          <ol v-else class="history-list">
            <li v-for="v in versions(c)" :key="v.id" class="version">
              <div class="version-meta">
                <strong>{{ v.label }}</strong>
                <span class="muted" :title="fullTime(v.createdAt)">{{ timeAgo(v.createdAt) }}</span>
              </div>
              <MarkdownMessage :content="v.body" literal-html class="comment-body" />
            </li>
          </ol>
        </div>
      </li>
    </ol>

    <form class="composer" @submit.prevent="post">
      <label class="sr-only" for="task-comment">Comment</label>
      <textarea
        id="task-comment"
        v-model="draft"
        class="form-input form-textarea"
        rows="2"
        placeholder="Leave a comment... (Markdown)"
        @keydown.meta.enter.prevent="post"
        @keydown.ctrl.enter.prevent="post"
      />
      <div class="form-actions">
        <span class="form-hint">The task's agent reads its comments when it starts and before it finishes.</span>
        <button type="submit" class="btn btn-secondary btn-sm" :disabled="posting || !draft.trim()">
          <span v-if="posting" class="spinner spinner-sm" />
          <MrIcon v-else name="comment" :size="12" />
          Comment
        </button>
      </div>
    </form>

    <p v-if="error" class="notice notice-error notice-row">
      <MrIcon name="alert" :size="14" />
      {{ error }}
    </p>
  </section>
</template>

<style scoped src="./shared.css"></style>
<style scoped src="./taskModal.css"></style>
<style scoped>
.comments {
  gap: var(--space-3);
  padding-top: var(--space-5);
  border-top: 0.5px solid var(--border-hairline);
}

.comments .field-head {
  margin-bottom: 0;
}

.count {
  padding: 0 6px;
  border-radius: var(--radius-sm);
  background: var(--bg-neutral);
  color: var(--text-secondary);
  font-size: 0.7rem;
}

.empty {
  font-size: 0.8rem;
}

.comment-list,
.history-list {
  display: flex;
  flex-direction: column;
  list-style: none;
}

.comment-list {
  gap: var(--space-2);
}

.comment {
  padding: var(--space-2) var(--space-3);
  background: var(--bg-elevated);
  border: 0.5px solid var(--border-subtle);
  border-radius: var(--radius-md);
  font-size: 0.82rem;
  min-width: 0;
}

.comment-meta {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: var(--space-2);
  min-height: 22px;
  margin-bottom: var(--space-1);
  font-size: 0.75rem;
  color: var(--text-secondary);
}

.comment-author {
  font-weight: 500;
  color: var(--text-primary);
}

.comment-actions {
  display: inline-flex;
  margin-left: auto;
}

.comment-body {
  overflow-wrap: anywhere;
}

.avatar {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 20px;
  height: 20px;
  border-radius: 50%;
  flex-shrink: 0;
}

.avatar-user {
  color: var(--hue-blue);
  background: color-mix(in srgb, var(--hue-blue) 16%, transparent);
}

.avatar-task_agent {
  color: var(--hue-green);
  background: color-mix(in srgb, var(--hue-green) 16%, transparent);
}

.avatar-assistant {
  color: var(--hue-yellow);
  background: color-mix(in srgb, var(--hue-yellow) 16%, transparent);
}

.tag {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  padding: 0 6px;
  border-radius: var(--radius-sm);
  background: var(--bg-neutral);
  color: var(--text-secondary);
  font-size: 0.68rem;
  white-space: nowrap;
  transition: background var(--transition-fast);
}

.tag-edited:hover,
.tag-edited[aria-expanded='true'] {
  background: var(--bg-hover);
  color: var(--text-primary);
}

/* An agent that changed what it said stands out, so its history is read. */
.tag-agent,
.tag-agent:hover,
.tag-agent[aria-expanded='true'] {
  color: var(--attention);
  background: var(--attention-bg);
}

.edit-form,
.composer {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
}

.form-actions {
  display: flex;
  align-items: center;
  justify-content: flex-end;
  flex-wrap: wrap;
  gap: var(--space-2);
}

.form-actions .form-hint {
  margin: 0 auto 0 0;
}

.history {
  margin-top: var(--space-2);
  padding-top: var(--space-2);
  border-top: 0.5px solid var(--border-hairline);
}

.history-loading {
  font-size: 0.75rem;
}

.history-list {
  gap: var(--space-2);
}

.version {
  padding-left: var(--space-3);
  border-left: 2px solid var(--border-subtle);
}

.version:first-child {
  border-left-color: var(--accent-border);
}

.version-meta {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  margin-bottom: 2px;
  font-size: 0.72rem;
  color: var(--text-secondary);
}

.version-meta strong {
  font-weight: 500;
  color: var(--text-primary);
}

.muted {
  color: var(--text-tertiary);
}
</style>
