import { computed, inject, ref, watch, type InjectionKey, type Ref } from 'vue'
import { projectApi, type RepoDiffFile, type WorkspaceBranchStatus } from '@/api/projects'
import {
  gateConfirmation,
  mergeConflictsFromError,
  mergeRequestApi,
  pipelineGateFromError,
  type Commit,
  type CommentInput,
  type CommitLog,
  type LookClosely,
  type MergeRequestChange,
  type MergeRequestDetail,
  type MergeStrategy,
  type GateConfirmation,
  type NewComment,
  type PipelineGateRefusal,
  type Push,
  type Review,
  type ReviewComment,
  type Verdict,
} from '@/api/mergeRequests'
import type { MrIconName } from '@/components/project/MrIcon.vue'
import type { SpentBudget } from '@/components/project/BudgetReached.vue'
import { taskApi } from '@/api/tasks'
import { branchActionError } from '@/components/project/branchErrors'
import { useActiveProjectStore } from '@/stores/activeProject'
import { useAiModelStore } from '@/stores/aiModels'
import type { Pipeline } from '@/api/pipelines'
import { PIPELINE_COPY } from '@/components/pipeline/pipelineCopy'
import { STRATEGIES, authorName, autoMergeReason, dirName, errorText, plural, type Author, type Tone } from './format'
import { createMrPipelines } from './useMrPipelines'

const NOTICE_MS = 6000
// Comparing the workspace's branch reads both repos, so polling does it at
// most this often.
const WORKSPACE_CHECK_MS = 15000

export type Section = 'overview' | 'changes'

// A comment that starts a thread, on a line or on the whole change, and the
// replies to it, oldest first. The first comment carries the resolution.
export interface Thread {
  root: ReviewComment
  replies: ReviewComment[]
}

// A move of the source branch and the commits it added that are still on
// the branch, newest first.
export interface PushEvent {
  push: Push
  commits: Commit[]
}

// An entry in the activity timeline: a review's verdict, a thread, a push,
// or a pipeline.
export type TimelineItem =
  | { kind: 'review'; at: string; review: Review }
  | { kind: 'thread'; at: string; thread: Thread }
  | { kind: 'push'; at: string; event: PushEvent }
  | { kind: 'pipeline'; at: string; pipeline: Pipeline }

// What the activity shows: everything, or only the threads still open.
export type ActivityFilter = 'all' | 'open'

// An agent a spent budget stopped, with the budgets to raise to let it go
// on: the latest agent review, stopped before it submitted, or the task's
// agent.
export interface BudgetStop {
  budgets: SpentBudget[]
  action: string
  hint?: string
}

export interface Check {
  key: string
  tone: Tone
  icon: MrIconName
  title: string
  detail?: string
  spin?: boolean
}

