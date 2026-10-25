<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import type { Workspace } from '@/api/workspaces'
import { ApiError } from '@/api/client'
import {
  projectApi,
  type ConvertResult,
  type CredentialFailure,
  type CredentialItem,
  type DiscoveredRepo,
  type RepoDiscovery,
} from '@/api/projects'
import { credentialKind, REVOKE_REMINDER } from '@/components/project/credentials'

// Turns a standalone workspace into a project. A repo at /workspace stays
// where it is and the workspace becomes the project's workspace; a repo in a
// subdirectory seeds a new project that gets a fresh workspace; a workspace
// with no repo can have one made from its files.
//
// Converting in place makes the workspace one where no credential may stay,
// though the IDE assistant could read everything it held: the user agrees to
// strip what devpad finds, devpad restarts the container and replaces the
// user's devpad SSH key, and the user revokes what was exposed.

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
// Branches and tags named before the rest are summarised.
const REFS_SHOWN = 12

const discovery = ref<RepoDiscovery | null>(null)
const loading = ref(false)
const loadError = ref('')
const converting = ref(false)
const convertError = ref('')
// Credentials the user agreed to strip that devpad could not, with why.
const stripFailures = ref<CredentialFailure[]>([])
// A conversion in place, shown until the user has read what to do next.
const result = ref<ConvertResult | null>(null)
const copied = ref(false)

const repoPath = ref<string | null>(null)
const includeSensitive = ref(false)
const stripConsent = ref(false)
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
// Discovery names at most 200 branches; past that the user may need to type one.
const allBranchesNamed = computed(
  () => !!selectedRepo.value && selectedRepo.value.branches.length >= selectedRepo.value.branchCount,
)
const fromFiles = computed(() => repoPath.value === FROM_FILES && !!contents.value)
const inPlace = computed(() => fromFiles.value || repoPath.value === ROOT)

const contentsBytes = computed(() => {
  const c = contents.value
  if (!c) return 0
  return c.bytes + (includeSensitive.value ? c.sensitiveBytes : 0)
})
const contentsTooBig = computed(() => !!contents.value && contentsBytes.value > contents.value.maxBytes)

const credentials = computed<CredentialItem[]>(() => discovery.value?.credentials ?? [])
const credentialIncludes = computed(() => discovery.value?.credentialIncludes ?? [])

const blocker = computed(() => {
  if (repoPath.value === null) return 'Pick a repository.'
  if (inPlace.value && credentials.value.length > 0 && !stripConsent.value) {
    return 'Agree to strip the credentials listed above, or remove them yourself and look again.'
  }
  if (selectedRepo.value?.hasDevpadRemote && inPlace.value) {
    return 'This repository has a remote named "devpad", which devpad needs for its own copies of project branches. Rename it (git remote rename devpad something-else) and try again.'
  }
  if (selectedRepo.value && selectedRepo.value.branchCount === 0) return 'This repository has no commits yet. Commit something first.'
  if (fromFiles.value && contentsTooBig.value) return `The files total ${formatBytes(contentsBytes.value)}, over the ${formatBytes(contents.value!.maxBytes)} limit.`
  return ''
})

// What a repo's import leaves out or takes from behind origin.
const refWarnings = computed(() => {
  const repo = selectedRepo.value
  const warnings: string[] = []
  if (!repo) return warnings
  if (repo.remoteOnlyBranches.length > 0) {
    warnings.push(
      `Only on origin, so not imported: ${listSome(repo.remoteOnlyBranches)}. Check them out locally first to include them.`,
    )
  }
  if (repo.behindBranches.length > 0) {
    const behind = repo.behindBranches.map((b) => `${b.name} (${b.behind} commit${b.behind === 1 ? '' : 's'})`)
    warnings.push(
      `Behind origin: ${listSome(behind)}. The project starts from your local copies; pull first for the latest.`,
    )
  }
  return warnings
})

function listSome(items: string[], total = items.length): string {
  const shown = items.slice(0, REFS_SHOWN).join(', ')
  return total > REFS_SHOWN ? `${shown} and ${total - REFS_SHOWN} more` : shown
}

// The branch merges land on: origin's default, else a conventional name,
// else whatever is checked out.
function suggestDefaultBranch(repo: DiscoveredRepo | null): string {
  if (!repo) return 'main'
  const has = (b: string) => !!b && repo.branches.includes(b)
  for (const candidate of [repo.originDefaultBranch, 'main', 'master', repo.branch]) {
    if (has(candidate)) return candidate
  }
  return repo.branch || repo.branches[0] || 'main'
}

