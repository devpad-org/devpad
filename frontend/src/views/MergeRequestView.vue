<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import { projectApi, type Project, type RepoDiff, type RepoDiffFile } from '@/api/projects'
import {
  mergeConflictsFromError,
  mergeRequestApi,
  type CommentInput,
  type MergeRequestDetail,
  type MergeStrategy,
  type Review,
  type Verdict,
} from '@/api/mergeRequests'
import MarkdownMessage from '@/components/ide/MarkdownMessage.vue'
import MonacoDiffViewer from '@/components/ide/MonacoDiffViewer.vue'
import AgentModelPicker from '@/components/project/AgentModelPicker.vue'
import StatusBadge from '@/components/project/StatusBadge.vue'

const POLL_INTERVAL_MS = 5000

const route = useRoute()
const projectId = computed(() => Number(route.params.id))
const mrId = computed(() => Number(route.params.mrId))

const project = ref<Project | null>(null)
const mr = ref<MergeRequestDetail | null>(null)
const diff = ref<RepoDiff | null>(null)
const loadError = ref('')
const actionError = ref('')
const notice = ref('')
const busy = ref(false)

const selectedPath = ref('')
const oldContent = ref('')
const newContent = ref('')
const fileLoading = ref(false)
const fileError = ref('')
const sideBySide = ref(window.innerWidth >= 1000)
const fileCache = new Map<string, string | null>()

const verdict = ref<Verdict>('comment')
const reviewBody = ref('')
const drafts = ref<CommentInput[]>([])
const draftLine = ref<number | null>(null)
const draftBody = ref('')
const sendToAgent = ref(true)

const agentModel = ref('')
const agentEffort = ref('')
const autoFix = ref(true)

const strategy = ref<MergeStrategy>('merge')
const pushAfterMerge = ref(true)
const conflicts = ref<string[] | null>(null)

let timer: ReturnType<typeof setInterval> | null = null

const isOpen = computed(() => mr.value?.status === 'open')
const files = computed(() => diff.value?.files ?? [])
const selectedFile = computed(() => files.value.find((f) => f.path === selectedPath.value) ?? null)
const pendingAgentReview = computed(() => mr.value?.reviews.some((r) => r.author === 'agent' && r.status === 'pending') ?? false)
const reviews = computed(() => [...(mr.value?.reviews ?? [])].sort((a, b) => b.id - a.id))

const commentsByPath = computed(() => {
  const byPath = new Map<string, MergeRequestDetail['comments']>()
  for (const c of mr.value?.comments ?? []) {
    const list = byPath.get(c.path) ?? []
    list.push(c)
    byPath.set(c.path, list)
  }
  for (const list of byPath.values()) list.sort((a, b) => a.line - b.line || a.id - b.id)
  return byPath
})

const selectedComments = computed(() => commentsByPath.value.get(selectedPath.value) ?? [])
const selectedDrafts = computed(() => drafts.value.filter((d) => d.path === selectedPath.value))

function commentCount(path: string): number {
  return (commentsByPath.value.get(path)?.length ?? 0) + drafts.value.filter((d) => d.path === path).length
}

function reviewComments(review: Review) {
  return (mr.value?.comments ?? []).filter((c) => c.reviewId === review.id)
}

function shortSha(sha: string): string {
  return sha ? sha.slice(0, 8) : ''
}

function errorText(e: unknown, fallback: string): string {
  return e instanceof Error ? e.message : fallback
}

async function loadDetail() {
  const res = await mergeRequestApi.get(projectId.value, mrId.value)
  const headChanged = mr.value?.headSha !== res.mergeRequest.headSha || mr.value?.status !== res.mergeRequest.status
  mr.value = res.mergeRequest
  if (headChanged || !diff.value) await loadDiff()
}

async function loadDiff() {
  const res = await mergeRequestApi.diff(projectId.value, mrId.value)
  diff.value = res
  if (!res.files.some((f) => f.path === selectedPath.value)) {
    selectedPath.value = res.files[0]?.path ?? ''
  } else {
    void loadFile()
  }
}

