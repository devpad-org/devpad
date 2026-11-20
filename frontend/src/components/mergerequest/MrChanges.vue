<script setup lang="ts">
import { computed, inject, onMounted, ref, watch } from 'vue'
import { gitApi } from '@/api/git'
import MonacoDiffViewer from '@/components/ide/MonacoDiffViewer.vue'
import MrIcon from '@/components/project/MrIcon.vue'
import { ideShellKey } from '@/components/ide/ideShell'
import { useIsMobile } from '@/composables/useMediaQuery'
import MrFileComments from './MrFileComments.vue'
import MrFileTree from './MrFileTree.vue'
import { baseName, dirName, fileStatusLabel, statBlocks } from './format'
import { useMergeRequest } from './useMergeRequest'

// The Changes section: the file tree, the selected file's diff filling the
// modal's height, and the file's comments beside it, or under it when the
// modal is narrower.

const {
  mr,
  isOpen,
  files,
  orderedFiles,
  selectedPath,
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

// On a phone the file list folds away behind a toggle so the diff gets the
// screen, and choosing a file folds it again.
const isMobile = useIsMobile()
const treeOpen = ref(false)
watch(selectedPath, () => {
  treeOpen.value = false
})

// The branch and commit the project workspace has checked out, so "Open in
// editor" can say when the editor shows a different version than this diff.
const workspaceBranch = ref('')
const workspaceHash = ref('')
const workspaceId = computed(() => shell?.liveWorkspaceId.value ?? null)
const canOpenInEditor = computed(() => workspaceId.value !== null && !!selectedFile.value && selectedFile.value.status !== 'D')
const otherBranch = computed(() =>
  canOpenInEditor.value && workspaceBranch.value && mr.value && workspaceBranch.value !== mr.value.sourceBranch
    ? workspaceBranch.value
    : '',
)
// On the right branch, but at a different commit than the merge request's
// head, e.g. after an agent pushed a fix. The branch list gives short hashes.
const otherCommit = computed(() => {
  const head = mr.value?.headSha ?? ''
  const hash = workspaceHash.value
  if (!canOpenInEditor.value || otherBranch.value || !workspaceBranch.value || !hash || !head) return ''
  return head.startsWith(hash) ? '' : hash
})

async function loadWorkspaceBranch() {
  const id = workspaceId.value
  if (id === null) {
    workspaceBranch.value = ''
    workspaceHash.value = ''
    return
  }
  try {
    const res = await gitApi.branches(id)
    if (workspaceId.value !== id) return
    workspaceBranch.value = res.current
    workspaceHash.value = res.branches.find((b) => !b.remote && b.name === res.current)?.hash ?? ''
  } catch {
    workspaceBranch.value = ''
    workspaceHash.value = ''
  }
}

watch(() => [workspaceId.value, mr.value?.headSha], () => void loadWorkspaceBranch(), { immediate: true })

// The shell closes the modal, asking first about a review draft, and shows
// the file in the editor.
function openInEditor() {
  const file = selectedFile.value
  if (!file || !shell) return
  shell.openFile(`/workspace/${file.path}`)
}

// Side by side needs room: the diff gets the modal's width less the file
// tree, and less the comments column too on the widest screens.
const SIDE_BY_SIDE_MIN_WIDTH = 1100

onMounted(() => {
  if (sideBySide.value === null) sideBySide.value = (root.value?.clientWidth ?? 0) >= SIDE_BY_SIDE_MIN_WIDTH
})
</script>

<template>
  <div ref="root" class="changes-tab">
    <div v-if="files.length === 0" class="empty-card">
      <MrIcon name="file" :size="28" :stroke-width="1.5" />
      <p>This branch changes nothing.</p>
    </div>
    <div v-else class="changes" :class="{ 'changes--phone': isMobile }">
      <button
        v-if="isMobile"
        type="button"
        class="tree-toggle"
        :aria-expanded="treeOpen"
        @click="treeOpen = !treeOpen"
      >
        <MrIcon name="folder" :size="14" />
        <span class="tree-toggle-label">{{ files.length }} {{ files.length === 1 ? 'file' : 'files' }} changed</span>
        <MrIcon name="chevron-down" :size="14" class="tree-toggle-chevron" :class="{ open: treeOpen }" />
      </button>
      <MrFileTree v-if="!isMobile || treeOpen" />

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
        <p v-else-if="otherCommit" class="notice notice-warning branch-note">
          Your workspace's <span class="mono">{{ workspaceBranch }}</span> is at
          <span class="mono">{{ otherCommit }}</span>, but this merge request is at
          <span class="mono">{{ mr?.headSha.slice(0, otherCommit.length) }}</span>, so the editor may show a different version
          of this file.
        </p>

        <div class="file-main">
          <p v-if="fileError" class="notice notice-error file-error">{{ fileError }}</p>
          <div v-else-if="selectedFile.binary" class="empty-card">
            <MrIcon name="file" :size="28" :stroke-width="1.5" />
            <p>Binary file; no preview.</p>
          </div>
          <div v-else class="diff-frame" :class="{ loading: fileLoading }">
            <MonacoDiffViewer
              :old-content="oldContent"
              :new-content="newContent"
              :old-file-name="selectedFile.oldPath || selectedFile.path"
              :new-file-name="selectedFile.path"
              :side-by-side="!isMobile && (sideBySide ?? true)"
              :marked-lines="markedLines"
              :reveal="reveal"
              :commentable="isOpen"
              @line-click="startComment"
            />
            <span v-if="fileLoading" class="diff-loading"><span class="spinner spinner-sm" /></span>
          </div>

          <div class="comments-pane">
            <MrFileComments />
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped src="@/components/project/shared.css"></style>
<style scoped src="@/assets/styles/segmented.css"></style>
<style scoped src="./mergerequest.css"></style>
<style scoped>
.changes-tab {
  display: flex;
  flex-direction: column;
  flex: 1;
  min-height: 0;
}

/* The tree and the file share the modal's height; each scrolls on its own. */
.changes {
  display: grid;
  grid-template-columns: 260px minmax(0, 1fr);
  grid-template-rows: minmax(0, 1fr);
  flex: 1;
  min-height: 0;
}

.empty-card {
  flex: 1;
  justify-content: center;
}

.file-view {
  display: flex;
  flex-direction: column;
  min-width: 0;
  min-height: 0;
}

.file-bar {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: var(--space-2) var(--space-3);
  padding: var(--space-2) var(--space-4);
  border-bottom: 0.5px solid var(--border-hairline);
  flex-shrink: 0;
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
  margin: var(--space-2) var(--space-4) 0;
  flex-shrink: 0;
}

/* The diff fills the rest of the height, with the file's comments in a
   column of their own beside it. */
.file-main {
  display: grid;
  grid-template-columns: minmax(0, 1fr) 320px;
  grid-template-rows: minmax(0, 1fr);
  flex: 1;
  min-height: 0;
}

.file-error {
  margin: var(--space-4);
}

.diff-frame {
  position: relative;
  min-height: 0;
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

.comments-pane {
  min-height: 0;
  overflow-y: auto;
  padding: var(--space-3) var(--space-4);
  border-left: 0.5px solid var(--border-hairline);
}

/* Narrower: the comments go under the diff, and the file scrolls. */
@container (max-width: 1399px) {
  .file-view {
    overflow-y: auto;
  }

  .file-main {
    display: flex;
    flex-direction: column;
    flex: none;
  }

  .diff-frame {
    flex: none;
    height: 70dvh;
    min-height: 360px;
  }

  .comments-pane {
    overflow: visible;
    border-left: none;
    border-top: 0.5px solid var(--border-hairline);
  }
}

/* Narrower still: the tree goes above the file, and the modal's body
   scrolls as one. */
@container (max-width: 800px) {
  .changes-tab,
  .changes {
    flex: none;
  }

  .changes {
    grid-template-columns: minmax(0, 1fr);
    grid-template-rows: none;
  }

  .file-view {
    overflow: visible;
  }
}

/* A phone folds the file list behind a toggle and shows the unified diff
   only; side by side has no room. The path takes its own line, with the
   status, stats and pager under it. */
.tree-toggle {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  min-height: 44px;
  padding: 0 var(--space-4);
  border-bottom: 0.5px solid var(--border-hairline);
  color: var(--text-secondary);
  font-size: 0.85rem;
  text-align: left;
}

.tree-toggle-label {
  flex: 1;
}

.tree-toggle-chevron {
  transition: transform var(--transition-fast);
}

.tree-toggle-chevron.open {
  transform: rotate(180deg);
}

/* A phone's sheet may be wide when held sideways; it still stacks. */
.changes--phone {
  grid-template-columns: minmax(0, 1fr);
  grid-template-rows: none;
}

.changes-tab:has(.changes--phone),
.changes--phone,
.changes--phone .file-main {
  flex: none;
}

.changes--phone .file-view {
  overflow: visible;
}

.changes--phone .file-main {
  display: flex;
  flex-direction: column;
}

.changes--phone .comments-pane {
  overflow: visible;
  border-left: none;
  border-top: 0.5px solid var(--border-hairline);
}

.changes--phone .segmented-icons {
  display: none;
}

.changes--phone .file-bar-path {
  flex-basis: 100%;
  order: -1;
}

.changes--phone .file-bar-end {
  margin-left: auto;
}

.changes--phone .diff-frame {
  height: 60dvh;
  min-height: 300px;
}
</style>
