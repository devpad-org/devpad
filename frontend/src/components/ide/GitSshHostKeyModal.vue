<script setup lang="ts">
import type { GitSshHostKey } from '@/api/git'

defineProps<{
  show: boolean
  hostKey: GitSshHostKey | null
  loading?: boolean
  error?: string
}>()

const emit = defineEmits<{
  accept: []
  cancel: []
}>()
</script>

<template>
  <Teleport to="body">
    <Transition name="ssh-key-modal">
      <div v-if="show && hostKey" class="modal-overlay" @click.self="emit('cancel')">
        <div class="modal" role="alertdialog" aria-label="Accept SSH host fingerprint">
          <div class="modal-header">
            <div class="modal-icon">
              <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                <path d="M20 13c0 5-3.5 7.5-7.66 8.95a1 1 0 0 1-.67 0C7.5 20.5 4 18 4 13V6a1 1 0 0 1 1-1c2 0 4.5-1.2 6.24-2.72a1.17 1.17 0 0 1 1.52 0C14.51 3.81 17 5 19 5a1 1 0 0 1 1 1z" />
                <path d="m9 12 2 2 4-4" />
              </svg>
            </div>
            <h2 class="modal-title">Accept SSH Host Fingerprint?</h2>
          </div>

          <div class="modal-body">
            <p class="modal-description">
              Git has not connected to this SSH host before. Verify the fingerprint with the host owner before accepting it into this workspace.
            </p>

            <dl class="fingerprint-card">
              <div class="fingerprint-row">
                <dt>Host</dt>
                <dd>{{ hostKey.host }}</dd>
              </div>
              <div class="fingerprint-row">
                <dt>Key type</dt>
                <dd>{{ hostKey.keyType }}</dd>
              </div>
              <div class="fingerprint-row">
                <dt>Fingerprint</dt>
                <dd class="fingerprint-value">{{ hostKey.fingerprint }}</dd>
              </div>
            </dl>

            <p v-if="error" class="modal-error">{{ error }}</p>
          </div>

          <div class="modal-actions">
            <button type="button" class="btn btn-cancel" :disabled="loading" @click="emit('cancel')">
              Cancel
            </button>
            <button type="button" class="btn btn-confirm" :disabled="loading" @click="emit('accept')">
              <span v-if="loading" class="spinner" />
              {{ loading ? 'Accepting…' : 'Accept Fingerprint' }}
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
  background: color-mix(in srgb, var(--bg-secondary) 70%, transparent);
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
  max-width: 480px;
  box-shadow: 0 16px 48px color-mix(in srgb, var(--bg-secondary) 70%, transparent);
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
  font-size: 0.82rem;
  line-height: 1.5;
  margin-bottom: var(--space-4);
}

.fingerprint-card {
  display: grid;
  gap: var(--space-3);
  padding: var(--space-4);
  background: var(--bg-primary);
  border: 0.5px solid var(--border-default);
  border-radius: var(--radius-md);
}

.fingerprint-row {
  display: grid;
  gap: var(--space-1);
}

.fingerprint-row dt {
  color: var(--text-muted);
  font-size: 0.72rem;
  font-weight: 600;
  letter-spacing: 0.04em;
  text-transform: uppercase;
}

.fingerprint-row dd {
  color: var(--text-primary);
  font-size: 0.84rem;
  word-break: break-word;
}

.fingerprint-value {
  font-family: var(--font-mono);
}

.modal-error {
  color: var(--accent-rose);
  font-size: 0.8rem;
  margin-top: var(--space-3);
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

.btn-confirm {
  display: inline-flex;
  align-items: center;
  gap: var(--space-2);
  background: var(--accent-blue);
  color: var(--bg-primary);
}

.btn-confirm:hover {
  background: color-mix(in srgb, var(--accent-blue) 85%, var(--text-primary));
}

.btn:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

.spinner {
  width: 14px;
  height: 14px;
  border: 2px solid color-mix(in srgb, currentColor 30%, transparent);
  border-top-color: currentColor;
  border-radius: 50%;
  animation: spin 0.6s linear infinite;
}

@keyframes spin {
  to {
    transform: rotate(360deg);
  }
}

.ssh-key-modal-enter-active,
.ssh-key-modal-leave-active {
  transition: opacity var(--transition-fast);
}

.ssh-key-modal-enter-active .modal,
.ssh-key-modal-leave-active .modal {
  transition: transform var(--transition-fast);
}

.ssh-key-modal-enter-from,
.ssh-key-modal-leave-to {
  opacity: 0;
}

.ssh-key-modal-enter-from .modal,
.ssh-key-modal-leave-to .modal {
  transform: scale(0.95);
}
</style>
