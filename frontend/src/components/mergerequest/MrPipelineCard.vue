<script setup lang="ts">
import { computed } from 'vue'
import DocLink from '@/components/DocLink.vue'
import MrIcon from '@/components/project/MrIcon.vue'
import PipelineView from '@/components/pipeline/PipelineView.vue'
import { PIPELINE_COPY, pipelineRoundsText } from '@/components/pipeline/pipelineCopy'
import { shortSha } from './format'
import { useMergeRequest } from './useMergeRequest'

// The merge request's pipeline: its latest, or one picked from the
// activity, with its jobs, where they came from and their output, a way to
// run it by hand, which devpad also does by itself into a ticked branch,
// and whether it keeps the merge request from merging. Its jobs come from
// the target branch, never the change.
const { mr, projectId, isOpen, pipelines } = useMergeRequest()
const { latest, noResult, status, current, shownId, shownStale, shownPipeline, pickedId, loaded, starting, startError, gateText, alsoFailing, targetPending } = pipelines

// How the task's automatic pipeline rounds stand: sent back after the
// pipeline failed, or the failure waiting for you.
const roundsText = computed(() => (isOpen.value ? pipelineRoundsText(mr.value?.task?.pipelineRounds) : ''))
const roundsHeld = computed(() => !!mr.value?.task?.pipelineRounds.held)
// The failing jobs to mark, only on the current pipeline.
const shownAlsoFailing = computed(() => (shownId.value !== null && shownId.value === current.value?.id ? alsoFailing.value : undefined))

// The card shows the pipeline of the current commits that was cancelled or
// couldn't run, saying why and whether devpad tries again.
const showingNoResult = computed(() => !!noResult.value && shownId.value === noResult.value.pipeline.id)

// Run again shows the pipeline it started, the latest.
function started() {
  pipelines.show(null)
  void pipelines.load()
}
</script>

<template>
  <section v-if="mr" id="mr-pipeline-card" class="card pipeline-card" aria-label="Pipeline">
    <div class="card-header">
      <span class="card-header-text">
        <strong>Pipeline</strong>
        <DocLink topic="mergeRequestPipelines" label="Merge request pipelines" />
      </span>
      <span class="card-header-end">
        <button v-if="pickedId !== null" type="button" class="btn btn-secondary btn-sm" @click="pipelines.show(null)">Show the current one</button>
        <button
          v-if="isOpen"
          type="button"
          class="btn btn-secondary btn-sm"
          :disabled="starting"
          :title="`Run the pipeline of ${mr.targetBranch} on this branch merged onto it`"
          @click="pipelines.start"
        >
          <MrIcon name="play" :size="12" />
          {{ starting ? 'Starting…' : latest || noResult ? 'Run pipeline again' : 'Run pipeline' }}
        </button>
      </span>
    </div>
    <div class="card-body">
      <p v-if="startError" class="notice notice-error">{{ startError }}</p>
      <p v-if="roundsText" class="notice" :class="roundsHeld ? 'notice-warning' : 'notice-info'">{{ roundsText }}</p>
      <p v-if="showingNoResult && noResult" class="notice" :class="noResult.pipeline.status === 'cancelled' ? 'notice-warning' : 'notice-error'">{{ noResult.reason }}</p>
      <p v-if="shownStale && status" class="notice notice-warning">
        <template v-if="shownPipeline">
          This pipeline tested <span class="mono">{{ shortSha(shownPipeline.headSha) }}</span> onto {{ shownPipeline.targetBranch }} at
          <span class="mono">{{ shortSha(shownPipeline.targetSha) }}</span>.
        </template>
        <template v-else>This pipeline isn't of the merge request as it is now.</template>
        Now it is <span class="mono">{{ shortSha(status.headSha) }}</span> onto {{ mr.targetBranch }} at <span class="mono">{{ shortSha(status.targetSha) }}</span><template v-if="shownPipeline && shownPipeline.headSha === status.headSha && shownPipeline.targetSha === status.targetSha">, with other jobs</template>.
      </p>
      <PipelineView
        v-if="shownId !== null"
        :key="shownId"
        :project-id="projectId"
        :pipeline-id="shownId"
        :active="true"
        :also-failing="shownAlsoFailing"
        compact
        @started="started"
      />
      <p v-else-if="loaded" class="muted empty">
        {{ status?.automatic ? PIPELINE_COPY.mergeRequestNoneAutomatic(mr.targetBranch) : PIPELINE_COPY.mergeRequestNone(mr.targetBranch) }} Its jobs come from <span class="mono">.devpad/pipeline.yml</span> on
        <span class="mono">{{ mr.targetBranch }}</span>, or the project's pipeline setting, and test this branch merged onto it.
      </p>
      <template v-if="loaded">
        <p v-if="targetPending && shownId !== null && shownId === current?.id" class="muted empty">{{ PIPELINE_COPY.targetPending(mr.targetBranch) }}</p>
        <p v-if="gateText" class="muted empty">{{ gateText }}</p>
        <p v-if="isOpen && mr.reviewAwaitsPipeline" class="muted empty">{{ PIPELINE_COPY.reviewAwaitsPipeline }}</p>
      </template>
      <p v-else class="muted empty">Loading pipelines...</p>
    </div>
  </section>
</template>

<style scoped src="@/components/project/shared.css"></style>
<style scoped src="./mergerequest.css"></style>
<style scoped>
.card-header-text {
  display: inline-flex;
  align-items: center;
  gap: var(--space-2);
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

.empty {
  font-size: 0.8rem;
}
</style>
