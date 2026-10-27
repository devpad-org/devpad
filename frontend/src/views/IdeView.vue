<script setup lang="ts">
import { computed, nextTick, provide, ref, watch, onMounted, onUnmounted } from 'vue'
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
import PreviewNewTabModal from '@/components/ide/PreviewNewTabModal.vue'
import GitPanel from '@/components/ide/GitPanel.vue'
import GitStatusBar from '@/components/ide/GitStatusBar.vue'
import WorkspaceInfoPanel from '@/components/ide/WorkspaceInfoPanel.vue'
import WorkspaceProcessesPanel from '@/components/ide/WorkspaceProcessesPanel.vue'
import ServiceCatalogPanel from '@/components/ide/ServiceCatalogPanel.vue'
import WorkspaceServicesPanel from '@/components/ide/WorkspaceServicesPanel.vue'
import WorkspaceGate, { type WorkspaceGateState } from '@/components/ide/WorkspaceGate.vue'
import GitPushToasts from '@/components/project/GitPushToasts.vue'
import ProjectAgentsPanel from '@/components/project/ProjectAgentsPanel.vue'
import ProjectInboxPanel from '@/components/project/ProjectInboxPanel.vue'
import InboxPopover from '@/components/inbox/InboxPopover.vue'
import ProjectMergeRequestTable from '@/components/project/ProjectMergeRequestTable.vue'
import ProjectPanel from '@/components/project/ProjectPanel.vue'
import ProjectTaskTable from '@/components/project/ProjectTaskTable.vue'
import TaskModal from '@/components/project/TaskModal.vue'
import MergeRequestModal from '@/components/mergerequest/MergeRequestModal.vue'
import ConfirmModal from '@/components/ConfirmModal.vue'
import { confirmDiscardKey, ideShellKey, type BranchChange } from '@/components/ide/ideShell'
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
const editorPanel = ref<InstanceType<typeof EditorPanel> | null>(null)
// The merge request modal holds a review draft that closing it would lose.
const mergeRequestDirty = ref(false)
const selectedGitCommit = ref<GitCommit | null>(null)
const gitDiffRequest = ref<GitDiffRequest | null>(null)
// The file whose diff the Git Graph shows, for the Source Control panel.
const activeGitDiff = computed(() => {
  const req = gitDiffRequest.value
  if (!req) return null
  return req.kind === 'working'
    ? { kind: 'working' as const, path: req.path, staged: req.staged }
    : { kind: 'commit' as const, path: req.file.path }
})
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
// The inbox is a phone-only activity; on wider screens the title bar's inbox
// button covers it.
type ProjectActivity = 'inbox' | 'tasks' | 'merge-requests' | 'agents' | 'project'
const PROJECT_ACTIVITIES: ActivityTab[] = ['inbox', 'tasks', 'merge-requests', 'agents', 'project']
const activeActivity = ref<ActivityTab>('ai')
const serviceCount = computed(() => serviceStore.services.length)
const isProjectActivity = computed(() => PROJECT_ACTIVITIES.includes(activeActivity.value))
// On narrow screens the side panels become slide-over drawers and the activity
// bar moves to the bottom, leaving the primary surface the full width.
const isMobile = useIsMobile()
const mobileSidebarOpen = ref(false)
const mobileRunsOpen = ref(false)

// A project on a phone is mostly for checking on agents, so it gets its own
// layout: a bottom tab bar of the project's lists, each shown full screen,
// with the editor and the rest of the IDE under More.
const mobileProject = computed(() => isMobile.value && isProjectMode.value)
// The tasks and merge requests tables have surfaces of their own, like the
// Git Graph, and take the side panel's room too; on a phone's project they
// are its lists instead.
const taskSurfaceVisible = computed(() => activeActivity.value === 'tasks' && !mobileProject.value)
const mergeRequestSurfaceVisible = computed(() => activeActivity.value === 'merge-requests' && !mobileProject.value)
const tableSurfaceVisible = computed(() => taskSurfaceVisible.value || mergeRequestSurfaceVisible.value)
// The editor sits behind the explorer and the other project panels.
const editorSurfaceVisible = computed(
  () => activeActivity.value === 'explorer' || (isProjectActivity.value && !tableSurfaceVisible.value),
)
const mobileMoreOpen = ref(false)
// The bottom tabs; everything else lives under More.
const MOBILE_TABS: ActivityTab[] = ['inbox', 'tasks', 'merge-requests', 'agents', 'ai']
// Surfaces whose side list leads on a phone's project.
const MOBILE_LIST_FIRST: ActivityTab[] = ['git', 'services', 'info']

