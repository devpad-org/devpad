<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue'
import MarkdownMessage from '@/components/ide/MarkdownMessage.vue'
import MrIcon from '@/components/project/MrIcon.vue'
import { fullTime, timeAgo } from '@/utils/time'
import { authorIcon, authorName, baseName, errorText, resolverName, shortSha } from './format'
import { useMergeRequest, type Thread } from './useMergeRequest'

// One comment thread: its first comment, the replies, and the form that
// replies to it, resolves it or opens it again. A resolved thread folds to
// one line until it is opened. In the activity it says which file and line
// it is on; beside the diff, inFile, only the line.
const props = defineProps<{ thread: Thread; inFile?: boolean }>()

const { mr, isOpen, replyDrafts, reply, setResolved, jumpToComment, selectFile } = useMergeRequest()

const root = computed(() => props.thread.root)
const resolved = computed(() => !!root.value.resolvedBy)
const outdated = computed(() => !!mr.value && root.value.headSha !== mr.value.headSha)

// Resolved threads start folded, and fold again when they are resolved.
const folded = ref(resolved.value)
watch(resolved, (r) => (folded.value = r))

const draft = computed({
  get: () => replyDrafts.value[root.value.id] ?? '',
  set: (text: string) => {
    replyDrafts.value[root.value.id] = text
  },
})

// A reply already being written, such as on the other tab, keeps its form open.
const replying = ref(draft.value.trim() !== '')
// Resolve the thread, or open it again, along with the reply.
const toggle = ref(false)
const busy = ref(false)
const error = ref('')
const input = ref<HTMLTextAreaElement | null>(null)

const replyCount = computed(() => {
  const n = props.thread.replies.length
  return n === 1 ? '1 reply' : `${n} replies`
})

