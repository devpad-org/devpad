<script setup lang="ts">
import MarkdownMessage from '@/components/ide/MarkdownMessage.vue'
import MrIcon from '@/components/project/MrIcon.vue'
import { fullTime, timeAgo } from '@/utils/time'
import MrActivity from './MrActivity.vue'
import MrMergeCard from './MrMergeCard.vue'
import MrSidebar from './MrSidebar.vue'
import { useMergeRequest } from './useMergeRequest'

// The Overview section: description, merge widget and activity, with the
// details sidebar beside them.
const { mr } = useMergeRequest()
</script>

<template>
  <div v-if="mr" class="overview">
    <div class="main-col">
      <article class="card">
        <div class="card-header">
          <span class="avatar avatar-user"><MrIcon name="user" :size="13" /></span>
          <span class="card-header-text">
            <strong>Description</strong>
            <span class="muted" :title="fullTime(mr.createdAt)">&middot; {{ timeAgo(mr.createdAt) }}</span>
          </span>
        </div>
        <div class="card-body description">
          <MarkdownMessage v-if="mr.description" :content="mr.description" />
          <p v-else class="muted">No description provided.</p>
        </div>
      </article>

      <MrMergeCard />
      <MrActivity />
    </div>

    <MrSidebar />
  </div>
</template>

<style scoped src="@/components/project/shared.css"></style>
<style scoped src="./mergerequest.css"></style>
<style scoped>
.overview {
  display: grid;
  grid-template-columns: minmax(0, 1fr) 280px;
  gap: var(--space-5);
  align-items: start;
}

/* The tab, not the window, decides the layout: the IDE's side panels take
   part of the width. */
@container (max-width: 900px) {
  .overview {
    grid-template-columns: minmax(0, 1fr);
  }
}

.main-col {
  display: flex;
  flex-direction: column;
  gap: var(--space-4);
  min-width: 0;
}

.description {
  max-height: 50vh;
  overflow-y: auto;
}
</style>
