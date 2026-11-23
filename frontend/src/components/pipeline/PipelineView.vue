<script setup lang="ts">
import { computed, toRef } from 'vue'
import type { Pipeline } from '@/api/pipelines'
import DocLink from '@/components/DocLink.vue'
import MrIcon from '@/components/project/MrIcon.vue'
import StatusBadge from '@/components/project/StatusBadge.vue'
import { pipelineFinished, usePipeline } from '@/composables/usePipelines'
import { useNow } from '@/composables/useNow'
import { shortSha } from '@/components/mergerequest/format'
import { fullTime, timeAgo } from '@/utils/time'
import { PIPELINE_STATUS, formatDuration, imageLabel, runTime, sourceLabel, subjectLabel, triggerLabel } from './pipelineFormat'
import PipelineJobs from './PipelineJobs.vue'

// One pipeline: what it tested, where its jobs came from, how it went, and
// its jobs with their output, read again while it runs. Run again and
// Cancel act on it.
const props = defineProps<{
  projectId: number
  pipelineId: number
  // Read it, and its jobs' output, again while it runs: false while it is
  // out of sight.
  active: boolean
  // Leaves out what the page around it already says, such as the merge
  // request it is of.
  compact?: boolean
  // Which of its failing jobs fail on the merge request's target too.
  alsoFailing?: { target: string; jobs: string[] }
}>()

const emit = defineEmits<{
  // A pipeline Run again started, or one already running the same.
  started: [pipeline: Pipeline]
}>()

const { pipeline, error, busy, cancel, runAgain } = usePipeline(toRef(props, 'projectId'), toRef(props, 'pipelineId'), toRef(props, 'active'))

const look = computed(() => (pipeline.value ? PIPELINE_STATUS[pipeline.value.status] : null))
const unfinished = computed(() => !!pipeline.value && !pipelineFinished(pipeline.value))
const now = useNow(unfinished)

// How long it waited for its turn before it started.
const waited = computed(() => {
  const p = pipeline.value
  if (!p) return ''
  const end = p.startedAt ? Date.parse(p.startedAt) : p.finishedAt ? Date.parse(p.finishedAt) : now.value
  return formatDuration(end - Date.parse(p.createdAt))
})

// What the status pill can't say.
const statusDetail = computed(() => {
  const p = pipeline.value
  if (!p) return ''
  switch (p.status) {
    case 'queued':
      return "Waiting for the environment to build, or for another of the project's jobs to finish."
    case 'not_configured':
      return `${p.kind === 'branch' ? 'The commit' : `${p.targetBranch}'s commit`} has no .devpad/pipeline.yml and the project has no pipeline setting, so nothing was tested.`
    case 'invalid':
      return "The pipeline doesn't parse, so nothing ran."
    case 'conflict':
      return `${p.sourceBranch} doesn't merge onto ${p.targetBranch}, so nothing ran.`
    default:
      return ''
  }
})

async function onRunAgain() {
  const p = await runAgain()
  if (p) emit('started', p)
}
</script>

