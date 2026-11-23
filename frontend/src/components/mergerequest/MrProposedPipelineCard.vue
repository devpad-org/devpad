<script setup lang="ts">
import { computed } from 'vue'
import DocLink from '@/components/DocLink.vue'
import MrIcon from '@/components/project/MrIcon.vue'
import PipelineView from '@/components/pipeline/PipelineView.vue'
import { PIPELINE_COPY } from '@/components/pipeline/pipelineFormat'
import { shortSha } from './format'
import { useMergeRequest } from './useMergeRequest'

// The pipeline the merge request's change proposes: when it touches the
// pipeline file, devpad runs the jobs of the file as the change leaves it,
// on the branch merged onto the target, apart from the merge request's own
// pipeline. Nothing here keeps the merge request from merging; the server
// counts none of it.
const { mr, projectId, isOpen, pipelines } = useMergeRequest()
const { proposed, proposedShown, proposedStale, proposedStarting, proposedStartError } = pipelines

// Nothing runs for a change that removes the pipeline file, that doesn't
// merge onto its target, or that proposes the jobs the merge request's own
// pipeline runs.
const runnable = computed(() => !!proposed.value && !proposed.value.removed && !proposed.value.conflict && !proposed.value.sameJobs)
const showingSettled = computed(() => {
  const p = proposed.value
  return !!p?.settled && proposedShown.value?.id === p.settled.pipeline.id
})

function started() {
  void pipelines.load()
}
</script>

<template>
  <section v-if="mr && isOpen && proposed" class="card proposed-card" aria-label="Proposed pipeline">
    <div class="card-header">
      <span class="card-header-text">
        <strong>Proposed pipeline</strong>
        <span class="note">{{ PIPELINE_COPY.proposedNote }}</span>
        <DocLink topic="proposedPipeline" label="Proposed pipelines" />
      </span>
      <span class="card-header-end">
        <button
          v-if="runnable"
          type="button"
          class="btn btn-secondary btn-sm"
          :disabled="proposedStarting"
          :title="`Run the jobs this change proposes on this branch merged onto ${mr.targetBranch}`"
          @click="pipelines.startProposed"
        >
          <MrIcon name="play" :size="12" />
          {{ proposedStarting ? 'Starting…' : proposedShown ? 'Run proposed pipeline again' : 'Run proposed pipeline' }}
        </button>
      </span>
    </div>
    <div class="card-body">
      <p class="explain">{{ PIPELINE_COPY.proposedExplain(proposed.file, mr.targetBranch) }}</p>
      <p v-if="proposedStartError" class="notice notice-error">{{ proposedStartError }}</p>
      <p v-if="proposed.removed" class="notice notice-info">{{ PIPELINE_COPY.proposedRemoved(mr.targetBranch) }}</p>
      <p v-else-if="proposed.conflict" class="notice notice-warning">{{ PIPELINE_COPY.proposedConflict(mr.targetBranch) }}</p>
      <p v-else-if="proposed.sameJobs" class="notice notice-info">{{ PIPELINE_COPY.proposedSameJobs(mr.targetBranch) }}</p>
      <template v-else>
        <!-- The pipeline shown may be of older commits, which parsed. -->
        <p v-if="proposed.invalid && !proposed.current" class="notice notice-error">{{ proposed.invalid }}</p>
        <p v-if="showingSettled && proposed.settled" class="notice" :class="proposed.settled.pipeline.status === 'cancelled' ? 'notice-warning' : 'notice-error'">
          {{ proposed.settled.reason }}
        </p>
        <p v-if="proposedStale && proposedShown" class="notice notice-warning">
          This proposed pipeline tested <span class="mono">{{ shortSha(proposedShown.headSha) }}</span> onto {{ proposedShown.targetBranch }} at
          <span class="mono">{{ shortSha(proposedShown.targetSha) }}</span>; now it is <span class="mono">{{ shortSha(proposed.headSha) }}</span> onto
          {{ mr.targetBranch }} at <span class="mono">{{ shortSha(proposed.targetSha) }}</span>.
        </p>
        <PipelineView v-if="proposedShown" :key="proposedShown.id" :project-id="projectId" :pipeline-id="proposedShown.id" :active="true" compact @started="started" />
        <p v-else class="muted empty">{{ proposed.automatic ? PIPELINE_COPY.proposedNoneAutomatic : PIPELINE_COPY.proposedNone(mr.targetBranch) }}</p>
      </template>
    </div>
  </section>
</template>

<style scoped src="@/components/project/shared.css"></style>
<style scoped src="./mergerequest.css"></style>
<style scoped>
.card-header-text {
  display: inline-flex;
  align-items: center;
  flex-wrap: wrap;
  gap: var(--space-2);
}

.note {
  color: var(--text-tertiary);
  font-size: 0.72rem;
}

.card-header-end .btn {
  display: inline-flex;
  align-items: center;
  gap: 5px;
}

.card-body {
  display: flex;
  flex-direction: column;
  gap: var(--space-3);
}

.card-body .notice {
  margin: 0;
}

.explain {
  margin: 0;
  color: var(--text-secondary);
  font-size: 0.8rem;
}

.empty {
  margin: 0;
  color: var(--text-tertiary);
  font-size: 0.8rem;
}
</style>