async function refresh() {
  try {
    if (!project.value) project.value = (await projectApi.get(projectId.value)).project
    await loadDetail()
    loadError.value = ''
  } catch (e) {
    loadError.value = errorText(e, 'Failed to load the merge request')
  }
}

async function fileAt(rev: string, path: string): Promise<string | null> {
  const key = `${rev}:${path}`
  if (fileCache.has(key)) return fileCache.get(key) ?? null
  const content = await projectApi.fileAt(projectId.value, rev, path)
  fileCache.set(key, content)
  return content
}

let fileRequest = 0
async function loadFile() {
  const file = selectedFile.value
  const d = diff.value
  if (!file || !d) return
  const request = ++fileRequest
  fileError.value = ''
  if (file.binary) {
    oldContent.value = ''
    newContent.value = ''
    return
  }
  fileLoading.value = true
  try {
    const [before, after] = await Promise.all([
      file.status === 'A' ? Promise.resolve('') : fileAt(d.mergeBase, file.oldPath || file.path),
      file.status === 'D' ? Promise.resolve('') : fileAt(d.headSha, file.path),
    ])
    if (request !== fileRequest) return
    oldContent.value = before ?? ''
    newContent.value = after ?? ''
  } catch (e) {
    if (request === fileRequest) fileError.value = errorText(e, 'Failed to load the file')
  } finally {
    if (request === fileRequest) fileLoading.value = false
  }
}

watch(selectedPath, () => {
  draftLine.value = null
  draftBody.value = ''
  void loadFile()
})

watch([projectId, mrId], () => {
  mr.value = null
  diff.value = null
  project.value = null
  fileCache.clear()
  drafts.value = []
  conflicts.value = null
  void refresh()
})

onMounted(() => {
  void refresh()
  timer = setInterval(() => {
    if (document.visibilityState === 'visible' && isOpen.value) void refresh()
  }, POLL_INTERVAL_MS)
})

onUnmounted(() => {
  if (timer !== null) clearInterval(timer)
})

function fileStatusLabel(file: RepoDiffFile): string {
  switch (file.status) {
    case 'A':
      return 'added'
    case 'D':
      return 'deleted'
    case 'R':
      return 'renamed'
    case 'C':
      return 'copied'
    default:
      return 'modified'
  }
}

function addDraft() {
  const line = Number(draftLine.value)
  if (!selectedPath.value || !Number.isInteger(line) || line < 1) {
    actionError.value = 'Pick a line number in the new version of the file'
    return
  }
  if (!draftBody.value.trim()) {
    actionError.value = 'A comment needs some text'
    return
  }
  actionError.value = ''
  drafts.value.push({ path: selectedPath.value, line, body: draftBody.value.trim() })
  draftLine.value = null
  draftBody.value = ''
}

function removeDraft(draft: CommentInput) {
  drafts.value = drafts.value.filter((d) => d !== draft)
}

async function act(action: () => Promise<void>) {
  busy.value = true
  actionError.value = ''
  notice.value = ''
  try {
    await action()
  } catch (e) {
    actionError.value = errorText(e, 'Request failed')
  } finally {
    busy.value = false
  }
}

function submitReview() {
  if (!reviewBody.value.trim() && drafts.value.length === 0 && verdict.value !== 'approve') {
    actionError.value = 'Write a summary or add a line comment'
    return
  }
  void act(async () => {
    await mergeRequestApi.review(projectId.value, mrId.value, {
      verdict: verdict.value,
      body: reviewBody.value.trim(),
      comments: drafts.value,
      sendToAgent: verdict.value === 'request_changes' && sendToAgent.value && Boolean(mr.value?.taskId),
    })
    reviewBody.value = ''
    drafts.value = []
    verdict.value = 'comment'
    notice.value = 'Review submitted.'
    await refresh()
  })
}

