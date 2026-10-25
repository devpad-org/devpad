<script setup lang="ts">
import { computed, inject, onMounted, ref, watch } from 'vue'
import { gitApi } from '@/api/git'
import MonacoDiffViewer from '@/components/ide/MonacoDiffViewer.vue'
import MrIcon from '@/components/project/MrIcon.vue'
import { ideShellKey } from '@/components/ide/viewTabs'
import MrFileComments from './MrFileComments.vue'
import MrFileTree from './MrFileTree.vue'
import { baseName, dirName, fileStatusLabel, statBlocks } from './format'
import { useMergeRequest } from './useMergeRequest'

// The Changes section: the file tree and the selected file's diff and
// comments.
const props = defineProps<{
  // The tab is showing, so the workspace's branch is worth checking.
  active: boolean
}>()

const {
  mr,
  isOpen,
  files,
  orderedFiles,
  selectedFile,
  selectedIndex,
  oldContent,
  newContent,
  fileLoading,
  fileError,
  sideBySide,
  reveal,
  markedLines,
  stepFile,
  startComment,
} = useMergeRequest()

const shell = inject(ideShellKey, null)
const root = ref<HTMLElement | null>(null)

// The branch the project workspace has checked out, so "Open in editor" can
// say when the editor shows a different version than this diff.
const workspaceBranch = ref('')
const workspaceId = computed(() => shell?.liveWorkspaceId.value ?? null)
const canOpenInEditor = computed(() => workspaceId.value !== null && !!selectedFile.value && selectedFile.value.status !== 'D')
const otherBranch = computed(() =>
  canOpenInEditor.value && workspaceBranch.value && mr.value && workspaceBranch.value !== mr.value.sourceBranch
    ? workspaceBranch.value
    : '',
)

async function loadWorkspaceBranch() {
  const id = workspaceId.value
  if (id === null) {
    workspaceBranch.value = ''
    return
  }
  try {
    const res = await gitApi.branches(id)
    if (workspaceId.value === id) workspaceBranch.value = res.current
  } catch {
    workspaceBranch.value = ''
  }
}

watch(() => [props.active, workspaceId.value] as const, ([active]) => {
  if (active) void loadWorkspaceBranch()
}, { immediate: true })

function openInEditor() {
  const file = selectedFile.value
  if (!file || !shell) return
  shell.openFile(`/workspace/${file.path}`)
  void loadWorkspaceBranch()
}

onMounted(() => {
  // Side by side needs room; the tab may be narrower than the window.
  if (sideBySide.value === null) sideBySide.value = (root.value?.clientWidth ?? 0) >= 1000
})
</script>

