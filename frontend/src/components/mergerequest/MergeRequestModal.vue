<script setup lang="ts">
import { onMounted, onUnmounted, provide, ref, toRef, watch } from 'vue'
import MrIcon from '@/components/project/MrIcon.vue'
import { useActiveProjectStore } from '@/stores/activeProject'
import MrChanges from './MrChanges.vue'
import MrHeader from './MrHeader.vue'
import MrOverview from './MrOverview.vue'
import MrPublishBanner from './MrPublishBanner.vue'
import MrReviewPopover from './MrReviewPopover.vue'
import { statBlocks } from './format'
import { createMergeRequestState, mergeRequestKey } from './useMergeRequest'

const POLL_INTERVAL_MS = 5000

// A merge request in a modal over the project, using nearly the whole
// window. The shell teleports it, so that a task opened from it shows on top.
const props = defineProps<{
  projectId: number
  mrId: number
}>()

const emit = defineEmits<{
  // Asks the shell to close it; the shell checks for a review draft first.
  close: []
  // Whether it holds an unsubmitted review that closing it would lose.
  dirty: [dirty: boolean]
}>()

const projectStore = useActiveProjectStore()
const state = createMergeRequestState(toRef(props, 'projectId'), toRef(props, 'mrId'))
provide(mergeRequestKey, state)

const {
  mr,
  loadError,
  actionError,
  notice,
  section,
  files,
  totals,
  isOpen,
  hasDraft,
  unpublishedCommits,
  reviewOpen,
  sendBackOpen,
  refresh,
} = state

let timer: ReturnType<typeof setInterval> | null = null

watch(hasDraft, (dirty) => emit('dirty', dirty))

// Escape closes the innermost thing first: a popover, then the modal. A
// task modal over this one, or the diff editor, takes its own Escape.
function onKeydown(e: KeyboardEvent) {
  if (e.key !== 'Escape' || e.defaultPrevented || projectStore.selectedTask) return
  e.preventDefault()
  if (reviewOpen.value) reviewOpen.value = false
  else if (sendBackOpen.value) sendBackOpen.value = false
  else emit('close')
}

// Only a press that starts on the backdrop closes the modal, so selecting
// text in a diff and letting go outside it doesn't.
const pressedBackdrop = ref(false)

function onBackdropClick() {
  if (pressedBackdrop.value) emit('close')
  pressedBackdrop.value = false
}

onMounted(() => {
  document.addEventListener('keydown', onKeydown)
  void refresh()
  timer = setInterval(() => {
    if (document.visibilityState === 'visible' && isOpen.value) void refresh()
  }, POLL_INTERVAL_MS)
})

onUnmounted(() => {
  document.removeEventListener('keydown', onKeydown)
  if (timer !== null) clearInterval(timer)
  state.dispose()
})
</script>

<template>
  <div
    class="modal-overlay"
    @pointerdown.self="pressedBackdrop = true"
    @click.self="onBackdropClick"
  >
    <div class="modal mr-modal" role="dialog" aria-modal="true" aria-labelledby="mr-modal-title">
      <template v-if="!mr">
        <header class="modal-header">
          <div class="heading">
            <span class="eyebrow">
              <MrIcon name="open" :size="12" />
              Merge request !{{ mrId }}
            </span>
          </div>
          <button class="modal-close" aria-label="Close" title="Close (Esc)" @click="emit('close')">
            <MrIcon name="close" :size="16" />
          </button>
        </header>
        <div class="modal-body">
          <p v-if="loadError" class="notice notice-error notice-row load-state">
            <MrIcon name="alert" :size="14" />
            {{ loadError }}
          </p>
          <div v-else class="load-state loading-state">
            <span class="spinner" />
            Loading merge request...
          </div>
        </div>
      </template>

      <template v-else>
        <MrHeader @close="emit('close')" />

        <div class="tabbar">
          <div class="tabs" role="tablist">
            <button type="button" role="tab" class="tab" :class="{ active: section === 'overview' }" :aria-selected="section === 'overview'" @click="section = 'overview'">
              <MrIcon name="comment" :size="14" />
              Overview
              <span v-if="mr.reviews.length" class="tab-count">{{ mr.reviews.length }}</span>
            </button>
            <button type="button" role="tab" class="tab" :class="{ active: section === 'changes' }" :aria-selected="section === 'changes'" @click="section = 'changes'">
              <MrIcon name="file" :size="14" />
              Changes
              <span class="tab-count">{{ files.length }}</span>
            </button>
          </div>
          <div class="tabbar-end">
            <span class="diffstat" :title="`${totals.additions} additions, ${totals.deletions} deletions`">
              <span class="add">+{{ totals.additions }}</span>
              <span class="del">&minus;{{ totals.deletions }}</span>
              <span class="stat-blocks">
                <span v-for="(b, i) in statBlocks(totals.additions, totals.deletions)" :key="i" :class="`block-${b}`" />
              </span>
            </span>
            <MrReviewPopover v-if="isOpen" />
          </div>
        </div>

        <div class="modal-body">
          <div v-if="loadError || actionError || notice || unpublishedCommits > 0" class="notices">
            <MrPublishBanner />
            <p v-if="loadError" class="notice notice-error notice-row">
              <MrIcon name="alert" :size="14" />
              {{ loadError }}
            </p>
            <p v-if="actionError" class="notice notice-error notice-row">
              <MrIcon name="alert" :size="14" />
              {{ actionError }}
            </p>
            <Transition name="fade">
              <p v-if="notice" class="notice notice-success notice-row">
                <MrIcon name="check" :size="14" />
                {{ notice }}
              </p>
            </Transition>
          </div>

          <MrOverview v-if="section === 'overview'" />
          <MrChanges v-else />
        </div>
      </template>
    </div>
  </div>
