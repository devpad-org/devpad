<script setup lang="ts">
import { computed, ref, onMounted, onBeforeUnmount, watch, nextTick } from 'vue'
import { Terminal } from '@xterm/xterm'
import { FitAddon } from '@xterm/addon-fit'
import '@xterm/xterm/css/xterm.css'
import { currentTheme, terminalFontFamily, terminalTheme } from '@/theme'
import { useServiceStore } from '@/stores/services'
import { serviceTypeLabel } from '@/components/ide/serviceCatalog'

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

const serviceStore = useServiceStore()

// The services the workspace had when this shell started, null until known.
// A process keeps the environment it started with, so a service added since
// is reachable only from a new shell.
const shellServices = ref<Set<number> | null>(null)

const missingServices = computed(() => {
  const known = shellServices.value
  if (!known || !connected.value || serviceStore.loadedFor !== props.workspaceId) return []
  return serviceStore.services.filter((s) => s.status === 'running' && !known.has(s.id))
})

const missingNotice = computed(() => {
  const names = missingServices.value.map((s) => serviceTypeLabel(s.serviceType))
  if (!names.length) return ''
  const list = names.length === 1 ? names[0] : `${names.slice(0, -1).join(', ')} and ${names[names.length - 1]}`
  return names.length === 1
    ? `This shell started before ${list} was added, so it doesn't have its connection variables.`
    : `This shell started before ${list} were added, so it doesn't have their connection variables.`
})

function snapshotShellServices() {
  shellServices.value = new Set(serviceStore.services.map((s) => s.id))
}

// noteShellServices records the services a shell that just started has,
// loading them first if the IDE hasn't.
function noteShellServices() {
  shellServices.value = null
  if (serviceStore.loadedFor === props.workspaceId) {
    snapshotShellServices()
  } else {
    void serviceStore.fetchServices(props.workspaceId)
  }
}

watch(() => serviceStore.loadedFor, (id) => {
  if (shellServices.value === null && connected.value && id === props.workspaceId) snapshotShellServices()
})

function dismissMissing() {
  const known = new Set(shellServices.value ?? [])
  for (const s of missingServices.value) known.add(s.id)
  shellServices.value = known
}

// newShell ends this shell, and whatever runs in it, for a new one with
// the workspace's current services in its environment.
function newShell() {
  const old = ws
  ws = null
  connected.value = false
  shellServices.value = null
  old?.close()
  clearReconnectTimer()
  reconnecting.value = false
  reconnectAttempt = 0
  terminal?.write('\r\n\x1b[32m[Started a new shell]\x1b[0m\r\n')
  connectSocket()
}

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
    fontFamily: terminalFontFamily,
    fontWeight: '400',
    fontWeightBold: '700',
    letterSpacing: 0,
    theme: terminalTheme(),
    // Programs pick ANSI colours without knowing the background; xterm
    // nudges any that would be hard to read on it.
    minimumContrastRatio: 4.5,
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
    noteShellServices()
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
    shellServices.value = null
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

watch(currentTheme, () => {
  if (terminal) terminal.options.theme = terminalTheme()
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
    <div v-if="missingNotice && !minimized" class="terminal-notice" role="status">
      <span class="terminal-notice-text">{{ missingNotice }}</span>
      <button
        class="terminal-notice-btn"
        title="Ends this shell and anything running in it, and starts a new one"
        @click="newShell"
      >
        New shell
      </button>
      <button class="terminal-notice-dismiss" aria-label="Dismiss" title="Dismiss" @click="dismissMissing">
        <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round">
          <path d="M18 6 6 18" />
          <path d="m6 6 12 12" />
        </svg>
      </button>
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
  background: var(--terminal-bg);
}

.terminal-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: var(--space-1) var(--space-3);
  background: var(--bg-elevated);
  border-bottom: 0.5px solid var(--border-subtle);
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
  color: var(--text-tertiary);
}

.terminal-status .status-dot {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: var(--error);
}

.terminal-status.connected .status-dot {
  background: var(--success);
}

.terminal-status.reconnecting .status-dot {
  background: var(--warning);
}

.terminal-toggle-btn {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 22px;
  height: 22px;
  border-radius: var(--radius-sm);
  color: var(--text-tertiary);
  transition: all var(--transition-fast);
}

.terminal-toggle-btn:hover {
  background: var(--bg-hover);
  color: var(--text-primary);
}

.terminal-notice {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  padding: var(--space-1) var(--space-3);
  font-size: 0.72rem;
  line-height: 1.4;
  color: var(--text-primary);
  background: var(--warning-bg);
  border-bottom: 0.5px solid var(--warning-border);
  flex-shrink: 0;
}

.terminal-notice-text {
  flex: 1;
  min-width: 0;
}

.terminal-notice-btn {
  flex-shrink: 0;
  padding: 2px var(--space-2);
  border: 0.5px solid var(--warning-border);
  border-radius: var(--radius-sm);
  color: var(--text-primary);
  font-size: 0.72rem;
  transition: background var(--transition-fast);
}

.terminal-notice-btn:hover {
  background: var(--bg-hover);
}

.terminal-notice-dismiss {
  display: flex;
  align-items: center;
  justify-content: center;
  flex-shrink: 0;
  width: 20px;
  height: 20px;
  border-radius: var(--radius-sm);
  color: var(--text-tertiary);
  transition: all var(--transition-fast);
}

.terminal-notice-dismiss:hover {
  background: var(--bg-hover);
  color: var(--text-primary);
}

.terminal-body {
  flex: 1;
  min-height: 0;
  overflow: hidden;
  padding: var(--space-1) var(--space-2);
  background: var(--terminal-bg);
}

.terminal-body :deep(.xterm) {
  height: 100%;
  width: 100%;
  background: var(--terminal-bg);
  font-family: var(--font-terminal);
  font-synthesis: none;
  font-variant-ligatures: none;
  font-feature-settings: "liga" 0, "calt" 0;
  text-rendering: geometricPrecision;
}

.terminal-body :deep(.xterm-screen),
.terminal-body :deep(.xterm-viewport) {
  background-color: var(--terminal-bg);
}

.terminal-body :deep(.xterm-rows) {
  font-family: var(--font-terminal);
  font-synthesis: none;
  font-variant-ligatures: none;
  font-feature-settings: "liga" 0, "calt" 0;
}

.terminal-body :deep(.xterm-viewport) {
  overflow-y: auto !important;
}

.terminal-error {
  padding: var(--space-1) var(--space-3);
  color: var(--error);
  font-size: 0.7rem;
  background: var(--error-bg);
  border-top: 0.5px solid var(--error-border);
}
</style>
