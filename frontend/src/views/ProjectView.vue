<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { projectApi, type Project, type ProjectInput, type Sandbox } from '@/api/projects'
import { taskApi, type Task, type TaskStatus } from '@/api/tasks'
import { mergeRequestApi, type MergeRequest } from '@/api/mergeRequests'
import { useProjectStore } from '@/stores/projects'
import ConfirmModal from '@/components/ConfirmModal.vue'
import AgentModelPicker from '@/components/project/AgentModelPicker.vue'
import ProjectModal from '@/components/project/ProjectModal.vue'
import StatusBadge from '@/components/project/StatusBadge.vue'
import TaskCard from '@/components/project/TaskCard.vue'
import TaskModal from '@/components/project/TaskModal.vue'

const POLL_INTERVAL_MS = 4000

const COLUMNS: { title: string; statuses: TaskStatus[] }[] = [
  { title: 'To do', statuses: ['todo', 'failed'] },
  { title: 'In progress', statuses: ['queued', 'in_progress'] },
  { title: 'Review', statuses: ['in_review', 'changes_requested'] },
  { title: 'Done', statuses: ['merged', 'cancelled'] },
]

const route = useRoute()
const router = useRouter()
const store = useProjectStore()

const projectId = computed(() => Number(route.params.id))
const project = ref<Project | null>(null)
const tasks = ref<Task[]>([])
const mergeRequests = ref<MergeRequest[]>([])
const sandboxes = ref<Sandbox[]>([])
const loadError = ref('')
const actionError = ref('')
const notice = ref('')

const newTitle = ref('')
const newBody = ref('')
const newModel = ref('')
const newEffort = ref('')
const startNow = ref(true)
const creating = ref(false)
const showNewTask = ref(false)

const selectedTaskId = ref<number | null>(null)
const showEdit = ref(false)
const savingProject = ref(false)
const editError = ref('')
const showDelete = ref(false)
const deleting = ref(false)
const pushing = ref(false)
const startingSandbox = ref<number | null>(null)
const showClosed = ref(false)

let timer: ReturnType<typeof setInterval> | null = null

const ready = computed(() => project.value?.status === 'ready')
const selectedTask = computed(() => tasks.value.find((t) => t.id === selectedTaskId.value) ?? null)
const openMergeRequests = computed(() => mergeRequests.value.filter((mr) => mr.status === 'open'))
const visibleMergeRequests = computed(() => (showClosed.value ? mergeRequests.value : openMergeRequests.value))
const activeSandboxes = computed(() => sandboxes.value.filter((s) => s.status !== 'stopped').length)

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
  COLUMNS.map((column) => ({
    ...column,
    tasks: tasks.value
      .filter((t) => column.statuses.includes(t.status))
      .sort((a, b) => b.updatedAt.localeCompare(a.updatedAt)),
  })),
)

async function loadProject() {
  const res = await projectApi.get(projectId.value)
  project.value = res.project
}

async function loadWork() {
  const [t, m, s] = await Promise.all([
    taskApi.list(projectId.value),
    mergeRequestApi.list(projectId.value),
    projectApi.sandboxes(projectId.value),
  ])
  tasks.value = t.tasks ?? []
  mergeRequests.value = m.mergeRequests ?? []
  sandboxes.value = s.sandboxes ?? []
}

