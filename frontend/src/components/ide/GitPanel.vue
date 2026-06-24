<script setup lang="ts">
import { ref, computed, onMounted, onUnmounted, watch } from 'vue'
import {
  gitApi,
  type GitActionResult,
  type GitStatus,
  type GitBranch,
  type GitCommit,
  type GitCommitFile,
} from '@/api/git'
import { useFileWatcher } from '@/composables/useFileWatcher'
import { useGitSshHostKey } from '@/composables/useGitSshHostKey'
import ConfirmModal from '@/components/ConfirmModal.vue'
import GitConfigModal from './GitConfigModal.vue'
import GitSshHostKeyModal from './GitSshHostKeyModal.vue'

const props = defineProps<{
  workspaceId: number
  selectedCommit: GitCommit | null
}>()

const emit = defineEmits<{
  (e: 'commitFileSelect', payload: { commit: GitCommit; file: GitCommitFile }): void
  (e: 'workingFileSelect', payload: { path: string; staged: boolean }): void
  (e: 'clearCommit'): void
}>()

// --- State ---
type Tab = 'changes' | 'branches'
const activeTab = ref<Tab>('changes')
const loading = ref(false)
const error = ref('')

// Status
const status = ref<GitStatus | null>(null)
const commitMsg = ref('')
const actionOutput = ref('')

// Branches
const branches = ref<GitBranch[]>([])
const currentBranch = ref('')
const newBranchName = ref('')
const showNewBranch = ref(false)
const branchAction = ref<string | null>(null)
const deleteBranchTarget = ref<GitBranch | null>(null)
const deletingBranch = ref(false)

// Commit selection
const commitFiles = ref<GitCommitFile[]>([])
const commitFilesLoading = ref(false)
const commitFilesError = ref('')

// Git config modal
const showGitConfig = ref(false)
const needsGitConfig = computed(() =>
  status.value?.isRepo && (!status.value.userName || !status.value.userEmail)
)

// File watcher for event-driven refresh
const { connect: connectWatcher, onEvent } = useFileWatcher(() => props.workspaceId)
const {
  hostKey: sshHostKey,
  accepting: acceptingSshHostKey,
  error: sshHostKeyError,
  promptFromActionResult,
  promptFromError,
  accept: acceptSshHostKey,
  cancel: cancelSshHostKey,
} = useGitSshHostKey(() => props.workspaceId)
let refreshTimer: ReturnType<typeof setTimeout> | null = null

function debouncedRefresh() {
  if (refreshTimer) clearTimeout(refreshTimer)
  refreshTimer = setTimeout(() => refresh(false), 1000)
}

onEvent(() => {
  debouncedRefresh()
})

// --- Computed ---
const stagedFiles = computed(() => (status.value?.files ?? []).filter((f) => f.staged))
const unstagedFiles = computed(() => (status.value?.files ?? []).filter((f) => !f.staged))
const selectedCommitHash = computed(() => props.selectedCommit?.hash ?? null)
const viewingCommit = computed(() => props.selectedCommit !== null)

const hasChanges = computed(() => (status.value?.files?.length ?? 0) > 0)
const canCommit = computed(() => stagedFiles.value.length > 0 && commitMsg.value.trim() !== '')
const changesTabCount = computed(() =>
  viewingCommit.value ? commitFiles.value.length : (status.value?.files?.length ?? 0)
)
const localBranches = computed(() => branches.value.filter((branch) => !branch.remote))
const remoteBranchGroups = computed(() => {
  const groups = new Map<string, GitBranch[]>()
  for (const branch of branches.value.filter((item) => item.remote)) {
    const remote = branch.remoteName || branch.name.split('/')[0] || 'remote'
    const group = groups.get(remote) ?? []
    group.push(branch)
    groups.set(remote, group)
  }
  return Array.from(groups, ([remote, groupBranches]) => ({ remote, branches: groupBranches }))
})
const deleteBranchMessage = computed(() => {
  const branch = deleteBranchTarget.value
  if (!branch) return ''
  const label = branchDisplayName(branch)
  return branch.remote
    ? `Delete remote branch '${label}' from '${branch.remoteName || 'remote'}'? This cannot be undone.`
    : `Delete local branch '${label}'? Git will refuse if it has unmerged commits.`
})

// --- Actions ---
function gitError(result: GitActionResult): string {
  return result.output || result.error || 'Unknown error'
}

async function refresh(showLoading = true) {
  if (!props.workspaceId) return
  if (showLoading) loading.value = true
  error.value = ''
  try {
    status.value = await gitApi.status(props.workspaceId)

    if (activeTab.value === 'branches') {
      const res = await gitApi.branches(props.workspaceId)
      branches.value = res.branches
      currentBranch.value = res.current
    }
  } catch (e: any) {
    error.value = e.message || 'Failed to load git status'
  } finally {
    loading.value = false
  }
}

async function loadCommitFiles(commit: GitCommit) {
  if (!props.workspaceId) return

  commitFilesLoading.value = true
  commitFilesError.value = ''
  try {
    commitFiles.value = await gitApi.commitFiles(props.workspaceId, commit.hash)
  } catch (e: any) {
    commitFilesError.value = e.message || 'Failed to load commit files'
    commitFiles.value = []
  } finally {
    commitFilesLoading.value = false
  }
}

