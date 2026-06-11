<script setup lang="ts">
import { ref, onMounted, onUnmounted } from 'vue'
import { workspaceApi } from '@/api/workspaces'
import { useFileWatcher, type FsEvent } from '@/composables/useFileWatcher'

const props = defineProps<{
  workspaceId: number
  workspaceName: string
}>()

const emit = defineEmits<{
  select: [path: string]
}>()

interface FileNode {
  name: string
  path: string
  type: 'file' | 'directory'
  children?: FileNode[]
  expanded?: boolean
  loading?: boolean
}

const files = ref<FileNode[]>([])
const loading = ref(true)
const refreshing = ref(false)
const selectedPath = ref<string | null>(null)
const operationError = ref<string | null>(null)
const uploadInput = ref<HTMLInputElement | null>(null)
const uploadTargetDirectory = ref('/workspace')
const uploading = ref(false)

// Filesystem watcher
const { connect: connectWatcher, onEvent } = useFileWatcher(() => props.workspaceId)

// Debounce directory refreshes to avoid flooding on rapid changes
let refreshTimers = new Map<string, ReturnType<typeof setTimeout>>()

function debouncedRefreshDir(dirPath: string) {
  const existing = refreshTimers.get(dirPath)
  if (existing) clearTimeout(existing)
  refreshTimers.set(dirPath, setTimeout(() => {
    refreshTimers.delete(dirPath)
    refreshDirectory(dirPath)
  }, 300))
}

onEvent((event: FsEvent) => {
  // Determine which parent directory was affected
  const parentPath = event.path.substring(0, event.path.lastIndexOf('/')) || '/workspace'
  debouncedRefreshDir(parentPath)
})

onMounted(async () => {
  await loadRootDirectory()
  loading.value = false
  connectWatcher()
})

onUnmounted(() => {
  refreshTimers.forEach((timer) => clearTimeout(timer))
  refreshTimers.clear()
})

async function loadRootDirectory() {
  try {
    const res = await workspaceApi.listFiles(props.workspaceId, '/workspace')
    files.value = mapEntries(res.entries)
  } catch {
    files.value = []
  }
}

function getErrorMessage(err: unknown, fallback: string): string {
  return err instanceof Error && err.message ? err.message : fallback
}

async function refreshTree() {
  refreshing.value = true
  try {
    await loadRootDirectory()
    // Re-expand previously expanded directories
    await reExpandNodes(files.value)
  } catch {
    // Silently fail — tree stays as-is
  }
  refreshing.value = false
}

async function reExpandNodes(nodes: FileNode[]) {
  for (const node of nodes) {
    if (node.type === 'directory' && node.expanded) {
      try {
        const res = await workspaceApi.listFiles(props.workspaceId, node.path)
        const newChildren = mapEntries(res.entries)
        // Preserve expanded state of children
        for (const child of newChildren) {
          const existing = node.children?.find(c => c.path === child.path)
          if (existing && existing.expanded) {
            child.expanded = true
            child.children = existing.children
          }
        }
        node.children = newChildren
        await reExpandNodes(node.children)
      } catch {
        // Keep existing children on error
      }
    }
  }
}

async function refreshDirectory(dirPath: string) {
  // If it's the root, refresh the root entries
  if (dirPath === '/workspace') {
    try {
      const res = await workspaceApi.listFiles(props.workspaceId, '/workspace')
      const newEntries = mapEntries(res.entries)
      // Preserve expanded state
      for (const entry of newEntries) {
        const existing = files.value.find(f => f.path === entry.path)
        if (existing && existing.expanded) {
          entry.expanded = true
          entry.children = existing.children
        }
      }
      files.value = newEntries
    } catch {
      // Silently fail
    }
    return
  }

  // Find the node in the tree and refresh its children
  const node = findNode(files.value, dirPath)
  if (node && node.expanded) {
    try {
      const res = await workspaceApi.listFiles(props.workspaceId, node.path)
      const newChildren = mapEntries(res.entries)
      // Preserve expanded state of children
      for (const child of newChildren) {
        const existing = node.children?.find(c => c.path === child.path)
        if (existing && existing.expanded) {
          child.expanded = true
          child.children = existing.children
        }
      }
      node.children = newChildren
    } catch {
      // Silently fail
    }
  }
}

function findNode(nodes: FileNode[], path: string): FileNode | null {
  for (const node of nodes) {
    if (node.path === path) return node
    if (node.children) {
      const found = findNode(node.children, path)
      if (found) return found
    }
  }
  return null
}

function mapEntries(entries: { name: string; path: string; type: string }[]): FileNode[] {
  return entries.map(e => ({
    name: e.name,
    path: e.path,
    type: e.type as 'file' | 'directory',
    children: e.type === 'directory' ? [] : undefined,
    expanded: false,
    loading: false,
  }))
}

