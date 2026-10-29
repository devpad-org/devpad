<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref, watch } from 'vue'
import type { Project, ProjectInput } from '@/api/projects'
import MrIcon from './MrIcon.vue'

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
const autoReview = ref(true)
const localError = ref('')
const nameInput = ref<HTMLInputElement | null>(null)

// An HTTPS upstream is fetched without the user's SSH key, so a private
// one fails.
const httpsUpstream = computed(() => /^https?:\/\//i.test(upstreamUrl.value.trim()))
const shownError = computed(() => localError.value || props.error)

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
    autoReview.value = p?.autoReview ?? true
    localError.value = ''
    if (!p) void nextTick(() => nameInput.value?.focus())
  },
  { immediate: true },
)

function stepSandboxes(delta: number) {
  const current = Number(maxSandboxes.value)
  const base = Number.isInteger(current) ? current : DEFAULT_MAX_SANDBOXES
  maxSandboxes.value = Math.min(MAX_SANDBOXES_LIMIT, Math.max(1, base + delta))
}

function handleSubmit() {
  if (props.saving) return
  if (!name.value.trim()) {
    localError.value = 'Name is required'
    nameInput.value?.focus()
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
    autoReview: autoReview.value,
  }
  if (props.project) {
    input.upstreamUrl = upstreamUrl.value.trim()
  } else {
    input.defaultBranch = defaultBranch.value.trim() || undefined
    input.upstreamUrl = upstreamUrl.value.trim() || undefined
  }
  emit('submit', input)
}

