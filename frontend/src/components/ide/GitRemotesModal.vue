<script setup lang="ts">
import { ref, watch } from 'vue'
import { gitApi, type GitRemote } from '@/api/git'

const props = defineProps<{
  show: boolean
  workspaceId: number
}>()

const emit = defineEmits<{
  close: []
  updated: []
}>()

const remotes = ref<GitRemote[]>([])
const loading = ref(false)
const error = ref('')
const actionMessage = ref('')

// Add form state
const showAddForm = ref(false)
const addName = ref('')
const addUrl = ref('')
const addLoading = ref(false)

// Inline edit state
const editingRemote = ref<string | null>(null)
const editUrl = ref('')
const editLoading = ref(false)

// Rename state
const renamingRemote = ref<string | null>(null)
const renameValue = ref('')
const renameLoading = ref(false)

watch(
  () => props.show,
  async (visible) => {
    if (visible) {
      error.value = ''
      actionMessage.value = ''
      showAddForm.value = false
      editingRemote.value = null
      renamingRemote.value = null
      await fetchRemotes()
    }
  },
)

async function fetchRemotes() {
  loading.value = true
  error.value = ''
  try {
    remotes.value = await gitApi.remotes(props.workspaceId)
  } catch (e: any) {
    error.value = e.message || 'Failed to fetch remotes'
  } finally {
    loading.value = false
  }
}

async function addRemote() {
  const name = addName.value.trim()
  const url = addUrl.value.trim()
  if (!name || !url) return

  addLoading.value = true
  error.value = ''
  actionMessage.value = ''
  try {
    const result = await gitApi.action(props.workspaceId, 'remote-add', { remote: name, url })
    if (result.success) {
      actionMessage.value = `Added remote "${name}"`
      addName.value = ''
      addUrl.value = ''
      showAddForm.value = false
      await fetchRemotes()
      emit('updated')
    } else {
      error.value = result.error || result.output || 'Failed to add remote'
    }
  } catch (e: any) {
    error.value = e.message || 'Failed to add remote'
  } finally {
    addLoading.value = false
  }
}

async function removeRemote(name: string) {
  error.value = ''
  actionMessage.value = ''
  try {
    const result = await gitApi.action(props.workspaceId, 'remote-remove', { remote: name })
    if (result.success) {
      actionMessage.value = `Removed remote "${name}"`
      await fetchRemotes()
      emit('updated')
    } else {
      error.value = result.error || result.output || 'Failed to remove remote'
    }
  } catch (e: any) {
    error.value = e.message || 'Failed to remove remote'
  }
}

function startEdit(remote: GitRemote) {
  editingRemote.value = remote.name
  editUrl.value = remote.fetchUrl
  renamingRemote.value = null
}

function cancelEdit() {
  editingRemote.value = null
  editUrl.value = ''
}

async function saveUrl(name: string) {
  const url = editUrl.value.trim()
  if (!url) return

  editLoading.value = true
  error.value = ''
  actionMessage.value = ''
  try {
    const result = await gitApi.action(props.workspaceId, 'remote-set-url', { remote: name, url })
    if (result.success) {
      actionMessage.value = `Updated URL for "${name}"`
      editingRemote.value = null
      await fetchRemotes()
      emit('updated')
    } else {
      error.value = result.error || result.output || 'Failed to update URL'
    }
  } catch (e: any) {
    error.value = e.message || 'Failed to update URL'
  } finally {
    editLoading.value = false
  }
}

function startRename(remote: GitRemote) {
  renamingRemote.value = remote.name
  renameValue.value = remote.name
  editingRemote.value = null
}

function cancelRename() {
  renamingRemote.value = null
  renameValue.value = ''
}

