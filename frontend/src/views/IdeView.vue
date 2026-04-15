<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { workspaceApi, type Workspace } from '@/api/workspaces'
import FileExplorer from '@/components/ide/FileExplorer.vue'
import EditorPanel from '@/components/ide/EditorPanel.vue'
import TerminalPanel from '@/components/ide/TerminalPanel.vue'
import AiAgentPanel from '@/components/ide/AiAgentPanel.vue'

const route = useRoute()
const router = useRouter()

const workspace = ref<Workspace | null>(null)
const loading = ref(true)
const error = ref<string | null>(null)

const activeFile = ref<string | null>(null)
const terminalVisible = ref(true)
const agentVisible = ref(true)

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
          class="titlebar-toggle"
          :class="{ active: terminalVisible }"
          @click="terminalVisible = !terminalVisible"
          title="Toggle terminal"
        >
          <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
            <polyline points="4 17 10 11 4 5" />
            <line x1="12" x2="20" y1="19" y2="19" />
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
          :workspace-name="workspace?.name ?? ''"
          @select="handleFileSelect"
        />
      </aside>

      <!-- Center + Bottom -->
      <div class="ide-center">
        <div class="ide-editor-area">
          <EditorPanel :file-path="activeFile" />
        </div>
        <div v-if="terminalVisible" class="ide-terminal-area">
          <TerminalPanel :workspace-id="workspace?.id ?? 0" />
        </div>
      </div>

      <!-- Right panel: AI Agent -->
      <aside v-if="agentVisible" class="ide-agent">
        <AiAgentPanel />
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
  gap: var(--space-1);
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
}

.ide-agent {
  width: 320px;
  flex-shrink: 0;
  border-left: 1px solid var(--border-default);
  overflow-y: auto;
  background: var(--bg-surface);
}
</style>