// One merge request's state and actions, shared by the components that make
// up its modal. The modal creates it and provides it to its children.
export function createMergeRequestState(projectId: Ref<number>, mrId: Ref<number>) {
  const projectStore = useActiveProjectStore()
  const aiModelStore = useAiModelStore()

  const mr = ref<MergeRequestDetail | null>(null)
  const diff = ref<MergeRequestChange | null>(null)
  // The files of the change devpad marks for a closer look, which come with
  // its diff.
  const lookClosely = computed<LookClosely | null>(() => diff.value?.lookClosely ?? null)
  // The commits the merge request proposes, or null until they load or when
  // the repo can't list them.
  const commitLog = ref<CommitLog | null>(null)
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
  // Decided from the modal's width the first time the changes are shown.
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

  // Comments being written outside a review: one on the whole change, and a
  // reply for each thread, by its first comment's id.
  const newComment = ref('')
  const replyDrafts = ref<Record<number, string>>({})
  const activityFilter = ref<ActivityFilter>('all')

  const agentModel = ref('')
  const agentEffort = ref('')
  const autoFix = ref(true)

  // Why continuing a stopped review failed, shown with the budget it needs.
  const continueError = ref('')

  const sendBackOpen = ref(false)
  const sendBackNote = ref('')
  const sendBackError = ref('')

  // The project workspace's copy of the source branch against the project's,
  // or null when the workspace can't say, such as when it is stopped or has
  // no such branch.
  const workspaceBranch = ref<WorkspaceBranchStatus | null>(null)
  let workspaceCheckedAt = 0

  const strategy = ref<MergeStrategy>('merge')
  const pushAfterMerge = ref(true)
  // The files a merge the repo refused found conflicting.
  const mergeConflicts = ref<string[] | null>(null)
  const mergeError = ref('')
  // The pipeline gate's state you are asked to confirm merging over, with
  // why it is shut; null while not asking.
  const overrideAsk = ref<PipelineGateRefusal | null>(null)

  let noticeTimer: ReturnType<typeof setTimeout> | null = null

  const pipelines = createMrPipelines(projectId, mrId, mr, () => void projectStore.refreshLatestPipelines())
  // The files that keep the branch from merging onto its target: those a
  // refused merge found, or else those the pipeline's trial merge found,
  // which a merge would find too.
  const conflicts = computed(() => mergeConflicts.value ?? pipelines.gateConflicts.value)
  // The pipeline found the branch doesn't merge onto its target as it is
  // now, so merging waits for the conflicts to be resolved.
  const gateConflicted = computed(() => pipelines.gate.value?.state === 'conflict')

  const project = computed(() => projectStore.project)
  const isOpen = computed(() => mr.value?.status === 'open')
  const files = computed(() => diff.value?.files ?? [])
  const pendingAgentReview = computed(() => mr.value?.reviews.some((r) => r.author === 'agent' && r.status === 'pending') ?? false)
  const timelineReviews = computed(() => [...(mr.value?.reviews ?? [])].sort((a, b) => a.id - b.id))

  // Every thread, oldest first.
  const threads = computed<Thread[]>(() => {
    const comments = [...(mr.value?.comments ?? [])].sort((a, b) => a.id - b.id)
    const byRoot = new Map<number, Thread>()
    for (const c of comments) {
      if (!c.replyTo) byRoot.set(c.id, { root: c, replies: [] })
    }
    for (const c of comments) {
      if (c.replyTo) byRoot.get(c.replyTo)?.replies.push(c)
    }
    return [...byRoot.values()]
  })
  const openThreads = computed(() => threads.value.filter((t) => !t.root.resolvedBy))

  // Which push added each commit still on the branch: each push takes the
  // commits its head reaches that its old head does not, newest push first
  // so a commit pushed again after a rewrite sits with its latest push. What
  // no push added, the merge request was opened with. Commit dates are left
  // out of it, since whoever made a commit chose its date.
  const commitGroups = computed(() => {
    const commits = commitLog.value?.commits ?? []
    const bySha = new Map(commits.map((c) => [c.sha, c]))
    const reach = (sha: string) => {
      const seen = new Set<string>()
      const stack = [sha]
      while (stack.length) {
        const c = bySha.get(stack.pop() as string)
        if (!c || seen.has(c.sha)) continue
        seen.add(c.sha)
        stack.push(...c.parents)
      }
      return seen
    }
    const assigned = new Set<string>()
    const byPush = new Map<number, Commit[]>()
    for (const push of [...(mr.value?.pushes ?? [])].reverse()) {
      const before = reach(push.beforeSha)
      const added = new Set([...reach(push.headSha)].filter((sha) => !before.has(sha) && !assigned.has(sha)))
      for (const sha of added) assigned.add(sha)
      byPush.set(push.id, commits.filter((c) => added.has(c.sha)))
    }
    return { opened: commits.filter((c) => !assigned.has(c.sha)), byPush }
  })
  const openedCommits = computed(() => commitGroups.value.opened)

  // A review's verdict sits where it was started, and each thread where it
  // was started, so a review agent's threads follow its verdict card. A
  // push sits where devpad saw the branch move.
  const timeline = computed<TimelineItem[]>(() => {
    const items: TimelineItem[] = timelineReviews.value.map((review) => ({ kind: 'review', at: review.createdAt, review }))
    for (const thread of threads.value) items.push({ kind: 'thread', at: thread.root.createdAt, thread })
    for (const push of mr.value?.pushes ?? []) {
      items.push({ kind: 'push', at: push.createdAt, event: { push, commits: commitGroups.value.byPush.get(push.id) ?? [] } })
    }
    for (const pipeline of pipelines.timelinePipelines.value) items.push({ kind: 'pipeline', at: pipeline.createdAt, pipeline })
    return items.sort((a, b) => Date.parse(a.at) - Date.parse(b.at))
  })

  const task = computed(() => mr.value?.task ?? null)
  const taskWorking = computed(() => task.value?.status === 'queued' || task.value?.status === 'in_progress')
  // The task's agent can be sent back while the change waits for review, or
  // after its last round failed.
  const canSendBack = computed(
    () => isOpen.value && ['in_review', 'changes_requested', 'failed'].includes(task.value?.status ?? ''),
  )
  // The reviews of the current head sent back with the note: all but approvals.
  const sendBackReviews = computed(() =>
    timelineReviews.value.filter((r) => r.status === 'submitted' && r.verdict !== 'approve' && r.headSha === mr.value?.headSha),
  )

  // A protected source, such as develop released into main, is never
  // squashed or published from the workspace: it changes through merges.
  const sourceProtected = computed(() => !!mr.value && projectStore.isProtected(mr.value.sourceBranch))
  const strategies = computed(() => (sourceProtected.value ? STRATEGIES.filter((s) => s.value !== 'squash') : STRATEGIES))
  watch(strategies, (list) => {
    if (!list.some((s) => s.value === strategy.value)) strategy.value = 'merge'
  })

  // Commits on the source branch in the project workspace that this merge
  // request does not have yet. Neither a sandbox's branch nor a protected
  // branch can be published from the workspace, so they never have any.
  const unpublishedCommits = computed(() => {
    const status = workspaceBranch.value
    if (!isOpen.value || !status || status.sandboxOwned || sourceProtected.value || status.branch !== mr.value?.sourceBranch) return 0
    return status.ahead
  })
  // Publishing only fast-forwards, so a workspace branch that also lacks
  // some of the project's commits must merge them first.
  const workspaceDiverged = computed(() => unpublishedCommits.value > 0 && (workspaceBranch.value?.behind ?? 0) > 0)

  // The task's budget, when its agent and the reviews of this merge request
  // have spent it.
  const spentTaskBudget = computed<SpentBudget | null>(() => {
    const t = task.value
    if (!t || t.budgetUsd <= 0 || t.costUsd < t.budgetUsd) return null
    return { key: 'task', owner: `Task #${t.id}`, label: `Task #${t.id} budget`, spentUsd: t.costUsd, budgetUsd: t.budgetUsd }
  })
  const taskBudgetHint = 'The task\'s agent and the reviews of its merge request share its budget.'

  // The latest agent review, when it stopped before submitting because a
  // budget it spends from ran out. Only a review of the change as it stands
  // can go on.
  const stoppedReview = computed(() => {
    if (!isOpen.value || pendingAgentReview.value) return null
    const review = [...timelineReviews.value].reverse().find((r) => r.author === 'agent')
    if (!review || review.status !== 'failed' || review.headSha !== mr.value?.headSha) return null
    const budgets: SpentBudget[] = []
    if (spentTaskBudget.value) budgets.push(spentTaskBudget.value)
    if (review.budgetUsd > 0 && review.costUsd >= review.budgetUsd) {
      budgets.push({ key: 'review', owner: 'This review', label: 'Review budget', spentUsd: review.costUsd, budgetUsd: review.budgetUsd })
    }
    if (!budgets.length) return null
    const stop: BudgetStop = {
      budgets,
      // A review that never ran starts afresh; one that ran picks up its
      // transcript.
      action: review.runId ? 'Raise and continue the review' : 'Raise and start the review',
      hint: spentTaskBudget.value ? taskBudgetHint : undefined,
    }
    return { review, stop }
  })

  // The task's agent, when it stopped, or its feedback waits, because the
  // task's budget is spent.
  const taskBudgetStop = computed<BudgetStop | null>(() => {
    const t = task.value
    if (!isOpen.value || stoppedReview.value || !spentTaskBudget.value || !t) return null
    if (t.status !== 'failed' && t.status !== 'changes_requested') return null
    return { budgets: [spentTaskBudget.value], action: "Raise and continue the task's agent", hint: taskBudgetHint }
  })

  const latestAgentReview = computed(() => [...timelineReviews.value].reverse().find((r) => r.author === 'agent' && r.status !== 'pending' && r.model) ?? null)
  // The model to name for a re-review, empty when the last review's can't be
  // used any more and the server will pick another.
  const rereviewModel = computed(() => {
    const model = latestAgentReview.value?.model ?? ''
    return model && aiModelStore.isModelAvailable(model) ? model : ''
  })
  const totals = computed(() =>
    files.value.reduce((sum, f) => ({ additions: sum.additions + f.additions, deletions: sum.deletions + f.deletions }), {
      additions: 0,
      deletions: 0,
    }),
  )

  // Unsubmitted review text, comments or replies, which closing the modal
  // would lose. Only an open merge request can still be commented on.
  const hasDraft = computed(
    () =>
      isOpen.value &&
      (reviewBody.value.trim() !== '' ||
        drafts.value.length > 0 ||
        draftBody.value.trim() !== '' ||
        sendBackNote.value.trim() !== '' ||
        newComment.value.trim() !== '' ||
        Object.values(replyDrafts.value).some((d) => d.trim() !== '')),
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

  // Each file's line threads, in line order.
  const threadsByPath = computed(() => {
    const byPath = new Map<string, Thread[]>()
    for (const t of threads.value) {
      if (!t.root.path) continue
      const list = byPath.get(t.root.path) ?? []
      list.push(t)
      byPath.set(t.root.path, list)
    }
    for (const list of byPath.values()) list.sort((a, b) => a.root.line - b.root.line || a.root.id - b.root.id)
    return byPath
  })

  const selectedThreads = computed(() => threadsByPath.value.get(selectedPath.value) ?? [])
  const selectedDrafts = computed(() => drafts.value.filter((d) => d.path === selectedPath.value))
  // Lines of the current version with an open thread or a pending comment.
  // A thread on an older version may point at a line that has moved since.
  const markedLines = computed(() => [
    ...selectedThreads.value.filter((t) => !t.root.resolvedBy && t.root.headSha === mr.value?.headSha).map((t) => t.root.line),
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
    const open = openThreads.value.length
    if (open > 0) {
      list.push({
        key: 'threads',
        tone: 'warning',
        icon: 'comment',
        title: plural(open, 'open thread'),
        detail: 'Resolve the threads that are addressed, or have a review agent check them.',
      })
    } else if (threads.value.length > 0) {
      list.push({ key: 'threads', tone: 'success', icon: 'check-circle', title: 'All threads resolved' })
    }
    if (pendingAgentReview.value) {
      list.push({ key: 'agent', tone: 'active', icon: 'bot', title: 'Agent review in progress', detail: 'An agent is reading the change.', spin: true })
    }
    if (taskWorking.value) {
      list.push({
        key: 'task',
        tone: 'active',
        icon: 'task',
        title: task.value?.status === 'queued' ? 'Task agent queued' : 'Task agent at work',
        detail: 'The task agent is working on the feedback; its commits update this merge request when it finishes.',
        spin: true,
      })
    } else if (task.value?.status === 'failed') {
      list.push({ key: 'task', tone: 'error', icon: 'warning', title: 'The task agent stopped', detail: task.value.error || undefined })
    }
    const pipeline = pipelines.check.value
    if (pipeline) list.push(pipeline)
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
    const auto = autoMergeCheck.value
    if (auto) list.push(auto)
    return list
  })

  // A task's merge request into a branch the project merges into
  // automatically merges once a review agent approves it and its run ends,
  // and once its pipeline passes when one guards the branch. The check says
  // so, that the latest approval is about to merge it or waits for its
  // pipeline, or that it did not and why. Without a pipeline guarding the
  // branch the approval is all that stands in the way, and the check warns.
  const autoMerges = computed(() => {
    const m = mr.value
    return !!m && m.openedBy === 'task_agent' && (project.value?.autoMergeBranches ?? []).includes(m.targetBranch)
  })
  const autoMergeCheck = computed<Check | null>(() => {
    const m = mr.value
    if (!m || !autoMerges.value) return null
    const latest = timelineReviews.value[timelineReviews.value.length - 1]
    const notMerged = (why: string): Check => ({
      key: 'auto-merge',
      tone: 'warning',
      icon: 'warning',
      title: 'Not merged automatically',
      detail: `The review agent's approval didn't merge it: ${why}. Merge it yourself, or have a review agent review it again.`,
    })
    if (latest?.autoMergeError) return notMerged(autoMergeReason(latest))
    // A change to the pipeline file is the user's to merge, approved or not,
    // as the server says of the change.
    const pipelineFile = diff.value?.pipelineFile
    if (pipelineFile) {
      return { key: 'auto-merge', tone: 'warning', icon: 'warning', title: "Won't merge automatically", detail: PIPELINE_COPY.autoMergePipelineFile(pipelineFile) }
    }
    if ((diff.value?.mergeBases ?? 1) > 1) {
      return {
        key: 'auto-merge',
        tone: 'warning',
        icon: 'warning',
        title: "Won't merge automatically",
        detail: `It has more than one merge base with ${m.targetBranch}, so its review couldn't read just what merging it does: merge it yourself once you've looked at its changes.`,
      }
    }
    const gate = pipelines.gate.value
    if (latest?.author === 'agent' && latest.status === 'submitted' && latest.verdict === 'approve') {
      if (latest.finishing) {
        const waiting = !!gate?.guarded && !gate.clear && gate.waits
        return {
          key: 'auto-merge',
          tone: 'active',
          icon: 'merged',
          title: waiting ? `Merging into ${m.targetBranch} once the pipeline passes` : `Merging into ${m.targetBranch} automatically`,
          detail: waiting ? PIPELINE_COPY.autoMergeWaiting(m.targetBranch) : 'The review agent approved it; devpad merges it once the agent finishes.',
          spin: true,
        }
      }
      // Devpad settled its run without a merge or a reason: the target was
      // not merged into automatically then.
      return notMerged(`it came before ${m.targetBranch} was merged into automatically`)
    }
    const reviewer = project.value?.autoReview ? 'the automatic review' : 'a review agent you start'
    // Until the server says how the gate stands, nothing claims a pipeline
    // guards the branch.
    const guarded = !!gate?.guarded && gate.state !== 'closed'
    return {
      key: 'auto-merge',
      tone: guarded ? 'neutral' : 'warning',
      icon: guarded ? 'merged' : 'warning',
      title: `Merges into ${m.targetBranch} automatically`,
      detail: guarded ? PIPELINE_COPY.autoMergeGuarded(reviewer, m.targetBranch) : PIPELINE_COPY.autoMergeUnguarded(reviewer, m.targetBranch),
    }
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

  // A file's open threads and pending comments.
  function commentCount(path: string): number {
    const open = threadsByPath.value.get(path)?.filter((t) => !t.root.resolvedBy).length ?? 0
    return open + drafts.value.filter((d) => d.path === path).length
  }

  // How many comments a review left, its replies in others' threads included.
  function reviewCommentCount(review: Review): number {
    return (mr.value?.comments ?? []).filter((c) => c.reviewId === review.id).length
  }

  function showNotice(text: string) {
    notice.value = text
    if (noticeTimer !== null) clearTimeout(noticeTimer)
    noticeTimer = setTimeout(() => (notice.value = ''), NOTICE_MS)
  }

  // Bumped whenever the modal switches to another merge request, so responses
  // for the previous one are dropped.
  let generation = 0

  async function loadDetail(gen: number) {
    const res = await mergeRequestApi.get(projectId.value, mrId.value)
    if (gen !== generation) return
    const headChanged = mr.value?.headSha !== res.mergeRequest.headSha || mr.value?.status !== res.mergeRequest.status
    mr.value = res.mergeRequest
    if (headChanged || !diff.value) await Promise.all([loadDiff(gen), loadCommits(gen)])
  }

  // The commits only add to the timeline, so it shows without them when
  // the repo can't list them.
  async function loadCommits(gen: number) {
    try {
      const res = await mergeRequestApi.commits(projectId.value, mrId.value)
      if (gen === generation) commitLog.value = res
    } catch {
      if (gen === generation) commitLog.value = null
    }
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
    void pipelines.load()
    try {
      await loadDetail(gen)
      if (gen === generation) loadError.value = ''
    } catch (e) {
      if (gen === generation) loadError.value = errorText(e, 'Failed to load the merge request')
    }
    void loadWorkspaceBranch(gen)
  }

  async function loadWorkspaceBranch(gen: number, force = false) {
    const m = mr.value
    if (!m || m.status !== 'open') {
      workspaceBranch.value = null
      return
    }
    if (!force && Date.now() - workspaceCheckedAt < WORKSPACE_CHECK_MS) return
    workspaceCheckedAt = Date.now()
    try {
      const status = await projectApi.workspaceBranchStatus(projectId.value, m.sourceBranch)
      if (gen === generation) workspaceBranch.value = status
    } catch {
      if (gen === generation) workspaceBranch.value = null
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
        file.status === 'A' ? Promise.resolve('') : fileAt(d.from, file.oldPath || file.path),
        file.status === 'D' ? Promise.resolve('') : fileAt(d.to, file.path),
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
      sendBackOpen.value = false
      reviewBody.value = ''
      drafts.value = []
      draftLine.value = null
      draftBody.value = ''
      draftError.value = ''
      newComment.value = ''
      replyDrafts.value = {}
    },
  )

  watch([projectId, mrId], () => {
    generation++
    pipelines.reset()
    mr.value = null
    diff.value = null
    commitLog.value = null
    workspaceBranch.value = null
    workspaceCheckedAt = 0
    fileCache.clear()
    drafts.value = []
    reviewBody.value = ''
    mergeConflicts.value = null
    overrideAsk.value = null
    reviewOpen.value = false
    sendBackOpen.value = false
    sendBackNote.value = ''
    newComment.value = ''
    replyDrafts.value = {}
    activityFilter.value = 'all'
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

  // Leaves a comment at once, outside a review. It throws, so the form that
  // sent it can say what went wrong where it is.
  async function postComment(input: NewComment) {
    await mergeRequestApi.comment(projectId.value, mrId.value, input)
    await refresh()
  }

  // Posts the line comment being written on the Changes tab at once,
  // instead of holding it for the review.
  async function commentNow() {
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
    try {
      await postComment({ path: selectedPath.value, line, body: draftBody.value.trim() })
      draftLine.value = null
      draftBody.value = ''
    } catch (e) {
      draftError.value = errorText(e, 'Failed to comment')
    }
  }

  // Replies in a thread, resolving it or opening it again after when
  // toggle is set. The reply stands even if the toggle fails.
  async function reply(thread: Thread, toggle: boolean) {
    const id = thread.root.id
    const body = (replyDrafts.value[id] ?? '').trim()
    if (body) {
      await mergeRequestApi.comment(projectId.value, mrId.value, { replyTo: id, body })
      delete replyDrafts.value[id]
    }
    try {
      if (toggle) await mergeRequestApi.resolveThread(projectId.value, mrId.value, id, !thread.root.resolvedBy)
    } finally {
      await refresh()
    }
  }

  async function setResolved(thread: Thread, resolved: boolean) {
    await mergeRequestApi.resolveThread(projectId.value, mrId.value, thread.root.id, resolved)
    await refresh()
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

  // Refreshes this merge request and the project's lists, whose tables
  // reflect what just happened.
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

  // Starts the review agent that stopped again, with the budgets that
  // stopped it raised.
  function continueReview(raised: Record<string, number>) {
    const review = stoppedReview.value?.review
    if (!review) return
    void act(async () => {
      await mergeRequestApi.continueReview(projectId.value, mrId.value, review.id, {
        budgetUsd: raised.review,
        taskBudgetUsd: raised.task,
      })
      showNotice(review.runId ? 'The review agent is picking up where it stopped.' : 'A review agent is reading the change.')
      await refreshAll()
    }, continueError)
  }

  // Raises the task's budget and starts its agent again, on any feedback
  // waiting for it.
  function continueTask(raised: Record<string, number>) {
    const t = task.value
    if (!t || raised.task === undefined) return
    void act(async () => {
      await taskApi.setBudget(projectId.value, t.id, raised.task)
      await taskApi.start(projectId.value, t.id)
      showNotice("The task's agent is picking up where it stopped.")
      await refreshAll()
    }, continueError)
  }

  // Reviews the change again with the last review agent's settings, or the
  // review agent's defaults when there was none.
  function rereview() {
    void act(async () => {
      await startRereview()
      showNotice('A review agent is reading the change again.')
      await refreshAll()
    })
  }

  async function startRereview() {
    const last = latestAgentReview.value
    await mergeRequestApi.startAgentReview(projectId.value, mrId.value, {
      agentId: last?.agentId || undefined,
      model: last?.model || undefined,
      effort: last?.effort || undefined,
      autoFix: Boolean(last?.autoFix && mr.value?.taskId),
    })
  }

  // Publishes the workspace's commits on the source branch, which moves this
  // merge request to them, and optionally has a review agent look again.
  function publishWorkspaceCommits(thenRereview: boolean) {
    const branch = mr.value?.sourceBranch
    if (!branch) return
    void act(async () => {
      let sha: string
      try {
        sha = (await projectApi.publishBranch(projectId.value, branch)).sha
      } catch (e) {
        throw new Error(branchActionError(e, 'Failed to publish the branch'))
      }
      workspaceBranch.value = null
      // The publish stands even when the review can't start, so the modal
      // catches up with it either way and the error says it went through.
      let reviewError: unknown = null
      if (thenRereview) {
        try {
          await startRereview()
        } catch (e) {
          reviewError = e
        }
      }
      await refreshAll()
      await loadWorkspaceBranch(generation, true)
      if (reviewError) {
        throw new Error(`Published ${sha.slice(0, 8)}, but the review could not start: ${errorText(reviewError, 'request failed')}`)
      }
      showNotice(
        thenRereview
          ? `Published ${sha.slice(0, 8)}. A review agent is reading the change again.`
          : `Published ${sha.slice(0, 8)}. This merge request now shows it.`,
      )
    })
  }

  function sendBack() {
    if (!sendBackNote.value.trim() && sendBackReviews.value.length === 0 && openThreads.value.length === 0) {
      sendBackError.value = 'There is no feedback on this commit yet: tell the agent what to change'
      return
    }
    void act(async () => {
      await mergeRequestApi.sendBack(projectId.value, mrId.value, { message: sendBackNote.value.trim() })
      sendBackNote.value = ''
      sendBackOpen.value = false
      showNotice("Sent back to the task's agent.")
      await refreshAll()
    }, sendBackError)
  }

  // Merges the merge request. While its pipeline keeps it from merging,
  // it asks first (overrideAsk), and merges over the gate only as you saw
  // it, its state, pipeline, head and target, which the server refuses,
  // asking again, once any of them has moved on.
  function merge(override?: GateConfirmation) {
    const gate = pipelines.gate.value
    if (!override && gate && !gate.clear && gate.overridable) {
      overrideAsk.value = { ...gateConfirmation(gate), reason: gate.reason, overridable: true, changed: false }
      return
    }
    void act(async () => {
      mergeConflicts.value = null
      try {
        const res = await mergeRequestApi.merge(projectId.value, mrId.value, {
          strategy: strategy.value,
          push: pushAfterMerge.value && Boolean(project.value?.upstreamUrl),
          overridePipeline: override,
        })
        overrideAsk.value = null
        if (res.pushError) {
          actionError.value = `Merged, but the push failed: ${res.pushError}`
        } else {
          showNotice(res.pushed ? 'Merged and pushed upstream.' : 'Merged.')
        }
        await refreshAll()
      } catch (e) {
        const files = mergeConflictsFromError(e)
        if (files) {
          overrideAsk.value = null
          mergeConflicts.value = files
          return
        }
        const refusal = pipelineGateFromError(e)
        if (refusal) {
          void pipelines.load()
          if (refusal.overridable) {
            overrideAsk.value = { ...refusal, changed: refusal.changed || override !== undefined }
            return
          }
          overrideAsk.value = null
        }
        throw e
      }
    }, mergeError)
  }

  function cancelOverride() {
    overrideAsk.value = null
  }

  function resolveConflicts() {
    const files = conflicts.value ?? []
    void act(async () => {
      await mergeRequestApi.resolveConflicts(projectId.value, mrId.value, files)
      mergeConflicts.value = null
      showNotice(`The task agent is merging ${mr.value?.targetBranch ?? 'the target branch'} in and resolving the conflicts.`)
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
    pipelines.reset()
    if (noticeTimer !== null) clearTimeout(noticeTimer)
  }

  return {
    projectId,
    mrId,
    project,
    mr,
    diff,
    lookClosely,
    commitLog,
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
    newComment,
    replyDrafts,
    activityFilter,
    agentModel,
    agentEffort,
    autoFix,
    sendBackOpen,
    sendBackNote,
    sendBackError,
    strategy,
    pushAfterMerge,
    conflicts,
    mergeError,
    overrideAsk,
    cancelOverride,
    gateConflicted,
    isOpen,
    files,
    pendingAgentReview,
    timelineReviews,
    timeline,
    openedCommits,
    threads,
    openThreads,
    task,
    taskWorking,
    canSendBack,
    sendBackReviews,
    workspaceBranch,
    unpublishedCommits,
    workspaceDiverged,
    latestAgentReview,
    rereviewModel,
    totals,
    hasDraft,
    orderedFiles,
    fileGroups,
    selectedIndex,
    selectedFile,
    selectedThreads,
    selectedDrafts,
    markedLines,
    latestReviews,
    checks,
    pipelines,
    strategyHint,
    strategies,
    statusIcon,
    commentCount,
    reviewCommentCount,
    refresh,
    selectFile,
    stepFile,
    jumpToComment,
    startComment,
    addDraft,
    removeDraft,
    postComment,
    commentNow,
    reply,
    setResolved,
    submitReview,
    startAgentReview,
    continueReview,
    continueTask,
    continueError,
    stoppedReview,
    taskBudgetStop,
    rereview,
    publishWorkspaceCommits,
    sendBack,
    merge,
    resolveConflicts,
    close,
    dispose,
  }
}

export type MergeRequestState = ReturnType<typeof createMergeRequestState>

export const mergeRequestKey: InjectionKey<MergeRequestState> = Symbol('mergeRequest')

// The state of the merge request whose modal this component sits in.
export function useMergeRequest(): MergeRequestState {
  const state = inject(mergeRequestKey)
  if (!state) throw new Error('useMergeRequest needs a MergeRequestModal ancestor')
  return state
}