async function saveRename(oldName: string) {
  const newName = renameValue.value.trim()
  if (!newName || newName === oldName) {
    cancelRename()
    return
  }

  renameLoading.value = true
  error.value = ''
  actionMessage.value = ''
  try {
    const result = await gitApi.action(props.workspaceId, 'remote-rename', {
      remote: oldName,
      newName,
    })
    if (result.success) {
      actionMessage.value = `Renamed "${oldName}" to "${newName}"`
      renamingRemote.value = null
      await fetchRemotes()
      emit('updated')
    } else {
      error.value = result.error || result.output || 'Failed to rename remote'
    }
  } catch (e: any) {
    error.value = e.message || 'Failed to rename remote'
  } finally {
    renameLoading.value = false
  }
}
</script>

<template>
  <Teleport to="body">
    <Transition name="remotes-modal">
      <div v-if="show" class="modal-overlay" @click.self="emit('close')">
        <div class="modal" role="dialog" aria-label="Git Remotes">
          <!-- Header -->
          <div class="modal-header">
            <div class="modal-icon">
              <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                <circle cx="12" cy="12" r="10" />
                <path d="M2 12h20" />
                <path d="M12 2a15.3 15.3 0 0 1 4 10 15.3 15.3 0 0 1-4 10 15.3 15.3 0 0 1-4-10 15.3 15.3 0 0 1 4-10z" />
              </svg>
            </div>
            <h2 class="modal-title">Git Remotes</h2>
            <button class="modal-close" @click="emit('close')" title="Close">
              <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                <path d="M18 6 6 18" /><path d="m6 6 12 12" />
              </svg>
            </button>
          </div>

          <!-- Body -->
          <div class="modal-body">
            <!-- Loading -->
            <div v-if="loading && remotes.length === 0" class="remotes-loading">
              <span class="loading-spinner" />
              Loading remotes…
            </div>

            <!-- Empty state -->
            <div v-else-if="remotes.length === 0 && !loading" class="remotes-empty">
              <svg width="28" height="28" viewBox="0 0 24 24" fill="none" stroke="var(--text-muted)" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round">
                <circle cx="12" cy="12" r="10" />
                <path d="M2 12h20" />
                <path d="M12 2a15.3 15.3 0 0 1 4 10 15.3 15.3 0 0 1-4 10 15.3 15.3 0 0 1-4-10 15.3 15.3 0 0 1 4-10z" />
              </svg>
              <p>No remotes configured</p>
              <p class="remotes-empty-hint">Add a remote to push and pull code.</p>
            </div>

            <!-- Remote list -->
            <div v-else class="remotes-list">
              <div v-for="remote in remotes" :key="remote.name" class="remote-card">
                <!-- Remote header -->
                <div class="remote-header">
                  <div v-if="renamingRemote === remote.name" class="remote-rename">
                    <input
                      v-model="renameValue"
                      class="remote-input remote-input--name"
                      placeholder="Remote name"
                      @keydown.enter="saveRename(remote.name)"
                      @keydown.escape="cancelRename"
                    />
                    <button
                      class="remote-action-btn remote-action-btn--confirm"
                      @click="saveRename(remote.name)"
                      :disabled="renameLoading || !renameValue.trim()"
                      title="Save"
                    >
                      <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round">
                        <path d="M20 6 9 17l-5-5" />
                      </svg>
                    </button>
                    <button
                      class="remote-action-btn"
                      @click="cancelRename"
                      title="Cancel"
                    >
                      <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round">
                        <path d="M18 6 6 18" /><path d="m6 6 12 12" />
                      </svg>
                    </button>
                  </div>
                  <template v-else>
                    <span class="remote-name">{{ remote.name }}</span>
                    <div class="remote-actions">
                      <button
                        class="remote-action-btn"
                        @click="startRename(remote)"
                        title="Rename remote"
                      >
                        <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                          <path d="M17 3a2.85 2.83 0 1 1 4 4L7.5 20.5 2 22l1.5-5.5Z" />
                          <path d="m15 5 4 4" />
                        </svg>
                      </button>
                      <button
                        class="remote-action-btn"
                        @click="startEdit(remote)"
                        title="Edit URL"
                      >
                        <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                          <path d="M10 13a5 5 0 0 0 7.54.54l3-3a5 5 0 0 0-7.07-7.07l-1.72 1.71" />
                          <path d="M14 11a5 5 0 0 0-7.54-.54l-3 3a5 5 0 0 0 7.07 7.07l1.71-1.71" />
                        </svg>
                      </button>
                      <button
                        class="remote-action-btn remote-action-btn--danger"
                        @click="removeRemote(remote.name)"
                        title="Remove remote"
                      >
                        <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                          <path d="M3 6h18" /><path d="M19 6v14c0 1-1 2-2 2H7c-1 0-2-1-2-2V6" /><path d="M8 6V4c0-1 1-2 2-2h4c1 0 2 1 2 2v2" />
                        </svg>
                      </button>
                    </div>
                  </template>
                </div>

                <!-- URL display / edit -->
                <div v-if="editingRemote === remote.name" class="remote-url-edit">
                  <label class="remote-url-label">URL</label>
                  <div class="remote-url-edit-row">
                    <input
                      v-model="editUrl"
                      class="remote-input"
                      placeholder="https://github.com/user/repo.git"
                      @keydown.enter="saveUrl(remote.name)"
                      @keydown.escape="cancelEdit"
                    />
                    <button
                      class="remote-action-btn remote-action-btn--confirm"
                      @click="saveUrl(remote.name)"
                      :disabled="editLoading || !editUrl.trim()"
                      title="Save URL"
                    >
                      <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round">
                        <path d="M20 6 9 17l-5-5" />
                      </svg>
                    </button>
                    <button
                      class="remote-action-btn"
                      @click="cancelEdit"
                      title="Cancel"
                    >
                      <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round">
                        <path d="M18 6 6 18" /><path d="m6 6 12 12" />
                      </svg>
                    </button>
                  </div>
                </div>
                <div v-else class="remote-urls">
                  <div class="remote-url-row">
                    <span class="remote-url-label">fetch</span>
                    <span class="remote-url-value">{{ remote.fetchUrl }}</span>
                  </div>
                  <div v-if="remote.pushUrl !== remote.fetchUrl" class="remote-url-row">
                    <span class="remote-url-label">push</span>
                    <span class="remote-url-value">{{ remote.pushUrl }}</span>
                  </div>
                </div>
              </div>
            </div>

            <!-- Feedback messages -->
            <div v-if="error" class="remotes-message remotes-message--error">{{ error }}</div>
            <div v-if="actionMessage" class="remotes-message remotes-message--success">{{ actionMessage }}</div>

            <!-- Add form -->
            <div v-if="showAddForm" class="add-remote-form">
              <div class="form-group">
                <label class="form-label" for="remote-name">Name</label>
                <input
                  id="remote-name"
                  v-model="addName"
                  class="remote-input"
                  placeholder="origin"
                  @keydown.enter="addRemote"
                />
              </div>
              <div class="form-group">
                <label class="form-label" for="remote-url">URL</label>
                <input
                  id="remote-url"
                  v-model="addUrl"
                  class="remote-input"
                  placeholder="https://github.com/user/repo.git"
                  @keydown.enter="addRemote"
                />
              </div>
            </div>
          </div>

          <!-- Footer -->
          <div class="modal-footer">
            <button
              v-if="!showAddForm"
              class="btn btn-add"
              @click="showAddForm = true"
            >
              <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                <path d="M12 5v14" /><path d="M5 12h14" />
              </svg>
              Add Remote
            </button>
            <template v-else>
              <button class="btn btn-cancel" @click="showAddForm = false; addName = ''; addUrl = ''">
                Cancel
              </button>
              <button
                class="btn btn-confirm"
                @click="addRemote"
                :disabled="addLoading || !addName.trim() || !addUrl.trim()"
              >
                {{ addLoading ? 'Adding…' : 'Add Remote' }}
              </button>
            </template>
          </div>
        </div>
      </div>
    </Transition>
  </Teleport>
