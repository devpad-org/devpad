<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { mergeRequestApi } from '@/api/mergeRequests'
import SelectMenu from '@/components/SelectMenu.vue'
import { useActiveProjectStore } from '@/stores/activeProject'

// Opens a merge request between two branches the project already has, such
// as a release of develop into main. Branches only in the project
// workspace are published from the Source Control panel first.
const props = defineProps<{
  show: boolean
}>()

const emit = defineEmits<{
  close: []
}>()

const store = useActiveProjectStore()

const source = ref('')
const target = ref('')
const title = ref('')
const description = ref('')
const loading = ref(false)
const saving = ref(false)
const error = ref('')
// The title follows the branches until the user edits it.
let titleEdited = false

const sources = computed(() => store.branches.map((b) => b.name).sort((a, b) => a.localeCompare(b)))
const targets = computed(() => store.protectedBranches.filter((b) => b !== source.value))
const duplicate = computed(() => store.openMergeRequestFor(source.value))

function suggestedTitle(): string {
  if (!source.value || !target.value) return ''
  if (store.isProtected(source.value)) return `Release ${source.value} into ${target.value}`
  return source.value.replace(/^[^/]+\//, '').replace(/[-_]+/g, ' ')
}

watch(
  () => props.show,
  async (visible) => {
    if (!visible) return
    error.value = ''
    description.value = ''
    titleEdited = false
    loading.value = true
    try {
      await store.refreshBranches()
    } catch (e) {
      error.value = e instanceof Error ? e.message : 'Failed to load branches'
    } finally {
      loading.value = false
    }
    // With more than one protected branch, the usual merge request opened
    // here is a release from the default branch into the next one.
    const [first, next] = store.protectedBranches
    if (first && next) {
      source.value = first
      target.value = next
    } else {
      source.value = sources.value.find((b) => !store.isProtected(b)) ?? ''
      target.value = first ?? ''
    }
    title.value = suggestedTitle()
  },
  { immediate: true },
)

watch(source, () => {
  if (!targets.value.includes(target.value)) target.value = targets.value[0] ?? ''
})

watch([source, target], () => {
  if (!titleEdited) title.value = suggestedTitle()
})

function markTitleEdited() {
  titleEdited = true
}

async function submit() {
  if (store.projectId === null || !source.value || !target.value || !title.value.trim()) return
  saving.value = true
  error.value = ''
  try {
    const res = await mergeRequestApi.create(store.projectId, {
      title: title.value.trim(),
      description: description.value.trim(),
      sourceBranch: source.value,
      targetBranch: target.value,
    })
    void store.refresh()
    emit('close')
    store.selectedMergeRequestId = res.mergeRequest.id
  } catch (e) {
    error.value = e instanceof Error ? e.message : 'Failed to open the merge request'
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
          <h2 class="modal-title">New merge request</h2>
          <button class="modal-close" aria-label="Close" @click="emit('close')">&times;</button>
        </div>
        <form class="modal-body" @submit.prevent="submit">
          <div class="branch-pair">
            <div class="form-group">
              <label class="form-label" for="new-mr-source">Merge</label>
              <SelectMenu
                id="new-mr-source"
                v-model="source"
                :options="sources.map((b) => ({ value: b, label: b }))"
                title="Merge"
                variant="field"
                mono
                :disabled="loading"
              />
            </div>
            <span class="branch-arrow" aria-hidden="true">&rarr;</span>
            <div class="form-group">
              <label class="form-label" for="new-mr-target">Into</label>
              <SelectMenu
                id="new-mr-target"
                v-model="target"
                :options="targets.map((b) => ({ value: b, label: b }))"
                title="Into"
                variant="field"
                mono
                :disabled="loading || targets.length === 0"
              />
            </div>
          </div>
          <p v-if="duplicate" class="notice notice-warning">
            <span class="mono">{{ source }}</span> already has an open merge request, !{{ duplicate.id }}.
          </p>
          <p v-else class="form-hint pair-hint">Merge requests target protected branches. Set them from Branches in the project panel.</p>
          <div class="form-group">
            <label class="form-label" for="new-mr-title">Title</label>
            <input id="new-mr-title" v-model="title" class="form-input" type="text" autocomplete="off" @input="markTitleEdited" />
          </div>
          <div class="form-group">
            <label class="form-label" for="new-mr-description">Description</label>
            <textarea id="new-mr-description" v-model="description" class="form-input form-textarea" rows="3" placeholder="Optional" />
          </div>
          <p v-if="error" class="form-error">{{ error }}</p>
          <div class="modal-actions">
            <button type="button" class="btn btn-secondary" @click="emit('close')">Cancel</button>
            <button type="submit" class="btn btn-primary" :disabled="saving || loading || !source || !target || !title.trim() || !!duplicate">
              {{ saving ? 'Opening...' : 'Open' }}
            </button>
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
  max-width: 480px;
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

.branch-pair {
  display: grid;
  grid-template-columns: minmax(0, 1fr) auto minmax(0, 1fr);
  align-items: end;
  gap: var(--space-2);
}

.branch-arrow {
  padding-bottom: var(--space-2);
  color: var(--text-muted);
}

.branch-pair .form-group {
  margin-bottom: 0;
}

.pair-hint {
  margin-bottom: var(--space-4);
}

.notice {
  margin: var(--space-2) 0 var(--space-4);
}

.modal-actions {
  display: flex;
  justify-content: flex-end;
  gap: var(--space-2);
  padding-top: var(--space-2);
}
</style>