function onKeydown(e: KeyboardEvent) {
  if (!props.show) return
  if (e.key === 'Escape') {
    emit('close')
  } else if (e.key === 'Enter' && (e.ctrlKey || e.metaKey)) {
    e.preventDefault()
    handleSubmit()
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
        aria-labelledby="project-modal-heading"
        @submit.prevent="handleSubmit"
      >
        <header class="modal-header">
          <div class="heading">
            <h2 id="project-modal-heading" class="modal-title">
              <MrIcon name="folder" :size="15" class="title-icon" />
              {{ project ? 'Edit project' : 'New project' }}
            </h2>
            <p v-if="!project" class="modal-subtitle">
              A repository, the image its workspaces are made from, and how agents work on it.
            </p>
          </div>
          <button type="button" class="modal-close" aria-label="Close" title="Close (Esc)" @click="emit('close')">
            <MrIcon name="close" :size="16" />
          </button>
        </header>

        <div class="modal-body">
          <div class="details-col">
            <section class="side-section">
              <div class="field">
                <label class="form-label" for="project-name">Name</label>
                <input
                  id="project-name"
                  ref="nameInput"
                  v-model="name"
                  class="form-input name-input"
                  type="text"
                  autocomplete="off"
                  placeholder="my-service"
                />
              </div>
              <div class="field">
                <label class="form-label" for="project-desc">Description</label>
                <textarea
                  id="project-desc"
                  v-model="description"
                  class="form-input form-textarea"
                  rows="3"
                  placeholder="Optional. Agents see it with every task."
                />
              </div>
            </section>

            <section class="side-section">
              <h3 class="side-title"><MrIcon name="branch" :size="12" /> Repository</h3>
              <div class="field">
                <label class="form-label" for="project-upstream">Upstream <span class="form-optional">(optional)</span></label>
                <input
                  id="project-upstream"
                  v-model="upstreamUrl"
                  class="form-input form-mono"
                  type="text"
                  autocomplete="off"
                  spellcheck="false"
                  placeholder="git@github.com:org/repo.git"
                />
                <p v-if="project" class="form-hint">
                  Fetched and pushed to with your SSH keys; the workspace sees it as <span class="mono">origin</span>. Changing it forgets what was synced from the old one, and leaving it empty makes the project local only.
                </p>
                <p v-else class="form-hint">
                  Cloned once with your SSH keys; merges can be pushed back. Leave it empty for a scratch project that starts from an empty repo.
                </p>
                <p v-if="httpsUpstream" class="notice notice-warning notice-row">
                  <MrIcon name="warning" :size="13" />
                  <span>SSH keys only work with SSH URLs, such as <span class="mono">git@github.com:org/repo.git</span>; a private repository needs one.</span>
                </p>
              </div>
              <div v-if="!project" class="field">
                <label class="form-label" for="project-branch">Default branch</label>
                <input
                  id="project-branch"
                  v-model="defaultBranch"
                  class="form-input form-mono"
                  type="text"
                  autocomplete="off"
                  spellcheck="false"
                  placeholder="main"
                />
                <p class="form-hint">Leave empty to use the upstream's default, or main.</p>
              </div>
              <div v-else class="field">
                <div class="setting-row">
                  <span class="form-label">Default branch</span>
                  <span class="branch-chip"><MrIcon name="branch" :size="11" />{{ project.defaultBranch }}</span>
                </div>
                <p class="form-hint">Change the default and protected branches from Branches in the project panel.</p>
              </div>
            </section>

            <section class="side-section">
              <h3 class="side-title"><MrIcon name="bot" :size="12" /> Agents</h3>
              <div class="field">
                <div class="setting-row">
                  <label class="form-label" for="project-max">Concurrent sandboxes</label>
                  <div class="stepper">
                    <button
                      type="button"
                      class="stepper-btn"
                      aria-label="Fewer sandboxes"
                      :disabled="Number(maxSandboxes) <= 1"
                      @click="stepSandboxes(-1)"
                    >
                      <MrIcon name="minus" :size="12" />
                    </button>
                    <input
                      id="project-max"
                      v-model.number="maxSandboxes"
                      class="stepper-input"
                      type="number"
                      inputmode="numeric"
                      min="1"
                      :max="MAX_SANDBOXES_LIMIT"
                    />
                    <button
                      type="button"
                      class="stepper-btn"
                      aria-label="More sandboxes"
                      :disabled="Number(maxSandboxes) >= MAX_SANDBOXES_LIMIT"
                      @click="stepSandboxes(1)"
                    >
                      <MrIcon name="plus" :size="12" />
                    </button>
                  </div>
                </div>
                <p class="form-hint">Tasks beyond this wait in the queue.</p>
              </div>
              <div class="field">
                <label class="setting-row toggle-row">
                  <span class="form-label">Review new merge requests automatically</span>
                  <input v-model="autoReview" class="toggle-input" type="checkbox" role="switch" />
                  <span class="toggle-switch" aria-hidden="true" />
                </label>
                <p class="form-hint">Starts the review agent on each merge request as it opens, with the model an admin set for it, else the task's. Changes it requests wait for you to send them to the task's agent.</p>
              </div>
            </section>
          </div>

          <div class="workspace-col">
            <div class="field code-field environment-field">
              <div class="field-head">
                <MrIcon name="box" :size="13" class="field-icon" />
                <label class="form-label" for="project-environment">Environment</label>
                <span class="field-head-end code-tag">Dockerfile</span>
              </div>
              <div class="code-frame">
                <pre class="code-locked" title="devpad adds these" aria-hidden="true">FROM devpad-workspace:latest
USER root</pre>
                <textarea
                  id="project-environment"
                  v-model="environment"
                  class="code-input"
                  rows="6"
                  wrap="off"
                  spellcheck="false"
                  placeholder="RUN apt-get update &amp;&amp; apt-get install -y nodejs"
                />
              </div>
              <p class="form-hint">Dockerfile lines run as root on devpad's workspace image, without <span class="mono">FROM</span>, <span class="mono">ADD</span> or <span class="mono">COPY</span>; download files with <span class="mono">RUN curl</span>. Workspaces are made from the result, so install toolchains and system packages here and project dependencies in the setup script.</p>
            </div>

            <div class="field code-field setup-field">
              <div class="field-head">
                <MrIcon name="terminal" :size="13" class="field-icon" />
                <label class="form-label" for="project-setup">Setup script</label>
                <span class="field-head-end code-tag">/workspace</span>
              </div>
              <div class="code-frame">
                <textarea
                  id="project-setup"
                  v-model="setupScript"
                  class="code-input"
                  rows="4"
                  wrap="off"
                  spellcheck="false"
                  placeholder="npm ci"
                />
              </div>
              <p class="form-hint">Runs in your project workspace and each new agent sandbox after the repo is checked out. Agents start once it finishes and are told if it failed.</p>
            </div>
          </div>
        </div>

        <footer class="modal-footer">
          <p v-if="shownError" class="footer-error" role="alert">
            <MrIcon name="alert" :size="14" />
            {{ shownError }}
          </p>
          <span v-else class="form-hint footer-hint"><kbd>Ctrl</kbd> <kbd>Enter</kbd> to {{ project ? 'save' : 'create' }}</span>
          <button type="button" class="btn btn-secondary" @click="emit('close')">Cancel</button>
          <button type="submit" class="btn btn-primary" :disabled="saving">
            <span v-if="saving" class="spinner spinner-sm spinner-on-accent" />
            {{ saving ? 'Saving...' : project ? 'Save' : 'Create project' }}
          </button>
        </footer>
      </form>
    </div>
  </Teleport>
</template>

<style scoped src="./shared.css"></style>
<style scoped src="./taskModal.css"></style>
<style scoped>
/* Settings on the left, the two scripts that shape a workspace on the right
   as editor panes that share the column's height. */
.modal {
  max-width: 1080px;
  max-height: calc(100dvh - 2 * var(--space-4));
}

.modal-header {
  align-items: center;
  flex-shrink: 0;
}

.title-icon {
  color: var(--text-muted);
}

.modal-subtitle {
  margin-top: 2px;
  font-size: 0.78rem;
  color: var(--text-muted);
  overflow-wrap: anywhere;
}

.modal-body {
  grid-template-columns: minmax(0, 360px) minmax(0, 1fr);
  flex: 1;
  min-height: 0;
  overflow-y: auto;
}

.modal-footer {
  flex-shrink: 0;
  background: var(--bg-surface);
}

.details-col {
  display: flex;
  flex-direction: column;
  min-width: 0;
  padding: var(--space-1) var(--space-5);
}

.details-col .side-section {
  display: flex;
  flex-direction: column;
  gap: var(--space-4);
}

.details-col .side-title {
  display: flex;
  align-items: center;
  gap: 6px;
  margin-bottom: 0;
}

.workspace-col {
  display: flex;
  flex-direction: column;
  gap: var(--space-5);
  min-width: 0;
  padding: var(--space-5);
  border-left: 0.5px solid var(--border-default);
  background: color-mix(in srgb, var(--bg-base) 55%, var(--bg-surface));
}

.form-optional {
  font-weight: 400;
  color: var(--text-muted);
}

.name-input {
  padding-top: 10px;
  padding-bottom: 10px;
  font-size: 0.95rem;
  font-weight: 500;
}

.form-input::placeholder,
.code-input::placeholder {
  color: var(--text-dim);
}

.details-col .notice {
  margin: var(--space-2) 0 0;
  font-size: 0.75rem;
}

/* A label with its control at the end of the row. */
.setting-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-3);
  min-height: 28px;
}

