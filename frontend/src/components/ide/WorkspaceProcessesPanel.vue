<script setup lang="ts">
import { onMounted, onUnmounted, ref, watch } from 'vue'
import { workspaceApi, type ProcessInfo } from '@/api/workspaces'

const props = defineProps<{
  workspaceId: number
  active: boolean
}>()

const processes = ref<ProcessInfo[]>([])
const loading = ref(false)
const error = ref('')
const killingPid = ref<number | null>(null)

let pollTimer: ReturnType<typeof setInterval> | null = null

async function refresh() {
  if (!props.workspaceId || !props.active) return
  loading.value = processes.value.length === 0
  error.value = ''
  try {
    const res = await workspaceApi.listProcesses(props.workspaceId)
    processes.value = res.processes
  } catch (e: any) {
    error.value = e.message || 'Failed to load processes'
  } finally {
    loading.value = false
  }
}

async function killProcess(process: ProcessInfo) {
  if (!process.killable || killingPid.value !== null) return
  killingPid.value = process.pid
  error.value = ''
  try {
    await workspaceApi.killProcess(props.workspaceId, process.pid)
    await refresh()
  } catch (e: any) {
    error.value = e.message || `Failed to kill process ${process.pid}`
  } finally {
    killingPid.value = null
  }
}

function formatBytes(bytes: number): string {
  if (bytes === 0) return '0 B'
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  const i = Math.floor(Math.log(bytes) / Math.log(1024))
  const val = bytes / Math.pow(1024, i)
  return `${val.toFixed(i > 1 ? 1 : 0)} ${units[i]}`
}

function formatDuration(totalSeconds: number): string {
  const hours = Math.floor(totalSeconds / 3600)
  const minutes = Math.floor((totalSeconds % 3600) / 60)
  const seconds = totalSeconds % 60
  if (hours > 0) return `${hours}h ${minutes}m`
  if (minutes > 0) return `${minutes}m ${seconds}s`
  return `${seconds}s`
}

function startPolling() {
  refresh()
  if (pollTimer) return
  pollTimer = setInterval(refresh, 5000)
}

function stopPolling() {
  if (!pollTimer) return
  clearInterval(pollTimer)
  pollTimer = null
}

watch(
  () => props.active,
  (active) => {
    if (active) {
      startPolling()
    } else {
      stopPolling()
    }
  }
)

watch(
  () => props.workspaceId,
  () => {
    processes.value = []
    if (props.active) refresh()
  }
)

onMounted(() => {
  if (props.active) startPolling()
})

onUnmounted(stopPolling)
</script>

<template>
  <div class="process-panel">
    <header class="process-header">
      <div class="process-title">
        <span class="process-title-icon">
          <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
            <rect width="16" height="16" x="4" y="4" rx="2" />
            <rect width="6" height="6" x="9" y="9" rx="1" />
            <path d="M15 2v2" />
            <path d="M15 20v2" />
            <path d="M2 15h2" />
            <path d="M2 9h2" />
            <path d="M20 15h2" />
            <path d="M20 9h2" />
            <path d="M9 2v2" />
            <path d="M9 20v2" />
          </svg>
        </span>
        <span class="process-title-text">Processes</span>
        <span class="process-subtitle">Workspace container</span>
        <span v-if="processes.length > 0" class="process-count">{{ processes.length }}</span>
      </div>
      <button class="refresh-btn" :disabled="loading" title="Refresh processes" aria-label="Refresh processes" @click="refresh">
        <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" :class="{ spinning: loading }" aria-hidden="true">
          <path d="M21 12a9 9 0 1 1-9-9c2.52 0 4.93 1 6.74 2.74L21 8" />
          <path d="M21 3v5h-5" />
        </svg>
      </button>
    </header>

    <div v-if="error" class="process-error">{{ error }}</div>

    <div v-if="loading" class="process-state">
      <div class="process-spinner" />
      Loading processes...
    </div>

    <div v-else-if="processes.length === 0" class="process-state">
      No running processes found.
    </div>

    <div v-else class="process-table-wrap">
      <table class="process-table">
        <thead>
          <tr>
            <th>PID</th>
            <th>PPID</th>
            <th>User</th>
            <th>State</th>
            <th>Memory</th>
            <th>CPU Time</th>
            <th>Command</th>
            <th class="action-col">Action</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="process in processes" :key="process.pid">
            <td class="mono">{{ process.pid }}</td>
            <td class="mono">{{ process.ppid }}</td>
            <td>{{ process.user || 'unknown' }}</td>
            <td class="mono">{{ process.state }}</td>
            <td>{{ formatBytes(process.memoryBytes) }}</td>
            <td>{{ formatDuration(process.cpuTimeSeconds) }}</td>
            <td class="command-cell" :title="process.command">{{ process.command }}</td>
            <td class="action-col">
              <button
                class="kill-btn"
                :disabled="!process.killable || killingPid !== null"
                :title="process.killable ? `Terminate process ${process.pid}` : 'Process cannot be terminated'"
                :aria-label="process.killable ? `Terminate process ${process.pid}` : 'Process cannot be terminated'"
                @click="killProcess(process)"
              >
                <svg v-if="killingPid === process.pid" class="spinning" width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
                  <path d="M21 12a9 9 0 1 1-9-9c2.52 0 4.93 1 6.74 2.74L21 8" />
                  <path d="M21 3v5h-5" />
                </svg>
                <svg v-else width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
                  <path d="M18 6 6 18" />
                  <path d="m6 6 12 12" />
                </svg>
              </button>
            </td>
          </tr>
        </tbody>
      </table>
    </div>
  </div>
