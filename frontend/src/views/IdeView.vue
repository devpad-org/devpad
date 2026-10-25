<script setup lang="ts">
import { computed, provide, ref, watch, onMounted, onUnmounted } from 'vue'
import { useRoute, useRouter, type LocationQuery } from 'vue-router'
import { workspaceApi, type Workspace } from '@/api/workspaces'
import { projectApi } from '@/api/projects'
import type { Task } from '@/api/tasks'
import FileExplorer from '@/components/ide/FileExplorer.vue'
import EditorPanel from '@/components/ide/EditorPanel.vue'
import GitGraphPanel from '@/components/ide/GitGraphPanel.vue'
import TerminalPanel from '@/components/ide/TerminalPanel.vue'
import AiAgentPanel from '@/components/ide/AiAgentPanel.vue'
import AiAgentsPanel from '@/components/ide/AiAgentsPanel.vue'
import AiAgentRunsPanel from '@/components/ide/AiAgentRunsPanel.vue'
import PreviewPanel from '@/components/ide/PreviewPanel.vue'
import PreviewNewTabModal from '@/components/ide/PreviewNewTabModal.vue'
import GitPanel from '@/components/ide/GitPanel.vue'
import GitStatusBar from '@/components/ide/GitStatusBar.vue'
import WorkspaceInfoPanel from '@/components/ide/WorkspaceInfoPanel.vue'
import WorkspaceProcessesPanel from '@/components/ide/WorkspaceProcessesPanel.vue'
import ServiceCatalogPanel from '@/components/ide/ServiceCatalogPanel.vue'
import WorkspaceServicesPanel from '@/components/ide/WorkspaceServicesPanel.vue'
import WorkspaceGate, { type WorkspaceGateState } from '@/components/ide/WorkspaceGate.vue'
import ProjectAgentsPanel from '@/components/project/ProjectAgentsPanel.vue'
import ProjectBoard from '@/components/project/ProjectBoard.vue'
import ProjectBranchActions from '@/components/project/ProjectBranchActions.vue'
import ProjectMergeRequestsPanel from '@/components/project/ProjectMergeRequestsPanel.vue'
import ProjectPanel from '@/components/project/ProjectPanel.vue'
import ProjectTasksPanel from '@/components/project/ProjectTasksPanel.vue'
import TaskModal from '@/components/project/TaskModal.vue'
import MergeRequestTab from '@/components/mergerequest/MergeRequestTab.vue'
import ConfirmModal from '@/components/ConfirmModal.vue'
import {
  BOARD_TAB,
  confirmDiscardKey,
  ideShellKey,
  mergeRequestTabId,
  parseViewTabId,
  type ViewTab,
  type ViewTabId,
} from '@/components/ide/viewTabs'
import { useServiceStore } from '@/stores/services'
import { useActiveProjectStore } from '@/stores/activeProject'
import { useAttentionStore } from '@/stores/attention'
import { useResizable } from '@/composables/useResizable'
import { useIsMobile } from '@/composables/useMediaQuery'
import { recordProjectOpened } from '@/utils/recentProjects'
import type { GitCommit, GitCommitFile } from '@/api/git'
import type { AgentRun } from '@/api/ai'

const WORKSPACE_POLL_INTERVAL_MS = 4000

const route = useRoute()
const router = useRouter()
const serviceStore = useServiceStore()
const projectStore = useActiveProjectStore()
const attention = useAttentionStore()

// The shell runs in one of two modes. At /projects/:id it is the project's
// IDE: project panels always work, and workspace panels wait for the project
// workspace to exist and run. At /workspace/:id it opens a single workspace,
// such as a standalone one or an agent's sandbox being debugged.
const projectId = computed(() => (route.name === 'project' ? Number(route.params.id) : null))
const isProjectMode = computed(() => projectId.value !== null)

const workspace = ref<Workspace | null>(null)
const loading = ref(true)
const error = ref<string | null>(null)
const workspaceAction = ref<'' | 'creating' | 'starting'>('')
const workspaceError = ref('')
// Bumped when the workspace's branch moves, to reload the git panels.
const gitRevision = ref(0)
let workspacePollTimer: ReturnType<typeof setInterval> | null = null

// Workspace panels need a running container. Outside project mode the
// workspace is started on open, as before.
const workspaceLive = computed(() =>
  !!workspace.value && (!isProjectMode.value || workspace.value.status === 'running'),
)

const gateState = computed<WorkspaceGateState>(() => {
  if (workspaceAction.value) return workspaceAction.value
  const project = projectStore.project
  if (project?.status === 'error') return 'error'
  if (!projectStore.ready) return 'not-ready'
  if (!workspace.value) return 'none'
  if (workspace.value.status === 'creating') return 'creating'
  if (workspace.value.status === 'stopped') return 'stopped'
  return 'error'
})
const gateError = computed(() =>
  workspaceError.value || (projectStore.project?.status === 'error' ? projectStore.project.error : ''),
)
const workspaceStatusLabel = computed(() => workspace.value?.status ?? 'none')

const activeFile = ref<string | null>(null)
const terminalMinimized = ref(false)
const previewVisible = ref(false)
const editorPanel = ref<InstanceType<typeof EditorPanel> | null>(null)
// The shell owns its view tabs, such as the board, and mirrors them and the
// active one into the URL so they survive reloads and back/forward.
const viewTabs = ref<ViewTab[]>([])
const activeView = ref<ViewTabId | null>(null)
// View tabs holding work that closing them would lose, such as a review draft.
const dirtyViews = ref(new Set<string>())
const selectedGitCommit = ref<GitCommit | null>(null)
const gitDiffRequest = ref<GitDiffRequest | null>(null)
const focusedAgentRunId = ref<number | null>(null)
const selectedAgentId = ref('default')
const previewModalVisible = ref(false)
const previewModalLoading = ref(false)
const previewModalError = ref<string | null>(null)
const defaultAgentSavingId = ref<string | null>(null)
const defaultAgentError = ref<string | null>(null)
let gitDiffRequestId = 0

type GitDiffRequest =
  | { kind: 'commit'; commit: GitCommit; file: GitCommitFile; requestId: number }
  | { kind: 'working'; path: string; staged: boolean; requestId: number }

type ActivityTab = 'ai' | 'explorer' | 'git' | 'info' | 'services' | ProjectActivity
type ProjectActivity = 'tasks' | 'merge-requests' | 'agents' | 'project'
const PROJECT_ACTIVITIES: ActivityTab[] = ['tasks', 'merge-requests', 'agents', 'project']
const activeActivity = ref<ActivityTab>('ai')
const serviceCount = computed(() => serviceStore.services.length)
const isProjectActivity = computed(() => PROJECT_ACTIVITIES.includes(activeActivity.value))
// The editor and its tabs, including the board, sit behind the explorer and
// the project panels.
const editorSurfaceVisible = computed(() => activeActivity.value === 'explorer' || isProjectActivity.value)

// On narrow screens the side panels become slide-over drawers and the activity
// bar moves to the bottom, leaving the primary surface the full width.
const isMobile = useIsMobile()
const mobileSidebarOpen = ref(false)
const mobileRunsOpen = ref(false)

watch(isMobile, (mobile) => {
  if (!mobile) closeMobilePanels()
})

function closeMobilePanels() {
  mobileSidebarOpen.value = false
  mobileRunsOpen.value = false
}

function selectActivity(tab: ActivityTab) {
  if (!isMobile.value) {
    activeActivity.value = tab
    return
  }
  mobileRunsOpen.value = false
  if (activeActivity.value === tab) {
    mobileSidebarOpen.value = !mobileSidebarOpen.value
    return
  }
  activeActivity.value = tab
  // The explorer surface is empty until a file is chosen, so lead with the
  // tree; the project panels are lists, so lead with those too.
  mobileSidebarOpen.value = (tab === 'explorer' && !activeFile.value) || PROJECT_ACTIVITIES.includes(tab)
}

