<script setup lang="ts">
defineProps<{
  show: boolean
  title: string
  message: string
  confirmLabel?: string
  cancelLabel?: string
  variant?: 'danger' | 'default'
  loading?: boolean
}>()

const emit = defineEmits<{
  confirm: []
  cancel: []
}>()
</script>

<template>
  <Teleport to="body">
    <Transition name="confirm-modal">
      <div v-if="show" class="modal-overlay" @click.self="emit('cancel')">
        <div class="modal" role="alertdialog" :aria-label="title">
          <div class="modal-header">
            <div class="modal-icon" :class="variant ?? 'default'">
              <svg
                v-if="variant === 'danger'"
                width="20"
                height="20"
                viewBox="0 0 24 24"
                fill="none"
                stroke="currentColor"
                stroke-width="2"
                stroke-linecap="round"
                stroke-linejoin="round"
              >
                <path d="m21.73 18-8-14a2 2 0 0 0-3.48 0l-8 14A2 2 0 0 0 4 21h16a2 2 0 0 0 1.73-3Z" />
                <path d="M12 9v4" />
                <path d="M12 17h.01" />
              </svg>
              <svg
                v-else
                width="20"
                height="20"
                viewBox="0 0 24 24"
                fill="none"
                stroke="currentColor"
                stroke-width="2"
                stroke-linecap="round"
                stroke-linejoin="round"
              >
                <circle cx="12" cy="12" r="10" />
                <path d="M12 16v-4" />
                <path d="M12 8h.01" />
              </svg>
            </div>
            <h2 class="modal-title">{{ title }}</h2>
          </div>
          <div class="modal-body">
            <p class="modal-message">{{ message }}</p>
          </div>
          <div class="modal-actions">
            <button class="btn btn-cancel" :disabled="loading" @click="emit('cancel')">
              {{ cancelLabel ?? 'Cancel' }}
            </button>
            <button
              class="btn btn-confirm"
              :class="variant ?? 'default'"
              :disabled="loading"
              @click="emit('confirm')"
            >
              <span v-if="loading" class="spinner" />
              {{ loading ? 'Deleting...' : (confirmLabel ?? 'Confirm') }}
            </button>
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
  max-width: 400px;
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
}

.modal-icon.danger {
  background: var(--error-bg);
  color: var(--accent-rose);
}

.modal-icon.default {
  background: var(--accent-glow);
  color: var(--accent-blue);
}

.modal-title {
  font-size: 1rem;
  font-weight: 600;
}

.modal-body {
  padding: var(--space-3) var(--space-5) var(--space-5);
}

.modal-message {
  color: var(--text-secondary);
  font-size: 0.85rem;
  line-height: 1.5;
}

.modal-actions {
  display: flex;
  justify-content: flex-end;
  gap: var(--space-2);
  padding: var(--space-4) var(--space-5);
  background: var(--bg-surface-alt);
  border-top: 0.5px solid var(--border-default);
}

.btn {
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

.btn-cancel:hover {
  color: var(--text-primary);
  border-color: var(--border-active);
}

.btn-confirm.danger {
  background: var(--accent-rose);
  color: var(--text-on-accent);
}

.btn-confirm.danger:hover {
  background: color-mix(in srgb, var(--accent-rose) 85%, var(--gruvbox-fg0));
}

.btn-confirm.default {
  background: var(--accent-blue);
  color: var(--bg-primary);
}

.btn-confirm.default:hover {
  background: color-mix(in srgb, var(--accent-blue) 85%, var(--gruvbox-fg0));
}

.btn:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

.btn-confirm {
  display: inline-flex;
  align-items: center;
  gap: var(--space-2);
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

/* Transition */
.confirm-modal-enter-active,
.confirm-modal-leave-active {
  transition: opacity var(--transition-fast);
}

.confirm-modal-enter-active .modal,
.confirm-modal-leave-active .modal {
  transition: transform var(--transition-fast);
}

.confirm-modal-enter-from,
.confirm-modal-leave-to {
  opacity: 0;
}

.confirm-modal-enter-from .modal {
  transform: scale(0.95);
}

.confirm-modal-leave-to .modal {
  transform: scale(0.95);
}
</style>