</template>

<style scoped>
.modal-overlay {
  position: fixed;
  inset: 0;
  background: rgba(0, 0, 0, 0.6);
  display: flex;
  align-items: center;
  justify-content: center;
  z-index: 1000;
}

.modal {
  background: var(--bg-surface);
  border: 0.5px solid var(--border-default);
  border-radius: var(--radius-lg);
  width: 100%;
  max-width: 520px;
  max-height: 80vh;
  display: flex;
  flex-direction: column;
  box-shadow: 0 16px 48px rgba(0, 0, 0, 0.4);
  overflow: hidden;
}

.modal-header {
  display: flex;
  align-items: center;
  gap: var(--space-3);
  padding: var(--space-4) var(--space-5);
  border-bottom: 0.5px solid var(--border-default);
  flex-shrink: 0;
}

.modal-icon {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 32px;
  height: 32px;
  border-radius: var(--radius-md);
  flex-shrink: 0;
  background: var(--bg-raised);
  color: var(--text-secondary);
}

.modal-title {
  font-size: 1rem;
  font-weight: 600;
  flex: 1;
}

.modal-close {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 28px;
  height: 28px;
  border-radius: var(--radius-sm);
  color: var(--text-muted);
  transition: all var(--transition-fast);
}

.modal-close:hover {
  background: var(--bg-hover);
  color: var(--text-primary);
}