async function refresh() {
  try {
    if (!project.value || project.value.status === 'creating') await loadProject()
    if (project.value?.status === 'ready') await loadWork()
    loadError.value = ''
  } catch (e) {
    loadError.value = e instanceof Error ? e.message : 'Failed to load project'
  }
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

watch(projectId, () => {
  project.value = null
  tasks.value = []
  mergeRequests.value = []
  sandboxes.value = []
  selectedTaskId.value = null
  void refresh()
})

onMounted(() => {
  void refresh()
  startPolling()
})

onUnmounted(stopPolling)

async function createTask() {
  if (!newTitle.value.trim()) {
    actionError.value = 'A task needs a title'
    return
  }
  creating.value = true
  actionError.value = ''
  try {
    const res = await taskApi.create(projectId.value, {
      title: newTitle.value.trim(),
      body: newBody.value,
      start: startNow.value,
      model: newModel.value || undefined,
      effort: newEffort.value || undefined,
    })
    tasks.value.unshift(res.task)
    newTitle.value = ''
    newBody.value = ''
    showNewTask.value = false
  } catch (e) {
    actionError.value = e instanceof Error ? e.message : 'Failed to create task'
  } finally {
    creating.value = false
  }
}

function taskChanged(task: Task) {
  const index = tasks.value.findIndex((t) => t.id === task.id)
  if (index !== -1) tasks.value[index] = task
  void refresh()
}

function taskDeleted(id: number) {
  tasks.value = tasks.value.filter((t) => t.id !== id)
  selectedTaskId.value = null
  void refresh()
}

async function saveProject(input: ProjectInput) {
  savingProject.value = true
  editError.value = ''
  try {
    project.value = await store.updateProject(projectId.value, input)
    showEdit.value = false
  } catch (e) {
    editError.value = e instanceof Error ? e.message : 'Failed to save project'
  } finally {
    savingProject.value = false
  }
}

async function deleteProject() {
  deleting.value = true
  try {
    await store.deleteProject(projectId.value)
    router.push({ name: 'projects' })
  } catch (e) {
    actionError.value = e instanceof Error ? e.message : 'Failed to delete project'
    showDelete.value = false
  } finally {
    deleting.value = false
  }
}

async function push() {
  if (!project.value) return
  pushing.value = true
  actionError.value = ''
  notice.value = ''
  try {
    const res = await projectApi.push(projectId.value)
    notice.value = `Pushed ${res.branch} (${res.sha.slice(0, 8)}) upstream.`
  } catch (e) {
    actionError.value = e instanceof Error ? e.message : 'Push failed'
  } finally {
    pushing.value = false
  }
}

async function startSandbox(sandbox: Sandbox) {
  startingSandbox.value = sandbox.id
  actionError.value = ''
  try {
    await projectApi.startSandbox(projectId.value, sandbox.id)
    await refresh()
  } catch (e) {
    actionError.value = e instanceof Error ? e.message : 'Failed to start sandbox'
  } finally {
    startingSandbox.value = null
  }
}

function sandboxTask(sandbox: Sandbox): Task | undefined {
  return tasks.value.find((t) => t.workspaceId === sandbox.id)
}
</script>

<template>
  <div class="page">
    <RouterLink :to="{ name: 'projects' }" class="breadcrumb">&larr; Projects</RouterLink>

    <p v-if="!project && loadError" class="notice notice-error">{{ loadError }}</p>
    <p v-else-if="!project" class="empty-text">Loading project...</p>

    <template v-else>
      <div class="page-header">
        <div class="title-block">
          <div class="title-row">
            <h1 class="page-title">{{ project.name }}</h1>
            <StatusBadge :status="project.status" />
          </div>
          <p v-if="project.description" class="page-subtitle">{{ project.description }}</p>
          <div class="meta-row">
            <span class="mono">{{ project.defaultBranch }}</span>
            <span>&middot;</span>
            <span class="mono">{{ project.upstreamUrl || 'local only' }}</span>
            <span>&middot;</span>
            <span>{{ activeSandboxes }} / {{ project.maxSandboxes }} sandboxes running</span>
          </div>
        </div>
        <div class="header-actions">
          <button v-if="project.upstreamUrl && ready" class="btn btn-secondary" :disabled="pushing" @click="push">
            {{ pushing ? 'Pushing...' : `Push ${project.defaultBranch}` }}
          </button>
          <button class="btn btn-secondary" @click="editError = ''; showEdit = true">Edit</button>
          <button class="btn btn-danger" @click="showDelete = true">Delete</button>
        </div>
      </div>

      <p v-if="project.status === 'creating'" class="notice notice-warning">Setting up the project repository...</p>
      <p v-if="project.status === 'error'" class="notice notice-error">{{ project.error || 'The project failed to set up.' }}</p>
      <p v-if="loadError" class="notice notice-error">{{ loadError }}</p>
      <p v-if="actionError" class="notice notice-error">{{ actionError }}</p>
      <p v-if="notice" class="notice notice-success">{{ notice }}</p>

      <template v-if="ready">
        <section class="section">
          <div class="section-header">
            <h2 class="section-title">Tasks</h2>
            <button class="btn btn-primary btn-sm" @click="showNewTask = !showNewTask">
              {{ showNewTask ? 'Close' : 'New task' }}
            </button>
          </div>

          <form v-if="showNewTask" class="panel new-task" @submit.prevent="createTask">
            <div class="form-group">
              <label class="form-label" for="new-task-title">Title</label>
              <input id="new-task-title" v-model="newTitle" class="form-input" type="text" autocomplete="off" placeholder="Add rate limiting to the login endpoint" />
            </div>
            <div class="form-group">
              <label class="form-label" for="new-task-body">Description</label>
              <textarea
                id="new-task-body"
                v-model="newBody"
                class="form-input form-textarea"
                rows="5"
                placeholder="What should change, and how will we know it works? Markdown is fine."
              />
            </div>
            <div class="new-task-row">
              <AgentModelPicker v-model:model="newModel" v-model:effort="newEffort" />
              <div class="new-task-actions">
                <label class="form-check">
                  <input v-model="startNow" type="checkbox" />
                  Start an agent now
                </label>
                <button type="submit" class="btn btn-primary" :disabled="creating || (startNow && !newModel)">
                  {{ creating ? 'Creating...' : 'Create task' }}
                </button>
              </div>
            </div>
          </form>

          <div class="board">
            <div v-for="column in columns" :key="column.title" class="column">
              <div class="column-header">
                <span>{{ column.title }}</span>
                <span class="column-count">{{ column.tasks.length }}</span>
              </div>
              <div class="column-body">
                <TaskCard
                  v-for="task in column.tasks"
                  :key="task.id"
                  :task="task"
                  :merge-request="mergeRequestByTask.get(task.id)"
                  @select="selectedTaskId = $event.id"
                />
                <p v-if="column.tasks.length === 0" class="empty-text">Nothing here</p>
              </div>
            </div>
          </div>
        </section>

        <div class="lower">
          <section class="section">
            <div class="section-header">
              <h2 class="section-title">Merge requests</h2>
              <label class="form-check">
                <input v-model="showClosed" type="checkbox" />
                Show merged and closed
              </label>
            </div>
            <div class="panel list">
              <RouterLink
                v-for="mr in visibleMergeRequests"
                :key="mr.id"
                :to="{ name: 'merge-request', params: { id: projectId, mrId: mr.id } }"
                class="list-row"
              >
                <div class="row-main">
                  <span class="row-title">!{{ mr.id }} {{ mr.title }}</span>
                  <span class="meta-row">
                    <span class="mono">{{ mr.sourceBranch }} &rarr; {{ mr.targetBranch }}</span>
                  </span>
                </div>
                <StatusBadge :status="mr.status" />
              </RouterLink>
              <p v-if="visibleMergeRequests.length === 0" class="empty-text">
                {{ showClosed ? 'No merge requests yet.' : 'No open merge requests.' }}
              </p>
            </div>
          </section>

          <section class="section">
            <div class="section-header">
              <h2 class="section-title">Sandboxes</h2>
            </div>
            <div class="panel list">
              <div v-for="sandbox in sandboxes" :key="sandbox.id" class="list-row">
                <div class="row-main">
                  <span class="row-title">{{ sandbox.name }}</span>
                  <span class="meta-row">
                    <span>{{ sandbox.kind }}</span>
                    <span class="mono">{{ sandbox.branch }}</span>
                    <template v-if="sandboxTask(sandbox)">
                      <span>&middot;</span>
                      <span>task #{{ sandboxTask(sandbox)?.id }}</span>
                    </template>
                  </span>
                </div>
                <div class="row-actions">
                  <StatusBadge :status="sandbox.status" />
                  <button
                    v-if="sandbox.status === 'stopped'"
                    class="btn btn-secondary btn-sm"
                    :disabled="startingSandbox === sandbox.id"
                    @click="startSandbox(sandbox)"
                  >
                    Start
                  </button>
                  <RouterLink class="btn btn-secondary btn-sm" :to="{ name: 'ide', params: { id: sandbox.id } }">Open</RouterLink>
                </div>
              </div>
              <p v-if="sandboxes.length === 0" class="empty-text">Agents get a sandbox when they start a task.</p>
            </div>
          </section>
        </div>
      </template>
    </template>

    <TaskModal
      :task="selectedTask"
      :project-id="projectId"
      :merge-request="selectedTask ? mergeRequestByTask.get(selectedTask.id) : undefined"
      @close="selectedTaskId = null"
      @changed="taskChanged"
      @deleted="taskDeleted"
    />

    <ProjectModal
      :show="showEdit"
      :project="project"
      :saving="savingProject"
      :error="editError"
      @close="showEdit = false"
      @submit="saveProject"
    />

    <ConfirmModal
      :show="showDelete"
      title="Delete Project"
      :message="`Delete &quot;${project?.name}&quot;, its sandboxes and its repository? Branches that were not pushed upstream are lost.`"
      confirm-label="Delete"
      variant="danger"
      :loading="deleting"
      @confirm="deleteProject"
      @cancel="showDelete = false"
    />
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

.new-task {
  margin-bottom: var(--space-4);
}

.new-task-row,
.new-task-actions {
  display: flex;
  align-items: center;
  justify-content: space-between;
  flex-wrap: wrap;
  gap: var(--space-3);
}

.board {
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  gap: var(--space-3);
}

@media (max-width: 900px) {
  .board {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
}

@media (max-width: 560px) {
  .board {
    grid-template-columns: minmax(0, 1fr);
  }
}

.column {
  display: flex;
  flex-direction: column;
  background: var(--bg-surface);
  border: 0.5px solid var(--border-default);
  border-radius: var(--radius-lg);
  min-height: 160px;
}

.column-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: var(--space-2) var(--space-3);
  font-size: 0.75rem;
  font-weight: 500;
  color: var(--text-secondary);
  border-bottom: 0.5px solid var(--border-default);
}

.column-count {
  color: var(--text-muted);
}

.column-body {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
  padding: var(--space-2);
  max-height: 60vh;
  overflow-y: auto;
}

.column-body .empty-text {
  text-align: center;
}

.lower {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: var(--space-4);
}

@media (max-width: 900px) {
  .lower {
    grid-template-columns: minmax(0, 1fr);
  }
}

.list {
  display: flex;
  flex-direction: column;
  padding: var(--space-1) var(--space-3);
}

.list-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-3);
  padding: var(--space-2) 0;
  color: var(--text-primary);
}

.list-row + .list-row {
  border-top: 0.5px solid var(--border-hairline);
}

a.list-row:hover .row-title {
  color: var(--accent-blue);
}

.row-main {
  display: flex;
  flex-direction: column;
  gap: 2px;
  min-width: 0;
}

.row-title {
  font-size: 0.8rem;
  overflow-wrap: anywhere;
}

.row-actions {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  flex-shrink: 0;
}
</style>
