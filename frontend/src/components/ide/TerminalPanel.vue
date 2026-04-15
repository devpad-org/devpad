<script setup lang="ts">
import { ref } from 'vue'

defineProps<{
  workspaceName: string
}>()

const history = ref<string[]>([
  '\x1b[32mdevpad\x1b[0m:\x1b[34m~/workspace\x1b[0m$ npm install',
  'added 847 packages in 12s',
  '',
  '\x1b[32mdevpad\x1b[0m:\x1b[34m~/workspace\x1b[0m$ npm run dev',
  '',
  '  VITE v5.4.0  ready in 312 ms',
  '',
  '  ➜  Local:   http://localhost:5173/',
  '  ➜  Network: http://172.17.0.2:5173/',
  '',
])

const inputValue = ref('')

function handleInput() {
  if (!inputValue.value.trim()) return
  history.value.push(`\x1b[32mdevpad\x1b[0m:\x1b[34m~/workspace\x1b[0m$ ${inputValue.value}`)
  history.value.push('Command not connected — this is a placeholder terminal.')
  history.value.push('')
  inputValue.value = ''
}

function renderLine(line: string): string {
  return line
    .replace(/\x1b\[32m/g, '<span style="color: var(--accent-green)">')
    .replace(/\x1b\[34m/g, '<span style="color: var(--accent-blue)">')
    .replace(/\x1b\[0m/g, '</span>')
}
</script>

<template>
  <div class="terminal-panel">
    <div class="terminal-header">
      <span class="terminal-title">
        <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
          <polyline points="4 17 10 11 4 5" />
          <line x1="12" x2="20" y1="19" y2="19" />
        </svg>
        Terminal
      </span>
      <span class="terminal-shell">bash</span>
    </div>
    <div class="terminal-body">
      <div class="terminal-output">
        <div
          v-for="(line, i) in history"
          :key="i"
          class="terminal-line"
          v-html="renderLine(line) || '&nbsp;'"
        />
      </div>
      <div class="terminal-input-row">
        <span class="terminal-prompt">
          <span style="color: var(--accent-green)">devpad</span>:<span style="color: var(--accent-blue)">~/workspace</span>$&nbsp;
        </span>
        <input
          v-model="inputValue"
          class="terminal-input"
          type="text"
          spellcheck="false"
          autocomplete="off"
          @keydown.enter="handleInput"
        />
      </div>
    </div>
  </div>
</template>

<style scoped>
.terminal-panel {
  display: flex;
  flex-direction: column;
  height: 100%;
  background: #0a0d13;
}

.terminal-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: var(--space-1) var(--space-3);
  background: var(--bg-surface);
  border-bottom: 1px solid var(--border-default);
  flex-shrink: 0;
}

.terminal-title {
  display: flex;
  align-items: center;
  gap: 6px;
  font-size: 0.75rem;
  font-weight: 500;
  color: var(--text-secondary);
  text-transform: uppercase;
  letter-spacing: 0.03em;
}

.terminal-shell {
  font-size: 0.7rem;
  color: var(--text-muted);
  padding: 1px 8px;
  background: var(--bg-hover);
  border-radius: var(--radius-sm);
}

.terminal-body {
  flex: 1;
  overflow-y: auto;
  padding: var(--space-2) var(--space-3);
  font-family: var(--font-mono);
  font-size: 0.78rem;
  line-height: 1.5;
}

.terminal-line {
  white-space: pre;
  color: var(--text-secondary);
}

.terminal-input-row {
  display: flex;
  align-items: center;
  margin-top: 2px;
}

.terminal-prompt {
  font-family: var(--font-mono);
  font-size: 0.78rem;
  white-space: pre;
  color: var(--text-secondary);
  flex-shrink: 0;
}

.terminal-input {
  flex: 1;
  background: none;
  border: none;
  outline: none;
  font-family: var(--font-mono);
  font-size: 0.78rem;
  color: var(--text-primary);
  caret-color: var(--accent-blue);
}
</style>
