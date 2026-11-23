<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref, watch } from 'vue'
import { pipelineApi, MAX_LATEST_SUBJECTS } from '@/api/pipelines'
import { projectApi } from '@/api/projects'
import DocLink from '@/components/DocLink.vue'
import SelectMenu from '@/components/SelectMenu.vue'
import { PIPELINE_COPY } from '@/components/pipeline/pipelineCopy'
import { useActiveProjectStore } from '@/stores/activeProject'
import MrIcon, { type MrIconName } from './MrIcon.vue'

// Picks the project's default branch and the other branches that change
// only through merge requests, such as main in a project that works on
// develop and releases into main, what devpad does with each by itself,
// and deletes branches no longer needed. The protected branches are one
// list with a column for each thing devpad can do with them, so a branch's
// name shows once however many columns there are; on a wide screen the
// other branches sit beside them.
const props = defineProps<{
  show: boolean
}>()

const emit = defineEmits<{
  close: []
}>()

// As the server's MaxProtectedBranches: the branches a project protects
// besides its default.
const MAX_PROTECTED_BRANCHES = 20
// The other branches get a filter once there are more than this.
const FILTER_FROM = 8

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
// Whether the project's branches loaded, so a protected branch missing from
// them is really missing.
const loaded = ref(false)
const saving = ref(false)
const error = ref('')
const query = ref('')
// The branch whose deletion awaits confirmation, and the one being deleted.
const confirming = ref('')
const deleting = ref('')
// The Cancel of the deletion being confirmed, which takes the focus from
// the button that asked, since that button goes.
const confirmCancel = ref<HTMLButtonElement[]>([])

// Protected branches first, then the rest by name.
const choices = computed(() => {
  const names = new Set(store.branches.map((b) => b.name))
  for (const b of store.protectedBranches) names.add(b)
  const initial = new Set(store.protectedBranches)
  return [...names].sort((a, b) => Number(initial.has(b)) - Number(initial.has(a)) || a.localeCompare(b))
})

// The branches protected in the edits not yet saved, the default first.
const protectedChoices = computed(() => [defaultBranch.value, ...choices.value.filter((b) => b !== defaultBranch.value && protectedSet.value.has(b))].filter(Boolean))

// The branches unprotected in the edits not yet saved, by name.
const others = computed(() =>
  choices.value.filter((b) => b !== defaultBranch.value && !protectedSet.value.has(b)).sort((a, b) => a.localeCompare(b)),
)

const shownOthers = computed(() => {
  const q = query.value.trim().toLowerCase()
  return q ? others.value.filter((b) => b.toLowerCase().includes(q)) : others.value
})

const atLimit = computed(() => protectedSet.value.size >= MAX_PROTECTED_BRANCHES)

const shas = computed(() => new Map(store.branches.map((b) => [b.name, b.sha])))

// The project's saved default branch, which stays protected when another
// becomes the default.
const savedDefault = computed(() => store.project?.defaultBranch ?? '')

// What Save sends.
const payload = computed(() => ({
  defaultBranch: defaultBranch.value,
  protectedBranches: choices.value.filter((b) => protectedSet.value.has(b)),
  // Only a protected branch can be merged into automatically.
  autoMergeBranches: protectedChoices.value.filter((b) => autoMergeSet.value.has(b)),
  // Only a protected branch runs pipelines.
  pipelineBranches: protectedChoices.value.filter((b) => pipelineSet.value.has(b)),
}))

function settingsKey(def: string, prot: string[], auto: string[], pipe: string[]): string {
  const sorted = (list: string[]) => [...list].sort().join('\n')
  return [def, sorted(prot), sorted(auto), sorted(pipe)].join('\0')
}

// Whether the edits differ from what the project has saved.
const dirty = computed(() => {
  const p = store.project
  if (!p) return false
  const saved = settingsKey(
    p.defaultBranch,
    store.protectedBranches.filter((b) => b !== p.defaultBranch),
    p.autoMergeBranches ?? [],
    p.pipelineBranches ?? [],
  )
  const next = payload.value
  return settingsKey(next.defaultBranch, next.protectedBranches, next.autoMergeBranches, next.pipelineBranches) !== saved
})

// A branch whose protection changes when the edits are saved.
function unsaved(branch: string): boolean {
  const protectedNow = branch === defaultBranch.value || protectedSet.value.has(branch)
  return protectedNow !== store.isProtected(branch)
}

