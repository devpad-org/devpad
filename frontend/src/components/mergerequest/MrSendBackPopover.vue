<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref, watch } from 'vue'
import MrIcon from '@/components/project/MrIcon.vue'
import { plural } from './format'
import { useMergeRequest } from './useMergeRequest'

// The Send back button and the popover that starts the task's agent again
// on the feedback for the current commit, with a note from the user.
const { busy, sendBackOpen, sendBackNote, sendBackError, sendBackReviews, reviewComments, sendBack } = useMergeRequest()

const box = ref<HTMLElement | null>(null)
const input = ref<HTMLTextAreaElement | null>(null)

const lineComments = computed(() => sendBackReviews.value.reduce((n, r) => n + reviewComments(r).length, 0))

watch(sendBackOpen, (open) => {
  sendBackError.value = ''
  if (open) void nextTick(() => input.value?.focus())
})

function onDocumentPointer(e: PointerEvent) {
  if (sendBackOpen.value && box.value && !box.value.contains(e.target as Node)) sendBackOpen.value = false
}

function onKeydown(e: KeyboardEvent) {
  if (e.key === 'Escape') sendBackOpen.value = false
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
  <div ref="box" class="send-anchor">
    <button class="btn btn-secondary" :aria-expanded="sendBackOpen" @click="sendBackOpen = !sendBackOpen">
      <MrIcon name="send" :size="14" />
      Send back to agent
    </button>
    <Transition name="pop">
      <div v-if="sendBackOpen" class="send-pop" role="dialog" aria-label="Send back to the task's agent">
        <div class="pop-header">
          <span class="pop-title">Send back to the task's agent</span>
          <button class="icon-button" aria-label="Close" @click="sendBackOpen = false">
            <MrIcon name="close" :size="14" />
          </button>
        </div>
        <p v-if="sendBackReviews.length" class="form-hint pop-hint">
          The agent gets the {{ plural(sendBackReviews.length, 'review') }} of this commit<template v-if="lineComments">
            with {{ plural(lineComments, 'line comment') }}</template>, and can read and reply to every comment.
        </p>
        <p v-else class="form-hint pop-hint">
          Nothing on this commit has been reviewed yet, so tell the agent what to change. It can read and reply to every earlier comment.
        </p>
        <textarea
          ref="input"
          v-model="sendBackNote"
          class="form-input form-textarea"
          rows="4"
          :placeholder="sendBackReviews.length ? 'Anything to add? (optional, Markdown)' : 'What should the agent change? (Markdown)'"
          aria-label="Note for the agent"
          @keydown.meta.enter.prevent="sendBack"
          @keydown.ctrl.enter.prevent="sendBack"
        />
        <p class="form-hint pop-hint">Your note stays on the merge request as a comment.</p>
        <p v-if="sendBackError" class="form-error">{{ sendBackError }}</p>
        <div class="pop-actions">
          <button class="btn btn-secondary btn-sm" @click="sendBackOpen = false">Cancel</button>
          <button class="btn btn-primary btn-sm" :disabled="busy" @click="sendBack">
            <MrIcon name="send" :size="13" />
            Send back
          </button>
        </div>
      </div>
    </Transition>
  </div>
</template>

<style scoped src="@/components/project/shared.css"></style>
<style scoped src="./mergerequest.css"></style>
<style scoped>
.send-anchor {
  position: relative;
}

.send-pop {
  position: absolute;
  top: calc(100% + var(--space-2));
  right: 0;
  z-index: 50;
  display: flex;
  flex-direction: column;
  gap: var(--space-3);
  width: 400px;
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

.pop-hint {
  margin: 0;
}

.pop-actions {
  display: flex;
  justify-content: flex-end;
  gap: var(--space-2);
}

.send-pop .form-error {
  margin: 0;
}

/* Matches MOBILE_MEDIA_QUERY in useMediaQuery.ts: the popover spans the tab. */
@media (max-width: 768px), (pointer: coarse) and (max-height: 500px) {
  .send-pop {
    width: calc(100cqw - 2 * var(--space-4));
  }
}
</style>