function clearSelectedCommit() {
  commitFiles.value = []
  commitFilesError.value = ''
  emit('clearCommit')
}

function selectCommitFile(file: GitCommitFile) {
  if (!props.selectedCommit) return
  emit('commitFileSelect', { commit: props.selectedCommit, file })
}

async function stageFile(path: string) {
  actionOutput.value = ''
  const result = await gitApi.action(props.workspaceId, 'stage', { files: [path] })
  if (!result.success) actionOutput.value = gitError(result)
  await refresh()
}

async function unstageFile(path: string) {
  actionOutput.value = ''
  const result = await gitApi.action(props.workspaceId, 'unstage', { files: [path] })
  if (!result.success) actionOutput.value = gitError(result)
  await refresh()
}

async function stageAll() {
  actionOutput.value = ''
  const result = await gitApi.action(props.workspaceId, 'stage')
  if (!result.success) actionOutput.value = gitError(result)
  await refresh()
}

async function unstageAll() {
  actionOutput.value = ''
  const result = await gitApi.action(props.workspaceId, 'unstage')
  if (!result.success) actionOutput.value = gitError(result)
  await refresh()
}

async function discardFile(path: string) {
  actionOutput.value = ''
  const result = await gitApi.action(props.workspaceId, 'discard', { files: [path] })
  if (!result.success) actionOutput.value = gitError(result)
  await refresh()
}

async function commit() {
  if (!canCommit.value) return
  // Prompt for git config if not set
  if (needsGitConfig.value) {
    showGitConfig.value = true
    return
  }
  await doCommit()
}

async function doCommit() {
  actionOutput.value = ''
  const result = await gitApi.action(props.workspaceId, 'commit', {
    message: commitMsg.value.trim(),
  })
  if (result.success) {
    commitMsg.value = ''
    actionOutput.value = ''
  } else {
    actionOutput.value = result.output || result.error || 'Commit failed'
  }
  await refresh()
}

async function handleGitConfigSubmit(name: string, email: string) {
  showGitConfig.value = false
  await gitApi.action(props.workspaceId, 'set-config', { userName: name, userEmail: email })
  await refresh()
  await doCommit()
}

async function initRepo() {
  actionOutput.value = ''
  const result = await gitApi.action(props.workspaceId, 'init')
  actionOutput.value = result.success ? 'Repository initialized' : gitError(result)
  await refresh()
}

const cloneUrl = ref('')
const cloning = ref(false)

async function cloneRepo() {
  const url = cloneUrl.value.trim()
  if (!url) return
  actionOutput.value = ''
  cloning.value = true
  try {
    const result = await gitApi.action(props.workspaceId, 'clone', { url })
    if (promptFromActionResult(result, cloneRepo)) return
    actionOutput.value = result.success ? 'Repository cloned' : gitError(result)
    if (result.success) cloneUrl.value = ''
    await refresh()
  } catch (e) {
    if (promptFromError(e, cloneRepo)) return
    actionOutput.value = e instanceof Error ? e.message : 'Failed to clone repository'
  } finally {
    cloning.value = false
  }
}

function branchDisplayName(branch: GitBranch): string {
  if (!branch.remote) return branch.name
  return branch.remoteBranch || branch.name.split('/').slice(1).join('/') || branch.name
}

function branchSubtitle(branch: GitBranch): string {
  const parts: string[] = []
  if (branch.remote) {
    parts.push(branch.remoteName ? `Remote: ${branch.remoteName}` : 'Remote branch')
  } else {
    parts.push(branch.upstream ? `Tracks ${branch.upstream}` : 'Local branch')
  }
  if (branch.hash) parts.push(branch.hash)
  return parts.join(' · ')
}

function branchActionPayload(branch: GitBranch): { branch: string; remote?: string } {
  if (!branch.remote) return { branch: branch.name }
  const remote = branch.remoteName || branch.name.split('/')[0]
  return {
    branch: branch.remoteBranch || branch.name.split('/').slice(1).join('/'),
    remote,
  }
}

function branchActionKey(action: string, branch: GitBranch): string {
  return `${action}:${branch.name}`
}

function isBranchActionRunning(action: string, branch: GitBranch): boolean {
  return branchAction.value === branchActionKey(action, branch)
}

function branchErrorMessage(err: unknown, fallback: string): string {
  return err instanceof Error ? err.message : fallback
}

async function checkoutBranch(branch: GitBranch) {
  if (branch.current) return
  const key = branchActionKey('checkout', branch)
  actionOutput.value = ''
  branchAction.value = key
  try {
    const result = await gitApi.action(props.workspaceId, 'checkout', branchActionPayload(branch))
    if (promptFromActionResult(result, () => checkoutBranch(branch))) return
    actionOutput.value = result.success ? `Checked out ${branchDisplayName(branch)}` : gitError(result)
    await refresh()
  } catch (err) {
    if (promptFromError(err, () => checkoutBranch(branch))) return
    actionOutput.value = branchErrorMessage(err, 'Failed to checkout branch')
  } finally {
    if (branchAction.value === key) branchAction.value = null
  }
}

async function createBranch() {
  const name = newBranchName.value.trim()
  if (!name) return
  actionOutput.value = ''
  const result = await gitApi.action(props.workspaceId, 'checkout-new', { branch: name })
  if (result.success) {
    newBranchName.value = ''
    showNewBranch.value = false
  } else {
    actionOutput.value = gitError(result)
  }
  await refresh()
}