// Deleting is immediate, so it is offered only for branches unprotected
// both now and in the edits not yet saved, and that nothing uses.
function deletable(branch: string): boolean {
  return shas.value.has(branch) && !store.isProtected(branch) && branch !== defaultBranch.value && !protectedSet.value.has(branch) && !usedBy(branch)
}

function ownedBySandbox(branch: string): boolean {
  return store.sandboxes.some((s) => s.branch === branch)
}

// What keeps a branch from being deleted, as devpad refuses it: a sandbox
// owning it, an open merge request from it, or a task not yet merged or
// cancelled that works on it or starts from it.
function usedBy(branch: string): string {
  if (ownedBySandbox(branch)) return 'a sandbox'
  const mr = store.openMergeRequestFor(branch)
  if (mr) return `merge request !${mr.id}`
  const task = store.tasks.find(
    (t) => t.status !== 'merged' && t.status !== 'cancelled' && (t.branch === branch || t.baseBranch === branch),
  )
  return task ? `task #${task.id}` : ''
}

// Why a branch can't be protected now, if it can't: devpad protects a
// branch only while no sandbox owns it.
function protectRefusal(branch: string): string {
  if (ownedBySandbox(branch)) return 'A sandbox owns this branch, so it can’t be protected'
  if (atLimit.value) return `A project can protect at most ${MAX_PROTECTED_BRANCHES} branches besides its default`
  return ''
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
    query.value = ''
    confirming.value = ''
    loaded.value = false
    loading.value = true
    try {
      await store.refreshBranches()
      loaded.value = true
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

type Fact = { text: string; tone: 'ok' | 'muted' | 'warn'; icon?: MrIconName }

// What a protected branch's row says under its name: whether it is in the
// project at all, and what its ticks mean as things stand.
function facts(branch: string): Fact[] {
  const out: Fact[] = []
  if (loaded.value && !shas.value.has(branch)) out.push({ text: 'not in the project', tone: 'warn', icon: 'alert' })
  if (pipelineSet.value.has(branch)) {
    const has = configured.value.get(branch)
    if (has === true) out.push({ text: 'guarded by a pipeline', tone: 'ok', icon: 'check-circle' })
    else if (has === false) out.push({ text: 'no pipeline configured', tone: 'muted' })
  }
  if (autoMergeSet.value.has(branch) && !guarded(branch)) out.push({ text: 'merges on approval alone', tone: 'warn', icon: 'warning' })
  return out
}

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

function withBranch(set: Set<string>, branch: string, on: boolean): Set<string> {
  const next = new Set(set)
  if (on) next.add(branch)
  else next.delete(branch)
  return next
}

function toggle(branch: string, on: boolean) {
  protectedSet.value = withBranch(protectedSet.value, branch, on)
  if (confirming.value === branch) confirming.value = ''
}

function toggleAutoMerge(branch: string, on: boolean) {
  autoMergeSet.value = withBranch(autoMergeSet.value, branch, on)
}

function togglePipelines(branch: string, on: boolean) {
  pipelineSet.value = withBranch(pipelineSet.value, branch, on)
}

async function confirmDelete(branch: string) {
  confirming.value = branch
  await nextTick()
  confirmCancel.value[0]?.focus()
}

function checked(event: Event): boolean {
  return (event.target as HTMLInputElement).checked
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
  if (store.projectId === null || !defaultBranch.value || !dirty.value) return
  saving.value = true
  error.value = ''
  try {
    const res = await projectApi.setBranches(store.projectId, payload.value)
    store.setProject(res.project)
    emit('close')
  } catch (e) {
    error.value = e instanceof Error ? e.message : 'Failed to save branches'
  } finally {
    saving.value = false
  }
}

// Escape clears the filter being typed in, backs out of a deletion being
// confirmed, and otherwise closes the dialog.
function onKeydown(e: KeyboardEvent) {
  if (!props.show || e.key !== 'Escape') return
  if (e.target instanceof HTMLInputElement && e.target.type === 'search' && query.value) query.value = ''
  else if (confirming.value && !deleting.value) confirming.value = ''
  else emit('close')
}

onMounted(() => document.addEventListener('keydown', onKeydown))
onUnmounted(() => document.removeEventListener('keydown', onKeydown))
</script>

<template>
  <Teleport to="body">
    <div v-if="show" class="modal-overlay" @click.self="emit('close')">
      <form class="modal" role="dialog" aria-modal="true" aria-labelledby="branches-modal-heading" @submit.prevent="save">
        <header class="modal-header">
          <div class="heading">
            <h2 id="branches-modal-heading" class="modal-title">
              <MrIcon name="branch" :size="15" class="title-icon" />
              Branches
              <DocLink topic="protectedBranches" label="Protected branches" />
            </h2>
            <p class="modal-subtitle">The branches that change only through merge requests, and what devpad does with them by itself.</p>
          </div>
          <button type="button" class="modal-close" aria-label="Close" title="Close (Esc)" @click="emit('close')">
            <MrIcon name="close" :size="16" />
          </button>
        </header>

        <div class="modal-body">
          <div class="pane pane-protected">
            <section class="group default-group">
              <div class="default-text">
                <label class="group-title" for="branches-default">Default branch</label>
                <p class="group-lead">Tasks and your project workspace start from it, and merge requests target it unless you pick another protected branch.</p>
                <p v-if="defaultBranch !== savedDefault && protectedSet.has(savedDefault)" class="group-lead default-moved">
                  <span class="mono">{{ savedDefault }}</span> stays protected.
                </p>
              </div>
              <SelectMenu
                id="branches-default"
                class="default-select"
                :model-value="defaultBranch"
                :options="choices.map((b) => ({ value: b, label: b }))"
                title="Default branch"
                variant="field"
                mono
                :disabled="loading"
                @update:model-value="pickDefault"
              />
            </section>

            <section class="group" aria-labelledby="branches-protected-heading">
              <div class="group-head">
                <h3 id="branches-protected-heading" class="group-title">
                  <MrIcon name="lock" :size="13" class="group-icon" />
                  Protected branches
                  <span class="count">{{ protectedChoices.length }}</span>
                </h3>
              </div>
              <p class="group-lead">
                They change only through merge requests: agents can't own them and your workspace can't publish to them. Merge requests can target any of them, so features can
                merge into <span class="mono">develop</span> and releases from <span class="mono">develop</span> into <span class="mono">main</span>.
              </p>

              <div class="matrix">
                <div class="matrix-head">
                  <span class="col-branch">Branch</span>
                  <span class="col-head">
                    <MrIcon name="pipeline" :size="12" />
                    Run pipelines
                    <DocLink topic="pipelineBranches" label="Which branches run pipelines" />
                  </span>
                  <span class="col-head">
                    <MrIcon name="merged" :size="12" />
                    Merge automatically
                    <DocLink topic="autoMerge" label="Automatic merges" />
                  </span>
                  <span />
                </div>
                <ul class="matrix-rows" aria-label="Protected branches">
                  <li v-for="b in protectedChoices" :key="b" class="matrix-row" :class="{ removable: b !== defaultBranch }">
                    <div class="row-branch">
                      <div class="row-name">
                        <span class="mono branch-name" :title="b">{{ b }}</span>
                        <span v-if="b === defaultBranch" class="tag tag-default">default</span>
                        <span v-if="unsaved(b)" class="tag tag-unsaved" title="Protected once you save">unsaved</span>
                      </div>
                      <ul v-if="facts(b).length" class="row-facts">
                        <li v-for="f in facts(b)" :key="f.text" class="fact" :class="`fact-${f.tone}`">
                          <MrIcon v-if="f.icon" :name="f.icon" :size="11" />
                          {{ f.text }}
                        </li>
                      </ul>
                    </div>
                    <label class="row-toggle toggle-pipelines">
                      <input
                        type="checkbox"
                        class="toggle-input"
                        :checked="pipelineSet.has(b)"
                        :aria-label="`Run pipelines on ${b}`"
                        @change="togglePipelines(b, checked($event))"
                      />
                      <span class="check" aria-hidden="true"><MrIcon name="check" :size="12" :stroke-width="3" /></span>
                      <span class="toggle-text" aria-hidden="true">Run pipelines</span>
                    </label>
                    <label class="row-toggle toggle-merge">
                      <input
                        type="checkbox"
                        class="toggle-input"
                        :checked="autoMergeSet.has(b)"
                        :aria-label="`Merge automatically into ${b}`"
                        @change="toggleAutoMerge(b, checked($event))"
                      />
                      <span class="check" aria-hidden="true"><MrIcon name="check" :size="12" :stroke-width="3" /></span>
                      <span class="toggle-text" aria-hidden="true">Merge automatically</span>
                    </label>
                    <button
                      v-if="b !== defaultBranch"
                      type="button"
                      class="ghost-btn row-remove"
                      :aria-label="`Unprotect ${b}`"
                      :title="`Stop protecting ${b}`"
                      @click="toggle(b, false)"
                    >
                      Unprotect
                    </button>
                    <span v-else class="row-remove-spacer" />
                  </li>
                </ul>
              </div>

              <p v-if="autoMergedUnguarded.length" class="notice notice-warning notice-row" role="note">
                <MrIcon name="warning" :size="14" />
                <span>{{ PIPELINE_COPY.autoMergeUnguardedWarning(names(autoMergedUnguarded)) }}</span>
              </p>
              <p v-if="autoMergedGuarded.length" class="notice notice-info notice-row" role="note">
                <MrIcon name="check-circle" :size="14" class="guarded-icon" />
                <span>{{ PIPELINE_COPY.autoMergeGuardedHint(names(autoMergedGuarded)) }}</span>
              </p>

              <dl class="legend">
                <div class="legend-item">
                  <dt><MrIcon name="pipeline" :size="12" /> Run pipelines</dt>
                  <dd>{{ PIPELINE_COPY.tickedBranches }}</dd>
                </div>
                <div class="legend-item">
                  <dt><MrIcon name="merged" :size="12" /> Merge automatically</dt>
                  <dd>
                    A task's merge request into a ticked branch merges as soon as a review agent approves it, and its pipeline passes where one guards the branch, with no one
                    looking first. Never tick a release branch such as <span class="mono">main</span>.
                  </dd>
                </div>
              </dl>
            </section>
          </div>

          <div class="pane pane-other">
            <section class="group" aria-labelledby="branches-other-heading">
              <div class="group-head">
                <h3 id="branches-other-heading" class="group-title">
                  <MrIcon name="branch" :size="13" class="group-icon" />
                  Other branches
                  <span v-if="loaded || others.length" class="count">{{ others.length }}</span>
                  <span v-if="loading" class="spinner" aria-label="Loading branches" />
                </h3>
                <label v-if="others.length > FILTER_FROM" class="search">
                  <MrIcon name="search" :size="13" />
                  <span class="sr-only">Filter branches</span>
                  <input v-model="query" type="search" placeholder="Filter" autocomplete="off" spellcheck="false" @keydown.enter.prevent />
                </label>
              </div>
              <p class="group-lead">Protect one for it to change only through merge requests. Deleting a branch happens at once, without Save.</p>

              <ul v-if="shownOthers.length" class="other-list" aria-label="Other branches">
                <li v-for="b in shownOthers" :key="b" class="other-row" :class="{ confirming: confirming === b }">
                  <template v-if="confirming === b">
                    <p class="confirm-text">
                      <MrIcon name="trash" :size="13" />
                      <span>Delete <span class="mono">{{ b }}</span> now?</span>
                    </p>
                    <div class="row-actions">
                      <button ref="confirmCancel" type="button" class="btn btn-secondary btn-sm" :disabled="deleting === b" @click="confirming = ''">Cancel</button>
                      <button type="button" class="btn btn-danger-solid btn-sm" :disabled="deleting !== ''" @click="deleteBranch(b)">
                        {{ deleting === b ? 'Deleting…' : 'Delete' }}
                      </button>
                    </div>
                  </template>
                  <template v-else>
                    <div class="other-name">
                      <span class="mono branch-name" :title="b">{{ b }}</span>
                      <span v-if="unsaved(b)" class="tag tag-unsaved" title="Unprotected once you save">unsaved</span>
                      <span v-if="usedBy(b)" class="other-note">used by {{ usedBy(b) }}</span>
                    </div>
                    <div class="row-actions">
                      <button
                        type="button"
                        class="ghost-btn"
                        :disabled="!!protectRefusal(b)"
                        :title="protectRefusal(b) || `Protect ${b}`"
                        :aria-label="`Protect ${b}`"
                        @click="toggle(b, true)"
                      >
                        <MrIcon name="lock" :size="12" />
                        Protect
                      </button>
                      <button
                        v-if="deletable(b)"
                        type="button"
                        class="icon-button delete-btn"
                        :aria-label="`Delete ${b}`"
                        :title="`Delete ${b}`"
                        :disabled="deleting !== ''"
                        @click="confirmDelete(b)"
                      >
                        <MrIcon name="trash" :size="14" />
                      </button>
                      <span v-else class="delete-spacer" />
                    </div>
                  </template>
                </li>
              </ul>
              <p v-else-if="loading" class="empty">Loading branches…</p>
              <p v-else-if="query.trim()" class="empty">No other branch matches “{{ query.trim() }}”.</p>
              <p v-else-if="loaded" class="empty">Every branch in the project is protected.</p>
            </section>
          </div>
        </div>

        <footer class="modal-footer">
          <p v-if="error" class="footer-error" role="alert">
            <MrIcon name="alert" :size="14" />
            <span>{{ error }}</span>
          </p>
          <p v-else-if="dirty" class="footer-status"><span class="dirty-dot" aria-hidden="true" />Unsaved changes</p>
          <div class="footer-actions">
            <button type="button" class="btn btn-secondary" @click="emit('close')">{{ dirty ? 'Cancel' : 'Close' }}</button>
            <button type="submit" class="btn btn-primary" :disabled="saving || loading || !defaultBranch || !dirty">
              <span v-if="saving" class="spinner spinner-on-accent" />
              {{ saving ? 'Saving…' : 'Save' }}
            </button>
          </div>
        </footer>
      </form>
    </div>
  </Teleport>
</template>

<style scoped src="./shared.css"></style>
<style scoped>
/* One surface: a header, a body that scrolls between it and the footer,
   and flat groups divided by hairlines. */
.modal {
  display: flex;
  flex-direction: column;
  width: 100%;
  max-width: 640px;
  max-height: calc(100dvh - 2 * var(--space-4));
  background: var(--bg-elevated);
  border: 0.5px solid var(--border-subtle);
  border-radius: var(--radius-lg);
  box-shadow: var(--shadow-strong);
  overflow: hidden;
}

.modal-header {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: var(--space-3);
  flex-shrink: 0;
  padding: var(--space-4) var(--space-5);
  border-bottom: 0.5px solid var(--border-subtle);
}

.heading {
  flex: 1;
  min-width: 0;
}

.modal-title {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  font-size: 1rem;
  font-weight: 600;
  line-height: 1.4;
}

.title-icon {
  color: var(--text-tertiary);
}

.modal-subtitle {
  margin-top: 2px;
  font-size: 0.78rem;
  color: var(--text-tertiary);
}

.modal-close {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  flex-shrink: 0;
  width: 28px;
  height: 28px;
  margin: -2px calc(var(--space-1) * -1) 0 0;
  border-radius: var(--radius-md);
  color: var(--text-tertiary);
  transition: all var(--transition-fast);
}

.modal-close:hover {
  color: var(--text-primary);
  background: var(--bg-hover);
}

.modal-body {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
}

/* The protected branches, then the others: one above the other, or side by
   side on a wide screen. */
.pane {
  min-width: 0;
  container: branch-pane / inline-size;
}

.pane + .pane {
  border-top: 0.5px solid var(--border-hairline);
}

/* Groups */
.group {
  padding: var(--space-5);
}

.group + .group {
  border-top: 0.5px solid var(--border-hairline);
}

.group-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-3);
  min-height: 26px;
}

