<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { projectApi, type WorkspaceSetup } from '@/api/projects'
import { useActiveProjectStore } from '@/stores/activeProject'

const POLL_INTERVAL_MS = 3000

// How the project's setup script went in the project workspace, with its
// output, and a way to run it again.
const props = defineProps<{
  // The project workspace's status, or 'none' before it is created.
  workspaceStatus: string
}>()

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

const statusLabel = computed(() => {
  const s = setup.value
  if (!s) return props.workspaceStatus === 'none' ? 'Runs when the workspace is created' : 'Not run'
  switch (s.status) {
    case 'running': return 'Running…'
    case 'succeeded': return 'Succeeded'
    default:
      if (s.error) return `Failed: ${s.error}`
      return s.exitCode !== null ? `Failed with exit status ${s.exitCode}` : 'Failed'
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
  const polling = setup.value?.status === 'running'
  if (polling && timer === null) {
    timer = setInterval(() => {
      if (document.visibilityState === 'visible') void load()
    }, POLL_INTERVAL_MS)
  } else if (!polling && timer !== null) {
    clearInterval(timer)
    timer = null
  }
}

watch(() => setup.value?.status, syncTimer)
watch(() => [store.projectId, props.workspaceStatus], () => void load())

onMounted(() => void load())
onUnmounted(() => {
  if (timer !== null) clearInterval(timer)
})
</script>

<template>
  <div v-if="loaded && (hasScript || setup)" class="side-section">
    <div class="side-section-title">Setup</div>
    <p class="setup-status" :class="setup ? `setup-${setup.status}` : ''">{{ statusLabel }}</p>
    <p v-if="changed" class="setup-note">The setup script has changed since this run.</p>
    <p v-if="!hasScript && setup" class="setup-note">The project no longer has a setup script.</p>

    <div class="setup-actions">
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
    </div>
    <pre v-if="showOutput && setup?.output" class="setup-output">{{ setup.output }}</pre>
    <p v-if="error" class="notice notice-error setup-error">{{ error }}</p>
  </div>
</template>

<style scoped src="./shared.css"></style>
<style scoped src="./panel.css"></style>
<style scoped>
.setup-status {
  font-size: 0.75rem;
  color: var(--text-secondary);
  overflow-wrap: anywhere;
}

.setup-running {
  color: var(--accent-amber);
}

.setup-succeeded {
  color: var(--accent-green);
}

.setup-failed {
  color: var(--accent-rose);
}

.setup-note {
  margin-top: var(--space-1);
  font-size: 0.72rem;
  color: var(--text-muted);
}

.setup-actions {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-1);
  margin-top: var(--space-2);
}

.setup-output {
  margin-top: var(--space-2);
  max-height: 240px;
  overflow: auto;
  padding: var(--space-2);
  background: var(--bg-primary);
  border: 0.5px solid var(--border-default);
  border-radius: var(--radius-md);
  font-family: var(--font-mono);
  font-size: 0.7rem;
  line-height: 1.4;
  color: var(--text-secondary);
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}

.setup-error {
  margin: var(--space-2) 0 0;
}
</style>