async function toggleDir(node: FileNode) {
  if (node.expanded) {
    node.expanded = false
    return
  }

  if (node.children && node.children.length === 0) {
    node.loading = true
    try {
      const res = await workspaceApi.listFiles(props.workspaceId, node.path)
      node.children = mapEntries(res.entries)
    } catch {
      node.children = []
    }
    node.loading = false
  }

  node.expanded = true
}

function selectFile(node: FileNode) {
  selectedPath.value = node.path
  emit('select', node.path)
}

// --- Create file/directory ---
const creatingIn = ref<string | null>(null)
const creatingType = ref<'file' | 'directory' | null>(null)
const creatingName = ref('')

function startCreate(parentPath: string, type: 'file' | 'directory') {
  creatingIn.value = parentPath
  creatingType.value = type
  creatingName.value = ''

  // Ensure the parent directory is expanded
  if (parentPath !== '/workspace') {
    const node = findNode(files.value, parentPath)
    if (node && !node.expanded) {
      toggleDir(node)
    }
  }
}

async function confirmCreate() {
  const name = creatingName.value.trim()
  if (!name || !creatingIn.value || !creatingType.value) return

  const fullPath = `${creatingIn.value}/${name}`
  try {
    operationError.value = null
    if (creatingType.value === 'directory') {
      await workspaceApi.mkdir(props.workspaceId, fullPath)
    } else {
      await workspaceApi.writeFile(props.workspaceId, fullPath, '')
    }
    await refreshDirectory(creatingIn.value)
  } catch (err) {
    operationError.value = getErrorMessage(err, `Failed to create ${creatingType.value}`)
  }
  cancelCreate()
}

function cancelCreate() {
  creatingIn.value = null
  creatingType.value = null
  creatingName.value = ''
}

// --- Delete file/directory ---
const deleteTarget = ref<FileNode | null>(null)
const showDeleteConfirm = ref(false)

function requestDelete(node: FileNode) {
  deleteTarget.value = node
  showDeleteConfirm.value = true
}

async function confirmDelete() {
  if (!deleteTarget.value) return
  try {
    operationError.value = null
    await workspaceApi.deleteFile(props.workspaceId, deleteTarget.value.path)
    // Refresh parent directory
    const parentPath = deleteTarget.value.path.substring(0, deleteTarget.value.path.lastIndexOf('/')) || '/workspace'
    await refreshDirectory(parentPath)
  } catch (err) {
    operationError.value = getErrorMessage(err, 'Failed to delete item')
  }
  showDeleteConfirm.value = false
  deleteTarget.value = null
}

function cancelDelete() {
  showDeleteConfirm.value = false
  deleteTarget.value = null
}

function focusInput(e: { el: HTMLElement }) {
  e.el.focus()
}

// --- Download file ---
async function downloadFile(node: FileNode) {
  operationError.value = null
  try {
    const blob = await workspaceApi.downloadFile(props.workspaceId, node.path)
    const url = URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url
    a.download = node.name
    a.click()
    URL.revokeObjectURL(url)
  } catch (err) {
    operationError.value = getErrorMessage(err, 'Failed to download file')
  }
}

// --- Upload file ---
function requestUpload(directoryPath: string) {
  uploadTargetDirectory.value = directoryPath
  operationError.value = null
  if (uploadInput.value) {
    uploadInput.value.value = ''
    uploadInput.value.click()
  }
}

async function uploadSelectedFile(event: Event) {
  const input = event.target as HTMLInputElement
  const file = input.files?.[0]
  if (!file) return

  uploading.value = true
  operationError.value = null
  try {
    const res = await workspaceApi.uploadFile(props.workspaceId, uploadTargetDirectory.value, file)
    await refreshDirectory(uploadTargetDirectory.value)
    selectedPath.value = res.path
    emit('select', res.path)
  } catch (err) {
    operationError.value = getErrorMessage(err, 'Failed to upload file')
  } finally {
    uploading.value = false
    input.value = ''
  }
}
</script>