.group-title {
  display: flex;
  align-items: center;
  gap: 6px;
  font-size: 0.85rem;
  font-weight: 600;
  color: var(--text-primary);
}

.group-icon {
  color: var(--text-tertiary);
}

.group-lead {
  margin-top: 2px;
  font-size: 0.75rem;
  line-height: 1.5;
  color: var(--text-tertiary);
}

.group-head + .group-lead {
  margin-bottom: var(--space-3);
}

.count {
  padding: 0 6px;
  border-radius: 999px;
  background: var(--bg-neutral);
  color: var(--text-secondary);
  font-size: 0.68rem;
  font-weight: 500;
  font-variant-numeric: tabular-nums;
  line-height: 1.5;
}

.mono {
  font-family: var(--font-mono);
}

/* A name shows whole, wrapping if it must: it is what tells branches
   apart before one is deleted. */
.branch-name {
  min-width: 0;
  overflow-wrap: anywhere;
  font-size: 0.8rem;
  line-height: 1.4;
  color: var(--text-primary);
}

.tag {
  flex-shrink: 0;
  padding: 0 6px;
  border-radius: var(--radius-sm);
  font-size: 0.66rem;
  font-weight: 500;
  line-height: 1.6;
}

.tag-default {
  background: var(--accent-glow);
  color: var(--accent);
}

