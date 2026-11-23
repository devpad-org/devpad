<script setup lang="ts">
import MrIcon from '@/components/project/MrIcon.vue'
import { computed, nextTick, ref, watch } from 'vue'
import { pipelineApi } from '@/api/pipelines'
import { useActiveProjectStore } from '@/stores/activeProject'
import ProjectCard from './ProjectCard.vue'
import type { CardStatus } from './card'

// The project's pipeline setting: the jobs a commit runs when it has no
// .devpad/pipeline.yml of its own. Only the user edits it; devpad checks it
// as it is saved and says on which line anything is wrong.
const store = useActiveProjectStore()

const editing = ref(false)
const draft = ref('')
const saving = ref(false)
const error = ref('')
const editor = ref<HTMLTextAreaElement | null>(null)
// Cancelling an edited draft asks first.
const confirmingDiscard = ref(false)

const PLACEHOLDER = `jobs:
  test:
    run: make test
    timeout: 15m
    services: [postgres]`

const config = computed(() => store.project?.pipelineConfig ?? '')
const hasConfig = computed(() => config.value.trim() !== '')
const ticked = computed(() => store.project?.pipelineBranches ?? [])

const status = computed<CardStatus>(() =>
  hasConfig.value ? { label: 'Set', tone: 'ok' } : { label: 'Not set', tone: 'neutral' },
)

watch(
  () => store.projectId,
  () => {
    editing.value = false
    error.value = ''
  },
)

function edit() {
  draft.value = config.value
  error.value = ''
  confirmingDiscard.value = false
  editing.value = true
  void nextTick(() => editor.value?.focus())
}

function cancel() {
  if (draft.value !== config.value && !confirmingDiscard.value) {
    confirmingDiscard.value = true
    return
  }
  confirmingDiscard.value = false
  editing.value = false
}

async function save() {
  const id = store.projectId
  if (id === null || saving.value) return
  saving.value = true
  error.value = ''
  try {
    const res = await pipelineApi.setConfig(id, draft.value)
    store.setProject(res.project)
    editing.value = false
  } catch (e) {
    error.value = e instanceof Error ? e.message : 'Failed to save the pipeline'
  } finally {
    saving.value = false
  }
}

function onKeydown(e: KeyboardEvent) {
  if (e.key === 'Enter' && (e.ctrlKey || e.metaKey)) {
    e.preventDefault()
    void save()
  } else if (e.key === 'Escape') {
    e.stopPropagation()
    cancel()
  } else if (e.key === 'Tab' && !e.shiftKey && editor.value) {
    // YAML is indented with spaces; a tab would not parse.
    e.preventDefault()
    const el = editor.value
    const at = el.selectionStart
    el.setRangeText('  ', at, el.selectionEnd, 'end')
    draft.value = el.value
  }
}
</script>

<template>
  <ProjectCard title="Pipeline setting" doc="pipelineSetting" doc-label="The project's pipeline setting" :status="status.label" :tone="status.tone">
    <template #icon>
      <MrIcon name="pipeline" :size="15" :stroke-width="1.8" />
    </template>

    <p class="card-lead">
      The jobs a pipeline runs when the commit it reads has no <code>.devpad/pipeline.yml</code>, in the same format.
      A pipeline file in the commit always wins over this setting, and the two are never combined.
    </p>

    <template v-if="editing">
      <textarea
        ref="editor"
        v-model="draft"
        class="pipeline-input"
        rows="10"
        wrap="off"
        spellcheck="false"
        aria-label="Pipeline setting (YAML)"
        :placeholder="PLACEHOLDER"
        @keydown="onKeydown"
      />
      <p v-if="confirmingDiscard" class="notice notice-warning card-message discard" role="alert">
        <span>Discard your changes to the pipeline setting?</span>
        <button type="button" class="btn btn-danger btn-sm" @click="cancel">Discard</button>
        <button type="button" class="btn btn-secondary btn-sm" @click="confirmingDiscard = false; editor?.focus()">Keep editing</button>
      </p>
      <p v-if="error" class="notice notice-error card-message pipeline-error" role="alert">{{ error }}</p>
      <p v-else class="card-note">Leave it empty to have none. <kbd>Ctrl</kbd> <kbd>Enter</kbd> saves.</p>
    </template>
    <template v-else>
      <pre v-if="hasConfig" class="card-code">{{ config.trim() }}</pre>
      <p v-else class="card-note">
        Not set: a commit without a pipeline file has no pipeline, and devpad says <strong>No pipeline configured</strong> rather than that it passed.
      </p>
      <p class="card-note">
        Pipelines run by hand on any protected branch and on merge requests into one.
        <template v-if="ticked.length">Shown on the Project page: <span class="mono">{{ ticked.join(', ') }}</span>.</template>
        <template v-else>Tick branches under Run pipelines in Manage branches to show their latest result here.</template>
      </p>
    </template>

    <template #actions>
      <template v-if="editing">
        <button class="btn btn-primary btn-sm" :disabled="saving" @click="save">{{ saving ? 'Saving…' : 'Save' }}</button>
        <button class="btn btn-secondary btn-sm" :disabled="saving" @click="cancel">Cancel</button>
      </template>
      <button v-else class="btn btn-secondary btn-sm" :disabled="!store.project" @click="edit">{{ hasConfig ? 'Edit' : 'Add a pipeline setting' }}</button>
    </template>
  </ProjectCard>
</template>

<style scoped src="./shared.css"></style>
<style scoped src="./card.css"></style>
<style scoped>
.pipeline-input {
  width: 100%;
  min-height: 160px;
  padding: var(--space-2) var(--space-3);
  background: var(--terminal-bg);
  border: 0.5px solid var(--border-subtle);
  border-radius: var(--radius-md);
  color: var(--text-primary);
  font-family: var(--font-mono);
  font-size: 0.78rem;
  line-height: 1.55;
  tab-size: 2;
  resize: vertical;
  transition: border-color var(--transition-fast);
}

.pipeline-input::placeholder {
  color: var(--text-dim);
}

.pipeline-input:focus {
  outline: none;
  border-color: var(--accent);
}

.discard {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: var(--space-2);
}

.discard span {
  margin-right: auto;
}

.pipeline-error {
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}

kbd {
  font-family: var(--font-mono);
  font-size: 0.68rem;
}
</style>
