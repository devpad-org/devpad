import { computed, ref, watch, type Ref } from 'vue'
import { MAX_COMMIT_LOOKUPS, pipelineApi, type Pipeline } from '@/api/pipelines'
import { useActiveProjectStore } from '@/stores/activeProject'
import { pipelineFinished } from './usePipelines'
import { pollFailure, usePolling, type PollOutcome } from './usePolling'

// How often it looks whether to read the commits' pipelines again.
const POLL_MS = 4000
// With none under way, how long it goes before reading them again anyway,
// for a pipeline that started on a commit already shown.
const IDLE_MS = 30_000

// useCommitPipelines reads, by commit, the newest pipeline of each branch
// each of shas (the project's commits, full hashes) was tested on, while
// active is true: at once when the project or the commits change or a
// branch's badge does, every few seconds while one is under way, and now
// and then otherwise. The pipelines are what the server found; nothing
// here works out which one counts. A failed read keeps what was shown.
export function useCommitPipelines(projectId: Ref<number | null>, shas: Ref<string[]>, active: Ref<boolean>) {
  const store = useActiveProjectStore()
  const byCommit = ref<Record<string, Pipeline[]>>({})
  let loadedKey = ''
  let loadedAt = 0
  let generation = 0

  const asked = computed(() => shas.value.slice(0, MAX_COMMIT_LOOKUPS))
  const key = computed(() => `${projectId.value}:${asked.value.join(',')}`)
  const underWay = computed(() => Object.values(byCommit.value).some((list) => list.some((p) => !pipelineFinished(p))))

  async function load(): Promise<PollOutcome> {
    const project = projectId.value
    const k = key.value
    if (project === null || asked.value.length === 0) return
    if (k === loadedKey && !underWay.value && Date.now() - loadedAt < IDLE_MS) return
    const gen = generation
    try {
      const res = await pipelineApi.commits(project, asked.value)
      if (gen !== generation) return
      byCommit.value = res.commits ?? {}
      loadedKey = k
      loadedAt = Date.now()
    } catch (e) {
      if (gen !== generation) return
      return pollFailure(e)
    }
  }

  const enabled = computed(() => active.value && projectId.value !== null && asked.value.length > 0)
  const { poll } = usePolling(load, POLL_MS, enabled)

  // Another project's pipelines are never shown for this one's commits.
  watch(projectId, () => {
    generation++
    byCommit.value = {}
    loadedKey = ''
  })
  watch(key, () => {
    if (enabled.value) void poll()
  })

  // The project's own badges are read every few seconds: when one of a
  // branch's moves, a pipeline of a commit shown may have started or ended.
  const badges = computed(() => {
    if (store.projectId === null || store.projectId !== projectId.value) return ''
    const of = (p: Pipeline | null | undefined) => `${p?.id}:${p?.status}`
    return Object.entries(store.pipelineBranchStandings)
      .map(([branch, st]) => `${branch}:${st.tip}:${of(st.current)}:${of(st.noResult?.pipeline)}:${of(st.latest)}`)
      .join(',')
  })
  watch(badges, () => {
    loadedAt = 0
    if (enabled.value) void poll()
  })

  return { byCommit }
}
