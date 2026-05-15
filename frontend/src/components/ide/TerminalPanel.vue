<script setup lang="ts">
import { computed, ref, onMounted, onBeforeUnmount, watch, nextTick } from 'vue'
import { Terminal } from '@xterm/xterm'
import { FitAddon } from '@xterm/addon-fit'
import '@xterm/xterm/css/xterm.css'
import { gruvboxTerminalTheme } from '@/theme/gruvbox'

const props = defineProps<{
  workspaceId: number
  minimized: boolean
}>()

const emit = defineEmits<{
  'toggle-minimize': []
}>()

const terminalRef = ref<HTMLElement | null>(null)
const connected = ref(false)
const reconnecting = ref(false)
const error = ref<string | null>(null)
const terminalStatus = computed(() => {
  if (connected.value) return 'Connected'
  if (reconnecting.value) return 'Reconnecting'
  return 'Disconnected'
})

let terminal: Terminal | null = null
let fitAddon: FitAddon | null = null
let ws: WebSocket | null = null
let resizeObserver: ResizeObserver | null = null
let reconnectTimer: ReturnType<typeof setTimeout> | null = null
let reconnectAttempt = 0
let closing = false

const reconnectBaseDelayMs = 1_000
const reconnectMaxDelayMs = 10_000

function connect() {
  if (!terminalRef.value) return

  error.value = null
  closing = false

  terminal = new Terminal({
    cursorBlink: true,
    fontSize: 13,
    lineHeight: 1.2,
    fontFamily: "'Geist Mono', 'JetBrains Mono', Menlo, monospace",
    theme: gruvboxTerminalTheme,
  })

  fitAddon = new FitAddon()
  terminal.loadAddon(fitAddon)
  terminal.open(terminalRef.value)
  fitAddon.fit()

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

  connectSocket()
}

function connectSocket() {
  if (closing) return

  clearReconnectTimer()

  const proto = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
  const wsUrl = `${proto}//${window.location.host}/api/workspaces/${props.workspaceId}/terminal`
  const socket = new WebSocket(wsUrl)
  ws = socket
  socket.binaryType = 'arraybuffer'

  socket.onopen = () => {
    if (ws !== socket) return

    connected.value = true
    reconnecting.value = false
    error.value = null

    if (reconnectAttempt > 0) {
      terminal?.write('\r\n\x1b[32m[Connected to a new terminal shell]\x1b[0m\r\n')
    }
    reconnectAttempt = 0
    sendResize()
  }

  socket.onmessage = (event) => {
    if (event.data instanceof ArrayBuffer) {
      terminal?.write(new Uint8Array(event.data))
    } else {
      terminal?.write(event.data)
    }
  }

  socket.onerror = () => {
    if (ws !== socket) return

    error.value = 'Connection error. Reconnecting...'
    connected.value = false
  }

  socket.onclose = () => {
    if (ws !== socket) return

    ws = null
    connected.value = false
    if (closing) return

    terminal?.write('\r\n\x1b[31m[Terminal disconnected]\x1b[0m\r\n')
    scheduleReconnect()
  }
}

function scheduleReconnect() {
  if (closing || reconnectTimer) return

  reconnecting.value = true
  reconnectAttempt += 1
  const delay = Math.min(reconnectBaseDelayMs * 2 ** (reconnectAttempt - 1), reconnectMaxDelayMs)
  terminal?.write(`\x1b[33m[Reconnecting in ${Math.round(delay / 1000)}s...]\x1b[0m\r\n`)

  reconnectTimer = setTimeout(() => {
    reconnectTimer = null
    connectSocket()
  }, delay)
}

function clearReconnectTimer() {
  if (!reconnectTimer) return

  clearTimeout(reconnectTimer)
  reconnectTimer = null
}

function sendResize() {
  if (terminal && ws && ws.readyState === WebSocket.OPEN) {
    ws.send(JSON.stringify({ type: 'resize', cols: terminal.cols, rows: terminal.rows }))
  }
}

function disconnect() {
  closing = true
  clearReconnectTimer()
  resizeObserver?.disconnect()
  resizeObserver = null
  ws?.close()
  ws = null
  terminal?.dispose()
  terminal = null
  fitAddon = null
  connected.value = false
  reconnecting.value = false
  reconnectAttempt = 0
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
        <span class="terminal-status" :class="{ connected, reconnecting }">
          <span class="status-dot" />
          {{ terminalStatus }}
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
  background: var(--bg-base);
}

.terminal-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: var(--space-1) var(--space-3);
  background: var(--bg-surface);
  border-bottom: 0.5px solid var(--border-default);
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
  font-size: 0.72rem;
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

.terminal-status.reconnecting .status-dot {
  background: var(--accent-amber);
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
  background: var(--error-bg);
  border-top: 0.5px solid var(--error-border);
}
</style>
