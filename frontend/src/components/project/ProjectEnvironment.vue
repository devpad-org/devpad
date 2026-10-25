<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { projectApi, type ProjectEnvironment } from '@/api/projects'
import { useActiveProjectStore } from '@/stores/activeProject'
import ConfirmModal from '@/components/ConfirmModal.vue'

const POLL_INTERVAL_MS = 3000

// How the project's environment build stands, and a way to move the project
// workspace onto it.
const props = defineProps<{
  // The project workspace's status, or 'none' before it is created.
  workspaceStatus: string
}>()

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

const statusLabel = computed(() => {
  const e = env.value
  if (!e) return ''
  const b = e.build
  if (!b) return 'Not built yet'
  switch (b.status) {
    case 'building': return 'Building…'
    case 'succeeded': return e.current ? 'Built' : 'Out of date'
    default: return b.error ? `Build failed: ${b.error}` : 'Build failed'
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
  <div v-if="loaded && (env || error)" class="side-section">
    <div class="side-section-title">Environment</div>
    <template v-if="env?.build || store.project?.environment.trim()">
      <p class="env-status" :class="env?.build ? `env-${env.build.status}` : ''">{{ statusLabel }}</p>
      <p v-if="fallbackNote" class="env-note">{{ fallbackNote }}</p>
    </template>
    <p v-else class="env-note">The project no longer has an environment.</p>

    <template v-if="env?.workspaceStale && hasWorkspace">
      <p class="env-note env-stale">Your workspace was made from {{ env.custom ? 'an older version of the environment' : 'a different image' }}.</p>
    </template>

    <div class="env-actions">
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
    </div>
    <pre v-if="showLog && env?.build?.log" class="env-log">{{ env.build.log }}</pre>
    <p v-if="error" class="notice notice-error env-message">{{ error }}</p>
    <p v-if="notice" class="notice notice-success env-message">{{ notice }}</p>

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
  </div>
</template>

<style scoped src="./shared.css"></style>
<style scoped src="./panel.css"></style>
<style scoped>
.env-status {
  font-size: 0.75rem;
  color: var(--text-secondary);
  overflow-wrap: anywhere;
}

.env-building {
  color: var(--accent-amber);
}

.env-succeeded {
  color: var(--accent-green);
}

.env-failed {
  color: var(--accent-rose);
}

.env-note {
  margin-top: var(--space-1);
  font-size: 0.72rem;
  color: var(--text-muted);
}

.env-stale {
  color: var(--accent-amber);
}

.env-actions {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-1);
  margin-top: var(--space-2);
}

.env-log {
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

.env-message {
  margin: var(--space-2) 0 0;
}
</style>
