<script setup lang="ts">
import { computed, ref, onMounted, onUnmounted } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { workspaceApi, type Workspace } from '@/api/workspaces'
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
import { useServiceStore } from '@/stores/services'
import { useResizable } from '@/composables/useResizable'
import type { GitCommit, GitCommitFile } from '@/api/git'
import type { AgentRun } from '@/api/ai'

const route = useRoute()
const router = useRouter()
const serviceStore = useServiceStore()

const workspace = ref<Workspace | null>(null)
const loading = ref(true)
const error = ref<string | null>(null)

const activeFile = ref<string | null>(null)
const terminalMinimized = ref(false)
const previewVisible = ref(false)
const editorPanel = ref<InstanceType<typeof EditorPanel> | null>(null)
const selectedGitCommit = ref<GitCommit | null>(null)
const gitDiffRequest = ref<GitDiffRequest | null>(null)
const focusedAgentRunId = ref<number | null>(null)
const selectedAgentId = ref('default')
const previewModalVisible = ref(false)
const previewModalLoading = ref(false)
const previewModalError = ref<string | null>(null)
let gitDiffRequestId = 0

type GitDiffRequest =
  | { kind: 'commit'; commit: GitCommit; file: GitCommitFile; requestId: number }
  | { kind: 'working'; path: string; staged: boolean; requestId: number }

type ActivityTab = 'ai' | 'explorer' | 'git' | 'info' | 'services'
const activeActivity = ref<ActivityTab>('ai')
const serviceCount = computed(() => serviceStore.services.length)

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

onMounted(async () => {
  window.addEventListener('keydown', handleGlobalKeydown)

  const id = Number(route.params.id)
  if (isNaN(id)) {
    router.push({ name: 'home' })
    return
  }
  try {
    const res = await workspaceApi.get(id)
    let ws = res.workspace
    // Auto-start the container if it's stopped
    if (ws.status === 'stopped') {
      const startRes = await workspaceApi.start(id)
      ws = startRes.workspace
    }
    workspace.value = ws
  } catch {
    error.value = 'Failed to load workspace'
  } finally {
    loading.value = false
  }
})

onUnmounted(() => {
  window.removeEventListener('keydown', handleGlobalKeydown)
})

function handleFileSelect(path: string) {
  activeFile.value = path
}

function handleGitCommitSelect(commit: GitCommit) {
  activeActivity.value = 'git'
  selectedGitCommit.value = commit
}

function handleGitCommitFileSelect(payload: { commit: GitCommit; file: GitCommitFile }) {
  selectedGitCommit.value = payload.commit
  gitDiffRequest.value = {
    kind: 'commit',
    ...payload,
    requestId: ++gitDiffRequestId,
  }
}

function handleGitWorkingFileSelect(payload: { path: string; staged: boolean }) {
  selectedGitCommit.value = null
  gitDiffRequest.value = {
    kind: 'working',
    ...payload,
    requestId: ++gitDiffRequestId,
  }
}

function handleAgentRunFocus(run: AgentRun) {
  focusedAgentRunId.value = run.id
  activeActivity.value = 'ai'
}

