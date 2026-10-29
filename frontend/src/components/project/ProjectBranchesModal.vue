<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { projectApi } from '@/api/projects'
import SelectMenu from '@/components/SelectMenu.vue'
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
  },
  { immediate: true },
)

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
          <h2 class="modal-title">Branches</h2>
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
  background: var(--bg-surface);
  border: 0.5px solid var(--border-default);
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
  border-bottom: 0.5px solid var(--border-default);
}

.modal-title {
  font-size: 1rem;
  font-weight: 600;
}

.modal-close {
  font-size: 1.25rem;
  color: var(--text-muted);
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
  border: 0.5px solid var(--border-default);
  border-radius: var(--radius-md);
  background: var(--bg-primary);
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
  color: var(--text-muted);
}

.branch-delete {
  font-size: 0.7rem;
  color: var(--text-muted);
  padding: 0 var(--space-1);
  border-radius: var(--radius-sm);
  transition: color var(--transition-fast);
}

.branch-delete:hover:not(:disabled),
.branch-delete:focus-visible {
  color: var(--accent-rose);
}

.branch-delete:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

.modal-actions {
  display: flex;
  justify-content: flex-end;
  gap: var(--space-2);
  padding-top: var(--space-2);
}
</style>