function toggleMobileSidebar() {
  mobileRunsOpen.value = false
  mobileSidebarOpen.value = !mobileSidebarOpen.value
}

function toggleMobileRuns() {
  mobileSidebarOpen.value = false
  mobileRunsOpen.value = !mobileRunsOpen.value
}

const sidebar = useResizable({
  direction: 'horizontal',
  edge: 'left',
  initialSize: 240,
  minSize: 160,
  maxSize: 480,
})

const terminal = useResizable({
  direction: 'vertical',
  edge: 'bottom',
  initialSize: 220,
  minSize: 80,
  maxSize: 600,
})

const aiSidePanel = useResizable({
  direction: 'horizontal',
  edge: 'right',
  initialSize: 320,
  minSize: 240,
  maxSize: 600,
})

const preview = useResizable({
  direction: 'horizontal',
  edge: 'right',
  initialSize: 480,
  minSize: 240,
  maxSize: 800,
})



function handleGlobalKeydown(e: KeyboardEvent) {
  const ctrl = e.ctrlKey || e.metaKey

  if (ctrl && !e.shiftKey && !e.altKey && e.key === 's') {
    e.preventDefault()
    editorPanel.value?.saveActiveFile()
  } else if (ctrl && e.shiftKey && !e.altKey && e.key === 'S') {
    e.preventDefault()
    editorPanel.value?.saveAllFiles()
  } else if (ctrl && !e.shiftKey && !e.altKey && e.key === '`') {
    e.preventDefault()
    terminalMinimized.value = !terminalMinimized.value
  }
}

function setWorkspace(ws: Workspace | null) {
  // Services belong to one workspace; the panels refetch them for the next.
  if (workspace.value?.id !== ws?.id) serviceStore.$reset()
  workspace.value = ws
  if (ws) selectedAgentId.value = ws.defaultAgentId || 'default'
}

// Bumped by each load, so a load superseded by navigation stops quietly.
let loadSeq = 0

async function loadWorkspace(id: number, seq: number) {
  try {
    const res = await workspaceApi.get(id)
    if (seq !== loadSeq) return
    let ws = res.workspace
    // A project's own workspace opens as its project; the route watcher
    // loads it.
    if (ws.projectId && ws.kind === 'interactive') {
      void router.replace({ name: 'project', params: { id: ws.projectId }, query: route.query })
      return
    }
    // Auto-start the container if it's stopped
    if (ws.status === 'stopped') {
      const startRes = await workspaceApi.start(id)
      ws = startRes.workspace
    }
    if (seq !== loadSeq) return
    setWorkspace(ws)
  } catch {
    if (seq !== loadSeq) return
    error.value = 'Failed to load workspace'
  }
  loading.value = false
}

async function loadProject(id: number, seq: number) {
  await projectStore.open(id)
  if (seq !== loadSeq) return
  if (!projectStore.project) {
    error.value = projectStore.loadError || 'Failed to load project'
    // Nothing on the error page uses the project, so stop polling it.
    projectStore.close()
    loading.value = false
    return
  }
  recordProjectOpened(id)
  await loadProjectWorkspace(id, seq)
  if (seq !== loadSeq) return
  applyTaskQuery()
  // A conversion leaves the user something to do upstream, so its notice
  // comes first.
  activeActivity.value = projectStore.conversion ? 'project' : workspaceLive.value ? 'ai' : 'tasks'
  applyViewQuery(route.query)
  // Open files don't survive a reload, so a view takes the editor instead.
  if (!activeView.value) activeView.value = viewTabs.value[0]?.id ?? null
  // A link to a particular tab shows it.
  if (activeView.value && route.query.tab) revealView(activeView.value)
  loading.value = false
  startWorkspacePolling()
}

// Finds the project workspace without creating it.
async function loadProjectWorkspace(id: number, seq: number) {
  try {
    const pws = await projectApi.getWorkspace(id)
    if (seq !== loadSeq) return
    const ws = pws ? (await workspaceApi.get(pws.id)).workspace : null
    if (seq !== loadSeq) return
    setWorkspace(ws)
    workspaceError.value = ''
  } catch (e) {
    if (seq !== loadSeq) return
    workspaceError.value = e instanceof Error ? e.message : 'Failed to load the workspace'
  }
}

// Clears everything that belongs to the previous project or workspace, since
// both routes reuse this view.
function resetShell() {
  stopWorkspacePolling()
  setWorkspace(null)
  workspaceAction.value = ''
  workspaceError.value = ''
  gitRevision.value = 0
  activeActivity.value = 'ai'
  activeFile.value = null
  previewVisible.value = false
  selectedGitCommit.value = null
  gitDiffRequest.value = null
  focusedAgentRunId.value = null
  defaultAgentError.value = null
  viewTabs.value = []
  activeView.value = null
  dirtyViews.value = new Set()
  closeMobilePanels()
}

async function load() {
  const seq = ++loadSeq
  loading.value = true
  error.value = null
  resetShell()

  if (projectId.value !== null) {
    if (isNaN(projectId.value)) {
      router.push({ name: 'home' })
      return
    }
    await loadProject(projectId.value, seq)
    return
  }

  projectStore.close()
  const id = Number(route.params.id)
  if (isNaN(id)) {
    router.push({ name: 'home' })
    return
  }
  await loadWorkspace(id, seq)
}

// Creating and starting take a while, and the user may move to another
// project or a sandbox meanwhile; a result for a superseded load is dropped.
async function createWorkspace() {
  if (projectId.value === null || workspaceAction.value) return
  const seq = loadSeq
  workspaceAction.value = 'creating'
  workspaceError.value = ''
  try {
    const { workspace: pws } = await projectApi.openWorkspace(projectId.value)
    if (seq !== loadSeq) return
    let ws = (await workspaceApi.get(pws.id)).workspace
    if (seq !== loadSeq) return
    if (ws.status === 'stopped') {
      workspaceAction.value = 'starting'
      ws = (await workspaceApi.start(ws.id)).workspace
      if (seq !== loadSeq) return
    }
    setWorkspace(ws)
  } catch (e) {
    if (seq === loadSeq) workspaceError.value = e instanceof Error ? e.message : 'Failed to create the workspace'
  } finally {
    if (seq === loadSeq) workspaceAction.value = ''
  }
}

async function startWorkspace() {
  if (!workspace.value || workspaceAction.value) return
  const seq = loadSeq
  workspaceAction.value = 'starting'
  workspaceError.value = ''
  try {
    const ws = (await workspaceApi.start(workspace.value.id)).workspace
    if (seq === loadSeq) setWorkspace(ws)
  } catch (e) {
    if (seq === loadSeq) workspaceError.value = e instanceof Error ? e.message : 'Failed to start the workspace'
  } finally {
    if (seq === loadSeq) workspaceAction.value = ''
  }
}

// Keeps the project workspace's status current, so the panels follow it
// being created, started or stopped elsewhere.
async function pollWorkspace() {
  const id = projectId.value
  if (id === null || workspaceAction.value || document.visibilityState !== 'visible') return
  const seq = loadSeq
  try {
    const pws = await projectApi.getWorkspace(id)
    if (seq !== loadSeq || workspaceAction.value) return
    if (!pws) {
      if (workspace.value) setWorkspace(null)
    } else if (!workspace.value || workspace.value.id !== pws.id || workspace.value.status !== pws.status) {
      const ws = (await workspaceApi.get(pws.id)).workspace
      if (seq !== loadSeq || workspaceAction.value) return
      setWorkspace(ws)
    }
  } catch {
    // Keep the last known state; the next poll tries again.
  }
}

function startWorkspacePolling() {
  stopWorkspacePolling()
  workspacePollTimer = setInterval(() => void pollWorkspace(), WORKSPACE_POLL_INTERVAL_MS)
}

