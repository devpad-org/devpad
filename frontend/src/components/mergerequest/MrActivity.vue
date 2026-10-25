<script setup lang="ts">
import MarkdownMessage from '@/components/ide/MarkdownMessage.vue'
import MrIcon from '@/components/project/MrIcon.vue'
import { fullTime, timeAgo } from '@/utils/time'
import { authorName, reviewAction, reviewIcon, reviewTone, shortSha } from './format'
import { useMergeRequest } from './useMergeRequest'

// The merge request's timeline: opened, each review with its line comments,
// and how it ended.
const { mr, isOpen, timelineReviews, statusIcon, reviewComments, jumpToComment } = useMergeRequest()
</script>

<template>
  <section v-if="mr" class="activity">
    <h2 class="section-title activity-title">Activity</h2>
    <ol class="timeline">
      <li class="event event-small">
        <span class="event-node tone-active"><MrIcon name="open" :size="13" /></span>
        <span class="event-line">
          Opened from <span class="mono">{{ mr.sourceBranch }}</span>
          <template v-if="mr.taskId"> by task #{{ mr.taskId }}</template>
          <span class="muted" :title="fullTime(mr.createdAt)"> &middot; {{ timeAgo(mr.createdAt) }}</span>
        </span>
      </li>

      <li v-for="review in timelineReviews" :key="review.id" class="event">
        <span class="event-node" :class="`tone-${reviewTone(review)}`">
          <MrIcon :name="reviewIcon(review)" :size="13" />
        </span>
        <article class="card review-card" :class="`edge-${reviewTone(review)}`">
          <div class="card-header">
            <span class="avatar" :class="`avatar-${review.author}`">
              <MrIcon :name="review.author === 'agent' ? 'bot' : 'user'" :size="13" />
            </span>
            <span class="card-header-text">
              <strong>{{ authorName(review.author) }}</strong>
              <span>{{ reviewAction(review) }}</span>
              <span class="muted" :title="fullTime(review.createdAt)">&middot; {{ timeAgo(review.createdAt) }}</span>
            </span>
            <span class="card-header-end">
              <span v-if="review.model" class="tag">{{ review.model }}</span>
              <span v-if="review.headSha && review.headSha !== mr.headSha" class="tag tag-outdated" :title="`Reviewed ${shortSha(review.headSha)}`">outdated</span>
              <span v-if="review.status === 'pending'" class="spinner spinner-sm" />
            </span>
          </div>
          <div v-if="review.error || review.body || reviewComments(review).length || (review.workspaceId && review.status === 'pending')" class="card-body">
            <p v-if="review.error" class="notice notice-error">{{ review.error }}</p>
            <MarkdownMessage v-if="review.body" :content="review.body" />
            <div v-if="reviewComments(review).length" class="thread-list">
              <div v-for="c in reviewComments(review)" :key="c.id" class="thread">
                <button class="thread-location" :title="`Open ${c.path} at line ${c.line}`" @click="jumpToComment(c.path, c.line)">
                  <MrIcon name="file" :size="12" />
                  <span class="mono">{{ c.path }}</span>
                  <span class="line-chip">L{{ c.line }}</span>
                  <MrIcon name="chevron-right" :size="12" class="thread-go" />
                </button>
                <div class="thread-body"><MarkdownMessage :content="c.body" /></div>
              </div>
            </div>
            <RouterLink
              v-if="review.workspaceId && review.status === 'pending'"
              class="btn btn-secondary btn-sm watch-link"
              :to="{ name: 'ide', params: { id: review.workspaceId } }"
            >
              <MrIcon name="terminal" :size="13" />
              Watch in sandbox
            </RouterLink>
          </div>
        </article>
      </li>

      <li v-if="mr.status !== 'open'" class="event event-small">
        <span class="event-node" :class="mr.status === 'merged' ? 'tone-success' : 'tone-neutral'">
          <MrIcon :name="statusIcon" :size="13" />
        </span>
        <span class="event-line">
          <template v-if="mr.status === 'merged'">
            Merged into <span class="mono">{{ mr.targetBranch }}</span>
            <span class="muted" :title="fullTime(mr.mergedAt)"> &middot; {{ timeAgo(mr.mergedAt) }}</span>
          </template>
          <template v-else>
            Closed
            <span class="muted" :title="fullTime(mr.updatedAt)"> &middot; {{ timeAgo(mr.updatedAt) }}</span>
          </template>
        </span>
      </li>
    </ol>
    <p v-if="timelineReviews.length === 0 && isOpen" class="empty-text timeline-empty">
      No reviews yet. Use <strong>Review</strong> above, or start an agent review from the sidebar.
    </p>
  </section>
</template>

<style scoped src="@/components/project/shared.css"></style>
<style scoped src="./mergerequest.css"></style>
<style scoped>
.activity-title {
  margin: var(--space-2) 0 var(--space-3);
}

.timeline {
  list-style: none;
  position: relative;
  display: flex;
  flex-direction: column;
  gap: var(--space-4);
}

.timeline::before {
  content: '';
  position: absolute;
  top: 12px;
  bottom: 12px;
  left: 13px;
  width: 1px;
  background: var(--border-default);
}

.event {
  position: relative;
  display: grid;
  grid-template-columns: 27px minmax(0, 1fr);
  gap: var(--space-3);
  align-items: start;
}

.event-node {
  position: relative;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 27px;
  height: 27px;
  border-radius: 50%;
  color: var(--text-secondary);
  background: var(--bg-neutral);
  box-shadow: 0 0 0 4px var(--ide-surface-bg);
}

.event-node::after {
  content: '';
  position: absolute;
  inset: 0;
  border-radius: 50%;
  background: var(--ide-surface-bg);
  z-index: -1;
}

.event-small {
  align-items: center;
}

.event-line {
  font-size: 0.8rem;
  color: var(--text-secondary);
}

.review-card {
  border-left-width: 2px;
}

.edge-success {
  border-left-color: var(--success);
}

.edge-attention {
  border-left-color: var(--attention);
}

.edge-review {
  border-left-color: var(--accent-purple);
}

.edge-active {
  border-left-color: var(--accent-blue);
}

.edge-error {
  border-left-color: var(--error);
}

.thread-list {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
  margin-top: var(--space-3);
}

.thread-list:first-child {
  margin-top: 0;
}

.thread {
  border: 0.5px solid var(--border-default);
  border-radius: var(--radius-md);
  overflow: hidden;
}

.thread-location {
  display: flex;
  align-items: center;
  gap: 6px;
  width: 100%;
  padding: 5px var(--space-3);
  background: var(--bg-raised);
  color: var(--text-secondary);
  text-align: left;
  transition: all var(--transition-fast);
}

.thread-location .mono {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.thread-location:hover {
  color: var(--text-primary);
  background: color-mix(in srgb, var(--accent-blue) 10%, var(--bg-raised));
}

.thread-go {
  margin-left: auto;
  opacity: 0;
  transition: opacity var(--transition-fast);
}

.thread-location:hover .thread-go {
  opacity: 1;
}

.thread-body {
  padding: var(--space-2) var(--space-3);
  font-size: 0.82rem;
}

.watch-link {
  margin-top: var(--space-3);
}

.timeline-empty {
  padding-left: calc(27px + var(--space-3));
}
</style>
