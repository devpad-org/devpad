<script setup lang="ts">
import { ref, onMounted, onUnmounted } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { workspaceApi, type Workspace } from '@/api/workspaces'
import FileExplorer from '@/components/ide/FileExplorer.vue'
import EditorPanel from '@/components/ide/EditorPanel.vue'
import TerminalPanel from '@/components/ide/TerminalPanel.vue'
import AiAgentPanel from '@/components/ide/AiAgentPanel.vue'
import PreviewPanel from '@/components/ide/PreviewPanel.vue'
import GitPanel from '@/components/ide/GitPanel.vue'
import WorkspaceInfoPanel from '@/components/ide/WorkspaceInfoPanel.vue'
import { useResizable } from '@/composables/useResizable'

const route = useRoute()
const router = useRouter()

const workspace = ref<Workspace | null>(null)
const loading = ref(true)
const error = ref<string | null>(null)

const activeFile = ref<string | null>(null)
const terminalMinimized = ref(false)
const agentVisible = ref(true)
const previewVisible = ref(false)
const editorPanel = ref<InstanceType<typeof EditorPanel> | null>(null)

type SidebarTab = 'explorer' | 'git' | 'info'
const activeSidebarTab = ref<SidebarTab>('explorer')

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

const agent = useResizable({
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

async function openPreviewNewTab() {
  const portStr = prompt('Enter port to preview:', '3000')
  if (!portStr) return
  const port = Number(portStr)
  if (!Number.isInteger(port) || port < 1 || port > 65535) return
  try {
    const url = await workspaceApi.getPreviewURL(workspace.value!.id, port)
    window.open(url, '_blank')
  } catch {
    // Silently fail — user can use the panel for error details
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
    <!-- Title bar -->
    <div class="ide-titlebar">
      <button class="titlebar-back" @click="handleBack" title="Back to workspaces">
        <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
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
          <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
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
          <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
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
          <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
            <rect width="20" height="14" x="2" y="3" rx="2" />
            <path d="M8 21h8" />
            <path d="M12 17v4" />
          </svg>
        </button>
        <button
          class="titlebar-btn"
          @click="openPreviewNewTab"
          title="Open preview in new tab"
        >
          <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
            <path d="M15 3h6v6" />
            <path d="M10 14 21 3" />
            <path d="M18 13v6a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V8a2 2 0 0 1 2-2h6" />
          </svg>
        </button>
        <button
          class="titlebar-toggle"
          :class="{ active: agentVisible }"
          @click="agentVisible = !agentVisible"
          title="Toggle AI agent"
        >
          <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
            <path d="M12 8V4H8" />
            <rect width="16" height="12" x="4" y="8" rx="2" />
            <path d="m2 14 6-6 6 6" />
            <path d="m14 8 4 4 4-4" />
          </svg>
        </button>
      </div>
    </div>

    <!-- Main IDE area -->
    <div class="ide-body">
      <!-- Activity Bar -->
      <div class="activity-bar">
        <button
          class="activity-btn"
          :class="{ active: activeSidebarTab === 'explorer' }"
          @click="activeSidebarTab = 'explorer'"
          title="Explorer"
        >
          <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round">
            <path d="M4 4h5l2 2h8a2 2 0 0 1 2 2v10a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2V6a2 2 0 0 1 2-2Z" />
          </svg>
        </button>
        <button
          class="activity-btn"
          :class="{ active: activeSidebarTab === 'git' }"
          @click="activeSidebarTab = 'git'"
          title="Source Control"
        >
          <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round">
            <circle cx="18" cy="18" r="3" />
            <circle cx="6" cy="6" r="3" />
            <path d="M6 21V9a9 9 0 0 0 9 9" />
          </svg>
        </button>
        <button
          class="activity-btn"
          :class="{ active: activeSidebarTab === 'info' }"
          @click="activeSidebarTab = 'info'"
          title="Workspace Info"
        >
          <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round">
            <circle cx="12" cy="12" r="10" />
            <path d="M12 16v-4" />
            <path d="M12 8h.01" />
          </svg>
        </button>
      </div>

      <!-- Sidebar -->
      <aside class="ide-sidebar" :style="{ width: sidebar.size.value + 'px' }">
        <FileExplorer
          v-show="activeSidebarTab === 'explorer'"
          :workspace-id="workspace?.id ?? 0"
          :workspace-name="workspace?.name ?? ''"
          @select="handleFileSelect"
        />
        <GitPanel
          v-show="activeSidebarTab === 'git'"
          :workspace-id="workspace?.id ?? 0"
        />
        <WorkspaceInfoPanel
          v-show="activeSidebarTab === 'info'"
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
        <div class="ide-editor-area">
          <EditorPanel
            ref="editorPanel"
            :workspace-id="workspace?.id ?? 0"
            :file-path="activeFile"
            @active-change="(p: string | null) => activeFile = p"
          />
        </div>
        <div
          class="resize-handle resize-handle--vertical"
          :class="{ active: terminal.isDragging.value, hidden: terminalMinimized }"
          @pointerdown="terminal.onPointerDown"
        />
        <div
          class="ide-terminal-area"
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

      <!-- Right panel: AI Agent -->
      <template v-if="agentVisible">
        <div
          class="resize-handle resize-handle--horizontal"
          :class="{ active: agent.isDragging.value }"
          @pointerdown="agent.onPointerDown"
        />
        <aside class="ide-agent" :style="{ width: agent.size.value + 'px' }">
          <AiAgentPanel :workspace-id="workspace?.id ?? 0" />
        </aside>
      </template>
    </div>
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
  border: 1px solid var(--border-default);
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
  border-bottom: 1px solid var(--border-default);
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
  font-size: 0.7rem;
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
  border-right: 1px solid var(--border-default);
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
  color: var(--text-primary);
}

.activity-btn.active {
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

/* Body layout */
.ide-body {
  display: flex;
  flex: 1;
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

.ide-terminal-area {
  flex-shrink: 0;
}

.ide-terminal-area.minimized {
  height: auto;
}

.ide-agent {
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
