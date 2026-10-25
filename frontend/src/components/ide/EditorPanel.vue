<script setup lang="ts">
import { computed, ref, reactive, watch, nextTick, onUnmounted } from 'vue'
import { workspaceApi } from '@/api/workspaces'
import { useMonacoEditor } from '@/composables/useMonacoEditor'
import { useIsMobile } from '@/composables/useMediaQuery'

// A tab is a workspace file, or a view the parent renders through the `view`
// slot, such as a project board. A file tab's id is its path.
interface Tab {
  id: string
  kind: 'file' | 'view'
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

const activeIsFile = computed(() => openTabs.some((t) => t.id === activeTab.value && t.kind === 'file'))
const viewTabs = computed(() => openTabs.filter((t) => t.kind === 'view'))

const editorContainer = ref<HTMLElement | null>(null)
const loading = ref(false)
const error = ref<string | null>(null)
const saving = ref(false)
const isMobile = useIsMobile()

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
} = useMonacoEditor(editorContainer, { minimap: () => !isMobile.value })

function tabName(path: string): string {
  return path.split('/').pop() ?? path
}

function isFileTab(tab: Tab | undefined | null): tab is Tab {
  return tab?.kind === 'file'
}

// Activates a tab. The parent hears about file tabs only; a view tab reports
// no active file.
function activate(tab: Tab) {
  activeTab.value = tab.id
  error.value = null
  if (isFileTab(tab)) switchToFile(tab.id)
  emit('activeChange', isFileTab(tab) ? tab.id : null)
}

function selectTab(id: string) {
  // Clicking the active tab again clears a failed file load.
  if (activeTab.value === id && !error.value) return
  const tab = openTabs.find((t) => t.id === id)
  if (tab) activate(tab)
}

function closeTab(id: string, event?: MouseEvent) {
  event?.stopPropagation()

  const idx = openTabs.findIndex((t) => t.id === id)
  if (idx === -1) return

  const [closed] = openTabs.splice(idx, 1)
  if (isFileTab(closed)) closeFile(id)

  if (activeTab.value === id) {
    // Switch to an adjacent tab, preferring the one to the left
    const nextTab = openTabs[Math.min(idx, openTabs.length - 1)] ?? null
    if (nextTab) {
      activate(nextTab)
    } else {
      activeTab.value = null
      emit('activeChange', null)
    }
  }
}

// Opens a view tab, or focuses it if it is already open.
function openView(id: string, name: string) {
  let tab = openTabs.find((t) => t.id === id)
  if (!tab) {
    tab = { id, kind: 'view', name }
    openTabs.push(tab)
  } else {
    tab.name = name
  }
  if (activeTab.value !== id) activate(tab)
}

function closeView(id: string) {
  if (openTabs.some((t) => t.id === id && t.kind === 'view')) closeTab(id)
}

async function saveActiveFile() {
  if (!activeTab.value || !activeIsFile.value || !isDirty.value || saving.value) return
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

const activeFileDirty = computed(() => activeIsFile.value && isDirty.value)

defineExpose({
  saveActiveFile,
  saveAllFiles,
  openView,
  closeView,
  isDirty: activeFileDirty,
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
  const existing = openTabs.find((t) => t.id === newPath && t.kind === 'file')
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
    openTabs.push({ id: newPath, kind: 'file', name: tabName(newPath) })
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
        :key="tab.id"
        class="editor-tab"
        :class="{ active: tab.id === activeTab, 'editor-tab--view': tab.kind === 'view' }"
        @click="selectTab(tab.id)"
      >
        <span class="tab-icon">
          <slot v-if="tab.kind === 'view'" name="view-icon" :id="tab.id" />
          <span v-else-if="isFileDirty(tab.id)" class="dot-modified" />
        </span>
        <span class="tab-name">{{ tab.name }}</span>
        <button
          class="tab-close"
          title="Close"
          @click="closeTab(tab.id, $event)"
        >×</button>
      </div>
      <div v-if="openTabs.length === 0" class="editor-tab-empty" />
    </div>

    <!-- Monaco editor container -->
    <div
      v-show="activeIsFile && !loading && !error"
      ref="editorContainer"
      class="editor-container"
    />

    <!-- View tabs stay mounted so they keep their state between visits -->
    <div
      v-for="tab in viewTabs"
      v-show="tab.id === activeTab && !loading && !error"
      :key="tab.id"
      class="editor-view"
    >
      <slot name="view" :id="tab.id" />
    </div>

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
  background: var(--ide-surface-bg);
}

/* Tabs */
.editor-tabs {
  display: flex;
  align-items: stretch;
  background: var(--ide-header-bg);
  border-bottom: 0.5px solid var(--border-hairline);
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
  border-right: 0.5px solid var(--border-hairline);
  border-top: 1.5px solid transparent;
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
  font-weight: 500;
  background: var(--ide-surface-bg);
  border-top-color: var(--accent);
}

.tab-icon {
  display: flex;
  align-items: center;
  width: 6px;
}

.editor-tab--view .tab-icon {
  width: auto;
  color: var(--text-muted);
}

.editor-tab--view.active .tab-icon {
  color: var(--accent);
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

.editor-view {
  flex: 1;
  min-height: 0;
  overflow: hidden;
}

/* Force opaque backgrounds on Monaco's absolutely-positioned overlay elements
   so that horizontally-scrolled content doesn't bleed through the minimap or
   overview ruler area on the right side of the editor. */
.editor-container :deep(.monaco-editor .minimap) {
  background-color: var(--ide-surface-bg);
}
.editor-container :deep(.monaco-editor .minimap-gutter) {
  background-color: var(--ide-surface-bg);
}
.editor-container :deep(.monaco-editor .decorationsOverviewRuler) {
  background-color: var(--ide-surface-bg);
}
.editor-container :deep(.monaco-editor .monaco-scrollable-element > .scrollbar.vertical) {
  background-color: var(--ide-surface-bg);
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
  font-weight: 500;
  color: var(--accent);
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
  border: 0.5px solid var(--border-default);
  border-radius: var(--radius-sm);
  font-family: var(--font-sans);
  font-size: 0.75rem;
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
