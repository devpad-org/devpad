<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { projectApi, type ProjectEnvironment } from '@/api/projects'
import { useActiveProjectStore } from '@/stores/activeProject'
import ConfirmModal from '@/components/ConfirmModal.vue'
import ProjectCard from './ProjectCard.vue'
import type { CardStatus } from './card'

const POLL_INTERVAL_MS = 3000

// How the project's environment build stands, and a way to move the project
// workspace onto it.
const props = defineProps<{
  // The project workspace's status, or 'none' before it is created.
  workspaceStatus: string
  step?: number
}>()

const emit = defineEmits<{ edit: [] }>()

const store = useActiveProjectStore()

const env = ref<ProjectEnvironment | null>(null)
const loaded = ref(false)
const busy = ref<'' | 'build' | 'rebuild'>('')
const error = ref('')
const notice = ref('')
const showLog = ref(false)
const confirmRebuild = ref(false)

let timer: ReturnType<typeof setInterval> | null = null
// Bumped for each load, so a slow response can't overwrite a newer one.
let seq = 0

const building = computed(() => env.value?.build?.status === 'building')
const hasWorkspace = computed(() => props.workspaceStatus !== 'none')

const hasEnvironment = computed(() => (store.project?.environment ?? '').trim() !== '')

const status = computed<CardStatus>(() => {
  const e = env.value
  if (!e) return { label: '', tone: 'neutral' }
  const b = e.build
  if (!b) return hasEnvironment.value ? { label: 'Not built yet', tone: 'neutral' } : { label: 'Standard image', tone: 'neutral' }
  switch (b.status) {
    case 'building': return { label: 'Building…', tone: 'busy' }
    case 'succeeded': return e.current ? { label: 'Built', tone: 'ok' } : { label: 'Out of date', tone: 'warn' }
    default: return { label: 'Build failed', tone: 'error' }
  }
})

// What new workspaces get while the latest build isn't the one they use.
const fallbackNote = computed(() => {
  const e = env.value
  if (!e || e.current || !e.build) return ''
  if (e.build.status === 'succeeded') return ''
  return e.custom
    ? 'New workspaces get the last version that built.'
    : "New workspaces get devpad's image without it."
})

async function load() {
  const id = store.projectId
  if (id === null) return
  const mine = ++seq
  try {
    const res = await projectApi.environment(id)
    if (mine !== seq) return
    env.value = res
    error.value = ''
  } catch (e) {
    if (mine !== seq) return
    error.value = e instanceof Error ? e.message : 'Failed to load the environment'
  } finally {
    if (mine === seq) loaded.value = true
  }
}

async function buildAgain() {
  const id = store.projectId
  if (id === null) return
  busy.value = 'build'
  error.value = ''
  notice.value = ''
  try {
    const res = await projectApi.buildEnvironment(id)
    seq++
    env.value = res.environment
  } catch (e) {
    error.value = e instanceof Error ? e.message : 'Failed to start the build'
  } finally {
    busy.value = ''
  }
}

async function rebuildWorkspace() {
  const id = store.projectId
  if (id === null) return
  busy.value = 'rebuild'
  error.value = ''
  notice.value = ''
  try {
    await projectApi.rebuildWorkspace(id)
    confirmRebuild.value = false
    notice.value = 'The workspace was rebuilt.'
    await load()
  } catch (e) {
    confirmRebuild.value = false
    error.value = e instanceof Error ? e.message : 'Failed to rebuild the workspace'
  } finally {
    busy.value = ''
  }
}

function syncTimer() {
  if (building.value && timer === null) {
    timer = setInterval(() => {
      if (document.visibilityState === 'visible') void load()
    }, POLL_INTERVAL_MS)
  } else if (!building.value && timer !== null) {
    clearInterval(timer)
    timer = null
  }
}

watch(building, syncTimer)
// Saving the project can start a build.
watch(() => [store.projectId, store.project?.environment, props.workspaceStatus], () => void load())

onMounted(() => void load())
onUnmounted(() => {
  if (timer !== null) clearInterval(timer)
})
</script>

<template>
  <ProjectCard
    title="Environment"
    doc="environment"
    doc-label="Environments"
    :step="step"
    :status="loaded ? status.label : ''"
    :tone="status.tone"
  >
    <template #icon>
      <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round">
        <path d="M21 8a2 2 0 0 0-1-1.73l-7-4a2 2 0 0 0-2 0l-7 4A2 2 0 0 0 3 8v8a2 2 0 0 0 1 1.73l7 4a2 2 0 0 0 2 0l7-4A2 2 0 0 0 21 16Z" />
      </svg>
    </template>

    <template v-if="hasEnvironment">
      <p class="card-lead">New workspaces are made from devpad's image with this on top:</p>
      <pre class="card-code">{{ store.project?.environment.trim() }}</pre>
    </template>
    <p v-else-if="env?.build" class="card-lead">The project no longer has an environment, so new workspaces get devpad's image as it is.</p>
    <p v-else class="card-lead">
      New workspaces get devpad's image as it is. To install what the project needs, add the body of a Dockerfile
      (<code>RUN</code>, <code>ENV</code> and the like) in the project's settings.
    </p>

    <p v-if="env?.build?.status === 'failed' && env.build.error" class="card-error">{{ env.build.error }}</p>
    <p v-if="fallbackNote" class="card-note">{{ fallbackNote }}</p>
    <p v-if="env?.workspaceStale && hasWorkspace" class="card-warn">
      Your workspace was made from {{ env.custom ? 'an older version of the environment' : 'a different image' }}.
    </p>

    <pre v-if="showLog && env?.build?.log" class="card-code card-log">{{ env.build.log }}</pre>
    <p v-if="error" class="notice notice-error card-message">{{ error }}</p>
    <p v-if="notice" class="notice notice-success card-message">{{ notice }}</p>

    <template #actions>
      <button
        v-if="env?.build?.status === 'failed'"
        class="btn btn-secondary btn-sm"
        :disabled="!!busy"
        @click="buildAgain"
      >
        {{ busy === 'build' ? 'Starting…' : 'Build again' }}
      </button>
      <button
        v-if="env?.workspaceStale && hasWorkspace"
        class="btn btn-secondary btn-sm"
        :disabled="!!busy || building || !store.ready"
        :title="building ? 'Wait for the build to finish' : 'Remake the workspace from the current environment'"
        @click="confirmRebuild = true"
      >
        Rebuild workspace
      </button>
      <button v-if="env?.build?.log" class="btn btn-secondary btn-sm" @click="showLog = !showLog">
        {{ showLog ? 'Hide build log' : 'Show build log' }}
      </button>
      <button class="btn btn-secondary btn-sm" @click="emit('edit')">{{ hasEnvironment ? 'Edit' : 'Add an environment' }}</button>
    </template>

    <ConfirmModal
      :show="confirmRebuild"
      title="Rebuild Workspace"
      message="Rebuilding replaces the workspace's container with one made from the current environment. Everything in /workspace is kept, including your repo and its uncommitted changes. Anything outside it is lost: packages you installed by hand, files in your home directory, and running processes. Terminals reconnect once it's back."
      confirm-label="Rebuild"
      variant="danger"
      :loading="busy === 'rebuild'"
      @confirm="rebuildWorkspace"
      @cancel="confirmRebuild = false"
    />
  </ProjectCard>
</template>

<style scoped src="./shared.css"></style>
<style scoped src="./card.css"></style>
