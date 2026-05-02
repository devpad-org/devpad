<script setup lang="ts">
interface FileNode {
  name: string
  path: string
  type: 'file' | 'directory'
  children?: FileNode[]
  expanded?: boolean
}

defineProps<{
  node: FileNode
  depth: number
  selectedPath: string | null
  creatingIn: string | null
  creatingType: 'file' | 'directory' | null
  creatingName: string
}>()

const emit = defineEmits<{
  toggle: [node: FileNode]
  select: [node: FileNode]
  'create-file': [parentPath: string]
  'create-dir': [parentPath: string]
  delete: [node: FileNode]
  download: [node: FileNode]
  'update:creatingName': [value: string]
  'confirm-create': []
  'cancel-create': []
}>()

interface IconInfo {
  color: string
  type: 'vue' | 'ts' | 'js' | 'css' | 'html' | 'json' | 'md' | 'image' | 'generic'
}

function getIconInfo(name: string): IconInfo {
  if (name.endsWith('.vue')) return { color: 'currentColor', type: 'vue' }
  if (name.endsWith('.ts') || name.endsWith('.tsx')) return { color: 'currentColor', type: 'ts' }
  if (name.endsWith('.js') || name.endsWith('.jsx')) return { color: 'currentColor', type: 'js' }
  if (name.endsWith('.css') || name.endsWith('.scss') || name.endsWith('.less')) return { color: 'currentColor', type: 'css' }
  if (name.endsWith('.html')) return { color: 'currentColor', type: 'html' }
  if (name.endsWith('.json')) return { color: 'currentColor', type: 'json' }
  if (name.endsWith('.md')) return { color: 'currentColor', type: 'md' }
  if (name.endsWith('.png') || name.endsWith('.jpg') || name.endsWith('.svg') || name.endsWith('.ico')) return { color: 'currentColor', type: 'image' }
  return { color: 'currentColor', type: 'generic' }
}

function stopPropagation(e: Event, action: () => void) {
  e.stopPropagation()
  action()
}

function focusInput(e: { el: HTMLElement }) {
  e.el.focus()
}
</script>

