<script setup lang="ts">
import { ref, computed, onBeforeUnmount } from 'vue'
import { workspaceApi } from '@/api/workspaces'

const props = defineProps<{
  workspaceId: number
}>()

const emit = defineEmits<{
  close: []
}>()

const port = ref(3000)
const portInput = ref('3000')
const previewUrl = ref<string | null>(null)
const loading = ref(false)
const error = ref<string | null>(null)
const iframeRef = ref<HTMLIFrameElement | null>(null)

const isValidPort = computed(() => {
  const p = Number(portInput.value)
  return Number.isInteger(p) && p >= 1 && p <= 65535
})

async function openPreview() {
  if (!isValidPort.value) return

  port.value = Number(portInput.value)
  loading.value = true
  error.value = null

  try {
    const url = await workspaceApi.getPreviewURL(props.workspaceId, port.value)
    previewUrl.value = url
  } catch (e) {
    error.value = e instanceof Error ? e.message : 'Failed to open preview'
  } finally {
    loading.value = false
  }
}

function refreshPreview() {
  if (iframeRef.value && previewUrl.value) {
    // Re-fetch a fresh token URL to refresh the iframe.
    openPreview()
  }
}

async function openInNewTab() {
  if (!isValidPort.value) return

  port.value = Number(portInput.value)
  loading.value = true
  error.value = null

  try {
    const url = await workspaceApi.getPreviewURL(props.workspaceId, port.value)
    window.open(url, '_blank')
  } catch (e) {
    error.value = e instanceof Error ? e.message : 'Failed to open preview'
  } finally {
    loading.value = false
  }
}

function openExternal() {
  if (previewUrl.value) {
    // Strip the token query parameter — the preview cookie handles auth
    // after the initial iframe load, so the token is no longer needed.
    try {
      const url = new URL(previewUrl.value)
      url.searchParams.delete('token')
      window.open(url.toString(), '_blank')
    } catch {
      window.open(previewUrl.value, '_blank')
    }
  }
}

function handlePortKeydown(e: KeyboardEvent) {
  if (e.key === 'Enter') {
    openPreview()
  }
}

// Clean up URL on unmount
onBeforeUnmount(() => {
  previewUrl.value = null
})
</script>

<template>
  <div class="preview-panel">
    <div class="preview-header">
      <span class="preview-title">Preview</span>
      <div class="preview-controls">
        <input
          v-model="portInput"
          class="port-input"
          type="text"
          inputmode="numeric"
          placeholder="Port"
          @keydown="handlePortKeydown"
        />
        <button
          class="preview-btn preview-btn-primary"
          :disabled="!isValidPort || loading"
          @click="openPreview"
          title="Open in panel"
        >
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
            <polygon points="6 3 20 12 6 21 6 3" />
          </svg>
        </button>
        <button
          class="preview-btn preview-btn-popout"
          :disabled="!isValidPort || loading"
          @click="openInNewTab"
          title="Open in new tab"
        >
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
            <path d="M15 3h6v6" />
            <path d="M10 14 21 3" />
            <path d="M18 13v6a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V8a2 2 0 0 1 2-2h6" />
          </svg>
        </button>
        <button
          class="preview-btn"
          :disabled="!previewUrl"
          @click="refreshPreview"
          title="Refresh"
        >
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
            <path d="M21 12a9 9 0 0 0-9-9 9.75 9.75 0 0 0-6.74 2.74L3 8" />
            <path d="M3 3v5h5" />
            <path d="M3 12a9 9 0 0 0 9 9 9.75 9.75 0 0 0 6.74-2.74L21 16" />
            <path d="M16 16h5v5" />
          </svg>
        </button>
        <button
          class="preview-btn"
          :disabled="!previewUrl"
          @click="openExternal"
          title="Open in new tab"
        >
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
            <path d="M15 3h6v6" />
            <path d="M10 14 21 3" />
            <path d="M18 13v6a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V8a2 2 0 0 1 2-2h6" />
          </svg>
        </button>
        <div class="preview-separator" />
        <button
          class="preview-btn"
          @click="emit('close')"
          title="Close preview"
        >
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
            <path d="M18 6 6 18" />
            <path d="m6 6 12 12" />
          </svg>
        </button>
      </div>
    </div>

    <div class="preview-body">
      <div v-if="loading" class="preview-state">
        <div class="preview-spinner" />
        <span>Loading preview…</span>
      </div>
      <div v-else-if="error" class="preview-state preview-state-error">
        <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
          <circle cx="12" cy="12" r="10" />
          <path d="m15 9-6 6" />
          <path d="m9 9 6 6" />
        </svg>
        <span>{{ error }}</span>
        <button class="preview-retry" @click="openPreview">Retry</button>
      </div>
      <div v-else-if="!previewUrl" class="preview-state preview-state-empty">
        <svg width="32" height="32" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round">
          <rect width="20" height="14" x="2" y="3" rx="2" />
          <path d="M8 21h8" />
          <path d="M12 17v4" />
        </svg>
        <span>Enter a port and click play to preview your app</span>
      </div>
      <iframe
        v-else
        ref="iframeRef"
        :src="previewUrl"
        class="preview-iframe"
        sandbox="allow-scripts allow-same-origin allow-forms allow-popups allow-modals"
        allow="clipboard-read; clipboard-write"
      />
    </div>
  </div>
