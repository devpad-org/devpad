<script setup lang="ts">
import { ref, reactive, watch, nextTick, onUnmounted } from 'vue'
import { workspaceApi } from '@/api/workspaces'
import { useMonacoEditor } from '@/composables/useMonacoEditor'

interface Tab {
  path: string
  name: string
}

const props = defineProps<{
  workspaceId: number
  filePath: string | null
}>()

const emit = defineEmits<{
  (e: 'save', path: string, content: string): void
  (e: 'activeChange', path: string | null): void
}>()

const openTabs = reactive<Tab[]>([])
const activeTab = ref<string | null>(null)

const editorContainer = ref<HTMLElement | null>(null)
const loading = ref(false)
const error = ref<string | null>(null)
const saving = ref(false)

const {
  editor,
  isDirty,
  hasDirtyFiles,
  setContent,
  switchToFile,
  closeFile,
  isFileDirty,
  markClean,
  getContent,
  getFileContent,
  getDirtyFiles,
} = useMonacoEditor(editorContainer)

function tabName(path: string): string {
  return path.split('/').pop() ?? path
}

function selectTab(path: string) {
  if (activeTab.value === path) return
  activeTab.value = path
  switchToFile(path)
  emit('activeChange', path)
}

function closeTab(path: string, event?: MouseEvent) {
  event?.stopPropagation()

  const idx = openTabs.findIndex((t) => t.path === path)
  if (idx === -1) return

  openTabs.splice(idx, 1)
  closeFile(path)

  if (activeTab.value === path) {
    // Switch to an adjacent tab, preferring the one to the left
    const nextTab = openTabs[Math.min(idx, openTabs.length - 1)] ?? null
    activeTab.value = nextTab?.path ?? null

    if (nextTab) {
      switchToFile(nextTab.path)
    }
    emit('activeChange', activeTab.value)
  }
}

async function saveActiveFile() {
  if (!activeTab.value || !isDirty.value || saving.value) return
  saving.value = true
  try {
    const content = getContent()
    await workspaceApi.writeFile(props.workspaceId, activeTab.value, content)
    markClean(activeTab.value)
    emit('save', activeTab.value, content)
  } catch (e) {
    error.value = e instanceof Error ? e.message : 'Failed to save file'
  } finally {
    saving.value = false
  }
}

async function saveAllFiles() {
  const dirty = getDirtyFiles()
  if (dirty.length === 0 || saving.value) return
  saving.value = true
  try {
    for (const filePath of dirty) {
      const content = getFileContent(filePath)
      await workspaceApi.writeFile(props.workspaceId, filePath, content)
      markClean(filePath)
      emit('save', filePath, content)
    }
  } catch (e) {
    error.value = e instanceof Error ? e.message : 'Failed to save files'
  } finally {
    saving.value = false
  }
}

defineExpose({
  saveActiveFile,
  saveAllFiles,
  isDirty,
  hasDirtyFiles,
})

// Register Ctrl+S keybinding once editor is available
watch(editor, (ed) => {
  if (!ed) return
  ed.addCommand(
    2048 | 49, // CtrlCmd + S
    () => saveActiveFile(),
  )
})

// When parent requests a file, open it as a tab
let loadAbortController: AbortController | null = null

watch(() => props.filePath, async (newPath) => {
  if (!newPath) return

  // If already open, just switch to it
  const existing = openTabs.find((t) => t.path === newPath)
  if (existing) {
    selectTab(newPath)
    return
  }

  // Cancel any in-flight file load to avoid race conditions on rapid switching
  loadAbortController?.abort()
  loadAbortController = new AbortController()
  const signal = loadAbortController.signal

  // Open new tab
  loading.value = true
  error.value = null
  try {
    const content = await workspaceApi.readFile(props.workspaceId, newPath, signal)
    if (signal.aborted) return
    openTabs.push({ path: newPath, name: tabName(newPath) })
    activeTab.value = newPath
    await nextTick()
    setContent(content, newPath)
    emit('activeChange', newPath)
  } catch (e) {
    if (e instanceof DOMException && e.name === 'AbortError') return
    error.value = e instanceof Error ? e.message : 'Failed to load file'
  } finally {
    if (!signal.aborted) {
      loading.value = false
    }
  }
}, { immediate: true })

onUnmounted(() => {
  loadAbortController?.abort()
})
</script>