function handleAgentSelect(agentId: string) {
  selectedAgentId.value = agentId
  focusedAgentRunId.value = null
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

function handleBack() {
  router.push({ name: 'home' })
}
</script>

<template>
  <div v-if="loading" class="ide-loading">
    <div class="ide-spinner" />
    <span>Loading workspace…</span>
  </div>
  <div v-else-if="error" class="ide-error">
    <p>{{ error }}</p>
    <button class="ide-back-btn" @click="handleBack">Back to Workspaces</button>
  </div>
  <div v-else class="ide-layout">
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
      <button class="titlebar-back" @click="handleBack" title="Back to workspaces">
        <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round">
          <path d="m15 18-6-6 6-6" />
        </svg>
      </button>
      <span class="titlebar-name">{{ workspace?.name }}</span>
      <span class="titlebar-status" :class="`status-${workspace?.status}`">
        <span class="status-dot" />
        {{ workspace?.status }}
      </span>
      <div class="titlebar-actions">
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
      </div>
    </div>

    <!-- Main IDE area -->
    <div class="ide-body">
      <nav id="ide-activity-bar" class="activity-bar" aria-label="Activity bar">
        <button
          class="activity-btn"
          :class="{ active: activeActivity === 'ai' }"
          @click="activeActivity = 'ai'"
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
          class="activity-btn"
          :class="{ active: activeActivity === 'explorer' }"
          @click="activeActivity = 'explorer'"
          title="Explorer"
        >
          <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round">
            <path d="M4 4h5l2 2h8a2 2 0 0 1 2 2v10a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2V6a2 2 0 0 1 2-2Z" />
          </svg>
        </button>
        <button
          class="activity-btn"
          :class="{ active: activeActivity === 'git' }"
          @click="activeActivity = 'git'"
          title="Source Control"
        >
          <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round">
            <circle cx="18" cy="18" r="3" />
            <circle cx="6" cy="6" r="3" />
            <path d="M6 21V9a9 9 0 0 0 9 9" />
          </svg>
        </button>
        <button
          class="activity-btn"
          :class="{ active: activeActivity === 'info' }"
          @click="activeActivity = 'info'"
          title="Workspace Info"
        >
          <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round">
            <circle cx="12" cy="12" r="10" />
            <path d="M12 16v-4" />
            <path d="M12 8h.01" />
          </svg>
        </button>
        <button
          class="activity-btn"
          :class="{ active: activeActivity === 'services' }"
          @click="activeActivity = 'services'"
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
      </nav>

      <aside
        id="ide-left-panel"
        class="ide-sidebar"
        aria-label="Left side panel"
        :style="{ width: sidebar.size.value + 'px' }"
      >
        <AiAgentsPanel
          v-show="activeActivity === 'ai'"
          :workspace-id="workspace?.id ?? 0"
          :selected-agent-id="selectedAgentId"
          @select="handleAgentSelect"
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
          @commit-file-select="handleGitCommitFileSelect"
          @working-file-select="handleGitWorkingFileSelect"
          @clear-commit="clearSelectedGitCommit"
        />
        <WorkspaceInfoPanel
          v-show="activeActivity === 'info'"
          :workspace-id="workspace?.id ?? 0"
        />
        <ServiceCatalogPanel
          v-show="activeActivity === 'services'"
          :workspace-id="workspace?.id ?? 0"
        />
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
              :workspace-id="workspace?.id ?? 0"
              :selected-agent-id="selectedAgentId"
              :focused-run-id="focusedAgentRunId"
              @clear-focused-run="focusedAgentRunId = null"
              @select-agent="handleAgentSelect"
            />
          </div>
          <div v-show="activeActivity !== 'ai' && activeActivity !== 'git' && activeActivity !== 'info' && activeActivity !== 'services'" class="ide-surface-pane">
            <EditorPanel
              ref="editorPanel"
              :workspace-id="workspace?.id ?? 0"
              :file-path="activeFile"
              @active-change="(p: string | null) => activeFile = p"
            />
          </div>
          <div v-show="activeActivity === 'info'" class="ide-surface-pane">
            <WorkspaceProcessesPanel
              :workspace-id="workspace?.id ?? 0"
              :active="activeActivity === 'info'"
            />
          </div>
          <div v-show="activeActivity === 'git'" class="ide-surface-pane">
            <GitGraphPanel
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
        </section>
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
      </div>

      <div id="ide-right-panel" class="ide-right-panel" role="region" aria-label="Right side panel">
        <!-- Right panel: Preview -->
        <template v-if="previewVisible">
          <div
            class="resize-handle resize-handle--horizontal"
            :class="{ active: preview.isDragging.value }"
            @pointerdown="preview.onPointerDown"
          />
          <aside class="ide-preview" :style="{ width: preview.size.value + 'px' }">
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
        <aside class="ide-ai-side-panel" :style="{ width: aiSidePanel.size.value + 'px' }" aria-label="AI agent runs">
          <AiAgentRunsPanel
            :workspace-id="workspace?.id ?? 0"
            :selected-run-id="focusedAgentRunId"
            @focus="handleAgentRunFocus"
          />
        </aside>
      </div>
    </div>
    <GitStatusBar
      :workspace-id="workspace?.id ?? 0"
      @open-git="activeActivity = 'git'"
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

@keyframes spin {
  to { transform: rotate(360deg); }
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
  font-size: 0.85rem;
  font-weight: 600;
  color: var(--text-primary);
}

.titlebar-status {
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
  display: flex;
  align-items: center;
  justify-content: center;
  width: 40px;
  height: 40px;
  border-radius: var(--radius-md);
  color: var(--text-muted);
  transition: all var(--transition-fast);
  position: relative;
}

.activity-btn:hover {
  background: var(--bg-hover);
  color: var(--text-secondary);
}

.activity-btn.active {
  background: var(--bg-selected);
  color: var(--text-primary);
}

.activity-btn.active::before {
  content: '';
  position: absolute;
  left: -4px;
  top: 8px;
  bottom: 8px;
  width: 2px;
  background: var(--accent-blue);
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
  background: var(--accent-blue);
  color: var(--bg-void);
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
}

.ide-surface-pane {
  height: 100%;
  overflow: hidden;
}

.ide-ai-surface {
  background: var(--bg-base);
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
</style>
