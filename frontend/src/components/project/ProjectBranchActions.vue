<script setup lang="ts">
import { computed, inject, nextTick, onMounted, onUnmounted, ref, watch } from 'vue'
import { gitApi } from '@/api/git'
import { projectApi, type WorkspaceBranchStatus } from '@/api/projects'
import { mergeRequestApi } from '@/api/mergeRequests'
import { useActiveProjectStore } from '@/stores/activeProject'
import { branchActionError } from './branchErrors'
import { confirmDiscardKey, useOpenMergeRequest, type BranchChange } from '@/components/ide/viewTabs'
import type { GitNotice } from '@/components/ide/gitNotice'

const REFRESH_INTERVAL_MS = 10000
const DEVPAD_REMOTE = 'devpad'

// How the project workspace's branch stands against the project's copy, and
// the one thing to do about it: publish, pull, or start a branch. Below it,
// the branch's merge request and a way to bring in the latest default branch.
const props = defineProps<{
  workspaceId: number
  // The checked-out branch; '' or 'HEAD' when detached.
  branch: string
  // The Source Control panel is showing, so keep the comparison fresh.
  active: boolean
  // Bumped whenever the workspace's branch may have moved.
  revision: number
}>()

const emit = defineEmits<{
  changed: [kind: BranchChange]
  notify: [notice: GitNotice]
  newBranch: []
}>()

const store = useActiveProjectStore()
const openMergeRequest = useOpenMergeRequest()
const confirmDiscard = inject(confirmDiscardKey, () => Promise.resolve(true))

const status = ref<WorkspaceBranchStatus | null>(null)
const statusError = ref('')
const busy = ref<'' | 'publish' | 'pull' | 'update' | 'mr'>('')
const showMrForm = ref(false)
const mrTitle = ref('')
const mrInput = ref<HTMLInputElement | null>(null)

let timer: ReturnType<typeof setInterval> | null = null
// Drops a comparison that finishes after the branch has changed.
let loadSeq = 0

const defaultBranch = computed(() => store.project?.defaultBranch ?? 'main')
const detached = computed(() => props.branch === '' || props.branch === 'HEAD')
const onDefault = computed(() => props.branch === defaultBranch.value)
const openMr = computed(() => (detached.value ? undefined : store.openMergeRequestFor(props.branch)))
const ahead = computed(() => status.value?.ahead ?? 0)
const behind = computed(() => status.value?.behind ?? 0)

type Tone = 'neutral' | 'pending' | 'synced' | 'attention'
type Primary = 'publish' | 'pull' | 'branch' | null

function plural(n: number, word: string): string {
  return `${n} ${word}${n === 1 ? '' : 's'}`
}

// What the card says, and the one action it offers.
const view = computed<{ tone: Tone; title: string; detail: string; primary: Primary }>(() => {
  if (detached.value) {
    return { tone: 'attention', title: 'Not on a branch', detail: 'Create a branch to keep the commits you make here.', primary: 'branch' }
  }
  const s = status.value
  if (!s) {
    return statusError.value
      ? { tone: 'neutral', title: "Couldn't compare with the project", detail: statusError.value, primary: onDefault.value ? 'branch' : 'publish' }
      : { tone: 'neutral', title: 'Comparing with the project…', detail: '', primary: null }
  }
  if (onDefault.value) {
    if (s.behind > 0) {
      return { tone: 'pending', title: `${plural(s.behind, 'new commit')} on the project`, detail: `Pull them into your ${props.branch}.`, primary: 'pull' }
    }
    if (s.ahead > 0) {
      return { tone: 'attention', title: `${plural(s.ahead, 'commit')} only on your ${props.branch}`, detail: `${props.branch} changes only through merge requests. Move them to a new branch.`, primary: 'branch' }
    }
    return { tone: 'neutral', title: `${props.branch} is the default branch`, detail: 'It changes through merge requests. Start a branch to make changes.', primary: 'branch' }
  }
  if (s.sandboxOwned) {
    return {
      tone: 'attention',
      title: 'An agent owns this branch',
      detail: s.behind > 0 ? `It has pushed ${plural(s.behind, 'commit')} you don't have yet.` : "You can follow its work here, but you can't publish to it.",
      primary: s.behind > 0 ? 'pull' : null,
    }
  }
  if (!s.published) {
    return { tone: 'pending', title: 'Only in this workspace', detail: 'Publish it so the project and its agents can see it.', primary: 'publish' }
  }
  if (s.ahead > 0 && s.behind > 0) {
    return { tone: 'attention', title: 'Diverged from the project', detail: `${plural(s.ahead, 'commit')} to publish and ${s.behind} to pull. Pull first.`, primary: 'pull' }
  }
  if (s.ahead > 0) {
    return { tone: 'pending', title: `${plural(s.ahead, 'commit')} to publish`, detail: "The project's copy doesn't have them yet.", primary: 'publish' }
  }
  if (s.behind > 0) {
    return { tone: 'pending', title: `${plural(s.behind, 'new commit')} on the project`, detail: 'Pull them to catch up.', primary: 'pull' }
  }
  return { tone: 'synced', title: 'Up to date with the project', detail: '', primary: null }
})

