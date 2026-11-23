<script setup lang="ts">
import { computed } from 'vue'
import type { Pipeline } from '@/api/pipelines'
import MrIcon from '@/components/project/MrIcon.vue'
import { useNow } from '@/composables/useNow'
import { shortSha } from '@/components/mergerequest/format'
import { fullTime, timeAgo } from '@/utils/time'
import { PIPELINE_STATUS, runTime } from './pipelineFormat'
import PipelineStatusIcon from './PipelineStatusIcon.vue'

// Pipelines, newest first, one row each: how it went, what it tested, and
// when. A row picks the pipeline to show.
const props = defineProps<{
  pipelines: Pipeline[]
  selectedId: number | null
}>()

const emit = defineEmits<{ select: [id: number] }>()

const running = computed(() => props.pipelines.some((p) => p.status === 'running'))
const now = useNow(running)
</script>

<template>
  <ul class="pl" aria-label="Pipelines">
    <li v-for="p in pipelines" :key="p.id">
      <button
        type="button"
        class="pl-row"
        :class="{ selected: p.id === selectedId }"
        :aria-current="p.id === selectedId ? 'true' : undefined"
        @click="emit('select', p.id)"
      >
        <PipelineStatusIcon :look="PIPELINE_STATUS[p.status]" :size="20" />
        <span class="pl-main">
          <span class="pl-title">
            <span v-if="p.kind === 'branch'" class="mono ellipsis"><MrIcon name="branch" :size="11" class="pl-kind" />{{ p.branch }}</span>
            <span v-else class="mono ellipsis"><MrIcon name="open" :size="11" class="pl-kind" />!{{ p.mergeRequestId }} {{ p.sourceBranch }} → {{ p.targetBranch }}</span>
          </span>
          <span class="pl-sub">
            <span class="mono">#{{ p.id }}</span>
            <span class="mono" :title="p.headSha">{{ shortSha(p.headSha) }}</span>
            <span>{{ PIPELINE_STATUS[p.status].label }}</span>
            <span v-if="p.jobs.length">{{ p.jobs.length }} {{ p.jobs.length === 1 ? 'job' : 'jobs' }}</span>
          </span>
        </span>
        <span class="pl-end">
          <span :title="fullTime(p.createdAt)">{{ timeAgo(p.createdAt) }}</span>
          <span v-if="p.startedAt" class="pl-time">{{ runTime(p, now) }}</span>
        </span>
      </button>
    </li>
  </ul>
</template>

<style scoped>
.pl {
  list-style: none;
  margin: 0;
  padding: 0;
}

.pl li + li {
  border-top: 0.5px solid var(--border-hairline);
}

.pl-row {
  display: flex;
  align-items: center;
  gap: var(--space-3);
  width: 100%;
  min-height: 52px;
  padding: var(--space-2) var(--space-3);
  text-align: left;
  color: var(--text-primary);
  transition: background var(--transition-fast);
}

.pl-row:hover {
  background: var(--bg-hover);
}

.pl-row.selected {
  background: var(--bg-selected);
  box-shadow: inset 2px 0 0 var(--accent);
}

.pl-row:focus-visible {
  outline: none;
  box-shadow: inset 0 0 0 1px var(--accent-border);
}

.pl-main {
  display: flex;
  flex-direction: column;
  gap: 2px;
  flex: 1;
  min-width: 0;
}

.pl-title {
  display: flex;
  min-width: 0;
  font-size: 0.8rem;
  font-weight: 500;
}

.pl-kind {
  margin-right: 5px;
  vertical-align: -1px;
  color: var(--text-tertiary);
}

.pl-sub {
  display: flex;
  gap: var(--space-2);
  min-width: 0;
  overflow: hidden;
  white-space: nowrap;
  font-size: 0.7rem;
  color: var(--text-tertiary);
}

.pl-end {
  display: flex;
  flex-direction: column;
  align-items: flex-end;
  gap: 2px;
  flex-shrink: 0;
  font-size: 0.7rem;
  color: var(--text-tertiary);
  white-space: nowrap;
}

.pl-time {
  font-variant-numeric: tabular-nums;
}

.mono {
  font-family: var(--font-mono);
}

.ellipsis {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
</style>
