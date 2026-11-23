import { computed, ref, type Ref } from 'vue'
import type { MergeRequestDetail } from '@/api/mergeRequests'
import { pipelineApi, type Pipeline } from '@/api/pipelines'
import { PIPELINE_STATUS, sourceLabel } from '@/components/pipeline/pipelineFormat'
import { errorText, plural } from './format'
import type { Check } from './useMergeRequest'

// How many of a merge request's pipelines its activity lists.
const LISTED = 50

// A merge request's pipelines: the latest, the one its Pipeline card shows,
// the merge card's row for it, and starting one by hand. Nothing here keeps
// the merge request from merging: the row is for the user to weigh.
export function createMrPipelines(projectId: Ref<number>, mrId: Ref<number>, mr: Ref<MergeRequestDetail | null>, onStarted: () => void) {
  const pipelines = ref<Pipeline[]>([])
  const loaded = ref(false)
  // The pipeline the card shows when the user picked an older one; null
  // shows the latest.
  const pickedId = ref<number | null>(null)
  const starting = ref(false)
  const startError = ref('')
  let generation = 0

  const latest = computed(() => pipelines.value[0] ?? null)
  const shownId = computed(() => pickedId.value ?? latest.value?.id ?? null)
  // The latest pipeline tested a head the merge request has moved on from.
  const latestStale = computed(() => !!latest.value && !!mr.value && latest.value.headSha !== mr.value.headSha)
  // A pipeline that was replaced before it ran says nothing worth listing.
  const timelinePipelines = computed(() => pipelines.value.filter((p) => p.status !== 'superseded'))

  async function load() {
    const gen = generation
    try {
      const res = await pipelineApi.list(projectId.value, { mergeRequestId: mrId.value, limit: LISTED })
      if (gen !== generation) return
      pipelines.value = res.pipelines
      loaded.value = true
    } catch {
      // The pipelines add to the page; it shows without them.
    }
  }

  // Starts a pipeline of the merge request as it is now, or finds the one
  // already running the same, and shows it.
  async function start() {
    const m = mr.value
    if (!m || starting.value) return
    starting.value = true
    startError.value = ''
    try {
      const res = await pipelineApi.start(projectId.value, { mergeRequestId: m.id })
      pickedId.value = null
      if (!pipelines.value.some((p) => p.id === res.pipeline.id)) pipelines.value = [res.pipeline, ...pipelines.value]
      onStarted()
    } catch (e) {
      startError.value = errorText(e, 'Failed to start the pipeline')
    } finally {
      starting.value = false
    }
  }

  function show(id: number | null) {
    pickedId.value = id === latest.value?.id ? null : id
  }

  const check = computed<Check | null>(() => {
    const m = mr.value
    if (!m || !loaded.value) return null
    const p = latest.value
    const base = { key: 'pipeline' }
    if (!p) {
      return { ...base, tone: 'neutral', icon: 'circle', title: 'No pipeline yet', detail: 'Run one from the Pipeline card below.' }
    }
    const older = latestStale.value ? ' It ran on an older commit than the branch is at now.' : ''
    const from = `Jobs from ${sourceLabel(p)}.`
    switch (p.status) {
      case 'not_configured':
        return {
          ...base,
          tone: 'neutral',
          icon: 'minus',
          title: 'No pipeline configured',
          detail: `${p.targetBranch} has no .devpad/pipeline.yml and the project no pipeline setting, so nothing tests the change.`,
        }
      case 'queued':
      case 'running':
        return { ...base, tone: 'active', icon: 'clock', title: `Pipeline ${p.status}`, detail: `${from}${older}`, spin: true }
      case 'passed':
        return latestStale.value
          ? { ...base, tone: 'warning', icon: 'clock', title: 'Pipeline passed on an older commit', detail: `New commits landed since; run it again. ${from}` }
          : { ...base, tone: 'success', icon: 'check', title: 'Pipeline passed', detail: `${plural(p.jobs.length, 'job')} passed. ${from}` }
      case 'failed': {
        const failed = p.jobs.filter((j) => ['failed', 'timed_out', 'out_of_memory'].includes(j.status)).map((j) => j.name)
        return { ...base, tone: 'error', icon: 'warning', title: 'Pipeline failed', detail: `${failed.length ? `Failed: ${failed.join(', ')}.` : ''}${older}` }
      }
      case 'conflict':
        return { ...base, tone: 'error', icon: 'warning', title: `Pipeline: doesn’t merge onto ${p.targetBranch}`, detail: `Nothing ran.${older}` }
      case 'invalid':
        return { ...base, tone: 'error', icon: 'warning', title: 'Pipeline doesn’t parse', detail: p.error || undefined }
      case 'error':
        return { ...base, tone: 'error', icon: 'warning', title: 'Pipeline couldn’t run', detail: `${p.error}${older}` || undefined }
      default:
        return {
          ...base,
          tone: 'neutral',
          icon: 'ban',
          title: `Pipeline ${PIPELINE_STATUS[p.status].label.toLowerCase()}`,
          detail: 'It said nothing of the change; run it again from the Pipeline card below.',
        }
    }
  })

  function reset() {
    generation++
    pipelines.value = []
    loaded.value = false
    pickedId.value = null
    startError.value = ''
  }

  return { pipelines, loaded, latest, latestStale, shownId, pickedId, timelinePipelines, starting, startError, check, load, start, show, reset }
}

export type MrPipelines = ReturnType<typeof createMrPipelines>