.tag-unsaved {
  border: 0.5px solid var(--border-strong);
  color: var(--text-secondary);
}

/* Default branch: its words, with the picker at the end of the row. */
.default-group {
  display: flex;
  align-items: center;
  gap: var(--space-5);
}

.default-text {
  flex: 1;
  min-width: 0;
}

.default-moved {
  color: var(--text-secondary);
}

.default-select {
  flex: 0 0 220px;
  min-width: 0;
}

/* Protected branches: a row per branch, a column per thing devpad does
   with it. */
.matrix {
  --col-toggle: 132px;
  --col-end: 84px;
  border: 0.5px solid var(--border-subtle);
  border-radius: var(--radius-md);
  background: var(--bg-base);
  overflow: hidden;
}

.matrix-head,
.matrix-row {
  display: grid;
  grid-template-columns: minmax(0, 1fr) var(--col-toggle) var(--col-toggle) var(--col-end);
  align-items: center;
  column-gap: var(--space-2);
  padding: 0 var(--space-2) 0 var(--space-3);
}

.matrix-head {
  min-height: 32px;
  border-bottom: 0.5px solid var(--border-subtle);
  font-size: 0.7rem;
  font-weight: 500;
  color: var(--text-tertiary);
}

.col-head {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 5px;
  white-space: nowrap;
}