// Separate sources, so the home page's polling, which replaces the workspace
// object every few seconds, doesn't reset the form unless one of them changed.
watch(
  [() => props.show, () => props.workspace?.id, () => props.workspace?.status],
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
  defaultBranch.value = suggestDefaultBranch(repo)
  upstreamUrl.value = repo?.originUrl ?? ''
})

function resetForm() {
  discovery.value = null
  loadError.value = ''
  convertError.value = ''
  stripFailures.value = []
  result.value = null
  copied.value = false
  repoPath.value = null
  includeSensitive.value = false
  stripConsent.value = false
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
  stripFailures.value = []
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
      stripCredentials: inPlace.value && stripConsent.value ? credentials.value.map((c) => c.id) : [],
      name: name.value.trim(),
      description: description.value.trim(),
      defaultBranch: defaultBranch.value.trim() || undefined,
      upstreamUrl: upstreamUrl.value.trim() || undefined,
      setupScript: setupScript.value,
      maxSandboxes: max,
    })
    // Converting in place leaves the user something to do upstream.
    if (res.inPlace) result.value = res
    else emit('converted', res)
  } catch (e) {
    const refused = credentialsFrom(e)
    if (refused && discovery.value) {
      discovery.value = { ...discovery.value, credentials: [...refused.failed, ...refused.credentials] }
      stripFailures.value = refused.failed
      stripConsent.value = false
      convertError.value =
        refused.failed.length > 0
          ? "Some credentials couldn't be stripped (see above). Remove them yourself, then look again."
          : 'The workspace holds credentials you have not agreed to strip. Review the list and try again.'
    } else {
      convertError.value = e instanceof Error ? e.message : 'Conversion failed'
    }
  } finally {
    converting.value = false
  }
}

// A refused conversion lists the credentials found since the user looked,
// and those it agreed to strip but could not.
function credentialsFrom(e: unknown): { credentials: CredentialItem[]; failed: CredentialFailure[] } | null {
  if (!(e instanceof ApiError) || e.status !== 409) return null
  const body = e.body as { credentials?: CredentialItem[]; failed?: CredentialFailure[] } | null
  if (!Array.isArray(body?.credentials)) return null
  return { credentials: body.credentials, failed: Array.isArray(body.failed) ? body.failed : [] }
}