<template>
  <div>
    <div
      v-if="node.type === 'directory'"
      class="tree-item tree-dir"
      :style="{ paddingLeft: `${depth * 16 + 12}px` }"
      @click="emit('toggle', node)"
    >
      <!-- Directory icon -->
      <svg class="dir-icon" width="16" height="16" viewBox="0 0 24 24" fill="none">
        <path
          v-if="node.expanded"
          d="M4 4h5l2 2h8a2 2 0 0 1 2 2v1H3V6a2 2 0 0 1 1-2Z"
          stroke="currentColor" stroke-width="1.5" fill="none" stroke-linecap="round" stroke-linejoin="round"
        />
        <path
          v-if="node.expanded"
          d="M3 9h18l-1.5 10a2 2 0 0 1-2 1H6.5a2 2 0 0 1-2-1L3 9Z"
          stroke="currentColor" stroke-width="1.5" fill="none" stroke-linecap="round" stroke-linejoin="round"
        />
        <path
          v-else
          d="M4 4h5l2 2h8a2 2 0 0 1 2 2v10a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2V6a2 2 0 0 1 2-2Z"
          stroke="currentColor" stroke-width="1.5" fill="none" stroke-linecap="round" stroke-linejoin="round"
        />
      </svg>
      <span class="tree-label">{{ node.name }}</span>
      <span class="tree-actions">
        <button class="action-btn" title="New File" @click="stopPropagation($event, () => emit('create-file', node.path))">
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
            <path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8Z" /><path d="M14 2v6h6" /><path d="M12 18v-6" /><path d="M9 15h6" />
          </svg>
        </button>
        <button class="action-btn" title="New Folder" @click="stopPropagation($event, () => emit('create-dir', node.path))">
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
            <path d="M12 10v6" /><path d="M9 13h6" /><path d="M20 20a2 2 0 0 0 2-2V8a2 2 0 0 0-2-2h-7.9a2 2 0 0 1-1.69-.9L9.6 3.9A2 2 0 0 0 7.93 3H4a2 2 0 0 0-2 2v13a2 2 0 0 0 2 2Z" />
          </svg>
        </button>
        <button class="action-btn action-btn--danger" title="Delete" @click="stopPropagation($event, () => emit('delete', node))">
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
            <path d="M3 6h18" /><path d="M19 6v14c0 1-1 2-2 2H7c-1 0-2-1-2-2V6" /><path d="M8 6V4c0-1 1-2 2-2h4c1 0 2 1 2 2v2" />
          </svg>
        </button>
      </span>
    </div>
    <div
      v-else
      class="tree-item tree-file"
      :class="{ selected: selectedPath === node.path }"
      :style="{ paddingLeft: `${depth * 16 + 12}px` }"
      @click="emit('select', node)"
    >
      <!-- File type icons -->
      <svg class="file-icon" width="16" height="16" viewBox="0 0 24 24">
        <!-- Vue -->
        <template v-if="getIconInfo(node.name).type === 'vue'">
          <rect x="2" y="2" width="20" height="20" rx="3" fill="none" :stroke="getIconInfo(node.name).color" stroke-width="1.5" />
          <path d="M7 8l5 8 5-8" :stroke="getIconInfo(node.name).color" stroke-width="1.5" fill="none" stroke-linecap="round" stroke-linejoin="round" />
        </template>
        <!-- TypeScript -->
        <template v-else-if="getIconInfo(node.name).type === 'ts'">
          <rect x="2" y="2" width="20" height="20" rx="3" fill="none" :stroke="getIconInfo(node.name).color" stroke-width="1.5" />
          <text x="12" y="16.5" text-anchor="middle" font-size="10" font-weight="700" font-family="var(--font-sans)" :fill="getIconInfo(node.name).color">TS</text>
        </template>
        <!-- JavaScript -->
        <template v-else-if="getIconInfo(node.name).type === 'js'">
          <rect x="2" y="2" width="20" height="20" rx="3" fill="none" :stroke="getIconInfo(node.name).color" stroke-width="1.5" />
          <text x="12" y="16.5" text-anchor="middle" font-size="10" font-weight="700" font-family="var(--font-sans)" :fill="getIconInfo(node.name).color">JS</text>
        </template>
        <!-- CSS -->
        <template v-else-if="getIconInfo(node.name).type === 'css'">
          <rect x="2" y="2" width="20" height="20" rx="3" fill="none" :stroke="getIconInfo(node.name).color" stroke-width="1.5" />
          <text x="12" y="16.5" text-anchor="middle" font-size="10" font-weight="700" font-family="var(--font-mono)" :fill="getIconInfo(node.name).color">{ }</text>
        </template>
        <!-- HTML -->
        <template v-else-if="getIconInfo(node.name).type === 'html'">
          <rect x="2" y="2" width="20" height="20" rx="3" fill="none" :stroke="getIconInfo(node.name).color" stroke-width="1.5" />
          <text x="12" y="16" text-anchor="middle" font-size="9" font-weight="700" font-family="var(--font-mono)" :fill="getIconInfo(node.name).color">&lt;/&gt;</text>
        </template>
        <!-- JSON -->
        <template v-else-if="getIconInfo(node.name).type === 'json'">
          <rect x="2" y="2" width="20" height="20" rx="3" fill="none" :stroke="getIconInfo(node.name).color" stroke-width="1.5" />
          <text x="12" y="16.5" text-anchor="middle" font-size="10" font-weight="700" font-family="var(--font-mono)" :fill="getIconInfo(node.name).color">{ }</text>
        </template>
        <!-- Markdown -->
        <template v-else-if="getIconInfo(node.name).type === 'md'">
          <rect x="2" y="2" width="20" height="20" rx="3" fill="none" :stroke="getIconInfo(node.name).color" stroke-width="1.5" />
          <path d="M7 15V9l2.5 3L12 9v6M15.5 12l2-2.5L19.5 12" :stroke="getIconInfo(node.name).color" stroke-width="1.5" fill="none" stroke-linecap="round" stroke-linejoin="round" />
        </template>
        <!-- Image -->
        <template v-else-if="getIconInfo(node.name).type === 'image'">
          <rect x="2" y="2" width="20" height="20" rx="3" fill="none" :stroke="getIconInfo(node.name).color" stroke-width="1.5" />
          <circle cx="9" cy="9" r="2" :fill="getIconInfo(node.name).color" opacity="0.6" />
          <path d="M4 17l4.5-4.5 3 3 3.5-3.5L20 17" :stroke="getIconInfo(node.name).color" stroke-width="1.5" fill="none" stroke-linecap="round" stroke-linejoin="round" />
        </template>
        <!-- Generic file -->
        <template v-else>
          <rect x="2" y="2" width="20" height="20" rx="3" fill="none" :stroke="getIconInfo(node.name).color" stroke-width="1.5" />
          <path d="M8 8h8M8 12h8M8 16h5" :stroke="getIconInfo(node.name).color" stroke-width="1.5" fill="none" stroke-linecap="round" />
        </template>
      </svg>
      <span class="tree-label">{{ node.name }}</span>
      <span class="tree-actions">
        <button class="action-btn" title="Download" @click="stopPropagation($event, () => emit('download', node))">
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
            <path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4" /><polyline points="7 10 12 15 17 10" /><line x1="12" y1="15" x2="12" y2="3" />
          </svg>
        </button>
        <button class="action-btn action-btn--danger" title="Delete" @click="stopPropagation($event, () => emit('delete', node))">
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
            <path d="M3 6h18" /><path d="M19 6v14c0 1-1 2-2 2H7c-1 0-2-1-2-2V6" /><path d="M8 6V4c0-1 1-2 2-2h4c1 0 2 1 2 2v2" />
          </svg>
        </button>
      </span>
    </div>
    <template v-if="node.type === 'directory' && node.expanded && node.children">
      <!-- Inline creation input -->
      <div
        v-if="creatingIn === node.path && creatingType"
        class="tree-item tree-create-input"
        :style="{ paddingLeft: `${(depth + 1) * 16 + 12}px` }"
      >
        <svg v-if="creatingType === 'directory'" class="dir-icon" width="16" height="16" viewBox="0 0 24 24" fill="none">
          <path d="M4 4h5l2 2h8a2 2 0 0 1 2 2v10a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2V6a2 2 0 0 1 2-2Z" stroke="currentColor" stroke-width="1.5" fill="none" stroke-linecap="round" stroke-linejoin="round" />
        </svg>
        <svg v-else class="file-icon" width="16" height="16" viewBox="0 0 24 24" fill="none">
          <rect x="2" y="2" width="20" height="20" rx="3" fill="none" stroke="currentColor" stroke-width="1.5" />
          <path d="M8 8h8M8 12h8M8 16h5" stroke="currentColor" stroke-width="1.5" fill="none" stroke-linecap="round" />
        </svg>
        <input
          class="create-input"
          type="text"
          :value="creatingName"
          :placeholder="creatingType === 'directory' ? 'folder name' : 'file name'"
          @input="emit('update:creatingName', ($event.target as HTMLInputElement).value)"
          @keydown.enter="emit('confirm-create')"
          @keydown.escape="emit('cancel-create')"
          @vue:mounted="focusInput"
        />
      </div>
      <FileTreeNode
        v-for="child in node.children"
        :key="child.path"
        :node="child"
        :depth="depth + 1"
        :selected-path="selectedPath"
        :creating-in="creatingIn"
        :creating-type="creatingType"
        :creating-name="creatingName"
        @toggle="emit('toggle', $event)"
        @select="emit('select', $event)"
        @create-file="emit('create-file', $event)"
        @create-dir="emit('create-dir', $event)"
        @delete="emit('delete', $event)"
        @download="emit('download', $event)"
        @update:creating-name="emit('update:creatingName', $event)"
        @confirm-create="emit('confirm-create')"
        @cancel-create="emit('cancel-create')"
      />
    </template>
  </div>
