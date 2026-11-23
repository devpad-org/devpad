<script setup lang="ts">
import DocLink from '@/components/DocLink.vue'
import { computed, ref, watch } from 'vue'
import { pipelineApi, MAX_LATEST_SUBJECTS } from '@/api/pipelines'
import { projectApi } from '@/api/projects'
import SelectMenu from '@/components/SelectMenu.vue'
import { PIPELINE_COPY } from '@/components/pipeline/pipelineFormat'
import { useActiveProjectStore } from '@/stores/activeProject'

// Picks the project's default branch and the other branches that change
// only through merge requests, such as main in a project that works on
// develop and releases into main, and deletes branches no longer needed.
const props = defineProps<{
  show: boolean
}>()

const emit = defineEmits<{
  close: []
}>()

const store = useActiveProjectStore()

const defaultBranch = ref('')
const protectedSet = ref(new Set<string>())
// The protected branches a task's merge request merges into once a review
// agent approves it.
const autoMergeSet = ref(new Set<string>())
// The protected branches that run the project's pipeline, and whose merge
// requests do.
const pipelineSet = ref(new Set<string>())
// Whether a pipeline is configured at each protected branch's commit, as
// the server read it when the dialog opened; a branch it couldn't read, or
// protected only in the edits not yet saved, is missing.
const configured = ref(new Map<string, boolean>())
const loading = ref(false)
const saving = ref(false)
const error = ref('')
// The branch whose deletion awaits confirmation, and the one being deleted.
const confirming = ref('')
const deleting = ref('')

// Protected branches first, then the rest by name.
const choices = computed(() => {
  const names = new Set(store.branches.map((b) => b.name))
  for (const b of store.protectedBranches) names.add(b)
  const initial = new Set(store.protectedBranches)
  return [...names].sort((a, b) => Number(initial.has(b)) - Number(initial.has(a)) || a.localeCompare(b))
})

// The branches protected in the edits not yet saved, the default first.
const protectedChoices = computed(() => [defaultBranch.value, ...choices.value.filter((b) => b !== defaultBranch.value && protectedSet.value.has(b))].filter(Boolean))

const shas = computed(() => new Map(store.branches.map((b) => [b.name, b.sha])))

// Deleting is immediate, so it is offered only for branches unprotected
// both now and in the edits not yet saved, and that nothing uses.
function deletable(branch: string): boolean {
  return shas.value.has(branch) && !store.isProtected(branch) && branch !== defaultBranch.value && !protectedSet.value.has(branch) && !usedBy(branch)
}

// What keeps a branch from being deleted, as devpad refuses it: a sandbox
// owning it, an open merge request from it, or a task not yet merged or
// cancelled that works on it or starts from it.
function usedBy(branch: string): string {
  if (store.sandboxes.some((s) => s.branch === branch)) return 'a sandbox'
  const mr = store.openMergeRequestFor(branch)
  if (mr) return `merge request !${mr.id}`
  const task = store.tasks.find(
    (t) => t.status !== 'merged' && t.status !== 'cancelled' && (t.branch === branch || t.baseBranch === branch),
  )
  return task ? `task #${task.id}` : ''
}

watch(
  () => props.show,
  async (visible) => {
    if (!visible || !store.project) return
    defaultBranch.value = store.project.defaultBranch
    protectedSet.value = new Set(store.protectedBranches.filter((b) => b !== store.project?.defaultBranch))
    autoMergeSet.value = new Set(store.project.autoMergeBranches ?? [])
    pipelineSet.value = new Set(store.project.pipelineBranches ?? [])
    error.value = ''
    confirming.value = ''
    loading.value = true
    try {
      await store.refreshBranches()
    } catch (e) {
      error.value = e instanceof Error ? e.message : 'Failed to load branches'
    } finally {
      loading.value = false
    }
    void loadConfigured()
  },
  { immediate: true },
)

// Reads whether each protected branch has a pipeline, for Merge
// automatically to say which a pipeline guards. Until it answers, or
// when it can't, every branch is taken for one without.
async function loadConfigured() {
  const id = store.projectId
  configured.value = new Map()
  if (id === null) return
  try {
    const res = await pipelineApi.latest(id, { branches: store.protectedBranches.slice(0, MAX_LATEST_SUBJECTS), mergeRequestIds: [] })
    const found = new Map<string, boolean>()
    for (const [name, standing] of Object.entries(res.branches ?? {})) {
      if (standing.configured !== null) found.set(name, standing.configured)
    }
    if (store.projectId === id) configured.value = found
  } catch {
    // The warning shows for every branch merged into automatically.
  }
}

// A pipeline guards a branch's merge requests when it is ticked to run
// pipelines and has one configured.
function guarded(branch: string): boolean {
  return pipelineSet.value.has(branch) && configured.value.get(branch) === true
}

const autoMerged = computed(() => protectedChoices.value.filter((b) => autoMergeSet.value.has(b)))
const autoMergedGuarded = computed(() => autoMerged.value.filter(guarded))
const autoMergedUnguarded = computed(() => autoMerged.value.filter((b) => !guarded(b)))

