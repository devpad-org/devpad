<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { workspaceApi } from '@/api/workspaces'

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
const selectedPath = ref<string | null>(null)

onMounted(async () => {
  await loadRootDirectory()
  loading.value = false
})

async function loadRootDirectory() {
  try {
    const res = await workspaceApi.listFiles(props.workspaceId, '/workspace')
    files.value = mapEntries(res.entries)
  } catch {
    files.value = []
  }
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