async function copyKey() {
  if (!result.value?.publicKey) return
  try {
    await navigator.clipboard.writeText(result.value.publicKey)
    copied.value = true
  } catch {
    copied.value = false
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

        <div v-if="result" class="modal-body">
          <p class="notice notice-success">The project is ready, and this workspace is now its workspace.</p>

          <div v-if="result.keyRotated" class="form-group">
            <p class="form-hint">
              Your devpad SSH key was replaced, because this workspace held it where the IDE assistant could read
              it. Add the new public key to GitHub (or wherever your upstream is), then remove the old one there.
            </p>
            <textarea class="form-input form-textarea form-mono" rows="3" readonly :value="result.publicKey" />
            <div class="key-actions">
              <button type="button" class="btn btn-secondary" @click="copyKey">{{ copied ? 'Copied' : 'Copy key' }}</button>
            </div>
            <p class="form-hint">
              Projects push with the new key. Other workspaces without a project get it on their next start; running
              ones keep the old key until restarted.
            </p>
          </div>
          <p v-else-if="result.rotationError" class="notice notice-error">
            Devpad couldn't replace your SSH key ({{ result.rotationError }}). This workspace held it, so generate a
            new one in Settings, add it upstream, and remove the old one there.
          </p>

          <div v-if="result.stripped.length > 0" class="notice notice-warning">
            Stripped from the workspace:
            <ul class="warning-list">
              <li v-for="item in result.stripped" :key="item.id">
                {{ credentialKind(item.kind) }}: <span class="mono">{{ item.location }}</span>
              </li>
            </ul>
            Revoke these tokens and keys where they were issued. Stripping them can't undo that the assistant could
            read them.
          </div>
          <p class="notice notice-warning">{{ REVOKE_REMINDER }}</p>

          <p v-if="result.progressError" class="notice notice-warning">{{ result.progressError }}.</p>
          <p v-if="result.restartError" class="notice notice-error">
            Devpad couldn't restart the workspace ({{ result.restartError }}). Stop and start it yourself, so no
            process started before the conversion keeps a credential.
          </p>
          <p v-else class="form-hint">The workspace was restarted, so terminals and commands start fresh.</p>

          <p v-if="result.ssh && result.ssh.kept.length > 0" class="form-hint">
            Left in place because {{ result.ssh.kept.length > 1 ? "they don't" : "it doesn't" }} match what devpad
            wrote: <span class="mono">{{ result.ssh.kept.join(', ') }}</span>.
          </p>
          <p class="form-hint">This stays on the project's panel until you dismiss it there.</p>
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
                    <span v-if="repo.tagCount > 0">{{ repo.tagCount }} tag{{ repo.tagCount === 1 ? '' : 's' }}</span>
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

          <div v-if="selectedRepo" class="form-group">
            <span class="form-label">Imported</span>
            <p class="form-hint">
              Branches: <span class="mono">{{ listSome(selectedRepo.branches, selectedRepo.branchCount) }}</span>
              <template v-if="selectedRepo.tagCount > 0">
                <br />Tags: <span class="mono">{{ listSome(selectedRepo.tags, selectedRepo.tagCount) }}</span>
              </template>
            </p>
            <p v-for="warning in refWarnings" :key="warning" class="form-hint warning-text">{{ warning }}</p>
          </div>

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
            <select v-if="selectedRepo && allBranchesNamed" id="convert-branch" v-model="defaultBranch" class="form-input form-mono">
              <option v-for="branch in selectedRepo.branches" :key="branch" :value="branch">{{ branch }}</option>
            </select>
            <template v-else>
              <input
                id="convert-branch"
                v-model="defaultBranch"
                class="form-input form-mono"
                type="text"
                autocomplete="off"
                placeholder="main"
                :list="selectedRepo ? 'convert-branches' : undefined"
              />
              <datalist v-if="selectedRepo" id="convert-branches">
                <option v-for="branch in selectedRepo.branches" :key="branch" :value="branch" />
              </datalist>
            </template>
            <p v-if="selectedRepo && allBranchesNamed" class="form-hint">Merges land here.</p>
            <p v-else-if="selectedRepo" class="form-hint">Merges land here. It must be one of the repository's branches.</p>
            <p v-else class="form-hint">The new repository's branch, where merges land.</p>
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

          <template v-if="inPlace">
            <div class="notice notice-warning">
              This workspace becomes the project's workspace, where the IDE assistant runs and no credential may
              stay. Converting:
              <ul class="warning-list">
                <li>removes the SSH key devpad put in <span class="mono">~/.ssh</span>;</li>
                <li>
                  restarts the workspace, so terminals and running commands restart, ending any process that kept
                  a credential;
                </li>
                <li>
                  replaces your devpad SSH key, since the assistant could read it here: you'll add the new key
                  upstream afterwards. Pushing upstream then happens from the project's Push button.
                </li>
              </ul>
              Deleting the project later deletes this workspace and its volume, untracked files included.
            </div>
            <div v-if="stripFailures.length > 0" class="notice notice-error">
              Couldn't strip:
              <ul class="warning-list">
                <li v-for="item in stripFailures" :key="item.id">
                  {{ credentialKind(item.kind) }}: <span class="mono">{{ item.location }}</span> ({{ item.reason }})
                </li>
              </ul>
            </div>
            <div v-if="credentials.length > 0" class="form-group credentials">
              <p class="form-hint">This workspace holds credentials the assistant could read:</p>
              <ul class="file-list">
                <li v-for="item in credentials" :key="item.id">
                  {{ credentialKind(item.kind) }}: <span class="mono">{{ item.location }}</span>
                </li>
              </ul>
              <label class="form-check">
                <input v-model="stripConsent" type="checkbox" />
                Strip these from the workspace. Stripping is permanent, even if the conversion then fails. Revoke
                them where they were issued afterwards; stripping can't undo that they were readable.
              </label>
            </div>
            <p v-if="credentialIncludes.length > 0" class="form-hint">
              Also searched, as included git config: <span class="mono">{{ credentialIncludes.join('; ') }}</span>.
            </p>
            <p v-if="discovery.credentialScanIncomplete" class="form-hint warning-text">
              The workspace is too large to search fully for repositories, so some credentials may not be listed.
            </p>
            <p class="form-hint">{{ REVOKE_REMINDER }}</p>
          </template>

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

.sensitive,
.credentials {
  margin-top: var(--space-2);
}

.key-actions {
  display: flex;
  justify-content: flex-end;
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