.modal-body {
  flex: 1;
  overflow-y: auto;
  padding: var(--space-4) var(--space-5);
}

/* Loading */
.remotes-loading {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  justify-content: center;
  padding: var(--space-6);
  color: var(--text-muted);
  font-size: 0.8rem;
}

.loading-spinner {
  width: 14px;
  height: 14px;
  border: 2px solid var(--border-default);
  border-top-color: var(--accent-blue);
  border-radius: 50%;
  animation: spin 0.6s linear infinite;
}

@keyframes spin {
  to { transform: rotate(360deg); }
}

/* Empty state */
.remotes-empty {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: var(--space-2);
  padding: var(--space-6);
  text-align: center;
}

.remotes-empty p {
  color: var(--text-muted);
  font-size: 0.85rem;
}

.remotes-empty-hint {
  font-size: 0.75rem !important;
  color: var(--text-muted);
  opacity: 0.7;
}

/* Remote list */
.remotes-list {
  display: flex;
  flex-direction: column;
  gap: var(--space-3);
}

.remote-card {
  background: var(--bg-primary);
  border: 0.5px solid var(--border-default);
  border-radius: var(--radius-md);
  padding: var(--space-3);
  transition: border-color var(--transition-fast);
}

.remote-card:hover {
  border-color: var(--border-active);
}

.remote-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  min-height: 24px;
}

.remote-name {
  font-family: var(--font-mono);
  font-size: 0.85rem;
  font-weight: 600;
  color: var(--accent-purple);
}

.remote-actions {
  display: flex;
  gap: 2px;
  opacity: 0;
  transition: opacity var(--transition-fast);
}

.remote-card:hover .remote-actions {
  opacity: 1;
}

.remote-action-btn {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 24px;
  height: 24px;
  border-radius: var(--radius-sm);
  color: var(--text-secondary);
  transition: all var(--transition-fast);
}

.remote-action-btn:hover:not(:disabled) {
  background: var(--bg-hover);
  color: var(--text-primary);
}

.remote-action-btn:disabled {
  opacity: 0.4;
  cursor: default;
}

.remote-action-btn--danger:hover:not(:disabled) {
  color: var(--accent-rose);
  background: var(--error-bg);
}

.remote-action-btn--confirm:hover:not(:disabled) {
  color: var(--accent-green);
  background: var(--success-bg);
}

/* URL display */
.remote-urls {
  margin-top: var(--space-2);
}

.remote-url-row {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  padding: 2px 0;
}