</template>

<style scoped>
.preview-panel {
  display: flex;
  flex-direction: column;
  height: 100%;
  background: var(--bg-surface);
}

.preview-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: var(--space-1) var(--space-2);
  border-bottom: 1px solid var(--border-default);
  flex-shrink: 0;
  min-height: 34px;
  gap: var(--space-2);
}

.preview-title {
  font-size: 0.75rem;
  font-weight: 600;
  color: var(--text-secondary);
  text-transform: uppercase;
  letter-spacing: 0.05em;
  white-space: nowrap;
}

.preview-controls {
  display: flex;
  align-items: center;
  gap: var(--space-1);
}

.port-input {
  width: 64px;
  padding: 2px var(--space-2);
  background: var(--bg-primary);
  border: 1px solid var(--border-default);
  border-radius: var(--radius-sm);
  color: var(--text-primary);
  font-family: var(--font-mono);
  font-size: 0.75rem;
  text-align: center;
  height: 24px;
  transition: border-color var(--transition-fast);
}

.port-input:focus {
  outline: none;
  border-color: var(--accent-blue);
}

.preview-btn {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 24px;
  height: 24px;
  border-radius: var(--radius-sm);
  color: var(--text-secondary);
  transition: all var(--transition-fast);
}

.preview-btn:hover:not(:disabled) {
  background: var(--bg-hover);
  color: var(--text-primary);
}

.preview-btn:disabled {
  color: var(--text-muted);
  opacity: 0.4;
  cursor: default;
}

.preview-btn-primary {
  color: var(--accent-green);
}

.preview-btn-primary:hover:not(:disabled) {
  color: var(--accent-green);
  background: rgba(16, 185, 129, 0.1);
}

.preview-btn-popout {
  color: var(--accent-blue);
}

.preview-btn-popout:hover:not(:disabled) {
  color: var(--accent-blue);
  background: rgba(0, 212, 255, 0.1);
}

.preview-separator {
  width: 1px;
  height: 14px;
  background: var(--border-default);
  margin: 0 2px;
}

.preview-body {
  flex: 1;
  position: relative;
  overflow: hidden;
}

.preview-state {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  height: 100%;
  gap: var(--space-2);
  color: var(--text-muted);
  font-size: 0.8rem;
}

.preview-state-error {
  color: var(--accent-rose);
}

.preview-state-empty svg {
  opacity: 0.3;
  margin-bottom: var(--space-1);
}

.preview-spinner {
  width: 20px;
  height: 20px;
  border: 2px solid var(--border-default);
  border-top-color: var(--accent-blue);
  border-radius: 50%;
  animation: spin 0.8s linear infinite;
}

@keyframes spin {
  to { transform: rotate(360deg); }
}

.preview-retry {
  padding: var(--space-1) var(--space-3);
  background: var(--bg-hover);
  color: var(--text-secondary);
  border: 1px solid var(--border-default);
  border-radius: var(--radius-sm);
  font-size: 0.75rem;
  cursor: pointer;
  transition: all var(--transition-fast);
}

.preview-retry:hover {
  background: var(--bg-surface-alt);
  color: var(--text-primary);
}

.preview-iframe {
  width: 100%;
  height: 100%;
  border: none;
  background: #fff;
}
</style>