function stopWorkspacePolling() {
  if (workspacePollTimer !== null) {
    clearInterval(workspacePollTimer)
    workspacePollTimer = null
  }
}

const MR_TAB_TITLE_LENGTH = 28

// The name of a view tab the shell can render, or null for one it can't. A
// merge request tab is named after its title once the project's list has it.
function viewTabName(id: ViewTabId): string | null {
  if (id === BOARD_TAB) return 'Board'
  const mrId = mergeRequestTabId(id)
  if (mrId === null) return null
  const title = projectStore.mergeRequests.find((m) => m.id === mrId)?.title
  if (!title) return `!${mrId}`
  const short = title.length > MR_TAB_TITLE_LENGTH ? `${title.slice(0, MR_TAB_TITLE_LENGTH - 1)}…` : title
  return `!${mrId} ${short}`
}

// The open view tabs with their current names, which follow renames.
const namedViewTabs = computed(() => viewTabs.value.map((t) => ({ id: t.id, name: viewTabName(t.id) ?? t.name })))

// Opens a view tab, or focuses it if it is already open, and shows it.
function openView(id: ViewTabId) {
  if (!viewTabs.value.some((t) => t.id === id)) {
    const name = viewTabName(id)
    if (!name) return
    viewTabs.value = [...viewTabs.value, { id, name }]
  }
  activeView.value = id
  revealView(id)
}

async function closeView(id: string) {
  if (dirtyViews.value.has(id)) {
    const ok = await askConfirm({
      title: 'Discard your review?',
      message: 'This merge request has a review you haven\'t submitted. Close it and lose the summary and line comments?',
      confirmLabel: 'Discard review',
    })
    if (!ok) return
  }
  setViewDirty(id, false)
  viewTabs.value = viewTabs.value.filter((t) => t.id !== id)
  if (activeView.value === id) activeView.value = null
}

function setViewDirty(id: string, dirty: boolean) {
  if (dirty === dirtyViews.value.has(id)) return
  const next = new Set(dirtyViews.value)
  if (dirty) next.add(id)
  else next.delete(id)
  dirtyViews.value = next
}

// A tab can close without closeView, when the URL's tab list changes, e.g.
// on back/forward. The leave guard has asked about any draft by then, so the
// tab's dirty mark just goes with it.
watch(viewTabs, (tabs) => {
  const open = new Set<string>(tabs.map((t) => t.id))
  for (const id of dirtyViews.value) {
    if (!open.has(id)) setViewDirty(id, false)
  }
})

// Something in the shell would be lost by leaving it: unsaved files or a
// review draft in a merge request tab.
const hasUnsavedWork = computed(() => !!editorPanel.value?.hasDirtyFiles || dirtyViews.value.size > 0)

function openFile(path: string) {
  activeFile.value = path
  mobileSidebarOpen.value = false
}

// View tabs sit on the editor surface, behind the project panels.
function revealView(id: ViewTabId) {
  if (!editorSurfaceVisible.value) activeActivity.value = id.startsWith('mr:') ? 'merge-requests' : 'tasks'
  mobileSidebarOpen.value = false
}

function showBoard() {
  openView(BOARD_TAB)
}

// The query holds the open view tabs and the active one, as
// ?tabs=board,mr:12&tab=mr:12. Without ?tabs the project opens on its board;
// an empty ?tabs= means every view tab was closed.
const DEFAULT_VIEWS: ViewTabId[] = [BOARD_TAB]

function queryViews(query: LocationQuery): { ids: ViewTabId[]; active: ViewTabId | null } {
  if (typeof query.tabs !== 'string') return { ids: DEFAULT_VIEWS, active: DEFAULT_VIEWS[0] ?? null }
  const ids: ViewTabId[] = []
  for (const part of query.tabs.split(',')) {
    const id = parseViewTabId(part)
    if (id && viewTabName(id) && !ids.includes(id)) ids.push(id)
  }
  const active = parseViewTabId(query.tab)
  return { ids, active: active && ids.includes(active) ? active : null }
}

function applyViewQuery(query: LocationQuery) {
  const { ids, active } = queryViews(query)
  const current = viewTabs.value.map((t) => t.id)
  if (ids.join() !== current.join()) {
    viewTabs.value = ids.map((id) => ({ id, name: viewTabName(id) ?? id }))
  }
  if (active && active !== activeView.value) activeView.value = active
}

function viewQuery(): { tabs?: string; tab?: string } {
  const ids = viewTabs.value.map((t) => t.id)
  if (ids.join() === DEFAULT_VIEWS.join() && activeView.value === DEFAULT_VIEWS[0]) return {}
  return { tabs: ids.join(','), tab: activeView.value ?? undefined }
}

watch([viewTabs, activeView], () => {
  if (!isProjectMode.value || loading.value) return
  const next = viewQuery()
  if (next.tabs === route.query.tabs && next.tab === route.query.tab) return
  void router.replace({ query: { ...route.query, tabs: next.tabs, tab: next.tab } })
})

// Back/forward and links within the project change only the query.
watch(() => [route.query.tabs, route.query.tab], () => {
  if (!isProjectMode.value || loading.value) return
  applyViewQuery(route.query)
  if (activeView.value && route.query.tab) revealView(activeView.value)
})

// ?task=<id> opens that task over the project, e.g. from a merge request's
// task link or the launcher's inbox.
function applyTaskQuery() {
  const task = Number(route.query.task)
  if (task) projectStore.selectedTaskId = task
}

function closeTask() {
  projectStore.selectedTaskId = null
  if (route.query.task) void router.replace({ query: { ...route.query, task: undefined } })
}

function taskChanged(task: Task) {
  projectStore.upsertTask(task)
  void projectStore.refresh()
}

function taskDeleted(id: number) {
  projectStore.removeTask(id)
  closeTask()
  void projectStore.refresh()
}

// A checkout moves HEAD: the git panels start over and open files reload
// from the new branch. Publish and sync move only refs, which the panels pick
// up on their own refresh.
function branchChanged(kind: 'checkout' | 'publish' | 'sync') {
  if (kind !== 'checkout') return
  gitRevision.value++
  void editorPanel.value?.reloadFiles()
}

function checkedOut() {
  branchChanged('checkout')
  openGitPanel()
}

// Work that an action would throw away, such as unsaved editor changes or a
// review draft, is only dropped once the user confirms.
interface DiscardPrompt {
  title: string
  message: string
  confirmLabel: string
}

const discardPrompt = ref<(DiscardPrompt & { resolve: (ok: boolean) => void }) | null>(null)

function askConfirm(prompt: DiscardPrompt): Promise<boolean> {
  discardPrompt.value?.resolve(false)
  return new Promise((resolve) => {
    discardPrompt.value = { ...prompt, resolve }
  })
}

const UNSAVED_FILES = { title: 'Discard unsaved changes?', confirmLabel: 'Discard changes' }

// Reloading files after a checkout loses only unsaved editor changes.
function confirmDiscard(message: string): Promise<boolean> {
  if (!editorPanel.value?.hasDirtyFiles) return Promise.resolve(true)
  return askConfirm({ ...UNSAVED_FILES, message })
}

// Leaving the shell also loses review drafts in merge request tabs.
function confirmLeave(): Promise<boolean> {
  if (!hasUnsavedWork.value) return Promise.resolve(true)
  const files = !!editorPanel.value?.hasDirtyFiles
  const reviews = dirtyViews.value.size > 0
  if (files && !reviews) {
    return askConfirm({ ...UNSAVED_FILES, message: 'You have unsaved changes in open files. Leave and lose them?' })
  }
  return askConfirm({
    title: files ? 'Discard unsaved work?' : 'Discard your review?',
    message: files
      ? 'You have unsaved changes in open files and a review you haven\'t submitted. Leave and lose them?'
      : 'You have a merge request review you haven\'t submitted. Leave and lose it?',
    confirmLabel: 'Discard and leave',
  })
}

