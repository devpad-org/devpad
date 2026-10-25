<script setup lang="ts">
import { computed, ref } from 'vue'
import { useActiveProjectStore } from '@/stores/activeProject'
import StatusBadge from './StatusBadge.vue'

// The project's merge requests in the IDE side bar.
const store = useActiveProjectStore()
const showClosed = ref(false)

const visible = computed(() => (showClosed.value ? store.mergeRequests : store.openMergeRequests))
</script>

<template>
  <div class="side-panel mr-panel">
    <div class="side-header">
      <span class="side-title">
        <svg class="panel-icon" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
          <circle cx="18" cy="18" r="3" />
          <circle cx="6" cy="6" r="3" />
          <path d="M13 6h3a2 2 0 0 1 2 2v7" />
          <path d="M6 9v12" />
        </svg>
        Merge requests
      </span>
      <label class="form-check closed-toggle" title="Show merged and closed">
        <input v-model="showClosed" type="checkbox" />
        All
      </label>
    </div>

    <div class="side-body">
      <div class="side-section">
        <RouterLink
          v-for="mr in visible"
          :key="mr.id"
          :to="{ name: 'merge-request', params: { id: mr.projectId, mrId: mr.id } }"
          class="side-row"
        >
          <span class="side-row-main">
            <span class="side-row-title">!{{ mr.id }} {{ mr.title }}</span>
            <span class="side-row-meta">
              <span class="mono">{{ mr.sourceBranch }} &rarr; {{ mr.targetBranch }}</span>
            </span>
          </span>
          <StatusBadge :status="mr.status" />
        </RouterLink>
        <p v-if="visible.length === 0" class="side-empty">
          {{ showClosed ? 'No merge requests yet.' : 'No open merge requests.' }}
        </p>
      </div>
    </div>
  </div>
</template>

<style scoped src="./shared.css"></style>
<style scoped src="./panel.css"></style>
<style scoped>
.mr-panel {
  --ide-header-icon: var(--accent-blue);
}

.closed-toggle {
  font-size: 0.72rem;
}
</style>