function startAgentReview() {
  void act(async () => {
    await mergeRequestApi.startAgentReview(projectId.value, mrId.value, {
      model: agentModel.value || undefined,
      effort: agentEffort.value || undefined,
      autoFix: autoFix.value && Boolean(mr.value?.taskId),
    })
    notice.value = 'A review agent is reading the change.'
    await refresh()
  })
}

function merge() {
  void act(async () => {
    conflicts.value = null
    try {
      const res = await mergeRequestApi.merge(projectId.value, mrId.value, {
        strategy: strategy.value,
        push: pushAfterMerge.value && Boolean(project.value?.upstreamUrl),
      })
      if (res.pushError) {
        actionError.value = `Merged, but the push failed: ${res.pushError}`
      } else {
        notice.value = res.pushed ? 'Merged and pushed upstream.' : 'Merged.'
      }
      await refresh()
    } catch (e) {
      const files = mergeConflictsFromError(e)
      if (files) {
        conflicts.value = files
        return
      }
      throw e
    }
  })
}

function resolveConflicts() {
  const files = conflicts.value ?? []
  void act(async () => {
    await mergeRequestApi.resolveConflicts(projectId.value, mrId.value, files)
    conflicts.value = null
    notice.value = 'The task agent is merging the default branch in and resolving the conflicts.'
    await refresh()
  })
}

function close() {
  void act(async () => {
    await mergeRequestApi.close(projectId.value, mrId.value)
    await refresh()
  })
}
</script>