async function mergeBranch(branch: GitBranch) {
  if (branch.current || !currentBranch.value || currentBranch.value === 'HEAD') return
  const key = branchActionKey('merge', branch)
  actionOutput.value = ''
  branchAction.value = key
  try {
    const result = await gitApi.action(props.workspaceId, 'merge', branchActionPayload(branch))
    if (promptFromActionResult(result, () => mergeBranch(branch))) return
    actionOutput.value = result.success
      ? `Merged ${branchDisplayName(branch)} into ${currentBranch.value}`
      : gitError(result)
    await refresh()
  } catch (err) {
    if (promptFromError(err, () => mergeBranch(branch))) return
    actionOutput.value = branchErrorMessage(err, 'Failed to merge branch')
  } finally {
    if (branchAction.value === key) branchAction.value = null
  }
}

function requestDeleteBranch(branch: GitBranch) {
  if (branch.current) return
  deleteBranchTarget.value = branch
}

async function deleteBranch(branch: GitBranch): Promise<boolean> {
  const key = branchActionKey('delete', branch)
  actionOutput.value = ''
  branchAction.value = key
  try {
    const result = await gitApi.action(props.workspaceId, 'delete-branch', branchActionPayload(branch))
    if (promptFromActionResult(result, async () => {
      const deleted = await deleteBranch(branch)
      if (deleted) cancelDeleteBranch()
    })) {
      return false
    }
    actionOutput.value = result.success ? `Deleted ${branchDisplayName(branch)}` : gitError(result)
    await refresh()
    return result.success
  } catch (err) {
    if (promptFromError(err, async () => {
      const deleted = await deleteBranch(branch)
      if (deleted) cancelDeleteBranch()
    })) {
      return false
    }
    actionOutput.value = branchErrorMessage(err, 'Failed to delete branch')
    return false
  } finally {
    if (branchAction.value === key) branchAction.value = null
  }
}

async function confirmDeleteBranch() {
  if (!deleteBranchTarget.value || deletingBranch.value) return
  deletingBranch.value = true
  const deleted = await deleteBranch(deleteBranchTarget.value)
  deletingBranch.value = false
  if (deleted) cancelDeleteBranch()
}

function cancelDeleteBranch() {
  if (deletingBranch.value) return
  deleteBranchTarget.value = null
}

function viewDiff(path: string, staged: boolean) {
  emit('workingFileSelect', { path, staged })
}

function statusIcon(status: string): string {
  switch (status) {
    case 'modified':
      return 'M'
    case 'added':
      return 'A'
    case 'deleted':
      return 'D'
    case 'renamed':
      return 'R'
    case 'copied':
      return 'C'
    case 'untracked':
      return 'U'
    default:
      return '?'
  }
}

function statusColor(status: string): string {
  switch (status) {
    case 'modified':
      return 'var(--accent-amber)'
    case 'added':
    case 'untracked':
      return 'var(--accent-green)'
    case 'deleted':
      return 'var(--accent-rose)'
    case 'renamed':
    case 'copied':
      return 'var(--accent-blue)'
    default:
      return 'var(--text-muted)'
  }
}



function fileName(path: string): string {
  return path.split('/').pop() || path
}

function dirName(path: string): string {
  const parts = path.split('/')
  return parts.length > 1 ? parts.slice(0, -1).join('/') + '/' : ''
}

// Auto-refresh
watch(activeTab, () => refresh())
watch(
  selectedCommitHash,
  () => {
    if (!props.selectedCommit) {
      commitFiles.value = []
      commitFilesError.value = ''
      return
    }

    activeTab.value = 'changes'
    void loadCommitFiles(props.selectedCommit)
  },
  { immediate: true }
)

onMounted(() => {
  refresh()
  connectWatcher()
})

onUnmounted(() => {
  if (refreshTimer) clearTimeout(refreshTimer)
})
</script>

