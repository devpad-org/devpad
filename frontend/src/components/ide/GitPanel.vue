<script setup lang="ts">
import { ref, computed, onMounted, onUnmounted, watch } from 'vue'
import {
  gitApi,
  type GitStatus,
  type GitCommit,
  type GitBranch,
} from '@/api/git'
import GitConfigModal from './GitConfigModal.vue'

const props = defineProps<{
  workspaceId: number
}>()

// --- State ---
type Tab = 'changes' | 'log' | 'branches'
const activeTab = ref<Tab>('changes')
const loading = ref(false)
const error = ref('')

// Status
const status = ref<GitStatus | null>(null)
const commitMsg = ref('')
const actionOutput = ref('')

// Log
const commits = ref<GitCommit[]>([])

// Branches
const branches = ref<GitBranch[]>([])
const currentBranch = ref('')
const newBranchName = ref('')
const showNewBranch = ref(false)

// Git config modal
const showGitConfig = ref(false)
const needsGitConfig = computed(() =>
  status.value?.isRepo && (!status.value.userName || !status.value.userEmail)
)

// Diff
const diffContent = ref('')
const diffFile = ref<string | null>(null)
const diffStaged = ref(false)
const showDiff = ref(false)

// Poll interval
let pollTimer: ReturnType<typeof setInterval> | null = null

// --- Computed ---
const stagedFiles = computed(() => (status.value?.files ?? []).filter((f) => f.staged))
const unstagedFiles = computed(() => (status.value?.files ?? []).filter((f) => !f.staged))

const hasChanges = computed(() => (status.value?.files?.length ?? 0) > 0)
const canCommit = computed(() => stagedFiles.value.length > 0 && commitMsg.value.trim() !== '')

