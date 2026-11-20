<script setup lang="ts">
import { nextTick, ref, watch } from 'vue'
import MrIcon from '@/components/project/MrIcon.vue'
import MrThread from './MrThread.vue'
import { useMergeRequest } from './useMergeRequest'

// The selected file's threads, the user's pending line comments, and the
// form that adds a line comment to the review or posts it at once.
const {
  mr,
  isOpen,
  selectedThreads,
  selectedDrafts,
  draftLine,
  draftBody,
  draftError,
  draftFocus,
  selectFile,
  addDraft,
  removeDraft,
  commentNow,
} = useMergeRequest()

const draftInput = ref<HTMLTextAreaElement | null>(null)
const posting = ref(false)

async function postNow() {
  if (posting.value) return
  posting.value = true
  try {
    await commentNow()
  } finally {
    posting.value = false
  }
}

// Clicking a line number in the diff starts a comment here.
watch(draftFocus, () => void nextTick(() => draftInput.value?.focus()))
</script>

<template>
  <section v-if="mr" class="file-comments-section">
    <div class="file-comments-header">
      <span class="section-title">
        Threads on this file
        <span class="muted">{{ selectedThreads.length + selectedDrafts.length }}</span>
      </span>
      <span v-if="isOpen" class="form-hint">Click a line number in the new version to comment on it.</span>
    </div>

    <MrThread v-for="t in selectedThreads" :key="t.root.id" :thread="t" in-file />

    <div v-for="(d, i) in selectedDrafts" :key="`draft-${i}`" class="comment draft">
      <div class="comment-meta">
        <span class="avatar avatar-sm avatar-user"><MrIcon name="user" :size="11" /></span>
        <span class="comment-author">You</span>
        <button class="line-chip line-chip-button" :title="`Show line ${d.line}`" @click="selectFile(d.path, d.line)">L{{ d.line }}</button>
        <span class="tag tag-draft">pending</span>
        <button class="icon-button comment-remove" aria-label="Remove comment" title="Remove" @click="removeDraft(d)">
          <MrIcon name="close" :size="13" />
        </button>
      </div>
      <p class="draft-body">{{ d.body }}</p>
    </div>

    <form v-if="isOpen" class="draft-form" @submit.prevent="addDraft">
      <div class="draft-form-row">
        <label class="draft-line-label">
          Line
          <input
            v-model.number="draftLine"
            class="form-input draft-line"
            type="number"
            min="1"
            placeholder="#"
            aria-label="Line number in the new file"
          />
        </label>
        <textarea
          ref="draftInput"
          v-model="draftBody"
          class="form-input form-textarea draft-text"
          rows="2"
          placeholder="Comment on this line... (Markdown)"
          aria-label="Comment"
          @keydown.meta.enter.prevent="addDraft"
          @keydown.ctrl.enter.prevent="addDraft"
        />
      </div>
      <div class="draft-form-footer">
        <span v-if="draftError" class="form-error draft-error">{{ draftError }}</span>
        <span v-else class="form-hint">A review holds its comments until you submit it.</span>
        <span class="draft-buttons">
          <button type="button" class="btn btn-secondary btn-sm" :disabled="posting" @click="postNow">Comment now</button>
          <button type="submit" class="btn btn-primary btn-sm">Add to review</button>
        </span>
      </div>
    </form>
  </section>
</template>

<style scoped src="@/components/project/shared.css"></style>
<style scoped src="./mergerequest.css"></style>
<style scoped>
.file-comments-section {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
}

.file-comments-header {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  flex-wrap: wrap;
  gap: var(--space-2);
}

.file-comments-header .form-hint {
  margin: 0;
}

.comment {
  padding: var(--space-2) var(--space-3);
  background: var(--bg-surface);
  border: 0.5px solid var(--border-default);
  border-radius: var(--radius-md);
  font-size: 0.82rem;
}

.comment.draft {
  border-style: dashed;
  border-color: color-mix(in srgb, var(--accent-blue) 45%, transparent);
}

.comment-meta {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  font-size: 0.74rem;
  margin-bottom: var(--space-1);
  color: var(--text-secondary);
}

.comment-author {
  font-weight: 500;
  color: var(--text-primary);
}

.comment-remove {
  margin-left: auto;
}

.draft-body {
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}

.draft-form {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
  padding: var(--space-3);
  background: var(--bg-surface);
  border: 0.5px solid var(--border-default);
  border-radius: var(--radius-md);
}

.draft-form-row {
  display: flex;
  align-items: flex-start;
  gap: var(--space-2);
}

.draft-line-label {
  display: flex;
  flex-direction: column;
  gap: 2px;
  font-size: 0.68rem;
  color: var(--text-muted);
}

.draft-line {
  width: 76px;
}

.draft-text {
  flex: 1;
  margin-top: calc(0.68rem * 1.5 + 2px);
}

.draft-form-footer {
  display: flex;
  align-items: center;
  justify-content: space-between;
  flex-wrap: wrap;
  gap: var(--space-2);
}

.draft-form-footer .form-hint,
.draft-error {
  margin: 0;
}

.draft-buttons {
  display: inline-flex;
  gap: var(--space-2);
  flex-shrink: 0;
}
</style>