<template>
  <div class="git-panel">
    <div class="git-header">
      <span class="git-title">
        <svg class="panel-icon" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
          <circle cx="18" cy="18" r="3" />
          <circle cx="6" cy="6" r="3" />
          <path d="M6 21V9a9 9 0 0 0 9 9" />
        </svg>
        Git
      </span>
      <span v-if="status?.branch" class="git-branch-badge">
        {{ status.branch }}
        <template v-if="status.ahead > 0">
          <span class="ahead-behind">↑{{ status.ahead }}</span>
        </template>
        <template v-if="status.behind > 0">
          <span class="ahead-behind">↓{{ status.behind }}</span>
        </template>
      </span>
      <div class="git-header-actions">
        <button class="git-icon-btn" @click="refresh()" title="Refresh" :disabled="loading">
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" :class="{ spinning: loading }">
            <path d="M21 12a9 9 0 1 1-9-9c2.52 0 4.93 1 6.74 2.74L21 8" />
            <path d="M21 3v5h-5" />
          </svg>
        </button>
      </div>
    </div>

    <!-- Not a repo -->
    <div v-if="status && !status.isRepo" class="git-empty">
      <svg width="32" height="32" viewBox="0 0 24 24" fill="none" stroke="var(--text-muted)" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round">
        <circle cx="18" cy="18" r="3" />
        <circle cx="6" cy="6" r="3" />
        <path d="M6 21V9a9 9 0 0 0 9 9" />
      </svg>
      <p>Not a git repository</p>
      <button class="git-btn git-btn--primary" @click="initRepo">Initialize Repository</button>
      <div class="git-clone-divider">or</div>
      <div class="git-clone-form">
        <input
          v-model="cloneUrl"
          type="text"
          class="git-clone-input"
          placeholder="https://github.com/user/repo.git"
          :disabled="cloning"
          @keydown.enter="cloneRepo"
        />
        <button
          class="git-btn git-btn--primary"
          :disabled="!cloneUrl.trim() || cloning"
          @click="cloneRepo"
        >
          {{ cloning ? 'Cloning…' : 'Clone' }}
        </button>
      </div>
    </div>

    <!-- Repo content -->
    <template v-else-if="status?.isRepo">
      <!-- Tabs -->
      <div class="git-tabs">
        <button
          class="git-tab"
          :class="{ active: activeTab === 'changes' }"
          @click="activeTab = 'changes'"
        >
          Changes
          <span v-if="changesTabCount > 0" class="git-tab-badge">{{ changesTabCount }}</span>
        </button>
        <button
          class="git-tab"
          :class="{ active: activeTab === 'branches' }"
          @click="activeTab = 'branches'"
        >
          Branches
        </button>
      </div>

      <!-- Changes tab -->
      <div v-if="activeTab === 'changes'" class="git-tab-content">
        <template v-if="viewingCommit && selectedCommit">
          <div class="git-commit-context">
            <div class="git-commit-context-main">
              <code>{{ selectedCommit.shortHash }}</code>
              <span>{{ selectedCommit.message }}</span>
            </div>
            <button class="git-icon-btn" title="Show working tree changes" @click="clearSelectedCommit">
              <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                <path d="M18 6 6 18" /><path d="m6 6 12 12" />
              </svg>
            </button>
          </div>

          <div v-if="commitFilesLoading" class="git-empty-small">
            <p>Loading commit files…</p>
          </div>
          <div v-else-if="commitFilesError" class="git-error">{{ commitFilesError }}</div>
          <div v-else-if="commitFiles.length > 0" class="git-section">
            <div class="git-section-header">
              <span>Changed Files</span>
            </div>
            <div
              v-for="file in commitFiles"
              :key="`${selectedCommit.hash}:${file.oldPath ?? ''}:${file.path}`"
              class="git-file"
              @click="selectCommitFile(file)"
            >
              <span class="git-file-status" :style="{ color: statusColor(file.status) }">
                {{ statusIcon(file.status) }}
              </span>
              <span class="git-file-name">{{ fileName(file.path) }}</span>
              <span class="git-file-dir">{{ dirName(file.path) }}</span>
            </div>
          </div>
          <div v-else class="git-empty-small">
            <p>No files changed in this commit</p>
          </div>
        </template>

        <template v-else>
          <!-- Staged files -->
          <div v-if="stagedFiles.length > 0" class="git-section">
            <div class="git-section-header">
              <span>Staged Changes</span>
              <button class="git-icon-btn" @click="unstageAll" title="Unstage all">
                <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round">
                  <path d="M5 12h14" />
                </svg>
              </button>
            </div>
            <div
              v-for="file in stagedFiles"
              :key="file.path + '-staged'"
              class="git-file"
              @click="viewDiff(file.path, true)"
            >
              <span class="git-file-status" :style="{ color: statusColor(file.status) }">
                {{ statusIcon(file.status) }}
              </span>
              <span class="git-file-name">{{ fileName(file.path) }}</span>
              <span class="git-file-dir">{{ dirName(file.path) }}</span>
              <div class="git-file-actions">
                <button class="git-icon-btn" @click.stop="unstageFile(file.path)" title="Unstage">
                  <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round">
                    <path d="M5 12h14" />
                  </svg>
                </button>
              </div>
            </div>
          </div>

          <!-- Unstaged files -->
          <div v-if="unstagedFiles.length > 0" class="git-section">
            <div class="git-section-header">
              <span>Changes</span>
              <button class="git-icon-btn" @click="stageAll" title="Stage all">
                <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round">
                  <path d="M12 5v14" /><path d="M5 12h14" />
                </svg>
              </button>
            </div>
            <div
              v-for="file in unstagedFiles"
              :key="file.path + '-unstaged'"
              class="git-file"
              @click="viewDiff(file.path, false)"
            >
              <span class="git-file-status" :style="{ color: statusColor(file.status) }">
                {{ statusIcon(file.status) }}
              </span>
              <span class="git-file-name">{{ fileName(file.path) }}</span>
              <span class="git-file-dir">{{ dirName(file.path) }}</span>
              <div class="git-file-actions">
                <button class="git-icon-btn" @click.stop="stageFile(file.path)" title="Stage">
                  <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round">
                    <path d="M12 5v14" /><path d="M5 12h14" />
                  </svg>
                </button>
                <button
                  v-if="file.status !== 'untracked'"
                  class="git-icon-btn git-icon-btn--danger"
                  @click.stop="discardFile(file.path)"
                  title="Discard changes"
                >
                  <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round">
                    <path d="m3 6 3 18h12l3-18" /><path d="M2 6h20" /><path d="m9 2 1-1h4l1 1" />
                  </svg>
                </button>
              </div>
            </div>
          </div>

          <!-- No changes -->
          <div v-if="!hasChanges && !loading" class="git-empty-small">
            <p>No changes</p>
          </div>

          <!-- Commit box -->
          <div class="git-commit-box">
            <textarea
              v-model="commitMsg"
              class="git-commit-input"
              placeholder="Commit message…"
              rows="2"
              @keydown.ctrl.enter="commit"
              @keydown.meta.enter="commit"
            />
            <button
              class="git-btn git-btn--primary git-btn--commit"
              :disabled="!canCommit"
              @click="commit"
            >
              Commit
            </button>
          </div>

          <!-- Action output -->
          <div v-if="actionOutput" class="git-output">
            <pre>{{ actionOutput }}</pre>
          </div>
        </template>
      </div>

      <!-- Branches tab -->
      <div v-if="activeTab === 'branches'" class="git-tab-content">
        <div class="git-section-header">
          <span>Branches</span>
          <button class="git-icon-btn" @click="showNewBranch = !showNewBranch" title="New branch">
            <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round">
              <path d="M12 5v14" /><path d="M5 12h14" />
            </svg>
          </button>
        </div>
        <div v-if="showNewBranch" class="git-new-branch">
          <input
            v-model="newBranchName"
            class="git-branch-input"
            placeholder="New branch name…"
            @keydown.enter="createBranch"
          />
          <button class="git-btn git-btn--small" @click="createBranch" :disabled="!newBranchName.trim()">
            Create
          </button>
        </div>
        <div v-if="localBranches.length === 0 && remoteBranchGroups.length === 0" class="git-empty-small">
          No branches found
        </div>

        <div v-if="localBranches.length" class="git-branch-group">
          <div class="git-branch-group-title">Local</div>
          <div
            v-for="b in localBranches"
            :key="b.name"
            class="git-branch-entry"
            :class="{ current: b.current }"
          >
            <svg v-if="b.current" width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="var(--accent-green)" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round">
              <path d="M20 6 9 17l-5-5" />
            </svg>
            <svg v-else width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="var(--text-muted)" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
              <line x1="6" x2="6" y1="3" y2="15" /><circle cx="18" cy="6" r="3" /><circle cx="6" cy="18" r="3" /><path d="M18 9a9 9 0 0 1-9 9" />
            </svg>
            <div class="git-branch-main">
              <span class="git-branch-name">{{ branchDisplayName(b) }}</span>
              <span class="git-branch-subtitle">{{ branchSubtitle(b) }}</span>
            </div>
            <span v-if="b.current" class="git-current-pill">Current</span>
            <div v-else class="git-branch-actions">
              <button
                class="git-branch-action"
                :disabled="b.current || branchAction !== null"
                title="Checkout branch"
                :aria-label="`Checkout ${branchDisplayName(b)}`"
                @click="checkoutBranch(b)"
              >
                <span v-if="isBranchActionRunning('checkout', b)" class="git-action-spinner" />
                <svg v-else width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.4" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
                  <path d="M15 3h4a2 2 0 0 1 2 2v4" />
                  <path d="m10 14 5-5-5-5" />
                  <path d="M15 9H3" />
                  <path d="M21 15v4a2 2 0 0 1-2 2h-4" />
                </svg>
              </button>
              <button
                class="git-branch-action"
                :disabled="b.current || !currentBranch || currentBranch === 'HEAD' || branchAction !== null"
                title="Merge into current branch"
                :aria-label="`Merge ${branchDisplayName(b)} into ${currentBranch}`"
                @click="mergeBranch(b)"
              >
                <span v-if="isBranchActionRunning('merge', b)" class="git-action-spinner" />
                <svg v-else width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.4" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
                  <circle cx="18" cy="18" r="3" />
                  <circle cx="6" cy="6" r="3" />
                  <path d="M6 21V9a9 9 0 0 0 9 9" />
                </svg>
              </button>
              <button
                class="git-branch-action git-branch-action--danger"
                :disabled="b.current || branchAction !== null"
                title="Delete branch"
                :aria-label="`Delete ${branchDisplayName(b)}`"
                @click="requestDeleteBranch(b)"
              >
                <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.4" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
                  <path d="M3 6h18" />
                  <path d="M8 6V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2" />
                  <path d="m19 6-1 14a2 2 0 0 1-2 2H8a2 2 0 0 1-2-2L5 6" />
                  <path d="M10 11v6" />
                  <path d="M14 11v6" />
                </svg>
              </button>
            </div>
          </div>
        </div>

        <div
          v-for="group in remoteBranchGroups"
          :key="group.remote"
          class="git-branch-group"
        >
          <div class="git-branch-group-title">
            <span>Remote</span>
            <span class="git-remote-label">{{ group.remote }}</span>
          </div>
          <div
            v-for="b in group.branches"
            :key="b.name"
            class="git-branch-entry remote"
          >
            <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="var(--text-muted)" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
              <circle cx="12" cy="12" r="10" /><path d="M2 12h20" /><path d="M12 2a15.3 15.3 0 0 1 4 10 15.3 15.3 0 0 1-4 10 15.3 15.3 0 0 1-4-10 15.3 15.3 0 0 1 4-10z" />
            </svg>
            <div class="git-branch-main">
              <span class="git-branch-name">{{ branchDisplayName(b) }}</span>
              <span class="git-branch-subtitle">{{ branchSubtitle(b) }}</span>
            </div>
            <div class="git-branch-actions">
              <button
                class="git-branch-action"
                :disabled="branchAction !== null"
                title="Checkout remote branch"
                :aria-label="`Checkout ${branchDisplayName(b)} from ${b.remoteName || 'remote'}`"
                @click="checkoutBranch(b)"
              >
                <span v-if="isBranchActionRunning('checkout', b)" class="git-action-spinner" />
                <svg v-else width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.4" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
                  <path d="M15 3h4a2 2 0 0 1 2 2v4" />
                  <path d="m10 14 5-5-5-5" />
                  <path d="M15 9H3" />
                  <path d="M21 15v4a2 2 0 0 1-2 2h-4" />
                </svg>
              </button>
              <button
                class="git-branch-action"
                :disabled="!currentBranch || currentBranch === 'HEAD' || branchAction !== null"
                title="Merge into current branch"
                :aria-label="`Merge ${branchDisplayName(b)} into ${currentBranch}`"
                @click="mergeBranch(b)"
              >
                <span v-if="isBranchActionRunning('merge', b)" class="git-action-spinner" />
                <svg v-else width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.4" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
                  <circle cx="18" cy="18" r="3" />
                  <circle cx="6" cy="6" r="3" />
                  <path d="M6 21V9a9 9 0 0 0 9 9" />
                </svg>
              </button>
              <button
                class="git-branch-action git-branch-action--danger"
                :disabled="branchAction !== null"
                title="Delete remote branch"
                :aria-label="`Delete ${branchDisplayName(b)} from ${b.remoteName || 'remote'}`"
                @click="requestDeleteBranch(b)"
              >
                <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.4" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
                  <path d="M3 6h18" />
                  <path d="M8 6V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2" />
                  <path d="m19 6-1 14a2 2 0 0 1-2 2H8a2 2 0 0 1-2-2L5 6" />
                  <path d="M10 11v6" />
                  <path d="M14 11v6" />
                </svg>
              </button>
            </div>
          </div>
        </div>

        <div v-if="actionOutput" class="git-output git-output--branches">
          <pre>{{ actionOutput }}</pre>
        </div>
      </div>

    </template>

    <!-- Error -->
    <div v-if="error" class="git-error">{{ error }}</div>

    <!-- Git config modal -->
    <GitConfigModal
      :show="showGitConfig"
      @submit="handleGitConfigSubmit"
      @cancel="showGitConfig = false"
    />
    <GitSshHostKeyModal
      :show="sshHostKey !== null"
      :host-key="sshHostKey"
      :loading="acceptingSshHostKey"
      :error="sshHostKeyError"
      @accept="acceptSshHostKey"
      @cancel="cancelSshHostKey"
    />
    <ConfirmModal
      :show="deleteBranchTarget !== null"
      title="Delete Branch"
      :message="deleteBranchMessage"
      confirm-label="Delete"
      variant="danger"
      :loading="deletingBranch"
      @confirm="confirmDeleteBranch"
      @cancel="cancelDeleteBranch"
    />
  </div>
