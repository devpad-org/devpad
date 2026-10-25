import { computed, inject, ref, watch, type InjectionKey, type Ref } from 'vue'
import { projectApi, type RepoDiff, type RepoDiffFile } from '@/api/projects'
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
import type { MrIconName } from '@/components/project/MrIcon.vue'
import { useActiveProjectStore } from '@/stores/activeProject'
import { STRATEGIES, authorName, dirName, errorText, plural, type Author, type Tone } from './format'

const NOTICE_MS = 6000

export type Section = 'overview' | 'changes'

export interface Check {
  key: string
  tone: Tone
  icon: MrIconName
  title: string
  detail?: string
  spin?: boolean
}

// One merge request's state and actions, shared by the components that make
// up its tab. The tab creates it and provides it to its children.
export function createMergeRequestState(projectId: Ref<number>, mrId: Ref<number>) {
  const projectStore = useActiveProjectStore()

  const mr = ref<MergeRequestDetail | null>(null)
  const diff = ref<RepoDiff | null>(null)
  const loadError = ref('')
  const actionError = ref('')
  const notice = ref('')
  const busy = ref(false)
  const section = ref<Section>('overview')

  const selectedPath = ref('')
  const fileFilter = ref('')
  const oldContent = ref('')
  const newContent = ref('')
  const fileLoading = ref(false)
  const fileError = ref('')
  // Decided from the tab's width the first time the changes are shown.
  const sideBySide = ref<boolean | null>(null)
  const reveal = ref<{ line: number } | null>(null)
  const fileCache = new Map<string, string | null>()

  const reviewOpen = ref(false)
  const reviewError = ref('')
  const verdict = ref<Verdict>('comment')
  const reviewBody = ref('')
  const drafts = ref<CommentInput[]>([])
  const draftLine = ref<number | null>(null)
  const draftBody = ref('')
  const draftError = ref('')
  // Bumped to ask the line comment form to take focus.
  const draftFocus = ref(0)
  const sendToAgent = ref(true)

  const agentModel = ref('')
  const agentEffort = ref('')
  const autoFix = ref(true)

  const strategy = ref<MergeStrategy>('merge')
  const pushAfterMerge = ref(true)
  const conflicts = ref<string[] | null>(null)
  const mergeError = ref('')

  let noticeTimer: ReturnType<typeof setTimeout> | null = null

  const project = computed(() => projectStore.project)
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

  // Unsubmitted review text or line comments, which closing the tab would lose.
  // Only an open merge request can still be reviewed.
  const hasDraft = computed(
    () => isOpen.value && (reviewBody.value.trim() !== '' || drafts.value.length > 0 || draftBody.value.trim() !== ''),
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

  function commentCount(path: string): number {
    return (commentsByPath.value.get(path)?.length ?? 0) + drafts.value.filter((d) => d.path === path).length
  }

  function reviewComments(review: Review): ReviewComment[] {
    return (mr.value?.comments ?? []).filter((c) => c.reviewId === review.id).sort((a, b) => a.path.localeCompare(b.path) || a.line - b.line)
  }

  function showNotice(text: string) {
    notice.value = text
    if (noticeTimer !== null) clearTimeout(noticeTimer)
    noticeTimer = setTimeout(() => (notice.value = ''), NOTICE_MS)
  }

  // Bumped whenever the tab switches to another merge request, so responses
  // for the previous one are dropped.
  let generation = 0

  async function loadDetail(gen: number) {
    const res = await mergeRequestApi.get(projectId.value, mrId.value)
    if (gen !== generation) return
    const headChanged = mr.value?.headSha !== res.mergeRequest.headSha || mr.value?.status !== res.mergeRequest.status
    mr.value = res.mergeRequest
    if (headChanged || !diff.value) await loadDiff(gen)
  }

  async function loadDiff(gen: number) {
    const res = await mergeRequestApi.diff(projectId.value, mrId.value)
    if (gen !== generation) return
    diff.value = res
    if (!res.files.some((f) => f.path === selectedPath.value)) {
      selectedPath.value = orderedFiles.value[0]?.path ?? ''
    } else {
      void loadFile()
    }
  }

  async function refresh() {
    const gen = generation
    try {
      await loadDetail(gen)
      if (gen === generation) loadError.value = ''
    } catch (e) {
      if (gen === generation) loadError.value = errorText(e, 'Failed to load the merge request')
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
    section.value = 'changes'
    selectFile(path, line)
  }

  function startComment(line: number) {
    if (!isOpen.value) return
    draftLine.value = line
    draftError.value = ''
    draftFocus.value++
  }

  watch(selectedPath, () => {
    draftLine.value = null
    draftBody.value = ''
    draftError.value = ''
    void loadFile()
  })

  // A merged or closed merge request can't take a review, so its drafts go.
  watch(
    () => mr.value?.status,
    (status) => {
      if (!status || status === 'open') return
      reviewOpen.value = false
      reviewBody.value = ''
      drafts.value = []
      draftLine.value = null
      draftBody.value = ''
      draftError.value = ''
    },
  )

  watch([projectId, mrId], () => {
    generation++
    mr.value = null
    diff.value = null
    fileCache.clear()
    drafts.value = []
    reviewBody.value = ''
    conflicts.value = null
    reviewOpen.value = false
    void refresh()
  })

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

  // Refreshes this merge request and the project's lists, whose badges and
  // board reflect what just happened.
  async function refreshAll() {
    void projectStore.refresh()
    await refresh()
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
      await refreshAll()
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
      await refreshAll()
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
        await refreshAll()
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
      await refreshAll()
    }, mergeError)
  }

  function close() {
    void act(async () => {
      await mergeRequestApi.close(projectId.value, mrId.value)
      await refreshAll()
    })
  }

  function dispose() {
    generation++
    if (noticeTimer !== null) clearTimeout(noticeTimer)
  }

  return {
    projectId,
    mrId,
    project,
    mr,
    diff,
    loadError,
    actionError,
    notice,
    busy,
    section,
    selectedPath,
    fileFilter,
    oldContent,
    newContent,
    fileLoading,
    fileError,
    sideBySide,
    reveal,
    reviewOpen,
    reviewError,
    verdict,
    reviewBody,
    drafts,
    draftLine,
    draftBody,
    draftError,
    draftFocus,
    sendToAgent,
    agentModel,
    agentEffort,
    autoFix,
    strategy,
    pushAfterMerge,
    conflicts,
    mergeError,
    isOpen,
    files,
    pendingAgentReview,
    timelineReviews,
    totals,
    hasDraft,
    orderedFiles,
    fileGroups,
    selectedIndex,
    selectedFile,
    selectedComments,
    selectedDrafts,
    markedLines,
    latestReviews,
    checks,
    strategyHint,
    statusIcon,
    commentCount,
    reviewComments,
    refresh,
    selectFile,
    stepFile,
    jumpToComment,
    startComment,
    addDraft,
    removeDraft,
    submitReview,
    startAgentReview,
    merge,
    resolveConflicts,
    close,
    dispose,
  }
}

export type MergeRequestState = ReturnType<typeof createMergeRequestState>

export const mergeRequestKey: InjectionKey<MergeRequestState> = Symbol('mergeRequest')

// The state of the merge request whose tab this component sits in.
export function useMergeRequest(): MergeRequestState {
  const state = inject(mergeRequestKey)
  if (!state) throw new Error('useMergeRequest needs a MergeRequestTab ancestor')
  return state
}
