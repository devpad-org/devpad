<script setup lang="ts">
import { computed, inject, onMounted, onUnmounted, ref, watch } from 'vue'
import { gitApi } from '@/api/git'
import { projectApi } from '@/api/projects'
import { mergeRequestApi } from '@/api/mergeRequests'
import { useActiveProjectStore } from '@/stores/activeProject'
import { branchActionError } from './branchErrors'
import { confirmDiscardKey, useOpenMergeRequest } from '@/components/ide/viewTabs'

const REFRESH_INTERVAL_MS = 10000

// Moves work between the project workspace and the project's canonical repo:
// switch to a project branch, publish the current one, sync the default
// branch, and open a merge request.
const props = defineProps<{
  workspaceId: number
  // The Source Control panel is showing, so keep the branch state fresh.
  active: boolean
  // Bumped by the shell whenever the workspace's branch may have moved.
  revision: number
}>()

// A checkout moves the workspace's HEAD; publish and sync only move refs.
const emit = defineEmits<{ changed: [kind: 'checkout' | 'publish' | 'sync'] }>()

const store = useActiveProjectStore()
const openMergeRequest = useOpenMergeRequest()
const confirmDiscard = inject(confirmDiscardKey, () => Promise.resolve(true))

const current = ref('')
const localSha = ref('')
const target = ref('')
const busy = ref<'' | 'checkout' | 'publish' | 'sync' | 'mr'>('')
const error = ref('')
const notice = ref('')
const showMrForm = ref(false)
const mrTitle = ref('')

let timer: ReturnType<typeof setInterval> | null = null

const defaultBranch = computed(() => store.project?.defaultBranch ?? 'main')
const detached = computed(() => current.value === '' || current.value === 'HEAD')
const onDefault = computed(() => current.value === defaultBranch.value)
const published = computed(() => store.branches.find((b) => b.name === current.value) ?? null)
const branchState = computed(() => {
  if (detached.value) return 'detached'
  if (onDefault.value) return 'default'
  if (!published.value) return 'unpublished'
  // The workspace reports abbreviated hashes, the canonical repo full ones.
  return localSha.value && published.value.sha.startsWith(localSha.value) ? 'published' : 'changed'
})
const openMr = computed(() => (current.value ? store.openMergeRequestFor(current.value) : undefined))
const switchable = computed(() => store.branches.filter((b) => b.name !== current.value))

const stateLabel = computed(() => {
  switch (branchState.value) {
    case 'detached': return 'Detached HEAD'
    case 'default': return 'Default branch. Changes land here through merge requests.'
    case 'unpublished': return 'Not published yet'
    case 'changed': return 'Local commits not published'
    default: return 'Published'
  }
})

async function loadLocal() {
  const res = await gitApi.branches(props.workspaceId)
  current.value = res.current
  localSha.value = res.branches.find((b) => !b.remote && b.name === res.current)?.hash ?? ''
}

async function refresh() {
  try {
    await Promise.all([loadLocal(), store.refreshBranches()])
  } catch {
    // The status bar and Source Control panel report git errors.
  }
}

function changed(kind: 'checkout' | 'publish' | 'sync') {
  emit('changed', kind)
  void refresh()
}

function begin(action: typeof busy.value) {
  busy.value = action
  error.value = ''
  notice.value = ''
}

async function checkout() {
  if (store.projectId === null || !target.value) return
  if (!(await confirmDiscard('Switching branches reloads your open files from the new branch, so their unsaved changes will be lost.'))) return
  begin('checkout')
  try {
    const res = await projectApi.checkoutBranch(store.projectId, target.value)
    notice.value = res.created || res.sha === res.upstreamSha
      ? `Switched to ${res.branch}.`
      : `Switched to ${res.branch}. It differs from the project's copy; merge ${res.upstream} to pick up the latest.`
    target.value = ''
    changed('checkout')
  } catch (e) {
    error.value = branchActionError(e, 'Failed to switch branches')
  } finally {
    busy.value = ''
  }
}

async function publishCurrent(): Promise<boolean> {
  if (store.projectId === null) return false
  const res = await projectApi.publishBranch(store.projectId, current.value)
  notice.value = `Published ${res.branch} (${res.sha.slice(0, 8)}).`
  return true
}

async function publish() {
  begin('publish')
  try {
    await publishCurrent()
    changed('publish')
    void store.refresh()
  } catch (e) {
    error.value = branchActionError(e, 'Failed to publish the branch')
  } finally {
    busy.value = ''
  }
}

async function sync() {
  if (store.projectId === null) return
  begin('sync')
  try {
    const res = await projectApi.syncWorkspace(store.projectId)
    notice.value = `Fetched ${defaultBranch.value} (${res.sha.slice(0, 8)}) as ${res.branch}. Merge it to bring your branch up to date.`
    changed('sync')
  } catch (e) {
    error.value = branchActionError(e, 'Failed to sync')
  } finally {
    busy.value = ''
  }
}