</template>

<style scoped>
.git-panel {
  --ide-header-icon: var(--accent-purple);
  display: flex;
  flex-direction: column;
  height: 100%;
  overflow: hidden;
  position: relative;
  container-type: inline-size;
  background: var(--ide-panel-bg);
}

.git-header {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  padding: 0 var(--space-3);
  border-bottom: 0.5px solid var(--border-default);
  background: var(--ide-header-bg);
  height: 38px;
  flex-shrink: 0;
}

.git-title {
  display: flex;
  align-items: center;
  gap: 6px;
  font-size: var(--ide-header-title-size);
  font-weight: 600;
  color: var(--text-primary);
}

.panel-icon {
  color: var(--ide-header-icon);
  flex-shrink: 0;
}

.git-branch-badge {
  font-size: 10.5px;
  font-family: var(--font-mono);
  padding: 1px 6px;
  border-radius: var(--radius-sm);
  background: var(--bg-raised);
  color: var(--text-secondary);
  border: 0.5px solid var(--border-subtle);
}

.ahead-behind {
  color: var(--accent-amber);
  margin-left: 4px;
}

.git-header-actions {
  margin-left: auto;
  display: flex;
  gap: var(--space-1);
}

.git-icon-btn {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 22px;
  height: 22px;
  border-radius: var(--radius-sm);
  color: var(--text-secondary);
  transition: all var(--transition-fast);
}

