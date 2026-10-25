<script setup lang="ts">
import { ref, watch } from 'vue'
import type { Project, ProjectInput } from '@/api/projects'

const props = defineProps<{
  show: boolean
  project?: Project | null
  saving?: boolean
  error?: string
}>()

const emit = defineEmits<{
  close: []
  submit: [input: ProjectInput]
}>()

const DEFAULT_MAX_SANDBOXES = 3
const MAX_SANDBOXES_LIMIT = 20

const name = ref('')
const description = ref('')
const defaultBranch = ref('')
const upstreamUrl = ref('')
const setupScript = ref('')
const environment = ref('')
const maxSandboxes = ref(DEFAULT_MAX_SANDBOXES)
const localError = ref('')

watch(
  () => props.show,
  (visible) => {
    if (!visible) return
    const p = props.project
    name.value = p?.name ?? ''
    description.value = p?.description ?? ''
    defaultBranch.value = p?.defaultBranch ?? ''
    upstreamUrl.value = p?.upstreamUrl ?? ''
    setupScript.value = p?.setupScript ?? ''
    environment.value = p?.environment ?? ''
    maxSandboxes.value = p?.maxSandboxes ?? DEFAULT_MAX_SANDBOXES
    localError.value = ''
  },
  { immediate: true },
)

function handleSubmit() {
  if (!name.value.trim()) {
    localError.value = 'Name is required'
    return
  }
  const max = Number(maxSandboxes.value)
  if (!Number.isInteger(max) || max < 1 || max > MAX_SANDBOXES_LIMIT) {
    localError.value = `Concurrent sandboxes must be between 1 and ${MAX_SANDBOXES_LIMIT}`
    return
  }
  localError.value = ''
  const input: ProjectInput = {
    name: name.value.trim(),
    description: description.value.trim(),
    setupScript: setupScript.value,
    environment: environment.value,
    maxSandboxes: max,
  }
  if (!props.project) {
    input.defaultBranch = defaultBranch.value.trim() || undefined
    input.upstreamUrl = upstreamUrl.value.trim() || undefined
  }
  emit('submit', input)
}
</script>

<template>
  <Teleport to="body">
    <div v-if="show" class="modal-overlay" @click.self="emit('close')">
      <div class="modal">
        <div class="modal-header">
          <h2 class="modal-title">{{ project ? 'Edit Project' : 'New Project' }}</h2>
          <button class="modal-close" aria-label="Close" @click="emit('close')">&times;</button>
        </div>
        <form class="modal-body" @submit.prevent="handleSubmit">
          <div class="form-group">
            <label class="form-label" for="project-name">Name</label>
            <input id="project-name" v-model="name" class="form-input" type="text" autocomplete="off" placeholder="my-service" />
          </div>
          <div class="form-group">
            <label class="form-label" for="project-desc">Description</label>
            <textarea id="project-desc" v-model="description" class="form-input form-textarea" rows="2" placeholder="Optional. Agents see it with every task." />
          </div>
          <template v-if="!project">
            <div class="form-group">
              <label class="form-label" for="project-upstream">Upstream repository <span class="form-optional">(optional)</span></label>
              <input id="project-upstream" v-model="upstreamUrl" class="form-input form-mono" type="text" autocomplete="off" placeholder="git@github.com:org/repo.git" />
              <p class="form-hint">Cloned once with your SSH keys; merges can be pushed back. Leave it empty for a scratch project that starts from an empty repo.</p>
            </div>
            <div class="form-group">
              <label class="form-label" for="project-branch">Default branch</label>
              <input id="project-branch" v-model="defaultBranch" class="form-input form-mono" type="text" autocomplete="off" placeholder="main" />
              <p class="form-hint">Leave empty to use the upstream's default, or main.</p>
            </div>
          </template>
          <div v-else class="form-group">
            <span class="form-label">Repository</span>
            <p class="form-hint">
              <span class="mono">{{ project.upstreamUrl || 'no upstream' }}</span> &middot;
              default branch <span class="mono">{{ project.defaultBranch }}</span>
            </p>
          </div>
          <div class="form-group">
            <label class="form-label" for="project-setup">Setup script</label>
            <textarea id="project-setup" v-model="setupScript" class="form-input form-textarea form-mono" rows="4" placeholder="npm ci" />
            <p class="form-hint">Runs in your project workspace and each new agent sandbox after the repo is checked out. Agents start once it finishes and are told if it failed.</p>
          </div>
          <div class="form-group">
            <label class="form-label" for="project-environment">Environment</label>
            <textarea
              id="project-environment"
              v-model="environment"
              class="form-input form-textarea form-mono"
              rows="5"
              spellcheck="false"
              placeholder="RUN apt-get update &amp;&amp; apt-get install -y nodejs"
            />
            <p class="form-hint">Dockerfile lines, without <span class="mono">FROM</span>, run as root on devpad's workspace image. Workspaces are made from the result, so install toolchains and system packages here and project dependencies in the setup script.</p>
          </div>
          <div class="form-group">
            <label class="form-label" for="project-max">Concurrent sandboxes</label>
            <input id="project-max" v-model.number="maxSandboxes" class="form-input" type="number" min="1" :max="MAX_SANDBOXES_LIMIT" />
            <p class="form-hint">Tasks beyond this wait in the queue.</p>
          </div>
          <p v-if="localError || error" class="form-error">{{ localError || error }}</p>
          <div class="modal-actions">
            <button type="button" class="btn btn-secondary" @click="emit('close')">Cancel</button>
            <button type="submit" class="btn btn-primary" :disabled="saving">{{ saving ? 'Saving...' : project ? 'Save' : 'Create' }}</button>
          </div>
        </form>
      </div>
    </div>
  </Teleport>
</template>

<style scoped src="./shared.css"></style>
<style scoped>
.form-optional {
  font-weight: 400;
  color: var(--text-muted);
}

.modal {
  background: var(--bg-surface);
  border: 0.5px solid var(--border-default);
  border-radius: var(--radius-lg);
  width: 100%;
  max-width: 520px;
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

.modal-actions {
  display: flex;
  justify-content: flex-end;
  gap: var(--space-2);
  padding-top: var(--space-2);
}
</style>
