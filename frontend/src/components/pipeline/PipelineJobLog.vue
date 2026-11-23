<script setup lang="ts">
import { computed } from 'vue'
import type { PipelineJob } from '@/api/pipelines'
import { useJobOutput } from '@/composables/useJobOutput'
import { formatBytes } from './pipelineFormat'
import { MAX_LOG_LINES } from '@/utils/logLines'
import LogView from './LogView.vue'

// One job's output, live while it runs, with devpad's notes on the job
// beside it rather than in it: the job prints whatever it likes, so nothing
// it prints is shown as devpad's.
const props = defineProps<{
  projectId: number
  pipelineId: number
  job: PipelineJob
  // Read the output: false while the job is out of sight.
  active: boolean
}>()

const target = computed(() => ({ projectId: props.projectId, pipelineId: props.pipelineId, jobId: props.job.id }))
// A queued job has printed nothing yet; its pipeline's next read says when
// it starts.
const reading = computed(() => props.active && props.job.status !== 'queued')
const { lines, notes, done, loaded, error, notKeptBytes, skipped, droppedLines } = useJobOutput(target, reading)
// Line numbers count from the job's first line only when every line from
// it on is here; past output devpad no longer keeps, or a gap, they
// would not.
const numbered = computed(() => notKeptBytes.value === 0 && !skipped.value)

const noteTime = (iso: string) => new Date(iso).toLocaleTimeString()
const lost = computed(() => !props.job.outputKept)
const shown = computed(() => props.job.status !== 'queued')
</script>

<template>
  <div class="job-log">
    <p v-if="!shown" class="log-state">Waiting for a job slot or for the pipeline to start.</p>
    <template v-else>
      <ol v-if="notes.length" class="notes" aria-label="Devpad's notes on this job">
        <li v-for="(note, i) in notes" :key="i" class="note">
          <span class="note-tag">devpad</span>
          <time class="note-time" :datetime="note.at">{{ noteTime(note.at) }}</time>
          <span class="note-text">{{ note.text }}</span>
        </li>
      </ol>
      <p v-if="error" class="notice notice-error">{{ error }}</p>
      <p v-if="lost" class="log-state">
        This job's output is no longer kept: devpad keeps the output of the project's 50 newest pipelines.
      </p>
      <template v-else>
        <p v-if="notKeptBytes > 0" class="log-state">
          Devpad keeps the last 1 MB of a job's output, so the first {{ formatBytes(notKeptBytes) }} the job printed aren't shown.
        </p>
        <p v-if="droppedLines > 0" class="log-state">Showing the last {{ MAX_LOG_LINES.toLocaleString() }} lines: the {{ droppedLines.toLocaleString() }} before them are left out here.</p>
        <p v-if="skipped" class="log-state">Output came faster than it could be read, so some of it is missing where marked.</p>
        <LogView v-if="lines.length" :lines="lines" :first-line="droppedLines + 1" :numbered="numbered" />
        <p v-else-if="loaded && done" class="log-state">{{ job.startedAt ? 'The job printed nothing.' : 'The job never ran.' }}</p>
        <p v-else-if="!loaded && !error" class="log-state">Loading output...</p>
      </template>
    </template>
  </div>
</template>

<style scoped src="@/components/project/shared.css"></style>
<style scoped>
.job-log {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
  min-width: 0;
}

.log-state {
  font-size: 0.75rem;
  color: var(--text-tertiary);
}

/* Devpad's notes: in the app's own type and colours, never the log's, so
   no line of output looks like one. */
.notes {
  list-style: none;
  display: flex;
  flex-direction: column;
  gap: 2px;
  padding: var(--space-2) var(--space-3);
  border-left: 2px solid var(--accent-border);
  background: var(--bg-elevated);
  border-radius: 0 var(--radius-md) var(--radius-md) 0;
}

.note {
  display: flex;
  align-items: baseline;
  gap: var(--space-2);
  min-width: 0;
  font-size: 0.75rem;
  color: var(--text-secondary);
}

.note-tag {
  flex-shrink: 0;
  padding: 0 5px;
  border-radius: var(--radius-sm);
  background: var(--accent-glow);
  color: var(--accent);
  font-size: 0.65rem;
  font-weight: 600;
  letter-spacing: 0.03em;
}

.note-time {
  flex-shrink: 0;
  color: var(--text-tertiary);
  font-variant-numeric: tabular-nums;
}

.note-text {
  min-width: 0;
  overflow-wrap: anywhere;
}
</style>
