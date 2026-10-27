<script setup lang="ts">
import MarkdownMessage from '@/components/ide/MarkdownMessage.vue'
import { fullTime, timeAgo } from '@/utils/time'
import MrActivity from './MrActivity.vue'
import MrMergeCard from './MrMergeCard.vue'
import MrSidebar from './MrSidebar.vue'
import { useMergeRequest } from './useMergeRequest'

// The Overview section: description, merge widget and activity, with the
// details sidebar beside them. Each column scrolls on its own.
const { mr } = useMergeRequest()
</script>

<template>
  <div v-if="mr" class="overview">
    <div class="main-col">
      <section class="field">
        <div class="field-head">
          <h3 class="form-label">Description</h3>
          <span class="field-head-end muted" :title="fullTime(mr.createdAt)">{{ timeAgo(mr.createdAt) }}</span>
        </div>
        <div class="description">
          <MarkdownMessage v-if="mr.description" :content="mr.description" />
          <p v-else class="muted">No description provided.</p>
        </div>
      </section>

      <MrMergeCard />
      <MrActivity />
    </div>

    <MrSidebar />
  </div>
</template>

<style scoped src="@/components/project/shared.css"></style>
<style scoped src="@/components/project/taskModal.css"></style>
<style scoped src="./mergerequest.css"></style>
<style scoped>
.overview {
  display: grid;
  grid-template-columns: minmax(0, 1fr) 300px;
  grid-template-rows: minmax(0, 1fr);
  flex: 1;
  min-height: 0;
}

.main-col {
  overflow-y: auto;
}

.field-head-end {
  font-size: 0.72rem;
}

.description {
  font-size: 0.85rem;
}

.muted {
  color: var(--text-muted);
}

/* The modal, not the window, decides the layout: on a phone it is the
   screen, and the body scrolls as one. */
@container (max-width: 900px) {
  .overview {
    grid-template-columns: minmax(0, 1fr);
    grid-template-rows: none;
    flex: none;
  }

  .main-col {
    overflow: visible;
    padding-left: var(--space-4);
    padding-right: var(--space-4);
  }
}
</style>