// "a", "a and b", "a, b and c".
function names(list: string[]): string {
  return list.length < 2 ? (list[0] ?? '') : `${list.slice(0, -1).join(', ')} and ${list[list.length - 1]}`
}

// The old default branch stays protected, so moving the default to develop
// leaves main changing only through merge requests.
function pickDefault(next: string) {
  const set = new Set(protectedSet.value)
  if (defaultBranch.value) set.add(defaultBranch.value)
  set.delete(next)
  protectedSet.value = set
  defaultBranch.value = next
}

function toggle(branch: string, on: boolean) {
  const set = new Set(protectedSet.value)
  if (on) set.add(branch)
  else set.delete(branch)
  protectedSet.value = set
}

function withBranch(set: Set<string>, branch: string, on: boolean): Set<string> {
  const next = new Set(set)
  if (on) next.add(branch)
  else next.delete(branch)
  return next
}

function toggleAutoMerge(branch: string, on: boolean) {
  autoMergeSet.value = withBranch(autoMergeSet.value, branch, on)
}

function togglePipelines(branch: string, on: boolean) {
  pipelineSet.value = withBranch(pipelineSet.value, branch, on)
}

// The branch is deleted only while it is where the list shows it, so
// commits published to it since are never lost. The list is reloaded
// either way, so a branch that moved or is already gone shows as it is.
async function deleteBranch(branch: string) {
  const sha = shas.value.get(branch)
  if (store.projectId === null || !sha) return
  deleting.value = branch
  error.value = ''
  try {
    await projectApi.deleteBranch(store.projectId, branch, sha)
  } catch (e) {
    error.value = e instanceof Error ? e.message : `Failed to delete ${branch}`
  } finally {
    deleting.value = ''
    confirming.value = ''
  }
  try {
    await store.refreshBranches()
  } catch (e) {
    error.value ||= e instanceof Error ? e.message : 'Failed to load branches'
  }
}

async function save() {
  if (store.projectId === null || !defaultBranch.value) return
  saving.value = true
  error.value = ''
  try {
    const res = await projectApi.setBranches(store.projectId, {
      defaultBranch: defaultBranch.value,
      protectedBranches: choices.value.filter((b) => protectedSet.value.has(b)),
      // Only a protected branch can be merged into automatically.
      autoMergeBranches: protectedChoices.value.filter((b) => autoMergeSet.value.has(b)),
      // Only a protected branch runs pipelines.
      pipelineBranches: protectedChoices.value.filter((b) => pipelineSet.value.has(b)),
    })
    store.setProject(res.project)
    emit('close')
  } catch (e) {
    error.value = e instanceof Error ? e.message : 'Failed to save branches'
  } finally {
    saving.value = false
  }
}
</script>

