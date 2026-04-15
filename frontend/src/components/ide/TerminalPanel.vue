<script setup lang="ts">
import { ref, onMounted, onBeforeUnmount, watch, nextTick } from 'vue'
import { Terminal } from '@xterm/xterm'
import { FitAddon } from '@xterm/addon-fit'
import '@xterm/xterm/css/xterm.css'

const props = defineProps<{
  workspaceId: number
  minimized: boolean
}>()

const emit = defineEmits<{
  'toggle-minimize': []
}>()

const terminalRef = ref<HTMLElement | null>(null)
const connected = ref(false)
const error = ref<string | null>(null)

let terminal: Terminal | null = null
let fitAddon: FitAddon | null = null
let ws: WebSocket | null = null
let resizeObserver: ResizeObserver | null = null

function connect() {
  if (!terminalRef.value) return

  error.value = null

  terminal = new Terminal({
    cursorBlink: true,
    fontSize: 13,
    fontFamily: "'JetBrains Mono', 'Fira Code', monospace",
    theme: {
      background: '#0a0d13',
      foreground: '#c9d1d9',
      cursor: '#00d4ff',
      selectionBackground: 'rgba(0, 212, 255, 0.2)',
      black: '#0a0d13',
      red: '#f43f5e',
      green: '#10b981',
      yellow: '#f59e0b',
      blue: '#00d4ff',
      magenta: '#7c3aed',
      cyan: '#06b6d4',
      white: '#c9d1d9',
      brightBlack: '#6e7681',
      brightRed: '#fb7185',
      brightGreen: '#34d399',
      brightYellow: '#fbbf24',
      brightBlue: '#38bdf8',
      brightMagenta: '#a78bfa',
      brightCyan: '#22d3ee',
      brightWhite: '#f0f6fc',
    },
  })

  fitAddon = new FitAddon()
  terminal.loadAddon(fitAddon)
  terminal.open(terminalRef.value)
  fitAddon.fit()

  const proto = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
  const wsUrl = `${proto}//${window.location.host}/api/workspaces/${props.workspaceId}/terminal`
  ws = new WebSocket(wsUrl)
  ws.binaryType = 'arraybuffer'

  ws.onopen = () => {
    connected.value = true
    // Send initial terminal size
    sendResize()
  }

  ws.onmessage = (event) => {
    if (event.data instanceof ArrayBuffer) {
      terminal?.write(new Uint8Array(event.data))
    } else {
      terminal?.write(event.data)
    }
  }

  ws.onerror = () => {
    error.value = 'Connection error'
    connected.value = false
  }

  ws.onclose = () => {
    connected.value = false
    terminal?.write('\r\n\x1b[31m[Terminal disconnected]\x1b[0m\r\n')
  }

  terminal.onData((data) => {
    if (ws && ws.readyState === WebSocket.OPEN) {
      ws.send(new TextEncoder().encode(data))
    }
  })

  // Handle resize
  resizeObserver = new ResizeObserver(() => {
    fitAddon?.fit()
    sendResize()
  })
  resizeObserver.observe(terminalRef.value)

  terminal.onResize(({ cols, rows }) => {
    if (ws && ws.readyState === WebSocket.OPEN) {
      ws.send(JSON.stringify({ type: 'resize', cols, rows }))
    }
  })
}

function sendResize() {
  if (terminal && ws && ws.readyState === WebSocket.OPEN) {
    ws.send(JSON.stringify({ type: 'resize', cols: terminal.cols, rows: terminal.rows }))
  }
}

function disconnect() {
  resizeObserver?.disconnect()
  resizeObserver = null
  ws?.close()
  ws = null
  terminal?.dispose()
  terminal = null
  fitAddon = null
  connected.value = false
}

onMounted(() => {
  connect()
})

onBeforeUnmount(() => {
  disconnect()
})

watch(() => props.workspaceId, () => {
  disconnect()
  connect()
})

watch(() => props.minimized, (isMinimized) => {
  if (!isMinimized) {
    nextTick(() => {
      fitAddon?.fit()
      sendResize()
    })
  }
})
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
      <div class="terminal-indicators">
        <span class="terminal-status" :class="{ connected }">
          <span class="status-dot" />
          {{ connected ? 'Connected' : 'Disconnected' }}
        </span>
        <button class="terminal-toggle-btn" @click="emit('toggle-minimize')" :title="minimized ? 'Expand terminal' : 'Minimize terminal'">
          <svg v-if="minimized" width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
            <polyline points="18 15 12 9 6 15" />
          </svg>
          <svg v-else width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
            <polyline points="6 9 12 15 18 9" />
          </svg>
        </button>
      </div>
    </div>
    <div v-show="!minimized" class="terminal-body" ref="terminalRef" />
    <div v-if="error && !minimized" class="terminal-error">{{ error }}</div>
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

.terminal-indicators {
  display: flex;
  align-items: center;
  gap: var(--space-2);
}

.terminal-status {
  display: flex;
  align-items: center;
  gap: 4px;
  font-size: 0.65rem;
  color: var(--text-muted);
}

.terminal-status .status-dot {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: var(--accent-rose);
}

.terminal-status.connected .status-dot {
  background: var(--accent-green);
}

.terminal-toggle-btn {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 22px;
  height: 22px;
  border-radius: var(--radius-sm);
  color: var(--text-muted);
  transition: all var(--transition-fast);
}

.terminal-toggle-btn:hover {
  background: var(--bg-hover);
  color: var(--text-primary);
}

.terminal-body {
  flex: 1;
  overflow: hidden;
  padding: var(--space-1) var(--space-2);
}

.terminal-body :deep(.xterm) {
  height: 100%;
}

.terminal-body :deep(.xterm-viewport) {
  overflow-y: auto !important;
}

.terminal-error {
  padding: var(--space-1) var(--space-3);
  color: var(--accent-rose);
  font-size: 0.7rem;
  background: rgba(244, 63, 94, 0.1);
  border-top: 1px solid rgba(244, 63, 94, 0.2);
}
</style>