.setting-row .form-label {
  margin-bottom: 0;
}

.branch-chip {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  min-width: 0;
  padding: 2px var(--space-2);
  border-radius: var(--radius-sm);
  background: var(--bg-neutral);
  color: var(--text-primary);
  font-family: var(--font-mono);
  font-size: 0.75rem;
  overflow-wrap: anywhere;
}

.stepper {
  display: inline-flex;
  flex-shrink: 0;
  background: var(--bg-primary);
  border: 0.5px solid var(--border-default);
  border-radius: var(--radius-md);
  overflow: hidden;
  transition: border-color var(--transition-fast);
}

.stepper:focus-within {
  border-color: var(--accent-blue);
}

.stepper-btn {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 28px;
  color: var(--text-muted);
  transition: all var(--transition-fast);
}

.stepper-btn:hover:not(:disabled) {
  color: var(--text-primary);
  background: var(--bg-hover);
}

.stepper-btn:disabled {
  opacity: 0.4;
  cursor: not-allowed;
}

.stepper-input {
  width: 38px;
  padding: 4px 0;
  border: none;
  border-left: 0.5px solid var(--border-hairline);
  border-right: 0.5px solid var(--border-hairline);
  background: transparent;
  color: var(--text-primary);
  font-family: var(--font-mono);
  font-size: 0.85rem;
  text-align: center;
  -moz-appearance: textfield;
  appearance: textfield;
}

.stepper-input:focus {
  outline: none;
}

.stepper-input::-webkit-inner-spin-button,
.stepper-input::-webkit-outer-spin-button {
  -webkit-appearance: none;
  margin: 0;
}