.col-head .doc-link {
  margin-left: 0;
}

.matrix-rows {
  margin: 0;
  padding: 0;
  list-style: none;
}

.matrix-row {
  min-height: 48px;
  transition: background var(--transition-fast);
}

.matrix-row + .matrix-row {
  border-top: 0.5px solid var(--border-hairline);
}

.matrix-row:hover {
  background: var(--bg-hover);
}

.row-branch {
  display: flex;
  flex-direction: column;
  justify-content: center;
  gap: 2px;
  min-width: 0;
  padding: var(--space-2) 0;
}

.row-name {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 2px 6px;
  min-width: 0;
}

.row-facts {
  display: flex;
  flex-wrap: wrap;
  gap: 2px var(--space-3);
  margin: 0;
  padding: 0;
  list-style: none;
}

.fact {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  font-size: 0.7rem;
}

.fact-ok {
  color: var(--success);
}

.fact-muted {
  color: var(--text-tertiary);
}

.fact-warn {
  color: var(--warning);
}

/* A tick fills its cell, so the whole cell is the target. */
.row-toggle {
  position: relative;
  display: flex;
  align-items: center;
  justify-content: center;
  gap: var(--space-2);
  align-self: stretch;
  min-height: 40px;
  cursor: pointer;
}

.toggle-input {
  position: absolute;
  opacity: 0;
  width: 0;
  height: 0;
}