.git-icon-btn:hover:not(:disabled) {
  background: var(--bg-hover);
  color: var(--text-primary);
}

.git-icon-btn:disabled {
  opacity: 0.4;
  cursor: default;
}

.git-icon-btn--danger:hover {
  color: var(--accent-rose) !important;
}

@keyframes spin {
  to { transform: rotate(360deg); }
}

.spinning {
  animation: spin 0.8s linear infinite;
}

/* Empty state */
.git-empty {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: var(--space-3);
  padding: var(--space-8);
  color: var(--text-muted);
  font-size: 0.8rem;
}

.git-clone-divider {
  font-size: 0.75rem;
  color: var(--text-muted);
  opacity: 0.6;
}

.git-clone-form {
  display: flex;
  gap: var(--space-2);
  width: 100%;
  max-width: 320px;
}

.git-clone-input {
  flex: 1;
  min-width: 0;
  padding: var(--space-1) var(--space-2);
  font-size: 0.75rem;
  font-family: var(--font-mono);
  color: var(--text-primary);
  background: var(--bg-primary);
  border: 0.5px solid var(--border-default);
  border-radius: var(--radius-md);
  outline: none;
  transition: border-color var(--transition-fast);
}

.git-clone-input:focus {
  border-color: var(--accent-blue);
}