// The first line of the first comment, for a folded thread.
const excerpt = computed(
  () =>
    root.value.body
      .split('\n')
      .find((line) => line.trim())
      ?.replace(/[#>*_`]/g, '')
      .trim() ?? '',
)

function startReply() {
  replying.value = true
  error.value = ''
  void nextTick(() => input.value?.focus())
}

function cancelReply() {
  replying.value = false
  toggle.value = false
  error.value = ''
  delete replyDrafts.value[root.value.id]
}

async function run(action: () => Promise<void>) {
  busy.value = true
  error.value = ''
  try {
    await action()
  } catch (e) {
    error.value = errorText(e, 'Request failed')
  } finally {
    busy.value = false
  }
}

function submitReply() {
  if (busy.value || (!draft.value.trim() && !toggle.value)) return
  void run(async () => {
    await reply(props.thread, toggle.value)
    replying.value = false
    toggle.value = false
  })
}

function toggleResolved() {
  void run(() => setResolved(props.thread, !resolved.value))
}

function showLocation() {
  const { path, line } = root.value
  if (props.inFile) selectFile(path, line)
  else jumpToComment(path, line)
}
</script>

<template>
  <article class="thread" :class="{ 'thread-resolved': resolved }">
    <button v-if="folded" type="button" class="thread-folded" :title="`Show the thread resolved by ${resolverName(root.resolvedBy)}`" @click="folded = false">
      <MrIcon name="check-circle" :size="14" class="resolved-icon" />
      <strong>{{ authorName(root.author) }}</strong>
      <span v-if="root.path && !inFile" class="mono folded-file">{{ baseName(root.path) }}</span>
      <span v-if="root.path" class="line-chip">L{{ root.line }}</span>
      <span class="folded-excerpt">{{ excerpt }}</span>
      <span class="folded-meta">
        <template v-if="thread.replies.length">{{ replyCount }} &middot; </template>resolved by {{ resolverName(root.resolvedBy) }}
      </span>
      <MrIcon name="chevron-down" :size="13" />
    </button>

    <template v-else>
      <button
        v-if="root.path && !inFile"
        type="button"
        class="thread-location"
        :title="`Open ${root.path} at line ${root.line}`"
        @click="showLocation"
      >
        <MrIcon name="file" :size="12" />
        <span class="mono ellipsis">{{ root.path }}</span>
        <span class="line-chip">L{{ root.line }}</span>
        <MrIcon name="chevron-right" :size="12" class="thread-go" />
      </button>

      <div class="note">
        <div class="note-meta">
          <span class="avatar avatar-sm" :class="`avatar-${root.author}`">
            <MrIcon :name="authorIcon(root.author)" :size="11" />
          </span>
          <strong class="note-author">{{ authorName(root.author) }}</strong>
          <button v-if="root.path && inFile" type="button" class="line-chip line-chip-button" :title="`Show line ${root.line}`" @click="showLocation">
            L{{ root.line }}
          </button>
          <span v-if="outdated" class="tag tag-outdated" :title="`On ${shortSha(root.headSha)}; the code may have changed since`">outdated</span>
          <span class="muted" :title="fullTime(root.createdAt)">{{ timeAgo(root.createdAt) }}</span>
          <span class="note-actions">
            <button
              v-if="isOpen"
              type="button"
              class="resolve-button"
              :class="{ active: resolved }"
              :disabled="busy"
              :title="resolved ? 'Open the thread again' : 'Resolve the thread'"
              @click="toggleResolved"
            >
              <MrIcon name="check-circle" :size="13" />
              {{ resolved ? 'Reopen' : 'Resolve' }}
            </button>
            <button v-if="resolved" type="button" class="icon-button" aria-label="Fold the thread" title="Fold" @click="folded = true">
              <MrIcon name="chevron-down" :size="13" class="fold-icon" />
            </button>
          </span>
        </div>
        <MarkdownMessage :content="root.body" />
      </div>

      <div v-for="r in thread.replies" :key="r.id" class="note note-reply">
        <div class="note-meta">
          <span class="avatar avatar-sm" :class="`avatar-${r.author}`">
            <MrIcon :name="authorIcon(r.author)" :size="11" />
          </span>
          <strong class="note-author">{{ authorName(r.author) }}</strong>
          <span class="muted" :title="fullTime(r.createdAt)">{{ timeAgo(r.createdAt) }}</span>
        </div>
        <MarkdownMessage :content="r.body" />
      </div>

      <p v-if="resolved" class="resolved-line">
        <MrIcon name="check-circle" :size="13" />
        Resolved by {{ resolverName(root.resolvedBy) }}
        <span v-if="root.resolvedAt" class="muted" :title="fullTime(root.resolvedAt)">&middot; {{ timeAgo(root.resolvedAt) }}</span>
      </p>

      <div v-if="isOpen" class="thread-footer">
        <form v-if="replying" class="reply-form" @submit.prevent="submitReply">
          <textarea
            ref="input"
            v-model="draft"
            class="form-input form-textarea"
            rows="3"
            placeholder="Reply... (Markdown)"
            aria-label="Reply"
            @keydown.meta.enter.prevent="submitReply"
            @keydown.ctrl.enter.prevent="submitReply"
          />
          <div class="reply-actions">
            <label class="form-check">
              <input v-model="toggle" type="checkbox" />
              {{ resolved ? 'Reopen thread' : 'Resolve thread' }}
            </label>
            <span class="reply-buttons">
              <button type="button" class="btn btn-secondary btn-sm" @click="cancelReply">Cancel</button>
              <button type="submit" class="btn btn-primary btn-sm" :disabled="busy || (!draft.trim() && !toggle)">
                {{ draft.trim() ? 'Reply' : resolved ? 'Reopen' : 'Resolve' }}
              </button>
            </span>
          </div>
        </form>
        <button v-else type="button" class="reply-placeholder" @click="startReply">Reply...</button>
      </div>
      <p v-if="error" class="form-error thread-error">{{ error }}</p>
    </template>
  </article>
</template>

<style scoped src="@/components/project/shared.css"></style>
<style scoped src="./mergerequest.css"></style>
<style scoped>
.thread {
  background: var(--bg-surface);
  border: 0.5px solid var(--border-default);
  border-radius: var(--radius-md);
  overflow: hidden;
  font-size: 0.82rem;
}

.thread-location {
  display: flex;
  align-items: center;
  gap: 6px;
  width: 100%;
  padding: 5px var(--space-3);
  background: var(--bg-raised);
  border-bottom: 0.5px solid var(--border-hairline);
  color: var(--text-secondary);
  text-align: left;
  transition: all var(--transition-fast);
}

.thread-location:hover {
  color: var(--text-primary);
  background: color-mix(in srgb, var(--accent-blue) 10%, var(--bg-raised));
}

.thread-location .mono {
  min-width: 0;
}

.thread-go {
  margin-left: auto;
  opacity: 0;
  transition: opacity var(--transition-fast);
}

.thread-location:hover .thread-go {
  opacity: 1;
}

.note {
  padding: var(--space-2) var(--space-3);
}

.note-reply {
  border-top: 0.5px solid var(--border-hairline);
  background: color-mix(in srgb, var(--bg-raised) 45%, transparent);
}

.note-meta {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: var(--space-2);
  margin-bottom: var(--space-1);
  font-size: 0.75rem;
  color: var(--text-secondary);
}

.note-author {
  font-weight: 500;
  color: var(--text-primary);
}

.note-actions {
  display: inline-flex;
  align-items: center;
  gap: 2px;
  margin-left: auto;
}

.resolve-button {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  padding: 2px 7px;
  border-radius: var(--radius-sm);
  color: var(--text-muted);
  font-size: 0.72rem;
  transition: all var(--transition-fast);
}

.resolve-button:hover:not(:disabled) {
  color: var(--success);
  background: var(--success-bg);
}

.resolve-button.active {
  color: var(--success);
}

.resolve-button.active:hover:not(:disabled) {
  color: var(--text-primary);
  background: var(--bg-hover);
}

.resolve-button:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

.fold-icon {
  transform: rotate(180deg);
}

.resolved-line {
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 5px var(--space-3);
  border-top: 0.5px solid var(--border-hairline);
  color: var(--success);
  font-size: 0.75rem;
}

.thread-footer {
  padding: var(--space-2) var(--space-3);
  border-top: 0.5px solid var(--border-hairline);
  background: color-mix(in srgb, var(--bg-raised) 45%, transparent);
}

.reply-placeholder {
  width: 100%;
  padding: 5px var(--space-2);
  border: 0.5px solid var(--border-default);
  border-radius: var(--radius-sm);
  background: var(--bg-base);
  color: var(--text-muted);
  font-size: 0.8rem;
  text-align: left;
  cursor: text;
  transition: border-color var(--transition-fast);
}

.reply-placeholder:hover {
  border-color: var(--border-strong);
}

.reply-form {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
}

.reply-actions {
  display: flex;
  align-items: center;
  justify-content: space-between;
  flex-wrap: wrap;
  gap: var(--space-2);
}

.reply-actions .form-check {
  margin: 0;
}

.reply-buttons {
  display: inline-flex;
  gap: var(--space-2);
  margin-left: auto;
}

.thread-error {
  margin: 0;
  padding: 0 var(--space-3) var(--space-2);
}

/* A resolved thread folds to one line. */
.thread-folded {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  width: 100%;
  min-width: 0;
  padding: 6px var(--space-3);
  color: var(--text-secondary);
  font-size: 0.78rem;
  text-align: left;
  transition: background var(--transition-fast);
}

.thread-folded:hover {
  background: var(--bg-hover);
}

.thread-folded strong {
  font-weight: 500;
  color: var(--text-primary);
  flex-shrink: 0;
}

.resolved-icon {
  color: var(--success);
  flex-shrink: 0;
}

.folded-file {
  flex-shrink: 0;
  max-width: 30%;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

/* The excerpt gives way first, then who resolved it; the chevron stays. */
.folded-excerpt,
.folded-meta {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  color: var(--text-muted);
}

.folded-excerpt {
  flex: 1 1 0;
}

.folded-meta {
  flex: 0 1 auto;
  font-size: 0.72rem;
}

.thread-folded > svg:last-child {
  flex-shrink: 0;
}

.muted {
  color: var(--text-muted);
}

/* On a phone the folded line drops its excerpt to fit. */
@media (max-width: 768px) {
  .folded-excerpt {
    display: none;
  }

  .folded-meta {
    margin-left: auto;
  }
}
</style>
