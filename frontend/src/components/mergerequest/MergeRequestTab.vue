<script setup lang="ts">
import { onUnmounted, provide, toRef, watch } from 'vue'
import MrIcon from '@/components/project/MrIcon.vue'
import MrChanges from './MrChanges.vue'
import MrHeader from './MrHeader.vue'
import MrOverview from './MrOverview.vue'
import MrPublishBanner from './MrPublishBanner.vue'
import MrReviewPopover from './MrReviewPopover.vue'
import { statBlocks } from './format'
import { createMergeRequestState, mergeRequestKey } from './useMergeRequest'

const POLL_INTERVAL_MS = 5000

// A merge request as an editor tab in the project shell. It stays mounted
// while other tabs show, keeping review drafts, and polls only while showing.
const props = defineProps<{
  projectId: number
  mrId: number
  // The tab is the one showing.
  active: boolean
}>()

// Whether the tab holds an unsubmitted review that closing it would lose.
const emit = defineEmits<{ dirty: [dirty: boolean] }>()

const state = createMergeRequestState(toRef(props, 'projectId'), toRef(props, 'mrId'))
provide(mergeRequestKey, state)

const { mr, loadError, actionError, notice, section, files, totals, isOpen, hasDraft, refresh } = state

let timer: ReturnType<typeof setInterval> | null = null

function startPolling() {
  stopPolling()
  timer = setInterval(() => {
    if (document.visibilityState === 'visible' && isOpen.value) void refresh()
  }, POLL_INTERVAL_MS)
}

function stopPolling() {
  if (timer !== null) {
    clearInterval(timer)
    timer = null
  }
}

// The first load happens even for a tab opened in the background; after
// that only a showing tab polls, and coming back refreshes at once.
watch(() => props.active, (active) => {
  if (active) {
    void refresh()
    startPolling()
  } else {
    stopPolling()
  }
})

void refresh()
if (props.active) startPolling()

watch(hasDraft, (dirty) => emit('dirty', dirty))

onUnmounted(() => {
  stopPolling()
  state.dispose()
})
</script>

<template>
  <div class="mr-tab">
    <div class="mr-inner">
      <p v-if="!mr && loadError" class="notice notice-error">{{ loadError }}</p>
      <div v-else-if="!mr" class="loading-state">
        <span class="spinner" />
        Loading merge request...
      </div>

      <template v-else>
        <MrHeader />
        <MrPublishBanner />

        <p v-if="loadError" class="notice notice-error">{{ loadError }}</p>
        <p v-if="actionError" class="notice notice-error">{{ actionError }}</p>
        <Transition name="fade">
          <p v-if="notice" class="notice notice-success notice-row">
            <MrIcon name="check" :size="14" />
            {{ notice }}
          </p>
        </Transition>

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

        <MrOverview v-if="section === 'overview'" />
        <MrChanges v-else :active="active" />
      </template>
    </div>
  </div>
</template>

<style scoped src="@/components/project/shared.css"></style>
<style scoped src="./mergerequest.css"></style>
<style scoped>
/* The tab scrolls on its own and is the size container its sections lay
   themselves out against. */
.mr-tab {
  height: 100%;
  overflow-y: auto;
  container-type: inline-size;
}

.mr-inner {
  max-width: 1200px;
  margin: 0 auto;
  padding: var(--space-4);
}

.loading-state {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  color: var(--text-muted);
  font-size: 0.8rem;
  padding: var(--space-6) 0;
}

.tabbar {
  display: flex;
  align-items: flex-end;
  justify-content: space-between;
  gap: var(--space-3);
  border-bottom: 0.5px solid var(--border-default);
  margin-bottom: var(--space-5);
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
  padding-bottom: var(--space-2);
}

.diffstat {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  font-family: var(--font-mono);
  font-size: 0.75rem;
}

@container (max-width: 640px) {
  .tabbar {
    flex-wrap: wrap;
  }

  .diffstat {
    display: none;
  }
}
</style>
