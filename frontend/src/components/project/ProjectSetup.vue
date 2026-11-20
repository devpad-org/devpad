<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { projectApi, type WorkspaceSetup } from '@/api/projects'
import { useActiveProjectStore } from '@/stores/activeProject'
import ProjectCard from './ProjectCard.vue'
import type { CardStatus } from './card'

const POLL_INTERVAL_MS = 3000

// How the project's setup script went in the project workspace, with its
// output, and a way to run it again.
const props = defineProps<{
  // The project workspace's status, or 'none' before it is created.
  workspaceStatus: string
  step?: number
}>()

const emit = defineEmits<{ edit: [] }>()

const store = useActiveProjectStore()

const setup = ref<WorkspaceSetup | null>(null)
const loaded = ref(false)
const running = ref(false)
const error = ref('')
const showOutput = ref(false)

let timer: ReturnType<typeof setInterval> | null = null
// Bumped for each load, so a slow response can't overwrite a newer one.
let seq = 0

const script = computed(() => store.project?.setupScript ?? '')
const hasScript = computed(() => script.value.trim() !== '')
const workspaceRunning = computed(() => props.workspaceStatus === 'running')
const changed = computed(() => setup.value !== null && setup.value.script !== script.value)
const canRun = computed(() => hasScript.value && workspaceRunning.value && store.ready && !running.value && setup.value?.status !== 'running')

const status = computed<CardStatus>(() => {
  const s = setup.value
  if (!s) return hasScript.value ? { label: 'Not run', tone: 'neutral' } : { label: 'No script', tone: 'neutral' }
  switch (s.status) {
    case 'pending': return { label: workspaceRunning.value ? 'Waiting…' : 'Pending', tone: 'busy' }
    case 'running': return { label: 'Running…', tone: 'busy' }
    case 'succeeded': return { label: 'Succeeded', tone: 'ok' }
    default: return { label: 'Failed', tone: 'error' }
  }
})

// What the status pill can't say.
const detail = computed(() => {
  const s = setup.value
  if (!s) {
    if (!hasScript.value) return ''
    return props.workspaceStatus === 'none' ? 'Runs when the workspace is created.' : ''
  }
  switch (s.status) {
    case 'pending': return workspaceRunning.value ? 'Waiting for the services to be ready…' : 'Runs once the workspace is started.'
    case 'failed':
      if (s.error) return `Failed: ${s.error}`
      return s.exitCode !== null ? `Failed with exit status ${s.exitCode}.` : ''
    default: return ''
  }
})

async function load() {
  const id = store.projectId
  if (id === null) return
  const mine = ++seq
  try {
    const res = await projectApi.workspaceSetup(id)
    if (mine !== seq) return
    setup.value = res
    loaded.value = true
    error.value = ''
  } catch (e) {
    if (mine !== seq) return
    // Show the section anyway, so the error and the run button are reachable.
    loaded.value = true
    error.value = e instanceof Error ? e.message : 'Failed to load the setup run'
  }
}

async function run() {
  const id = store.projectId
  if (id === null) return
  running.value = true
  error.value = ''
  try {
    const res = await projectApi.runWorkspaceSetup(id)
    seq++
    setup.value = res.setup
  } catch (e) {
    error.value = e instanceof Error ? e.message : 'Failed to run the setup script'
  } finally {
    running.value = false
  }
}

function syncTimer() {
  // A pending run starts on its own once the workspace's services are ready.
  const status = setup.value?.status
  const polling = status === 'running' || (status === 'pending' && workspaceRunning.value)
  if (polling && timer === null) {
    timer = setInterval(() => {
      if (document.visibilityState === 'visible') void load()
    }, POLL_INTERVAL_MS)
  } else if (!polling && timer !== null) {
    clearInterval(timer)
    timer = null
  }
}

watch(() => [setup.value?.status, workspaceRunning.value], syncTimer)
watch(() => [store.projectId, props.workspaceStatus], () => void load())

onMounted(() => void load())
onUnmounted(() => {
  if (timer !== null) clearInterval(timer)
})
</script>

<template>
  <ProjectCard
    title="Setup script"
    doc="setupScript"
    doc-label="Setup scripts"
    :step="step"
    :status="loaded ? status.label : ''"
    :tone="status.tone"
  >
    <template #icon>
      <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round">
        <path d="m4 17 6-6-6-6" />
        <path d="M12 19h8" />
      </svg>
    </template>

    <template v-if="hasScript">
      <p class="card-lead">Runs once in each new workspace, after its services are ready:</p>
      <pre class="card-code">{{ script.trim() }}</pre>
    </template>
    <p v-else-if="setup" class="card-lead">The project no longer has a setup script.</p>
    <p v-else class="card-lead">
      A shell script that runs once in each new workspace and sandbox, such as installing dependencies or loading
      a database, so agents start from a working checkout.
    </p>

    <p v-if="detail" :class="setup?.status === 'failed' ? 'card-error' : 'card-note'">{{ detail }}</p>
    <p v-if="changed && hasScript" class="card-note">The setup script has changed since this run.</p>

    <pre v-if="showOutput && setup?.output" class="card-code card-log">{{ setup.output }}</pre>
    <p v-if="error" class="notice notice-error card-message">{{ error }}</p>

    <template #actions>
      <button
        v-if="hasScript"
        class="btn btn-secondary btn-sm"
        :disabled="!canRun"
        :title="workspaceRunning ? 'Run the project\'s setup script in the workspace' : 'Start the workspace to run setup'"
        @click="run"
      >
        {{ running ? 'Starting…' : setup ? 'Run again' : 'Run setup' }}
      </button>
      <button v-if="setup?.output" class="btn btn-secondary btn-sm" @click="showOutput = !showOutput">
        {{ showOutput ? 'Hide output' : 'Show output' }}
      </button>
      <button class="btn btn-secondary btn-sm" @click="emit('edit')">{{ hasScript ? 'Edit' : 'Add a setup script' }}</button>
    </template>
  </ProjectCard>
</template>

<style scoped src="./shared.css"></style>
<style scoped src="./card.css"></style>
