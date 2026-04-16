<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { workspaceApi, type Workspace } from '@/api/workspaces'
import FileExplorer from '@/components/ide/FileExplorer.vue'
import EditorPanel from '@/components/ide/EditorPanel.vue'
import TerminalPanel from '@/components/ide/TerminalPanel.vue'
import AiAgentPanel from '@/components/ide/AiAgentPanel.vue'
import PreviewPanel from '@/components/ide/PreviewPanel.vue'

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

onMounted(async () => {
  const id = Number(route.params.id)
  if (isNaN(id)) {
    router.push({ name: 'home' })
    return
  }
  try {
    const res = await workspaceApi.get(id)
    workspace.value = res.workspace
  } catch {
    error.value = 'Failed to load workspace'
  } finally {
    loading.value = false
  }
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
      <!-- Sidebar: File Explorer -->
      <aside class="ide-sidebar">
        <FileExplorer
          :workspace-id="workspace?.id ?? 0"
          :workspace-name="workspace?.name ?? ''"
          @select="handleFileSelect"
        />
      </aside>

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
        <div class="ide-terminal-area" :class="{ minimized: terminalMinimized }">
          <TerminalPanel
            :workspace-id="workspace?.id ?? 0"
            :minimized="terminalMinimized"
            @toggle-minimize="terminalMinimized = !terminalMinimized"
          />
        </div>
      </div>

      <!-- Right panel: Preview -->
      <aside v-if="previewVisible" class="ide-preview">
        <PreviewPanel
          :workspace-id="workspace?.id ?? 0"
          @close="previewVisible = false"
        />
      </aside>

      <!-- Right panel: AI Agent -->
      <aside v-if="agentVisible" class="ide-agent">
        <AiAgentPanel :workspace-id="workspace?.id ?? 0" />
      </aside>
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

/* Body layout */
.ide-body {
  display: flex;
  flex: 1;
  overflow: hidden;
}

.ide-sidebar {
  width: 240px;
  flex-shrink: 0;
  border-right: 1px solid var(--border-default);
  overflow-y: auto;
  background: var(--bg-surface);
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
  height: 220px;
  flex-shrink: 0;
  border-top: 1px solid var(--border-default);
  transition: height var(--transition-fast);
}

.ide-terminal-area.minimized {
  height: auto;
}

.ide-agent {
  width: 320px;
  flex-shrink: 0;
  border-left: 1px solid var(--border-default);
  overflow-y: auto;
  background: var(--bg-surface);
}

.ide-preview {
  width: 480px;
  flex-shrink: 0;
  border-left: 1px solid var(--border-default);
  overflow: hidden;
  background: var(--bg-surface);
}
</style>
