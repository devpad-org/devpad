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
}>()

const emit = defineEmits<{
  toggle: [node: FileNode]
  select: [node: FileNode]
}>()

interface IconInfo {
  color: string
  type: 'vue' | 'ts' | 'js' | 'css' | 'html' | 'json' | 'md' | 'image' | 'generic'
}

function getIconInfo(name: string): IconInfo {
  if (name.endsWith('.vue')) return { color: '#42b883', type: 'vue' }
  if (name.endsWith('.ts') || name.endsWith('.tsx')) return { color: '#3178c6', type: 'ts' }
  if (name.endsWith('.js') || name.endsWith('.jsx')) return { color: '#f0db4f', type: 'js' }
  if (name.endsWith('.css') || name.endsWith('.scss') || name.endsWith('.less')) return { color: '#56b6c2', type: 'css' }
  if (name.endsWith('.html')) return { color: '#e34c26', type: 'html' }
  if (name.endsWith('.json')) return { color: '#f59e0b', type: 'json' }
  if (name.endsWith('.md')) return { color: '#9ca3af', type: 'md' }
  if (name.endsWith('.png') || name.endsWith('.jpg') || name.endsWith('.svg') || name.endsWith('.ico')) return { color: '#a78bfa', type: 'image' }
  return { color: '#6b7280', type: 'generic' }
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
      <svg
        class="tree-chevron"
        :class="{ expanded: node.expanded }"
        width="10"
        height="10"
        viewBox="0 0 24 24"
        fill="none"
        stroke="currentColor"
        stroke-width="2"
        stroke-linecap="round"
        stroke-linejoin="round"
      >
        <polyline points="9 18 15 12 9 6" />
      </svg>
      <!-- Directory icon -->
      <svg class="dir-icon" width="16" height="16" viewBox="0 0 24 24" fill="none">
        <path
          v-if="node.expanded"
          d="M4 4h5l2 2h8a2 2 0 0 1 2 2v1H3V6a2 2 0 0 1 1-2Z"
          stroke="#e8a87c" stroke-width="1.5" fill="none" stroke-linecap="round" stroke-linejoin="round"
        />
        <path
          v-if="node.expanded"
          d="M3 9h18l-1.5 10a2 2 0 0 1-2 1H6.5a2 2 0 0 1-2-1L3 9Z"
          stroke="#e8a87c" stroke-width="1.5" fill="none" stroke-linecap="round" stroke-linejoin="round"
        />
        <path
          v-else
          d="M4 4h5l2 2h8a2 2 0 0 1 2 2v10a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2V6a2 2 0 0 1 2-2Z"
          stroke="#e8a87c" stroke-width="1.5" fill="none" stroke-linecap="round" stroke-linejoin="round"
        />
      </svg>
      <span class="tree-label">{{ node.name }}</span>
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
    </div>
    <template v-if="node.type === 'directory' && node.expanded && node.children">
      <FileTreeNode
        v-for="child in node.children"
        :key="child.path"
        :node="child"
        :depth="depth + 1"
        :selected-path="selectedPath"
        @toggle="emit('toggle', $event)"
        @select="emit('select', $event)"
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

.tree-file.selected {
  background: rgba(0, 212, 255, 0.08);
  color: var(--accent-blue);
}

.tree-chevron {
  flex-shrink: 0;
  transition: transform var(--transition-fast);
}

.tree-chevron.expanded {
  transform: rotate(90deg);
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