// On a phone a detail, such as a file or a diff, covers its list; back
// returns to the list before leaving the project.
const MOBILE_LIST_NAMES: Partial<Record<ActivityTab, string>> = {
  inbox: 'inbox',
  tasks: 'tasks',
  'merge-requests': 'merge requests',
  agents: 'agents',
  project: 'project',
  explorer: 'files',
  git: 'source control',
  services: 'services',
  info: 'workspace info',
}
const mobileBackList = computed(() =>
  mobileProject.value && !mobileSidebarOpen.value ? MOBILE_LIST_NAMES[activeActivity.value] ?? null : null,
)

watch(isMobile, (mobile) => {
  if (mobile) {
    // A project's list shows, rather than a detail nobody chose.
    if (isProjectMode.value && isProjectActivity.value) mobileSidebarOpen.value = true
    return
  }
  closeMobilePanels()
  if (activeActivity.value === 'inbox') activeActivity.value = desktopActivity()
})

// What a project opens on, or falls back to, on a wider screen.
function desktopActivity(): ActivityTab {
  return projectStore.conversion ? 'project' : workspaceLive.value ? 'ai' : 'tasks'
}

function closeMobilePanels() {
  mobileSidebarOpen.value = false
  mobileRunsOpen.value = false
  mobileMoreOpen.value = false
}

// The agent runs in this project waiting for the user: the project
// workspace's own and its agents' sandboxes'.
const projectAttention = computed(() => {
  if (!isProjectMode.value) return []
  const ids = new Set(projectStore.sandboxes.map((s) => s.id))
  if (workspace.value) ids.add(workspace.value.id)
  return attention.items.filter((item) => ids.has(item.run.workspaceId))
})

function selectActivity(tab: ActivityTab) {
  if (!isMobile.value) {
    activeActivity.value = tab
    return
  }
  mobileRunsOpen.value = false
  mobileMoreOpen.value = false
  // On a phone a project's lists fill the screen, so tapping the tab of the
  // one showing a detail goes back to the list rather than hiding it.
  if (mobileProject.value && activeActivity.value === tab && PROJECT_ACTIVITIES.includes(tab)) {
    mobileSidebarOpen.value = true
    return
  }
  if (activeActivity.value === tab) {
    mobileSidebarOpen.value = !mobileSidebarOpen.value
    return
  }
  activeActivity.value = tab
  // The explorer surface is empty until a file is chosen, so lead with the
  // tree; the project panels are lists, so lead with those too. On a project
  // the other surfaces with a side list lead with it as well, since the
  // phone layout has no side-panel toggle for them otherwise.
  mobileSidebarOpen.value =
    (tab === 'explorer' && !activeFile.value) ||
    PROJECT_ACTIVITIES.includes(tab) ||
    (mobileProject.value && MOBILE_LIST_FIRST.includes(tab))
}

function toggleMobileSidebar() {
  mobileRunsOpen.value = false
  mobileSidebarOpen.value = !mobileSidebarOpen.value
}

function toggleMobileRuns() {
  // A project's full-screen list stays put under the runs drawer.
  if (!mobileProject.value) mobileSidebarOpen.value = false
  mobileMoreOpen.value = false
  mobileRunsOpen.value = !mobileRunsOpen.value
}

const moreSheet = ref<HTMLElement | null>(null)
const moreButton = ref<HTMLButtonElement | null>(null)

function toggleMobileMore() {
  mobileRunsOpen.value = false
  mobileMoreOpen.value = !mobileMoreOpen.value
  if (mobileMoreOpen.value) {
    void nextTick(() => moreSheet.value?.querySelector<HTMLButtonElement>('.more-item:not(:disabled)')?.focus())
  }
}

// Escape closes the sheet and hands focus back to the More tab.
function dismissMore() {
  mobileMoreOpen.value = false
  moreButton.value?.focus()
}

// More lists the IDE's other surfaces; each choice closes it.
function moreActivity(tab: ActivityTab) {
  mobileMoreOpen.value = false
  selectActivity(tab)
}

function moreTerminal() {
  mobileMoreOpen.value = false
  closeMobilePanels()
  // The terminal sits under the editor, so it needs a surface that isn't a
  // full-screen list.
  if (isProjectActivity.value) activeActivity.value = 'explorer'
  terminalMinimized.value = false
}

function morePreview() {
  closeMobilePanels()
  showPreviewNewTabModal()
}

