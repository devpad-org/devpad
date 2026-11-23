<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import type { Pipeline, PipelineJob } from '@/api/pipelines'
import MrIcon from '@/components/project/MrIcon.vue'
import { useNow } from '@/composables/useNow'
import { JOB_STATUS, PIPELINE_COPY, formatDuration, jobOutcome, runTime } from './pipelineFormat'
import PipelineJobLog from './PipelineJobLog.vue'
import PipelineStatusIcon from './PipelineStatusIcon.vue'

// A pipeline's jobs in their file's order, each with how it stands and how
// long it ran, opening to its script and its output.
const props = defineProps<{
  pipeline: Pipeline
  // Read open jobs' output: false while the jobs are out of sight.
  active: boolean
  // The failing jobs that fail on the merge request's target too, in its
  // own pipeline of the same commit, and the target's name.
  alsoFailing?: { target: string; jobs: string[] }
}>()

const failingOnTarget = computed(() => new Set(props.alsoFailing?.jobs ?? []))

const open = ref(new Set<number>())

// A pipeline opens on its first job that didn't pass, so a failure shows
// what went wrong at once.
watch(
  () => props.pipeline.id,
  () => {
    const failed = props.pipeline.jobs.find((j) => JOB_STATUS[j.status].tone === 'error')
    open.value = new Set(failed ? [failed.id] : props.pipeline.jobs.length === 1 ? [props.pipeline.jobs[0].id] : [])
  },
  { immediate: true },
)

const running = computed(() => props.pipeline.jobs.some((j) => j.status === 'running'))
const now = useNow(running)

function toggle(job: PipelineJob) {
  const set = new Set(open.value)
  if (set.has(job.id)) set.delete(job.id)
  else set.add(job.id)
  open.value = set
}
</script>

<template>
  <ol class="jobs" aria-label="Jobs">
    <li v-for="job in pipeline.jobs" :key="job.id" class="job" :class="{ open: open.has(job.id) }">
      <button type="button" class="job-head" :aria-expanded="open.has(job.id)" @click="toggle(job)">
        <MrIcon name="chevron-right" :size="12" class="job-chevron" />
        <PipelineStatusIcon :look="JOB_STATUS[job.status]" :size="18" />
        <span class="job-name mono">{{ job.name }}</span>
        <span v-for="svc in job.services" :key="svc" class="job-tag" :title="`Gets a fresh ${svc} of its own`">{{ svc }}</span>
        <span
          v-if="alsoFailing && failingOnTarget.has(job.name)"
          class="job-tag job-tag-target"
          :title="PIPELINE_COPY.alsoFailingHint(alsoFailing.target)"
        >{{ PIPELINE_COPY.alsoFailing(alsoFailing.target) }}</span>
        <span class="job-status">{{ JOB_STATUS[job.status].label }}</span>
        <span class="job-time" :title="`Timeout ${formatDuration(job.timeoutMs)}`">{{ runTime(job, now) }}</span>
      </button>
      <p v-if="jobOutcome(job) && !open.has(job.id)" class="job-outcome" :class="`tone-${JOB_STATUS[job.status].tone}`">{{ jobOutcome(job) }}</p>
      <div v-if="open.has(job.id)" class="job-body">
        <p v-if="jobOutcome(job)" class="job-outcome job-outcome-open" :class="`tone-${JOB_STATUS[job.status].tone}`">{{ jobOutcome(job) }}</p>
        <details v-if="job.run" class="job-script">
          <summary>Script <span class="muted">· timeout {{ formatDuration(job.timeoutMs) }}</span></summary>
          <pre class="script">{{ job.run }}</pre>
        </details>
        <PipelineJobLog :project-id="pipeline.projectId" :pipeline-id="pipeline.id" :job="job" :active="active" />
      </div>
    </li>
  </ol>
</template>

<style scoped>
.jobs {
  list-style: none;
  display: flex;
  flex-direction: column;
  border: 0.5px solid var(--border-subtle);
  border-radius: var(--radius-md);
  background: var(--bg-base);
  overflow: hidden;
}

.job + .job {
  border-top: 0.5px solid var(--border-hairline);
}

.job-head {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  width: 100%;
  min-height: 38px;
  padding: var(--space-2) var(--space-3);
  text-align: left;
  color: var(--text-primary);
  transition: background var(--transition-fast);
}

.job-head:hover {
  background: var(--bg-hover);
}

.job-head:focus-visible {
  outline: none;
  box-shadow: inset 0 0 0 1px var(--accent-border);
}

.job-chevron {
  color: var(--text-tertiary);
  transition: transform var(--transition-fast);
}

.job.open .job-chevron {
  transform: rotate(90deg);
}

.job-name {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: 0.8rem;
  font-weight: 500;
}

.job-tag {
  flex-shrink: 0;
  padding: 0 6px;
  border-radius: var(--radius-sm);
  background: var(--bg-neutral);
  color: var(--text-tertiary);
  font-family: var(--font-mono);
  font-size: 0.66rem;
}

.job-tag-target {
  border: 0.5px solid var(--warning-border);
  background: var(--warning-bg);
  color: var(--warning);
  font-family: var(--font-sans);
}

.job-status {
  margin-left: auto;
  flex-shrink: 0;
  font-size: 0.75rem;
  color: var(--text-secondary);
}

.job-time {
  flex-shrink: 0;
  min-width: 4.5em;
  text-align: right;
  font-size: 0.75rem;
  color: var(--text-tertiary);
  font-variant-numeric: tabular-nums;
}

.job-outcome {
  margin: 0;
  padding: 0 var(--space-3) var(--space-2) calc(var(--space-3) + 12px + 18px + 2 * var(--space-2));
  font-size: 0.75rem;
  color: var(--text-secondary);
  overflow-wrap: anywhere;
}

.job-outcome.tone-error {
  color: var(--error);
}

.job-outcome-open {
  padding: 0;
}

.job-body {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
  padding: 0 var(--space-3) var(--space-3);
}

.job-script summary {
  cursor: pointer;
  font-size: 0.75rem;
  color: var(--text-secondary);
}

.script {
  margin-top: var(--space-1);
  max-height: 12em;
  overflow: auto;
  padding: var(--space-2) var(--space-3);
  background: var(--bg-void);
  border: 0.5px solid var(--border-hairline);
  border-radius: var(--radius-md);
  font-family: var(--font-mono);
  font-size: 0.72rem;
  line-height: 1.45;
  color: var(--text-secondary);
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}

.mono {
  font-family: var(--font-mono);
}

.muted {
  color: var(--text-tertiary);
}
</style>
