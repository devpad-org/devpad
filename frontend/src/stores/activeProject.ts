import { defineStore } from 'pinia'
import { computed, ref } from 'vue'
import { projectApi, type ConversionNotice, type GitPushEvent, type Project, type RepoBranch, type Sandbox } from '@/api/projects'

// A push notice, keyed by devpad's epoch and the push's sequence number.
export type GitPushNotice = GitPushEvent & { key: string }
import { taskApi, type Task, type TaskStatus } from '@/api/tasks'
import { mergeRequestApi, type MergeRequest } from '@/api/mergeRequests'

const POLL_INTERVAL_MS = 4000

export const TASK_COLUMNS: { title: string; statuses: TaskStatus[] }[] = [
  { title: 'To do', statuses: ['todo', 'failed'] },
  { title: 'In progress', statuses: ['queued', 'in_progress'] },
  { title: 'Review', statuses: ['in_review', 'changes_requested'] },
  { title: 'Done', statuses: ['merged', 'cancelled'] },
]

// The project open in the IDE shell: its tasks, merge requests and agent
// sandboxes, polled while the shell is mounted.
export const useActiveProjectStore = defineStore('activeProject', () => {
  const projectId = ref<number | null>(null)
  const project = ref<Project | null>(null)
  const tasks = ref<Task[]>([])
  const mergeRequests = ref<MergeRequest[]>([])
  const sandboxes = ref<Sandbox[]>([])
  // The canonical repo's branches, loaded on demand by the panels that need
  // them rather than on every poll.
  const branches = ref<RepoBranch[]>([])
  const loadError = ref('')
  const selectedTaskId = ref<number | null>(null)
  // What converting a workspace into this project did, until dismissed.
  const conversion = ref<ConversionNotice | null>(null)
  let conversionLoaded = false
  // Pushes made with `git push devpad` since the shell opened, until shown
  // and dismissed. Each is keyed by devpad's epoch and its sequence number.
  const gitPushes = ref<GitPushNotice[]>([])
  let gitActivitySeq: number | null = null
  let gitActivityEpoch: string | null = null
  // Many panels call refresh; only one activity request runs at a time, so
  // two cannot deliver the same pushes.
  let gitActivityLoading = false

  // Bumped whenever the open project changes, so responses for the previous
  // one are dropped.
  let generation = 0
  let timer: ReturnType<typeof setInterval> | null = null

  const ready = computed(() => project.value?.status === 'ready')
  const selectedTask = computed(() => tasks.value.find((t) => t.id === selectedTaskId.value) ?? null)
  const openMergeRequests = computed(() => mergeRequests.value.filter((mr) => mr.status === 'open'))
  const activeSandboxes = computed(() => sandboxes.value.filter((s) => s.status !== 'stopped').length)
  const branchNames = computed(() => new Set(branches.value.map((b) => b.name)))

  // A task's newest merge request, open ones first.
  const mergeRequestByTask = computed(() => {
    const byTask = new Map<number, MergeRequest>()
    for (const mr of mergeRequests.value) {
      if (!mr.taskId) continue
      const current = byTask.get(mr.taskId)
      if (!current || (current.status !== 'open' && (mr.status === 'open' || mr.id > current.id))) {
        byTask.set(mr.taskId, mr)
      }
    }
    return byTask
  })

  const columns = computed(() =>
    TASK_COLUMNS.map((column) => ({
      ...column,
      tasks: tasks.value
        .filter((t) => column.statuses.includes(t.status))
        .sort((a, b) => b.updatedAt.localeCompare(a.updatedAt)),
    })),
  )

  function sandboxTask(sandboxId: number): Task | undefined {
    return tasks.value.find((t) => t.workspaceId === sandboxId)
  }

  function openMergeRequestFor(branch: string): MergeRequest | undefined {
    return openMergeRequests.value.find((mr) => mr.sourceBranch === branch)
  }

  async function refresh() {
    const id = projectId.value
    if (id === null) return
    const gen = generation
    try {
      if (!project.value || project.value.status === 'creating') {
        const res = await projectApi.get(id)
        if (gen !== generation) return
        project.value = res.project
      }
      if (project.value?.status === 'ready') {
        const [t, m, s] = await Promise.all([
          taskApi.list(id),
          mergeRequestApi.list(id),
          projectApi.sandboxes(id),
        ])
        if (gen !== generation) return
        tasks.value = t.tasks ?? []
        mergeRequests.value = m.mergeRequests ?? []
        sandboxes.value = s.sandboxes ?? []
      }
      if (project.value?.status === 'ready') await refreshGitActivity(id, gen)
      // Load the notice once, then follow it while devpad finishes it.
      if (!conversionLoaded || (conversion.value && !conversion.value.finished)) {
        const res = await projectApi.conversionNotice(id)
        if (gen !== generation) return
        conversion.value = res.conversion
        conversionLoaded = true
      }
      loadError.value = ''
    } catch (e) {
      if (gen === generation) loadError.value = e instanceof Error ? e.message : 'Failed to load project'
    }
  }

  async function refreshGitActivity(id: number, gen: number) {
    if (gitActivityLoading) return
    gitActivityLoading = true
    try {
      const res = await projectApi.gitActivity(id, gitActivitySeq ?? undefined, gitActivityEpoch ?? undefined)
      if (gen !== generation) return
      const restarted = gitActivityEpoch !== null && res.epoch !== gitActivityEpoch
      if (gitActivitySeq !== null && res.events.length > 0) {
        const known = new Set(gitPushes.value.map((e) => e.key))
        const fresh = res.events
          .map((e) => ({ ...e, key: `${res.epoch}:${e.seq}` }))
          .filter((e) => !known.has(e.key))
        if (fresh.length > 0) {
          gitPushes.value = [...gitPushes.value, ...fresh]
          if (fresh.some((e) => e.ok) && branches.value.length > 0) void refreshBranches()
        }
      }
      // The cursor only moves forward, except across a devpad restart,
      // where the sequence starts again.
      if (restarted || gitActivitySeq === null || res.latest > gitActivitySeq) gitActivitySeq = res.latest
      gitActivityEpoch = res.epoch
    } catch {
      // Push notices are a convenience; the board still loads without them.
    } finally {
      gitActivityLoading = false
    }
  }

  function dismissGitPush(key: string) {
    gitPushes.value = gitPushes.value.filter((e) => e.key !== key)
  }

  async function refreshBranches() {
    const id = projectId.value
    if (id === null || !ready.value) return
    const gen = generation
    const res = await projectApi.branches(id)
    if (gen === generation) branches.value = res.branches ?? []
  }

  function startPolling() {
    stopPolling()
    timer = setInterval(() => {
      if (document.visibilityState === 'visible') void refresh()
    }, POLL_INTERVAL_MS)
  }

  function stopPolling() {
    if (timer !== null) {
      clearInterval(timer)
      timer = null
    }
  }

  function reset() {
    generation++
    project.value = null
    tasks.value = []
    mergeRequests.value = []
    sandboxes.value = []
    branches.value = []
    loadError.value = ''
    selectedTaskId.value = null
    conversion.value = null
    conversionLoaded = false
    gitPushes.value = []
    gitActivitySeq = null
    gitActivityEpoch = null
  }

  // Opens a project and polls it until close.
  async function open(id: number) {
    if (projectId.value !== id) {
      reset()
      projectId.value = id
    }
    startPolling()
    await refresh()
  }

  function close() {
    stopPolling()
    reset()
    projectId.value = null
  }

  function setProject(p: Project) {
    project.value = p
  }

  async function dismissConversion() {
    const id = projectId.value
    if (id === null) return
    await projectApi.dismissConversionNotice(id)
    if (projectId.value === id) conversion.value = null
  }

  function upsertTask(task: Task) {
    const index = tasks.value.findIndex((t) => t.id === task.id)
    if (index === -1) tasks.value.unshift(task)
    else tasks.value[index] = task
  }

  function removeTask(id: number) {
    tasks.value = tasks.value.filter((t) => t.id !== id)
    if (selectedTaskId.value === id) selectedTaskId.value = null
  }

  return {
    projectId,
    project,
    tasks,
    mergeRequests,
    sandboxes,
    branches,
    loadError,
    selectedTaskId,
    conversion,
    gitPushes,
    ready,
    selectedTask,
    openMergeRequests,
    activeSandboxes,
    branchNames,
    mergeRequestByTask,
    columns,
    sandboxTask,
    openMergeRequestFor,
    refresh,
    refreshBranches,
    open,
    close,
    setProject,
    dismissConversion,
    dismissGitPush,
    upsertTask,
    removeTask,
  }
})
