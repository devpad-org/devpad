<script setup lang="ts">
import { computed } from 'vue'

const props = defineProps<{
  filePath: string | null
}>()

const fileName = computed(() => {
  if (!props.filePath) return null
  return props.filePath.split('/').pop()
})

const placeholderContent = computed(() => {
  if (!props.filePath) return ''
  if (props.filePath.endsWith('.vue')) return vueTemplate
  if (props.filePath.endsWith('.ts')) return tsTemplate
  if (props.filePath.endsWith('.css')) return cssTemplate
  if (props.filePath.endsWith('.json')) return jsonTemplate
  if (props.filePath.endsWith('.md')) return mdTemplate
  if (props.filePath.endsWith('.html')) return htmlTemplate
  return genericTemplate
})

const lines = computed(() => {
  if (!placeholderContent.value) return []
  return placeholderContent.value.split('\n')
})

const vueTemplate = `<script setup lang="ts">
import { ref } from 'vue'

const count = ref(0)

function increment() {
  count.value++
}
<\/script>

<template>
  <div class="container">
    <h1>Hello World</h1>
    <p>Count: {{ count }}</p>
    <button @click="increment">+1</button>
  </div>
</template>

<style scoped>
.container {
  padding: 2rem;
}
</style>`

const tsTemplate = `import { createApp } from 'vue'
import { createPinia } from 'pinia'
import App from './App.vue'
import router from './router'

const app = createApp(App)

app.use(createPinia())
app.use(router)

app.mount('#app')`

const cssTemplate = `:root {
  --primary: #00d4ff;
  --bg: #1a1a2e;
  --surface: #1e1e2e;
  --text: #e4e4e7;
}

body {
  margin: 0;
  font-family: system-ui, sans-serif;
  background: var(--bg);
  color: var(--text);
}

.container {
  max-width: 1200px;
  margin: 0 auto;
  padding: 1rem;
}`

const jsonTemplate = `{
  "name": "my-project",
  "version": "1.0.0",
  "private": true,
  "scripts": {
    "dev": "vite",
    "build": "vue-tsc && vite build",
    "preview": "vite preview"
  },
  "dependencies": {
    "vue": "^3.4.0",
    "pinia": "^2.1.0",
    "vue-router": "^4.2.0"
  }
}`

const mdTemplate = `# Project

A modern web application built with Vue 3.

## Getting Started

\`\`\`bash
npm install
npm run dev
\`\`\`

## Features

- Fast build with Vite
- Type-safe with TypeScript
- State management with Pinia`

const htmlTemplate = `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8" />
  <meta name="viewport" content="width=device-width, initial-scale=1.0" />
  <title>My App</title>
</head>
<body>
  <div id="app"></div>
  <script type="module" src="/src/main.ts"><\/script>
</body>
</html>`

const genericTemplate = `// File contents will appear here
// when connected to a workspace`
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
    <div v-if="filePath" class="editor-content">
      <div class="editor-gutter">
        <span
          v-for="(_, i) in lines"
          :key="i"
          class="line-number"
        >{{ i + 1 }}</span>
      </div>
      <div class="editor-code">
        <pre><code>{{ placeholderContent }}</code></pre>
      </div>
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
</style>