// The More tab stands for whichever surface isn't one of the bottom tabs.
const moreActive = computed(() => !MOBILE_TABS.includes(activeActivity.value))

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
  const legacy = legacyTabQuery(route.query)
  if (legacy) {
    await router.replace({ query: legacy })
    if (seq !== loadSeq) return
  }
  applyTaskQuery()
  applyMergeRequestQuery()
  if (isMobile.value && !attention.loaded) {
    await attention.refresh()
    if (seq !== loadSeq) return
  }
  // A conversion leaves the user something to do upstream, so its notice
  // comes first. The screen may have changed size during the wait.
  if (isMobile.value) {
    // A phone opens on what needs the user, or else the tasks, as a list;
    // never on the editor.
    activeActivity.value = projectStore.conversion ? 'project' : projectAttention.value.length > 0 ? 'inbox' : 'tasks'
    mobileSidebarOpen.value = true
  } else {
    activeActivity.value = desktopActivity()
  }
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
  selectedGitCommit.value = null
  gitDiffRequest.value = null
  focusedAgentRunId.value = null
  defaultAgentError.value = null
  mergeRequestDirty.value = false
  closeMobilePanels()
  detailPushed = false
  taskPushed = false
  reuseTaskEntry = false
  mergeRequestPushed = false
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

// Something in the shell would be lost by leaving it: unsaved files or a
// review draft in the merge request modal.
const hasUnsavedWork = computed(() => !!editorPanel.value?.hasDirtyFiles || mergeRequestDirty.value)

function openFile(path: string) {
  activeFile.value = path
  mobileSidebarOpen.value = false
}

// Panels and modals open files through the shell: a merge request in front
// of the editor closes first, and the editor comes forward if it is hidden.
async function openFileFromShell(path: string) {
  if (!(await closeMergeRequest())) return
  if (!editorSurfaceVisible.value) activeActivity.value = 'explorer'
  openFile(path)
}

// On a phone a detail shown over its list, such as a file or a diff, has its
// own history entry marked view=detail, so the system back gesture returns
// to the list.
let detailPushed = false

watch(mobileBackList, async (list) => {
  if (!isProjectMode.value || loading.value) return
  const view = list === null ? undefined : 'detail'
  if (view === route.query.view) return
  const query = { ...route.query, view }
  if (view === 'detail') await router.push({ query })
  else await router.replace({ query })
})

watch(() => route.query.view, (view, prev) => {
  if (!isProjectMode.value || loading.value || !mobileProject.value) return
  if (view === 'detail') {
    // Reached by a push or by forward, so the list is the entry behind.
    if (prev !== 'detail') detailPushed = true
    return
  }
  if (prev !== 'detail') return
  detailPushed = false
  // Back from a detail shows its list again.
  if (mobileBackList.value) {
    closeMobilePanels()
    mobileSidebarOpen.value = true
  }
})

// ?task=<id> opens that task over the project, e.g. from a merge request's
// task link or the inbox.
function applyTaskQuery() {
  const task = Number(route.query.task)
  if (task) projectStore.selectedTaskId = task
}

// On a phone a task opens full screen, so opening one from the project adds
// a history entry and closing it goes back.
let taskPushed = false
// Set while a closed task's entry may go to the detail it led to.
let reuseTaskEntry = false

watch(() => projectStore.selectedTaskId, (id) => {
  if (id === null || !mobileProject.value || loading.value) return
  if (Number(route.query.task) === id) return
  taskPushed = true
  void router.push({ query: { ...route.query, task: String(id) } })
})

function closeTask() {
  projectStore.selectedTaskId = null
  if (!route.query.task) return
  if (taskPushed) {
    taskPushed = false
    // Closing a task can lead straight to a detail, such as its merge
    // request, which then takes the task's entry; otherwise go back.
    reuseTaskEntry = true
    void nextTick(() => {
      if (!reuseTaskEntry) return
      reuseTaskEntry = false
      router.back()
    })
    return
  }
  void router.replace({ query: { ...route.query, task: undefined } })
}

// ?mr=<id> opens that merge request in its modal over the project, so a
// reload, back and forward, or a shared link shows it again. The URL leads:
// opening one asks to navigate, and the modal follows the query, so the leave
// guard can keep a review draft from being dropped.
function queryMergeRequestId(query: LocationQuery): number | null {
  const id = Number(query.mr)
  return Number.isInteger(id) && id > 0 ? id : null
}

const shownMergeRequestId = computed(() => (isProjectMode.value ? queryMergeRequestId(route.query) : null))

// Links from when merge requests opened as editor tabs name them as
// ?tabs=mr:3,mr:12&tab=mr:12; the one that was showing opens in the modal.
function legacyTabQuery(query: LocationQuery): LocationQuery | null {
  if (query.tabs === undefined && query.tab === undefined) return null
  const tab = typeof query.tab === 'string' ? /^mr:([1-9]\d*)$/.exec(query.tab) : null
  const { tabs: _tabs, tab: _tab, ...rest } = query
  return tab && rest.mr === undefined ? { ...rest, mr: tab[1] } : rest
}

