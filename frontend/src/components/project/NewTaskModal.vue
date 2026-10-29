<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref, watch } from 'vue'
import { taskApi, type Task } from '@/api/tasks'
import MarkdownMessage from '@/components/ide/MarkdownMessage.vue'
import { useActiveProjectStore } from '@/stores/activeProject'
import AgentModelPicker from './AgentModelPicker.vue'
import MrIcon from './MrIcon.vue'

// Creates a task, either left to do or handed to an agent at once. Closing
// keeps the draft, so an accidental Escape loses nothing; creating clears it.
const props = defineProps<{
  show: boolean
  projectId: number
}>()

const emit = defineEmits<{
  close: []
  created: [task: Task]
}>()

const store = useActiveProjectStore()

const title = ref('')
const body = ref('')
const descriptionTab = ref<'write' | 'preview'>('write')
const base = ref('')
const model = ref('')
const effort = ref('')
// The budget as typed, in USD; empty takes the default. v-model on a number
// input stores a number once anything is typed.
const budget = ref<number | string>('')
const busy = ref<'' | 'save' | 'start'>('')
const error = ref('')
const titleInput = ref<HTMLInputElement | null>(null)

const defaultBranch = computed(() => store.project?.defaultBranch ?? '')
// The protected branches first, in the project's order, then the rest.
const bases = computed(() => {
  const names = new Set(store.branches.map((b) => b.name))
  const others = [...names].filter((name) => !store.isProtected(name)).sort((a, b) => a.localeCompare(b))
  const protectedFirst = store.protectedBranches.filter((name) => names.has(name) || name === defaultBranch.value)
  return [...protectedFirst, ...others]
})

watch(
  () => props.show,
  async (visible) => {
    if (!visible) return
    error.value = ''
    if (!base.value) base.value = defaultBranch.value
    void nextTick(() => titleInput.value?.focus())
    try {
      await store.refreshBranches()
    } catch {
      // The default branch is always there to start from.
    }
    if (!bases.value.includes(base.value)) base.value = defaultBranch.value
  },
  { immediate: true },
)

function parsedBudget(): number | undefined | null {
  if (String(budget.value).trim() === '') return undefined
  const usd = Number(budget.value)
  return Number.isFinite(usd) && usd >= 0 ? usd : null
}

async function submit(start: boolean) {
  if (busy.value) return
  if (!title.value.trim()) {
    error.value = 'A task needs a title'
    titleInput.value?.focus()
    return
  }
  if (start && !model.value) {
    error.value = 'Pick a model for the agent'
    return
  }
  const budgetUsd = parsedBudget()
  if (budgetUsd === null) {
    error.value = 'The budget must be an amount in USD, or empty for the default'
    return
  }
  busy.value = start ? 'start' : 'save'
  error.value = ''
  try {
    const res = await taskApi.create(props.projectId, {
      title: title.value.trim(),
      body: body.value,
      start,
      baseBranch: base.value && base.value !== defaultBranch.value ? base.value : undefined,
      budgetUsd,
      model: model.value || undefined,
      effort: effort.value || undefined,
    })
    title.value = ''
    body.value = ''
    budget.value = ''
    descriptionTab.value = 'write'
    emit('created', res.task)
  } catch (e) {
    error.value = e instanceof Error ? e.message : 'Failed to create the task'
  } finally {
    busy.value = ''
  }
}

function onKeydown(e: KeyboardEvent) {
  if (!props.show) return
  if (e.key === 'Escape') {
    emit('close')
  } else if (e.key === 'Enter' && (e.ctrlKey || e.metaKey)) {
    e.preventDefault()
    void submit(true)
  }
}

onMounted(() => document.addEventListener('keydown', onKeydown))
onUnmounted(() => document.removeEventListener('keydown', onKeydown))
</script>

