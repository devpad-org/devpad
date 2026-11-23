import { computed, ref, type Ref } from 'vue'
import type { MergeRequestDetail } from '@/api/mergeRequests'
import { pipelineApi, type MergeRequestPipelineStatus, type Pipeline } from '@/api/pipelines'
import { PIPELINE_COPY, PIPELINE_STATUS, sourceLabel } from '@/components/pipeline/pipelineFormat'
import { errorText, plural, shortSha } from './format'
import type { Check } from './useMergeRequest'

// How many of a merge request's pipelines its activity lists.
const LISTED = 50

// A merge request's pipelines: the server's word on how it stands now
// (whether a pipeline is configured, the one of its current head, target
// tip and jobs, and the merge gate), the one its Pipeline card shows, the
// merge card's row, its pipelines for the activity, and starting one by
// hand; and apart from all that, the pipeline its change proposes, which
// counts for none of it. Whether a result is current, and whether it lets
// the merge request merge, is never worked out here: the server says.
export function createMrPipelines(projectId: Ref<number>, mrId: Ref<number>, mr: Ref<MergeRequestDetail | null>, onStarted: () => void) {
  const pipelines = ref<Pipeline[]>([])
  const status = ref<MergeRequestPipelineStatus | null>(null)
  const loaded = ref(false)
  // The pipeline the card shows when the user picked one from the
  // activity; null shows the current one, or else the latest.
  const pickedId = ref<number | null>(null)
  const starting = ref(false)
  const startError = ref('')
  const proposedStarting = ref(false)
  const proposedStartError = ref('')
  // Bumped for each load and on reset, so an answer older than the last
  // asked for is dropped.
  let seq = 0

  const current = computed(() => status.value?.current ?? null)
  // Whether the pipeline lets the merge request merge.
  const gate = computed(() => status.value?.gate ?? null)
  // Why no pipeline guards the merge request, when none does.
  const unguardedText = computed(() => {
    const g = gate.value
    const target = mr.value?.targetBranch
    if (!g || !target || g.guarded) return ''
    return status.value?.configured ? PIPELINE_COPY.gateNotTicked(target) : PIPELINE_COPY.gateNotConfigured(target)
  })
  // What the Pipeline card says of the gate: that the pipeline guards the
  // merge request, or why none does.
  const gateText = computed(() => {
    const g = gate.value
    const target = mr.value?.targetBranch
    if (!g || !target || g.state === 'closed') return ''
    return g.guarded ? PIPELINE_COPY.gateGuarded(target) : unguardedText.value
  })
  // What the merge card's row adds of the gate: what it takes to merge
  // while the pipeline keeps it from merging, or why no pipeline guards it.
  const gateNote = computed(() => {
    const g = gate.value
    if (!g || g.state === 'closed' || g.clear) return unguardedText.value
    return g.state === 'conflict' ? PIPELINE_COPY.gateConflict : PIPELINE_COPY.gateShut
  })
  // The files the pipeline's trial merge found conflicting, when its
  // branch doesn't merge onto the target as it is now.
  const gateConflicts = computed(() => {
    const paths = gate.value?.state === 'conflict' ? (current.value?.conflictPaths ?? []) : []
    return paths.length ? paths : null
  })
  // The current pipeline's failing jobs that fail on the target too, in
  // its own pipeline of the same commit, which devpad doesn't send back to
  // the task's agent.
  const alsoFailing = computed(() => {
    const t = status.value?.onTarget
    const target = mr.value?.targetBranch
    return t && target && t.failing.length ? { target, jobs: t.failing } : undefined
  })
  // The target's own pipeline of the same commit is still to say which of
  // the current pipeline's failures are already there.
  const targetPending = computed(() => !!status.value?.onTarget?.pending)
  const latest = computed(() => status.value?.latest ?? null)
  // The pipeline of the current commits and jobs that was cancelled or
  // couldn't run, when none of them counts, with why.
  const noResult = computed(() => status.value?.noResult ?? null)
  const shownId = computed(() => pickedId.value ?? current.value?.id ?? noResult.value?.pipeline.id ?? latest.value?.id ?? null)
  // The pipeline the card shows is not of the commits and jobs the merge
  // request has now.
  const shownStale = computed(
    () => !!status.value?.open && shownId.value !== null && shownId.value !== current.value?.id && shownId.value !== noResult.value?.pipeline.id,
  )
  const shownPipeline = computed(
    () =>
      pipelines.value.find((p) => p.id === shownId.value) ??
      [latest.value, noResult.value?.pipeline].find((p) => p && p.id === shownId.value) ??
      null,
  )
  // A pipeline that was replaced before it ran says nothing worth listing.
  const timelinePipelines = computed(() => pipelines.value.filter((p) => p.status !== 'superseded'))

  // The pipeline the change proposes, and the one of them its card shows:
  // the one of the commits and jobs as they are now, or the newest that
  // ended without a result on them, or else the latest, out of date.
  const proposed = computed(() => status.value?.proposed ?? null)
  const proposedShown = computed(() => {
    const p = proposed.value
    return p?.current ?? p?.noResult?.pipeline ?? p?.latest ?? null
  })
  const proposedStale = computed(() => {
    const p = proposed.value
    const shown = proposedShown.value
    return !!p && !!shown && shown.id !== p.current?.id && shown.id !== p.noResult?.pipeline.id
  })

  async function load() {
    const mine = ++seq
    // Each answer stands alone: the status reads the repo, which may be
    // down while the list still loads.
    const [list, st] = await Promise.allSettled([
      // The activity lists the merge request's own pipelines; the one its
      // change proposes has its card.
      pipelineApi.list(projectId.value, { mergeRequestId: mrId.value, purpose: 'official', limit: LISTED }),
      pipelineApi.mergeRequestStatus(projectId.value, mrId.value),
    ])
    if (mine !== seq) return
    if (list.status === 'fulfilled') pipelines.value = list.value.pipelines
    if (st.status === 'fulfilled') status.value = st.value.status
    if (list.status === 'fulfilled' || st.status === 'fulfilled') loaded.value = true
  }

  // Starts a pipeline of the merge request as it is now, or finds the one
  // already running the same, and shows it.
  async function start() {
    const m = mr.value
    if (!m || starting.value) return
    starting.value = true
    startError.value = ''
    try {
      await pipelineApi.start(projectId.value, { mergeRequestId: m.id })
      pickedId.value = null
      await load()
      onStarted()
    } catch (e) {
      startError.value = errorText(e, 'Failed to start the pipeline')
    } finally {
      starting.value = false
    }
  }

  // Runs the pipeline the change proposes as the merge request is now, or
  // finds the one already running the same.
  async function startProposed() {
    const m = mr.value
    if (!m || proposedStarting.value) return
    proposedStarting.value = true
    proposedStartError.value = ''
    try {
      await pipelineApi.start(projectId.value, { mergeRequestId: m.id, purpose: 'proposed' })
      await load()
    } catch (e) {
      proposedStartError.value = errorText(e, 'Failed to start the proposed pipeline')
    } finally {
      proposedStarting.value = false
    }
  }

  function show(id: number | null) {
    pickedId.value = id === (current.value?.id ?? noResult.value?.pipeline.id ?? latest.value?.id) ? null : id
  }

  // The merge card's row: how the pipeline stands, and the gate's note.
  const check = computed<Check | null>(() => {
    const row = pipelineRow.value
    if (!row) return null
    const detail = [row.detail, gateNote.value].filter(Boolean).join(' ')
    return { ...row, detail: detail || undefined }
  })

  const pipelineRow = computed<Check | null>(() => {
    const st = status.value
    if (!mr.value || !st || !st.open) return null
    const base = { key: 'pipeline' }
    if (!st.configured) {
      return {
        ...base,
        tone: 'neutral',
        icon: 'minus',
        title: 'No pipeline configured',
        detail: 'Nothing tests the change.',
      }
    }
    if (st.invalid) return { ...base, tone: 'error', icon: 'warning', title: "Pipeline doesn't parse", detail: st.invalid }
    const p = st.current
    if (!p && st.noResult) {
      const cancelled = st.noResult.pipeline.status === 'cancelled'
      return {
        ...base,
        tone: cancelled ? 'neutral' : 'error',
        icon: cancelled ? 'ban' : 'warning',
        title: cancelled ? 'Pipeline cancelled' : "Pipeline couldn't run",
        detail: st.noResult.reason,
      }
    }
    if (!p) {
      const old = st.latest
      if (!old) {
        const detail = st.automatic ? 'Devpad runs one by itself.' : 'Run one from the Pipeline card below.'
        return { ...base, tone: 'neutral', icon: 'circle', title: 'No pipeline yet', detail }
      }
      return {
        ...base,
        tone: 'warning',
        icon: 'clock',
        title: 'Pipeline out of date',
        detail: `The latest, #${old.id}, ${PIPELINE_STATUS[old.status].label.toLowerCase()} on ${shortSha(old.headSha)} onto ${old.targetBranch} at ${shortSha(old.targetSha)}; the branch, ${old.targetBranch} or the jobs have changed since. Run it again.`,
      }
    }
    const from = `Jobs from ${sourceLabel(p)}.`
    switch (p.status) {
      case 'queued':
      case 'running':
        return { ...base, tone: 'active', icon: 'clock', title: `Pipeline ${p.status}`, detail: from, spin: true }
      case 'passed':
        return { ...base, tone: 'success', icon: 'check', title: 'Pipeline passed', detail: `${plural(p.jobs.length, 'job')} passed on this branch merged onto ${p.targetBranch} as it is now. ${from}` }
      case 'failed': {
        const failed = p.jobs.filter((j) => ['failed', 'timed_out', 'out_of_memory'].includes(j.status)).map((j) => j.name)
        return { ...base, tone: 'error', icon: 'warning', title: 'Pipeline failed', detail: failed.length ? `Failed: ${failed.join(', ')}.` : undefined }
      }
      case 'conflict':
        return { ...base, tone: 'error', icon: 'warning', title: `Doesn't merge onto ${p.targetBranch}`, detail: 'The pipeline found conflicts, so nothing ran.' }
      default:
        return { ...base, tone: 'error', icon: 'warning', title: PIPELINE_STATUS[p.status].label, detail: p.error || undefined }
    }
  })

  function reset() {
    seq++
    pipelines.value = []
    status.value = null
    loaded.value = false
    pickedId.value = null
    startError.value = ''
    proposedStartError.value = ''
  }

  return {
    pipelines,
    status,
    loaded,
    current,
    latest,
    noResult,
    gate,
    gateText,
    gateNote,
    gateConflicts,
    alsoFailing,
    targetPending,
    shownId,
    shownStale,
    shownPipeline,
    pickedId,
    timelinePipelines,
    starting,
    startError,
    proposed,
    proposedShown,
    proposedStale,
    proposedStarting,
    proposedStartError,
    check,
    load,
    start,
    startProposed,
    show,
    reset,
  }
}

export type MrPipelines = ReturnType<typeof createMrPipelines>
