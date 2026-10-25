<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import type { Workspace } from '@/api/workspaces'
import {
  projectApi,
  type ConvertResult,
  type DiscoveredRepo,
  type RepoDiscovery,
  type SSHCleanup,
} from '@/api/projects'

// Turns a standalone workspace into a project. A repo at /workspace stays
// where it is and the workspace becomes the project's workspace; a repo in a
// subdirectory seeds a new project that gets a fresh workspace; a workspace
// with no repo can have one made from its files.

const props = defineProps<{
  show: boolean
  workspace: Workspace | null
}>()

const emit = defineEmits<{
  close: []
  start: [workspace: Workspace]
  converted: [result: ConvertResult]
}>()

const DEFAULT_MAX_SANDBOXES = 3
const MAX_SANDBOXES_LIMIT = 20
// The root repo, or a repo made from the workspace's files.
const ROOT = '.'
const FROM_FILES = ''

const discovery = ref<RepoDiscovery | null>(null)
const loading = ref(false)
const loadError = ref('')
const converting = ref(false)
const convertError = ref('')
// Set when devpad kept SSH files it did not write; shown before leaving.
const keptSSH = ref<SSHCleanup | null>(null)
const result = ref<ConvertResult | null>(null)

const repoPath = ref<string | null>(null)
const includeSensitive = ref(false)
const name = ref('')
const description = ref('')
const defaultBranch = ref('')
const upstreamUrl = ref('')
const setupScript = ref('')
const maxSandboxes = ref(DEFAULT_MAX_SANDBOXES)

const running = computed(() => props.workspace?.status === 'running')
const repos = computed(() => discovery.value?.repos ?? [])
const contents = computed(() => discovery.value?.contents ?? null)
const selectedRepo = computed<DiscoveredRepo | null>(
  () => repos.value.find((r) => r.path === repoPath.value) ?? null,
)
const fromFiles = computed(() => repoPath.value === FROM_FILES && !!contents.value)
const inPlace = computed(() => fromFiles.value || repoPath.value === ROOT)

const contentsBytes = computed(() => {
  const c = contents.value
  if (!c) return 0
  return c.bytes + (includeSensitive.value ? c.sensitiveBytes : 0)
})
const contentsTooBig = computed(() => !!contents.value && contentsBytes.value > contents.value.maxBytes)

const blocker = computed(() => {
  if (repoPath.value === null) return 'Pick a repository.'
  if (selectedRepo.value?.hasDevpadRemote && inPlace.value) {
    return 'This repository has a remote named "devpad", which devpad needs for its own copies of project branches. Rename it (git remote rename devpad something-else) and try again.'
  }
  if (selectedRepo.value && selectedRepo.value.branchCount === 0) return 'This repository has no commits yet. Commit something first.'
  if (fromFiles.value && contentsTooBig.value) return `The files total ${formatBytes(contentsBytes.value)}, over the ${formatBytes(contents.value!.maxBytes)} limit.`
  return ''
})

// Credentials devpad found and left in place. The IDE assistant can read
// anything in the workspace, so the user should know they are there.
const credentialWarnings = computed(() => {
  const warnings: string[] = []
  const d = discovery.value
  if (!d) return warnings
  for (const file of d.credentialFiles) warnings.push(`${file} holds git credentials.`)
  const helpers = [...d.credentialHelpers, ...(selectedRepo.value?.credentialHelpers ?? [])]
  if (helpers.length > 0) warnings.push(`Git is set up with a credential helper (${helpers.join(', ')}).`)
  const remotes = selectedRepo.value?.remotesWithCredentials ?? []
  if (remotes.length > 0) {
    warnings.push(`The ${remotes.join(', ')} remote URL${remotes.length > 1 ? 's embed' : ' embeds'} a password or token.`)
  }
  return warnings
})

watch(
  () => [props.show, props.workspace?.id, props.workspace?.status] as const,
  ([visible]) => {
    if (!visible || !props.workspace) return
    resetForm()
    if (running.value) void load()
  },
  { immediate: true },
)

// Picking a repo fills in the branch and upstream it suggests.
watch(repoPath, () => {
  const repo = selectedRepo.value
  defaultBranch.value = repo?.originDefaultBranch || repo?.branch || 'main'
  upstreamUrl.value = repo?.originUrl ?? ''
})