<template>
  <div class="editor-panel">
    <!-- Tab bar -->
    <div class="editor-tabs">
      <div
        v-for="tab in openTabs"
        :key="tab.path"
        class="editor-tab"
        :class="{ active: tab.path === activeTab }"
        @click="selectTab(tab.path)"
      >
        <span class="tab-icon">
          <span v-if="isFileDirty(tab.path)" class="dot-modified" />
        </span>
        <span class="tab-name">{{ tab.name }}</span>
        <button
          class="tab-close"
          title="Close"
          @click="closeTab(tab.path, $event)"
        >×</button>
      </div>
      <div v-if="openTabs.length === 0" class="editor-tab-empty" />
    </div>

    <!-- Monaco editor container -->
    <div
      v-show="activeTab && !loading && !error"
      ref="editorContainer"
      class="editor-container"
    />

    <!-- Loading state -->
    <div v-if="loading" class="editor-empty">
      <span class="loading-text">Loading...</span>
    </div>

    <!-- Error state -->
    <div v-else-if="error" class="editor-empty">
      <span class="error-text">{{ error }}</span>
    </div>

    <!-- Empty state (no tabs open) -->
    <div v-else-if="openTabs.length === 0" class="editor-empty">
      <div class="empty-logo">
        <span class="logo-text">Devpad</span>
      </div>
      <div class="empty-shortcuts">
        <div class="shortcut-row">
          <kbd>Ctrl</kbd> + <kbd>S</kbd>
          <span class="shortcut-label">Save File</span>
        </div>
        <div class="shortcut-row">
          <kbd>Ctrl</kbd> + <kbd>Shift</kbd> + <kbd>S</kbd>
          <span class="shortcut-label">Save All Files</span>
        </div>
        <div class="shortcut-row">
          <kbd>Ctrl</kbd> + <kbd>`</kbd>
          <span class="shortcut-label">Toggle Terminal</span>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.editor-panel {
  display: flex;
  flex-direction: column;
  height: 100%;
  background: var(--bg-primary);
}

/* Tabs */
.editor-tabs {
  display: flex;
  align-items: stretch;
  background: var(--bg-surface);
  border-bottom: 1px solid var(--border-default);
  height: 38px;
  flex-shrink: 0;
  overflow-x: auto;
  overflow-y: hidden;
}

.editor-tabs::-webkit-scrollbar {
  height: 0;
}

.editor-tab {
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 0 var(--space-3);
  font-size: 0.78rem;
  color: var(--text-secondary);
  border-right: 1px solid var(--border-default);
  box-shadow: inset 0 -1px 0 transparent;
  cursor: pointer;
  transition: color var(--transition-fast), background var(--transition-fast);
  white-space: nowrap;
  flex-shrink: 0;
}

.editor-tab:hover {
  background: var(--bg-hover);
}

.editor-tab.active {
  color: var(--text-primary);
  background: var(--bg-primary);
  box-shadow: inset 0 -1px 0 var(--accent-blue);
}

.tab-icon {
  display: flex;
  align-items: center;
  width: 6px;
}

.dot-modified {
  display: block;
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: var(--text-muted);
}

.tab-close {
  font-size: 1rem;
  line-height: 1;
  color: var(--text-muted);
  border-radius: var(--radius-sm);
  width: 18px;
  height: 18px;
  display: flex;
  align-items: center;
  justify-content: center;
  transition: all var(--transition-fast);
}

.tab-close:hover {
  background: var(--bg-hover);
  color: var(--text-primary);
}

/* Monaco container */
.editor-container {
  flex: 1;
  overflow: hidden;
}

/* Empty state */
.editor-empty {
  flex: 1;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: var(--space-8);
}

.empty-logo {
  opacity: 0.08;
}

.logo-text {
  font-size: 4rem;
  font-weight: 800;
  background: linear-gradient(135deg, var(--accent-blue), var(--accent-purple));
  -webkit-background-clip: text;
  background-clip: text;
  -webkit-text-fill-color: transparent;
}

.empty-shortcuts {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
}

.shortcut-row {
  display: flex;
  align-items: center;
  gap: 6px;
  font-size: 0.78rem;
  color: var(--text-muted);
}

.shortcut-label {
  margin-left: var(--space-2);
  color: var(--text-secondary);
}

kbd {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  min-width: 22px;
  height: 22px;
  padding: 0 6px;
  background: var(--bg-surface);
  border: 1px solid var(--border-default);
  border-radius: var(--radius-sm);
  font-family: var(--font-sans);
  font-size: 0.7rem;
  color: var(--text-secondary);
}

.loading-text {
  color: var(--text-muted);
  font-size: 0.85rem;
}

.error-text {
  color: var(--accent-rose);
  font-size: 0.85rem;
}
</style>
