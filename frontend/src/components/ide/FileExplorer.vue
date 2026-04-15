<script setup lang="ts">
import { ref } from 'vue'

const props = defineProps<{
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
}

// Placeholder file tree
const files = ref<FileNode[]>([
  {
    name: 'src',
    path: '/src',
    type: 'directory',
    expanded: true,
    children: [
      {
        name: 'components',
        path: '/src/components',
        type: 'directory',
        expanded: false,
        children: [
          { name: 'App.vue', path: '/src/components/App.vue', type: 'file' },
          { name: 'Header.vue', path: '/src/components/Header.vue', type: 'file' },
          { name: 'Sidebar.vue', path: '/src/components/Sidebar.vue', type: 'file' },
        ],
      },
      { name: 'main.ts', path: '/src/main.ts', type: 'file' },
      { name: 'router.ts', path: '/src/router.ts', type: 'file' },
      { name: 'styles.css', path: '/src/styles.css', type: 'file' },
    ],
  },
  {
    name: 'public',
    path: '/public',
    type: 'directory',
    expanded: false,
    children: [
      { name: 'index.html', path: '/public/index.html', type: 'file' },
      { name: 'favicon.ico', path: '/public/favicon.ico', type: 'file' },
    ],
  },
  { name: 'package.json', path: '/package.json', type: 'file' },
  { name: 'tsconfig.json', path: '/tsconfig.json', type: 'file' },
  { name: 'README.md', path: '/README.md', type: 'file' },
])

const selectedPath = ref<string | null>(null)

function toggleDir(node: FileNode) {
  node.expanded = !node.expanded
}

function selectFile(node: FileNode) {
  selectedPath.value = node.path
  emit('select', node.path)
}
</script>

<template>
  <div class="file-explorer">
    <div class="explorer-header">
      <span class="explorer-title">Explorer</span>
    </div>
    <div class="explorer-section-label">
      <svg width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
        <polyline points="6 9 12 15 18 9" />
      </svg>
      {{ props.workspaceName || 'Workspace' }}
    </div>
    <div class="file-tree">
      <template v-for="node in files" :key="node.path">
        <component
          :is="'div'"
          v-bind="{ class: 'tree-node-wrapper' }"
        >
          <FileTreeNode
            :node="node"
            :depth="0"
            :selected-path="selectedPath"
            @toggle="toggleDir"
            @select="selectFile"
          />
        </component>
      </template>
    </div>
  </div>
</template>

<script lang="ts">
import FileTreeNode from './FileTreeNode.vue'
</script>

<style scoped>
.file-explorer {
  display: flex;
  flex-direction: column;
  height: 100%;
  font-size: 0.8rem;
}

.explorer-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: var(--space-2) var(--space-3);
  flex-shrink: 0;
}

.explorer-title {
  font-size: 0.7rem;
  font-weight: 600;
  text-transform: uppercase;
  letter-spacing: 0.05em;
  color: var(--text-secondary);
}

.explorer-section-label {
  display: flex;
  align-items: center;
  gap: 6px;
  padding: var(--space-1) var(--space-3);
  font-size: 0.75rem;
  font-weight: 600;
  color: var(--text-primary);
  text-transform: uppercase;
  letter-spacing: 0.02em;
  background: var(--bg-hover);
}

.file-tree {
  flex: 1;
  overflow-y: auto;
  padding: var(--space-1) 0;
}
</style>