<template>
  <div ref="root" class="changes-tab">
    <div v-if="files.length === 0" class="card empty-card">
      <MrIcon name="file" :size="28" :stroke-width="1.5" />
      <p>This branch changes nothing.</p>
    </div>
    <div v-else class="changes">
      <MrFileTree />

      <div v-if="selectedFile" class="file-view">
        <div class="file-bar">
          <span class="file-status-pill" :class="`status-${selectedFile.status}`">{{ fileStatusLabel(selectedFile) }}</span>
          <span class="file-bar-path" :title="selectedFile.path">
            <template v-if="selectedFile.oldPath"><span class="muted">{{ selectedFile.oldPath }} &rarr; </span></template>
            <span class="muted">{{ dirName(selectedFile.path) ? `${dirName(selectedFile.path)}/` : '' }}</span><strong>{{ baseName(selectedFile.path) }}</strong>
          </span>
          <span v-if="!selectedFile.binary" class="file-bar-stats">
            <span class="add">+{{ selectedFile.additions }}</span>
            <span class="del">&minus;{{ selectedFile.deletions }}</span>
            <span class="stat-blocks">
              <span v-for="(b, i) in statBlocks(selectedFile.additions, selectedFile.deletions)" :key="i" :class="`block-${b}`" />
            </span>
          </span>
          <div class="file-bar-end">
            <button
              v-if="canOpenInEditor"
              class="btn btn-secondary btn-sm"
              :title="`Open ${selectedFile.path} in the project workspace's editor`"
              @click="openInEditor"
            >
              Open in editor
            </button>
            <div class="segmented segmented-icons" role="radiogroup" aria-label="Diff layout">
              <button type="button" role="radio" class="segment" :class="{ active: sideBySide }" :aria-checked="!!sideBySide" title="Side by side" @click="sideBySide = true">
                <MrIcon name="split" :size="13" />
              </button>
              <button type="button" role="radio" class="segment" :class="{ active: !sideBySide }" :aria-checked="!sideBySide" title="Unified" @click="sideBySide = false">
                <MrIcon name="unified" :size="13" />
              </button>
            </div>
            <div class="pager">
              <button class="icon-button" :disabled="selectedIndex <= 0" aria-label="Previous file" title="Previous file" @click="stepFile(-1)">
                <MrIcon name="chevron-left" :size="14" />
              </button>
              <span class="pager-count">{{ selectedIndex + 1 }} / {{ orderedFiles.length }}</span>
              <button class="icon-button" :disabled="selectedIndex >= orderedFiles.length - 1" aria-label="Next file" title="Next file" @click="stepFile(1)">
                <MrIcon name="chevron-right" :size="14" />
              </button>
            </div>
          </div>
        </div>

        <p v-if="otherBranch" class="notice notice-warning branch-note">
          Your workspace is on <span class="mono">{{ otherBranch }}</span>, not <span class="mono">{{ mr?.sourceBranch }}</span>,
          so the editor shows that branch's version of this file.
        </p>

        <p v-if="fileError" class="notice notice-error">{{ fileError }}</p>
        <div v-else-if="selectedFile.binary" class="card empty-card">
          <MrIcon name="file" :size="28" :stroke-width="1.5" />
          <p>Binary file; no preview.</p>
        </div>
        <div v-else class="diff-frame" :class="{ loading: fileLoading }">
          <MonacoDiffViewer
            :old-content="oldContent"
            :new-content="newContent"
            :old-file-name="selectedFile.oldPath || selectedFile.path"
            :new-file-name="selectedFile.path"
            :side-by-side="sideBySide ?? true"
            :marked-lines="markedLines"
            :reveal="reveal"
            :commentable="isOpen"
            @line-click="startComment"
          />
          <span v-if="fileLoading" class="diff-loading"><span class="spinner spinner-sm" /></span>
        </div>

        <MrFileComments />
      </div>
    </div>
  </div>
</template>

<style scoped src="@/components/project/shared.css"></style>
<style scoped src="./mergerequest.css"></style>
<style scoped>
.changes {
  display: grid;
  grid-template-columns: minmax(200px, 270px) minmax(0, 1fr);
  gap: var(--space-4);
  align-items: start;
}

@container (max-width: 800px) {
  .changes {
    grid-template-columns: minmax(0, 1fr);
  }
}

.file-view {
  display: flex;
  flex-direction: column;
  gap: var(--space-3);
  min-width: 0;
}

.file-bar {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: var(--space-2) var(--space-3);
  padding: var(--space-2) var(--space-3);
  background: var(--bg-surface);
  border: 0.5px solid var(--border-default);
  border-radius: var(--radius-lg);
}

.file-status-pill {
  padding: 0 6px;
  border-radius: var(--radius-sm);
  background: var(--bg-neutral);
  font-size: 0.68rem;
  font-weight: 500;
  text-transform: capitalize;
}

.file-bar-path {
  flex: 1;
  min-width: 0;
  font-family: var(--font-mono);
  font-size: 0.78rem;
  overflow-wrap: anywhere;
}

.file-bar-path strong {
  font-weight: 500;
  color: var(--text-primary);
}

.file-bar-stats {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  font-family: var(--font-mono);
  font-size: 0.72rem;
}

.file-bar-end {
  display: flex;
  align-items: center;
  gap: var(--space-2);
}

.pager {
  display: inline-flex;
  align-items: center;
  gap: 2px;
}

.pager-count {
  min-width: 44px;
  text-align: center;
  font-size: 0.72rem;
  color: var(--text-muted);
  font-variant-numeric: tabular-nums;
}

.branch-note {
  margin: 0;
}

.diff-frame {
  position: relative;
  height: 65vh;
  min-height: 360px;
  border: 0.5px solid var(--border-default);
  border-radius: var(--radius-lg);
  overflow: hidden;
  transition: opacity var(--transition-fast);
}

.diff-frame.loading {
  opacity: 0.6;
}

.diff-loading {
  position: absolute;
  top: var(--space-3);
  right: var(--space-3);
}
</style>
