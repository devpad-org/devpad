<script setup lang="ts">
import { ref, watch } from 'vue'

const props = defineProps<{
  show: boolean
}>()

const emit = defineEmits<{
  submit: [name: string, email: string]
  cancel: []
}>()

const nameInput = ref('')
const emailInput = ref('')
const error = ref('')

watch(
  () => props.show,
  (visible) => {
    if (visible) {
      nameInput.value = ''
      emailInput.value = ''
      error.value = ''
    }
  },
)

function handleSubmit() {
  if (!nameInput.value.trim()) {
    error.value = 'Name is required'
    return
  }
  if (!emailInput.value.trim()) {
    error.value = 'Email is required'
    return
  }
  emit('submit', nameInput.value.trim(), emailInput.value.trim())
}
</script>

<template>
  <Teleport to="body">
    <Transition name="git-config-modal">
      <div v-if="show" class="modal-overlay" @click.self="emit('cancel')">
        <div class="modal" role="dialog" aria-label="Configure Git Identity">
          <div class="modal-header">
            <div class="modal-icon">
              <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                <path d="M20 21v-2a4 4 0 0 0-4-4H8a4 4 0 0 0-4 4v2" />
                <circle cx="12" cy="7" r="4" />
              </svg>
            </div>
            <h2 class="modal-title">Configure Git Identity</h2>
          </div>
          <form class="modal-body" @submit.prevent="handleSubmit">
            <p class="modal-description">
              Git needs your name and email to make commits. This will be stored in the workspace git config.
            </p>
            <div class="form-group">
              <label class="form-label" for="git-name">Name</label>
              <input
                id="git-name"
                v-model="nameInput"
                class="form-input"
                type="text"
                placeholder="Your Name"
                autocomplete="name"
              />
            </div>
            <div class="form-group">
              <label class="form-label" for="git-email">Email</label>
              <input
                id="git-email"
                v-model="emailInput"
                class="form-input"
                type="email"
                placeholder="you@example.com"
                autocomplete="email"
              />
            </div>
            <p v-if="error" class="form-error">{{ error }}</p>
            <div class="modal-actions">
              <button type="button" class="btn btn-cancel" @click="emit('cancel')">Cancel</button>
              <button type="submit" class="btn btn-confirm">Save &amp; Commit</button>
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

.form-error {
  color: var(--accent-rose);
  font-size: 0.8rem;
  margin-bottom: var(--space-3);
}

.modal-actions {
  display: flex;
  justify-content: flex-end;
  gap: var(--space-2);
  padding-top: var(--space-2);
  background: var(--bg-surface-alt);
  margin: var(--space-4) calc(-1 * var(--space-5)) calc(-1 * var(--space-5));
  padding: var(--space-4) var(--space-5);
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
  background: var(--accent-blue);
  color: var(--bg-primary);
}

.btn-confirm:hover {
  background: color-mix(in srgb, var(--accent-blue) 85%, var(--gruvbox-fg0));
}

.btn:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

/* Transition */
.git-config-modal-enter-active,
.git-config-modal-leave-active {
  transition: opacity var(--transition-fast);
}

.git-config-modal-enter-active .modal,
.git-config-modal-leave-active .modal {
  transition: transform var(--transition-fast);
}

.git-config-modal-enter-from,
.git-config-modal-leave-to {
  opacity: 0;
}

.git-config-modal-enter-from .modal {
  transform: scale(0.95);
}

.git-config-modal-leave-to .modal {
  transform: scale(0.95);
}
</style>