</template>

<style scoped src="@/components/project/shared.css"></style>
<style scoped src="@/components/project/taskModal.css"></style>
<style scoped src="./mergerequest.css"></style>
<style scoped>
/* Nearly the whole window, so the diff and the conversation get room. The
   modal is the size container its sections lay themselves out against. */
.mr-modal {
  max-width: 1680px;
  height: calc(100dvh - 2 * var(--space-4));
  container-type: inline-size;
}

.modal-body {
  display: flex;
  flex-direction: column;
  flex: 1;
  min-height: 0;
  overflow: hidden;
}

.load-state {
  margin: var(--space-5);
}

.loading-state {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  color: var(--text-muted);
  font-size: 0.8rem;
}

.tabbar {
  display: flex;
  align-items: flex-end;
  justify-content: space-between;
  gap: var(--space-3);
  padding: 0 var(--space-5);
  border-bottom: 0.5px solid var(--border-default);
  flex-shrink: 0;
}

.tabs {
  display: flex;
  gap: var(--space-1);
}

.tab {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  padding: var(--space-2) var(--space-3);
  margin-bottom: -0.5px;
  font-size: 0.82rem;
  color: var(--text-muted);
  border-bottom: 2px solid transparent;
  border-radius: var(--radius-md) var(--radius-md) 0 0;
  transition: all var(--transition-fast);
}

.tab:hover {
  color: var(--text-secondary);
  background: var(--bg-hover);
}

.tab.active {
  color: var(--text-primary);
  border-bottom-color: var(--accent-blue);
}

.tab-count {
  min-width: 18px;
  padding: 0 6px;
  border-radius: 999px;
  background: var(--bg-neutral);
  color: var(--text-secondary);
  font-size: 0.7rem;
  text-align: center;
}

.tab.active .tab-count {
  background: color-mix(in srgb, var(--accent-blue) 18%, transparent);
  color: var(--accent-blue);
}

.tabbar-end {
  display: flex;
  align-items: center;
  gap: var(--space-3);
  padding: var(--space-2) 0;
}

.diffstat {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  font-family: var(--font-mono);
  font-size: 0.75rem;
}

/* Notices sit above both sections, flush with the hairline under the tabs. */
.notices {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
  padding: var(--space-3) var(--space-5);
  border-bottom: 0.5px solid var(--border-hairline);
  flex-shrink: 0;
}

.notices .notice {
  margin: 0;
}

/* A narrow window, or a phone's full-screen sheet: the body scrolls as one
   instead of each column on its own. */
@container (max-width: 900px) {
  .modal-body {
    overflow-y: auto;
  }

  .tabbar,
  .notices {
    padding-left: var(--space-4);
    padding-right: var(--space-4);
  }
}

@container (max-width: 640px) {
  .tabbar {
    flex-wrap: wrap;
  }

  .diffstat {
    display: none;
  }
}

/* Matches MOBILE_MEDIA_QUERY in useMediaQuery.ts, where the modal is a
   full-screen sheet. */
@media (max-width: 768px), (pointer: coarse) and (max-height: 500px) {
  .mr-modal {
    height: 100%;
  }

  .modal-body {
    overflow-y: auto;
  }
}
</style>