// A navigation within the shell changes only the query, but a new tab list
// can still close merge request tabs that hold a review draft.
function confirmDropViews(query: LocationQuery): boolean | Promise<boolean> {
  if (!isProjectMode.value || dirtyViews.value.size === 0) return true
  const kept = new Set<string>(queryViews(query).ids)
  if ([...dirtyViews.value].every((id) => kept.has(id))) return true
  return askConfirm({
    title: 'Discard your review?',
    message: 'This closes a merge request with a review you haven\'t submitted. Close it and lose the summary and line comments?',
    confirmLabel: 'Discard review',
  })
}

function answerDiscard(ok: boolean) {
  const prompt = discardPrompt.value
  discardPrompt.value = null
  prompt?.resolve(ok)
}

provide(confirmDiscardKey, confirmDiscard)
provide(ideShellKey, {
  openView,
  openFile,
  activeView,
  liveWorkspaceId: computed(() => (isProjectMode.value && workspaceLive.value ? workspace.value?.id ?? null : null)),
})

function handleBeforeUnload(e: BeforeUnloadEvent) {
  if (!hasUnsavedWork.value) return
  e.preventDefault()
  e.returnValue = ''
}

// Both routes share this view, and in-component route guards stay bound to
// the route the view first mounted for, so a global guard covers leaving.
let removeLeaveGuard: (() => void) | null = null

// Both routes render this view, so moving between them reuses it.
watch(() => [route.name, route.params.id], (next, prev) => {
  const [name, id] = next
  if (name !== 'project' && name !== 'ide') return
  if (name !== prev[0] || id !== prev[1]) void load()
})
watch(() => route.query.task, () => {
  if (isProjectMode.value) applyTaskQuery()
})

onMounted(() => {
  window.addEventListener('keydown', handleGlobalKeydown)
  window.addEventListener('beforeunload', handleBeforeUnload)
  removeLeaveGuard = router.beforeEach((to, from) => {
    if (to.name === from.name && to.params.id === from.params.id) return confirmDropViews(to.query)
    return confirmLeave()
  })
  if (isMobile.value) terminalMinimized.value = true
  void load()
})

onUnmounted(() => {
  // Ends any load still in flight, so it can't start polling afterwards.
  loadSeq++
  window.removeEventListener('keydown', handleGlobalKeydown)
  window.removeEventListener('beforeunload', handleBeforeUnload)
  removeLeaveGuard?.()
  answerDiscard(false)
  stopWorkspacePolling()
  projectStore.close()
})

function handleFileSelect(path: string) {
  activeFile.value = path
  mobileSidebarOpen.value = false
}

function handleGitCommitSelect(commit: GitCommit) {
  activeActivity.value = 'git'
  selectedGitCommit.value = commit
}

function handleGitCommitFileSelect(payload: { commit: GitCommit; file: GitCommitFile }) {
  selectedGitCommit.value = payload.commit
  mobileSidebarOpen.value = false
  gitDiffRequest.value = {
    kind: 'commit',
    ...payload,
    requestId: ++gitDiffRequestId,
  }
}

function handleGitWorkingFileSelect(payload: { path: string; staged: boolean }) {
  selectedGitCommit.value = null
  mobileSidebarOpen.value = false
  gitDiffRequest.value = {
    kind: 'working',
    ...payload,
    requestId: ++gitDiffRequestId,
  }
}

function handleAgentRunFocus(run: AgentRun) {
  focusedAgentRunId.value = run.id
  activeActivity.value = 'ai'
  closeMobilePanels()
}

function handleAgentSelect(agentId: string) {
  selectedAgentId.value = agentId
  focusedAgentRunId.value = null
  mobileSidebarOpen.value = false
}

async function handleDefaultAgentSet(agentId: string) {
  if (!workspace.value) return
  if ((workspace.value.defaultAgentId || 'default') === agentId) {
    handleAgentSelect(agentId)
    return
  }

  const previousAgentId = workspace.value.defaultAgentId || 'default'
  defaultAgentSavingId.value = agentId
  defaultAgentError.value = null
  selectedAgentId.value = agentId
  focusedAgentRunId.value = null
  try {
    const res = await workspaceApi.setDefaultAgent(workspace.value.id, agentId)
    workspace.value = res.workspace
    selectedAgentId.value = res.workspace.defaultAgentId || 'default'
  } catch (e) {
    selectedAgentId.value = previousAgentId
    defaultAgentError.value = e instanceof Error ? e.message : 'Failed to set default agent'
  } finally {
    defaultAgentSavingId.value = null
  }
}

function clearSelectedGitCommit() {
  selectedGitCommit.value = null
  gitDiffRequest.value = null
}

function closeGitDiff() {
  gitDiffRequest.value = null
}

function showPreviewNewTabModal() {
  previewModalError.value = null
  previewModalVisible.value = true
}

function closePreviewNewTabModal() {
  if (previewModalLoading.value) return

  previewModalVisible.value = false
  previewModalError.value = null
}

async function openPreviewNewTab(port: number) {
  previewModalLoading.value = true
  previewModalError.value = null

  const previewWindow = window.open('about:blank', '_blank')
  if (!previewWindow) {
    previewModalLoading.value = false
    previewModalError.value = 'Your browser blocked the preview tab. Allow pop-ups for Devpad and try again.'
    return
  }

  previewWindow.opener = null

  try {
    const url = await workspaceApi.getPreviewURL(workspace.value!.id, port)
    previewWindow.location.href = url
    previewModalVisible.value = false
  } catch (e) {
    previewWindow.close()
    previewModalError.value = e instanceof Error ? e.message : 'Failed to open preview'
  } finally {
    previewModalLoading.value = false
  }
}

function openGitPanel() {
  activeActivity.value = 'git'
  if (isMobile.value) {
    mobileRunsOpen.value = false
    mobileSidebarOpen.value = true
  }
}

// A project goes back to the projects list, and an agent's sandbox to its
// project.
function handleBack() {
  if (isProjectMode.value) {
    router.push({ name: 'home' })
    return
  }
  const sandboxProject = workspace.value?.projectId
  if (sandboxProject) router.push({ name: 'project', params: { id: sandboxProject } })
  else router.push({ name: 'home' })
}

const backLabel = computed(() => {
  if (isProjectMode.value) return 'Back to projects'
  return workspace.value?.projectId ? 'Back to project' : 'Back to projects'
})
</script>