.check {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  flex-shrink: 0;
  width: 18px;
  height: 18px;
  border: 1px solid var(--border-strong);
  border-radius: var(--radius-md);
  background: var(--bg-elevated);
  color: var(--text-on-accent);
  transition:
    background var(--transition-fast),
    border-color var(--transition-fast);
}

.check .mr-icon {
  opacity: 0;
  transform: scale(0.6);
  transition:
    opacity var(--transition-fast),
    transform var(--transition-fast);
}

.row-toggle:hover .check {
  border-color: var(--text-tertiary);
}

.toggle-input:checked + .check {
  background: var(--accent);
  border-color: var(--accent);
}

.toggle-input:checked + .check .mr-icon {
  opacity: 1;
  transform: none;
}

.toggle-input:focus-visible + .check {
  box-shadow: 0 0 0 3px var(--accent-glow), 0 0 0 1px var(--accent-border);
}

/* The column heads say what a tick is on a wide screen. */
.toggle-text {
  display: none;
  font-size: 0.78rem;
  color: var(--text-secondary);
}

/* Protect and Unprotect: quiet beside each row, since every row has one. */
.ghost-btn {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 5px;
  min-height: 28px;
  padding: 0 var(--space-2);
  border: 0.5px solid transparent;
  border-radius: var(--radius-md);
  font-size: 0.75rem;
  font-weight: 500;
  color: var(--text-secondary);
  white-space: nowrap;
  transition: all var(--transition-fast);
}

.ghost-btn:hover:not(:disabled) {
  color: var(--text-primary);
  background: var(--bg-hover);
  border-color: var(--border-subtle);
}

.ghost-btn:disabled {
  opacity: 0.45;
  cursor: not-allowed;
}

.row-remove {
  justify-self: end;
  color: var(--text-tertiary);
}

.notice-row {
  display: flex;
  align-items: flex-start;
  gap: var(--space-2);
  margin: var(--space-3) 0 0;
  font-size: 0.75rem;
}

.notice-row .mr-icon {
  margin-top: 2px;
  flex-shrink: 0;
}

.notice-row + .notice-row {
  margin-top: var(--space-2);
}

.guarded-icon {
  color: var(--success);
}

/* What each column does, beside each other under the list. */
.legend {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: var(--space-3) var(--space-5);
  margin-top: var(--space-4);
}

.legend dt {
  display: flex;
  align-items: center;
  gap: 6px;
  margin-bottom: 2px;
  font-size: 0.75rem;
  font-weight: 500;
  color: var(--text-secondary);
}

.legend dt .mr-icon {
  color: var(--text-tertiary);
}

