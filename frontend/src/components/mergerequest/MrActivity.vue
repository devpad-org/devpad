<script setup lang="ts">
import { computed, ref } from 'vue'
import MarkdownMessage from '@/components/ide/MarkdownMessage.vue'
import MrIcon from '@/components/project/MrIcon.vue'
import { fullTime, timeAgo } from '@/utils/time'
import MrCommits from './MrCommits.vue'
import MrThread from './MrThread.vue'
import { authorIcon, authorName, errorText, plural, pushSummary, reviewAction, reviewIcon, reviewTone, shortSha } from './format'
import { useMergeRequest, type TimelineItem } from './useMergeRequest'

// The merge request's timeline: opened with its first commits, each push of
// more, each review's verdict, every thread, on a line or on the whole
// change, and how it ended. It can show only the threads still open, and
// ends with a box for a new comment.
const {
  mr,
  isOpen,
  timeline,
  openedCommits,
  commitLog,
  threads,
  openThreads,
  activityFilter,
  newComment,
  statusIcon,
  reviewCommentCount,
  postComment,
} = useMergeRequest()

const items = computed(() =>
  activityFilter.value === 'open' ? timeline.value.filter((item) => item.kind === 'thread' && !item.thread.root.resolvedBy) : timeline.value,
)
const showAll = computed(() => activityFilter.value === 'all')

function itemKey(item: TimelineItem): string {
  if (item.kind === 'review') return `review-${item.review.id}`
  if (item.kind === 'push') return `push-${item.event.push.id}`
  return `thread-${item.thread.root.id}`
}

const posting = ref(false)
const commentError = ref('')

async function submitComment() {
  const body = newComment.value.trim()
  if (!body || posting.value) return
  posting.value = true
  commentError.value = ''
  try {
    await postComment({ body })
    newComment.value = ''
  } catch (e) {
    commentError.value = errorText(e, 'Failed to comment')
  } finally {
    posting.value = false
  }
}
</script>

