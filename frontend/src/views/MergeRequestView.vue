<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { projectApi, type Project, type RepoDiff, type RepoDiffFile } from '@/api/projects'
import {
  mergeConflictsFromError,
  mergeRequestApi,
  type CommentInput,
  type MergeRequestDetail,
  type MergeStrategy,
  type Review,
  type ReviewComment,
  type Verdict,
} from '@/api/mergeRequests'
import MarkdownMessage from '@/components/ide/MarkdownMessage.vue'
import MonacoDiffViewer from '@/components/ide/MonacoDiffViewer.vue'
import AgentModelPicker from '@/components/project/AgentModelPicker.vue'
import MrIcon, { type MrIconName } from '@/components/project/MrIcon.vue'
import StatusBadge from '@/components/project/StatusBadge.vue'

const POLL_INTERVAL_MS = 5000
const NOTICE_MS = 6000

type Tab = 'overview' | 'changes'
type Author = Review['author']
type Tone = 'success' | 'active' | 'review' | 'attention' | 'error' | 'warning' | 'neutral'

const VERDICTS: { value: Verdict; label: string; hint: string; icon: MrIconName; tone: Tone }[] = [
  { value: 'comment', label: 'Comment', hint: 'General feedback without a decision.', icon: 'comment', tone: 'review' },
  { value: 'approve', label: 'Approve', hint: 'The change is ready to merge.', icon: 'check', tone: 'success' },
  { value: 'request_changes', label: 'Request changes', hint: 'Feedback that must be addressed before merging.', icon: 'alert', tone: 'attention' },
]

const STRATEGIES: { value: MergeStrategy; label: string; hint: string }[] = [
  { value: 'merge', label: 'Merge commit', hint: 'Keeps every commit and records a merge commit.' },
  { value: 'squash', label: 'Squash', hint: 'Combines the branch into a single commit.' },
  { value: 'fast-forward', label: 'Fast-forward', hint: 'Moves the target branch forward; no new commit.' },
]

const route = useRoute()
const router = useRouter()
const projectId = computed(() => Number(route.params.id))
const mrId = computed(() => Number(route.params.mrId))

const tab = computed<Tab>({
  get: () => (route.query.tab === 'changes' ? 'changes' : 'overview'),
  set: (value) => {
    void router.replace({ query: { ...route.query, tab: value === 'overview' ? undefined : value } })
  },
})

const project = ref<Project | null>(null)
const mr = ref<MergeRequestDetail | null>(null)
const diff = ref<RepoDiff | null>(null)
const loadError = ref('')
const actionError = ref('')
const notice = ref('')
const busy = ref(false)

const selectedPath = ref('')
const fileFilter = ref('')
const oldContent = ref('')
const newContent = ref('')
const fileLoading = ref(false)
const fileError = ref('')
const sideBySide = ref(window.innerWidth >= 1000)
const reveal = ref<{ line: number } | null>(null)
const fileCache = new Map<string, string | null>()

const reviewOpen = ref(false)
const reviewBox = ref<HTMLElement | null>(null)
const reviewError = ref('')
const verdict = ref<Verdict>('comment')
const reviewBody = ref('')
const drafts = ref<CommentInput[]>([])
const draftLine = ref<number | null>(null)
const draftBody = ref('')
const draftError = ref('')
const draftInput = ref<HTMLTextAreaElement | null>(null)
const sendToAgent = ref(true)

const agentModel = ref('')
const agentEffort = ref('')
const autoFix = ref(true)

const strategy = ref<MergeStrategy>('merge')
const pushAfterMerge = ref(true)
const conflicts = ref<string[] | null>(null)
const mergeError = ref('')

let timer: ReturnType<typeof setInterval> | null = null
let noticeTimer: ReturnType<typeof setTimeout> | null = null

const isOpen = computed(() => mr.value?.status === 'open')
const files = computed(() => diff.value?.files ?? [])
const pendingAgentReview = computed(() => mr.value?.reviews.some((r) => r.author === 'agent' && r.status === 'pending') ?? false)
const timelineReviews = computed(() => [...(mr.value?.reviews ?? [])].sort((a, b) => a.id - b.id))
const totals = computed(() =>
  files.value.reduce((sum, f) => ({ additions: sum.additions + f.additions, deletions: sum.deletions + f.deletions }), {
    additions: 0,
    deletions: 0,
  }),
)

// Files sorted so each directory's files sit together, as a tree would show them.
const orderedFiles = computed(() =>
  [...files.value].sort((a, b) => dirName(a.path).localeCompare(dirName(b.path)) || a.path.localeCompare(b.path)),
)

const fileGroups = computed(() => {
  const needle = fileFilter.value.trim().toLowerCase()
  const groups: { dir: string; files: RepoDiffFile[] }[] = []
  for (const file of orderedFiles.value) {
    if (needle && !file.path.toLowerCase().includes(needle)) continue
    const dir = dirName(file.path)
    const last = groups[groups.length - 1]
    if (last && last.dir === dir) last.files.push(file)
    else groups.push({ dir, files: [file] })
  }
  return groups
})

const selectedIndex = computed(() => orderedFiles.value.findIndex((f) => f.path === selectedPath.value))
const selectedFile = computed(() => orderedFiles.value[selectedIndex.value] ?? null)