function applyMergeRequestQuery() {
  projectStore.selectedMergeRequestId = shownMergeRequestId.value
}

// On a phone a merge request is a full-screen sheet, so, like a task, opening
// one adds a history entry and closing it goes back.
let mergeRequestPushed = false

watch(() => projectStore.selectedMergeRequestId, async (id) => {
  if (id === null || !isProjectMode.value || loading.value || id === shownMergeRequestId.value) return
  // A task closed on its way here, such as by its Review button, leaves the
  // URL with the task.
  const query = {
    ...route.query,
    mr: String(id),
    task: projectStore.selectedTaskId === null ? undefined : route.query.task,
  }
  let failure
  if (reuseTaskEntry) {
    // The task's own history entry goes to the merge request.
    reuseTaskEntry = false
    mergeRequestPushed = true
    failure = await router.replace({ query })
  } else if (mobileProject.value && !mergeRequestPushed) {
    mergeRequestPushed = true
    failure = await router.push({ query })
  } else {
    failure = await router.replace({ query })
  }
  // The user kept a review draft in the one showing.
  if (failure) projectStore.selectedMergeRequestId = shownMergeRequestId.value
})

watch(shownMergeRequestId, (id) => {
  if (id === null) mergeRequestPushed = false
  if (!loading.value) projectStore.selectedMergeRequestId = id
})

const DISCARD_REVIEW = {
  title: 'Discard your review?',
  message: 'This merge request has a review you haven\'t submitted. Close it and lose the summary and line comments?',
  confirmLabel: 'Discard review',
}