.legend dd {
  font-size: 0.72rem;
  line-height: 1.5;
  color: var(--text-tertiary);
}

/* Other branches */
.search {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  width: 200px;
  max-width: 100%;
  height: 28px;
  padding: 0 var(--space-2);
  border: 0.5px solid var(--border-subtle);
  border-radius: var(--radius-md);
  background: var(--bg-base);
  color: var(--text-tertiary);
  transition: border-color var(--transition-fast);
}

.search:focus-within {
  border-color: var(--accent);
}

.search input {
  flex: 1;
  min-width: 0;
  background: transparent;
  border: none;
  color: var(--text-primary);
  font-family: inherit;
  font-size: 0.78rem;
}

.search input:focus {
  outline: none;
}

.other-list {
  margin: 0;
  padding: 0;
  list-style: none;
  border: 0.5px solid var(--border-subtle);
  border-radius: var(--radius-md);
  background: var(--bg-base);
  overflow: hidden;
}

.other-row {
  display: flex;
  align-items: center;
  gap: var(--space-3);
  min-height: 44px;
  padding: var(--space-1) var(--space-2) var(--space-1) var(--space-3);
  transition: background var(--transition-fast);
}

.other-row + .other-row {
  border-top: 0.5px solid var(--border-hairline);
}

.other-row:hover {
  background: var(--bg-hover);
}

.other-row.confirming {
  background: var(--error-bg);
}

.other-name {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 2px var(--space-2);
  flex: 1;
  min-width: 0;
}

.other-name .branch-name {
  max-width: 100%;
}

.other-note {
  font-size: 0.7rem;
  color: var(--text-tertiary);
}

.row-actions {
  display: flex;
  align-items: center;
  gap: var(--space-1);
  flex-shrink: 0;
  margin-left: auto;
}

.icon-button,
.delete-spacer {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  flex-shrink: 0;
  width: 28px;
  height: 28px;
  border-radius: var(--radius-md);
}

.icon-button {
  color: var(--text-tertiary);
  transition: all var(--transition-fast);
}

.delete-btn:hover:not(:disabled) {
  color: var(--error);
  background: var(--error-bg);
}

.icon-button:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

.confirm-text {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  flex: 1;
  min-width: 0;
  font-size: 0.8rem;
  color: var(--error);
  overflow-wrap: anywhere;
}

.confirm-text .mono {
  color: var(--text-primary);
}

.btn-danger-solid {
  background: var(--error);
  color: var(--text-on-accent);
}

.btn-danger-solid:hover:not(:disabled) {
  background: color-mix(in srgb, var(--error) 85%, var(--fill-hover-mix));
}

.empty {
  padding: var(--space-4) var(--space-3);
  border: 0.5px dashed var(--border-subtle);
  border-radius: var(--radius-md);
  font-size: 0.78rem;
  color: var(--text-tertiary);
  text-align: center;
}

/* Footer */
.modal-footer {
  display: flex;
  align-items: center;
  gap: var(--space-3);
  flex-shrink: 0;
  padding: var(--space-3) var(--space-5);
  border-top: 0.5px solid var(--border-subtle);
  background: var(--bg-elevated);
}

.footer-status,
.footer-error {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  min-width: 0;
  font-size: 0.78rem;
}

.footer-status {
  color: var(--text-secondary);
}

.footer-error {
  align-items: flex-start;
  color: var(--error);
  overflow-wrap: anywhere;
}

.footer-error .mr-icon {
  margin-top: 2px;
  flex-shrink: 0;
}

.dirty-dot {
  width: 7px;
  height: 7px;
  border-radius: 50%;
  background: var(--accent);
}

.footer-actions {
  display: flex;
  gap: var(--space-2);
  margin-left: auto;
}

.spinner {
  display: inline-block;
  width: 12px;
  height: 12px;
  border: 1.5px solid var(--spinner-track);
  border-top-color: var(--accent);
  border-radius: 50%;
  animation: spin 0.8s linear infinite;
  flex-shrink: 0;
}

.spinner-on-accent {
  border-color: color-mix(in srgb, var(--text-on-accent) 30%, transparent);
  border-top-color: var(--text-on-accent);
}

.sr-only {
  position: absolute;
  width: 1px;
  height: 1px;
  overflow: hidden;
  clip: rect(0 0 0 0);
  white-space: nowrap;
}

/* A narrow pane: each protected branch is a card of its own, its ticks
   labelled beneath its name since the column heads no longer fit. */
