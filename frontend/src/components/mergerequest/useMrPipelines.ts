import { computed, ref, type Ref } from 'vue'
import type { MergeRequestDetail } from '@/api/mergeRequests'
import { pipelineApi, type MergeRequestPipelineStatus, type Pipeline } from '@/api/pipelines'
import { PIPELINE_STATUS, sourceLabel } from '@/components/pipeline/pipelineFormat'
import { errorText, plural, shortSha } from './format'
import type { Check } from './useMergeRequest'

// How many of a merge request's pipelines its activity lists.
const LISTED = 50

// A merge request's pipelines: the server's word on how it stands now
// (whether a pipeline is configured, and the one of its current head,
// target tip and jobs), the one its Pipeline card shows, the merge card's
// row, its pipelines for the activity, and starting one by hand. Whether a
// result is current is never worked out here: the server says. Nothing here
// keeps the merge request from merging.
export function createMrPipelines(projectId: Ref<number>, mrId: Ref<number>, mr: Ref<MergeRequestDetail | null>, onStarted: () => void) {
  const pipelines = ref<Pipeline[]>([])
  const status = ref<MergeRequestPipelineStatus | null>(null)
  const loaded = ref(false)
  // The pipeline the card shows when the user picked one from the
  // activity; null shows the current one, or else the latest.
  const pickedId = ref<number | null>(null)
  const starting = ref(false)
  const startError = ref('')
  // Bumped for each load and on reset, so an answer older than the last
  // asked for is dropped.
  let seq = 0

  const current = computed(() => status.value?.current ?? null)
  const latest = computed(() => status.value?.latest ?? null)
  // The pipeline of the current commits and jobs that was cancelled or
  // couldn't run, when none of them counts, with why.
  const settled = computed(() => status.value?.settled ?? null)
  const shownId = computed(() => pickedId.value ?? current.value?.id ?? settled.value?.pipeline.id ?? latest.value?.id ?? null)
  // The pipeline the card shows is not of the commits and jobs the merge
  // request has now.
  const shownStale = computed(
    () => !!status.value?.open && shownId.value !== null && shownId.value !== current.value?.id && shownId.value !== settled.value?.pipeline.id,
  )
  const shownPipeline = computed(
    () =>
      pipelines.value.find((p) => p.id === shownId.value) ??
      [latest.value, settled.value?.pipeline].find((p) => p && p.id === shownId.value) ??
      null,
  )
  // A pipeline that was replaced before it ran says nothing worth listing.
  const timelinePipelines = computed(() => pipelines.value.filter((p) => p.status !== 'superseded'))

  async function load() {
    const mine = ++seq
    // Each answer stands alone: the status reads the repo, which may be
    // down while the list still loads.
    const [list, st] = await Promise.allSettled([
      pipelineApi.list(projectId.value, { mergeRequestId: mrId.value, limit: LISTED }),
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

  function show(id: number | null) {
    pickedId.value = id === (current.value?.id ?? settled.value?.pipeline.id ?? latest.value?.id) ? null : id
  }

  const check = computed<Check | null>(() => {
    const st = status.value
    if (!mr.value || !st || !st.open) return null
    const base = { key: 'pipeline' }
    if (!st.configured) {
      return {
        ...base,
        tone: 'neutral',
        icon: 'minus',
        title: 'No pipeline configured',
        detail: `${mr.value.targetBranch} has no .devpad/pipeline.yml and the project no pipeline setting, so nothing tests the change.`,
      }
    }
    if (st.invalid) return { ...base, tone: 'error', icon: 'warning', title: "Pipeline doesn't parse", detail: st.invalid }
    const p = st.current
    if (!p && st.settled) {
      const cancelled = st.settled.pipeline.status === 'cancelled'
      return {
        ...base,
        tone: cancelled ? 'neutral' : 'error',
        icon: cancelled ? 'ban' : 'warning',
        title: cancelled ? 'Pipeline cancelled' : "Pipeline couldn't run",
        detail: st.settled.reason,
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
  }

  return {
    pipelines,
    status,
    loaded,
    current,
    latest,
    settled,
    shownId,
    shownStale,
    shownPipeline,
    pickedId,
    timelinePipelines,
    starting,
    startError,
    check,
    load,
    start,
    show,
    reset,
  }
}

export type MrPipelines = ReturnType<typeof createMrPipelines>