.git-clone-input:disabled {
  opacity: 0.5;
}

.git-empty-small {
  padding: var(--space-4);
  text-align: center;
  color: var(--text-muted);
  font-size: 0.75rem;
}

/* Tabs */
.git-tabs {
  display: flex;
  border-bottom: 0.5px solid var(--border-default);
  flex-shrink: 0;
}

.git-tab {
  flex: 1;
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 4px;
  padding: var(--space-2) var(--space-2);
  font-size: 0.72rem;
  color: var(--text-muted);
  border-bottom: 2px solid transparent;
  transition: all var(--transition-fast);
}

.git-tab:hover {
  color: var(--text-secondary);
  background: var(--bg-hover);
}

.git-tab.active {
  color: var(--text-primary);
  border-bottom-color: var(--accent-blue);
}

.git-tab-badge {
  font-size: 0.72rem;
  min-width: 16px;
  height: 16px;
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 0 4px;
  border-radius: 8px;
  background: var(--accent-blue);
  color: var(--bg-primary);
  font-weight: 600;
}

.git-tab-content {
  flex: 1;
  overflow-y: auto;
}

.git-commit-context {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  padding: var(--space-2) var(--space-3);
  border-bottom: 0.5px solid var(--border-default);
  background: var(--bg-raised);
}