function startMr() {
  mrTitle.value = current.value.replace(/^[^/]+\//, '').replace(/[-_]+/g, ' ')
  showMrForm.value = true
}

// Publishes the branch if it has unpublished commits, then opens a merge
// request for it.
async function createMr() {
  if (store.projectId === null || !mrTitle.value.trim()) return
  begin('mr')
  try {
    if (branchState.value !== 'published') await publishCurrent()
    const res = await mergeRequestApi.create(store.projectId, {
      title: mrTitle.value.trim(),
      description: '',
      sourceBranch: current.value,
    })
    showMrForm.value = false
    void store.refresh()
    openMergeRequest(store.projectId, res.mergeRequest.id)
  } catch (e) {
    error.value = branchActionError(e, 'Failed to open the merge request')
    void refresh()
  } finally {
    busy.value = ''
  }
}

function startTimer() {
  stopTimer()
  timer = setInterval(() => {
    if (props.active && document.visibilityState === 'visible' && !busy.value) void refresh()
  }, REFRESH_INTERVAL_MS)
}

function stopTimer() {
  if (timer !== null) {
    clearInterval(timer)
    timer = null
  }
}

watch(() => props.active, (active) => {
  if (active) void refresh()
})
watch(() => props.revision, () => void refresh())

onMounted(() => {
  void refresh()
  startTimer()
})
onUnmounted(stopTimer)
</script>

<template>
  <div class="branch-actions">
    <div class="branch-head">
      <span class="branch-label">Project</span>
      <span class="branch-name mono" :title="current">{{ detached ? 'detached' : current }}</span>
    </div>
    <p class="branch-state" :class="`state-${branchState}`">{{ stateLabel }}</p>

    <div class="branch-row">
      <select v-model="target" class="branch-select" aria-label="Project branch" :disabled="!!busy">
        <option value="" disabled>Switch to a project branch…</option>
        <option v-for="b in switchable" :key="b.name" :value="b.name">{{ b.name }}</option>
      </select>
      <button class="btn btn-secondary btn-sm" :disabled="!target || !!busy" @click="checkout">
        {{ busy === 'checkout' ? 'Switching…' : 'Switch' }}
      </button>
    </div>

    <div class="branch-row">
      <button
        class="btn btn-secondary btn-sm"
        :disabled="!!busy || detached || onDefault || branchState === 'published'"
        :title="onDefault ? 'The default branch changes only through merge requests' : 'Publish this branch to the project'"
        @click="publish"
      >
        {{ busy === 'publish' ? 'Publishing…' : 'Publish' }}
      </button>
      <button
        class="btn btn-secondary btn-sm"
        :disabled="!!busy"
        :title="`Fetch the project's ${defaultBranch} as devpad/${defaultBranch}`"
        @click="sync"
      >
        {{ busy === 'sync' ? 'Syncing…' : `Sync ${defaultBranch}` }}
      </button>
      <button
        v-if="openMr"
        class="btn btn-secondary btn-sm"
        @click="openMergeRequest(openMr.projectId, openMr.id)"
      >
        MR !{{ openMr.id }}
      </button>
      <button
        v-else
        class="btn btn-secondary btn-sm"
        :disabled="!!busy || detached || onDefault"
        @click="startMr"
      >
        Merge request
      </button>
    </div>

    <form v-if="showMrForm && !openMr" class="mr-form" @submit.prevent="createMr">
      <input v-model="mrTitle" class="form-input" type="text" aria-label="Merge request title" placeholder="Title" />
      <div class="branch-row">
        <button type="button" class="btn btn-secondary btn-sm" @click="showMrForm = false">Cancel</button>
        <button type="submit" class="btn btn-primary btn-sm" :disabled="!!busy || !mrTitle.trim()">
          {{ busy === 'mr' ? 'Opening…' : branchState === 'published' ? 'Open' : 'Publish and open' }}
        </button>
      </div>
    </form>

    <p v-if="error" class="notice notice-error">{{ error }}</p>
    <p v-if="notice" class="notice notice-success">{{ notice }}</p>
  </div>
</template>

<style scoped src="./shared.css"></style>
<style scoped>
.branch-actions {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
  padding: var(--space-2) var(--space-3);
  border-bottom: 0.5px solid var(--border-default);
  background: var(--ide-panel-bg);
  flex-shrink: 0;
}

.branch-head {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  min-width: 0;
}

.branch-label {
  font-size: 0.7rem;
  font-weight: 600;
  text-transform: uppercase;
  letter-spacing: 0.05em;
  color: var(--text-muted);
}

.branch-name {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  color: var(--text-primary);
}

.branch-state {
  font-size: 0.72rem;
  color: var(--text-muted);
}

.state-changed,
.state-unpublished {
  color: var(--accent-amber);
}

.state-published {
  color: var(--accent-green);
}

.branch-row {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: var(--space-1);
}

.branch-select {
  flex: 1;
  min-width: 0;
  padding: var(--space-1) var(--space-2);
  background: var(--bg-primary);
  border: 0.5px solid var(--border-default);
  border-radius: var(--radius-md);
  color: var(--text-primary);
  font-size: 0.75rem;
}

.mr-form {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
}

.mr-form .form-input {
  font-size: 0.78rem;
  padding: var(--space-1) var(--space-2);
}

.mr-form .branch-row {
  justify-content: flex-end;
}

.notice {
  margin-bottom: 0;
}
</style>