@container branch-pane (max-width: 540px) {
  .default-group {
    flex-direction: column;
    align-items: stretch;
    gap: var(--space-3);
  }

  .default-select {
    flex-basis: auto;
  }

  .matrix {
    border: none;
    background: none;
    overflow: visible;
  }

  .matrix-head {
    display: none;
  }

  .matrix-rows {
    display: flex;
    flex-direction: column;
    gap: var(--space-2);
  }

  .matrix-row {
    grid-template-columns: repeat(2, minmax(0, 1fr));
    gap: var(--space-2);
    padding: var(--space-3);
    border: 0.5px solid var(--border-subtle);
    border-radius: var(--radius-md);
    background: var(--bg-base);
  }

  .matrix-row + .matrix-row {
    border-top: 0.5px solid var(--border-subtle);
  }

  .matrix-row:hover {
    background: var(--bg-base);
  }

  .row-branch {
    grid-column: 1 / -1;
    grid-row: 1;
    padding: 0;
  }

  .removable .row-branch {
    padding-right: 88px;
  }

  .row-remove {
    grid-column: 2;
    grid-row: 1;
    align-self: start;
    margin: -6px -6px 0 0;
  }

  .row-remove-spacer {
    display: none;
  }

  .row-toggle {
    justify-content: flex-start;
    min-height: 40px;
    padding: var(--space-2) var(--space-3);
    border: 0.5px solid var(--border-subtle);
    border-radius: var(--radius-md);
    background: var(--bg-elevated);
    transition:
      background var(--transition-fast),
      border-color var(--transition-fast);
  }

  .row-toggle:has(.toggle-input:checked) {
    border-color: var(--accent-border);
    background: var(--accent-glow);
  }

  .row-toggle:has(.toggle-input:checked) .toggle-text {
    color: var(--text-primary);
  }

  .toggle-text {
    display: inline;
    min-width: 0;
    line-height: 1.3;
  }

  .legend {
    grid-template-columns: minmax(0, 1fr);
  }

  .search {
    width: 140px;
  }

  .other-row {
    flex-wrap: wrap;
    padding: var(--space-2) var(--space-2) var(--space-2) var(--space-3);
  }

  .other-row.confirming .row-actions {
    width: 100%;
    justify-content: flex-end;
  }
}

/* A wide screen: the protected branches beside the others, each pane
   scrolling on its own, so a long list of other branches never pushes the
   protected ones out of sight. The other branches' list scrolls under its
   heading and filter. The dialog keeps one height, so a tick that brings a
   warning or takes it away doesn't resize it. */
@media (min-width: 1000px) and (min-height: 600px) {
  .modal {
    max-width: 1080px;
    height: min(800px, calc(100dvh - 2 * var(--space-4)));
  }

  .modal-body {
    display: grid;
    grid-template-columns: minmax(0, 1.6fr) minmax(0, 1fr);
    grid-template-rows: minmax(0, 1fr);
    overflow: hidden;
  }

  .pane-protected {
    overflow-y: auto;
  }

  .pane + .pane {
    border-top: none;
    border-left: 0.5px solid var(--border-subtle);
  }

  .pane-other {
    display: flex;
    flex-direction: column;
    background: color-mix(in srgb, var(--bg-base) 45%, var(--bg-elevated));
  }

  .pane-other .group {
    display: flex;
    flex-direction: column;
    flex: 1;
    min-height: 0;
  }

  .pane-other .other-list {
    flex: 0 1 auto;
    min-height: 0;
    overflow-y: auto;
  }
}

/* Matches MOBILE_MEDIA_QUERY in useMediaQuery.ts. The sheet scrolls with the
   overlay, and the footer stays in reach at the bottom of the screen. `clip`
   rather than `hidden` keeps the overlay the footer's scroll container. */
@media (max-width: 768px), (pointer: coarse) and (max-height: 500px) {
  .modal {
    max-height: none;
    overflow: clip;
  }

  .modal-header {
    padding: var(--space-3) var(--space-4);
  }

  .modal-body {
    flex: 1 0 auto;
    overflow: visible;
  }

  .group {
    padding: var(--space-4);
  }

  .modal-footer {
    position: sticky;
    bottom: 0;
    z-index: 1;
    flex-wrap: wrap;
    padding: var(--space-3) var(--space-4);
  }

  .footer-actions {
    flex: 1 0 auto;
  }

  .footer-actions .btn {
    flex: 1;
  }

  .icon-button,
  .delete-spacer {
    width: 40px;
    height: 40px;
  }

  .ghost-btn {
    min-height: 40px;
    padding: 0 var(--space-3);
    border-color: var(--border-subtle);
  }

  .search {
    height: 36px;
  }
}
</style>