const commentsByPath = computed(() => {
  const byPath = new Map<string, ReviewComment[]>()
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
const markedLines = computed(() => [
  ...selectedComments.value.filter((c) => c.headSha === mr.value?.headSha).map((c) => c.line),
  ...selectedDrafts.value.map((d) => d.line),
])

// The newest review from each kind of reviewer.
const latestReviews = computed(() => {
  const latest = new Map<Author, Review>()
  for (const review of timelineReviews.value) latest.set(review.author, review)
  return latest
})

interface Check {
  key: string
  tone: Tone
  icon: MrIconName
  title: string
  detail?: string
  spin?: boolean
}

const checks = computed<Check[]>(() => {
  const m = mr.value
  if (!m) return []
  const list: Check[] = []
  const submitted = [...latestReviews.value.values()].filter((r) => r.status === 'submitted')
  const requested = submitted.filter((r) => r.verdict === 'request_changes')
  const approved = submitted.filter((r) => r.verdict === 'approve')
  if (requested.length > 0) {
    list.push({
      key: 'review',
      tone: 'attention',
      icon: 'alert',
      title: 'Changes requested',
      detail: `${requested.map((r) => authorName(r.author)).join(' and ')} asked for changes.`,
    })
  } else if (approved.some((r) => r.headSha === m.headSha)) {
    list.push({
      key: 'review',
      tone: 'success',
      icon: 'check',
      title: 'Approved',
      detail: `Approved by ${approved.map((r) => authorName(r.author).toLowerCase()).join(' and ')}.`,
    })
  } else if (approved.length > 0) {
    list.push({ key: 'review', tone: 'warning', icon: 'clock', title: 'Approved an older commit', detail: 'New commits landed after the approval.' })
  } else {
    list.push({ key: 'review', tone: 'neutral', icon: 'comment', title: 'Not approved yet', detail: 'Review the changes or ask an agent to.' })
  }
  if (pendingAgentReview.value) {
    list.push({ key: 'agent', tone: 'active', icon: 'bot', title: 'Agent review in progress', detail: 'An agent is reading the change.', spin: true })
  }
  if (conflicts.value) {
    list.push({
      key: 'branch',
      tone: 'error',
      icon: 'warning',
      title: 'Merge conflicts',
      detail: `${m.targetBranch} conflicts with this branch in ${plural(conflicts.value.length, 'file')}.`,
    })
  } else if (m.behind > 0) {
    list.push({
      key: 'branch',
      tone: 'warning',
      icon: 'branch',
      title: `${m.targetBranch} has moved on`,
      detail: `${plural(m.behind, 'new commit')} on ${m.targetBranch} since this branch started.`,
    })
  } else {
    list.push({ key: 'branch', tone: 'success', icon: 'branch', title: `Up to date with ${m.targetBranch}`, detail: `${plural(m.ahead, 'commit')} to merge.` })
  }
  return list
})

const strategyHint = computed(() => {
  if (strategy.value === 'fast-forward' && (mr.value?.behind ?? 0) > 0) {
    return `${mr.value?.targetBranch} has new commits, so a fast-forward is not possible.`
  }
  return STRATEGIES.find((s) => s.value === strategy.value)?.hint ?? ''
})

const statusIcon = computed<MrIconName>(() => {
  if (mr.value?.status === 'merged') return 'merged'
  if (mr.value?.status === 'closed') return 'closed'
  return 'open'
})

function plural(count: number, noun: string): string {
  return `${count} ${noun}${count === 1 ? '' : 's'}`
}

function dirName(path: string): string {
  const i = path.lastIndexOf('/')
  return i === -1 ? '' : path.slice(0, i)
}

function baseName(path: string): string {
  return path.slice(path.lastIndexOf('/') + 1)
}

function authorName(author: Author): string {
  return author === 'agent' ? 'Agent' : 'You'
}

function commentCount(path: string): number {
  return (commentsByPath.value.get(path)?.length ?? 0) + drafts.value.filter((d) => d.path === path).length
}

function reviewComments(review: Review): ReviewComment[] {
  return (mr.value?.comments ?? []).filter((c) => c.reviewId === review.id).sort((a, b) => a.path.localeCompare(b.path) || a.line - b.line)
}

function reviewTone(review: Review): Tone {
  if (review.status === 'pending') return 'active'
  if (review.status === 'failed') return 'error'
  if (review.verdict === 'approve') return 'success'
  if (review.verdict === 'request_changes') return 'attention'
  return 'review'
}

function reviewIcon(review: Review): MrIconName {
  if (review.status === 'pending') return 'clock'
  if (review.status === 'failed') return 'warning'
  if (review.verdict === 'approve') return 'check'
  if (review.verdict === 'request_changes') return 'alert'
  return 'comment'
}

function reviewAction(review: Review): string {
  if (review.status === 'pending') return 'is reviewing the change'
  if (review.status === 'failed') return 'could not finish the review'
  if (review.verdict === 'approve') return 'approved these changes'
  if (review.verdict === 'request_changes') return 'requested changes'
  return 'left a review'
}

function shortSha(sha: string): string {
  return sha ? sha.slice(0, 8) : ''
}

function timeAgo(iso: string | null | undefined): string {
  if (!iso) return ''
  const seconds = Math.max(0, (Date.now() - new Date(iso).getTime()) / 1000)
  if (seconds < 60) return 'just now'
  const minutes = Math.floor(seconds / 60)
  if (minutes < 60) return `${minutes}m ago`
  const hours = Math.floor(minutes / 60)
  if (hours < 24) return `${hours}h ago`
  const days = Math.floor(hours / 24)
  if (days < 30) return `${days}d ago`
  return new Date(iso).toLocaleDateString()
}

function fullTime(iso: string | null | undefined): string {
  return iso ? new Date(iso).toLocaleString() : ''
}

// Five blocks split between additions and deletions, like a diffstat.
function statBlocks(additions: number, deletions: number): ('add' | 'del' | 'none')[] {
  const total = additions + deletions
  if (total === 0) return Array(5).fill('none')
  const add = Math.round((additions / total) * 5)
  return Array.from({ length: 5 }, (_, i) => (i < add ? 'add' : 'del'))
}

function errorText(e: unknown, fallback: string): string {
  return e instanceof Error ? e.message : fallback
}

function showNotice(text: string) {
  notice.value = text
  if (noticeTimer !== null) clearTimeout(noticeTimer)
  noticeTimer = setTimeout(() => (notice.value = ''), NOTICE_MS)
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
    selectedPath.value = orderedFiles.value[0]?.path ?? ''
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

function selectFile(path: string, line?: number) {
  reveal.value = line ? { line } : null
  selectedPath.value = path
}

function stepFile(offset: number) {
  const next = orderedFiles.value[selectedIndex.value + offset]
  if (next) selectFile(next.path)
}

function jumpToComment(path: string, line: number) {
  tab.value = 'changes'
  selectFile(path, line)
}

function startComment(line: number) {
  if (!isOpen.value) return
  draftLine.value = line
  draftError.value = ''
  void nextTick(() => draftInput.value?.focus())
}

watch(selectedPath, () => {
  draftLine.value = null
  draftBody.value = ''
  draftError.value = ''
  void loadFile()
})

watch([projectId, mrId], () => {
  mr.value = null
  diff.value = null
  project.value = null
  fileCache.clear()
  drafts.value = []
  conflicts.value = null
  reviewOpen.value = false
  void refresh()
})

function onDocumentPointer(e: PointerEvent) {
  if (reviewOpen.value && reviewBox.value && !reviewBox.value.contains(e.target as Node)) reviewOpen.value = false
}

function onKeydown(e: KeyboardEvent) {
  if (e.key === 'Escape') reviewOpen.value = false
}

onMounted(() => {
  void refresh()
  timer = setInterval(() => {
    if (document.visibilityState === 'visible' && isOpen.value) void refresh()
  }, POLL_INTERVAL_MS)
  document.addEventListener('pointerdown', onDocumentPointer)
  document.addEventListener('keydown', onKeydown)
})

onUnmounted(() => {
  if (timer !== null) clearInterval(timer)
  if (noticeTimer !== null) clearTimeout(noticeTimer)
  document.removeEventListener('pointerdown', onDocumentPointer)
  document.removeEventListener('keydown', onKeydown)
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
    draftError.value = 'Pick a line in the new version of the file'
    return
  }
  if (!draftBody.value.trim()) {
    draftError.value = 'A comment needs some text'
    return
  }
  draftError.value = ''
  drafts.value.push({ path: selectedPath.value, line, body: draftBody.value.trim() })
  draftLine.value = null
  draftBody.value = ''
}

function removeDraft(draft: CommentInput) {
  drafts.value = drafts.value.filter((d) => d !== draft)
}

async function act(action: () => Promise<void>, error = actionError) {
  busy.value = true
  error.value = ''
  notice.value = ''
  try {
    await action()
  } catch (e) {
    error.value = errorText(e, 'Request failed')
  } finally {
    busy.value = false
  }
}

function submitReview() {
  if (!reviewBody.value.trim() && drafts.value.length === 0 && verdict.value !== 'approve') {
    reviewError.value = 'Write a summary or add a line comment'
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
    reviewOpen.value = false
    showNotice('Review submitted.')
    await refresh()
  }, reviewError)
}

function startAgentReview() {
  void act(async () => {
    await mergeRequestApi.startAgentReview(projectId.value, mrId.value, {
      model: agentModel.value || undefined,
      effort: agentEffort.value || undefined,
      autoFix: autoFix.value && Boolean(mr.value?.taskId),
    })
    showNotice('A review agent is reading the change.')
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
        showNotice(res.pushed ? 'Merged and pushed upstream.' : 'Merged.')
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
  }, mergeError)
}

function resolveConflicts() {
  const files = conflicts.value ?? []
  void act(async () => {
    await mergeRequestApi.resolveConflicts(projectId.value, mrId.value, files)
    conflicts.value = null
    showNotice('The task agent is merging the default branch in and resolving the conflicts.')
    await refresh()
  }, mergeError)
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
    <nav class="crumbs" aria-label="Breadcrumb">
      <RouterLink :to="{ name: 'projects' }">Projects</RouterLink>
      <MrIcon name="chevron-right" :size="12" />
      <RouterLink :to="{ name: 'project', params: { id: projectId } }">{{ project?.name ?? 'Project' }}</RouterLink>
      <MrIcon name="chevron-right" :size="12" />
      <span>Merge request !{{ mrId }}</span>
    </nav>

    <p v-if="!mr && loadError" class="notice notice-error">{{ loadError }}</p>
    <div v-else-if="!mr" class="loading-state">
      <span class="spinner" />
      Loading merge request...
    </div>

    <template v-else>
      <header class="mr-header">
        <div class="mr-heading">
          <h1 class="page-title">
            {{ mr.title }}
            <span class="mr-number">!{{ mr.id }}</span>
          </h1>
          <div class="mr-summary">
            <span class="state-pill" :class="`state-${mr.status}`">
              <MrIcon :name="statusIcon" :size="13" />
              {{ mr.status }}
            </span>
            <span class="branch-chip" :title="mr.sourceBranch">
              <MrIcon name="branch" :size="12" />
              {{ mr.sourceBranch }}
            </span>
            <MrIcon name="arrow-right" :size="13" class="muted" />
            <span class="branch-chip" :title="mr.targetBranch">
              <MrIcon name="branch" :size="12" />
              {{ mr.targetBranch }}
            </span>
            <span class="summary-text" :title="fullTime(mr.createdAt)">opened {{ timeAgo(mr.createdAt) }}</span>
          </div>
        </div>
        <div v-if="isOpen" class="header-actions">
          <button class="btn btn-secondary" :disabled="busy" @click="close">Close merge request</button>
        </div>
      </header>

      <p v-if="loadError" class="notice notice-error">{{ loadError }}</p>
      <p v-if="actionError" class="notice notice-error">{{ actionError }}</p>
      <Transition name="fade">
        <p v-if="notice" class="notice notice-success notice-row">
          <MrIcon name="check" :size="14" />
          {{ notice }}
        </p>
      </Transition>

      <div class="tabbar">
        <div class="tabs" role="tablist">
          <button type="button" role="tab" class="tab" :class="{ active: tab === 'overview' }" :aria-selected="tab === 'overview'" @click="tab = 'overview'">
            <MrIcon name="comment" :size="14" />
            Overview
            <span v-if="mr.reviews.length" class="tab-count">{{ mr.reviews.length }}</span>
          </button>
          <button type="button" role="tab" class="tab" :class="{ active: tab === 'changes' }" :aria-selected="tab === 'changes'" @click="tab = 'changes'">
            <MrIcon name="file" :size="14" />
            Changes
            <span class="tab-count">{{ files.length }}</span>
          </button>
        </div>
        <div class="tabbar-end">
          <span class="diffstat" :title="`${totals.additions} additions, ${totals.deletions} deletions`">
            <span class="add">+{{ totals.additions }}</span>
            <span class="del">&minus;{{ totals.deletions }}</span>
            <span class="stat-blocks">
              <span v-for="(b, i) in statBlocks(totals.additions, totals.deletions)" :key="i" :class="`block-${b}`" />
            </span>
          </span>
          <div v-if="isOpen" ref="reviewBox" class="review-anchor">
            <button class="btn btn-primary btn-sm" :aria-expanded="reviewOpen" @click="reviewOpen = !reviewOpen">
              Review
              <span v-if="drafts.length" class="draft-count">{{ drafts.length }}</span>
              <MrIcon name="chevron-down" :size="13" />
            </button>
            <Transition name="pop">
              <div v-if="reviewOpen" class="review-pop" role="dialog" aria-label="Finish your review">
                <div class="pop-header">
                  <span class="pop-title">Finish your review</span>
                  <button class="icon-button" aria-label="Close" @click="reviewOpen = false">
                    <MrIcon name="close" :size="14" />
                  </button>
                </div>
                <textarea
                  v-model="reviewBody"
                  class="form-input form-textarea"
                  rows="4"
                  placeholder="Leave a summary (Markdown)"
                  aria-label="Review summary"
                />
                <div class="verdicts" role="radiogroup" aria-label="Verdict">
                  <label v-for="v in VERDICTS" :key="v.value" class="verdict" :class="[`tone-${v.tone}`, { active: verdict === v.value }]">
                    <input v-model="verdict" type="radio" name="verdict" :value="v.value" />
                    <MrIcon :name="v.icon" :size="15" class="verdict-icon" />
                    <span class="verdict-text">
                      <span class="verdict-label">{{ v.label }}</span>
                      <span class="verdict-hint">{{ v.hint }}</span>
                    </span>
                  </label>
                </div>
                <div v-if="drafts.length" class="pop-drafts">
                  <span class="form-hint">{{ plural(drafts.length, 'line comment') }} will be included</span>
                  <button
                    v-for="(d, i) in drafts"
                    :key="i"
                    class="location-chip"
                    @click="reviewOpen = false; jumpToComment(d.path, d.line)"
                  >
                    {{ baseName(d.path) }}:{{ d.line }}
                  </button>
                </div>
                <p v-else class="form-hint">Tip: click a line number in the Changes tab to comment on a line.</p>
                <label v-if="verdict === 'request_changes' && mr.taskId" class="form-check">
                  <input v-model="sendToAgent" type="checkbox" />
                  Send the feedback to the task's agent now
                </label>
                <p v-if="reviewError" class="form-error">{{ reviewError }}</p>
                <div class="pop-actions">
                  <button class="btn btn-secondary btn-sm" @click="reviewOpen = false">Cancel</button>
                  <button class="btn btn-primary btn-sm" :disabled="busy" @click="submitReview">Submit review</button>
                </div>
              </div>
            </Transition>
          </div>
        </div>
      </div>

      <!-- Overview -->
      <div v-if="tab === 'overview'" class="overview">
        <div class="main-col">
          <article class="card">
            <div class="card-header">
              <span class="avatar avatar-user"><MrIcon name="user" :size="13" /></span>
              <span class="card-header-text">
                <strong>Description</strong>
                <span class="muted" :title="fullTime(mr.createdAt)">&middot; {{ timeAgo(mr.createdAt) }}</span>
              </span>
            </div>
            <div class="card-body description">
              <MarkdownMessage v-if="mr.description" :content="mr.description" />
              <p v-else class="muted">No description provided.</p>
            </div>
          </article>

          <!-- Merge widget -->
          <section v-if="isOpen" class="card merge-card" aria-label="Merge">
            <ul class="checks">
              <li v-for="check in checks" :key="check.key" class="check" :class="`tone-${check.tone}`">
                <span class="check-icon">
                  <span v-if="check.spin" class="spinner spinner-sm" />
                  <MrIcon v-else :name="check.icon" :size="14" />
                </span>
                <span class="check-text">
                  <span class="check-title">{{ check.title }}</span>
                  <span v-if="check.detail" class="check-detail">{{ check.detail }}</span>
                </span>
              </li>
            </ul>
            <div v-if="conflicts" class="conflicts">
              <ul class="conflict-list">
                <li v-for="file in conflicts" :key="file" class="mono">
                  <MrIcon name="file" :size="12" />
                  {{ file }}
                </li>
              </ul>
              <button v-if="mr.taskId" class="btn btn-secondary btn-sm" :disabled="busy" @click="resolveConflicts">
                <MrIcon name="bot" :size="13" />
                Ask the task's agent to resolve them
              </button>
            </div>
            <div class="merge-footer">
              <div class="merge-options">
                <div class="segmented" role="radiogroup" aria-label="Merge strategy">
                  <button
                    v-for="s in STRATEGIES"
                    :key="s.value"
                    type="button"
                    role="radio"
                    :aria-checked="strategy === s.value"
                    class="segment"
                    :class="{ active: strategy === s.value }"
                    @click="strategy = s.value"
                  >
                    {{ s.label }}
                  </button>
                </div>
                <span class="form-hint strategy-hint">{{ strategyHint }}</span>
              </div>
              <div class="merge-actions">
                <label v-if="project?.upstreamUrl" class="form-check">
                  <input v-model="pushAfterMerge" type="checkbox" />
                  Push {{ mr.targetBranch }} upstream
                </label>
                <button class="btn btn-success" :disabled="busy" @click="merge">
                  <MrIcon name="merged" :size="14" />
                  Merge into {{ mr.targetBranch }}
                </button>
              </div>
            </div>
            <p v-if="mergeError" class="form-error merge-error">{{ mergeError }}</p>
          </section>

          <section v-else class="card outcome-card" :class="`state-${mr.status}`">
            <span class="outcome-icon"><MrIcon :name="statusIcon" :size="18" /></span>
            <div v-if="mr.status === 'merged'">
              <strong>Merged into {{ mr.targetBranch }}</strong>
              <p class="muted outcome-detail">
                as <span class="mono">{{ shortSha(mr.mergeSha) }}</span>
                <span :title="fullTime(mr.mergedAt)"> &middot; {{ timeAgo(mr.mergedAt) }}</span>
              </p>
            </div>
            <div v-else>
              <strong>Closed without merging</strong>
              <p class="muted outcome-detail" :title="fullTime(mr.updatedAt)">{{ timeAgo(mr.updatedAt) }}</p>
            </div>
          </section>

          <!-- Activity -->
          <section class="activity">
            <h2 class="section-title activity-title">Activity</h2>
            <ol class="timeline">
              <li class="event event-small">
                <span class="event-node tone-active"><MrIcon name="open" :size="13" /></span>
                <span class="event-line">
                  Opened from <span class="mono">{{ mr.sourceBranch }}</span>
                  <template v-if="mr.taskId"> by task #{{ mr.taskId }}</template>
                  <span class="muted" :title="fullTime(mr.createdAt)"> &middot; {{ timeAgo(mr.createdAt) }}</span>
                </span>
              </li>

              <li v-for="review in timelineReviews" :key="review.id" class="event">
                <span class="event-node" :class="`tone-${reviewTone(review)}`">
                  <MrIcon :name="reviewIcon(review)" :size="13" />
                </span>
                <article class="card review-card" :class="`edge-${reviewTone(review)}`">
                  <div class="card-header">
                    <span class="avatar" :class="`avatar-${review.author}`">
                      <MrIcon :name="review.author === 'agent' ? 'bot' : 'user'" :size="13" />
                    </span>
                    <span class="card-header-text">
                      <strong>{{ authorName(review.author) }}</strong>
                      <span>{{ reviewAction(review) }}</span>
                      <span class="muted" :title="fullTime(review.createdAt)">&middot; {{ timeAgo(review.createdAt) }}</span>
                    </span>
                    <span class="card-header-end">
                      <span v-if="review.model" class="tag">{{ review.model }}</span>
                      <span v-if="review.headSha && review.headSha !== mr.headSha" class="tag tag-outdated" :title="`Reviewed ${shortSha(review.headSha)}`">outdated</span>
                      <span v-if="review.status === 'pending'" class="spinner spinner-sm" />
                    </span>
                  </div>
                  <div v-if="review.error || review.body || reviewComments(review).length || (review.workspaceId && review.status === 'pending')" class="card-body">
                    <p v-if="review.error" class="notice notice-error">{{ review.error }}</p>
                    <MarkdownMessage v-if="review.body" :content="review.body" />
                    <div v-if="reviewComments(review).length" class="thread-list">
                      <div v-for="c in reviewComments(review)" :key="c.id" class="thread">
                        <button class="thread-location" :title="`Open ${c.path} at line ${c.line}`" @click="jumpToComment(c.path, c.line)">
                          <MrIcon name="file" :size="12" />
                          <span class="mono">{{ c.path }}</span>
                          <span class="line-chip">L{{ c.line }}</span>
                          <MrIcon name="chevron-right" :size="12" class="thread-go" />
                        </button>
                        <div class="thread-body"><MarkdownMessage :content="c.body" /></div>
                      </div>
                    </div>
                    <RouterLink
                      v-if="review.workspaceId && review.status === 'pending'"
                      class="btn btn-secondary btn-sm watch-link"
                      :to="{ name: 'ide', params: { id: review.workspaceId } }"
                    >
                      <MrIcon name="terminal" :size="13" />
                      Watch in sandbox
                    </RouterLink>
                  </div>
                </article>
              </li>

              <li v-if="mr.status !== 'open'" class="event event-small">
                <span class="event-node" :class="mr.status === 'merged' ? 'tone-success' : 'tone-neutral'">
                  <MrIcon :name="statusIcon" :size="13" />
                </span>
                <span class="event-line">
                  <template v-if="mr.status === 'merged'">
                    Merged into <span class="mono">{{ mr.targetBranch }}</span>
                    <span class="muted" :title="fullTime(mr.mergedAt)"> &middot; {{ timeAgo(mr.mergedAt) }}</span>
                  </template>
                  <template v-else>
                    Closed
                    <span class="muted" :title="fullTime(mr.updatedAt)"> &middot; {{ timeAgo(mr.updatedAt) }}</span>
                  </template>
                </span>
              </li>
            </ol>
            <p v-if="timelineReviews.length === 0 && isOpen" class="empty-text timeline-empty">
              No reviews yet. Use <strong>Review</strong> above, or start an agent review from the sidebar.
            </p>
          </section>
        </div>

        <aside class="side-col">
          <section class="card side-card">
            <h3 class="side-title">Details</h3>
            <dl class="details">
              <dt>Source</dt>
              <dd><span class="mono ellipsis" :title="mr.sourceBranch">{{ mr.sourceBranch }}</span></dd>
              <dt>Target</dt>
              <dd><span class="mono ellipsis" :title="mr.targetBranch">{{ mr.targetBranch }}</span></dd>
              <dt>Head</dt>
              <dd><span class="mono" :title="mr.headSha">{{ shortSha(mr.headSha) }}</span></dd>
              <template v-if="isOpen">
                <dt>Commits</dt>
                <dd>
                  <span>{{ mr.ahead }} ahead</span>
                  <span class="muted">&middot;</span>
                  <span :class="{ warn: mr.behind > 0 }">{{ mr.behind }} behind</span>
                </dd>
              </template>
              <dt>Changes</dt>
              <dd>
                <button class="text-button" @click="tab = 'changes'">{{ plural(files.length, 'file') }}</button>
                <span class="add mono">+{{ totals.additions }}</span>
                <span class="del mono">&minus;{{ totals.deletions }}</span>
              </dd>
              <template v-if="mr.taskId">
                <dt>Task</dt>
                <dd>
                  <RouterLink :to="{ name: 'project', params: { id: projectId }, query: { task: mr.taskId } }" class="task-link">
                    <MrIcon name="task" :size="12" />
                    #{{ mr.taskId }}
                  </RouterLink>
                </dd>
              </template>
              <dt>Opened</dt>
              <dd :title="fullTime(mr.createdAt)">{{ timeAgo(mr.createdAt) }}</dd>
              <dt>Updated</dt>
              <dd :title="fullTime(mr.updatedAt)">{{ timeAgo(mr.updatedAt) }}</dd>
            </dl>
          </section>

          <section class="card side-card">
            <h3 class="side-title">Reviewers</h3>
            <div v-for="author in (['user', 'agent'] as const)" :key="author" class="reviewer">
              <span class="avatar" :class="`avatar-${author}`">
                <MrIcon :name="author === 'agent' ? 'bot' : 'user'" :size="13" />
              </span>
              <span class="reviewer-name">{{ authorName(author) }}</span>
              <template v-if="latestReviews.get(author)">
                <StatusBadge
                  :status="latestReviews.get(author)!.status === 'submitted' && latestReviews.get(author)!.verdict
                    ? latestReviews.get(author)!.verdict
                    : latestReviews.get(author)!.status"
                />
              </template>
              <span v-else class="muted reviewer-none">No review</span>
            </div>
          </section>

          <section v-if="isOpen" class="card side-card">
            <h3 class="side-title">
              <MrIcon name="bot" :size="14" />
              Agent review
            </h3>
            <p class="form-hint side-hint">
              An agent reads the change in its own sandbox and leaves line comments and a verdict.
            </p>
            <AgentModelPicker v-model:model="agentModel" v-model:effort="agentEffort" class="side-picker" />
            <label v-if="mr.taskId" class="form-check side-check">
              <input v-model="autoFix" type="checkbox" />
              Send requested changes to the task's agent
            </label>
            <button class="btn btn-secondary side-button" :disabled="busy || pendingAgentReview || !agentModel" @click="startAgentReview">
              <span v-if="pendingAgentReview" class="spinner spinner-sm" />
              {{ pendingAgentReview ? 'Review in progress...' : 'Start agent review' }}
            </button>
          </section>
        </aside>
      </div>

      <!-- Changes -->
      <div v-else class="changes-tab">
        <div v-if="files.length === 0" class="card empty-card">
          <MrIcon name="file" :size="28" :stroke-width="1.5" />
          <p>This branch changes nothing.</p>
        </div>
        <div v-else class="changes">
          <nav class="file-tree card" aria-label="Changed files">
            <div class="tree-search">
              <MrIcon name="search" :size="13" />
              <input v-model="fileFilter" type="text" placeholder="Filter files" aria-label="Filter files" />
            </div>
            <div class="tree-body">
              <div v-for="group in fileGroups" :key="group.dir" class="tree-group">
                <div v-if="group.dir" class="tree-dir" :title="group.dir">
                  <MrIcon name="folder" :size="12" />
                  <span class="ellipsis-start">{{ group.dir }}</span>
                </div>
                <button
                  v-for="file in group.files"
                  :key="file.path"
                  type="button"
                  class="tree-file"
                  :class="{ active: file.path === selectedPath, nested: group.dir }"
                  :title="file.oldPath ? `${file.oldPath} → ${file.path}` : file.path"
                  @click="selectFile(file.path)"
                >
                  <span class="file-status" :class="`status-${file.status}`">{{ file.status }}</span>
                  <span class="file-name">{{ baseName(file.path) }}</span>
                  <span v-if="commentCount(file.path)" class="file-comments">
                    <MrIcon name="comment" :size="10" />
                    {{ commentCount(file.path) }}
                  </span>
                  <span v-if="!file.binary" class="file-stats">
                    <span class="add">+{{ file.additions }}</span>
                    <span class="del">&minus;{{ file.deletions }}</span>
                  </span>
                </button>
              </div>
              <p v-if="fileGroups.length === 0" class="empty-text tree-empty">No files match.</p>
            </div>
          </nav>

          <div v-if="selectedFile" class="file-view">
            <div class="file-bar">
              <span class="file-status-pill" :class="`status-${selectedFile.status}`">{{ fileStatusLabel(selectedFile) }}</span>
              <span class="file-bar-path" :title="selectedFile.path">
                <template v-if="selectedFile.oldPath"><span class="muted">{{ selectedFile.oldPath }} &rarr; </span></template>
                <span class="muted">{{ dirName(selectedFile.path) ? `${dirName(selectedFile.path)}/` : '' }}</span><strong>{{ baseName(selectedFile.path) }}</strong>
              </span>
              <span v-if="!selectedFile.binary" class="file-bar-stats">
                <span class="add">+{{ selectedFile.additions }}</span>
                <span class="del">&minus;{{ selectedFile.deletions }}</span>
                <span class="stat-blocks">
                  <span v-for="(b, i) in statBlocks(selectedFile.additions, selectedFile.deletions)" :key="i" :class="`block-${b}`" />
                </span>
              </span>
              <div class="file-bar-end">
                <div class="segmented segmented-icons" role="radiogroup" aria-label="Diff layout">
                  <button type="button" role="radio" class="segment" :class="{ active: sideBySide }" :aria-checked="sideBySide" title="Side by side" @click="sideBySide = true">
                    <MrIcon name="split" :size="13" />
                  </button>
                  <button type="button" role="radio" class="segment" :class="{ active: !sideBySide }" :aria-checked="!sideBySide" title="Unified" @click="sideBySide = false">
                    <MrIcon name="unified" :size="13" />
                  </button>
                </div>
                <div class="pager">
                  <button class="icon-button" :disabled="selectedIndex <= 0" aria-label="Previous file" title="Previous file" @click="stepFile(-1)">
                    <MrIcon name="chevron-left" :size="14" />
                  </button>
                  <span class="pager-count">{{ selectedIndex + 1 }} / {{ orderedFiles.length }}</span>
                  <button class="icon-button" :disabled="selectedIndex >= orderedFiles.length - 1" aria-label="Next file" title="Next file" @click="stepFile(1)">
                    <MrIcon name="chevron-right" :size="14" />
                  </button>
                </div>
              </div>
            </div>

            <p v-if="fileError" class="notice notice-error">{{ fileError }}</p>
            <div v-else-if="selectedFile.binary" class="card empty-card">
              <MrIcon name="file" :size="28" :stroke-width="1.5" />
              <p>Binary file; no preview.</p>
            </div>
            <div v-else class="diff-frame" :class="{ loading: fileLoading }">
              <MonacoDiffViewer
                :old-content="oldContent"
                :new-content="newContent"
                :old-file-name="selectedFile.oldPath || selectedFile.path"
                :new-file-name="selectedFile.path"
                :side-by-side="sideBySide"
                :marked-lines="markedLines"
                :reveal="reveal"
                :commentable="isOpen"
                @line-click="startComment"
              />
              <span v-if="fileLoading" class="diff-loading"><span class="spinner spinner-sm" /></span>
            </div>

            <section class="file-comments-section">
              <div class="file-comments-header">
                <span class="section-title">
                  Comments on this file
                  <span class="muted">{{ selectedComments.length + selectedDrafts.length }}</span>
                </span>
                <span v-if="isOpen" class="form-hint">Click a line number in the new version to comment on it.</span>
              </div>

              <div v-for="c in selectedComments" :key="c.id" class="comment" :class="{ outdated: c.headSha !== mr.headSha }">
                <div class="comment-meta">
                  <span class="avatar avatar-sm" :class="`avatar-${c.author}`">
                    <MrIcon :name="c.author === 'agent' ? 'bot' : 'user'" :size="11" />
                  </span>
                  <span class="comment-author">{{ authorName(c.author) }}</span>
                  <button class="line-chip line-chip-button" :title="`Show line ${c.line}`" @click="selectFile(c.path, c.line)">L{{ c.line }}</button>
                  <span v-if="c.headSha !== mr.headSha" class="tag tag-outdated">outdated</span>
                  <span class="muted comment-time" :title="fullTime(c.createdAt)">{{ timeAgo(c.createdAt) }}</span>
                </div>
                <MarkdownMessage :content="c.body" />
              </div>

              <div v-for="(d, i) in selectedDrafts" :key="`draft-${i}`" class="comment draft">
                <div class="comment-meta">
                  <span class="avatar avatar-sm avatar-user"><MrIcon name="user" :size="11" /></span>
                  <span class="comment-author">You</span>
                  <button class="line-chip line-chip-button" :title="`Show line ${d.line}`" @click="selectFile(d.path, d.line)">L{{ d.line }}</button>
                  <span class="tag tag-draft">pending</span>
                  <button class="icon-button comment-remove" aria-label="Remove comment" title="Remove" @click="removeDraft(d)">
                    <MrIcon name="close" :size="13" />
                  </button>
                </div>
                <p class="draft-body">{{ d.body }}</p>
              </div>

              <form v-if="isOpen" class="draft-form" @submit.prevent="addDraft">
                <div class="draft-form-row">
                  <label class="draft-line-label">
                    Line
                    <input
                      v-model.number="draftLine"
                      class="form-input draft-line"
                      type="number"
                      min="1"
                      placeholder="#"
                      aria-label="Line number in the new file"
                    />
                  </label>
                  <textarea
                    ref="draftInput"
                    v-model="draftBody"
                    class="form-input form-textarea draft-text"
                    rows="2"
                    placeholder="Comment on this line... (Markdown)"
                    aria-label="Comment"
                    @keydown.meta.enter.prevent="addDraft"
                    @keydown.ctrl.enter.prevent="addDraft"
                  />
                </div>
                <div class="draft-form-footer">
                  <span v-if="draftError" class="form-error draft-error">{{ draftError }}</span>
                  <span v-else class="form-hint">Comments are held until you submit your review.</span>
                  <button type="submit" class="btn btn-secondary btn-sm">Add to review</button>
                </div>
              </form>
            </section>
          </div>
        </div>
      </div>
    </template>
  </div>
</template>

<style scoped src="@/components/project/shared.css"></style>
<style scoped>
/* Header */
.crumbs {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: var(--space-1);
  font-size: 0.78rem;
  color: var(--text-muted);
  margin-bottom: var(--space-3);
}

.crumbs a {
  color: var(--text-secondary);
}

.crumbs a:hover {
  color: var(--text-primary);
}

.loading-state {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  color: var(--text-muted);
  font-size: 0.8rem;
  padding: var(--space-6) 0;
}

.mr-header {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  flex-wrap: wrap;
  gap: var(--space-3);
  margin-bottom: var(--space-5);
}

.mr-heading {
  min-width: 0;
  flex: 1;
}

.mr-number {
  color: var(--text-muted);
  font-weight: 400;
}

.mr-summary {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: var(--space-2);
  margin-top: var(--space-2);
  font-size: 0.78rem;
  color: var(--text-secondary);
}

.state-pill {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  padding: 3px 10px 3px 8px;
  border-radius: 999px;
  font-size: 0.75rem;
  font-weight: 600;
  text-transform: capitalize;
  border: 0.5px solid transparent;
}

.state-open {
  color: var(--accent-blue);
  background: color-mix(in srgb, var(--accent-blue) 14%, transparent);
  border-color: color-mix(in srgb, var(--accent-blue) 32%, transparent);
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

.branch-chip {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  max-width: 260px;
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

.summary-text {
  color: var(--text-muted);
}

.notice-row {
  display: flex;
  align-items: center;
  gap: var(--space-2);
}

/* Tabs */
.tabbar {
  display: flex;
  align-items: flex-end;
  justify-content: space-between;
  gap: var(--space-3);
  border-bottom: 0.5px solid var(--border-default);
  margin-bottom: var(--space-5);
}

.tabs {
  display: flex;
  gap: var(--space-1);
}

.tab {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  padding: var(--space-2) var(--space-3);
  margin-bottom: -0.5px;
  font-size: 0.82rem;
  color: var(--text-muted);
  border-bottom: 2px solid transparent;
  border-radius: var(--radius-md) var(--radius-md) 0 0;
  transition: all var(--transition-fast);
}

.tab:hover {
  color: var(--text-secondary);
  background: var(--bg-hover);
}

.tab.active {
  color: var(--text-primary);
  border-bottom-color: var(--accent-blue);
}

.tab-count {
  min-width: 18px;
  padding: 0 6px;
  border-radius: 999px;
  background: var(--bg-neutral);
  color: var(--text-secondary);
  font-size: 0.7rem;
  text-align: center;
}

.tab.active .tab-count {
  background: color-mix(in srgb, var(--accent-blue) 18%, transparent);
  color: var(--accent-blue);
}

.tabbar-end {
  display: flex;
  align-items: center;
  gap: var(--space-3);
  padding-bottom: var(--space-2);
}

.diffstat {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  font-family: var(--font-mono);
  font-size: 0.75rem;
}

.stat-blocks {
  display: inline-flex;
  gap: 2px;
}

.stat-blocks span {
  width: 7px;
  height: 7px;
  border-radius: 1.5px;
}

.block-add {
  background: var(--success);
}

.block-del {
  background: var(--error);
}

.block-none {
  background: var(--bg-neutral);
}

.add {
  color: var(--success);
}

.del {
  color: var(--error);
}

/* Review popover */
.review-anchor {
  position: relative;
}

.draft-count {
  min-width: 16px;
  padding: 0 5px;
  border-radius: 999px;
  background: var(--bg-primary);
  color: var(--accent-blue);
  font-size: 0.68rem;
  font-weight: 600;
}

.review-pop {
  position: absolute;
  top: calc(100% + var(--space-2));
  right: 0;
  z-index: 50;
  display: flex;
  flex-direction: column;
  gap: var(--space-3);
  width: 420px;
  max-width: calc(100vw - 2 * var(--space-4));
  padding: var(--space-4);
  background: var(--bg-elevated);
  border: 0.5px solid var(--border-strong);
  border-radius: var(--radius-lg);
  box-shadow: var(--shadow-strong);
}

.pop-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
}

.pop-title {
  font-size: 0.9rem;
  font-weight: 500;
}

.verdicts {
  display: flex;
  flex-direction: column;
  gap: var(--space-1);
}

.verdict {
  display: flex;
  align-items: flex-start;
  gap: var(--space-2);
  padding: var(--space-2) var(--space-3);
  border: 0.5px solid var(--border-default);
  border-radius: var(--radius-md);
  cursor: pointer;
  transition: all var(--transition-fast);
}

.verdict input {
  position: absolute;
  opacity: 0;
  pointer-events: none;
}

.verdict:hover {
  background: var(--bg-hover);
}

.verdict:has(input:focus-visible) {
  box-shadow: 0 0 0 3px var(--accent-glow), 0 0 0 1px var(--accent-border);
}

.verdict-icon {
  margin-top: 2px;
  color: var(--text-muted);
}

.verdict-text {
  display: flex;
  flex-direction: column;
}

.verdict-label {
  font-size: 0.8rem;
  font-weight: 500;
}

.verdict-hint {
  font-size: 0.72rem;
  color: var(--text-muted);
}

.verdict.active.tone-review {
  border-color: color-mix(in srgb, var(--accent-purple) 45%, transparent);
  background: color-mix(in srgb, var(--accent-purple) 9%, transparent);
}

.verdict.active.tone-success {
  border-color: var(--success-border);
  background: var(--success-bg);
}

.verdict.active.tone-attention {
  border-color: var(--attention-border);
  background: var(--attention-bg);
}

.verdict.active.tone-review .verdict-icon {
  color: var(--accent-purple);
}

.verdict.active.tone-success .verdict-icon {
  color: var(--success);
}

.verdict.active.tone-attention .verdict-icon {
  color: var(--attention);
}

.pop-drafts {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: var(--space-1);
}

.pop-drafts .form-hint {
  margin: 0 var(--space-1) 0 0;
}

.location-chip {
  padding: 1px 6px;
  border-radius: var(--radius-sm);
  background: var(--bg-raised);
  border: 0.5px solid var(--border-default);
  color: var(--accent-blue);
  font-family: var(--font-mono);
  font-size: 0.7rem;
}

.location-chip:hover {
  border-color: var(--accent-blue);
}

.pop-actions {
  display: flex;
  justify-content: flex-end;
  gap: var(--space-2);
}

.review-pop .form-error {
  margin: 0;
}

.icon-button {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 24px;
  height: 24px;
  border-radius: var(--radius-sm);
  color: var(--text-muted);
  transition: all var(--transition-fast);
}

.icon-button:hover:not(:disabled) {
  color: var(--text-primary);
  background: var(--bg-hover);
}

.icon-button:disabled {
  opacity: 0.35;
  cursor: not-allowed;
}

/* Overview layout */
.overview {
  display: grid;
  grid-template-columns: minmax(0, 1fr) 280px;
  gap: var(--space-5);
  align-items: start;
}

@media (max-width: 900px) {
  .overview {
    grid-template-columns: minmax(0, 1fr);
  }
}

.main-col {
  display: flex;
  flex-direction: column;
  gap: var(--space-4);
  min-width: 0;
}

.side-col {
  display: flex;
  flex-direction: column;
  gap: var(--space-3);
  position: sticky;
  top: var(--space-4);
}

.card {
  background: var(--bg-surface);
  border: 0.5px solid var(--border-default);
  border-radius: var(--radius-lg);
  box-shadow: inset 0 1px 0 var(--border-top-highlight);
}

.card-header {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: var(--space-2);
  padding: var(--space-2) var(--space-3);
  border-bottom: 0.5px solid var(--border-hairline);
  font-size: 0.78rem;
  color: var(--text-secondary);
}

.card-header strong {
  color: var(--text-primary);
  font-weight: 500;
}

.card-header-text {
  display: flex;
  align-items: baseline;
  flex-wrap: wrap;
  gap: 5px;
  min-width: 0;
}

.card-header-end {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  margin-left: auto;
}

.card-body {
  padding: var(--space-3) var(--space-4);
  font-size: 0.85rem;
}

.card-body > .notice {
  margin-bottom: var(--space-3);
}

.description {
  max-height: 50vh;
  overflow-y: auto;
}

/* Markdown from reviews and descriptions. */
:deep(.markdown-body) {
  line-height: 1.6;
  overflow-wrap: anywhere;
}

:deep(.markdown-body > * + *) {
  margin-top: var(--space-2);
}

:deep(.markdown-body ul),
:deep(.markdown-body ol) {
  padding-left: 1.4em;
}

:deep(.markdown-body li + li) {
  margin-top: 2px;
}

:deep(.markdown-body h1),
:deep(.markdown-body h2),
:deep(.markdown-body h3) {
  font-size: 0.95rem;
  font-weight: 500;
}

:deep(.markdown-body code) {
  padding: 1px 4px;
  border-radius: var(--radius-sm);
  background: var(--bg-raised);
  font-size: 0.85em;
}

:deep(.markdown-body pre) {
  padding: var(--space-2) var(--space-3);
  border-radius: var(--radius-md);
  background: var(--bg-void);
  overflow-x: auto;
}

:deep(.markdown-body pre code) {
  padding: 0;
  background: none;
}

:deep(.markdown-body blockquote) {
  padding-left: var(--space-3);
  border-left: 2px solid var(--border-strong);
  color: var(--text-secondary);
}

.avatar {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 24px;
  height: 24px;
  border-radius: 50%;
  flex-shrink: 0;
}

.avatar-sm {
  width: 20px;
  height: 20px;
}

.avatar-user {
  color: var(--accent-blue);
  background: color-mix(in srgb, var(--accent-blue) 16%, transparent);
}

.avatar-agent {
  color: var(--accent-purple);
  background: color-mix(in srgb, var(--accent-purple) 16%, transparent);
}

.tag {
  padding: 0 6px;
  border-radius: var(--radius-sm);
  background: var(--bg-neutral);
  color: var(--text-secondary);
  font-size: 0.68rem;
  white-space: nowrap;
}

.tag-outdated {
  color: var(--warning);
  background: var(--warning-bg);
}

.tag-draft {
  color: var(--accent-blue);
  background: color-mix(in srgb, var(--accent-blue) 14%, transparent);
}

/* Merge widget */
.merge-card {
  overflow: hidden;
}

.checks {
  list-style: none;
}

.check {
  display: flex;
  align-items: flex-start;
  gap: var(--space-3);
  padding: var(--space-3) var(--space-4);
}

.check + .check {
  border-top: 0.5px solid var(--border-hairline);
}

.check-icon {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 26px;
  height: 26px;
  border-radius: 50%;
  flex-shrink: 0;
  color: var(--text-secondary);
  background: var(--bg-neutral);
}

.check-text {
  display: flex;
  flex-direction: column;
  min-width: 0;
}

.check-title {
  font-size: 0.85rem;
  font-weight: 500;
}

.check-detail {
  font-size: 0.75rem;
  color: var(--text-muted);
}

.tone-success .check-icon,
.event-node.tone-success {
  color: var(--success);
  background: var(--success-bg);
}

.tone-active .check-icon,
.event-node.tone-active {
  color: var(--accent-blue);
  background: color-mix(in srgb, var(--accent-blue) 14%, transparent);
}

.tone-review .check-icon,
.event-node.tone-review {
  color: var(--accent-purple);
  background: color-mix(in srgb, var(--accent-purple) 14%, transparent);
}

.tone-attention .check-icon,
.event-node.tone-attention {
  color: var(--attention);
  background: var(--attention-bg);
}

.tone-warning .check-icon,
.event-node.tone-warning {
  color: var(--warning);
  background: var(--warning-bg);
}

.tone-error .check-icon,
.event-node.tone-error {
  color: var(--error);
  background: var(--error-bg);
}

.conflicts {
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  gap: var(--space-3);
  padding: 0 var(--space-4) var(--space-3) calc(var(--space-4) + 26px + var(--space-3));
}

.conflict-list {
  list-style: none;
  display: flex;
  flex-direction: column;
  gap: 2px;
  color: var(--text-secondary);
}

.conflict-list li {
  display: flex;
  align-items: center;
  gap: 6px;
}

.merge-footer {
  display: flex;
  align-items: center;
  justify-content: space-between;
  flex-wrap: wrap;
  gap: var(--space-3);
  padding: var(--space-3) var(--space-4);
  background: var(--bg-raised);
  border-top: 0.5px solid var(--border-default);
}

.merge-options {
  display: flex;
  flex-direction: column;
  gap: var(--space-1);
  min-width: 0;
}

.strategy-hint {
  margin: 0;
}

.merge-actions {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: var(--space-3);
}

.merge-error {
  margin: 0;
  padding: var(--space-2) var(--space-4);
  border-top: 0.5px solid var(--error-border);
  background: var(--error-bg);
}

.segmented {
  display: inline-flex;
  padding: 2px;
  gap: 2px;
  background: var(--bg-base);
  border: 0.5px solid var(--border-default);
  border-radius: var(--radius-md);
  align-self: flex-start;
}

.segment {
  display: inline-flex;
  align-items: center;
  padding: 3px var(--space-3);
  border-radius: var(--radius-sm);
  font-size: 0.75rem;
  color: var(--text-muted);
  transition: all var(--transition-fast);
}

.segment:hover {
  color: var(--text-primary);
}

.segment.active {
  background: var(--bg-raised);
  color: var(--text-primary);
  box-shadow: 0 0 0 0.5px var(--border-strong);
}

.segmented-icons .segment {
  padding: 3px 6px;
}

.outcome-card {
  display: flex;
  align-items: center;
  gap: var(--space-3);
  padding: var(--space-3) var(--space-4);
  font-size: 0.85rem;
}

.outcome-card strong {
  font-weight: 500;
}

.outcome-icon {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 36px;
  height: 36px;
  border-radius: 50%;
  background: var(--bg-neutral);
}

.outcome-card.state-merged {
  border-color: var(--success-border);
  background: color-mix(in srgb, var(--success) 6%, var(--bg-surface));
}

.outcome-card.state-merged .outcome-icon {
  color: var(--success);
  background: var(--success-bg);
}

.outcome-detail {
  font-size: 0.75rem;
}

/* Activity timeline */
.activity-title {
  margin: var(--space-2) 0 var(--space-3);
}

.timeline {
  list-style: none;
  position: relative;
  display: flex;
  flex-direction: column;
  gap: var(--space-4);
}

.timeline::before {
  content: '';
  position: absolute;
  top: 12px;
  bottom: 12px;
  left: 13px;
  width: 1px;
  background: var(--border-default);
}

.event {
  position: relative;
  display: grid;
  grid-template-columns: 27px minmax(0, 1fr);
  gap: var(--space-3);
  align-items: start;
}

.event-node {
  position: relative;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 27px;
  height: 27px;
  border-radius: 50%;
  color: var(--text-secondary);
  background: var(--bg-neutral);
  box-shadow: 0 0 0 4px var(--bg-base);
}

.event-node::after {
  content: '';
  position: absolute;
  inset: 0;
  border-radius: 50%;
  background: var(--bg-base);
  z-index: -1;
}

.event-small {
  align-items: center;
}

.event-line {
  font-size: 0.8rem;
  color: var(--text-secondary);
}

.review-card {
  border-left-width: 2px;
}

.edge-success {
  border-left-color: var(--success);
}

.edge-attention {
  border-left-color: var(--attention);
}

.edge-review {
  border-left-color: var(--accent-purple);
}

.edge-active {
  border-left-color: var(--accent-blue);
}

.edge-error {
  border-left-color: var(--error);
}

.thread-list {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
  margin-top: var(--space-3);
}

.thread-list:first-child {
  margin-top: 0;
}

.thread {
  border: 0.5px solid var(--border-default);
  border-radius: var(--radius-md);
  overflow: hidden;
}

.thread-location {
  display: flex;
  align-items: center;
  gap: 6px;
  width: 100%;
  padding: 5px var(--space-3);
  background: var(--bg-raised);
  color: var(--text-secondary);
  text-align: left;
  transition: all var(--transition-fast);
}

.thread-location .mono {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.thread-location:hover {
  color: var(--text-primary);
  background: color-mix(in srgb, var(--accent-blue) 10%, var(--bg-raised));
}

.thread-go {
  margin-left: auto;
  opacity: 0;
  transition: opacity var(--transition-fast);
}

.thread-location:hover .thread-go {
  opacity: 1;
}

.thread-body {
  padding: var(--space-2) var(--space-3);
  font-size: 0.82rem;
}

.line-chip {
  padding: 0 5px;
  border-radius: var(--radius-sm);
  background: color-mix(in srgb, var(--accent-blue) 14%, transparent);
  color: var(--accent-blue);
  font-family: var(--font-mono);
  font-size: 0.68rem;
  flex-shrink: 0;
}

.line-chip-button:hover {
  background: color-mix(in srgb, var(--accent-blue) 26%, transparent);
}

.watch-link {
  margin-top: var(--space-3);
}

.timeline-empty {
  padding-left: calc(27px + var(--space-3));
}

/* Sidebar */
.side-card {
  padding: var(--space-3) var(--space-4);
}

.side-title {
  display: flex;
  align-items: center;
  gap: 6px;
  font-size: 0.72rem;
  font-weight: 600;
  letter-spacing: 0.04em;
  text-transform: uppercase;
  color: var(--text-muted);
  margin-bottom: var(--space-3);
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
  gap: 6px;
  min-width: 0;
  color: var(--text-primary);
}

.ellipsis {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.warn {
  color: var(--warning);
}

.text-button {
  color: var(--accent-blue);
  font-size: inherit;
}

.text-button:hover {
  text-decoration: underline;
}

.task-link {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  color: var(--accent-blue);
}

.reviewer {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  font-size: 0.8rem;
}

.reviewer + .reviewer {
  margin-top: var(--space-2);
}

.reviewer-name {
  flex: 1;
}

.reviewer-none {
  font-size: 0.72rem;
}

.side-hint {
  margin: 0 0 var(--space-3);
}

.side-picker {
  width: 100%;
  margin-bottom: var(--space-3);
}

.side-check {
  margin-bottom: var(--space-3);
}

.side-button {
  width: 100%;
}

/* Changes */
.empty-card {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: var(--space-2);
  padding: var(--space-8) var(--space-4);
  color: var(--text-muted);
  font-size: 0.82rem;
}

.changes {
  display: grid;
  grid-template-columns: minmax(200px, 270px) minmax(0, 1fr);
  gap: var(--space-4);
  align-items: start;
}

@media (max-width: 800px) {
  .changes {
    grid-template-columns: minmax(0, 1fr);
  }
}

.file-tree {
  display: flex;
  flex-direction: column;
  max-height: calc(100vh - 140px);
  position: sticky;
  top: var(--space-4);
  overflow: hidden;
}

@media (max-width: 800px) {
  .file-tree {
    position: static;
    max-height: 40vh;
  }
}

.tree-search {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  padding: var(--space-2) var(--space-3);
  border-bottom: 0.5px solid var(--border-hairline);
  color: var(--text-muted);
}

.tree-search input {
  flex: 1;
  min-width: 0;
  background: none;
  border: none;
  color: var(--text-primary);
  font-family: inherit;
  font-size: 0.78rem;
}

.tree-search input:focus {
  outline: none;
}

.tree-search:focus-within {
  color: var(--accent-blue);
}

.tree-body {
  overflow-y: auto;
  padding: var(--space-1);
}

.tree-group + .tree-group {
  margin-top: var(--space-1);
}

.tree-dir {
  display: flex;
  align-items: center;
  gap: 6px;
  padding: var(--space-1) var(--space-2);
  font-family: var(--font-mono);
  font-size: 0.68rem;
  color: var(--text-muted);
}

.ellipsis-start {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  direction: rtl;
  text-align: left;
}

.tree-file {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  width: 100%;
  padding: 3px var(--space-2);
  border-radius: var(--radius-sm);
  font-size: 0.76rem;
  color: var(--text-secondary);
  text-align: left;
  transition: background var(--transition-fast);
}

.tree-file.nested {
  padding-left: calc(var(--space-2) + 14px);
}

.tree-file:hover {
  background: var(--bg-hover);
  color: var(--text-primary);
}

.tree-file.active {
  background: var(--bg-selected);
  color: var(--text-primary);
  box-shadow: inset 2px 0 0 var(--accent);
}

.file-status {
  font-family: var(--font-mono);
  font-size: 0.68rem;
  font-weight: 600;
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

.file-name {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.file-comments {
  display: inline-flex;
  align-items: center;
  gap: 3px;
  padding: 0 5px;
  border-radius: 999px;
  background: color-mix(in srgb, var(--accent-blue) 16%, transparent);
  color: var(--accent-blue);
  font-size: 0.66rem;
}

.file-stats {
  display: flex;
  gap: var(--space-1);
  font-family: var(--font-mono);
  font-size: 0.66rem;
  flex-shrink: 0;
}

.tree-empty {
  text-align: center;
}

.file-view {
  display: flex;
  flex-direction: column;
  gap: var(--space-3);
  min-width: 0;
}

.file-bar {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: var(--space-2) var(--space-3);
  padding: var(--space-2) var(--space-3);
  background: var(--bg-surface);
  border: 0.5px solid var(--border-default);
  border-radius: var(--radius-lg);
}

.file-status-pill {
  padding: 0 6px;
  border-radius: var(--radius-sm);
  background: var(--bg-neutral);
  font-size: 0.68rem;
  font-weight: 500;
  text-transform: capitalize;
}

.file-bar-path {
  flex: 1;
  min-width: 0;
  font-family: var(--font-mono);
  font-size: 0.78rem;
  overflow-wrap: anywhere;
}

.file-bar-path strong {
  font-weight: 500;
  color: var(--text-primary);
}

.file-bar-stats {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  font-family: var(--font-mono);
  font-size: 0.72rem;
}

.file-bar-end {
  display: flex;
  align-items: center;
  gap: var(--space-2);
}

.pager {
  display: inline-flex;
  align-items: center;
  gap: 2px;
}

.pager-count {
  min-width: 44px;
  text-align: center;
  font-size: 0.72rem;
  color: var(--text-muted);
  font-variant-numeric: tabular-nums;
}

.diff-frame {
  position: relative;
  height: 65vh;
  min-height: 360px;
  border: 0.5px solid var(--border-default);
  border-radius: var(--radius-lg);
  overflow: hidden;
  transition: opacity var(--transition-fast);
}

.diff-frame.loading {
  opacity: 0.6;
}

.diff-loading {
  position: absolute;
  top: var(--space-3);
  right: var(--space-3);
}

.file-comments-section {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
}

.file-comments-header {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  flex-wrap: wrap;
  gap: var(--space-2);
}

.file-comments-header .form-hint {
  margin: 0;
}

.comment {
  padding: var(--space-2) var(--space-3);
  background: var(--bg-surface);
  border: 0.5px solid var(--border-default);
  border-radius: var(--radius-md);
  font-size: 0.82rem;
}

.comment.outdated {
  opacity: 0.75;
}

.comment.draft {
  border-style: dashed;
  border-color: color-mix(in srgb, var(--accent-blue) 45%, transparent);
}

.comment-meta {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  font-size: 0.74rem;
  margin-bottom: var(--space-1);
  color: var(--text-secondary);
}

.comment-author {
  font-weight: 500;
  color: var(--text-primary);
}

.comment-time,
.comment-remove {
  margin-left: auto;
}

.draft-body {
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}

.draft-form {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
  padding: var(--space-3);
  background: var(--bg-surface);
  border: 0.5px solid var(--border-default);
  border-radius: var(--radius-md);
}

.draft-form-row {
  display: flex;
  align-items: flex-start;
  gap: var(--space-2);
}

.draft-line-label {
  display: flex;
  flex-direction: column;
  gap: 2px;
  font-size: 0.68rem;
  color: var(--text-muted);
}

.draft-line {
  width: 76px;
}

.draft-text {
  flex: 1;
  margin-top: calc(0.68rem * 1.5 + 2px);
}

.draft-form-footer {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-2);
}

.draft-form-footer .form-hint,
.draft-error {
  margin: 0;
}

/* Shared bits */
.spinner {
  display: inline-block;
  width: 16px;
  height: 16px;
  border: 2px solid var(--spinner-track);
  border-top-color: var(--accent-blue);
  border-radius: 50%;
  animation: spin 0.8s linear infinite;
  flex-shrink: 0;
}

.spinner-sm {
  width: 12px;
  height: 12px;
  border-width: 1.5px;
}

.fade-enter-active,
.fade-leave-active {
  transition: opacity var(--transition-normal);
}

.fade-enter-from,
.fade-leave-to {
  opacity: 0;
}

.pop-enter-active,
.pop-leave-active {
  transition: opacity var(--transition-fast), transform var(--transition-fast);
}

.pop-enter-from,
.pop-leave-to {
  opacity: 0;
  transform: translateY(-4px);
}

@media (max-width: 640px) {
  .tabbar {
    flex-wrap: wrap;
  }

  .diffstat {
    display: none;
  }
}
</style>