<template>
  <div class="file-explorer">
    <div class="explorer-header">
      <span class="explorer-title">
        <svg class="panel-icon" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
          <path d="M20 20a2 2 0 0 0 2-2V8a2 2 0 0 0-2-2h-7.9a2 2 0 0 1-1.69-.9L9.6 3.9A2 2 0 0 0 7.93 3H4a2 2 0 0 0-2 2v13a2 2 0 0 0 2 2Z" />
        </svg>
        Explorer
      </span>
      <div class="header-actions">
        <button
          class="header-btn"
          title="New File"
          @click="startCreate('/workspace', 'file')"
        >
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
            <path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8Z" /><path d="M14 2v6h6" /><path d="M12 18v-6" /><path d="M9 15h6" />
          </svg>
        </button>
        <button
          class="header-btn"
          title="New Folder"
          @click="startCreate('/workspace', 'directory')"
        >
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
            <path d="M12 10v6" /><path d="M9 13h6" /><path d="M20 20a2 2 0 0 0 2-2V8a2 2 0 0 0-2-2h-7.9a2 2 0 0 1-1.69-.9L9.6 3.9A2 2 0 0 0 7.93 3H4a2 2 0 0 0-2 2v13a2 2 0 0 0 2 2Z" />
          </svg>
        </button>
        <button
          class="header-btn"
          :disabled="uploading"
          title="Upload File"
          @click="requestUpload('/workspace')"
        >
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
            <path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4" /><polyline points="17 8 12 3 7 8" /><line x1="12" y1="3" x2="12" y2="15" />
          </svg>
        </button>
        <button
          class="header-btn"
          :class="{ spinning: refreshing }"
          title="Refresh file tree"
          @click="refreshTree"
        >
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
            <path d="M21 12a9 9 0 1 1-9-9c2.52 0 4.93 1 6.74 2.74L21 8" />
            <path d="M21 3v5h-5" />
          </svg>
        </button>
      </div>
    </div>
    <input
      ref="uploadInput"
      class="upload-input"
      type="file"
      @change="uploadSelectedFile"
    />
    <div v-if="operationError" class="explorer-error" role="alert">
      <span>{{ operationError }}</span>
      <button class="error-dismiss" title="Dismiss" @click="operationError = null">×</button>
    </div>
    <div class="file-tree">
      <!-- Root-level inline creation input -->
      <div
        v-if="creatingIn === '/workspace' && creatingType"
        class="tree-item tree-create-input"
        :style="{ paddingLeft: '12px' }"
      >
        <svg v-if="creatingType === 'directory'" width="16" height="16" viewBox="0 0 24 24" fill="none">
          <path d="M4 4h5l2 2h8a2 2 0 0 1 2 2v10a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2V6a2 2 0 0 1 2-2Z" stroke="currentColor" stroke-width="1.5" fill="none" stroke-linecap="round" stroke-linejoin="round" />
        </svg>
        <svg v-else width="16" height="16" viewBox="0 0 24 24" fill="none">
          <rect x="2" y="2" width="20" height="20" rx="3" fill="none" stroke="currentColor" stroke-width="1.5" />
          <path d="M8 8h8M8 12h8M8 16h5" stroke="currentColor" stroke-width="1.5" fill="none" stroke-linecap="round" />
        </svg>
        <input
          class="create-input"
          type="text"
          v-model="creatingName"
          :placeholder="creatingType === 'directory' ? 'folder name' : 'file name'"
          @keydown.enter="confirmCreate"
          @keydown.escape="cancelCreate"
          @vue:mounted="focusInput"
        />
      </div>
      <!-- Empty state -->
      <div v-if="!loading && files.length === 0 && !creatingIn" class="explorer-empty">
        <svg class="explorer-empty-icon" width="36" height="36" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.2" stroke-linecap="round" stroke-linejoin="round">
          <path d="M20 20a2 2 0 0 0 2-2V8a2 2 0 0 0-2-2h-7.9a2 2 0 0 1-1.69-.9L9.6 3.9A2 2 0 0 0 7.93 3H4a2 2 0 0 0-2 2v13a2 2 0 0 0 2 2Z" />
        </svg>
        <span class="explorer-empty-title">No files yet</span>
        <span class="explorer-empty-hint">Create a file or folder to get started</span>
        <div class="explorer-empty-actions">
          <button class="explorer-empty-btn" @click="startCreate('/workspace', 'file')">
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
              <path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8Z" /><path d="M14 2v6h6" /><path d="M12 18v-6" /><path d="M9 15h6" />
            </svg>
            New File
          </button>
          <button class="explorer-empty-btn" @click="startCreate('/workspace', 'directory')">
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
              <path d="M12 10v6" /><path d="M9 13h6" /><path d="M20 20a2 2 0 0 0 2-2V8a2 2 0 0 0-2-2h-7.9a2 2 0 0 1-1.69-.9L9.6 3.9A2 2 0 0 0 7.93 3H4a2 2 0 0 0-2 2v13a2 2 0 0 0 2 2Z" />
            </svg>
            New Folder
          </button>
        </div>
      </div>
      <template v-for="node in files" :key="node.path">
        <component
          :is="'div'"
          v-bind="{ class: 'tree-node-wrapper' }"
        >
          <FileTreeNode
            :node="node"
            :depth="0"
            :selected-path="selectedPath"
            :creating-in="creatingIn"
            :creating-type="creatingType"
            :creating-name="creatingName"
            @toggle="toggleDir"
            @select="selectFile"
            @create-file="startCreate($event, 'file')"
            @create-dir="startCreate($event, 'directory')"
            @upload="requestUpload"
            @delete="requestDelete"
            @download="downloadFile"
            @update:creating-name="creatingName = $event"
            @confirm-create="confirmCreate"
            @cancel-create="cancelCreate"
          />
        </component>
      </template>
    </div>

    <ConfirmModal
      :show="showDeleteConfirm"
      :title="deleteTarget?.type === 'directory' ? 'Delete Folder' : 'Delete File'"
      :message="`Are you sure you want to delete '${deleteTarget?.name}'?${deleteTarget?.type === 'directory' ? ' This will delete all contents inside it.' : ''}`"
      confirm-label="Delete"
      variant="danger"
      @confirm="confirmDelete"
      @cancel="cancelDelete"
    />
  </div>