<template>
  <Teleport to="body">
    <div v-if="show" class="modal-overlay" @click.self="emit('close')">
      <form
        class="modal"
        role="dialog"
        aria-modal="true"
        aria-labelledby="new-task-heading"
        @submit.prevent="submit(true)"
      >
        <header class="modal-header">
          <h2 id="new-task-heading" class="modal-title">
            <MrIcon name="task" :size="15" class="title-icon" />
            New task
          </h2>
          <button type="button" class="modal-close" aria-label="Close" title="Close (Esc)" @click="emit('close')">
            <MrIcon name="close" :size="16" />
          </button>
        </header>

        <div class="modal-body">
          <div class="main-col">
            <div class="field">
              <label class="form-label" for="new-task-title">Title</label>
              <input
                id="new-task-title"
                ref="titleInput"
                v-model="title"
                class="form-input title-input"
                type="text"
                autocomplete="off"
                placeholder="Add rate limiting to the login endpoint"
              />
            </div>

            <div class="field">
              <div class="field-head">
                <label class="form-label" for="new-task-body">Description</label>
                <span class="field-head-end segmented" role="tablist" aria-label="Description view">
                  <button
                    type="button"
                    role="tab"
                    class="segment"
                    :class="{ active: descriptionTab === 'write' }"
                    :aria-selected="descriptionTab === 'write'"
                    @click="descriptionTab = 'write'"
                  >
                    Write
                  </button>
                  <button
                    type="button"
                    role="tab"
                    class="segment"
                    :class="{ active: descriptionTab === 'preview' }"
                    :aria-selected="descriptionTab === 'preview'"
                    @click="descriptionTab = 'preview'"
                  >
                    Preview
                  </button>
                </span>
              </div>
              <textarea
                v-if="descriptionTab === 'write'"
                id="new-task-body"
                v-model="body"
                class="form-input form-textarea description-input"
                rows="12"
                placeholder="What should change, and how will we know it works? Markdown works."
              />
              <div v-else class="description-preview">
                <MarkdownMessage v-if="body.trim()" :content="body" />
                <p v-else class="muted">Nothing to preview yet.</p>
              </div>
            </div>

            <p v-if="error" class="notice notice-error notice-row" role="alert">
              <MrIcon name="alert" :size="14" />
              {{ error }}
            </p>
          </div>

          <aside class="side-col">
            <section class="side-section">
              <h3 class="side-title">Start from</h3>
              <label class="sr-only" for="new-task-base">Branch to start from</label>
              <select id="new-task-base" v-model="base" class="form-input form-mono">
                <option v-for="name in bases" :key="name" :value="name">{{ name }}</option>
              </select>
              <p class="form-hint">The agent works on a branch of its own, made from this one.</p>
            </section>

            <section class="side-section">
              <h3 class="side-title">Agent</h3>
              <AgentModelPicker v-model:model="model" v-model:effort="effort" />
            </section>

            <section class="side-section">
              <h3 class="side-title">Budget</h3>
              <label class="sr-only" for="new-task-budget">Budget in USD</label>
              <div class="budget-field">
                <span class="budget-prefix" aria-hidden="true">$</span>
                <input
                  id="new-task-budget"
                  v-model="budget"
                  class="form-input budget-input"
                  type="number"
                  min="0"
                  step="any"
                  inputmode="decimal"
                  placeholder="Default"
                />
              </div>
              <p class="form-hint">What agents may spend on this task across all their runs. Empty uses the default; 0 means no limit.</p>
            </section>
          </aside>
        </div>

        <footer class="modal-footer">
          <span class="form-hint footer-hint"><kbd>Ctrl</kbd> <kbd>Enter</kbd> to create and start</span>
          <button type="button" class="btn btn-secondary" @click="emit('close')">Cancel</button>
          <button type="button" class="btn btn-secondary" :disabled="!!busy" @click="submit(false)">
            <span v-if="busy === 'save'" class="spinner spinner-sm" />
            Save as to do
          </button>
          <button type="submit" class="btn btn-primary" :disabled="!!busy || !model">
            <span v-if="busy === 'start'" class="spinner spinner-sm spinner-on-accent" />
            <MrIcon v-else name="play" :size="13" />
            Create and start agent
          </button>
        </footer>
      </form>
    </div>
  </Teleport>
</template>

<style scoped src="./shared.css"></style>
<style scoped src="@/assets/styles/segmented.css"></style>
<style scoped src="./taskModal.css"></style>
<style scoped>
.modal-header {
  align-items: center;
}

.title-icon {
  color: var(--text-muted);
}

.title-input {
  padding-top: 10px;
  padding-bottom: 10px;
  font-size: 0.95rem;
  font-weight: 500;
}

.title-input::placeholder,
.description-input::placeholder {
  color: var(--text-dim);
}

/* Smaller, to sit in the field's header. */
.segment {
  padding: 1px var(--space-2);
  font-size: 0.72rem;
}

/* Write and Preview share one frame, so switching doesn't shift the layout. */
.description-input,
.description-preview {
  min-height: 260px;
}

.description-preview {
  max-height: 50vh;
  overflow-y: auto;
  padding: var(--space-2) var(--space-3);
  background: var(--bg-base);
  border: 0.5px solid var(--border-default);
  border-radius: var(--radius-md);
  font-size: 0.85rem;
}

.budget-field {
  position: relative;
}

.budget-prefix {
  position: absolute;
  left: var(--space-3);
  top: 50%;
  transform: translateY(-50%);
  color: var(--text-muted);
  font-size: 0.85rem;
  pointer-events: none;
}

.budget-input {
  padding-left: calc(var(--space-3) + 0.8em);
}
</style>