</template>

<style scoped>
.process-panel {
  display: flex;
  flex-direction: column;
  height: 100%;
  min-width: 0;
  background: var(--bg-base);
  color: var(--text-primary);
}

.process-header {
  display: flex;
  align-items: center;
  gap: var(--space-3);
  height: 38px;
  padding: 0 var(--space-3);
  border-bottom: 0.5px solid var(--border-default);
  background: var(--bg-elevated);
  flex-shrink: 0;
}

.process-title {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  flex: 1;
  min-width: 0;
  color: var(--text-primary);
  font-size: 0.74rem;
}

.process-title-icon {
  color: var(--accent-blue);
  line-height: 0;
  flex-shrink: 0;
}

.process-title-text {
  font-weight: 600;
}

.process-subtitle {
  min-width: 0;
  overflow: hidden;
  padding-left: var(--space-2);
  border-left: 0.5px solid var(--border-default);
  color: var(--text-secondary);
  text-overflow: ellipsis;
  white-space: nowrap;
}

.process-count {
  min-width: 18px;
  height: 18px;
  padding: 0 6px;
  border: 0.5px solid var(--border-subtle);
  border-radius: 999px;
  background: var(--bg-raised);
  color: var(--text-muted);
  font-family: var(--font-mono);
  font-size: 0.68rem;
  line-height: 17px;
  text-align: center;
}

.refresh-btn {
  border: 1px solid var(--border-default);
  border-radius: var(--radius-sm);
  color: var(--text-primary);
  transition: all var(--transition-fast);
}

.refresh-btn {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 24px;
  height: 24px;
  flex-shrink: 0;
  color: var(--text-secondary);
}

.refresh-btn:hover:not(:disabled) {
  background: var(--bg-hover);
  color: var(--text-primary);
}

.refresh-btn .spinning {
  animation: spin 0.8s linear infinite;
}

.process-error {
  margin: var(--space-3) var(--space-4) 0;
  padding: var(--space-2) var(--space-3);
  border: 1px solid var(--error-border);
  border-radius: var(--radius-md);
  background: var(--error-bg);
  color: var(--error);
  font-size: 0.8rem;
}

.process-state {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: var(--space-2);
  flex: 1;
  color: var(--text-secondary);
  font-size: 0.85rem;
}

.process-spinner {
  width: 14px;
  height: 14px;
  border: 2px solid var(--spinner-track);
  border-top-color: var(--accent);
  border-radius: 50%;
  animation: spin 0.8s linear infinite;
}

@keyframes spin {
  to { transform: rotate(360deg); }
}

.process-table-wrap {
  flex: 1;
  min-height: 0;
  overflow: auto;
}

.process-table {
  width: 100%;
  border-collapse: collapse;
  font-size: 0.78rem;
}

.process-table th,
.process-table td {
  padding: var(--space-2) var(--space-3);
  border-bottom: 1px solid var(--border-hairline);
  text-align: left;
  vertical-align: middle;
}

.process-table th {
  position: sticky;
  top: 0;
  z-index: 1;
  background: var(--bg-elevated);
  color: var(--text-secondary);
  font-size: 0.68rem;
  font-weight: 600;
  letter-spacing: 0.05em;
  text-transform: uppercase;
}

.process-table tbody tr:hover {
  background: var(--bg-hover);
}

.mono {
  font-family: var(--font-mono);
}

.command-cell {
  max-width: 420px;
  overflow: hidden;
  color: var(--text-secondary);
  font-family: var(--font-mono);
  text-overflow: ellipsis;
  white-space: nowrap;
}

.action-col {
  width: 56px;
  text-align: right;
}

.kill-btn {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 22px;
  height: 22px;
  border-radius: var(--radius-sm);
  color: var(--text-secondary);
  transition: all var(--transition-fast);
}

.kill-btn:hover:not(:disabled) {
  color: var(--accent-rose);
  background: var(--error-bg);
}

.kill-btn .spinning {
  animation: spin 0.8s linear infinite;
}

.refresh-btn:disabled,
.kill-btn:disabled {
  cursor: not-allowed;
  opacity: 0.4;
}
</style>