</template>

<script lang="ts">
import FileTreeNode from './FileTreeNode.vue'
import ConfirmModal from '@/components/ConfirmModal.vue'
</script>

<style scoped>
.file-explorer {
  --ide-header-icon: var(--accent-blue);
  display: flex;
  flex-direction: column;
  height: 100%;
  font-size: 0.8rem;
  background: var(--ide-panel-bg);
}

.explorer-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 0 var(--space-3);
  border-bottom: 0.5px solid var(--border-default);
  background: var(--ide-header-bg);
  height: 38px;
  flex-shrink: 0;
}

.explorer-title {
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

.header-actions {
  display: flex;
  align-items: center;
  gap: 2px;
}

.header-btn {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 22px;
  height: 22px;
  padding: 0;
  border: none;
  border-radius: 4px;
  background: transparent;
  color: var(--text-secondary);
  cursor: pointer;
  transition: color 150ms ease, background 150ms ease;
}

.header-btn:hover {
  color: var(--text-primary);
  background: var(--bg-hover);
}

.header-btn:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

.upload-input {
  display: none;
}

.explorer-error {
  display: flex;
  align-items: flex-start;
  gap: var(--space-2);
  padding: var(--space-2) var(--space-3);
  border-bottom: 0.5px solid var(--border-default);
  color: var(--accent-rose);
  background: var(--bg-surface);
  font-size: 0.72rem;
  line-height: 1.35;
}

.error-dismiss {
  margin-left: auto;
  color: var(--text-secondary);
  cursor: pointer;
  transition: color var(--transition-fast);
}

.error-dismiss:hover {
  color: var(--text-primary);
}

.header-btn.spinning svg {
  animation: spin-refresh 0.6s linear infinite;
}

@keyframes spin-refresh {
  to { transform: rotate(360deg); }
}

.tree-item {
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 3px 0;
  padding-right: var(--space-3);
  cursor: pointer;
  user-select: none;
  white-space: nowrap;
  font-size: 0.8rem;
}

.tree-create-input {
  cursor: default;
}

.create-input {
  flex: 1;
  min-width: 0;
  padding: 1px 4px;
  border: 0.5px solid var(--accent-blue);
  border-radius: 3px;
  background: var(--bg-primary);
  color: var(--text-primary);
  font-size: 0.8rem;
  font-family: var(--font-sans);
  outline: none;
}

.file-tree {
  flex: 1;
  overflow-y: auto;
  padding: var(--space-1) 0;
}

/* Empty state */
.explorer-empty {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: var(--space-2);
  padding: var(--space-8) var(--space-4);
  text-align: center;
}

.explorer-empty-icon {
  color: var(--text-muted);
  opacity: 0.4;
  margin-bottom: var(--space-1);
}

.explorer-empty-title {
  font-size: 0.82rem;
  font-weight: 600;
  color: var(--text-secondary);
}

.explorer-empty-hint {
  font-size: 0.72rem;
  color: var(--text-muted);
  line-height: 1.4;
}

.explorer-empty-actions {
  display: flex;
  gap: var(--space-2);
  margin-top: var(--space-2);
}

.explorer-empty-btn {
  display: flex;
  align-items: center;
  gap: 5px;
  padding: var(--space-1) var(--space-3);
  font-size: 0.72rem;
  font-weight: 500;
  color: var(--text-secondary);
  background: var(--bg-hover);
  border: 0.5px solid var(--border-default);
  border-radius: var(--radius-sm);
  cursor: pointer;
  transition: all var(--transition-fast);
}

.explorer-empty-btn:hover {
  color: var(--text-primary);
  background: var(--bg-tertiary);
  border-color: var(--accent-blue);
}
</style>
