<script setup lang="ts">
import { computed } from 'vue'
import MrIcon from '@/components/project/MrIcon.vue'
import { plural } from './format'
import { useMergeRequest } from './useMergeRequest'

// Offers to publish commits the project workspace has on the merge request's
// branch, so addressing a review doesn't mean finding the Publish button in
// Source Control.
const { mr, busy, unpublishedCommits, workspaceDiverged, workspaceBranch, latestAgentReview, pendingAgentReview, taskWorking, publishWorkspaceCommits } = useMergeRequest()

const canRereview = computed(() => Boolean(latestAgentReview.value) && !pendingAgentReview.value && !taskWorking.value)
</script>

<template>
  <div v-if="mr && unpublishedCommits > 0" class="notice notice-warning publish-banner">
    <MrIcon name="branch" :size="14" class="banner-icon" />
    <p v-if="workspaceDiverged" class="banner-text">
      Your workspace's <span class="mono">{{ mr.sourceBranch }}</span> has {{ plural(unpublishedCommits, 'commit') }} this merge request
      doesn't, but is missing {{ plural(workspaceBranch?.behind ?? 0, 'commit') }} it does. Publishing only fast-forwards: merge
      <span class="mono">devpad/{{ mr.sourceBranch }}</span> into your branch first.
    </p>
    <template v-else>
      <p class="banner-text">
        Your workspace has {{ plural(unpublishedCommits, 'commit') }} on <span class="mono">{{ mr.sourceBranch }}</span> that this merge
        request doesn't have yet.
      </p>
      <div class="banner-actions">
        <button
          v-if="canRereview"
          class="btn btn-secondary btn-sm"
          :disabled="busy"
          :title="`Publish, then review the new commit with ${latestAgentReview?.model}`"
          @click="publishWorkspaceCommits(true)"
        >
          Publish and re-review
        </button>
        <button class="btn btn-primary btn-sm" :disabled="busy" @click="publishWorkspaceCommits(false)">Publish</button>
      </div>
    </template>
  </div>
</template>

<style scoped src="@/components/project/shared.css"></style>
<style scoped src="./mergerequest.css"></style>
<style scoped>
.publish-banner {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: var(--space-2) var(--space-3);
}

.banner-icon {
  flex-shrink: 0;
}

.banner-text {
  flex: 1;
  min-width: 200px;
  margin: 0;
}

.banner-actions {
  display: flex;
  gap: var(--space-2);
  margin-left: auto;
}
</style>
