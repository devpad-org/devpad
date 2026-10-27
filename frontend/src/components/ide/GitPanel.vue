<script setup lang="ts">
import { ref, computed, inject, onMounted, onUnmounted, watch } from 'vue'
import {
  gitApi,
  type GitActionResult,
  type GitStatus,
  type GitFileStatus,
  type GitCommit,
  type GitCommitFile,
} from '@/api/git'
import { useFileWatcher } from '@/composables/useFileWatcher'
import { useGitSshHostKey } from '@/composables/useGitSshHostKey'
import { timeAgo } from '@/utils/time'
import ConfirmModal from '@/components/ConfirmModal.vue'
import ProjectBranchActions from '@/components/project/ProjectBranchActions.vue'
import GitBranchPicker from './GitBranchPicker.vue'
import GitConfigModal from './GitConfigModal.vue'
import GitSshHostKeyModal from './GitSshHostKeyModal.vue'
import { ideShellKey, type BranchChange } from './ideShell'
import type { GitNotice } from './gitNotice'

// The Source Control side bar: the current branch, how it stands against the
// project in a project workspace, and the working tree's changes with the
// box to commit them. A commit picked in the Git Graph shows its files here.
const props = withDefaults(defineProps<{
  workspaceId: number
  selectedCommit: GitCommit | null
  // The diff open in the Git Graph, so its file shows as selected.
  activeDiff?: { kind: 'working'; path: string; staged: boolean } | { kind: 'commit'; path: string } | null
  projectMode?: boolean
  // The panel is showing.
  active?: boolean
  // Bumped by the shell whenever the workspace's branch may have moved.
  revision?: number
}>(), {
  activeDiff: null,
  projectMode: false,
  active: true,
  revision: 0,
})

const emit = defineEmits<{
  (e: 'commitFileSelect', payload: { commit: GitCommit; file: GitCommitFile }): void
  (e: 'workingFileSelect', payload: { path: string; staged: boolean }): void
  (e: 'clearCommit'): void
  (e: 'changed', kind: BranchChange): void
}>()

const NOTICE_MS = 5000

const shell = inject(ideShellKey, null)

const loading = ref(false)
const error = ref('')
const status = ref<GitStatus | null>(null)
const commitMsg = ref('')
const committing = ref(false)
const notice = ref<GitNotice | null>(null)
const picker = ref<InstanceType<typeof GitBranchPicker> | null>(null)
// Bumped after this panel moves HEAD, so the project card compares again.
const localRevision = ref(0)
const collapsed = ref({ staged: false, changes: false })
const busyPaths = ref(new Set<string>())
const discardTarget = ref<GitFileStatus | null>(null)

// Commit selection
const commitFiles = ref<GitCommitFile[]>([])
const commitFilesLoading = ref(false)
const commitFilesError = ref('')

// Git config modal
const showGitConfig = ref(false)
const pendingCommitAll = ref(false)
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
let noticeTimer: ReturnType<typeof setTimeout> | null = null

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
const branch = computed(() => status.value?.branch ?? '')

const hasChanges = computed(() => (status.value?.files?.length ?? 0) > 0)
const commitsAll = computed(() => stagedFiles.value.length === 0 && unstagedFiles.value.length > 0)
const canCommit = computed(() => hasChanges.value && commitMsg.value.trim() !== '' && !committing.value)
const commitLabel = computed(() => {
  if (committing.value) return 'Committing…'
  return commitsAll.value ? 'Commit all' : 'Commit'
})
const commitCount = computed(() => (commitsAll.value ? unstagedFiles.value.length : stagedFiles.value.length))
const commitPlaceholder = `Message (${isMac() ? '⌘' : 'Ctrl'}+Enter to commit)`
const discardMessage = computed(() =>
  discardTarget.value ? `Discard your changes to ${discardTarget.value.path}? This can't be undone.` : ''
)

function isMac(): boolean {
  return typeof navigator !== 'undefined' && /Mac|iPhone|iPad/.test(navigator.platform)
}

// --- Notices ---
function notify(next: GitNotice) {
  if (noticeTimer) clearTimeout(noticeTimer)
  notice.value = next
  noticeTimer = next.tone === 'error' ? null : setTimeout(() => (notice.value = null), NOTICE_MS)
}

function dismissNotice() {
  if (noticeTimer) clearTimeout(noticeTimer)
  notice.value = null
}