<template>
  <div v-if="loading" class="ide-loading">
    <div class="ide-spinner" />
    <span>{{ isProjectMode ? 'Loading project…' : 'Loading workspace…' }}</span>
  </div>
  <div v-else-if="error" class="ide-error">
    <p>{{ error }}</p>
    <div class="ide-error-actions">
      <button class="ide-back-btn" @click="load">Retry</button>
      <button class="ide-back-btn" @click="handleBack">{{ backLabel }}</button>
    </div>
  </div>
  <div v-else class="ide-layout" :class="{ 'is-mobile': isMobile }">
    <!--
      IDE panel IDs:
      - ide-toolbar: top toolbar/title bar
      - ide-activity-bar: far-left activity switcher
      - ide-left-panel: left side panel next to the activity bar
      - ide-primary-surface: AI chat, editor, or git graph surface
      - ide-right-panel: far-right side panel region
      - ide-terminal-panel: bottom terminal panel
      - ide-status-bar: bottom status bar (declared in GitStatusBar.vue)
    -->
    <div id="ide-toolbar" class="ide-titlebar" role="toolbar" aria-label="IDE toolbar">
      <button class="titlebar-back" @click="handleBack" :title="backLabel">
        <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round">
          <path d="m15 18-6-6 6-6" />
        </svg>
      </button>
      <button
        v-if="isMobile"
        class="titlebar-toggle"
        :class="{ active: mobileSidebarOpen }"
        @click="toggleMobileSidebar"
        title="Toggle side panel"
        aria-label="Toggle side panel"
        :aria-expanded="mobileSidebarOpen"
      >
        <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round">
          <rect width="18" height="18" x="3" y="3" rx="2" />
          <path d="M9 3v18" />
        </svg>
      </button>
      <span class="titlebar-name">{{ isProjectMode ? projectStore.project?.name : workspace?.name }}</span>
      <span
        class="titlebar-status"
        :class="`status-${workspaceStatusLabel}`"
        :title="isProjectMode ? `Workspace ${workspaceStatusLabel === 'none' ? 'not created' : workspaceStatusLabel}` : workspaceStatusLabel"
      >
        <span class="status-dot" />
        <span class="titlebar-status-text">{{ workspaceStatusLabel === 'none' ? 'no workspace' : workspaceStatusLabel }}</span>
      </span>
      <div class="titlebar-actions">
        <RouterLink
          :to="{ name: 'home', hash: '#inbox' }"
          class="titlebar-btn titlebar-inbox"
          :class="{ 'has-items': attention.count > 0 }"
          :title="attention.count > 0 ? `Inbox: ${attention.count} waiting for you` : 'Inbox'"
          :aria-label="attention.count > 0 ? `Inbox, ${attention.count} waiting for you` : 'Inbox'"
        >
          <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round">
            <path d="M22 12h-6l-2 3h-4l-2-3H2" />
            <path d="M5.45 5.11 2 12v6a2 2 0 0 0 2 2h16a2 2 0 0 0 2-2v-6l-3.45-6.89A2 2 0 0 0 16.76 4H7.24a2 2 0 0 0-1.79 1.11z" />
          </svg>
          <span v-if="attention.count > 0" class="titlebar-badge">{{ attention.count }}</span>
        </RouterLink>
      <template v-if="workspaceLive">
        <div class="titlebar-separator" />
        <button
          class="titlebar-btn"
          :disabled="!editorPanel?.isDirty"
          @click="editorPanel?.saveActiveFile()"
          title="Save (Ctrl+S)"
        >
          <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round">
            <path d="M15.2 3a2 2 0 0 1 1.4.6l3.8 3.8a2 2 0 0 1 .6 1.4V19a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2z" />
            <path d="M17 21v-7a1 1 0 0 0-1-1H8a1 1 0 0 0-1 1v7" />
            <path d="M7 3v4a1 1 0 0 0 1 1h7" />
          </svg>
        </button>
        <button
          class="titlebar-btn"
          :disabled="!editorPanel?.hasDirtyFiles"
          @click="editorPanel?.saveAllFiles()"
          title="Save All (Ctrl+Alt+S)"
        >
          <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round">
            <path d="M15.2 3a2 2 0 0 1 1.4.6l3.8 3.8a2 2 0 0 1 .6 1.4V19a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2z" />
            <path d="M17 21v-7a1 1 0 0 0-1-1H8a1 1 0 0 0-1 1v7" />
            <path d="M7 3v4a1 1 0 0 0 1 1h7" />
          </svg>
          <svg width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="3" stroke-linecap="round" stroke-linejoin="round" class="save-all-badge">
            <path d="M12 5v14" />
            <path d="M5 12h14" />
          </svg>
        </button>
        <div class="titlebar-separator" />
        <button
          class="titlebar-toggle"
          :class="{ active: previewVisible }"
          @click="previewVisible = !previewVisible"
          title="Toggle preview panel"
        >
          <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round">
            <rect width="20" height="14" x="2" y="3" rx="2" />
            <path d="M8 21h8" />
            <path d="M12 17v4" />
          </svg>
        </button>
        <button
          class="titlebar-btn"
          @click="showPreviewNewTabModal"
          title="Open preview in new tab"
        >
          <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round">
            <path d="M15 3h6v6" />
            <path d="M10 14 21 3" />
            <path d="M18 13v6a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V8a2 2 0 0 1 2-2h6" />
          </svg>
        </button>
        <button
          v-if="isMobile"
          class="titlebar-toggle"
          :class="{ active: mobileRunsOpen }"
          @click="toggleMobileRuns"
          title="Agent runs"
          aria-label="Agent runs"
          :aria-expanded="mobileRunsOpen"
        >
          <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round">
            <path d="M3 12h4l3-9 4 18 3-9h4" />
          </svg>
        </button>
      </template>
      </div>
    </div>

    <!-- Main IDE area -->
    <div class="ide-body">
      <nav id="ide-activity-bar" class="activity-bar" aria-label="Activity bar">
        <button
          class="activity-btn activity-btn--ai"
          :class="{ active: activeActivity === 'ai' }"
          @click="selectActivity('ai')"
          title="AI"
        >
          <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round">
            <path d="m12 3-1.912 5.813a2 2 0 0 1-1.275 1.275L3 12l5.813 1.912a2 2 0 0 1 1.275 1.275L12 21l1.912-5.813a2 2 0 0 1 1.275-1.275L21 12l-5.813-1.912a2 2 0 0 1-1.275-1.275L12 3Z" />
            <path d="M5 3v4" />
            <path d="M19 17v4" />
            <path d="M3 5h4" />
            <path d="M17 19h4" />
          </svg>
        </button>
        <button
          class="activity-btn activity-btn--explorer"
          :class="{ active: activeActivity === 'explorer' }"
          @click="selectActivity('explorer')"
          title="Explorer"
        >
          <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round">
            <path d="M4 4h5l2 2h8a2 2 0 0 1 2 2v10a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2V6a2 2 0 0 1 2-2Z" />
          </svg>
        </button>
        <button
          class="activity-btn activity-btn--git"
          :class="{ active: activeActivity === 'git' }"
          @click="selectActivity('git')"
          title="Source Control"
        >
          <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round">
            <circle cx="18" cy="18" r="3" />
            <circle cx="6" cy="6" r="3" />
            <path d="M6 21V9a9 9 0 0 0 9 9" />
          </svg>
        </button>
        <template v-if="isProjectMode">
          <div class="activity-separator" aria-hidden="true" />
          <button
            class="activity-btn activity-btn--tasks"
            :class="{ active: activeActivity === 'tasks' }"
            @click="selectActivity('tasks')"
            title="Tasks"
          >
            <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round">
              <path d="m3 17 2 2 4-4" />
              <path d="m3 7 2 2 4-4" />
              <path d="M13 6h8" />
              <path d="M13 12h8" />
              <path d="M13 18h8" />
            </svg>
          </button>
          <button
            class="activity-btn activity-btn--merge-requests"
            :class="{ active: activeActivity === 'merge-requests' }"
            @click="selectActivity('merge-requests')"
            :title="projectStore.openMergeRequests.length > 0 ? `Merge requests (${projectStore.openMergeRequests.length} open)` : 'Merge requests'"
          >
            <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round">
              <circle cx="18" cy="18" r="3" />
              <circle cx="6" cy="6" r="3" />
              <path d="M13 6h3a2 2 0 0 1 2 2v7" />
              <path d="M6 9v12" />
            </svg>
            <span
              v-if="projectStore.openMergeRequests.length > 0"
              class="activity-badge"
              :aria-label="`${projectStore.openMergeRequests.length} open merge requests`"
            >
              {{ projectStore.openMergeRequests.length }}
            </span>
          </button>
          <button
            class="activity-btn activity-btn--agents"
            :class="{ active: activeActivity === 'agents' }"
            @click="selectActivity('agents')"
            :title="projectStore.activeSandboxes > 0 ? `Agents (${projectStore.activeSandboxes} running)` : 'Agents'"
          >
            <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round">
              <path d="M12 8V4H8" />
              <rect width="16" height="12" x="4" y="8" rx="2" />
              <path d="M2 14h2" />
              <path d="M20 14h2" />
              <path d="M15 13v2" />
              <path d="M9 13v2" />
            </svg>
            <span
              v-if="projectStore.activeSandboxes > 0"
              class="activity-badge"
              :aria-label="`${projectStore.activeSandboxes} sandboxes running`"
            >
              {{ projectStore.activeSandboxes }}
            </span>
          </button>
          <div class="activity-separator" aria-hidden="true" />
        </template>
        <button
          class="activity-btn activity-btn--info"
          :class="{ active: activeActivity === 'info' }"
          @click="selectActivity('info')"
          title="Workspace Info"
        >
          <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round">
            <circle cx="12" cy="12" r="10" />
            <path d="M12 16v-4" />
            <path d="M12 8h.01" />
          </svg>
        </button>
        <button
          class="activity-btn activity-btn--services"
          :class="{ active: activeActivity === 'services' }"
          @click="selectActivity('services')"
          :title="serviceCount > 0 ? `Database Services (${serviceCount})` : 'Database Services'"
        >
          <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round">
            <ellipse cx="12" cy="5" rx="9" ry="3" />
            <path d="M3 5v14a9 3 0 0 0 18 0V5" />
            <path d="M3 12a9 3 0 0 0 18 0" />
          </svg>
          <span
            v-if="serviceCount > 0"
            class="activity-badge"
            :aria-label="`${serviceCount} services attached`"
          >
            {{ serviceCount }}
          </span>
        </button>
        <button
          v-if="isProjectMode"
          class="activity-btn activity-btn--project"
          :class="{ active: activeActivity === 'project' }"
          @click="selectActivity('project')"
          :title="projectStore.conversion ? 'Project (conversion notice)' : 'Project'"
        >
          <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round">
            <path d="M20 20a2 2 0 0 0 2-2V8a2 2 0 0 0-2-2h-7.9a2 2 0 0 1-1.69-.9L9.6 3.9A2 2 0 0 0 7.93 3H4a2 2 0 0 0-2 2v13a2 2 0 0 0 2 2Z" />
          </svg>
          <span v-if="projectStore.conversion" class="activity-badge" aria-label="Conversion notice">!</span>
        </button>
      </nav>

      <div
        v-if="isMobile && (mobileSidebarOpen || mobileRunsOpen)"
        class="mobile-backdrop"
        aria-hidden="true"
        @click="closeMobilePanels"
      />

      <aside
        id="ide-left-panel"
        class="ide-sidebar"
        :class="{ open: mobileSidebarOpen }"
        aria-label="Left side panel"
        :style="isMobile ? undefined : { width: sidebar.size.value + 'px' }"
      >
        <template v-if="workspaceLive">
          <AiAgentsPanel
            v-show="activeActivity === 'ai'"
            :workspace-id="workspace?.id ?? 0"
            :selected-agent-id="selectedAgentId"
            :default-agent-id="workspace?.defaultAgentId ?? 'default'"
            :default-agent-error="defaultAgentError"
            :default-agent-saving-id="defaultAgentSavingId"
            @select="handleAgentSelect"
            @set-default="handleDefaultAgentSet"
          />
          <FileExplorer
            v-show="activeActivity === 'explorer'"
            :workspace-id="workspace?.id ?? 0"
            :workspace-name="workspace?.name ?? ''"
            @select="handleFileSelect"
          />
          <div v-show="activeActivity === 'git'" class="git-sidebar">
            <ProjectBranchActions
              v-if="isProjectMode"
              :workspace-id="workspace?.id ?? 0"
              :active="activeActivity === 'git'"
              :revision="gitRevision"
              @changed="branchChanged"
            />
            <GitPanel
              :key="`git-${gitRevision}`"
              class="git-sidebar-panel"
              :workspace-id="workspace?.id ?? 0"
              :selected-commit="selectedGitCommit"
              @commit-file-select="handleGitCommitFileSelect"
              @working-file-select="handleGitWorkingFileSelect"
              @clear-commit="clearSelectedGitCommit"
            />
          </div>
          <WorkspaceInfoPanel
            v-show="activeActivity === 'info'"
            :workspace-id="workspace?.id ?? 0"
          />
          <ServiceCatalogPanel
            v-show="activeActivity === 'services'"
            :workspace-id="workspace?.id ?? 0"
          />
        </template>
        <WorkspaceGate
          v-else-if="!isProjectActivity"
          compact
          :state="gateState"
          :error="gateError"
          :default-branch="projectStore.project?.defaultBranch"
          @create="createWorkspace"
          @start="startWorkspace"
        />
        <template v-if="isProjectMode">
          <ProjectTasksPanel v-show="activeActivity === 'tasks'" @open-board="showBoard" />
          <ProjectMergeRequestsPanel v-show="activeActivity === 'merge-requests'" />
          <ProjectAgentsPanel
            v-show="activeActivity === 'agents'"
            :workspace-live="workspaceLive"
            :active="activeActivity === 'agents'"
            @checked-out="checkedOut"
          />
          <ProjectPanel v-show="activeActivity === 'project'" :workspace-status="workspaceStatusLabel" />
        </template>
      </aside>
      <div
        class="resize-handle resize-handle--horizontal"
        :class="{ active: sidebar.isDragging.value }"
        @pointerdown="sidebar.onPointerDown"
      />

      <!-- Center + Bottom -->
      <div class="ide-center">
        <section id="ide-primary-surface" class="ide-editor-area" aria-label="Primary IDE surface">
          <div v-show="activeActivity === 'ai'" class="ide-surface-pane ide-ai-surface">
            <AiAgentPanel
              v-if="workspaceLive"
              :workspace-id="workspace?.id ?? 0"
              :selected-agent-id="selectedAgentId"
              :focused-run-id="focusedAgentRunId"
              @clear-focused-run="focusedAgentRunId = null"
              @select-agent="handleAgentSelect"
            />
            <WorkspaceGate
              v-else
              :state="gateState"
              :error="gateError"
              :default-branch="projectStore.project?.defaultBranch"
              @create="createWorkspace"
              @start="startWorkspace"
            />
          </div>
          <div v-show="editorSurfaceVisible" class="ide-surface-pane">
            <EditorPanel
              ref="editorPanel"
              v-model:active-view="activeView"
              :workspace-id="workspace?.id ?? 0"
              :file-path="activeFile"
              :views="namedViewTabs"
              @active-change="(p: string | null) => activeFile = p"
              @close-view="closeView"
            >
              <!-- The slot's `active` says the tab is showing; with
                   editorSurfaceVisible it tells a view when it can pause. -->
              <template #view="{ id, active }">
                <ProjectBoard v-if="id === BOARD_TAB" />
                <MergeRequestTab
                  v-else-if="projectId !== null && mergeRequestTabId(id) !== null"
                  :project-id="projectId"
                  :mr-id="mergeRequestTabId(id)!"
                  :active="active && editorSurfaceVisible"
                  @dirty="(dirty: boolean) => setViewDirty(id, dirty)"
                />
              </template>
              <template #view-icon="{ id }">
                <svg v-if="id === BOARD_TAB" width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                  <rect width="18" height="18" x="3" y="3" rx="2" />
                  <path d="M9 3v18" />
                  <path d="M15 3v18" />
                </svg>
                <svg v-else-if="mergeRequestTabId(id) !== null" width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                  <circle cx="18" cy="18" r="3" />
                  <circle cx="6" cy="6" r="3" />
                  <path d="M13 6h3a2 2 0 0 1 2 2v7" />
                  <path d="M6 9v12" />
                </svg>
              </template>
            </EditorPanel>
          </div>
          <template v-if="workspaceLive">
            <div v-show="activeActivity === 'info'" class="ide-surface-pane">
              <WorkspaceProcessesPanel
                :workspace-id="workspace?.id ?? 0"
                :active="activeActivity === 'info'"
              />
            </div>
            <div v-show="activeActivity === 'git'" class="ide-surface-pane">
              <GitGraphPanel
                :key="`graph-${gitRevision}`"
                :workspace-id="workspace?.id ?? 0"
                :active="activeActivity === 'git'"
                :selected-commit-hash="selectedGitCommit?.hash ?? null"
                :diff-request="gitDiffRequest"
                @commit-select="handleGitCommitSelect"
                @close-diff="closeGitDiff"
              />
            </div>
            <div v-show="activeActivity === 'services'" class="ide-surface-pane">
              <WorkspaceServicesPanel :workspace-id="workspace?.id ?? 0" />
            </div>
          </template>
          <div
            v-else-if="activeActivity === 'info' || activeActivity === 'git' || activeActivity === 'services'"
            class="ide-surface-pane"
          >
            <WorkspaceGate
              :state="gateState"
              :error="gateError"
              :default-branch="projectStore.project?.defaultBranch"
              @create="createWorkspace"
              @start="startWorkspace"
            />
          </div>
        </section>
        <template v-if="workspaceLive">
        <div
          class="resize-handle resize-handle--vertical"
          :class="{ active: terminal.isDragging.value, hidden: terminalMinimized }"
          @pointerdown="terminal.onPointerDown"
        />
        <div
          id="ide-terminal-panel"
          class="ide-terminal-area"
          role="region"
          aria-label="Terminal panel"
          :class="{ minimized: terminalMinimized }"
          :style="terminalMinimized ? {} : { height: terminal.size.value + 'px' }"
        >
          <TerminalPanel
            :workspace-id="workspace?.id ?? 0"
            :minimized="terminalMinimized"
            @toggle-minimize="terminalMinimized = !terminalMinimized"
          />
        </div>
        </template>
      </div>

      <div v-if="workspaceLive" id="ide-right-panel" class="ide-right-panel" role="region" aria-label="Right side panel">
        <!-- Right panel: Preview -->
        <template v-if="previewVisible">
          <div
            class="resize-handle resize-handle--horizontal"
            :class="{ active: preview.isDragging.value }"
            @pointerdown="preview.onPointerDown"
          />
          <aside class="ide-preview" :style="isMobile ? undefined : { width: preview.size.value + 'px' }">
            <PreviewPanel
              :workspace-id="workspace?.id ?? 0"
              @close="previewVisible = false"
            />
          </aside>
        </template>

        <div
          class="resize-handle resize-handle--horizontal"
          :class="{ active: aiSidePanel.isDragging.value }"
          @pointerdown="aiSidePanel.onPointerDown"
        />
        <aside
          class="ide-ai-side-panel"
          :class="{ open: mobileRunsOpen }"
          :style="isMobile ? undefined : { width: aiSidePanel.size.value + 'px' }"
          aria-label="AI agent runs"
        >
          <AiAgentRunsPanel
            :workspace-id="workspace?.id ?? 0"
            :selected-run-id="focusedAgentRunId"
            @focus="handleAgentRunFocus"
          />
        </aside>
      </div>
    </div>
    <GitStatusBar
      v-if="workspaceLive"
      :key="`status-${gitRevision}`"
      :workspace-id="workspace?.id ?? 0"
      @open-git="openGitPanel"
    />
    <TaskModal
      v-if="isProjectMode && projectId !== null"
      :task="projectStore.selectedTask"
      :project-id="projectId"
      :merge-request="projectStore.selectedTask ? projectStore.mergeRequestByTask.get(projectStore.selectedTask.id) : undefined"
      @close="closeTask"
      @changed="taskChanged"
      @deleted="taskDeleted"
    />
    <ConfirmModal
      :show="!!discardPrompt"
      :title="discardPrompt?.title ?? ''"
      :message="discardPrompt?.message ?? ''"
      :confirm-label="discardPrompt?.confirmLabel ?? ''"
      cancel-label="Keep editing"
      variant="danger"
      @confirm="answerDiscard(true)"
      @cancel="answerDiscard(false)"
    />
    <PreviewNewTabModal
      :show="previewModalVisible"
      :loading="previewModalLoading"
      :error="previewModalError"
      @open="openPreviewNewTab"
      @cancel="closePreviewNewTabModal"
    />
  </div>