<template>
  <div class="page">
    <RouterLink :to="{ name: 'project', params: { id: projectId } }" class="breadcrumb">
      &larr; {{ project?.name ?? 'Project' }}
    </RouterLink>

    <p v-if="!mr && loadError" class="notice notice-error">{{ loadError }}</p>
    <p v-else-if="!mr" class="empty-text">Loading merge request...</p>

    <template v-else>
      <div class="page-header">
        <div class="title-block">
          <div class="title-row">
            <h1 class="page-title">{{ mr.title }} <span class="muted">!{{ mr.id }}</span></h1>
            <StatusBadge :status="mr.status" />
          </div>
          <div class="meta-row">
            <span class="mono">{{ mr.sourceBranch }} &rarr; {{ mr.targetBranch }}</span>
            <span>&middot;</span>
            <span class="mono">{{ shortSha(mr.headSha) }}</span>
            <template v-if="isOpen">
              <span>&middot;</span>
              <span>{{ mr.ahead }} ahead, {{ mr.behind }} behind</span>
            </template>
            <template v-if="mr.status === 'merged'">
              <span>&middot;</span>
              <span>merged as <span class="mono">{{ shortSha(mr.mergeSha) }}</span></span>
            </template>
            <template v-if="mr.taskId">
              <span>&middot;</span>
              <span>task #{{ mr.taskId }}</span>
            </template>
          </div>
        </div>
        <div v-if="isOpen" class="header-actions">
          <button class="btn btn-secondary" :disabled="busy" @click="close">Close</button>
        </div>
      </div>

      <p v-if="loadError" class="notice notice-error">{{ loadError }}</p>
      <p v-if="actionError" class="notice notice-error">{{ actionError }}</p>
      <p v-if="notice" class="notice notice-success">{{ notice }}</p>

      <section v-if="mr.description" class="section panel description">
        <MarkdownMessage :content="mr.description" />
      </section>

      <section v-if="isOpen" class="section panel merge-box">
        <div v-if="conflicts" class="conflicts">
          <p class="notice notice-warning">
            {{ mr.targetBranch }} has changes that conflict with this branch in
            {{ conflicts.length === 1 ? 'one file' : `${conflicts.length} files` }}.
          </p>
          <ul class="conflict-list">
            <li v-for="file in conflicts" :key="file" class="mono">{{ file }}</li>
          </ul>
          <button v-if="mr.taskId" class="btn btn-primary" :disabled="busy" @click="resolveConflicts">
            Ask the task's agent to resolve them
          </button>
        </div>
        <p v-else-if="mr.behind > 0" class="merge-hint">
          {{ mr.targetBranch }} moved on by {{ mr.behind }} commit{{ mr.behind === 1 ? '' : 's' }} since this branch started.
        </p>
        <div class="merge-row">
          <select v-model="strategy" class="form-input merge-select" aria-label="Merge strategy">
            <option value="merge">Merge commit</option>
            <option value="squash">Squash</option>
            <option value="fast-forward">Fast-forward only</option>
          </select>
          <label v-if="project?.upstreamUrl" class="form-check">
            <input v-model="pushAfterMerge" type="checkbox" />
            Push {{ mr.targetBranch }} upstream
          </label>
          <button class="btn btn-success" :disabled="busy" @click="merge">Merge</button>
        </div>
      </section>

      <section class="section">
        <div class="section-header">
          <h2 class="section-title">
            Changes
            <span class="muted">{{ files.length }} file{{ files.length === 1 ? '' : 's' }}</span>
          </h2>
          <label class="form-check">
            <input v-model="sideBySide" type="checkbox" />
            Side by side
          </label>
        </div>
        <p v-if="files.length === 0" class="empty-text">This branch changes nothing.</p>
        <div v-else class="changes">
          <nav class="file-list panel" aria-label="Changed files">
            <button
              v-for="file in files"
              :key="file.path"
              type="button"
              class="file-row"
              :class="{ active: file.path === selectedPath }"
              :title="file.oldPath ? `${file.oldPath} → ${file.path}` : file.path"
              @click="selectedPath = file.path"
            >
              <span class="file-status" :class="`status-${file.status}`">{{ file.status }}</span>
              <span class="file-path">{{ file.path }}</span>
              <span v-if="commentCount(file.path)" class="file-comments">{{ commentCount(file.path) }}</span>
              <span v-if="!file.binary" class="file-stats">
                <span class="add">+{{ file.additions }}</span>
                <span class="del">-{{ file.deletions }}</span>
              </span>
            </button>
          </nav>

          <div v-if="selectedFile" class="file-view">
            <div class="file-header">
              <span class="mono">
                <template v-if="selectedFile.oldPath">{{ selectedFile.oldPath }} &rarr; </template>{{ selectedFile.path }}
              </span>
              <span class="muted">{{ fileStatusLabel(selectedFile) }}</span>
            </div>
            <p v-if="fileError" class="notice notice-error">{{ fileError }}</p>
            <p v-else-if="selectedFile.binary" class="empty-text">Binary file; no preview.</p>
            <div v-else class="diff-frame" :class="{ loading: fileLoading }">
              <MonacoDiffViewer
                :old-content="oldContent"
                :new-content="newContent"
                :old-file-name="selectedFile.oldPath || selectedFile.path"
                :new-file-name="selectedFile.path"
                :side-by-side="sideBySide"
              />
            </div>

            <div class="file-comments-list">
              <div v-for="c in selectedComments" :key="c.id" class="comment">
                <div class="comment-meta">
                  <span class="comment-author" :class="`author-${c.author}`">{{ c.author }}</span>
                  <span class="mono">line {{ c.line }}</span>
                  <span v-if="c.headSha !== mr.headSha" class="muted">outdated</span>
                </div>
                <MarkdownMessage :content="c.body" />
              </div>
              <div v-for="(d, i) in selectedDrafts" :key="`draft-${i}`" class="comment draft">
                <div class="comment-meta">
                  <span class="comment-author author-user">draft</span>
                  <span class="mono">line {{ d.line }}</span>
                  <button class="link-button" @click="removeDraft(d)">remove</button>
                </div>
                <p class="draft-body">{{ d.body }}</p>
              </div>
              <form v-if="isOpen" class="draft-form" @submit.prevent="addDraft">
                <input
                  v-model.number="draftLine"
                  class="form-input draft-line"
                  type="number"
                  min="1"
                  placeholder="Line"
                  aria-label="Line number in the new file"
                />
                <input v-model="draftBody" class="form-input" type="text" placeholder="Comment on this line..." aria-label="Comment" />
                <button type="submit" class="btn btn-secondary">Add</button>
              </form>
            </div>
          </div>
        </div>
      </section>

      <div v-if="isOpen" class="review-grid">
        <section class="section panel">
          <h2 class="section-title box-title">Your review</h2>
          <div class="form-group">
            <select v-model="verdict" class="form-input" aria-label="Verdict">
              <option value="comment">Comment</option>
              <option value="approve">Approve</option>
              <option value="request_changes">Request changes</option>
            </select>
          </div>
          <div class="form-group">
            <textarea v-model="reviewBody" class="form-input form-textarea" rows="4" placeholder="Summary of your review (Markdown)" aria-label="Review summary" />
            <p class="form-hint">
              {{ drafts.length }} line comment{{ drafts.length === 1 ? '' : 's' }} will be included.
            </p>
          </div>
          <label v-if="verdict === 'request_changes' && mr.taskId" class="form-check form-group">
            <input v-model="sendToAgent" type="checkbox" />
            Send the feedback to the task's agent now
          </label>
          <div class="box-actions">
            <button class="btn btn-primary" :disabled="busy" @click="submitReview">Submit review</button>
          </div>
        </section>

        <section class="section panel">
          <h2 class="section-title box-title">Agent review</h2>
          <p class="form-hint box-hint">
            An agent reads the change in its own sandbox and leaves line comments and a verdict.
          </p>
          <div class="form-group">
            <AgentModelPicker v-model:model="agentModel" v-model:effort="agentEffort" />
          </div>
          <label v-if="mr.taskId" class="form-check form-group">
            <input v-model="autoFix" type="checkbox" />
            If it requests changes, send them to the task's agent
          </label>
          <div class="box-actions">
            <button class="btn btn-primary" :disabled="busy || pendingAgentReview || !agentModel" @click="startAgentReview">
              {{ pendingAgentReview ? 'Review in progress...' : 'Start agent review' }}
            </button>
          </div>
        </section>
      </div>

      <section class="section">
        <h2 class="section-title box-title">Reviews</h2>
        <p v-if="reviews.length === 0" class="empty-text">No reviews yet.</p>
        <div v-for="review in reviews" :key="review.id" class="panel review">
          <div class="review-header">
            <span class="comment-author" :class="`author-${review.author}`">{{ review.author }}</span>
            <StatusBadge v-if="review.status === 'submitted' && review.verdict" :status="review.verdict" />
            <StatusBadge v-else :status="review.status" />
            <span v-if="review.model" class="muted">{{ review.model }}</span>
            <span class="mono muted">{{ shortSha(review.headSha) }}</span>
            <span v-if="review.headSha !== mr.headSha" class="muted">outdated</span>
            <RouterLink
              v-if="review.workspaceId && review.status === 'pending'"
              class="btn btn-secondary btn-sm review-open"
              :to="{ name: 'ide', params: { id: review.workspaceId } }"
            >
              Watch in sandbox
            </RouterLink>
          </div>
          <p v-if="review.error" class="notice notice-error">{{ review.error }}</p>
          <MarkdownMessage v-if="review.body" :content="review.body" />
          <p v-if="reviewComments(review).length" class="form-hint">
            {{ reviewComments(review).length }} line comment{{ reviewComments(review).length === 1 ? '' : 's' }}:
            <button
              v-for="c in reviewComments(review)"
              :key="c.id"
              class="link-button mono"
              @click="selectedPath = c.path"
            >
              {{ c.path }}:{{ c.line }}
            </button>
          </p>
        </div>
      </section>
    </template>
  </div>
