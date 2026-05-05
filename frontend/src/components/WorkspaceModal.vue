<script setup lang="ts">
import { ref, watch } from 'vue'

const props = defineProps<{
  show: boolean
  title: string
  name?: string
  description?: string
}>()

const emit = defineEmits<{
  close: []
  submit: [name: string, description: string]
}>()

const nameInput = ref(props.name ?? '')
const descInput = ref(props.description ?? '')
const error = ref('')

watch(
  () => props.show,
  (visible) => {
    if (visible) {
      nameInput.value = props.name ?? ''
      descInput.value = props.description ?? ''
      error.value = ''
    }
  },
)

function handleSubmit() {
  if (!nameInput.value.trim()) {
    error.value = 'Name is required'
    return
  }
  emit('submit', nameInput.value.trim(), descInput.value.trim())
}
</script>

<template>
  <Teleport to="body">
    <div v-if="show" class="modal-overlay" @click.self="emit('close')">
      <div class="modal">
        <div class="modal-header">
          <h2 class="modal-title">{{ title }}</h2>
          <button class="modal-close" @click="emit('close')">&times;</button>
        </div>
        <form class="modal-body" @submit.prevent="handleSubmit">
          <div class="form-group">
            <label class="form-label" for="ws-name">Name</label>
            <input
              id="ws-name"
              v-model="nameInput"
              class="form-input"
              type="text"
              placeholder="My Project"
              autocomplete="off"
            />
          </div>
          <div class="form-group">
            <label class="form-label" for="ws-desc">Description</label>
            <textarea
              id="ws-desc"
              v-model="descInput"
              class="form-input form-textarea"
              placeholder="Optional description..."
              rows="3"
            />
          </div>
          <p v-if="error" class="form-error">{{ error }}</p>
          <div class="modal-actions">
            <button type="button" class="btn btn-secondary" @click="emit('close')">Cancel</button>
            <button type="submit" class="btn btn-primary">Save</button>
          </div>
        </form>
      </div>
    </div>
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

.form-group {
  margin-bottom: var(--space-4);
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

.form-textarea {
  resize: vertical;
  min-height: 60px;
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
}

.btn {
  padding: var(--space-2) var(--space-4);
  font-size: 0.8rem;
  font-weight: 500;
  border-radius: var(--radius-md);
  transition: all var(--transition-fast);
}

.btn-secondary {
  color: var(--text-secondary);
  border: 0.5px solid var(--border-default);
}

.btn-secondary:hover {
  color: var(--text-primary);
  border-color: var(--border-active);
}

.btn-primary {
  background: var(--accent-blue);
  color: var(--bg-primary);
}

.btn-primary:hover {
  background: color-mix(in srgb, var(--accent-blue) 85%, var(--gruvbox-fg0));
}
</style>