</template>

<style scoped>
.ide-loading {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  height: 100%;
  gap: var(--space-3);
  color: var(--text-secondary);
  font-size: 0.85rem;
}

.ide-spinner {
  width: 24px;
  height: 24px;
  border: 2px solid var(--border-default);
  border-top-color: var(--accent-blue);
  border-radius: 50%;
  animation: spin 0.8s linear infinite;
}

.ide-error {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  height: 100%;
  gap: var(--space-3);
  color: var(--accent-rose);
  font-size: 0.85rem;
}

.ide-error-actions {
  display: flex;
  gap: var(--space-2);
}

.ide-back-btn {
  padding: var(--space-2) var(--space-4);
  background: var(--bg-surface);
  color: var(--text-primary);
  border: 0.5px solid var(--border-default);
  border-radius: var(--radius-md);
  font-size: 0.8rem;
  transition: all var(--transition-fast);
}

.ide-back-btn:hover {
  border-color: var(--border-active);
  background: var(--bg-hover);
}

.ide-layout {
  display: flex;
  flex-direction: column;
  height: 100%;
  overflow: hidden;
}

/* Title bar */
.ide-titlebar {
  display: flex;
  align-items: center;
  gap: var(--space-3);
  padding: var(--space-1) var(--space-3);
  background: var(--bg-surface);
  border-bottom: 0.5px solid var(--border-default);
  flex-shrink: 0;
  min-height: 36px;
}