// --- Actions ---
async function refresh(showLoading = true) {
  if (!props.workspaceId) return
  if (showLoading) loading.value = true
  error.value = ''
  try {
    status.value = await gitApi.status(props.workspaceId)

    if (activeTab.value === 'log') {
      const res = await gitApi.log(props.workspaceId)
      commits.value = res.commits
    } else if (activeTab.value === 'branches') {
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

async function stageFile(path: string) {
  actionOutput.value = ''
  const result = await gitApi.action(props.workspaceId, 'stage', { files: [path] })
  if (!result.success) actionOutput.value = result.error || result.output
  await refresh()
}

async function unstageFile(path: string) {
  actionOutput.value = ''
  const result = await gitApi.action(props.workspaceId, 'unstage', { files: [path] })
  if (!result.success) actionOutput.value = result.error || result.output
  await refresh()
}

async function stageAll() {
  actionOutput.value = ''
  const result = await gitApi.action(props.workspaceId, 'stage')
  if (!result.success) actionOutput.value = result.error || result.output
  await refresh()
}

async function unstageAll() {
  actionOutput.value = ''
  const result = await gitApi.action(props.workspaceId, 'unstage')
  if (!result.success) actionOutput.value = result.error || result.output
  await refresh()
}

async function discardFile(path: string) {
  actionOutput.value = ''
  const result = await gitApi.action(props.workspaceId, 'discard', { files: [path] })
  if (!result.success) actionOutput.value = result.error || result.output
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

async function push() {
  actionOutput.value = ''
  loading.value = true
  try {
    const result = await gitApi.action(props.workspaceId, 'push')
    actionOutput.value = result.success ? 'Pushed successfully' : result.error || result.output
  } catch (e: any) {
    actionOutput.value = e.message
  }
  await refresh()
}

async function pull() {
  actionOutput.value = ''
  loading.value = true
  try {
    const result = await gitApi.action(props.workspaceId, 'pull')
    actionOutput.value = result.success ? 'Pulled successfully' : result.error || result.output
  } catch (e: any) {
    actionOutput.value = e.message
  }
  await refresh()
}

async function initRepo() {
  actionOutput.value = ''
  const result = await gitApi.action(props.workspaceId, 'init')
  actionOutput.value = result.success ? 'Repository initialized' : result.error || result.output
  await refresh()
}

async function checkoutBranch(name: string) {
  actionOutput.value = ''
  // Strip "origin/" prefix for remote branch checkout
  const branchName = name.startsWith('origin/') ? name.slice(7) : name
  const result = await gitApi.action(props.workspaceId, 'checkout', { branch: branchName })
  if (!result.success) actionOutput.value = result.error || result.output
  await refresh()
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
    actionOutput.value = result.error || result.output
  }
  await refresh()
}

async function viewDiff(path: string, staged: boolean) {
  diffFile.value = path
  diffStaged.value = staged
  showDiff.value = true
  try {
    diffContent.value = await gitApi.diff(props.workspaceId, path, staged)
  } catch {
    diffContent.value = 'Failed to load diff'
  }
}

function closeDiff() {
  showDiff.value = false
  diffContent.value = ''
  diffFile.value = null
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
    case 'untracked':
      return 'U'
    case 'copied':
      return 'C'
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

function formatTime(timestamp: string): string {
  const date = new Date(parseInt(timestamp) * 1000)
  const now = new Date()
  const diff = now.getTime() - date.getTime()

  if (diff < 60_000) return 'just now'
  if (diff < 3_600_000) return `${Math.floor(diff / 60_000)}m ago`
  if (diff < 86_400_000) return `${Math.floor(diff / 3_600_000)}h ago`
  if (diff < 604_800_000) return `${Math.floor(diff / 86_400_000)}d ago`

  return date.toLocaleDateString('en-US', { month: 'short', day: 'numeric' })
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

onMounted(() => {
  refresh()
  pollTimer = setInterval(() => refresh(false), 5000)
})

onUnmounted(() => {
  if (pollTimer) clearInterval(pollTimer)
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
          <span v-if="(status.files?.length ?? 0) > 0" class="git-tab-badge">{{ status.files.length }}</span>
        </button>
        <button
          class="git-tab"
          :class="{ active: activeTab === 'log' }"
          @click="activeTab = 'log'"
        >
          Log
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
        <!-- Sync buttons -->
        <div v-if="(status.remotes?.length ?? 0) > 0" class="git-sync-bar">
          <button class="git-btn git-btn--small" @click="pull" :disabled="loading" title="Pull">
            <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round">
              <path d="M12 5v14" /><path d="m19 12-7 7-7-7" />
            </svg>
            Pull
          </button>
          <button class="git-btn git-btn--small" @click="push" :disabled="loading" title="Push">
            <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round">
              <path d="M12 19V5" /><path d="m5 12 7-7 7 7" />
            </svg>
            Push
          </button>
        </div>

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
      </div>

      <!-- Log tab -->
      <div v-if="activeTab === 'log'" class="git-tab-content">
        <div v-if="commits.length === 0 && !loading" class="git-empty-small">
          <p>No commits yet</p>
        </div>
        <div
          v-for="c in commits"
          :key="c.hash"
          class="git-commit-entry"
        >
          <div class="git-commit-top">
            <span class="git-commit-hash">{{ c.shortHash }}</span>
            <span class="git-commit-time">{{ formatTime(c.timestamp) }}</span>
          </div>
          <div class="git-commit-msg">{{ c.message }}</div>
          <div class="git-commit-author">{{ c.author }}</div>
        </div>
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
        <div
          v-for="b in branches"
          :key="b.name"
          class="git-branch-entry"
          :class="{ current: b.current, remote: b.remote }"
          @click="!b.current && checkoutBranch(b.name)"
        >
          <svg v-if="b.current" width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="var(--accent-green)" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round">
            <path d="M20 6 9 17l-5-5" />
          </svg>
          <svg v-else-if="b.remote" width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="var(--text-muted)" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
            <circle cx="12" cy="12" r="10" /><path d="M2 12h20" /><path d="M12 2a15.3 15.3 0 0 1 4 10 15.3 15.3 0 0 1-4 10 15.3 15.3 0 0 1-4-10 15.3 15.3 0 0 1 4-10z" />
          </svg>
          <svg v-else width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="var(--text-muted)" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
            <line x1="6" x2="6" y1="3" y2="15" /><circle cx="18" cy="6" r="3" /><circle cx="6" cy="18" r="3" /><path d="M18 9a9 9 0 0 1-9 9" />
          </svg>
          <span class="git-branch-name">{{ b.name }}</span>
          <span v-if="b.hash" class="git-branch-hash">{{ b.hash }}</span>
        </div>
      </div>

      <!-- Diff overlay -->
      <div v-if="showDiff" class="git-diff-overlay">
        <div class="git-diff-header">
          <span class="git-diff-title">{{ diffFile }}</span>
          <span v-if="diffStaged" class="git-diff-badge">staged</span>
          <button class="git-icon-btn" @click="closeDiff">
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
              <path d="M18 6 6 18" /><path d="m6 6 12 12" />
            </svg>
          </button>
        </div>
        <pre class="git-diff-content"><code v-for="(line, i) in diffContent.split('\n')" :key="i" :class="diffLineClass(line)">{{ line }}
</code></pre>
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
  </div>
</template>

<script lang="ts">
function diffLineClass(line: string): string {
  if (line.startsWith('+++') || line.startsWith('---')) return 'diff-meta'
  if (line.startsWith('@@')) return 'diff-hunk'
  if (line.startsWith('+')) return 'diff-add'
  if (line.startsWith('-')) return 'diff-del'
  return ''
}
</script>

<style scoped>
.git-panel {
  display: flex;
  flex-direction: column;
  height: 100%;
  overflow: hidden;
  position: relative;
}

.git-header {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  padding: 0 var(--space-3);
  border-bottom: 1px solid var(--border-default);
  height: 38px;
  flex-shrink: 0;
}

.git-title {
  display: flex;
  align-items: center;
  gap: 6px;
  font-size: 0.75rem;
  font-weight: 600;
  text-transform: uppercase;
  letter-spacing: 0.05em;
  color: var(--text-secondary);
}

.panel-icon {
  color: var(--accent-purple);
  opacity: 0.7;
  flex-shrink: 0;
}

.git-branch-badge {
  font-size: 0.7rem;
  font-family: var(--font-mono);
  padding: 1px 6px;
  border-radius: var(--radius-sm);
  background: rgba(124, 58, 237, 0.15);
  color: var(--accent-purple);
  border: 1px solid rgba(124, 58, 237, 0.25);
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

.git-empty-small {
  padding: var(--space-4);
  text-align: center;
  color: var(--text-muted);
  font-size: 0.75rem;
}

/* Tabs */
.git-tabs {
  display: flex;
  border-bottom: 1px solid var(--border-default);
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
  font-size: 0.65rem;
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

/* Sync bar */
.git-sync-bar {
  display: flex;
  gap: var(--space-1);
  padding: var(--space-2) var(--space-3);
  border-bottom: 1px solid var(--border-default);
}

/* Sections */
.git-section {
  border-bottom: 1px solid var(--border-default);
}

.git-section-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: var(--space-1) var(--space-3);
  font-size: 0.7rem;
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
  font-size: 0.7rem;
  font-weight: 700;
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
  font-size: 0.65rem;
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
  border-top: 1px solid var(--border-default);
  flex-shrink: 0;
}

.git-commit-input {
  width: 100%;
  padding: var(--space-2);
  background: var(--bg-primary);
  color: var(--text-primary);
  border: 1px solid var(--border-default);
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
  border: 1px solid var(--border-default);
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
  background: color-mix(in srgb, var(--accent-blue) 85%, white);
}

.git-btn--small {
  padding: 2px var(--space-2);
  font-size: 0.7rem;
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
  font-size: 0.7rem;
  color: var(--text-secondary);
  white-space: pre-wrap;
  word-break: break-all;
  background: var(--bg-primary);
  padding: var(--space-2);
  border-radius: var(--radius-sm);
  border: 1px solid var(--border-default);
  max-height: 100px;
  overflow-y: auto;
}

/* Commit log */
.git-commit-entry {
  padding: var(--space-2) var(--space-3);
  border-bottom: 1px solid var(--border-default);
  transition: background var(--transition-fast);
}

.git-commit-entry:hover {
  background: var(--bg-hover);
}

.git-commit-top {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 2px;
}

.git-commit-hash {
  font-family: var(--font-mono);
  font-size: 0.68rem;
  color: var(--accent-blue);
}

.git-commit-time {
  font-size: 0.65rem;
  color: var(--text-muted);
}

.git-commit-msg {
  font-size: 0.75rem;
  color: var(--text-primary);
  line-height: 1.3;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.git-commit-author {
  font-size: 0.65rem;
  color: var(--text-muted);
  margin-top: 1px;
}

/* Branches */
.git-new-branch {
  display: flex;
  gap: var(--space-1);
  padding: var(--space-2) var(--space-3);
  border-bottom: 1px solid var(--border-default);
}

.git-branch-input {
  flex: 1;
  padding: 3px var(--space-2);
  background: var(--bg-primary);
  color: var(--text-primary);
  border: 1px solid var(--border-default);
  border-radius: var(--radius-sm);
  font-size: 0.72rem;
  outline: none;
}

.git-branch-input:focus {
  border-color: var(--accent-blue);
}

.git-branch-entry {
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 4px var(--space-3);
  cursor: pointer;
  transition: background var(--transition-fast);
}

.git-branch-entry:hover {
  background: var(--bg-hover);
}

.git-branch-entry.current {
  cursor: default;
}

.git-branch-entry.remote {
  opacity: 0.7;
}

.git-branch-name {
  font-size: 0.75rem;
  font-family: var(--font-mono);
  color: var(--text-primary);
}

.git-branch-entry.current .git-branch-name {
  color: var(--accent-green);
  font-weight: 600;
}

.git-branch-hash {
  font-size: 0.65rem;
  font-family: var(--font-mono);
  color: var(--text-muted);
  margin-left: auto;
}

/* Diff overlay */
.git-diff-overlay {
  position: absolute;
  inset: 0;
  background: var(--bg-surface);
  display: flex;
  flex-direction: column;
  z-index: 10;
}

.git-diff-header {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  padding: var(--space-2) var(--space-3);
  border-bottom: 1px solid var(--border-default);
  flex-shrink: 0;
}

.git-diff-title {
  font-family: var(--font-mono);
  font-size: 0.75rem;
  color: var(--text-primary);
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.git-diff-badge {
  font-size: 0.6rem;
  padding: 1px 5px;
  border-radius: var(--radius-sm);
  background: rgba(16, 185, 129, 0.15);
  color: var(--accent-green);
  border: 1px solid rgba(16, 185, 129, 0.25);
}

.git-diff-content {
  flex: 1;
  overflow: auto;
  padding: var(--space-2);
  font-family: var(--font-mono);
  font-size: 0.72rem;
  line-height: 1.65;
  background: var(--bg-primary);
  margin: 0;
}

.git-diff-content code {
  display: block;
  padding: 0 var(--space-2);
}

.diff-add {
  background: rgba(16, 185, 129, 0.1);
  color: var(--accent-green);
}

.diff-del {
  background: rgba(244, 63, 94, 0.1);
  color: var(--accent-rose);
}

.diff-hunk {
  color: var(--accent-blue);
  background: rgba(0, 212, 255, 0.05);
}

.diff-meta {
  color: var(--text-muted);
  font-weight: 600;
}

/* Error */
.git-error {
  padding: var(--space-3);
  font-size: 0.75rem;
  color: var(--accent-rose);
}
</style>
