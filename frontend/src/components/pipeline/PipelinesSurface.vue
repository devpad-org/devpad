<script setup lang="ts">
import MrIcon from '@/components/project/MrIcon.vue'
import { computed, onMounted, onUnmounted, ref, toRef, watch } from 'vue'
import { pipelineApi, type Pipeline, type PipelineListFilter } from '@/api/pipelines'
import DocLink from '@/components/DocLink.vue'
import SelectMenu from '@/components/SelectMenu.vue'
import { usePipelineList } from '@/composables/usePipelines'
import { useActiveProjectStore } from '@/stores/activeProject'
import { PIPELINE_COPY } from './pipelineFormat'
import PipelineList from './PipelineList.vue'
import PipelineView from './PipelineView.vue'

// The project's pipelines on a surface of their own: the list, newest
// first, beside the one picked, with a way to run a protected branch's
// pipeline by hand.
const props = defineProps<{
  // The surface is in view: lists and output are read again only then.
  active: boolean
}>()

const store = useActiveProjectStore()

// '' is every pipeline; otherwise one protected branch's.
const branchFilter = ref('')
const filter = computed<PipelineListFilter>(() => (branchFilter.value ? { branch: branchFilter.value } : {}))
const projectId = computed(() => (store.ready ? store.projectId : null))
const list = usePipelineList(projectId, filter, toRef(props, 'active'))
const { pipelines, loaded, error, hasMore, loadingMore } = list

const selectedId = ref<number | null>(null)
// On a narrow surface the list and the pipeline take turns, and the
// pipeline is read only while it shows.
const showingDetail = ref(false)
const NARROW_PX = 760
const root = ref<HTMLElement | null>(null)
const narrow = ref(false)
const detailActive = computed(() => props.active && (!narrow.value || showingDetail.value))
let resize: ResizeObserver | null = null
onMounted(() => {
  if (!root.value) return
  resize = new ResizeObserver(([entry]) => (narrow.value = entry.contentRect.width <= NARROW_PX))
  resize.observe(root.value)
})
onUnmounted(() => resize?.disconnect())

const runBranch = ref('')
const starting = ref(false)
const startError = ref('')

watch(
  () => store.protectedBranches,
  (branches) => {
    if (!branches.includes(runBranch.value)) runBranch.value = branches[0] ?? ''
    if (branchFilter.value && !branches.includes(branchFilter.value)) branchFilter.value = ''
  },
  { immediate: true },
)

watch(() => store.projectId, () => {
  selectedId.value = null
  showingDetail.value = false
})

// The newest shows until one is picked.
watch(pipelines, (list) => {
  if (selectedId.value === null && list.length > 0) selectedId.value = list[0].id
})

const running = computed(() => pipelines.value.filter((p) => p.status === 'running' || p.status === 'queued').length)

function select(id: number) {
  selectedId.value = id
  showingDetail.value = true
}

function started(p: Pipeline) {
  if (!branchFilter.value || p.branch === branchFilter.value) list.upsert(p)
  selectedId.value = p.id
  showingDetail.value = true
  void store.refreshLatestPipelines()
}

async function run() {
  const id = store.projectId
  if (id === null || !runBranch.value || starting.value) return
  starting.value = true
  startError.value = ''
  try {
    const res = await pipelineApi.start(id, { branch: runBranch.value })
    started(res.pipeline)
  } catch (e) {
    startError.value = e instanceof Error ? e.message : 'Failed to start the pipeline'
  } finally {
    starting.value = false
  }
}
</script>

