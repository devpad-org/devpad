<script setup lang="ts">
import { computed, ref } from 'vue'
import type { Commit } from '@/api/mergeRequests'
import { fullTime } from '@/utils/time'
import { plural, shortSha } from './format'

// The commits one event in the activity added, newest first. A long list
// shows its newest few until it is opened. A commit's date is whatever its
// author chose, so it is only in the tooltip.
const props = defineProps<{ commits: Commit[]; truncated?: boolean }>()

const FOLDED = 5

const expanded = ref(false)
const shown = computed(() => (expanded.value ? props.commits : props.commits.slice(0, FOLDED)))
const hidden = computed(() => props.commits.length - shown.value.length)
</script>

<template>
  <div class="card commits">
    <ol class="commit-list">
      <li v-for="commit in shown" :key="commit.sha" class="commit" :title="`${commit.sha}\nAuthored ${fullTime(commit.authoredAt)}`">
        <span class="mono commit-sha">{{ shortSha(commit.sha) }}</span>
        <span class="commit-subject">{{ commit.subject || '(no message)' }}</span>
        <span class="commit-author muted">{{ commit.author }}</span>
      </li>
    </ol>
    <div v-if="hidden > 0 || (expanded && commits.length > FOLDED) || truncated" class="commits-footer">
      <button v-if="hidden > 0" type="button" class="text-button" @click="expanded = true">Show {{ plural(hidden, 'more commit') }}</button>
      <button v-else-if="expanded && commits.length > FOLDED" type="button" class="text-button" @click="expanded = false">Show fewer</button>
      <span v-if="truncated" class="muted">Older commits are not listed.</span>
    </div>
  </div>
</template>

<style scoped src="@/components/project/shared.css"></style>
<style scoped src="./mergerequest.css"></style>
<style scoped>
.commits {
  margin-top: var(--space-2);
  overflow: hidden;
}

.commit-list {
  list-style: none;
}

.commit {
  display: flex;
  align-items: baseline;
  gap: var(--space-3);
  padding: 6px var(--space-3);
  font-size: 0.8rem;
}

.commit + .commit {
  border-top: 0.5px solid var(--border-hairline);
}

.commit-sha {
  flex-shrink: 0;
  color: var(--accent-blue);
}

.commit-subject {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  color: var(--text-primary);
}

.commit-author {
  flex-shrink: 1;
  max-width: 30%;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: 0.75rem;
}

.commits-footer {
  display: flex;
  align-items: center;
  gap: var(--space-3);
  padding: 6px var(--space-3);
  border-top: 0.5px solid var(--border-hairline);
  font-size: 0.75rem;
}
</style>