// Closes the merge request modal once the user agrees to lose any review
// draft in it, resolving false when they keep it.
async function closeMergeRequest(): Promise<boolean> {
  if (shownMergeRequestId.value === null) return true
  if (discardPrompt.value) return false
  if (mergeRequestDirty.value && !(await askConfirm(DISCARD_REVIEW))) return false
  mergeRequestDirty.value = false
  if (mergeRequestPushed) {
    mergeRequestPushed = false
    router.back()
    return true
  }
  await router.replace({ query: { ...route.query, mr: undefined } })
  return true
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

// A checkout moves HEAD to other files: the git panels start over and open
// files reload from the new branch. A commit or a new branch moves HEAD
// without touching the files, so only the git panels start over. Publish
// and sync move only refs, which the panels pick up on their own refresh.
function branchChanged(kind: BranchChange) {
  if (kind === 'publish' || kind === 'sync') return
  gitRevision.value++
  if (kind === 'checkout') void editorPanel.value?.reloadFiles()
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

// Leaving the shell also loses a review draft in the merge request modal.
function confirmLeave(): Promise<boolean> {
  if (!hasUnsavedWork.value) return Promise.resolve(true)
  const files = !!editorPanel.value?.hasDirtyFiles
  const reviews = mergeRequestDirty.value
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

// A navigation within the shell changes only the query, but it can still
// close or replace a merge request that holds a review draft, e.g. on back.
async function confirmDropMergeRequest(query: LocationQuery): Promise<boolean> {
  if (!isProjectMode.value || !mergeRequestDirty.value) return true
  if (queryMergeRequestId(query) === shownMergeRequestId.value) return true
  const ok = await askConfirm(DISCARD_REVIEW)
  // The next merge request starts without a draft.
  if (ok) mergeRequestDirty.value = false
  return ok
}

function answerDiscard(ok: boolean) {
  const prompt = discardPrompt.value
  discardPrompt.value = null
  prompt?.resolve(ok)
}

provide(confirmDiscardKey, confirmDiscard)
provide(ideShellKey, {
  openFile: (path) => void openFileFromShell(path),
  liveWorkspaceId: computed(() => (isProjectMode.value && workspaceLive.value ? workspace.value?.id ?? null : null)),
  unsavedFiles: computed(() => !!editorPanel.value?.hasDirtyFiles),
  async saveAllFiles() {
    await editorPanel.value?.saveAllFiles()
    return !editorPanel.value?.hasDirtyFiles
  },
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
watch(() => route.query.task, (task) => {
  if (!isProjectMode.value) return
  if (task) {
    applyTaskQuery()
    return
  }
  // Back past a task closes it.
  taskPushed = false
  projectStore.selectedTaskId = null
})

onMounted(() => {
  window.addEventListener('keydown', handleGlobalKeydown)
  window.addEventListener('beforeunload', handleBeforeUnload)
  removeLeaveGuard = router.beforeEach((to, from) => {
    if (to.name === from.name && to.params.id === from.params.id) return confirmDropMergeRequest(to.query)
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
  if (mobileBackList.value) {
    // A detail opened from its list goes back through history, so this and
    // the system back gesture agree.
    if (detailPushed && route.query.view === 'detail') {
      router.back()
      return
    }
    closeMobilePanels()
    mobileSidebarOpen.value = true
    return
  }
  // The agent list over the chat closes back to the chat.
  if (mobileAgentList.value) {
    mobileSidebarOpen.value = false
    return
  }
  if (isProjectMode.value) {
    router.push({ name: 'home' })
    return
  }
  const sandboxProject = workspace.value?.projectId
  if (sandboxProject) router.push({ name: 'project', params: { id: sandboxProject } })
  else router.push({ name: 'home' })
}

const mobileAgentList = computed(() => mobileProject.value && mobileSidebarOpen.value && activeActivity.value === 'ai')

const backLabel = computed(() => {
  if (mobileBackList.value) return `Back to ${mobileBackList.value}`
  if (mobileAgentList.value) return 'Back to chat'
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
  <div v-else class="ide-layout" :class="{ 'is-mobile': isMobile, 'is-mobile-project': mobileProject }">
    <!--
      IDE panel IDs:
      - ide-toolbar: top toolbar/title bar
      - ide-activity-bar: far-left activity switcher
      - ide-left-panel: left side panel next to the activity bar
      - ide-primary-surface: AI chat, editor, tasks or merge requests table, or git graph surface
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
      <!-- On a phone's project, only the IDE's own surfaces have a side list
           to toggle; the project's lists are full screen already. -->
      <button
        v-if="isMobile && (!mobileProject || !isProjectActivity)"
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
        <InboxPopover
          :current-project-id="projectId"
          :current-workspace-id="workspace?.id ?? null"
          @open-chat="handleAgentRunFocus"
        >
          <template #trigger="{ open, toggle }">
            <button
              class="titlebar-btn titlebar-inbox"
              :class="{ 'has-items': attention.count > 0, active: open }"
              :title="attention.count > 0 ? `Inbox: ${attention.count} waiting for you` : 'Inbox'"
              :aria-label="attention.count > 0 ? `Inbox, ${attention.count} waiting for you` : 'Inbox'"
              :aria-expanded="open"
              aria-haspopup="dialog"
              @click="toggle"
            >
              <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round">
                <path d="M22 12h-6l-2 3h-4l-2-3H2" />
                <path d="M5.45 5.11 2 12v6a2 2 0 0 0 2 2h16a2 2 0 0 0 2-2v-6l-3.45-6.89A2 2 0 0 0 16.76 4H7.24a2 2 0 0 0-1.79 1.11z" />
              </svg>
              <span v-if="attention.count > 0" class="titlebar-badge">{{ attention.count }}</span>
            </button>
          </template>
        </InboxPopover>
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
          v-if="!mobileProject"
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
        <template v-if="!mobileProject">
        <div class="titlebar-separator" />
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
        </template>
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
      <nav v-if="!mobileProject" id="ide-activity-bar" class="activity-bar" aria-label="Activity bar">
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
            <path d="M11 21.73a2 2 0 0 0 2 0l7-4A2 2 0 0 0 21 16V8a2 2 0 0 0-1-1.73l-7-4a2 2 0 0 0-2 0l-7 4A2 2 0 0 0 3 8v8a2 2 0 0 0 1 1.73z" />
            <path d="M12 22V12" />
            <path d="m3.3 7 7.703 4.734a2 2 0 0 0 1.994 0L20.7 7" />
            <path d="m7.5 4.27 9 5.15" />
          </svg>
          <span v-if="projectStore.conversion" class="activity-badge" aria-label="Conversion notice">!</span>
        </button>
      </nav>

      <nav v-else id="ide-activity-bar" class="mobile-tabbar" aria-label="Project sections">
        <button
          class="mobile-tab mobile-tab--inbox"
          :class="{ active: activeActivity === 'inbox' }"
          :aria-current="activeActivity === 'inbox' ? 'page' : undefined"
          @click="selectActivity('inbox')"
        >
          <span class="mobile-tab-icon">
            <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round">
              <path d="M22 12h-6l-2 3h-4l-2-3H2" />
              <path d="M5.45 5.11 2 12v6a2 2 0 0 0 2 2h16a2 2 0 0 0 2-2v-6l-3.45-6.89A2 2 0 0 0 16.76 4H7.24a2 2 0 0 0-1.79 1.11z" />
            </svg>
            <span v-if="projectAttention.length > 0" class="mobile-tab-badge attention" :aria-label="`${projectAttention.length} waiting for you`">
              {{ projectAttention.length }}
            </span>
          </span>
          <span class="mobile-tab-label">Inbox</span>
        </button>
        <button
          class="mobile-tab mobile-tab--tasks"
          :class="{ active: activeActivity === 'tasks' }"
          :aria-current="activeActivity === 'tasks' ? 'page' : undefined"
          @click="selectActivity('tasks')"
        >
          <span class="mobile-tab-icon">
            <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round">
              <path d="m3 17 2 2 4-4" />
              <path d="m3 7 2 2 4-4" />
              <path d="M13 6h8" />
              <path d="M13 12h8" />
              <path d="M13 18h8" />
            </svg>
          </span>
          <span class="mobile-tab-label">Tasks</span>
        </button>
        <button
          class="mobile-tab mobile-tab--merge-requests"
          :class="{ active: activeActivity === 'merge-requests' }"
          :aria-current="activeActivity === 'merge-requests' ? 'page' : undefined"
          @click="selectActivity('merge-requests')"
        >
          <span class="mobile-tab-icon">
            <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round">
              <circle cx="18" cy="18" r="3" />
              <circle cx="6" cy="6" r="3" />
              <path d="M13 6h3a2 2 0 0 1 2 2v7" />
              <path d="M6 9v12" />
            </svg>
            <span
              v-if="projectStore.openMergeRequests.length > 0"
              class="mobile-tab-badge"
              :aria-label="`${projectStore.openMergeRequests.length} open merge requests`"
            >
              {{ projectStore.openMergeRequests.length }}
            </span>
          </span>
          <span class="mobile-tab-label">MRs</span>
        </button>
        <button
          class="mobile-tab mobile-tab--agents"
          :class="{ active: activeActivity === 'agents' }"
          :aria-current="activeActivity === 'agents' ? 'page' : undefined"
          @click="selectActivity('agents')"
        >
          <span class="mobile-tab-icon">
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
              class="mobile-tab-badge"
              :aria-label="`${projectStore.activeSandboxes} sandboxes running`"
            >
              {{ projectStore.activeSandboxes }}
            </span>
          </span>
          <span class="mobile-tab-label">Agents</span>
        </button>
        <button
          class="mobile-tab mobile-tab--ai"
          :class="{ active: activeActivity === 'ai' }"
          :aria-current="activeActivity === 'ai' ? 'page' : undefined"
          @click="selectActivity('ai')"
        >
          <span class="mobile-tab-icon">
            <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round">
              <path d="m12 3-1.912 5.813a2 2 0 0 1-1.275 1.275L3 12l5.813 1.912a2 2 0 0 1 1.275 1.275L12 21l1.912-5.813a2 2 0 0 1 1.275-1.275L21 12l-5.813-1.912a2 2 0 0 1-1.275-1.275L12 3Z" />
            </svg>
          </span>
          <span class="mobile-tab-label">Chat</span>
        </button>
        <button
          class="mobile-tab mobile-tab--more"
          ref="moreButton"
          :class="{ active: mobileMoreOpen || moreActive }"
          :aria-expanded="mobileMoreOpen"
          :aria-controls="mobileMoreOpen ? 'ide-more-sheet' : undefined"
          @click="toggleMobileMore"
        >
          <span class="mobile-tab-icon">
            <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round">
              <circle cx="12" cy="12" r="1" />
              <circle cx="19" cy="12" r="1" />
              <circle cx="5" cy="12" r="1" />
            </svg>
            <span v-if="projectStore.conversion" class="mobile-tab-badge" aria-label="Conversion notice">!</span>
          </span>
          <span class="mobile-tab-label">More</span>
        </button>
      </nav>

      <div
        v-if="mobileProject && mobileMoreOpen"
        id="ide-more-sheet"
        ref="moreSheet"
        class="more-sheet"
        role="group"
        aria-label="More"
        @keydown.esc="dismissMore"
      >
        <button class="more-item" @click="moreActivity('explorer')">
          <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round">
            <path d="M4 4h5l2 2h8a2 2 0 0 1 2 2v10a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2V6a2 2 0 0 1 2-2Z" />
          </svg>
          Files
        </button>
        <button class="more-item" @click="moreActivity('git')">
          <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round">
            <circle cx="18" cy="18" r="3" />
            <circle cx="6" cy="6" r="3" />
            <path d="M6 21V9a9 9 0 0 0 9 9" />
          </svg>
          Source control
        </button>
        <button class="more-item" :disabled="!workspaceLive" @click="moreTerminal">
          <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round">
            <path d="m4 17 6-6-6-6" />
            <path d="M12 19h8" />
          </svg>
          Terminal
        </button>
        <button class="more-item" :disabled="!workspaceLive" @click="morePreview">
          <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round">
            <rect width="20" height="14" x="2" y="3" rx="2" />
            <path d="M8 21h8" />
            <path d="M12 17v4" />
          </svg>
          Preview
        </button>
        <button class="more-item" @click="moreActivity('services')">
          <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round">
            <ellipse cx="12" cy="5" rx="9" ry="3" />
            <path d="M3 5v14a9 3 0 0 0 18 0V5" />
            <path d="M3 12a9 3 0 0 0 18 0" />
          </svg>
          Services
          <span v-if="serviceCount > 0" class="more-count">{{ serviceCount }}</span>
        </button>
        <button class="more-item" @click="moreActivity('info')">
          <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round">
            <circle cx="12" cy="12" r="10" />
            <path d="M12 16v-4" />
            <path d="M12 8h.01" />
          </svg>
          Workspace info
        </button>
        <button class="more-item" @click="moreActivity('project')">
          <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round">
            <path d="M11 21.73a2 2 0 0 0 2 0l7-4A2 2 0 0 0 21 16V8a2 2 0 0 0-1-1.73l-7-4a2 2 0 0 0-2 0l-7 4A2 2 0 0 0 3 8v8a2 2 0 0 0 1 1.73z" />
            <path d="M12 22V12" />
            <path d="m3.3 7 7.703 4.734a2 2 0 0 0 1.994 0L20.7 7" />
            <path d="m7.5 4.27 9 5.15" />
          </svg>
          Project
          <span v-if="projectStore.conversion" class="more-count attention">!</span>
        </button>
      </div>

      <div
        v-if="isMobile && (mobileRunsOpen || mobileMoreOpen || (mobileSidebarOpen && !mobileProject))"
        class="mobile-backdrop"
        aria-hidden="true"
        @click="closeMobilePanels"
      />

      <aside
        id="ide-left-panel"
        class="ide-sidebar"
        :class="{ open: mobileSidebarOpen, collapsed: tableSurfaceVisible }"
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
          <GitPanel
            v-show="activeActivity === 'git'"
            :workspace-id="workspace?.id ?? 0"
            :selected-commit="selectedGitCommit"
            :active-diff="activeGitDiff"
            :project-mode="isProjectMode"
            :active="activeActivity === 'git'"
            :revision="gitRevision"
            @commit-file-select="handleGitCommitFileSelect"
            @working-file-select="handleGitWorkingFileSelect"
            @clear-commit="clearSelectedGitCommit"
            @changed="branchChanged"
          />
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
          <ProjectInboxPanel
            v-if="mobileProject"
            v-show="activeActivity === 'inbox'"
            :items="projectAttention"
            :workspace-id="workspace?.id ?? null"
            @open-chat="handleAgentRunFocus"
          />
          <ProjectTaskTable v-if="mobileProject" v-show="activeActivity === 'tasks'" />
          <ProjectMergeRequestTable v-if="mobileProject" v-show="activeActivity === 'merge-requests'" />
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
        v-show="!tableSurfaceVisible"
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
              :project-id="isProjectMode ? projectId : null"
              @branch-changed="branchChanged"
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
          <div v-if="isProjectMode && !mobileProject" v-show="taskSurfaceVisible" class="ide-surface-pane">
            <ProjectTaskTable />
          </div>
          <div v-if="isProjectMode && !mobileProject" v-show="mergeRequestSurfaceVisible" class="ide-surface-pane">
            <ProjectMergeRequestTable />
          </div>
          <div v-show="editorSurfaceVisible" class="ide-surface-pane">
            <EditorPanel
              ref="editorPanel"
              :workspace-id="workspace?.id ?? 0"
              :file-path="activeFile"
              @active-change="(p: string | null) => activeFile = p"
            />
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
      v-if="workspaceLive && !mobileProject"
      :key="`status-${gitRevision}`"
      :workspace-id="workspace?.id ?? 0"
      @open-git="openGitPanel"
    />
    <!-- The shell teleports the merge request modal, before the task modal's
         own teleport, so a task opened from a merge request shows on top. -->
    <Teleport to="body">
      <MergeRequestModal
        v-if="projectId !== null && shownMergeRequestId !== null"
        :key="shownMergeRequestId"
        :project-id="projectId"
        :mr-id="shownMergeRequestId"
        @close="closeMergeRequest"
        @dirty="(dirty: boolean) => (mergeRequestDirty = dirty)"
      />
    </Teleport>
    <TaskModal
      v-if="isProjectMode && projectId !== null"
      :task="projectStore.selectedTask"
      :project-id="projectId"
      :merge-request="projectStore.selectedTask ? projectStore.mergeRequestByTask.get(projectStore.selectedTask.id) : undefined"
      :workspace-live="workspaceLive"
      @close="closeTask"
      @checked-out="checkedOut"
      @changed="taskChanged"
      @deleted="taskDeleted"
    />
    <GitPushToasts v-if="isProjectMode" />
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

.titlebar-inbox.active {
  background: var(--bg-hover);
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

/* The tasks and merge requests tables take the side panel's room too. */
.ide-sidebar.collapsed {
  display: none;
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
 * Mobile layout: bottom activity bar and slide-over side panels.
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

/*
 * Phone layout of a project: a labelled bottom tab bar, the project's lists
 * full screen above it, and the rest of the IDE in the More sheet. Only the
 * project route on a narrow screen gets it; the desktop layout is untouched.
 */
.is-mobile-project .ide-body {
  /* #app already pads the bottom safe-area inset. */
  --mobile-activity-bar-height: var(--mobile-tabbar-height);
}

.mobile-tabbar {
  order: 1;
  display: flex;
  flex-shrink: 0;
  height: var(--mobile-activity-bar-height);
  padding: 0 var(--space-1);
  background: var(--bg-surface);
  border-top: 0.5px solid var(--border-default);
}

.mobile-tab {
  --activity-accent: var(--text-muted);
  position: relative;
  flex: 1;
  min-width: 0;
  height: var(--mobile-tabbar-height);
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 3px;
  color: color-mix(in srgb, var(--activity-accent) 58%, var(--text-muted));
  transition: color var(--transition-fast);
}

.mobile-tab--inbox {
  --activity-accent: var(--attention);
}

.mobile-tab--tasks {
  --activity-accent: var(--accent-amber);
}

.mobile-tab--merge-requests {
  --activity-accent: var(--accent-blue);
}

.mobile-tab--agents {
  --activity-accent: var(--gruvbox-orange);
}

.mobile-tab--ai {
  --activity-accent: var(--accent);
}

.mobile-tab--more {
  --activity-accent: var(--text-secondary);
}

.mobile-tab.active {
  color: var(--activity-accent);
}

.mobile-tab.active::before {
  content: '';
  position: absolute;
  top: 0;
  left: 25%;
  right: 25%;
  height: 2px;
  border-radius: 1px;
  background: var(--activity-accent);
}

.mobile-tab-icon {
  position: relative;
  display: flex;
}

.mobile-tab-label {
  font-size: 0.7rem;
  font-weight: 500;
  line-height: 1;
}

.mobile-tab-badge {
  position: absolute;
  top: -6px;
  right: -11px;
  min-width: 16px;
  height: 16px;
  padding: 0 4px;
  border-radius: 999px;
  background: var(--activity-accent);
  color: var(--text-on-accent);
  border: 1px solid var(--bg-surface);
  font-size: 0.65rem;
  font-weight: 700;
  line-height: 14px;
  text-align: center;
  pointer-events: none;
}

.mobile-tab-badge.attention {
  background: var(--attention);
}

/* The project's lists fill the screen above the tab bar instead of sliding
   over the surface, so there is no backdrop and no slide. */
.is-mobile-project .ide-sidebar {
  left: 0;
  right: 0;
  width: auto;
  transform: none;
  box-shadow: none;
  border-right: none;
  transition: none;
}

.is-mobile-project .ide-sidebar.open {
  transition: none;
}

/* Over a full-screen list the backdrop and the runs drawer sit above it. */
.is-mobile-project .mobile-backdrop {
  z-index: 55;
}

.is-mobile-project .ide-ai-side-panel {
  z-index: 58;
}

.more-sheet {
  position: absolute;
  left: 0;
  right: 0;
  bottom: var(--mobile-activity-bar-height);
  /* Above the git push notices (900), below dialogs (1000). */
  z-index: 950;
  display: flex;
  flex-direction: column;
  max-height: 70dvh;
  overflow-y: auto;
  padding: var(--space-2) var(--space-4);
  background: var(--bg-elevated);
  border-top: 0.5px solid var(--border-strong);
  border-radius: var(--radius-xl) var(--radius-xl) 0 0;
  box-shadow: var(--shadow-strong);
}

.more-item {
  display: flex;
  align-items: center;
  gap: var(--space-3);
  min-height: 48px;
  padding: 0 var(--space-2);
  border-radius: var(--radius-md);
  font-size: 0.9rem;
  color: var(--text-primary);
  text-align: left;
}

.more-item svg {
  flex-shrink: 0;
  color: var(--text-secondary);
}

.more-item:hover:not(:disabled) {
  background: var(--bg-hover);
}

.more-item:disabled {
  opacity: 0.45;
  cursor: default;
}

.more-count {
  margin-left: auto;
  min-width: 20px;
  padding: 0 6px;
  border-radius: 999px;
  background: var(--bg-selected);
  color: var(--text-secondary);
  font-size: 0.7rem;
  line-height: 20px;
  text-align: center;
}

.more-count.attention {
  background: var(--attention);
  color: var(--text-on-accent);
}
</style>

