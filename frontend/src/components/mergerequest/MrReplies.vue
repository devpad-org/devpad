<script setup lang="ts">
import type { ReviewComment } from '@/api/mergeRequests'
import MarkdownMessage from '@/components/ide/MarkdownMessage.vue'
import MrIcon from '@/components/project/MrIcon.vue'
import { fullTime, timeAgo } from '@/utils/time'
import { authorIcon, authorName } from './format'

// The replies under a comment, such as the task agent saying how it
// addressed it.
defineProps<{ replies: ReviewComment[] }>()
</script>

<template>
  <div class="replies">
    <div v-for="r in replies" :key="r.id" class="reply">
      <div class="reply-meta">
        <span class="avatar avatar-sm" :class="`avatar-${r.author}`">
          <MrIcon :name="authorIcon(r.author)" :size="11" />
        </span>
        <strong>{{ authorName(r.author) }}</strong>
        <span class="muted" :title="fullTime(r.createdAt)">{{ timeAgo(r.createdAt) }}</span>
      </div>
      <MarkdownMessage :content="r.body" />
    </div>
  </div>
</template>

<style scoped src="./mergerequest.css"></style>
<style scoped>
.replies {
  display: flex;
  flex-direction: column;
  border-top: 0.5px solid var(--border-default);
}

.reply {
  padding: var(--space-2) var(--space-3) var(--space-2) calc(var(--space-3) + var(--space-4));
  font-size: 0.82rem;
}

.reply + .reply {
  border-top: 0.5px solid var(--border-subtle);
}

.reply-meta {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  margin-bottom: var(--space-1);
  font-size: 0.75rem;
}
</style>