<template>
  <Teleport to="body">
    <div v-if="show" class="modal-overlay" @click.self="emit('close')">
      <div class="modal">
        <div class="modal-header">
          <h2 class="modal-title">Branches<DocLink topic="protectedBranches" label="Protected branches" /></h2>
          <button class="modal-close" aria-label="Close" @click="emit('close')">&times;</button>
        </div>
        <form class="modal-body" @submit.prevent="save">
          <div class="form-group">
            <label class="form-label" for="branches-default">Default branch</label>
            <SelectMenu
              id="branches-default"
              :model-value="defaultBranch"
              :options="choices.map((b) => ({ value: b, label: b }))"
              title="Default branch"
              variant="field"
              mono
              :disabled="loading"
              @update:model-value="pickDefault"
            />
            <p class="form-hint">Tasks and your project workspace start from it, and merge requests target it unless you pick another protected branch.</p>
          </div>
          <div class="form-group">
            <span class="form-label">Protected branches</span>
            <div class="branch-list" role="group" aria-label="Protected branches">
              <div v-for="b in choices" :key="b" class="branch-row">
                <template v-if="confirming === b">
                  <span class="branch-confirm">Delete <span class="mono">{{ b }}</span>?</span>
                  <button type="button" class="btn btn-danger btn-sm" :disabled="deleting !== ''" @click="deleteBranch(b)">
                    {{ deleting === b ? 'Deleting...' : 'Delete' }}
                  </button>
                  <button type="button" class="btn btn-secondary btn-sm" :disabled="deleting === b" @click="confirming = ''">Cancel</button>
                </template>
                <template v-else>
                  <label class="form-check branch-check">
                    <input
                      type="checkbox"
                      :checked="b === defaultBranch || protectedSet.has(b)"
                      :disabled="b === defaultBranch"
                      @change="toggle(b, ($event.target as HTMLInputElement).checked)"
                    />
                    <span class="mono">{{ b }}</span>
                  </label>
                  <span v-if="b === defaultBranch" class="branch-note">default</span>
                  <span v-else-if="usedBy(b)" class="branch-note">used by {{ usedBy(b) }}</span>
                  <button
                    v-else-if="deletable(b)"
                    type="button"
                    class="branch-delete"
                    :aria-label="`Delete ${b}`"
                    :disabled="deleting !== ''"
                    @click="confirming = b"
                  >
                    Delete
                  </button>
                </template>
              </div>
              <p v-if="loading" class="form-hint">Loading branches...</p>
            </div>
            <p class="form-hint">
              Protected branches change only through merge requests: agents can't own them and your workspace can't publish to them. Merge requests can target any of them,
              so features can merge into <span class="mono">develop</span> and releases from <span class="mono">develop</span> into <span class="mono">main</span>.
              Deleting an unprotected branch happens at once, without Save.
            </p>
          </div>
          <div class="form-group">
            <span class="form-label">Merge automatically<DocLink topic="autoMerge" label="Automatic merges" /></span>
            <div class="branch-list" role="group" aria-label="Merge automatically">
              <div v-for="b in protectedChoices" :key="b" class="branch-row">
                <label class="form-check branch-check">
                  <input type="checkbox" :checked="autoMergeSet.has(b)" @change="toggleAutoMerge(b, ($event.target as HTMLInputElement).checked)" />
                  <span class="mono">{{ b }}</span>
                </label>
              </div>
            </div>
            <p class="form-hint">
              A task's merge request into a branch ticked here merges as soon as a review agent approves it, and its pipeline passes where one guards the branch, with no one looking
              first. Leave a branch unticked to review its merge requests yourself; never tick a release branch such as <span class="mono">main</span>.
            </p>
            <p v-if="autoMergedUnguarded.length" class="notice notice-warning auto-merge-warning" role="note">
              {{ PIPELINE_COPY.autoMergeUnguardedWarning(names(autoMergedUnguarded)) }}
            </p>
            <p v-if="autoMergedGuarded.length" class="form-hint">{{ PIPELINE_COPY.autoMergeGuardedHint(names(autoMergedGuarded)) }}</p>
          </div>
          <div class="form-group">
            <span class="form-label">Run pipelines<DocLink topic="pipelineBranches" label="Which branches run pipelines" /></span>
            <div class="branch-list" role="group" aria-label="Run pipelines">
              <div v-for="b in protectedChoices" :key="b" class="branch-row">
                <label class="form-check branch-check">
                  <input type="checkbox" :checked="pipelineSet.has(b)" @change="togglePipelines(b, ($event.target as HTMLInputElement).checked)" />
                  <span class="mono">{{ b }}</span>
                </label>
              </div>
            </div>
            <p class="form-hint">
              Only protected branches run pipelines, since anyone who can push to a branch can change its pipeline file. {{ PIPELINE_COPY.tickedBranches }}
            </p>
          </div>
          <p v-if="error" class="form-error">{{ error }}</p>
          <div class="modal-actions">
            <button type="button" class="btn btn-secondary" @click="emit('close')">Cancel</button>
            <button type="submit" class="btn btn-primary" :disabled="saving || loading || !defaultBranch">{{ saving ? 'Saving...' : 'Save' }}</button>
          </div>
        </form>
      </div>
    </div>
  </Teleport>
</template>

<style scoped src="./shared.css"></style>
<style scoped>
.modal {
  background: var(--bg-elevated);
  border: 0.5px solid var(--border-subtle);
  border-radius: var(--radius-lg);
  width: 100%;
  max-width: 440px;
  box-shadow: var(--shadow-strong);
}

.modal-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: var(--space-4) var(--space-5);
  border-bottom: 0.5px solid var(--border-subtle);
}

.modal-title {
  font-size: 1rem;
  font-weight: 600;
}

.modal-close {
  font-size: 1.25rem;
  color: var(--text-tertiary);
  padding: var(--space-1);
  transition: color var(--transition-fast);
}

.modal-close:hover {
  color: var(--text-primary);
}

.modal-body {
  padding: var(--space-5);
}

.branch-list {
  display: flex;
  flex-direction: column;
  max-height: 220px;
  overflow-y: auto;
  padding: var(--space-1) var(--space-3);
  border: 0.5px solid var(--border-subtle);
  border-radius: var(--radius-md);
  background: var(--bg-base);
}

.branch-row {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  min-height: calc(var(--space-6) + var(--space-1));
  padding: var(--space-1) 0;
  min-width: 0;
}

.branch-check {
  flex: 1;
  min-width: 0;
}

.branch-confirm {
  flex: 1;
  min-width: 0;
  display: flex;
  gap: var(--space-1);
  font-size: 0.8rem;
  color: var(--text-primary);
}

.branch-row .mono {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.branch-note {
  margin-left: auto;
  font-size: 0.7rem;
  color: var(--text-tertiary);
}

.branch-delete {
  font-size: 0.7rem;
  color: var(--text-tertiary);
  padding: 0 var(--space-1);
  border-radius: var(--radius-sm);
  transition: color var(--transition-fast);
}

.branch-delete:hover:not(:disabled),
.branch-delete:focus-visible {
  color: var(--error);
}

.branch-delete:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

.auto-merge-warning {
  margin: var(--space-2) 0 0;
}

.modal-actions {
  display: flex;
  justify-content: flex-end;
  gap: var(--space-2);
  padding-top: var(--space-2);
}
</style>
