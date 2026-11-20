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
  // Set when the socket dropped on its own: whatever changed until it is
  // back sent no events, so listeners reload once it reconnects.
  let missedEvents = false
  const listeners: Array<(event: FsEvent) => void> = []
  const resyncListeners: Array<() => void> = []

  function connect() {
    missedEvents = false
    open()
  }

  function open() {
    close()

    const id = workspaceId()
    if (!id) return

    const proto = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
    const wsUrl = `${proto}//${window.location.host}/api/workspaces/${id}/watch`
    ws = new WebSocket(wsUrl)

    ws.onopen = () => {
      connected.value = true
      if (!missedEvents) return
      missedEvents = false
      for (const listener of resyncListeners) {
        listener()
      }
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
      missedEvents = true
      // Reconnect after a delay
      reconnectTimer = setTimeout(open, 3000)
    }
  }

  function close() {
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

  function disconnect() {
    missedEvents = false
    close()
  }

  function onEvent(listener: (event: FsEvent) => void) {
    listeners.push(listener)
  }

  // Called when the watcher reconnects after losing its connection, since
  // changes made meanwhile sent no events.
  function onResync(listener: () => void) {
    resyncListeners.push(listener)
  }

  onBeforeUnmount(() => {
    disconnect()
  })

  return { connected, connect, disconnect, onEvent, onResync }
}
