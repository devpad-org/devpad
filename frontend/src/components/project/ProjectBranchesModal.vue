<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { projectApi } from '@/api/projects'
import { useActiveProjectStore } from '@/stores/activeProject'

// Picks the project's default branch and the other branches that change
// only through merge requests, such as main in a project that works on
// develop and releases into main.
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

// Protected branches first, then the rest by name.
const choices = computed(() => {
  const names = new Set(store.branches.map((b) => b.name))
  for (const b of store.protectedBranches) names.add(b)
  const initial = new Set(store.protectedBranches)
  return [...names].sort((a, b) => Number(initial.has(b)) - Number(initial.has(a)) || a.localeCompare(b))
})

watch(
  () => props.show,
  async (visible) => {
    if (!visible || !store.project) return
    defaultBranch.value = store.project.defaultBranch
    protectedSet.value = new Set(store.protectedBranches.filter((b) => b !== store.project?.defaultBranch))
    error.value = ''
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
            <select
              id="branches-default"
              :value="defaultBranch"
              class="form-input form-mono"
              :disabled="loading"
              @change="pickDefault(($event.target as HTMLSelectElement).value)"
            >
              <option v-for="b in choices" :key="b" :value="b">{{ b }}</option>
            </select>
            <p class="form-hint">Tasks and your project workspace start from it, and merge requests target it unless you pick another protected branch.</p>
          </div>
          <div class="form-group">
            <span class="form-label">Protected branches</span>
            <div class="branch-list" role="group" aria-label="Protected branches">
              <label v-for="b in choices" :key="b" class="form-check branch-row">
                <input
                  type="checkbox"
                  :checked="b === defaultBranch || protectedSet.has(b)"
                  :disabled="b === defaultBranch"
                  @change="toggle(b, ($event.target as HTMLInputElement).checked)"
                />
                <span class="mono">{{ b }}</span>
                <span v-if="b === defaultBranch" class="branch-note">default</span>
              </label>
              <p v-if="loading" class="form-hint">Loading branches...</p>
            </div>
            <p class="form-hint">
              Protected branches change only through merge requests: agents can't own them and your workspace can't publish to them. Merge requests can target any of them,
              so features can merge into <span class="mono">develop</span> and releases from <span class="mono">develop</span> into <span class="mono">main</span>.
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
  padding: var(--space-1) 0;
  min-width: 0;
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

.modal-actions {
  display: flex;
  justify-content: flex-end;
  gap: var(--space-2);
  padding-top: var(--space-2);
}
</style>