</template>

<style scoped src="@/components/project/shared.css"></style>
<style scoped>
.title-block {
  min-width: 0;
}

.title-row {
  display: flex;
  align-items: center;
  gap: var(--space-2);
}

.title-block .meta-row {
  margin-top: var(--space-2);
}

.description {
  font-size: 0.85rem;
  max-height: 40vh;
  overflow-y: auto;
}

.merge-box {
  display: flex;
  flex-direction: column;
  gap: var(--space-3);
}

.merge-row {
  display: flex;
  align-items: center;
  justify-content: flex-end;
  flex-wrap: wrap;
  gap: var(--space-3);
}

.merge-select {
  width: auto;
}

.merge-hint {
  font-size: 0.8rem;
  color: var(--text-secondary);
}

.conflicts .notice {
  margin-bottom: var(--space-2);
}

.conflict-list {
  list-style: none;
  margin-bottom: var(--space-3);
  color: var(--text-secondary);
}

.changes {
  display: grid;
  grid-template-columns: minmax(200px, 280px) minmax(0, 1fr);
  gap: var(--space-3);
  align-items: start;
}

@media (max-width: 800px) {
  .changes {
    grid-template-columns: minmax(0, 1fr);
  }
}

.file-list {
  display: flex;
  flex-direction: column;
  padding: var(--space-1);
  max-height: 70vh;
  overflow-y: auto;
}