const canStartMr = computed(() => !detached.value && !onDefault.value && !status.value?.sandboxOwned)

async function load() {
  if (detached.value) {
    status.value = null
    statusError.value = ''
    return
  }
  if (store.projectId === null) return
  const seq = ++loadSeq
  const branch = props.branch
  try {
    const res = await projectApi.workspaceBranchStatus(store.projectId, branch)
    if (seq !== loadSeq) return
    status.value = res
    statusError.value = ''
  } catch (e) {
    if (seq !== loadSeq) return
    status.value = null
    statusError.value = branchActionError(e, 'Failed to compare with the project')
  }
}

function begin(action: typeof busy.value) {
  busy.value = action
}

async function publishCurrent() {
  if (store.projectId === null) return
  const res = await projectApi.publishBranch(store.projectId, props.branch)
  emit('notify', { tone: 'success', text: `Published ${res.branch} to the project (${res.sha.slice(0, 7)})` })
}

async function publish() {
  begin('publish')
  try {
    await publishCurrent()
    emit('changed', 'publish')
    void store.refresh()
  } catch (e) {
    emit('notify', { tone: 'error', text: branchActionError(e, 'Failed to publish the branch') })
  } finally {
    busy.value = ''
    void load()
  }
}

// Merges devpad/<branch> into the local branch; on the default branch that
// fast-forwards it.
async function mergeFromProject(branch: string): Promise<'merged' | 'current' | 'failed'> {
  const result = await gitApi.action(props.workspaceId, 'merge', { branch, remote: DEVPAD_REMOTE })
  const current = result.success && /up to date/i.test(result.output)
  // A merge rewrites files, and a conflicted one leaves markers in them.
  if (!current) emit('changed', 'checkout')
  if (!result.success) {
    emit('notify', {
      tone: 'error',
      text: /conflict/i.test(result.output) ? 'The merge has conflicts. Resolve them, then commit.' : `Couldn't merge ${DEVPAD_REMOTE}/${branch}`,
      detail: result.output || result.error || undefined,
    })
    return 'failed'
  }
  return current ? 'current' : 'merged'
}

async function pull() {
  if (!(await confirmDiscard('Pulling reloads your open files from disk, so their unsaved changes will be lost.'))) return
  begin('pull')
  try {
    const outcome = await mergeFromProject(props.branch)
    if (outcome === 'merged') emit('notify', { tone: 'success', text: `Pulled the project's ${props.branch}` })
    if (outcome === 'current') emit('notify', { tone: 'info', text: `${props.branch} already has the project's commits` })
  } catch (e) {
    emit('notify', { tone: 'error', text: branchActionError(e, 'Failed to pull') })
  } finally {
    busy.value = ''
    void load()
  }
}

// Fetches the project's default branch and merges it into this one.
async function updateFromDefault() {
  if (store.projectId === null) return
  if (!(await confirmDiscard('Merging reloads your open files from disk, so their unsaved changes will be lost.'))) return
  begin('update')
  try {
    await projectApi.syncWorkspace(store.projectId)
    emit('changed', 'sync')
    const outcome = await mergeFromProject(defaultBranch.value)
    if (outcome === 'merged') emit('notify', { tone: 'success', text: `Merged the latest ${defaultBranch.value} into ${props.branch}` })
    if (outcome === 'current') emit('notify', { tone: 'info', text: `${props.branch} already has the latest ${defaultBranch.value}` })
  } catch (e) {
    emit('notify', { tone: 'error', text: branchActionError(e, `Failed to update from ${defaultBranch.value}`) })
  } finally {
    busy.value = ''
    void load()
  }
}

