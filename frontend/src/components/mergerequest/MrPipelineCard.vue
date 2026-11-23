<script setup lang="ts">
import DocLink from '@/components/DocLink.vue'
import MrIcon from '@/components/project/MrIcon.vue'
import PipelineView from '@/components/pipeline/PipelineView.vue'
import { shortSha } from './format'
import { useMergeRequest } from './useMergeRequest'

// The merge request's pipeline: its latest, or one picked from the
// activity, with its jobs, where they came from and their output, and a way
// to run it by hand. Its jobs come from the target branch, never the change.
const { mr, projectId, isOpen, pipelines } = useMergeRequest()
const { latest, latestStale, shownId, pickedId, loaded, starting, startError } = pipelines

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
        <button v-if="pickedId !== null" type="button" class="btn btn-secondary btn-sm" @click="pipelines.show(null)">Show the latest</button>
        <button
          v-if="isOpen"
          type="button"
          class="btn btn-secondary btn-sm"
          :disabled="starting"
          :title="`Run the pipeline of ${mr.targetBranch} on this branch merged onto it`"
          @click="pipelines.start"
        >
          <MrIcon name="play" :size="12" />
          {{ starting ? 'Starting…' : latest ? 'Run pipeline again' : 'Run pipeline' }}
        </button>
      </span>
    </div>
    <div class="card-body">
      <p v-if="startError" class="notice notice-error">{{ startError }}</p>
      <p v-if="latestStale && pickedId === null && latest" class="notice notice-warning">
        This pipeline tested <span class="mono">{{ shortSha(latest.headSha) }}</span>; the branch is at <span class="mono">{{ shortSha(mr.headSha) }}</span> now.
      </p>
      <PipelineView v-if="shownId !== null" :key="shownId" :project-id="projectId" :pipeline-id="shownId" :active="true" compact @started="started" />
      <p v-else-if="loaded" class="muted empty">
        No pipeline has run on this merge request. Its jobs come from <span class="mono">.devpad/pipeline.yml</span> on
        <span class="mono">{{ mr.targetBranch }}</span>, or the project's pipeline setting, and test this branch merged onto it. A pipeline's result doesn't block merging yet.
      </p>
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