function failure(result: GitActionResult, text: string) {
  notify({ tone: 'error', text, detail: result.output || result.error || undefined })
}

// --- Actions ---
async function refresh(showLoading = true) {
  if (!props.workspaceId) return
  if (showLoading) loading.value = true
  error.value = ''
  try {
    status.value = await gitApi.status(props.workspaceId)
  } catch (e: any) {
    error.value = e.message || 'Failed to load git status'
  } finally {
    loading.value = false
  }
}

function branchChanged(kind: BranchChange) {
  localRevision.value++
  emit('changed', kind)
  void refresh(false)
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

// Runs a per-file action, marking the file busy so a double click can't
// send it twice.
async function fileAction(path: string, action: string, failText: string) {
  if (busyPaths.value.has(path)) return
  busyPaths.value = new Set(busyPaths.value).add(path)
  try {
    const result = await gitApi.action(props.workspaceId, action, { files: [path] })
    if (!result.success) failure(result, failText)
  } catch (e) {
    notify({ tone: 'error', text: e instanceof Error ? e.message : failText })
  } finally {
    const next = new Set(busyPaths.value)
    next.delete(path)
    busyPaths.value = next
    await refresh(false)
  }
}

function stageFile(path: string) {
  return fileAction(path, 'stage', `Couldn't stage ${path}`)
}

function unstageFile(path: string) {
  return fileAction(path, 'unstage', `Couldn't unstage ${path}`)
}

async function confirmDiscard() {
  const file = discardTarget.value
  discardTarget.value = null
  if (file) await fileAction(file.path, 'discard', `Couldn't discard ${file.path}`)
}

async function bulkAction(action: 'stage' | 'unstage') {
  try {
    const result = await gitApi.action(props.workspaceId, action)
    if (!result.success) failure(result, action === 'stage' ? "Couldn't stage the changes" : "Couldn't unstage the changes")
  } catch (e) {
    notify({ tone: 'error', text: e instanceof Error ? e.message : 'Git action failed' })
  }
  await refresh(false)
}

async function commit() {
  if (!canCommit.value) return
  // Prompt for git config if not set
  if (needsGitConfig.value) {
    pendingCommitAll.value = commitsAll.value
    showGitConfig.value = true
    return
  }
  await doCommit(commitsAll.value)
}

async function doCommit(stageAll: boolean) {
  const message = commitMsg.value.trim()
  if (!message) return
  committing.value = true
  try {
    if (stageAll) {
      const staged = await gitApi.action(props.workspaceId, 'stage')
      if (!staged.success) {
        failure(staged, "Couldn't stage the changes")
        return
      }
    }
    const result = await gitApi.action(props.workspaceId, 'commit', { message })
    if (!result.success) {
      failure(result, 'Commit failed')
      return
    }
    commitMsg.value = ''
    const hash = /\[[^\]]*?\s([0-9a-f]{7,})\]/.exec(result.output)?.[1]
    const subject = message.split('\n')[0]
    notify({ tone: 'success', text: hash ? `Committed ${hash.slice(0, 7)} · ${subject}` : `Committed · ${subject}` })
    branchChanged('head')
  } catch (e) {
    notify({ tone: 'error', text: e instanceof Error ? e.message : 'Commit failed' })
  } finally {
    committing.value = false
    await refresh(false)
  }
}

async function handleGitConfigSubmit(name: string, email: string) {
  showGitConfig.value = false
  await gitApi.action(props.workspaceId, 'set-config', { userName: name, userEmail: email })
  await refresh(false)
  await doCommit(pendingCommitAll.value)
}

async function initRepo() {
  const result = await gitApi.action(props.workspaceId, 'init')
  if (result.success) notify({ tone: 'success', text: 'Repository initialized' })
  else failure(result, "Couldn't initialize the repository")
  await refresh()
}

const cloneUrl = ref('')
const cloning = ref(false)

async function cloneRepo() {
  const url = cloneUrl.value.trim()
  if (!url) return
  cloning.value = true
  try {
    const result = await gitApi.action(props.workspaceId, 'clone', { url })
    if (promptFromActionResult(result, cloneRepo)) return
    if (result.success) {
      cloneUrl.value = ''
      notify({ tone: 'success', text: 'Repository cloned' })
    } else {
      failure(result, "Couldn't clone the repository")
    }
    await refresh()
  } catch (e) {
    if (promptFromError(e, cloneRepo)) return
    notify({ tone: 'error', text: e instanceof Error ? e.message : 'Failed to clone repository' })
  } finally {
    cloning.value = false
  }
}