function runPrimary() {
  switch (view.value.primary) {
    case 'publish': return publish()
    case 'pull': return pull()
    case 'branch': return emit('newBranch')
  }
}

function startMr() {
  mrTitle.value = props.branch.replace(/^[^/]+\//, '').replace(/[-_]+/g, ' ')
  showMrForm.value = true
  void nextTick(() => mrInput.value?.select())
}

// Publishes the branch if it has unpublished commits, then opens a merge
// request for it.
async function createMr() {
  if (store.projectId === null || !mrTitle.value.trim()) return
  begin('mr')
  try {
    if (!status.value?.published || ahead.value > 0) await publishCurrent()
    const res = await mergeRequestApi.create(store.projectId, {
      title: mrTitle.value.trim(),
      description: '',
      sourceBranch: props.branch,
    })
    showMrForm.value = false
    void store.refresh()
    openMergeRequest(res.mergeRequest.id)
  } catch (e) {
    emit('notify', { tone: 'error', text: branchActionError(e, 'Failed to open the merge request') })
  } finally {
    busy.value = ''
    void load()
  }
}

const mrSubmitLabel = computed(() => {
  if (busy.value === 'mr') return 'Opening…'
  return status.value?.published && ahead.value === 0 ? 'Open' : 'Publish and open'
})

const primaryLabel = computed(() => {
  switch (view.value.primary) {
    case 'publish': return busy.value === 'publish' ? 'Publishing…' : 'Publish'
    case 'pull': return busy.value === 'pull' ? 'Pulling…' : behind.value > 0 ? `Pull ${behind.value}` : 'Pull'
    case 'branch': return 'New branch'
    default: return ''
  }
})

function startTimer() {
  stopTimer()
  timer = setInterval(() => {
    if (props.active && document.visibilityState === 'visible' && !busy.value) void load()
  }, REFRESH_INTERVAL_MS)
}

function stopTimer() {
  if (timer !== null) {
    clearInterval(timer)
    timer = null
  }
}

watch(() => props.branch, () => {
  status.value = null
  statusError.value = ''
  showMrForm.value = false
  void load()
})
watch(() => props.active, (active) => {
  if (active) void load()
})
watch(() => props.revision, () => void load())

onMounted(() => {
  void load()
  startTimer()
})
onUnmounted(stopTimer)
</script>

<template>
  <section class="project-card" :class="`tone-${view.tone}`" aria-label="Branch on the project">
    <div class="project-state">
      <span class="project-state-icon" aria-hidden="true">
        <svg v-if="view.tone === 'synced'" width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.6" stroke-linecap="round" stroke-linejoin="round">
          <path d="M20 6 9 17l-5-5" />
        </svg>
        <svg v-else-if="view.primary === 'publish'" width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round">
          <path d="M12 19V5" /><path d="m5 12 7-7 7 7" />
        </svg>
        <svg v-else-if="view.primary === 'pull'" width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round">
          <path d="M12 5v14" /><path d="m19 12-7 7-7-7" />
        </svg>
        <svg v-else-if="view.tone === 'attention'" width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round">
          <circle cx="12" cy="12" r="9" /><path d="M12 8v4" /><path d="M12 16h.01" />
        </svg>
        <svg v-else width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
          <path d="M21 16V8a2 2 0 0 0-1-1.73l-7-4a2 2 0 0 0-2 0l-7 4A2 2 0 0 0 3 8v8a2 2 0 0 0 1 1.73l7 4a2 2 0 0 0 2 0l7-4A2 2 0 0 0 21 16z" />
          <path d="m3.3 7 8.7 5 8.7-5" /><path d="M12 22V12" />
        </svg>
      </span>
      <div class="project-state-text">
        <span class="project-state-title">{{ view.title }}</span>
        <span v-if="view.detail" class="project-state-detail">{{ view.detail }}</span>
      </div>
      <button
        v-if="view.primary"
        type="button"
        class="project-primary"
        :class="{ quiet: view.primary === 'branch' }"
        :disabled="!!busy"
        @click="runPrimary"
      >
        <span v-if="busy === view.primary" class="spinner" />
        {{ primaryLabel }}
      </button>
    </div>

    <form v-if="showMrForm && !openMr" class="mr-form" @submit.prevent="createMr" @keydown.esc="showMrForm = false">
      <label class="mr-label" for="git-mr-title">
        Merge <code>{{ branch }}</code> into <code>{{ defaultBranch }}</code>
      </label>
      <input id="git-mr-title" ref="mrInput" v-model="mrTitle" class="mr-input" type="text" placeholder="Title" />
      <div class="mr-actions">
        <button type="button" class="mr-btn" @click="showMrForm = false">Cancel</button>
        <button type="submit" class="mr-btn mr-btn--primary" :disabled="!!busy || !mrTitle.trim()">{{ mrSubmitLabel }}</button>
      </div>
    </form>

    <div v-else-if="!detached && !onDefault && (openMr || !status?.sandboxOwned)" class="project-links">
      <button
        v-if="openMr"
        type="button"
        class="project-link project-link--mr"
        :title="`Open merge request !${openMr.id}`"
        @click="openMergeRequest(openMr.id)"
      >
        <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
          <circle cx="18" cy="18" r="3" /><circle cx="6" cy="6" r="3" /><path d="M13 6h3a2 2 0 0 1 2 2v7" /><path d="M6 9v12" />
        </svg>
        <span class="mr-id">!{{ openMr.id }}</span>
        <span class="mr-title">{{ openMr.title }}</span>
      </button>
      <button
        v-else-if="canStartMr"
        type="button"
        class="project-link"
        :disabled="!!busy"
        :title="`Ask to merge ${branch} into ${defaultBranch}`"
        @click="startMr"
      >
        <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
          <circle cx="18" cy="18" r="3" /><circle cx="6" cy="6" r="3" /><path d="M13 6h3a2 2 0 0 1 2 2v7" /><path d="M6 9v12" />
        </svg>
        Merge request
      </button>
      <button
        v-if="!status?.sandboxOwned"
        type="button"
        class="project-link"
        :disabled="!!busy"
        :title="`Fetch the project's ${defaultBranch} and merge it into ${branch}`"
        @click="updateFromDefault"
      >
        <span v-if="busy === 'update'" class="spinner" />
        <svg v-else width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
          <path d="M21 12a9 9 0 1 1-9-9c2.52 0 4.93 1 6.74 2.74L21 8" /><path d="M21 3v5h-5" />
        </svg>
        Update from {{ defaultBranch }}
      </button>
    </div>
  </section>
</template>

<style scoped>
.project-card {
  --tone: var(--text-muted);
  --tone-bg: var(--bg-neutral);
  border: 0.5px solid var(--border-default);
  border-radius: var(--radius-md);
  background: var(--bg-elevated);
  overflow: hidden;
}

.tone-pending {
  --tone: var(--accent-blue);
  --tone-bg: color-mix(in srgb, var(--accent-blue) 16%, transparent);
}

.tone-synced {
  --tone: var(--accent-green);
  --tone-bg: var(--success-bg);
}

.tone-attention {
  --tone: var(--attention);
  --tone-bg: var(--attention-bg);
}

.project-state {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: var(--space-2) var(--space-2) var(--space-2) 10px;
}

.project-state-icon {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 24px;
  height: 24px;
  flex-shrink: 0;
  border-radius: 50%;
  background: var(--tone-bg);
  color: var(--tone);
}

.project-state-text {
  display: flex;
  flex: 1;
  flex-direction: column;
  gap: 1px;
  min-width: 0;
}

.project-state-title {
  font-size: 0.76rem;
  font-weight: 500;
  color: var(--text-primary);
}

.project-state-detail {
  font-size: 0.68rem;
  line-height: 1.35;
  color: var(--text-muted);
  overflow-wrap: anywhere;
}

.project-primary {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  flex-shrink: 0;
  height: 26px;
  padding: 0 10px;
  border-radius: var(--radius-md);
  background: var(--accent-blue);
  color: var(--text-on-accent);
  font-size: 0.72rem;
  font-weight: 600;
  white-space: nowrap;
  transition: background var(--transition-fast), opacity var(--transition-fast);
}

.project-primary:hover:not(:disabled) {
  background: color-mix(in srgb, var(--accent-blue) 85%, var(--gruvbox-fg0));
}

.project-primary.quiet {
  border: 0.5px solid var(--border-strong);
  background: transparent;
  color: var(--text-primary);
}

.project-primary.quiet:hover:not(:disabled) {
  background: var(--bg-hover);
}

.project-primary:disabled {
  opacity: 0.55;
  cursor: default;
}

.project-links {
  display: flex;
  align-items: stretch;
  border-top: 0.5px solid var(--border-hairline);
}

.project-link {
  display: inline-flex;
  flex: 1 1 0;
  align-items: center;
  justify-content: center;
  gap: 6px;
  min-width: 0;
  height: 28px;
  padding: 0 var(--space-2);
  color: var(--text-secondary);
  font-size: 0.7rem;
  white-space: nowrap;
  overflow: hidden;
  transition: color var(--transition-fast), background var(--transition-fast);
}

.project-link + .project-link {
  border-left: 0.5px solid var(--border-hairline);
}

.project-link svg {
  flex-shrink: 0;
  color: var(--text-muted);
}

.project-link:hover:not(:disabled) {
  background: var(--bg-hover);
  color: var(--text-primary);
}

.project-link:disabled {
  opacity: 0.45;
  cursor: default;
}

.project-link--mr {
  flex-grow: 1.6;
  justify-content: flex-start;
}

.project-link--mr svg {
  color: var(--accent-blue);
}

.mr-id {
  flex-shrink: 0;
  font-family: var(--font-mono);
  color: var(--accent-blue);
}

.mr-title {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  color: var(--text-primary);
}

.mr-form {
  display: flex;
  flex-direction: column;
  gap: 6px;
  padding: var(--space-2) 10px 10px;
  border-top: 0.5px solid var(--border-hairline);
}

.mr-label {
  font-size: 0.68rem;
  color: var(--text-muted);
}

.mr-label code {
  font-size: 0.68rem;
  color: var(--text-secondary);
}

.mr-input {
  width: 100%;
  height: 28px;
  padding: 0 var(--space-2);
  border: 0.5px solid var(--border-default);
  border-radius: var(--radius-md);
  background: var(--bg-base);
  color: var(--text-primary);
  font-size: 0.75rem;
  outline: none;
  transition: border-color var(--transition-fast);
}

.mr-input:focus {
  border-color: var(--accent-blue);
}

.mr-actions {
  display: flex;
  justify-content: flex-end;
  gap: var(--space-1);
}

.mr-btn {
  height: 24px;
  padding: 0 10px;
  border-radius: var(--radius-md);
  color: var(--text-secondary);
  font-size: 0.72rem;
  transition: color var(--transition-fast), background var(--transition-fast);
}

.mr-btn:hover:not(:disabled) {
  background: var(--bg-hover);
  color: var(--text-primary);
}

.mr-btn--primary {
  background: var(--accent-blue);
  color: var(--text-on-accent);
  font-weight: 600;
}

.mr-btn--primary:hover:not(:disabled) {
  background: color-mix(in srgb, var(--accent-blue) 85%, var(--gruvbox-fg0));
  color: var(--text-on-accent);
}

.mr-btn:disabled {
  opacity: 0.5;
  cursor: default;
}

.spinner {
  width: 10px;
  height: 10px;
  border: 1.5px solid var(--spinner-track);
  border-top-color: currentColor;
  border-radius: 50%;
  animation: spin 0.6s linear infinite;
}

/* Matches MOBILE_MEDIA_QUERY in useMediaQuery.ts: touch-sized controls. */
@media (max-width: 768px), (pointer: coarse) and (max-height: 500px) {
  .project-state-title {
    font-size: 0.85rem;
  }

  .project-state-detail {
    font-size: 0.75rem;
  }

  .project-primary,
  .mr-btn,
  .mr-input {
    height: 40px;
  }

  .project-link {
    height: 44px;
    font-size: 0.78rem;
  }
}
</style>