.file-row {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  padding: var(--space-1) var(--space-2);
  border-radius: var(--radius-sm);
  font-size: 0.75rem;
  color: var(--text-secondary);
  text-align: left;
}

.file-row:hover {
  background: var(--bg-hover);
  color: var(--text-primary);
}

.file-row.active {
  background: var(--bg-selected);
  color: var(--text-primary);
}

.file-status {
  font-family: var(--font-mono);
  width: 1ch;
  flex-shrink: 0;
}

.status-A {
  color: var(--success);
}

.status-D {
  color: var(--error);
}

.status-M {
  color: var(--warning);
}

.status-R,
.status-C {
  color: var(--accent-blue);
}

.file-path {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  direction: rtl;
  text-align: left;
}

.file-comments {
  padding: 0 var(--space-1);
  border-radius: var(--radius-sm);
  background: var(--bg-neutral);
  font-size: 0.7rem;
}

.file-stats {
  display: flex;
  gap: var(--space-1);
  font-family: var(--font-mono);
  font-size: 0.7rem;
  flex-shrink: 0;
}

.add {
  color: var(--success);
}

.del {
  color: var(--error);
}

.file-view {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
  min-width: 0;
}

.file-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-2);
  font-size: 0.8rem;
  overflow-wrap: anywhere;
}

.diff-frame {
  height: 60vh;
  min-height: 320px;
  border: 0.5px solid var(--border-default);
  border-radius: var(--radius-md);
  overflow: hidden;
  transition: opacity var(--transition-fast);
}

.diff-frame.loading {
  opacity: 0.5;
}

.file-comments-list {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
}

.comment {
  padding: var(--space-2) var(--space-3);
  background: var(--bg-surface);
  border: 0.5px solid var(--border-default);
  border-radius: var(--radius-md);
  font-size: 0.8rem;
}

.comment.draft {
  border-style: dashed;
}

.comment-meta {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  font-size: 0.72rem;
  margin-bottom: var(--space-1);
  color: var(--text-secondary);
}

.comment-author {
  font-weight: 500;
  text-transform: capitalize;
}

.author-agent {
  color: var(--accent-purple);
}

.author-user {
  color: var(--accent-blue);
}

.draft-body {
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}

.draft-form {
  display: flex;
  gap: var(--space-2);
}

.draft-line {
  width: 90px;
  flex-shrink: 0;
}

.link-button {
  color: var(--accent-blue);
  font-size: inherit;
  margin-right: var(--space-2);
}

.link-button:hover {
  text-decoration: underline;
}

.review-grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: var(--space-4);
}

@media (max-width: 800px) {
  .review-grid {
    grid-template-columns: minmax(0, 1fr);
  }
}

.box-title {
  margin-bottom: var(--space-3);
}

.box-hint {
  margin-bottom: var(--space-3);
}

.box-actions {
  display: flex;
  justify-content: flex-end;
}

.review {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
  margin-bottom: var(--space-3);
  font-size: 0.85rem;
}

.review .notice {
  margin-bottom: 0;
}

.review-header {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: var(--space-2);
  font-size: 0.75rem;
}

.review-open {
  margin-left: auto;
}
</style>