function viewDiff(path: string, staged: boolean) {
  emit('workingFileSelect', { path, staged })
}

function isActiveWorking(file: GitFileStatus): boolean {
  const d = props.activeDiff
  return !viewingCommit.value && d?.kind === 'working' && d.path === file.path && d.staged === file.staged
}

function isActiveCommitFile(file: GitCommitFile): boolean {
  const d = props.activeDiff
  return d?.kind === 'commit' && d.path === file.path
}

function statusLetter(status: string): string {
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

function statusTitle(status: string): string {
  return status === 'untracked' ? 'Untracked' : status.charAt(0).toUpperCase() + status.slice(1)
}

function fileName(path: string): string {
  return path.split('/').pop() || path
}

function dirName(path: string): string {
  const parts = path.split('/')
  return parts.length > 1 ? parts.slice(0, -1).join('/') : ''
}

// Auto-refresh
watch(() => props.revision, () => void refresh(false))
watch(() => props.active, (active) => {
  if (active) void refresh(false)
})
watch(
  selectedCommitHash,
  () => {
    if (!props.selectedCommit) {
      commitFiles.value = []
      commitFilesError.value = ''
      return
    }
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
  if (noticeTimer) clearTimeout(noticeTimer)
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
        Source Control
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
      <span class="git-empty-icon">
        <svg width="22" height="22" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round">
          <circle cx="18" cy="18" r="3" />
          <circle cx="6" cy="6" r="3" />
          <path d="M6 21V9a9 9 0 0 0 9 9" />
        </svg>
      </span>
      <p class="git-empty-title">No repository here yet</p>
      <p class="git-empty-text">Start one in this workspace, or clone an existing one.</p>
      <button class="git-btn git-btn--primary git-btn--wide" @click="initRepo">Initialize repository</button>
      <div class="git-clone-divider"><span>or clone</span></div>
      <form class="git-clone-form" @submit.prevent="cloneRepo">
        <input
          v-model="cloneUrl"
          type="text"
          class="git-input git-input--mono"
          placeholder="https://github.com/user/repo.git"
          :disabled="cloning"
        />
        <button type="submit" class="git-btn" :disabled="!cloneUrl.trim() || cloning">
          {{ cloning ? 'Cloning…' : 'Clone' }}
        </button>
      </form>
      <div v-if="notice" class="git-notice" :class="`git-notice--${notice.tone}`" role="status">
        <span class="git-notice-text">{{ notice.text }}</span>
        <pre v-if="notice.detail" class="git-notice-detail">{{ notice.detail }}</pre>
      </div>
    </div>

    <!-- Repo content -->
    <template v-else-if="status?.isRepo">
      <div class="git-top">
        <GitBranchPicker
          ref="picker"
          :workspace-id="workspaceId"
          :current="branch"
          :ahead="status.ahead"
          :behind="status.behind"
          :project-mode="projectMode"
          @changed="branchChanged"
          @notify="notify"
        />
        <ProjectBranchActions
          v-if="projectMode"
          :workspace-id="workspaceId"
          :branch="branch"
          :active="active"
          :revision="revision + localRevision"
          @changed="branchChanged"
          @notify="notify"
          @new-branch="picker?.show('create')"
        />
        <Transition name="notice">
          <div v-if="notice" class="git-notice" :class="`git-notice--${notice.tone}`" role="status" aria-live="polite">
            <svg v-if="notice.tone === 'error'" class="git-notice-icon" width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
              <circle cx="12" cy="12" r="9" /><path d="M12 8v4" /><path d="M12 16h.01" />
            </svg>
            <svg v-else-if="notice.tone === 'success'" class="git-notice-icon" width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.6" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
              <path d="M20 6 9 17l-5-5" />
            </svg>
            <svg v-else class="git-notice-icon" width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
              <circle cx="12" cy="12" r="9" /><path d="M12 16v-4" /><path d="M12 8h.01" />
            </svg>
            <div class="git-notice-body">
              <span class="git-notice-text">{{ notice.text }}</span>
              <pre v-if="notice.detail" class="git-notice-detail">{{ notice.detail }}</pre>
            </div>
            <button class="git-notice-close" type="button" aria-label="Dismiss" @click="dismissNotice">
              <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round"><path d="M18 6 6 18" /><path d="m6 6 12 12" /></svg>
            </button>
          </div>
        </Transition>
      </div>

      <div class="git-body">
        <!-- A commit picked in the Git Graph -->
        <template v-if="viewingCommit && selectedCommit">
          <div class="git-commit-card">
            <div class="git-commit-card-head">
              <span class="git-commit-card-label">Commit</span>
              <code class="git-hash">{{ selectedCommit.shortHash }}</code>
              <button class="git-back" type="button" title="Back to your changes" @click="clearSelectedCommit">
                <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
                  <path d="m15 18-6-6 6-6" />
                </svg>
                Changes
              </button>
            </div>
            <p class="git-commit-card-message">{{ selectedCommit.message }}</p>
            <p class="git-commit-card-meta">{{ selectedCommit.author }} · {{ timeAgo(selectedCommit.timestamp) }}</p>
          </div>

          <div v-if="commitFilesLoading" class="git-placeholder">Loading files…</div>
          <div v-else-if="commitFilesError" class="git-inline-error">{{ commitFilesError }}</div>
          <section v-else-if="commitFiles.length > 0" class="git-section">
            <div class="git-section-header git-section-header--static">
              <span class="git-section-title">Files</span>
              <span class="git-count">{{ commitFiles.length }}</span>
            </div>
            <div
              v-for="file in commitFiles"
              :key="`${selectedCommit.hash}:${file.oldPath ?? ''}:${file.path}`"
              class="git-file"
              :class="{ active: isActiveCommitFile(file) }"
              :title="file.oldPath ? `${file.oldPath} → ${file.path}` : file.path"
              @click="selectCommitFile(file)"
            >
              <span class="git-file-status" :class="`st-${file.status}`" :title="statusTitle(file.status)">{{ statusLetter(file.status) }}</span>
              <span class="git-file-name" :class="{ deleted: file.status === 'deleted' }">{{ fileName(file.path) }}</span>
              <span class="git-file-dir">{{ dirName(file.path) }}</span>
            </div>
          </section>
          <div v-else class="git-placeholder">No files changed in this commit</div>
        </template>

        <!-- The working tree -->
        <template v-else>
          <div v-if="hasChanges" class="git-composer">
            <textarea
              v-model="commitMsg"
              class="git-input git-composer-input"
              :placeholder="commitPlaceholder"
              rows="2"
              aria-label="Commit message"
              @keydown.ctrl.enter.prevent="commit"
              @keydown.meta.enter.prevent="commit"
            />
            <button
              class="git-btn git-btn--primary git-btn--commit"
              :disabled="!canCommit"
              :title="!commitMsg.trim() ? 'Write a commit message first' : commitsAll ? 'Stage every change and commit it' : 'Commit the staged changes'"
              @click="commit"
            >
              <span v-if="committing" class="git-spinner" />
              <svg v-else width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.6" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
                <path d="M20 6 9 17l-5-5" />
              </svg>
              {{ commitLabel }}
              <span class="git-btn-count">{{ commitCount }}</span>
            </button>
          </div>

          <!-- Staged files -->
          <section v-if="stagedFiles.length > 0" class="git-section">
            <div class="git-section-header" @click="collapsed.staged = !collapsed.staged">
              <svg class="git-chevron" :class="{ collapsed: collapsed.staged }" width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
                <path d="m6 9 6 6 6-6" />
              </svg>
              <span class="git-section-title">Staged</span>
              <span class="git-count git-count--staged">{{ stagedFiles.length }}</span>
              <div class="git-section-actions">
                <button class="git-icon-btn" title="Unstage all" @click.stop="bulkAction('unstage')">
                  <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.4" stroke-linecap="round" stroke-linejoin="round"><path d="M5 12h14" /></svg>
                </button>
              </div>
            </div>
            <template v-if="!collapsed.staged">
              <div
                v-for="file in stagedFiles"
                :key="file.path + '-staged'"
                class="git-file"
                :class="{ active: isActiveWorking(file), busy: busyPaths.has(file.path) }"
                :title="file.path"
                @click="viewDiff(file.path, true)"
              >
                <span class="git-file-status" :class="`st-${file.status}`" :title="statusTitle(file.status)">{{ statusLetter(file.status) }}</span>
                <span class="git-file-name" :class="{ deleted: file.status === 'deleted' }">{{ fileName(file.path) }}</span>
                <span class="git-file-dir">{{ dirName(file.path) }}</span>
                <div class="git-file-actions">
                  <button v-if="shell && file.status !== 'deleted'" class="git-icon-btn" title="Open file" @click.stop="shell.openFile(file.path)">
                    <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z" /><path d="M14 2v6h6" /></svg>
                  </button>
                  <button class="git-icon-btn" title="Unstage" @click.stop="unstageFile(file.path)">
                    <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.4" stroke-linecap="round" stroke-linejoin="round"><path d="M5 12h14" /></svg>
                  </button>
                </div>
              </div>
            </template>
          </section>

          <!-- Unstaged files -->
          <section v-if="unstagedFiles.length > 0" class="git-section">
            <div class="git-section-header" @click="collapsed.changes = !collapsed.changes">
              <svg class="git-chevron" :class="{ collapsed: collapsed.changes }" width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
                <path d="m6 9 6 6 6-6" />
              </svg>
              <span class="git-section-title">Changes</span>
              <span class="git-count">{{ unstagedFiles.length }}</span>
              <div class="git-section-actions">
                <button class="git-icon-btn" title="Stage all" @click.stop="bulkAction('stage')">
                  <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.4" stroke-linecap="round" stroke-linejoin="round"><path d="M12 5v14" /><path d="M5 12h14" /></svg>
                </button>
              </div>
            </div>
            <template v-if="!collapsed.changes">
              <div
                v-for="file in unstagedFiles"
                :key="file.path + '-unstaged'"
                class="git-file"
                :class="{ active: isActiveWorking(file), busy: busyPaths.has(file.path) }"
                :title="file.path"
                @click="viewDiff(file.path, false)"
              >
                <span class="git-file-status" :class="`st-${file.status}`" :title="statusTitle(file.status)">{{ statusLetter(file.status) }}</span>
                <span class="git-file-name" :class="{ deleted: file.status === 'deleted' }">{{ fileName(file.path) }}</span>
                <span class="git-file-dir">{{ dirName(file.path) }}</span>
                <div class="git-file-actions">
                  <button v-if="shell && file.status !== 'deleted'" class="git-icon-btn" title="Open file" @click.stop="shell.openFile(file.path)">
                    <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z" /><path d="M14 2v6h6" /></svg>
                  </button>
                  <button
                    v-if="file.status !== 'untracked'"
                    class="git-icon-btn git-icon-btn--danger"
                    title="Discard changes"
                    @click.stop="discardTarget = file"
                  >
                    <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M3 7v6h6" /><path d="M21 17a9 9 0 0 0-9-9 9 9 0 0 0-6 2.3L3 13" /></svg>
                  </button>
                  <button class="git-icon-btn" title="Stage" @click.stop="stageFile(file.path)">
                    <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.4" stroke-linecap="round" stroke-linejoin="round"><path d="M12 5v14" /><path d="M5 12h14" /></svg>
                  </button>
                </div>
              </div>
            </template>
          </section>

          <!-- No changes -->
          <div v-if="!hasChanges && !loading" class="git-clean">
            <span class="git-clean-icon">
              <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
                <path d="M20 6 9 17l-5-5" />
              </svg>
            </span>
            <p class="git-clean-title">No changes</p>
            <p class="git-clean-text">Edits you make in this workspace show up here, ready to commit.</p>
          </div>
        </template>
      </div>
    </template>

    <div v-else-if="!error" class="git-placeholder">Loading…</div>

    <!-- Error -->
    <div v-if="error" class="git-inline-error git-inline-error--panel">
      <span>{{ error }}</span>
      <button class="git-btn git-btn--small" @click="refresh()">Retry</button>
    </div>

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
      :show="discardTarget !== null"
      title="Discard changes"
      :message="discardMessage"
      confirm-label="Discard"
      variant="danger"
      @confirm="confirmDiscard"
      @cancel="discardTarget = null"
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

.git-icon-btn--danger:hover:not(:disabled) {
  color: var(--accent-rose);
  background: var(--error-bg);
}

.spinning {
  animation: spin 0.8s linear infinite;
}

/* Branch, project and notices */
.git-top {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
  padding: 10px var(--space-3);
  border-bottom: 0.5px solid var(--border-default);
  flex-shrink: 0;
}

.git-notice {
  display: flex;
  align-items: flex-start;
  gap: var(--space-2);
  padding: 6px 6px 6px 10px;
  border: 0.5px solid var(--border-default);
  border-radius: var(--radius-md);
  background: var(--bg-elevated);
  font-size: 0.72rem;
  color: var(--text-secondary);
}

.git-notice--success {
  border-color: var(--success-border);
  background: var(--success-bg);
}

.git-notice--error {
  border-color: var(--error-border);
  background: var(--error-bg);
}

.git-notice-icon {
  flex-shrink: 0;
  margin-top: 2px;
}

.git-notice--success .git-notice-icon {
  color: var(--success);
}

.git-notice--error .git-notice-icon {
  color: var(--error);
}

.git-notice--info .git-notice-icon {
  color: var(--accent-blue);
}

.git-notice-body {
  display: flex;
  flex: 1;
  flex-direction: column;
  gap: 4px;
  min-width: 0;
}

.git-notice-text {
  color: var(--text-primary);
  line-height: 1.45;
  overflow-wrap: anywhere;
}

.git-notice-detail {
  max-height: 96px;
  overflow: auto;
  padding: 6px var(--space-2);
  border-radius: var(--radius-sm);
  background: var(--bg-void);
  color: var(--text-secondary);
  font-family: var(--font-mono);
  font-size: 0.66rem;
  white-space: pre-wrap;
  word-break: break-word;
}

.git-notice-close {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 20px;
  height: 20px;
  flex-shrink: 0;
  border-radius: var(--radius-sm);
  color: var(--text-muted);
  transition: color var(--transition-fast), background var(--transition-fast);
}

.git-notice-close:hover {
  background: var(--bg-hover);
  color: var(--text-primary);
}

.notice-enter-active,
.notice-leave-active {
  transition: opacity var(--transition-normal), transform var(--transition-normal);
}

.notice-enter-from,
.notice-leave-to {
  opacity: 0;
  transform: translateY(-3px);
}

/* Body */
.git-body {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  padding-bottom: var(--space-3);
}

/* Commit composer */
.git-composer {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
  padding: var(--space-3) var(--space-3) var(--space-2);
}

.git-input {
  width: 100%;
  padding: 6px var(--space-2);
  background: var(--bg-void);
  color: var(--text-primary);
  border: 0.5px solid var(--border-default);
  border-radius: var(--radius-md);
  font-family: var(--font-sans);
  font-size: 0.75rem;
  outline: none;
  transition: border-color var(--transition-fast), box-shadow var(--transition-fast);
}

.git-input::placeholder {
  color: var(--text-muted);
}

.git-input:focus {
  border-color: var(--accent-border);
  box-shadow: 0 0 0 3px var(--accent-glow);
}

.git-input:disabled {
  opacity: 0.5;
}

.git-input--mono {
  font-family: var(--font-mono);
}

.git-composer-input {
  resize: none;
  min-height: 52px;
  max-height: 180px;
  line-height: 1.45;
  field-sizing: content;
}

/* Buttons */
.git-btn {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 6px;
  height: 28px;
  padding: 0 var(--space-3);
  font-size: 0.74rem;
  border-radius: var(--radius-md);
  border: 0.5px solid var(--border-default);
  color: var(--text-primary);
  background: var(--bg-raised);
  white-space: nowrap;
  transition: all var(--transition-fast);
}

.git-btn:hover:not(:disabled) {
  background: var(--bg-hover);
  border-color: var(--border-active);
}

.git-btn:disabled {
  opacity: 0.45;
  cursor: default;
}

.git-btn--primary {
  background: var(--accent-blue);
  color: var(--text-on-accent);
  border-color: transparent;
  font-weight: 600;
}

.git-btn--primary:hover:not(:disabled) {
  background: color-mix(in srgb, var(--accent-blue) 85%, var(--gruvbox-fg0));
  border-color: transparent;
}

.git-btn--small {
  height: 24px;
  padding: 0 var(--space-2);
  font-size: 0.72rem;
}

.git-btn--wide {
  width: 100%;
  max-width: 260px;
}

.git-btn--commit {
  width: 100%;
  height: 30px;
}

.git-btn--commit:disabled {
  opacity: 1;
  background: var(--bg-raised);
  color: var(--text-muted);
}

.git-btn-count {
  min-width: 18px;
  padding: 0 5px;
  border-radius: 8px;
  background: color-mix(in srgb, currentColor 18%, transparent);
  font-size: 0.66rem;
  line-height: 16px;
}

.git-spinner {
  width: 11px;
  height: 11px;
  border: 1.5px solid color-mix(in srgb, currentColor 30%, transparent);
  border-top-color: currentColor;
  border-radius: 50%;
  animation: spin 0.6s linear infinite;
}

/* Sections */
.git-section {
  padding-top: var(--space-1);
}

.git-section-header {
  position: sticky;
  top: 0;
  z-index: 1;
  display: flex;
  align-items: center;
  gap: 6px;
  height: 26px;
  padding: 0 var(--space-2) 0 var(--space-3);
  background: var(--ide-panel-bg);
  cursor: pointer;
  user-select: none;
}

.git-section-header--static {
  cursor: default;
}

.git-section-title {
  font-size: 0.66rem;
  font-weight: 600;
  letter-spacing: 0.06em;
  text-transform: uppercase;
  color: var(--text-secondary);
}

.git-count {
  min-width: 18px;
  padding: 0 5px;
  border-radius: 8px;
  background: var(--bg-neutral);
  color: var(--text-secondary);
  font-size: 0.64rem;
  font-weight: 600;
  line-height: 16px;
  text-align: center;
}

.git-count--staged {
  background: color-mix(in srgb, var(--accent-green) 16%, transparent);
  color: var(--accent-green);
}

.git-chevron {
  flex-shrink: 0;
  color: var(--text-muted);
  transition: transform var(--transition-fast);
}

.git-chevron.collapsed {
  transform: rotate(-90deg);
}

.git-section-actions {
  display: flex;
  gap: 2px;
  margin-left: auto;
  opacity: 0;
  transition: opacity var(--transition-fast);
}

.git-section-header:hover .git-section-actions,
.git-section-header:focus-within .git-section-actions {
  opacity: 1;
}

/* File entries */
.git-file {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  height: 24px;
  padding: 0 var(--space-2) 0 var(--space-3);
  cursor: pointer;
  transition: background var(--transition-fast);
}

.git-file:hover {
  background: var(--bg-hover);
}

.git-file.active {
  background: var(--bg-selected);
  box-shadow: inset 2px 0 0 var(--accent);
}

.git-file.busy {
  opacity: 0.55;
  pointer-events: none;
}

.git-file:hover .git-file-actions,
.git-file:focus-within .git-file-actions {
  opacity: 1;
}

.git-file-status {
  width: 12px;
  flex-shrink: 0;
  font-family: var(--font-mono);
  font-size: 0.7rem;
  font-weight: 600;
  text-align: center;
  color: var(--text-muted);
}

.st-modified {
  color: var(--accent-amber);
}

.st-added,
.st-untracked {
  color: var(--accent-green);
}

.st-deleted {
  color: var(--accent-rose);
}

.st-renamed,
.st-copied {
  color: var(--accent-blue);
}

.git-file-name {
  flex-shrink: 0;
  max-width: 70%;
  font-size: 0.75rem;
  color: var(--text-primary);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.git-file-name.deleted {
  text-decoration: line-through;
  text-decoration-color: var(--text-decoration-muted);
  color: var(--text-secondary);
}

.git-file-dir {
  flex: 1;
  min-width: 0;
  font-size: 0.7rem;
  color: var(--text-muted);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.git-file-actions {
  display: flex;
  gap: 1px;
  margin-left: auto;
  opacity: 0;
  transition: opacity var(--transition-fast);
  flex-shrink: 0;
}

/* Commit picked in the graph */
.git-commit-card {
  margin: var(--space-3) var(--space-3) var(--space-1);
  padding: var(--space-2) 10px;
  border: 0.5px solid var(--border-default);
  border-radius: var(--radius-md);
  background: var(--bg-elevated);
}

.git-commit-card-head {
  display: flex;
  align-items: center;
  gap: var(--space-2);
}

.git-commit-card-label {
  font-size: 0.64rem;
  font-weight: 600;
  letter-spacing: 0.06em;
  text-transform: uppercase;
  color: var(--text-muted);
}

.git-hash {
  padding: 0 5px;
  border-radius: var(--radius-sm);
  background: color-mix(in srgb, var(--accent-blue) 14%, transparent);
  color: var(--accent-blue);
  font-size: 0.68rem;
  line-height: 18px;
}

.git-back {
  display: inline-flex;
  align-items: center;
  gap: 2px;
  margin-left: auto;
  height: 22px;
  padding: 0 6px 0 4px;
  border-radius: var(--radius-sm);
  color: var(--text-secondary);
  font-size: 0.7rem;
  transition: color var(--transition-fast), background var(--transition-fast);
}

.git-back:hover {
  background: var(--bg-hover);
  color: var(--text-primary);
}

.git-commit-card-message {
  margin-top: 6px;
  font-size: 0.76rem;
  color: var(--text-primary);
  line-height: 1.4;
  overflow-wrap: anywhere;
  display: -webkit-box;
  -webkit-line-clamp: 3;
  -webkit-box-orient: vertical;
  overflow: hidden;
}

.git-commit-card-meta {
  margin-top: 2px;
  font-size: 0.68rem;
  color: var(--text-muted);
}

/* Empty and clean states */
.git-empty {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: var(--space-2);
  padding: var(--space-8) var(--space-4);
  text-align: center;
  overflow-y: auto;
}

.git-empty-icon,
.git-clean-icon {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  border-radius: 50%;
}

.git-empty-icon {
  width: 44px;
  height: 44px;
  margin-bottom: var(--space-1);
  background: color-mix(in srgb, var(--accent-purple) 14%, transparent);
  color: var(--accent-purple);
}

.git-empty-title,
.git-clean-title {
  font-size: 0.8rem;
  font-weight: 500;
  color: var(--text-primary);
}

.git-empty-text,
.git-clean-text {
  max-width: 240px;
  font-size: 0.72rem;
  line-height: 1.45;
  color: var(--text-muted);
}

.git-empty-text {
  margin-bottom: var(--space-2);
}

.git-clone-divider {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  width: 100%;
  max-width: 260px;
  margin: var(--space-2) 0 var(--space-1);
  font-size: 0.66rem;
  text-transform: uppercase;
  letter-spacing: 0.06em;
  color: var(--text-muted);
}

.git-clone-divider::before,
.git-clone-divider::after {
  content: '';
  flex: 1;
  border-top: 0.5px solid var(--border-default);
}

.git-clone-form {
  display: flex;
  gap: var(--space-1);
  width: 100%;
  max-width: 260px;
}

.git-clone-form .git-input {
  flex: 1;
  min-width: 0;
  height: 28px;
}

.git-empty .git-notice {
  width: 100%;
  max-width: 260px;
  margin-top: var(--space-2);
  text-align: left;
}

.git-clean {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 6px;
  padding: var(--space-8) var(--space-4);
  text-align: center;
}

.git-clean-icon {
  width: 36px;
  height: 36px;
  margin-bottom: 2px;
  background: var(--success-bg);
  color: var(--accent-green);
}

.git-placeholder {
  padding: var(--space-4);
  text-align: center;
  color: var(--text-muted);
  font-size: 0.74rem;
}

.git-inline-error {
  margin: var(--space-2) var(--space-3);
  padding: var(--space-2) 10px;
  border: 0.5px solid var(--error-border);
  border-radius: var(--radius-md);
  background: var(--error-bg);
  color: var(--error);
  font-size: 0.72rem;
  overflow-wrap: anywhere;
}

.git-inline-error--panel {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-2);
}

@media (hover: none) {
  .git-file-actions,
  .git-section-actions {
    opacity: 1;
  }
}

/* Matches MOBILE_MEDIA_QUERY in useMediaQuery.ts. On a phone the panel
   fills the screen, so it takes the page gutter and touch-sized rows. */
@media (max-width: 768px), (pointer: coarse) and (max-height: 500px) {
  .git-header {
    height: 44px;
    padding: 0 var(--space-4);
  }

  .git-top,
  .git-composer {
    padding-left: var(--space-4);
    padding-right: var(--space-4);
  }

  .git-icon-btn {
    width: 40px;
    height: 40px;
  }

  .git-section-header {
    height: 40px;
    padding-left: var(--space-4);
  }

  .git-file {
    height: 44px;
    padding-left: var(--space-4);
  }

  .git-file-name {
    font-size: 0.875rem;
  }

  .git-file-dir {
    font-size: 0.75rem;
  }

  .git-btn,
  .git-btn--commit {
    height: 40px;
  }
}
</style>
