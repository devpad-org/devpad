<script setup lang="ts">
import MrIcon from '@/components/project/MrIcon.vue'
import { baseName } from './format'
import { useMergeRequest } from './useMergeRequest'

// The changed files, grouped by directory, with a filter.
const { fileFilter, fileGroups, selectedPath, commentCount, selectFile } = useMergeRequest()
</script>

<template>
  <nav class="file-tree" aria-label="Changed files">
    <div class="tree-search">
      <MrIcon name="search" :size="13" />
      <input v-model="fileFilter" type="text" placeholder="Filter files" aria-label="Filter files" />
    </div>
    <div class="tree-body">
      <div v-for="group in fileGroups" :key="group.dir" class="tree-group">
        <div v-if="group.dir" class="tree-dir" :title="group.dir">
          <MrIcon name="folder" :size="12" />
          <span class="ellipsis-start">{{ group.dir }}</span>
        </div>
        <button
          v-for="file in group.files"
          :key="file.path"
          type="button"
          class="tree-file"
          :class="{ active: file.path === selectedPath, nested: group.dir }"
          :title="file.oldPath ? `${file.oldPath} → ${file.path}` : file.path"
          @click="selectFile(file.path)"
        >
          <span class="file-status" :class="`status-${file.status}`">{{ file.status }}</span>
          <span class="file-name">{{ baseName(file.path) }}</span>
          <span v-if="commentCount(file.path)" class="file-comments">
            <MrIcon name="comment" :size="10" />
            {{ commentCount(file.path) }}
          </span>
          <span v-if="!file.binary" class="file-stats">
            <span class="add">+{{ file.additions }}</span>
            <span class="del">&minus;{{ file.deletions }}</span>
          </span>
        </button>
      </div>
      <p v-if="fileGroups.length === 0" class="empty-text tree-empty">No files match.</p>
    </div>
  </nav>
</template>

<style scoped src="@/components/project/shared.css"></style>
<style scoped src="./mergerequest.css"></style>
<style scoped>
/* A pane down the modal's side, or above the file when it is narrow. */
.file-tree {
  display: flex;
  flex-direction: column;
  min-height: 0;
  border-right: 0.5px solid var(--border-hairline);
  overflow: hidden;
}

@container (max-width: 800px) {
  .file-tree {
    max-height: 40vh;
    border-right: none;
    border-bottom: 0.5px solid var(--border-hairline);
  }
}

.tree-search {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  flex-shrink: 0;
  padding: var(--space-2) var(--space-3);
  border-bottom: 0.5px solid var(--border-hairline);
  color: var(--text-muted);
}

.tree-search input {
  flex: 1;
  min-width: 0;
  background: none;
  border: none;
  color: var(--text-primary);
  font-family: inherit;
  font-size: 0.78rem;
}

.tree-search input:focus {
  outline: none;
}

.tree-search:focus-within {
  color: var(--accent-blue);
}

.tree-body {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  padding: var(--space-1);
}

.tree-group + .tree-group {
  margin-top: var(--space-1);
}

.tree-dir {
  display: flex;
  align-items: center;
  gap: 6px;
  padding: var(--space-1) var(--space-2);
  font-family: var(--font-mono);
  font-size: 0.68rem;
  color: var(--text-muted);
}

.ellipsis-start {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  direction: rtl;
  text-align: left;
}

.tree-file {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  width: 100%;
  padding: 3px var(--space-2);
  border-radius: var(--radius-sm);
  font-size: 0.76rem;
  color: var(--text-secondary);
  text-align: left;
  transition: background var(--transition-fast);
}

.tree-file.nested {
  padding-left: calc(var(--space-2) + 14px);
}

.tree-file:hover {
  background: var(--bg-hover);
  color: var(--text-primary);
}

.tree-file.active {
  background: var(--bg-selected);
  color: var(--text-primary);
  box-shadow: inset 2px 0 0 var(--accent);
}

.file-status {
  font-family: var(--font-mono);
  font-size: 0.68rem;
  font-weight: 600;
  width: 1ch;
  flex-shrink: 0;
}

.file-name {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.file-comments {
  display: inline-flex;
  align-items: center;
  gap: 3px;
  padding: 0 5px;
  border-radius: 999px;
  background: color-mix(in srgb, var(--accent-blue) 16%, transparent);
  color: var(--accent-blue);
  font-size: 0.66rem;
}

.file-stats {
  display: flex;
  gap: var(--space-1);
  font-family: var(--font-mono);
  font-size: 0.66rem;
  flex-shrink: 0;
}

.tree-empty {
  text-align: center;
}
</style>
