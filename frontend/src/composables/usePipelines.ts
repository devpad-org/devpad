import { computed, ref, watch, type Ref } from 'vue'
import { pipelineApi, type Pipeline, type PipelineListFilter, type PipelineSubject } from '@/api/pipelines'
import { pollFailure, usePolling, type PollOutcome } from './usePolling'

const DETAIL_POLL_MS = 2500
const LIST_POLL_MS = 5000
const PAGE = 30

export function pipelineFinished(p: Pipeline): boolean {
  return p.status !== 'queued' && p.status !== 'running'
}

// What a pipeline tests, and what for, to start another of the same.
export function subjectOf(p: Pipeline): PipelineSubject {
  return p.kind === 'branch' ? { branch: p.branch } : { mergeRequestId: p.mergeRequestId, purpose: p.purpose }
}

function errorText(e: unknown, fallback: string): string {
  return e instanceof Error ? e.message : fallback
}

// usePipeline reads one pipeline with its jobs' scripts, and reads it again
// while it runs and active is true.
export function usePipeline(projectId: Ref<number | null>, pipelineId: Ref<number | null>, active: Ref<boolean>) {
  const pipeline = ref<Pipeline | null>(null)
  const error = ref('')
  const busy = ref(false)
  let generation = 0

  async function load(): Promise<PollOutcome> {
    const project = projectId.value
    const id = pipelineId.value
    if (project === null || id === null) return
    const gen = generation
    try {
      const res = await pipelineApi.get(project, id)
      if (gen !== generation) return
      pipeline.value = res.pipeline
      error.value = ''
    } catch (e) {
      if (gen !== generation) return
      error.value = errorText(e, 'Failed to load the pipeline')
      return pollFailure(e)
    }
  }

  const polling = computed(() => active.value && pipelineId.value !== null && (!pipeline.value || !pipelineFinished(pipeline.value)))
  const { poll } = usePolling(load, DETAIL_POLL_MS, polling)

  watch([projectId, pipelineId], () => {
    generation++
    pipeline.value = null
    error.value = ''
    void poll()
  })

  // Reads it again now, finished or not: a finished one isn't read on a
  // schedule, though what is around it may have changed, such as whether
  // Run again would start.
  function reread() {
    if (polling.value) void poll()
    else void load()
  }
  // A finished one is read again as it comes back into sight.
  watch(active, (on) => {
    if (on && pipeline.value && pipelineFinished(pipeline.value)) reread()
  })

  async function act(fn: (project: number, p: Pipeline) => Promise<Pipeline | null>): Promise<Pipeline | null> {
    const project = projectId.value
    const p = pipeline.value
    if (project === null || !p || busy.value) return null
    busy.value = true
    error.value = ''
    try {
      return await fn(project, p)
    } catch (e) {
      error.value = errorText(e, 'The pipeline could not do that')
      return null
    } finally {
      busy.value = false
    }
  }

  // Stops the pipeline, which waits a while for its jobs to be removed.
  function cancel() {
    return act(async (project, p) => {
      const res = await pipelineApi.cancel(project, p.id)
      if (pipelineId.value === p.id) pipeline.value = res.pipeline
      return res.pipeline
    })
  }

  // Runs the pipeline's branch or merge request again at the commits they
  // are at now, returning the pipeline that does.
  function runAgain() {
    return act(async (project, p) => (await pipelineApi.start(project, subjectOf(p))).pipeline)
  }

  return { pipeline, error, busy, refresh: poll, reread, cancel, runAgain }
}

// usePipelineList lists a project's pipelines newest first, as filter
// picks, reading the newest again while active is true, and pages back on
// request.
export function usePipelineList(projectId: Ref<number | null>, filter: Ref<PipelineListFilter>, active: Ref<boolean>) {
  const pipelines = ref<Pipeline[]>([])
  const loaded = ref(false)
  const error = ref('')
  // Older pipelines may be left to page back to.
  const hasMore = ref(false)
  const loadingMore = ref(false)
  let generation = 0

  // Reads the newest again, as many as are shown, and keeps the older
  // pages shown below them.
  async function load(): Promise<PollOutcome> {
    const project = projectId.value
    if (project === null) return
    const gen = generation
    const limit = Math.min(100, Math.max(PAGE, pipelines.value.length))
    try {
      const res = await pipelineApi.list(project, { ...filter.value, limit })
      if (gen !== generation) return
      const fresh = res.pipelines
      const oldest = fresh[fresh.length - 1]?.id ?? Infinity
      pipelines.value = [...fresh, ...pipelines.value.filter((p) => p.id < oldest)]
      if (!loaded.value) hasMore.value = fresh.length === limit
      loaded.value = true
      error.value = ''
    } catch (e) {
      if (gen !== generation) return
      error.value = errorText(e, 'Failed to load pipelines')
      return pollFailure(e)
    }
  }

  async function loadMore() {
    const project = projectId.value
    const oldest = pipelines.value[pipelines.value.length - 1]
    if (project === null || !oldest || loadingMore.value) return
    const gen = generation
    loadingMore.value = true
    try {
      const res = await pipelineApi.list(project, { ...filter.value, before: oldest.id, limit: PAGE })
      if (gen !== generation) return
      pipelines.value = [...pipelines.value, ...res.pipelines]
      hasMore.value = res.pipelines.length === PAGE
    } catch (e) {
      if (gen === generation) error.value = errorText(e, 'Failed to load older pipelines')
    } finally {
      loadingMore.value = false
    }
  }

  const polling = computed(() => active.value && projectId.value !== null)
  const { poll } = usePolling(load, LIST_POLL_MS, polling)

  watch(
    [projectId, filter],
    () => {
      generation++
      pipelines.value = []
      loaded.value = false
      hasMore.value = false
      error.value = ''
      void poll()
    },
    { deep: true },
  )

  // Puts a pipeline just started or changed in the list at once.
  function upsert(p: Pipeline) {
    const i = pipelines.value.findIndex((x) => x.id === p.id)
    if (i !== -1) pipelines.value[i] = p
    else pipelines.value = [p, ...pipelines.value].sort((a, b) => b.id - a.id)
  }

  return { pipelines, loaded, error, hasMore, loadingMore, refresh: poll, loadMore, upsert }
}
