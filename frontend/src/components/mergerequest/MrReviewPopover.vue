<script setup lang="ts">
import { onMounted, onUnmounted, ref } from 'vue'
import MrIcon from '@/components/project/MrIcon.vue'
import { VERDICTS, baseName, plural } from './format'
import { useMergeRequest } from './useMergeRequest'

// The Review button and the popover where the user finishes a review: a
// summary, a verdict and the line comments gathered on the Changes tab.
const { mr, busy, reviewOpen, reviewBody, verdict, drafts, sendToAgent, reviewError, jumpToComment, submitReview } = useMergeRequest()

const box = ref<HTMLElement | null>(null)

function onDocumentPointer(e: PointerEvent) {
  if (reviewOpen.value && box.value && !box.value.contains(e.target as Node)) reviewOpen.value = false
}

function onKeydown(e: KeyboardEvent) {
  if (e.key === 'Escape') reviewOpen.value = false
}

onMounted(() => {
  document.addEventListener('pointerdown', onDocumentPointer)
  document.addEventListener('keydown', onKeydown)
})

onUnmounted(() => {
  document.removeEventListener('pointerdown', onDocumentPointer)
  document.removeEventListener('keydown', onKeydown)
})
</script>

<template>
  <div ref="box" class="review-anchor">
    <button class="btn btn-primary btn-sm" :aria-expanded="reviewOpen" @click="reviewOpen = !reviewOpen">
      Review
      <span v-if="drafts.length" class="draft-count">{{ drafts.length }}</span>
      <MrIcon name="chevron-down" :size="13" />
    </button>
    <Transition name="pop">
      <div v-if="reviewOpen" class="review-pop" role="dialog" aria-label="Finish your review">
        <div class="pop-header">
          <span class="pop-title">Finish your review</span>
          <button class="icon-button" aria-label="Close" @click="reviewOpen = false">
            <MrIcon name="close" :size="14" />
          </button>
        </div>
        <textarea
          v-model="reviewBody"
          class="form-input form-textarea"
          rows="4"
          placeholder="Leave a summary (Markdown)"
          aria-label="Review summary"
        />
        <div class="verdicts" role="radiogroup" aria-label="Verdict">
          <label v-for="v in VERDICTS" :key="v.value" class="verdict" :class="[`tone-${v.tone}`, { active: verdict === v.value }]">
            <input v-model="verdict" type="radio" :name="`verdict-${mr?.id}`" :value="v.value" />
            <MrIcon :name="v.icon" :size="15" class="verdict-icon" />
            <span class="verdict-text">
              <span class="verdict-label">{{ v.label }}</span>
              <span class="verdict-hint">{{ v.hint }}</span>
            </span>
          </label>
        </div>
        <div v-if="drafts.length" class="pop-drafts">
          <span class="form-hint">{{ plural(drafts.length, 'line comment') }} will be included</span>
          <button
            v-for="(d, i) in drafts"
            :key="i"
            class="location-chip"
            @click="reviewOpen = false; jumpToComment(d.path, d.line)"
          >
            {{ baseName(d.path) }}:{{ d.line }}
          </button>
        </div>
        <p v-else class="form-hint">Tip: click a line number in the Changes tab to comment on a line.</p>
        <label v-if="verdict === 'request_changes' && mr?.taskId" class="form-check">
          <input v-model="sendToAgent" type="checkbox" />
          Send the feedback to the task's agent now
        </label>
        <p v-if="reviewError" class="form-error">{{ reviewError }}</p>
        <div class="pop-actions">
          <button class="btn btn-secondary btn-sm" @click="reviewOpen = false">Cancel</button>
          <button class="btn btn-primary btn-sm" :disabled="busy" @click="submitReview">Submit review</button>
        </div>
      </div>
    </Transition>
  </div>
</template>

<style scoped src="@/components/project/shared.css"></style>
<style scoped src="./mergerequest.css"></style>
<style scoped>
.review-anchor {
  position: relative;
}

.draft-count {
  min-width: 16px;
  padding: 0 5px;
  border-radius: 999px;
  background: var(--bg-primary);
  color: var(--accent-blue);
  font-size: 0.68rem;
  font-weight: 600;
}

.review-pop {
  position: absolute;
  top: calc(100% + var(--space-2));
  right: 0;
  z-index: 50;
  display: flex;
  flex-direction: column;
  gap: var(--space-3);
  width: 420px;
  /* The tab is a size container, so the popover never outgrows it. */
  max-width: calc(100cqw - 2 * var(--space-4));
  padding: var(--space-4);
  background: var(--bg-elevated);
  border: 0.5px solid var(--border-strong);
  border-radius: var(--radius-lg);
  box-shadow: var(--shadow-strong);
}

.pop-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
}

.pop-title {
  font-size: 0.9rem;
  font-weight: 500;
}

.verdicts {
  display: flex;
  flex-direction: column;
  gap: var(--space-1);
}

.verdict {
  display: flex;
  align-items: flex-start;
  gap: var(--space-2);
  padding: var(--space-2) var(--space-3);
  border: 0.5px solid var(--border-default);
  border-radius: var(--radius-md);
  cursor: pointer;
  transition: all var(--transition-fast);
}

.verdict input {
  position: absolute;
  opacity: 0;
  pointer-events: none;
}

.verdict:hover {
  background: var(--bg-hover);
}

.verdict:has(input:focus-visible) {
  box-shadow: 0 0 0 3px var(--accent-glow), 0 0 0 1px var(--accent-border);
}

.verdict-icon {
  margin-top: 2px;
  color: var(--text-muted);
}

.verdict-text {
  display: flex;
  flex-direction: column;
}

.verdict-label {
  font-size: 0.8rem;
  font-weight: 500;
}

.verdict-hint {
  font-size: 0.72rem;
  color: var(--text-muted);
}

.verdict.active.tone-review {
  border-color: color-mix(in srgb, var(--accent-purple) 45%, transparent);
  background: color-mix(in srgb, var(--accent-purple) 9%, transparent);
}

.verdict.active.tone-success {
  border-color: var(--success-border);
  background: var(--success-bg);
}

.verdict.active.tone-attention {
  border-color: var(--attention-border);
  background: var(--attention-bg);
}

.verdict.active.tone-review .verdict-icon {
  color: var(--accent-purple);
}

.verdict.active.tone-success .verdict-icon {
  color: var(--success);
}

.verdict.active.tone-attention .verdict-icon {
  color: var(--attention);
}

.pop-drafts {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: var(--space-1);
}

.pop-drafts .form-hint {
  margin: 0 var(--space-1) 0 0;
}

.location-chip {
  padding: 1px 6px;
  border-radius: var(--radius-sm);
  background: var(--bg-raised);
  border: 0.5px solid var(--border-default);
  color: var(--accent-blue);
  font-family: var(--font-mono);
  font-size: 0.7rem;
}

.location-chip:hover {
  border-color: var(--accent-blue);
}

.pop-actions {
  display: flex;
  justify-content: flex-end;
  gap: var(--space-2);
}

.review-pop .form-error {
  margin: 0;
}
</style>