<template>
  <section ref="root" class="list-surface pipelines-surface" aria-label="Pipelines">
    <header class="list-header">
      <div class="list-title">
        <span class="list-title-icon">
          <MrIcon name="pipeline" :size="16" :stroke-width="1.8" />
        </span>
        <span class="list-title-text">Pipelines</span>
        <DocLink topic="pipelines" label="Pipelines" />
        <span v-if="store.ready && loaded" class="list-summary">
          <span v-if="running" class="summary-running">{{ running }} running or queued</span>
          <span v-else>none running</span>
        </span>
      </div>
      <div v-if="store.ready" class="run-controls">
        <SelectMenu
          v-model="runBranch"
          :options="store.protectedBranches.map((b) => ({ value: b, label: b }))"
          title="Protected branch to run"
          mono
          :disabled="starting"
        />
        <button class="new-btn" type="button" :disabled="starting || !runBranch" :title="`Run ${runBranch}'s pipeline at its current commit`" @click="run">
          <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.4" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
            <path d="m6 4 14 8-14 8Z" />
          </svg>
          {{ starting ? 'Starting…' : 'Run pipeline' }}
        </button>
      </div>
    </header>

    <p v-if="!store.ready" class="list-empty">Pipelines appear once the project repository is ready.</p>

    <template v-else>
      <div class="list-toolbar">
        <div class="filters" role="group" aria-label="Show pipelines">
          <button type="button" class="filter" :class="{ active: branchFilter === '' }" :aria-pressed="branchFilter === ''" @click="branchFilter = ''">All</button>
          <button
            v-for="b in store.protectedBranches"
            :key="b"
            type="button"
            class="filter mono"
            :class="{ active: branchFilter === b }"
            :aria-pressed="branchFilter === b"
            @click="branchFilter = b"
          >
            {{ b }}
          </button>
        </div>
        <span class="toolbar-hint">{{ PIPELINE_COPY.startedByHand }}</span>
      </div>
      <p v-if="startError" class="notice notice-error surface-notice">{{ startError }}</p>
      <p v-if="error" class="notice notice-error surface-notice">{{ error }}</p>

      <div class="split" :class="{ 'show-detail': showingDetail }">
        <div class="split-list">
          <PipelineList v-if="pipelines.length" :pipelines="pipelines" :selected-id="selectedId" @select="select" />
          <p v-else-if="loaded" class="list-empty">
            {{ branchFilter ? `No pipelines of ${branchFilter} yet.` : 'No pipelines yet. Pick a protected branch and press Run pipeline.' }}
          </p>
          <p v-else class="list-empty">Loading pipelines...</p>
          <div v-if="hasMore" class="more">
            <button type="button" class="btn btn-secondary btn-sm" :disabled="loadingMore" @click="list.loadMore">
              {{ loadingMore ? 'Loading…' : 'Older pipelines' }}
            </button>
          </div>
        </div>
        <div class="split-detail">
          <button type="button" class="back" @click="showingDetail = false">← All pipelines</button>
          <PipelineView
            v-if="selectedId !== null && store.projectId !== null"
            :key="selectedId"
            :project-id="store.projectId"
            :pipeline-id="selectedId"
            :active="detailActive"
            @started="started"
          />
          <p v-else class="list-empty">Pick a pipeline to see its jobs.</p>
        </div>
      </div>
    </template>
  </section>
</template>

<style scoped src="@/components/project/listTable.css"></style>
<style scoped src="@/components/project/shared.css"></style>
<style scoped>
.pipelines-surface {
  --ide-header-icon: var(--hue-green);
}

.summary-running {
  color: var(--hue-blue);
}

.run-controls {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  flex-shrink: 0;
}

.toolbar-hint {
  font-size: 0.72rem;
  color: var(--text-tertiary);
}

.surface-notice {
  margin: var(--space-2) var(--space-3) 0;
}

.split {
  display: grid;
  grid-template-columns: minmax(260px, 380px) minmax(0, 1fr);
  flex: 1;
  min-height: 0;
}

.split-list {
  min-height: 0;
  overflow-y: auto;
  border-right: 0.5px solid var(--border-subtle);
}

.split-detail {
  min-height: 0;
  overflow-y: auto;
  padding: var(--space-4) var(--space-5) var(--space-6);
}

.more {
  display: flex;
  justify-content: center;
  padding: var(--space-3);
}

.back {
  display: none;
  margin-bottom: var(--space-3);
  font-size: 0.78rem;
  color: var(--text-secondary);
}

.back:hover {
  color: var(--text-primary);
}

/* The header wraps rather than overflowing a phone's width. */
.pipelines-surface .list-header {
  height: auto;
  min-height: 38px;
  flex-wrap: wrap;
  row-gap: var(--space-1);
  padding-top: 4px;
  padding-bottom: 4px;
}

@container (max-width: 520px) {
  .pipelines-surface .list-summary {
    display: none;
  }

  .run-controls {
    flex: 1 1 100%;
    justify-content: flex-end;
    min-width: 0;
  }
}

/* A narrow surface shows the list, or the pipeline picked from it; this
   width is NARROW_PX. */
@container (max-width: 760px) {
  .split {
    grid-template-columns: minmax(0, 1fr);
  }

  .split-list {
    border-right: none;
  }

  .split .split-detail,
  .split.show-detail .split-list {
    display: none;
  }

  .split.show-detail .split-detail {
    display: block;
    padding: var(--space-3) var(--space-4) var(--space-6);
  }

  .back {
    display: inline-block;
  }

  .toolbar-hint {
    display: none;
  }
}
</style>