</template>

<style scoped>
.tree-item {
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 3px 0;
  padding-right: var(--space-3);
  cursor: pointer;
  user-select: none;
  white-space: nowrap;
  transition: background var(--transition-fast);
  font-size: 0.8rem;
}

.tree-item:hover {
  background: var(--bg-hover);
}

.tree-item:hover .tree-actions {
  opacity: 1;
}

.tree-actions {
  display: flex;
  align-items: center;
  gap: 2px;
  margin-left: auto;
  opacity: 0;
  transition: opacity var(--transition-fast);
}

.action-btn {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 20px;
  height: 20px;
  padding: 0;
  border: none;
  border-radius: 3px;
  background: transparent;
  color: var(--text-secondary);
  cursor: pointer;
  transition: color 150ms ease, background 150ms ease;
}

.action-btn:hover {
  color: var(--text-primary);
  background: var(--bg-hover);
}

.action-btn--danger:hover {
  color: var(--accent-rose);
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

.tree-file.selected {
  background: var(--accent-glow);
  color: var(--accent-blue);
}

.dir-icon {
  flex-shrink: 0;
  width: 16px;
  height: 16px;
}

.file-icon {
  flex-shrink: 0;
  width: 16px;
  height: 16px;
  opacity: 0.85;
}

.tree-file.selected .file-icon {
  opacity: 1;
}

.tree-label {
  overflow: hidden;
  text-overflow: ellipsis;
  color: var(--text-secondary);
}

.tree-dir .tree-label {
  color: var(--text-primary);
}

.tree-file.selected .tree-label {
  color: var(--accent-blue);
}
</style>
