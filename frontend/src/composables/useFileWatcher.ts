import { ref, onBeforeUnmount } from 'vue'

export interface FsEvent {
  type: 'create' | 'write' | 'remove' | 'rename'
  path: string
  name: string
  isDir: boolean
}

export function useFileWatcher(workspaceId: () => number) {
  const connected = ref(false)
  let ws: WebSocket | null = null
  let reconnectTimer: ReturnType<typeof setTimeout> | null = null
  const listeners: Array<(event: FsEvent) => void> = []

  function connect() {
    disconnect()

    const id = workspaceId()
    if (!id) return

    const proto = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
    const wsUrl = `${proto}//${window.location.host}/api/workspaces/${id}/watch`
    ws = new WebSocket(wsUrl)

    ws.onopen = () => {
      connected.value = true
    }

    ws.onmessage = (event) => {
      try {
        const fsEvent: FsEvent = JSON.parse(event.data)
        for (const listener of listeners) {
          listener(fsEvent)
        }
      } catch {
        // Ignore malformed messages
      }
    }

    ws.onerror = () => {
      connected.value = false
    }

    ws.onclose = () => {
      connected.value = false
      // Reconnect after a delay
      reconnectTimer = setTimeout(() => connect(), 3000)
    }
  }

  function disconnect() {
    if (reconnectTimer) {
      clearTimeout(reconnectTimer)
      reconnectTimer = null
    }
    if (ws) {
      ws.onclose = null
      ws.close()
      ws = null
    }
    connected.value = false
  }

  function onEvent(listener: (event: FsEvent) => void) {
    listeners.push(listener)
  }

  onBeforeUnmount(() => {
    disconnect()
  })

  return { connected, connect, disconnect, onEvent }
}