<template>
  <section v-if="mr" class="activity">
    <div class="activity-head">
      <h3 class="form-label">Activity</h3>
      <div v-if="threads.length" class="segmented" role="radiogroup" aria-label="Show">
        <button type="button" role="radio" class="segment" :class="{ active: showAll }" :aria-checked="showAll" @click="activityFilter = 'all'">
          All activity
        </button>
        <button type="button" role="radio" class="segment" :class="{ active: !showAll }" :aria-checked="!showAll" @click="activityFilter = 'open'">
          Open threads
          <span class="segment-count">{{ openThreads.length }}</span>
        </button>
      </div>
    </div>
    <ol class="timeline">
      <li v-if="showAll" class="event" :class="{ 'event-small': !openedCommits.length }">
        <span class="event-node tone-active"><MrIcon name="open" :size="13" /></span>
        <div class="event-content">
          <span class="event-line">
            Opened from <span class="mono">{{ mr.sourceBranch }}</span>
            <template v-if="mr.taskId"> by task #{{ mr.taskId }}</template>
            <template v-if="openedCommits.length"> with {{ plural(openedCommits.length, 'commit') }}</template>
            <span class="muted" :title="fullTime(mr.createdAt)"> &middot; {{ timeAgo(mr.createdAt) }}</span>
          </span>
          <MrCommits v-if="openedCommits.length" :commits="openedCommits" :truncated="commitLog?.truncated" />
        </div>
      </li>

      <template v-for="item in items" :key="itemKey(item)">
        <li v-if="item.kind === 'review'" class="event">
          <span class="event-node" :class="`tone-${reviewTone(item.review)}`">
            <MrIcon :name="reviewIcon(item.review)" :size="13" />
          </span>
          <article class="card review-card" :class="`edge-${reviewTone(item.review)}`">
            <div class="card-header">
              <span class="avatar" :class="`avatar-${item.review.author}`">
                <MrIcon :name="authorIcon(item.review.author)" :size="13" />
              </span>
              <span class="card-header-text">
                <strong>{{ authorName(item.review.author) }}</strong>
                <span>{{ reviewAction(item.review) }}</span>
                <span class="muted" :title="fullTime(item.review.createdAt)">&middot; {{ timeAgo(item.review.createdAt) }}</span>
              </span>
              <span class="card-header-end">
                <span v-if="reviewCommentCount(item.review)" class="tag">{{ plural(reviewCommentCount(item.review), 'comment') }}</span>
                <span v-if="item.review.model" class="tag">{{ item.review.model }}</span>
                <span v-if="item.review.headSha && item.review.headSha !== mr.headSha" class="tag tag-outdated" :title="`Reviewed ${shortSha(item.review.headSha)}`">outdated</span>
                <span v-if="item.review.status === 'pending'" class="spinner spinner-sm" />
              </span>
            </div>
            <div v-if="item.review.error || item.review.body || (item.review.workspaceId && item.review.status === 'pending')" class="card-body">
              <p v-if="item.review.error" class="notice notice-error">{{ item.review.error }}</p>
              <MarkdownMessage v-if="item.review.body" :content="item.review.body" />
              <RouterLink
                v-if="item.review.workspaceId && item.review.status === 'pending'"
                class="btn btn-secondary btn-sm watch-link"
                :to="{ name: 'ide', params: { id: item.review.workspaceId } }"
              >
                <MrIcon name="terminal" :size="13" />
                Watch in sandbox
              </RouterLink>
            </div>
          </article>
        </li>

        <li v-else-if="item.kind === 'push'" class="event" :class="{ 'event-small': !item.event.commits.length }">
          <span class="event-node tone-neutral"><MrIcon name="commit" :size="13" /></span>
          <div class="event-content">
            <span class="event-line">
              {{ pushSummary(item.event.push, item.event.commits.length, mr.sourceBranch) }}
              <span class="muted" :title="fullTime(item.event.push.createdAt)"> &middot; {{ timeAgo(item.event.push.createdAt) }}</span>
            </span>
            <MrCommits v-if="item.event.commits.length" :commits="item.event.commits" />
          </div>
        </li>

        <li v-else class="event">
          <span class="event-node" :class="item.thread.root.resolvedBy ? 'tone-success' : 'tone-neutral'">
            <MrIcon :name="item.thread.root.resolvedBy ? 'check-circle' : 'comment'" :size="13" />
          </span>
          <MrThread :thread="item.thread" />
        </li>
      </template>

      <li v-if="showAll && mr.status !== 'open'" class="event event-small">
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

      <li v-if="isOpen && showAll && timeline.length === 0" class="event event-small">
        <span class="event-node tone-neutral"><MrIcon name="comment" :size="13" /></span>
        <span class="event-line">No reviews or comments yet. Use <strong>Review</strong> above, or start an agent review from the sidebar.</span>
      </li>

      <li v-if="isOpen && showAll" class="event">
        <span class="event-node tone-active"><MrIcon name="user" :size="13" /></span>
        <form class="card composer" @submit.prevent="submitComment">
          <textarea
            v-model="newComment"
            class="form-input form-textarea"
            rows="3"
            placeholder="Comment on the merge request... (Markdown)"
            aria-label="New comment"
            @keydown.meta.enter.prevent="submitComment"
            @keydown.ctrl.enter.prevent="submitComment"
          />
          <div class="composer-footer">
            <span v-if="commentError" class="form-error">{{ commentError }}</span>
            <span v-else class="form-hint">Starts a thread that can be replied to and resolved. To comment on a line, click its number on the Changes tab.</span>
            <button type="submit" class="btn btn-primary btn-sm" :disabled="posting || !newComment.trim()">Comment</button>
          </div>
        </form>
      </li>
    </ol>
    <p v-if="!showAll && items.length === 0" class="empty-text timeline-empty">Every thread is resolved.</p>
  </section>
</template>

<style scoped src="@/components/project/shared.css"></style>
<style scoped src="@/assets/styles/segmented.css"></style>
<style scoped src="./mergerequest.css"></style>
<style scoped>
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
  box-shadow: 0 0 0 4px var(--bg-base);
}

.event-node::after {
  content: '';
  position: absolute;
  inset: 0;
  border-radius: 50%;
  background: var(--bg-base);
  z-index: -1;
}

.event-small {
  align-items: center;
}

.event-line {
  font-size: 0.8rem;
  color: var(--text-secondary);
}

.event-content {
  min-width: 0;
}

/* Lines up the text with the middle of the node beside it, when commits
 * follow it below. */
.event:not(.event-small) .event-content > .event-line {
  display: block;
  padding-top: 5px;
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

.activity-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  flex-wrap: wrap;
  gap: var(--space-2);
  margin-bottom: var(--space-3);
}

.activity-head .form-label {
  margin: 0;
}

.segment-count {
  margin-left: 5px;
  padding: 0 5px;
  border-radius: var(--radius-sm);
  background: var(--bg-neutral);
  font-size: 0.68rem;
  font-variant-numeric: tabular-nums;
}

.composer {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
  padding: var(--space-3);
}

.composer-footer {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-3);
}

.composer-footer .form-hint,
.composer-footer .form-error {
  margin: 0;
}

.watch-link {
  margin-top: var(--space-3);
}

.timeline-empty {
  padding-left: calc(27px + var(--space-3));
}
</style>
