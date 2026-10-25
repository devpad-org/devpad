<script setup lang="ts">
import { nextTick, ref, watch } from 'vue'
import MarkdownMessage from '@/components/ide/MarkdownMessage.vue'
import MrIcon from '@/components/project/MrIcon.vue'
import { fullTime, timeAgo } from '@/utils/time'
import { authorIcon, authorName } from './format'
import { useMergeRequest } from './useMergeRequest'

// The selected file's comments, the user's pending line comments, and the
// form that adds one to the review.
const {
  mr,
  isOpen,
  selectedComments,
  selectedDrafts,
  draftLine,
  draftBody,
  draftError,
  draftFocus,
  selectFile,
  addDraft,
  removeDraft,
} = useMergeRequest()

const draftInput = ref<HTMLTextAreaElement | null>(null)

// Clicking a line number in the diff starts a comment here.
watch(draftFocus, () => void nextTick(() => draftInput.value?.focus()))
</script>

<template>
  <section v-if="mr" class="file-comments-section">
    <div class="file-comments-header">
      <span class="section-title">
        Comments on this file
        <span class="muted">{{ selectedComments.length + selectedDrafts.length }}</span>
      </span>
      <span v-if="isOpen" class="form-hint">Click a line number in the new version to comment on it.</span>
    </div>

    <div v-for="c in selectedComments" :key="c.id" class="comment" :class="{ outdated: c.headSha !== mr.headSha, reply: c.replyTo }">
      <div class="comment-meta">
        <span class="avatar avatar-sm" :class="`avatar-${c.author}`">
          <MrIcon :name="authorIcon(c.author)" :size="11" />
        </span>
        <span class="comment-author">{{ authorName(c.author) }}</span>
        <span v-if="c.replyTo" class="muted">replied</span>
        <button class="line-chip line-chip-button" :title="`Show line ${c.line}`" @click="selectFile(c.path, c.line)">L{{ c.line }}</button>
        <span v-if="c.headSha !== mr.headSha" class="tag tag-outdated">outdated</span>
        <span class="muted comment-time" :title="fullTime(c.createdAt)">{{ timeAgo(c.createdAt) }}</span>
      </div>
      <MarkdownMessage :content="c.body" />
    </div>

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
        <span v-else class="form-hint">Comments are held until you submit your review.</span>
        <button type="submit" class="btn btn-secondary btn-sm">Add to review</button>
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

.comment.outdated {
  opacity: 0.75;
}

/* A reply sits indented under the comment it answers. */
.comment.reply {
  margin-left: var(--space-5);
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

.comment-time,
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
  gap: var(--space-2);
}

.draft-form-footer .form-hint,
.draft-error {
  margin: 0;
}
</style>