.remote-url-label {
  font-size: 0.72rem;
  font-weight: 600;
  text-transform: uppercase;
  letter-spacing: 0.5px;
  color: var(--text-muted);
  width: 36px;
  flex-shrink: 0;
}

.remote-url-value {
  font-family: var(--font-mono);
  font-size: 0.75rem;
  color: var(--text-secondary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  min-width: 0;
}

/* Edit URL */
.remote-url-edit {
  margin-top: var(--space-2);
}

.remote-url-edit-row {
  display: flex;
  gap: var(--space-1);
  margin-top: var(--space-1);
}

/* Rename */
.remote-rename {
  display: flex;
  align-items: center;
  gap: var(--space-1);
  flex: 1;
}

/* Input */
.remote-input {
  flex: 1;
  padding: var(--space-1) var(--space-2);
  background: var(--bg-surface);
  border: 0.5px solid var(--border-default);
  border-radius: var(--radius-sm);
  color: var(--text-primary);
  font-family: var(--font-mono);
  font-size: 0.78rem;
  outline: none;
  transition: border-color var(--transition-fast);
  min-width: 0;
}

.remote-input:focus {
  border-color: var(--accent-blue);
}

.remote-input--name {
  max-width: 180px;
}

/* Messages */
.remotes-message {
  font-size: 0.78rem;
  padding: var(--space-2) var(--space-3);
  border-radius: var(--radius-sm);
  margin-top: var(--space-3);
}

.remotes-message--error {
  background: var(--error-bg);
  color: var(--accent-rose);
  border: 0.5px solid var(--error-border);
}

.remotes-message--success {
  background: var(--success-bg);
  color: var(--accent-green);
  border: 0.5px solid var(--success-border);
}

/* Add form */
.add-remote-form {
  margin-top: var(--space-3);
  padding-top: var(--space-3);
  border-top: 0.5px solid var(--border-default);
  display: flex;
  flex-direction: column;
  gap: var(--space-3);
}

.form-group {
  display: flex;
  flex-direction: column;
  gap: var(--space-1);
}

.form-label {
  font-size: 0.78rem;
  font-weight: 500;
  color: var(--text-secondary);
}

/* Footer */
.modal-footer {
  display: flex;
  justify-content: flex-end;
  gap: var(--space-2);
  padding: var(--space-3) var(--space-5);
  border-top: 0.5px solid var(--border-default);
  background: var(--bg-surface-alt);
  flex-shrink: 0;
}

.btn {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  padding: var(--space-2) var(--space-4);
  font-size: 0.8rem;
  font-weight: 500;
  border-radius: var(--radius-md);
  transition: all var(--transition-fast);
}

.btn:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

.btn-add {
  color: var(--accent);
  border: 0.5px solid var(--accent-border);
  background: var(--accent-glow);
}

.btn-add:hover {
  background: color-mix(in srgb, var(--accent-glow) 150%, transparent);
  border-color: var(--accent-border);
}

.btn-cancel {
  color: var(--text-secondary);
  border: 0.5px solid var(--border-default);
}

.btn-cancel:hover {
  color: var(--text-primary);
  border-color: var(--border-active);
}

.btn-confirm {
  background: var(--accent-blue);
  color: var(--bg-primary);
}

.btn-confirm:hover:not(:disabled) {
  background: color-mix(in srgb, var(--accent-blue) 85%, white);
}

/* Transitions */
.remotes-modal-enter-active,
.remotes-modal-leave-active {
  transition: opacity var(--transition-fast);
}

.remotes-modal-enter-active .modal,
.remotes-modal-leave-active .modal {
  transition: transform var(--transition-fast);
}

.remotes-modal-enter-from,
.remotes-modal-leave-to {
  opacity: 0;
}

.remotes-modal-enter-from .modal {
  transform: scale(0.95);
}

.remotes-modal-leave-to .modal {
  transform: scale(0.95);
}
</style>