<template>
  <div class="pipeline-view">
    <p v-if="!pipeline && error" class="notice notice-error">{{ error }}</p>
    <p v-else-if="!pipeline" class="muted">Loading pipeline...</p>

    <template v-else>
      <header class="pv-head">
        <StatusBadge :status="pipeline.status" :tone="look?.tone" :label="look?.label" class="pv-status" />
        <h3 class="pv-title">
          <span class="mono">#{{ pipeline.id }}</span>
          <span v-if="!compact" class="pv-subject mono">{{ subjectLabel(pipeline) }}</span>
        </h3>
        <DocLink topic="pipelineStatuses" label="Pipeline statuses" />
        <span class="pv-actions">
          <button v-if="unfinished" type="button" class="btn btn-secondary btn-sm" :disabled="busy" @click="cancel">
            <MrIcon name="stop" :size="12" />
            {{ busy ? 'Cancelling…' : 'Cancel' }}
          </button>
          <button v-else type="button" class="btn btn-secondary btn-sm" :disabled="busy" :title="`Run ${subjectLabel(pipeline)} again at its current commits`" @click="onRunAgain">
            <MrIcon name="retry" :size="12" />
            Run again
          </button>
        </span>
      </header>

      <p v-if="statusDetail" class="pv-detail">{{ statusDetail }}</p>
      <p v-if="pipeline.error" class="notice pv-error" :class="look?.tone === 'error' ? 'notice-error' : 'notice-warning'">{{ pipeline.error }}</p>
      <ul v-if="pipeline.conflictPaths.length" class="pv-conflicts">
        <li v-for="path in pipeline.conflictPaths" :key="path" class="mono"><MrIcon name="file" :size="12" />{{ path }}</li>
      </ul>
      <p v-if="error" class="notice notice-error">{{ error }}</p>

      <dl class="pv-facts">
        <div class="fact">
          <dt>Tested</dt>
          <dd v-if="pipeline.kind === 'branch'">
            <span class="mono">{{ pipeline.branch }}</span> at <span class="mono" :title="pipeline.headSha">{{ shortSha(pipeline.headSha) }}</span>
          </dd>
          <dd v-else>
            <span class="mono" :title="pipeline.headSha">{{ shortSha(pipeline.headSha) }}</span> merged onto
            <span class="mono">{{ pipeline.targetBranch }}</span> at <span class="mono" :title="pipeline.targetSha">{{ shortSha(pipeline.targetSha) }}</span>
            <span v-if="pipeline.testedSha && pipeline.testedSha !== pipeline.headSha && pipeline.testedSha !== pipeline.targetSha" class="muted">
              as <span class="mono" :title="pipeline.testedSha">{{ shortSha(pipeline.testedSha) }}</span></span>
          </dd>
        </div>
        <div class="fact">
          <dt>Jobs from</dt>
          <dd>
            <span :class="{ mono: pipeline.configSource === 'file' }">{{ sourceLabel(pipeline) }}</span>
            <span v-if="pipeline.configSource === 'project'" class="muted"> (the commit has no .devpad/pipeline.yml)</span>
          </dd>
        </div>
        <div class="fact">
          <dt>Trigger</dt>
          <dd>{{ triggerLabel(pipeline) }} <span class="muted" :title="fullTime(pipeline.createdAt)">· {{ timeAgo(pipeline.createdAt) }}</span></dd>
        </div>
        <div v-if="pipeline.image" class="fact">
          <dt>Image</dt>
          <dd class="mono ellipsis" :title="pipeline.image">{{ imageLabel(pipeline.image) }}</dd>
        </div>
        <div class="fact">
          <dt>Time</dt>
          <dd>
            <template v-if="pipeline.startedAt">
              ran {{ runTime(pipeline, now) }}<span class="muted"> after waiting {{ waited }}</span>
            </template>
            <template v-else-if="unfinished">waiting {{ waited }}</template>
            <template v-else>never ran</template>
          </dd>
        </div>
      </dl>

      <div v-if="pipeline.jobs.length" class="pv-jobs">
        <div class="pv-jobs-head">
          <h4 class="pv-label">Jobs</h4>
          <DocLink topic="pipelineJobs" label="How jobs run" />
        </div>
        <PipelineJobs :pipeline="pipeline" :active="active" :also-failing="alsoFailing" />
      </div>
    </template>
  </div>
</template>

<style scoped src="@/components/project/shared.css"></style>
<style scoped>
.pipeline-view {
  display: flex;
  flex-direction: column;
  gap: var(--space-3);
  min-width: 0;
}

.pv-head {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: var(--space-2);
  min-width: 0;
}

.pv-title {
  display: flex;
  align-items: baseline;
  gap: var(--space-2);
  min-width: 0;
  font-size: 0.9rem;
  font-weight: 600;
  color: var(--text-primary);
}

.pv-subject {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: 0.8rem;
  font-weight: 500;
  color: var(--text-secondary);
}

.pv-actions {
  display: flex;
  gap: var(--space-2);
  margin-left: auto;
}

.pv-actions .btn {
  display: inline-flex;
  align-items: center;
  gap: 5px;
}

.pv-detail {
  font-size: 0.78rem;
  color: var(--text-secondary);
}

.pv-error {
  margin: 0;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}

.pv-conflicts {
  list-style: none;
  display: flex;
  flex-direction: column;
  gap: 2px;
  font-size: 0.75rem;
  color: var(--text-secondary);
}

.pv-conflicts li {
  display: flex;
  align-items: center;
  gap: 6px;
}

.pv-facts {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(220px, 1fr));
  gap: var(--space-2) var(--space-4);
  margin: 0;
}

.fact {
  display: flex;
  flex-direction: column;
  gap: 1px;
  min-width: 0;
}

.fact dt {
  font-size: 0.66rem;
  font-weight: 600;
  letter-spacing: 0.05em;
  text-transform: uppercase;
  color: var(--text-tertiary);
}

.fact dd {
  margin: 0;
  font-size: 0.78rem;
  color: var(--text-primary);
  overflow-wrap: anywhere;
}

.pv-jobs {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
}

.pv-jobs-head {
  display: flex;
  align-items: center;
  gap: var(--space-2);
}

.pv-label {
  font-size: 0.7rem;
  font-weight: 600;
  letter-spacing: 0.05em;
  text-transform: uppercase;
  color: var(--text-tertiary);
}

.ellipsis {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
</style>