.titlebar-back {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 28px;
  height: 28px;
  border-radius: var(--radius-md);
  color: var(--text-secondary);
  transition: all var(--transition-fast);
}

.titlebar-back:hover {
  background: var(--bg-hover);
  color: var(--text-primary);
}

.titlebar-name {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: 0.85rem;
  font-weight: 600;
  color: var(--text-primary);
}

.titlebar-status {
  flex-shrink: 0;
  display: flex;
  align-items: center;
  gap: 6px;
  font-size: 0.75rem;
  text-transform: capitalize;
  color: var(--text-muted);
}

.status-dot {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: var(--text-muted);
}

.status-running .status-dot {
  background: var(--accent-green);
  box-shadow: 0 0 6px var(--accent-green);
}

.status-creating .status-dot {
  background: var(--accent-amber);
  box-shadow: 0 0 6px var(--accent-amber);
}

.titlebar-actions {
  flex-shrink: 0;
  margin-left: auto;
  display: flex;
  align-items: center;
  gap: var(--space-1);
}

.titlebar-btn {
  position: relative;
  display: flex;
  align-items: center;
  justify-content: center;
  width: 28px;
  height: 28px;
  border-radius: var(--radius-md);
  color: var(--text-secondary);
  transition: all var(--transition-fast);
}

.titlebar-btn:hover:not(:disabled) {
  background: var(--bg-hover);
  color: var(--text-primary);
}

.titlebar-btn:disabled {
  color: var(--text-muted);
  opacity: 0.4;
  cursor: default;
}

.save-all-badge {
  position: absolute;
  bottom: 3px;
  right: 2px;
}

.titlebar-badge {
  position: absolute;
  top: 1px;
  right: 0;
  min-width: 14px;
  height: 14px;
  padding: 0 3px;
  border-radius: 999px;
  background: var(--attention);
  color: var(--text-on-accent);
  font-size: 0.6rem;
  font-weight: 700;
  line-height: 14px;
  text-align: center;
}

.titlebar-inbox.has-items {
  color: var(--attention);
}

.titlebar-separator {
  width: 1px;
  height: 16px;
  background: var(--border-default);
  margin: 0 var(--space-1);
}

.titlebar-toggle {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 28px;
  height: 28px;
  border-radius: var(--radius-md);
  color: var(--text-muted);
  transition: all var(--transition-fast);
}

