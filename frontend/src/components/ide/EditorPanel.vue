<script setup lang="ts">
import { ref, computed, watch } from 'vue'
import { workspaceApi } from '@/api/workspaces'

const props = defineProps<{
  workspaceId: number
  filePath: string | null
}>()

const fileName = computed(() => {
  if (!props.filePath) return null
  return props.filePath.split('/').pop()
})

const content = ref('')
const loading = ref(false)
const error = ref<string | null>(null)

const lines = computed(() => {
  if (!content.value) return []
  return content.value.split('\n')
})

watch(() => props.filePath, async (newPath) => {
  if (!newPath) {
    content.value = ''
    return
  }
  loading.value = true
  error.value = null
  try {
    content.value = await workspaceApi.readFile(props.workspaceId, newPath)
  } catch (e) {
    error.value = e instanceof Error ? e.message : 'Failed to load file'
    content.value = ''
  } finally {
    loading.value = false
  }
}, { immediate: true })
</script>

<template>
  <div class="editor-panel">
    <!-- Tab bar -->
    <div class="editor-tabs">
      <div v-if="fileName" class="editor-tab active">
        <span class="tab-name">{{ fileName }}</span>
        <button class="tab-close" title="Close">×</button>
      </div>
      <div v-else class="editor-tab-empty" />
    </div>

    <!-- Editor content -->
    <div v-if="filePath && !loading && !error" class="editor-content">
      <div class="editor-gutter">
        <span
          v-for="(_, i) in lines"
          :key="i"
          class="line-number"
        >{{ i + 1 }}</span>
      </div>
      <div class="editor-code">
        <pre><code>{{ content }}</code></pre>
      </div>
    </div>

    <!-- Loading state -->
    <div v-else-if="loading" class="editor-empty">
      <span class="loading-text">Loading...</span>
    </div>

    <!-- Error state -->
    <div v-else-if="error" class="editor-empty">
      <span class="error-text">{{ error }}</span>
    </div>

    <!-- Empty state -->
    <div v-else class="editor-empty">
      <div class="empty-logo">
        <span class="logo-text">Devpad</span>
      </div>
      <div class="empty-shortcuts">
        <div class="shortcut-row">
          <kbd>Ctrl</kbd> + <kbd>P</kbd>
          <span class="shortcut-label">Quick Open</span>
        </div>
        <div class="shortcut-row">
          <kbd>Ctrl</kbd> + <kbd>Shift</kbd> + <kbd>P</kbd>
          <span class="shortcut-label">Command Palette</span>
        </div>
        <div class="shortcut-row">
          <kbd>Ctrl</kbd> + <kbd>`</kbd>
          <span class="shortcut-label">Toggle Terminal</span>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.editor-panel {
  display: flex;
  flex-direction: column;
  height: 100%;
  background: var(--bg-primary);
}

/* Tabs */
.editor-tabs {
  display: flex;
  align-items: stretch;
  background: var(--bg-surface);
  border-bottom: 1px solid var(--border-default);
  min-height: 34px;
  flex-shrink: 0;
}

.editor-tab {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 0 var(--space-3);
  font-size: 0.78rem;
  color: var(--text-secondary);
  border-right: 1px solid var(--border-default);
  cursor: pointer;
  transition: all var(--transition-fast);
}

.editor-tab.active {
  color: var(--text-primary);
  background: var(--bg-primary);
  border-bottom: 1px solid var(--accent-blue);
  margin-bottom: -1px;
}

.tab-close {
  font-size: 1rem;
  line-height: 1;
  color: var(--text-muted);
  border-radius: var(--radius-sm);
  width: 18px;
  height: 18px;
  display: flex;
  align-items: center;
  justify-content: center;
  transition: all var(--transition-fast);
}

.tab-close:hover {
  background: var(--bg-hover);
  color: var(--text-primary);
}

/* Editor content */
.editor-content {
  display: flex;
  flex: 1;
  overflow: auto;
  font-family: var(--font-mono);
  font-size: 0.82rem;
  line-height: 1.65;
}

.editor-gutter {
  display: flex;
  flex-direction: column;
  padding: var(--space-3) 0;
  padding-right: var(--space-3);
  text-align: right;
  min-width: 48px;
  background: var(--bg-primary);
  border-right: 1px solid var(--border-default);
  user-select: none;
  flex-shrink: 0;
}

.line-number {
  padding: 0 var(--space-2);
  color: var(--text-muted);
  font-size: 0.75rem;
}

.editor-code {
  flex: 1;
  padding: var(--space-3) var(--space-4);
  overflow-x: auto;
}

.editor-code pre {
  margin: 0;
}

.editor-code code {
  color: var(--text-secondary);
  font-size: 0.82rem;
  line-height: 1.65;
}

/* Empty state */
.editor-empty {
  flex: 1;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: var(--space-8);
}

.empty-logo {
  opacity: 0.08;
}

.logo-text {
  font-size: 4rem;
  font-weight: 800;
  background: linear-gradient(135deg, var(--accent-blue), var(--accent-purple));
  -webkit-background-clip: text;
  background-clip: text;
  -webkit-text-fill-color: transparent;
}

.empty-shortcuts {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
}

.shortcut-row {
  display: flex;
  align-items: center;
  gap: 6px;
  font-size: 0.78rem;
  color: var(--text-muted);
}

.shortcut-label {
  margin-left: var(--space-2);
  color: var(--text-secondary);
}

kbd {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  min-width: 22px;
  height: 22px;
  padding: 0 6px;
  background: var(--bg-surface);
  border: 1px solid var(--border-default);
  border-radius: var(--radius-sm);
  font-family: var(--font-sans);
  font-size: 0.7rem;
  color: var(--text-secondary);
}

.loading-text {
  color: var(--text-muted);
  font-size: 0.85rem;
}

.error-text {
  color: var(--accent-rose);
  font-size: 0.85rem;
}
</style>