function resetForm() {
  discovery.value = null
  loadError.value = ''
  convertError.value = ''
  keptSSH.value = null
  result.value = null
  repoPath.value = null
  includeSensitive.value = false
  name.value = props.workspace?.name ?? ''
  description.value = props.workspace?.description ?? ''
  defaultBranch.value = 'main'
  upstreamUrl.value = ''
  setupScript.value = ''
  maxSandboxes.value = DEFAULT_MAX_SANDBOXES
}

async function load() {
  const ws = props.workspace
  if (!ws) return
  loading.value = true
  try {
    const found = await projectApi.discoverConversion(ws.id)
    if (props.workspace?.id !== ws.id) return
    discovery.value = found
    // The root repo, or the only repo, or making one from files.
    const root = found.repos.find((r) => r.path === ROOT)
    if (root) repoPath.value = ROOT
    else if (found.repos.length === 1) repoPath.value = found.repos[0]!.path
    else if (found.repos.length === 0 && found.contents) repoPath.value = FROM_FILES
  } catch (e) {
    loadError.value = e instanceof Error ? e.message : 'Failed to look for repositories'
  } finally {
    loading.value = false
  }
}

async function convert() {
  const ws = props.workspace
  if (!ws || repoPath.value === null || blocker.value) return
  if (!name.value.trim()) {
    convertError.value = 'Name is required'
    return
  }
  const max = Number(maxSandboxes.value)
  if (!Number.isInteger(max) || max < 1 || max > MAX_SANDBOXES_LIMIT) {
    convertError.value = `Concurrent sandboxes must be between 1 and ${MAX_SANDBOXES_LIMIT}`
    return
  }
  converting.value = true
  convertError.value = ''
  try {
    const res = await projectApi.convertWorkspace(ws.id, {
      repoPath: repoPath.value,
      includeSensitive: fromFiles.value && includeSensitive.value,
      name: name.value.trim(),
      description: description.value.trim(),
      defaultBranch: defaultBranch.value.trim() || undefined,
      upstreamUrl: upstreamUrl.value.trim() || undefined,
      setupScript: setupScript.value,
      maxSandboxes: max,
    })
    if (res.ssh && res.ssh.kept.length > 0) {
      keptSSH.value = res.ssh
      result.value = res
    } else {
      emit('converted', res)
    }
  } catch (e) {
    convertError.value = e instanceof Error ? e.message : 'Conversion failed'
  } finally {
    converting.value = false
  }
}

function close() {
  if (converting.value) return
  if (result.value) emit('converted', result.value)
  else emit('close')
}