.titlebar-toggle:hover {
  background: var(--bg-hover);
  color: var(--text-primary);
}

.titlebar-toggle.active {
  color: var(--accent-blue);
}

/* Activity bar */
.activity-bar {
  display: flex;
  flex-direction: column;
  align-items: center;
  width: 48px;
  flex-shrink: 0;
  background: var(--bg-surface);
  border-right: 0.5px solid var(--border-default);
  padding-top: var(--space-2);
  gap: 2px;
}

.activity-btn {
  --activity-accent: var(--text-muted);
  display: flex;
  align-items: center;
  justify-content: center;
  width: 40px;
  height: 40px;
  border-radius: var(--radius-md);
  color: color-mix(in srgb, var(--activity-accent) 58%, var(--text-muted));
  transition: all var(--transition-fast);
  position: relative;
}

.activity-btn--ai {
  --activity-accent: var(--accent);
}

.activity-btn--explorer {
  --activity-accent: var(--accent-blue);
}

.activity-btn--git {
  --activity-accent: var(--accent-purple);
}

.activity-btn--info {
  --activity-accent: var(--accent-green);
}

.activity-btn--services {
  --activity-accent: var(--gruvbox-aqua);
}

.activity-btn--tasks {
  --activity-accent: var(--accent-amber);
}

.activity-btn--merge-requests {
  --activity-accent: var(--accent-blue);
}

.activity-btn--agents {
  --activity-accent: var(--gruvbox-orange);
}

.activity-btn--project {
  --activity-accent: var(--gruvbox-yellow);
}

.activity-separator {
  width: 24px;
  height: 1px;
  margin: var(--space-1) 0;
  background: var(--border-default);
  flex-shrink: 0;
}

.activity-btn:hover {
  background: var(--bg-hover);
  color: var(--activity-accent);
}

.activity-btn.active {
  background: var(--bg-selected);
  color: var(--activity-accent);
}

.activity-btn.active::before {
  content: '';
  position: absolute;
  left: -4px;
  top: 8px;
  bottom: 8px;
  width: 2px;
  background: var(--activity-accent);
  border-radius: 1px;
}

.activity-badge {
  position: absolute;
  top: 3px;
  right: 3px;
  min-width: 16px;
  height: 16px;
  padding: 0 4px;
  border-radius: 999px;
  background: var(--activity-accent);
  color: var(--text-on-accent);
  border: 1px solid var(--bg-surface);
  font-family: var(--font-sans);
  font-size: 0.65rem;
  font-weight: 700;
  line-height: 14px;
  text-align: center;
  pointer-events: none;
}

/* Body layout */
.ide-body {
  display: flex;
  flex: 1;
  min-height: 0;
  overflow: hidden;
}

.ide-sidebar {
  flex-shrink: 0;
  overflow: hidden;
  background: var(--bg-surface);
  display: flex;
  flex-direction: column;
}

.git-sidebar {
  display: flex;
  flex-direction: column;
  height: 100%;
  min-height: 0;
}

.git-sidebar > .git-sidebar-panel {
  flex: 1;
  min-height: 0;
  height: auto;
}

.ide-center {
  flex: 1;
  display: flex;
  flex-direction: column;
  overflow: hidden;
  min-width: 0;
}

.ide-editor-area {
  flex: 1;
  overflow: hidden;
  min-height: 0;
  background: var(--ide-surface-bg);
}

.ide-surface-pane {
  height: 100%;
  overflow: hidden;
  background: var(--ide-surface-bg);
}

.ide-ai-surface {
  background: var(--ide-surface-bg);
}

.ide-terminal-area {
  flex-shrink: 0;
}

.ide-terminal-area.minimized {
  height: auto;
}

.ide-right-panel {
  display: flex;
  flex-shrink: 0;
  min-width: 0;
}

.ide-ai-side-panel {
  flex-shrink: 0;
  overflow-y: auto;
  background: var(--bg-surface);
}

.ide-preview {
  flex-shrink: 0;
  overflow: hidden;
  background: var(--bg-surface);
}

/* Resize handles */
.resize-handle {
  flex-shrink: 0;
  touch-action: none;
  position: relative;
  z-index: 10;
}

.resize-handle::after {
  content: '';
  position: absolute;
  transition: background var(--transition-fast);
  border-radius: 2px;
}

.resize-handle--horizontal {
  width: 1px;
  background: var(--border-default);
  cursor: col-resize;
}

.resize-handle--horizontal::after {
  top: 0;
  bottom: 0;
  left: -2px;
  width: 5px;
}

.resize-handle--vertical {
  height: 1px;
  background: var(--border-default);
  cursor: row-resize;
}

.resize-handle--vertical::after {
  left: 0;
  right: 0;
  top: -2px;
  height: 5px;
}

.resize-handle--vertical.hidden {
  display: none;
}

.resize-handle:hover::after,
.resize-handle.active::after {
  background: var(--accent-blue);
}

.resize-handle.active {
  background: var(--accent-blue);
}

/*
 * Mobile layout: bottom activity bar, slide-over side panels, full-screen preview.
 * The drawers and their backdrop stop above the activity bar so it stays tappable.
 */
.mobile-backdrop {
  position: absolute;
  inset: 0 0 var(--mobile-activity-bar-height);
  z-index: 40;
  background: var(--bg-overlay);
}

.is-mobile .ide-titlebar {
  gap: var(--space-2);
  padding: var(--space-1) var(--space-2);
  min-height: 44px;
}

.is-mobile .titlebar-back,
.is-mobile .titlebar-btn,
.is-mobile .titlebar-toggle {
  width: 36px;
  height: 36px;
}

.is-mobile .titlebar-actions {
  gap: 0;
}

.is-mobile .titlebar-status-text {
  display: none;
}

.is-mobile .ide-body {
  --mobile-activity-bar-height: 52px;
  position: relative;
  flex-direction: column;
}

.is-mobile .activity-bar {
  order: 1;
  flex-direction: row;
  justify-content: space-around;
  width: 100%;
  height: var(--mobile-activity-bar-height);
  padding: 0 var(--space-1);
  border-right: none;
  border-top: 0.5px solid var(--border-default);
}

.is-mobile .activity-btn {
  flex: 1;
  max-width: 72px;
  height: 44px;
}

.is-mobile .activity-separator {
  display: none;
}

.is-mobile .activity-btn.active::before {
  left: 25%;
  right: 25%;
  top: -4px;
  bottom: auto;
  width: auto;
  height: 2px;
}

.is-mobile .resize-handle--horizontal {
  display: none;
}

.is-mobile .resize-handle--vertical::after {
  top: -10px;
  height: 21px;
}

.is-mobile .ide-terminal-area:not(.minimized) {
  max-height: 50dvh;
}

.is-mobile .ide-sidebar,
.is-mobile .ide-ai-side-panel {
  position: absolute;
  top: 0;
  bottom: var(--mobile-activity-bar-height);
  z-index: 50;
  width: min(88vw, 380px);
  box-shadow: var(--shadow-strong);
  visibility: hidden;
  transition: transform var(--transition-normal), visibility 0s linear 200ms;
}

.is-mobile .ide-sidebar {
  left: 0;
  transform: translateX(-100%);
  border-right: 0.5px solid var(--border-default);
}

.is-mobile .ide-ai-side-panel {
  right: 0;
  transform: translateX(100%);
  border-left: 0.5px solid var(--border-default);
}

.is-mobile .ide-sidebar.open,
.is-mobile .ide-ai-side-panel.open {
  transform: none;
  visibility: visible;
  transition: transform var(--transition-normal), visibility 0s;
}

.is-mobile .ide-right-panel {
  display: contents;
}

.is-mobile .ide-preview {
  position: absolute;
  inset: 0;
  z-index: 30;
}
</style>