.git-commit-context-main {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.git-commit-context-main code {
  font-family: var(--font-mono);
  font-size: 0.72rem;
  color: var(--accent-blue);
}

.git-commit-context-main span {
  font-size: 0.75rem;
  color: var(--text-primary);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

/* Sections */
.git-section {
  border-bottom: 0.5px solid var(--border-default);
}

.git-section-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: var(--space-1) var(--space-3);
  font-size: 0.75rem;
  font-weight: 600;
  color: var(--text-secondary);
  text-transform: uppercase;
  letter-spacing: 0.5px;
}

/* File entries */
.git-file {
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 3px var(--space-3);
  cursor: pointer;
  transition: background var(--transition-fast);
}

.git-file:hover {
  background: var(--bg-hover);
}

.git-file:hover .git-file-actions {
  opacity: 1;
}

.git-file-status {
  font-family: var(--font-mono);
  font-size: 0.75rem;
  font-weight: 500;
  width: 14px;
  text-align: center;
  flex-shrink: 0;
}

.git-file-name {
  font-size: 0.75rem;
  color: var(--text-primary);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.git-file-dir {
  font-size: 0.72rem;
  color: var(--text-muted);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  flex: 1;
  min-width: 0;
}

.git-file-actions {
  display: flex;
  gap: 2px;
  opacity: 0;
  transition: opacity var(--transition-fast);
  flex-shrink: 0;
}

/* Commit box */
.git-commit-box {
  padding: var(--space-2) var(--space-3);
  border-top: 0.5px solid var(--border-default);
  flex-shrink: 0;
}

.git-commit-input {
  width: 100%;
  padding: var(--space-2);
  background: var(--bg-primary);
  color: var(--text-primary);
  border: 0.5px solid var(--border-default);
  border-radius: var(--radius-md);
  font-family: var(--font-sans);
  font-size: 0.75rem;
  resize: none;
  outline: none;
  transition: border-color var(--transition-fast);
}

.git-commit-input::placeholder {
  color: var(--text-muted);
}

.git-commit-input:focus {
  border-color: var(--accent-blue);
}

/* Buttons */
.git-btn {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  padding: var(--space-1) var(--space-3);
  font-size: 0.75rem;
  border-radius: var(--radius-md);
  border: 0.5px solid var(--border-default);
  color: var(--text-primary);
  background: var(--bg-surface-alt);
  transition: all var(--transition-fast);
}

.git-btn:hover:not(:disabled) {
  background: var(--bg-hover);
  border-color: var(--border-active);
}

.git-btn:disabled {
  opacity: 0.4;
  cursor: default;
}

.git-btn--primary {
  background: var(--accent-blue);
  color: var(--bg-primary);
  border-color: transparent;
  font-weight: 600;
}

.git-btn--primary:hover:not(:disabled) {
  background: color-mix(in srgb, var(--accent-blue) 85%, var(--gruvbox-fg0));
}

.git-btn--small {
  padding: 2px var(--space-2);
  font-size: 0.75rem;
}

.git-btn--commit {
  width: 100%;
  justify-content: center;
  margin-top: var(--space-2);
  padding: 6px;
}

/* Action output */
.git-output {
  padding: var(--space-2) var(--space-3);
}

.git-output pre {
  font-family: var(--font-mono);
  font-size: 0.75rem;
  color: var(--text-secondary);
  white-space: pre-wrap;
  word-break: break-all;
  background: var(--bg-primary);
  padding: var(--space-2);
  border-radius: var(--radius-sm);
  border: 0.5px solid var(--border-default);
  max-height: 100px;
  overflow-y: auto;
}

/* Branches */
.git-new-branch {
  display: flex;
  gap: var(--space-1);
  padding: var(--space-2) var(--space-3);
  border-bottom: 0.5px solid var(--border-default);
}

.git-branch-input {
  flex: 1;
  padding: 3px var(--space-2);
  background: var(--bg-primary);
  color: var(--text-primary);
  border: 0.5px solid var(--border-default);
  border-radius: var(--radius-sm);
  font-size: 0.72rem;
  outline: none;
}

.git-branch-input:focus {
  border-color: var(--accent-blue);
}

.git-branch-group {
  border-bottom: 0.5px solid var(--border-subtle);
}

.git-branch-group:last-child {
  border-bottom: 0;
}

.git-branch-group-title {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  padding: var(--space-2) var(--space-3) var(--space-1);
  font-size: 0.68rem;
  font-weight: 700;
  letter-spacing: 0.06em;
  text-transform: uppercase;
  color: var(--text-muted);
}

.git-remote-label {
  max-width: 120px;
  padding: 1px 5px;
  border: 0.5px solid var(--border-subtle);
  border-radius: var(--radius-sm);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  color: var(--accent-blue);
  background: var(--accent-glow);
  font-family: var(--font-mono);
  letter-spacing: 0;
  text-transform: none;
}

.git-branch-entry {
  display: grid;
  grid-template-columns: 14px minmax(0, 1fr) auto;
  align-items: center;
  column-gap: 7px;
  min-height: 40px;
  padding: 5px var(--space-2) 5px var(--space-3);
  border-left: 2px solid transparent;
  transition:
    background var(--transition-fast),
    border-color var(--transition-fast);
}

.git-branch-entry > svg {
  grid-column: 1;
  width: 13px;
  height: 13px;
  justify-self: center;
}

.git-branch-entry:hover {
  background: var(--bg-hover);
}

.git-branch-entry.current {
  cursor: default;
  border-left-color: var(--accent-green);
  background: color-mix(in srgb, var(--accent-green) 8%, transparent);
}

.git-branch-entry.remote {
  background: color-mix(in srgb, var(--bg-raised) 35%, transparent);
}

.git-branch-entry.remote:hover {
  background: var(--bg-hover);
}

.git-branch-main {
  display: flex;
  min-width: 0;
  flex-direction: column;
  gap: 1px;
}

.git-branch-name {
  font-size: 0.75rem;
  font-family: var(--font-mono);
  color: var(--text-primary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.git-branch-entry.current .git-branch-name {
  color: var(--accent-green);
  font-weight: 600;
}

.git-branch-subtitle {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  color: var(--text-muted);
  font-size: 0.68rem;
}

.git-current-pill {
  padding: 1px 5px;
  border: 0.5px solid color-mix(in srgb, var(--accent-green) 40%, transparent);
  border-radius: var(--radius-sm);
  color: var(--accent-green);
  background: color-mix(in srgb, var(--accent-green) 12%, transparent);
  font-size: 0.62rem;
  font-weight: 700;
  letter-spacing: 0.04em;
  text-transform: uppercase;
}

.git-branch-actions {
  display: flex;
  align-items: center;
  gap: 3px;
  opacity: 0.78;
  transition: opacity var(--transition-fast);
}

.git-branch-entry:hover .git-branch-actions,
.git-branch-entry:focus-within .git-branch-actions {
  opacity: 1;
}

.git-branch-action {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 22px;
  height: 22px;
  padding: 0;
  border: 0.5px solid var(--border-default);
  border-radius: var(--radius-sm);
  color: var(--text-secondary);
  background: var(--bg-surface-alt);
  transition: all var(--transition-fast);
}

.git-branch-action:hover:not(:disabled) {
  color: var(--text-primary);
  border-color: var(--accent-blue);
  background: var(--accent-glow);
  box-shadow: 0 0 0 1px color-mix(in srgb, var(--accent-blue) 18%, transparent);
}

.git-branch-action--danger:hover:not(:disabled) {
  color: var(--accent-rose);
  border-color: var(--accent-rose);
  background: color-mix(in srgb, var(--accent-rose) 12%, transparent);
  box-shadow: 0 0 0 1px color-mix(in srgb, var(--accent-rose) 18%, transparent);
}

.git-branch-action:disabled {
  opacity: 0.4;
  cursor: default;
}

.git-action-spinner {
  width: 12px;
  height: 12px;
  border: 1.5px solid var(--spinner-track);
  border-top-color: currentColor;
  border-radius: 50%;
  animation: spin 0.6s linear infinite;
}

.git-output--branches {
  border-top: 0.5px solid var(--border-default);
}

@container (max-width: 280px) {
  .git-branch-entry {
    grid-template-columns: 12px minmax(0, 1fr) auto;
    column-gap: 5px;
    padding-right: var(--space-1);
  }

  .git-branch-actions {
    gap: 2px;
  }

  .git-branch-action {
    width: 20px;
    height: 20px;
  }

  .git-current-pill {
    display: none;
  }
}

/* Error */
.git-error {
  padding: var(--space-3);
  font-size: 0.75rem;
  color: var(--accent-rose);
}
</style>