function formatBytes(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`
}
</script>

<template>
  <Teleport to="body">
    <div v-if="show && workspace" class="modal-overlay" @click.self="close">
      <div class="modal" role="dialog" aria-label="Convert to project">
        <div class="modal-header">
          <h2 class="modal-title">Convert “{{ workspace.name }}” to a project</h2>
          <button class="modal-close" aria-label="Close" :disabled="converting" @click="close">&times;</button>
        </div>

        <div v-if="keptSSH" class="modal-body">
          <p class="notice notice-success">The project is ready, and this workspace is now its workspace.</p>
          <p class="notice notice-warning">
            Devpad removed the SSH keys it had put in this workspace, but left
            <span class="mono">{{ keptSSH.kept.join(', ') }}</span>
            because {{ keptSSH.kept.length > 1 ? "they don't" : "it doesn't" }} match what devpad wrote, so
            {{ keptSSH.kept.length > 1 ? 'they may be' : 'it may be' }} yours. The IDE assistant can read
            anything in the workspace; remove any keys you don't want it to have.
          </p>
          <div class="modal-actions">
            <button type="button" class="btn btn-primary" @click="close">Open project</button>
          </div>
        </div>

        <div v-else-if="!running" class="modal-body">
          <p class="notice notice-warning">Start the workspace so devpad can look for its repositories.</p>
          <div class="modal-actions">
            <button type="button" class="btn btn-secondary" @click="emit('close')">Cancel</button>
            <button type="button" class="btn btn-primary" :disabled="workspace.status === 'creating'" @click="emit('start', workspace)">
              Start workspace
            </button>
          </div>
        </div>

        <div v-else-if="loading" class="modal-body">
          <p class="empty-text">Looking for repositories...</p>
        </div>

        <div v-else-if="loadError" class="modal-body">
          <p class="notice notice-error">{{ loadError }}</p>
          <div class="modal-actions">
            <button type="button" class="btn btn-secondary" @click="emit('close')">Cancel</button>
            <button type="button" class="btn btn-primary" @click="load">Try again</button>
          </div>
        </div>

        <form v-else-if="discovery" class="modal-body" @submit.prevent="convert">
          <div class="form-group">
            <span class="form-label">Repository</span>
            <div class="choices">
              <label v-for="repo in repos" :key="repo.path" class="choice" :class="{ selected: repoPath === repo.path }">
                <input v-model="repoPath" type="radio" name="convert-repo" :value="repo.path" />
                <span class="choice-body">
                  <span class="choice-title mono">{{ repo.path === '.' ? '/workspace' : `/workspace/${repo.path}` }}</span>
                  <span class="meta-row">
                    <span v-if="repo.branch">on <span class="mono">{{ repo.branch }}</span></span>
                    <span>{{ repo.branchCount }} branch{{ repo.branchCount === 1 ? '' : 'es' }}</span>
                    <span v-if="repo.dirty" class="tag tag-warning">uncommitted changes</span>
                  </span>
                  <span v-if="repo.originUrl" class="choice-sub mono">{{ repo.originUrl }}</span>
                </span>
              </label>
              <label v-if="contents" class="choice" :class="{ selected: repoPath === '' }">
                <input v-model="repoPath" type="radio" name="convert-repo" value="" />
                <span class="choice-body">
                  <span class="choice-title">Make a repository from the workspace's files</span>
                  <span class="meta-row">
                    <span>{{ contents.files }} file{{ contents.files === 1 ? '' : 's' }}, {{ formatBytes(contents.bytes) }}</span>
                  </span>
                </span>
              </label>
            </div>
            <p v-if="repos.length === 0 && !contents" class="empty-text">No repositories found.</p>
          </div>

          <template v-if="fromFiles && contents">
            <p class="form-hint">
              Left out: <span class="mono">{{ contents.junkPatterns.join(' ') }}</span>. They stay in the
              workspace, untracked; nothing is added to or changed in your files.
            </p>
            <div v-if="contents.sensitiveCount > 0" class="form-group sensitive">
              <p class="form-hint">
                {{ contents.sensitiveCount }} file{{ contents.sensitiveCount === 1 ? ' looks' : 's look' }} like
                {{ contents.sensitiveCount === 1 ? 'it holds' : 'they hold' }} secrets and
                {{ contents.sensitiveCount === 1 ? 'is' : 'are' }} left out:
              </p>
              <ul class="file-list mono">
                <li v-for="file in contents.sensitiveFiles" :key="file">{{ file }}</li>
                <li v-if="contents.sensitiveCount > contents.sensitiveFiles.length" class="muted">
                  and {{ contents.sensitiveCount - contents.sensitiveFiles.length }} more
                </li>
              </ul>
              <label class="form-check">
                <input v-model="includeSensitive" type="checkbox" />
                Commit these too (agents and the project's history will see them)
              </label>
            </div>
          </template>

          <p v-if="selectedRepo && !inPlace" class="notice notice-warning">
            This repository isn't at /workspace, so it can't become the project's workspace in place. Its
            branches seed the project, which gets a fresh workspace when you open it; this workspace stays as it
            is<template v-if="selectedRepo.dirty">, uncommitted changes included</template>.
          </p>
          <p v-else-if="selectedRepo?.dirty" class="form-hint">
            Uncommitted changes stay in your working tree; they reach the project once you commit and publish
            them.
          </p>

          <div class="form-group">
            <label class="form-label" for="convert-name">Project name</label>
            <input id="convert-name" v-model="name" class="form-input" type="text" autocomplete="off" />
          </div>
          <div class="form-group">
            <label class="form-label" for="convert-desc">Description</label>
            <textarea id="convert-desc" v-model="description" class="form-input form-textarea" rows="2" placeholder="Optional. Agents see it with every task." />
          </div>
          <div class="form-group">
            <label class="form-label" for="convert-branch">Default branch</label>
            <input id="convert-branch" v-model="defaultBranch" class="form-input form-mono" type="text" autocomplete="off" placeholder="main" />
            <p class="form-hint">Merges land here. It must be one of the repository's branches.</p>
          </div>
          <div class="form-group">
            <label class="form-label" for="convert-upstream">Upstream repository</label>
            <input id="convert-upstream" v-model="upstreamUrl" class="form-input form-mono" type="text" autocomplete="off" placeholder="git@github.com:org/repo.git" />
            <p v-if="selectedRepo?.originHadCredentials" class="form-hint warning-text">
              The credentials in origin's URL were removed. The project pushes with your devpad SSH key, so an
              SSH URL works best.
            </p>
            <p v-else class="form-hint">Optional. The project's Push sends merged work here with your SSH key.</p>
          </div>
          <div class="form-group">
            <label class="form-label" for="convert-setup">Setup script</label>
            <textarea id="convert-setup" v-model="setupScript" class="form-input form-textarea form-mono" rows="3" placeholder="npm ci" />
            <p class="form-hint">Runs in each new sandbox. It doesn't run here; this workspace is already set up.</p>
          </div>
          <div class="form-group">
            <label class="form-label" for="convert-max">Concurrent sandboxes</label>
            <input id="convert-max" v-model.number="maxSandboxes" class="form-input" type="number" min="1" :max="MAX_SANDBOXES_LIMIT" />
          </div>

          <div v-if="inPlace" class="notice notice-warning">
            After converting, this workspace no longer holds your SSH keys: devpad removes the ones it put in
            <span class="mono">~/.ssh</span>, since the IDE assistant runs here too. Pushing upstream happens from
            the project's Push button, not from the terminal.
          </div>
          <div v-if="credentialWarnings.length > 0" class="notice notice-warning">
            Devpad leaves these in place, where the IDE assistant can read them:
            <ul class="warning-list">
              <li v-for="warning in credentialWarnings" :key="warning">{{ warning }}</li>
            </ul>
          </div>

          <p v-if="blocker && repoPath !== null" class="form-error">{{ blocker }}</p>
          <p v-if="convertError" class="form-error">{{ convertError }}</p>
          <div class="modal-actions">
            <button type="button" class="btn btn-secondary" :disabled="converting" @click="emit('close')">Cancel</button>
            <button type="submit" class="btn btn-primary" :disabled="converting || !!blocker">
              {{ converting ? 'Converting...' : 'Convert' }}
            </button>
          </div>
        </form>
      </div>
    </div>
  </Teleport>
</template>

<style scoped src="@/components/project/shared.css"></style>
<style scoped>
.modal {
  background: var(--bg-surface);
  border: 0.5px solid var(--border-default);
  border-radius: var(--radius-lg);
  width: 100%;
  max-width: 560px;
  box-shadow: var(--shadow-strong);
}

.modal-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-3);
  padding: var(--space-4) var(--space-5);
  border-bottom: 0.5px solid var(--border-default);
}

.modal-title {
  font-size: 1rem;
  font-weight: 600;
  overflow-wrap: anywhere;
}

.modal-close {
  font-size: 1.25rem;
  color: var(--text-muted);
  padding: var(--space-1);
  transition: color var(--transition-fast);
}

.modal-close:hover:not(:disabled) {
  color: var(--text-primary);
}

.modal-body {
  padding: var(--space-5);
}

.modal-actions {
  display: flex;
  justify-content: flex-end;
  gap: var(--space-2);
  padding-top: var(--space-2);
}

.choices {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
}

.choice {
  display: flex;
  align-items: flex-start;
  gap: var(--space-2);
  padding: var(--space-2) var(--space-3);
  border: 0.5px solid var(--border-default);
  border-radius: var(--radius-md);
  cursor: pointer;
  transition: border-color var(--transition-fast);
}

.choice.selected {
  border-color: var(--accent-blue);
}

.choice input {
  margin-top: 3px;
  accent-color: var(--accent-blue);
}

.choice-body {
  display: flex;
  flex-direction: column;
  gap: 2px;
  min-width: 0;
}

.choice-title {
  font-size: 0.8rem;
  color: var(--text-primary);
  overflow-wrap: anywhere;
}

.choice-sub {
  color: var(--text-muted);
  overflow-wrap: anywhere;
}

.tag {
  padding: 0 var(--space-1);
  border-radius: var(--radius-sm);
  font-size: 0.7rem;
}

.tag-warning {
  color: var(--warning);
  background: var(--warning-bg);
}

.sensitive {
  margin-top: var(--space-2);
}

.file-list {
  margin: var(--space-1) 0 var(--space-2) var(--space-4);
  color: var(--text-secondary);
  list-style: disc;
  overflow-wrap: anywhere;
}

.warning-list {
  margin: var(--space-1) 0 0 var(--space-4);
  list-style: disc;
}

.warning-text {
  color: var(--warning);
}
</style>