.toggle-row {
  position: relative;
  cursor: pointer;
}

.toggle-input {
  position: absolute;
  opacity: 0;
  width: 0;
  height: 0;
}

.toggle-switch {
  position: relative;
  flex-shrink: 0;
  width: 34px;
  height: 20px;
  background: var(--bg-surface-alt);
  border: 0.5px solid var(--border-default);
  border-radius: 10px;
  transition: all var(--transition-fast);
}

.toggle-switch::after {
  content: '';
  position: absolute;
  top: 2.5px;
  left: 2.5px;
  width: 14px;
  height: 14px;
  border-radius: 50%;
  background: var(--text-muted);
  transition: transform var(--transition-fast), background var(--transition-fast);
}

.toggle-input:checked + .toggle-switch {
  background: var(--success-bg);
  border-color: var(--success-border);
}

.toggle-input:checked + .toggle-switch::after {
  transform: translateX(14px);
  background: var(--accent-green);
}

.toggle-input:focus-visible + .toggle-switch {
  box-shadow: 0 0 0 3px var(--accent-glow), 0 0 0 1px var(--accent-border);
}

/* Editor panes: the Dockerfile shows the lines devpad puts before it, and
   both grow to share the column. */
.code-field {
  min-height: 0;
}

.environment-field {
  flex: 3 1 0;
}

.setup-field {
  flex: 2 1 0;
}

.field-icon {
  color: var(--text-muted);
  flex-shrink: 0;
}

.field-head .form-label {
  color: var(--text-primary);
}

.code-tag {
  padding: 1px 6px;
  border-radius: var(--radius-sm);
  background: var(--bg-neutral);
  color: var(--text-muted);
  font-family: var(--font-mono);
  font-size: 0.68rem;
}

.code-frame {
  display: flex;
  flex-direction: column;
  flex: 1;
  min-height: 110px;
  background: var(--terminal-bg);
  border: 0.5px solid var(--border-default);
  border-radius: var(--radius-md);
  overflow: hidden;
  transition: border-color var(--transition-fast);
}

.environment-field .code-frame {
  min-height: 170px;
}

.code-frame:focus-within {
  border-color: var(--accent-blue);
}

.code-locked,
.code-input {
  font-family: var(--font-mono);
  font-size: 0.8rem;
  line-height: 1.6;
  tab-size: 4;
}

.code-locked {
  padding: var(--space-2) var(--space-3) 0;
  color: var(--text-dim);
  white-space: pre;
  overflow: hidden;
  text-overflow: ellipsis;
  user-select: none;
}

.code-input {
  flex: 1;
  width: 100%;
  min-height: 0;
  padding: var(--space-2) var(--space-3);
  border: none;
  background: transparent;
  color: var(--text-primary);
  resize: none;
}

.code-input:focus {
  outline: none;
}

.code-field .form-hint {
  margin-top: var(--space-2);
}

.footer-error {
  display: flex;
  align-items: flex-start;
  gap: var(--space-2);
  flex: 1 1 240px;
  min-width: 0;
  margin-right: auto;
  color: var(--error);
  font-size: 0.8rem;
  overflow-wrap: anywhere;
}

.footer-error .mr-icon {
  margin-top: 2px;
  flex-shrink: 0;
}

/* Touch devices raise every field to 16px, so the Dockerfile's fixed lines
   follow to stay in step with what is typed under them. */
@media (pointer: coarse) {
  .code-locked {
    font-size: 16px;
  }
}

@media (max-width: 860px) {
  .modal-body {
    grid-template-columns: minmax(0, 1fr);
  }

  .details-col {
    padding-left: var(--space-4);
    padding-right: var(--space-4);
  }

  .workspace-col {
    padding: var(--space-5) var(--space-4);
    border-left: none;
    border-top: 0.5px solid var(--border-default);
  }

  .environment-field,
  .setup-field {
    flex: none;
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

  .modal-body {
    flex: 1 0 auto;
    overflow: visible;
  }

  .modal-footer {
    position: sticky;
    bottom: 0;
    z-index: 1;
  }

  .stepper-btn {
    width: 40px;
  }

  .toggle-row {
    min-height: 40px;
  }
}
</style>
