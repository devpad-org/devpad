<script setup lang="ts">
import { nextTick, ref, watch } from 'vue'

const props = defineProps<{
  show: boolean
  loading?: boolean
  error?: string | null
}>()

const emit = defineEmits<{
  open: [port: number]
  cancel: []
}>()

const portInput = ref('3000')
const validationError = ref('')
const inputRef = ref<HTMLInputElement | null>(null)

watch(
  () => props.show,
  async (visible) => {
    if (!visible) return

    portInput.value = '3000'
    validationError.value = ''
    await nextTick()
    inputRef.value?.focus()
    inputRef.value?.select()
  },
)

function handleSubmit() {
  validationError.value = ''

  const port = Number(portInput.value)
  if (!Number.isInteger(port) || port < 1 || port > 65535) {
    validationError.value = 'Enter a valid port between 1 and 65535.'
    return
  }

  emit('open', port)
}
</script>

<template>
  <Teleport to="body">
    <Transition name="preview-modal">
      <div v-if="show" class="modal-overlay" @click.self="!loading && emit('cancel')">
        <div class="modal" role="dialog" aria-label="Open Preview in New Tab" aria-modal="true">
          <div class="modal-header">
            <div class="modal-icon">
              <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                <path d="M15 3h6v6" />
                <path d="M10 14 21 3" />
                <path d="M18 13v6a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V8a2 2 0 0 1 2-2h6" />
              </svg>
            </div>
            <h2 class="modal-title">Open Preview in New Tab</h2>
          </div>

          <form class="modal-body" @submit.prevent="handleSubmit">
            <p class="modal-description">
              Choose the workspace port your app is listening on. Devpad will generate a secure preview URL and open it in a new tab.
            </p>

            <div class="form-group">
              <label class="form-label" for="preview-port">Port</label>
              <input
                id="preview-port"
                ref="inputRef"
                v-model="portInput"
                class="form-input"
                type="text"
                inputmode="numeric"
                placeholder="3000"
                :disabled="loading"
              />
            </div>

            <p v-if="validationError || error" class="form-error">
              {{ validationError || error }}
            </p>

            <div class="modal-actions">
              <button type="button" class="btn btn-cancel" :disabled="loading" @click="emit('cancel')">
                Cancel
              </button>
              <button type="submit" class="btn btn-confirm" :disabled="loading">
                <span v-if="loading" class="spinner" />
                {{ loading ? 'Opening...' : 'Open Preview' }}
              </button>
            </div>
          </form>
        </div>
      </div>
    </Transition>
  </Teleport>
</template>

<style scoped>
.modal-overlay {
  position: fixed;
  inset: 0;
  background: var(--bg-overlay);
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
  max-width: 420px;
  box-shadow: var(--shadow-strong);
  overflow: hidden;
}

.modal-header {
  display: flex;
  align-items: center;
  gap: var(--space-3);
  padding: var(--space-5) var(--space-5) 0;
}

.modal-icon {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 36px;
  height: 36px;
  border-radius: 50%;
  flex-shrink: 0;
  background: var(--accent-glow);
  color: var(--accent-blue);
}

.modal-title {
  font-size: 1rem;
  font-weight: 600;
}

.modal-body {
  padding: var(--space-4) var(--space-5) var(--space-5);
}

.modal-description {
  color: var(--text-secondary);
  font-size: 0.8rem;
  line-height: 1.5;
  margin-bottom: var(--space-4);
}

.form-group {
  margin-bottom: var(--space-3);
}

.form-label {
  display: block;
  font-size: 0.8rem;
  font-weight: 500;
  color: var(--text-secondary);
  margin-bottom: var(--space-1);
}

.form-input {
  width: 100%;
  padding: var(--space-2) var(--space-3);
  background: var(--bg-primary);
  border: 0.5px solid var(--border-default);
  border-radius: var(--radius-md);
  color: var(--text-primary);
  font-family: inherit;
  font-size: 0.85rem;
  transition: border-color var(--transition-fast);
}

.form-input:focus {
  outline: none;
  border-color: var(--accent-blue);
}

.form-input:disabled {
  opacity: 0.6;
  cursor: not-allowed;
}

.form-error {
  color: var(--accent-rose);
  font-size: 0.8rem;
  margin-bottom: var(--space-3);
}

.modal-actions {
  display: flex;
  justify-content: flex-end;
  gap: var(--space-2);
  background: var(--bg-surface-alt);
  margin: var(--space-4) calc(-1 * var(--space-5)) calc(-1 * var(--space-5));
  padding: var(--space-4) var(--space-5);
  border-top: 0.5px solid var(--border-default);
}

.btn {
  display: inline-flex;
  align-items: center;
  gap: var(--space-2);
  padding: var(--space-2) var(--space-4);
  font-size: 0.8rem;
  font-weight: 500;
  border-radius: var(--radius-md);
  transition: all var(--transition-fast);
}

.btn-cancel {
  color: var(--text-secondary);
  border: 0.5px solid var(--border-default);
}

.btn-cancel:hover:not(:disabled) {
  color: var(--text-primary);
  border-color: var(--border-active);
}

.btn-confirm {
  background: var(--accent-blue);
  color: var(--bg-primary);
}

.btn-confirm:hover:not(:disabled) {
  background: color-mix(in srgb, var(--accent-blue) 85%, var(--gruvbox-fg0));
}

.btn:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

.spinner {
  width: 14px;
  height: 14px;
  border: 2px solid var(--spinner-track);
  border-top-color: currentColor;
  border-radius: 50%;
  animation: spin 0.6s linear infinite;
}

@keyframes spin {
  to {
    transform: rotate(360deg);
  }
}

.preview-modal-enter-active,
.preview-modal-leave-active {
  transition: opacity var(--transition-fast);
}

.preview-modal-enter-active .modal,
.preview-modal-leave-active .modal {
  transition: transform var(--transition-fast);
}

.preview-modal-enter-from,
.preview-modal-leave-to {
  opacity: 0;
}

.preview-modal-enter-from .modal,
.preview-modal-leave-to .modal {
  transform: scale(0.95);
}
</style>
