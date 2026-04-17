<script setup lang="ts">
import { ref, onMounted, onUnmounted } from 'vue'
import { workspaceApi, type WorkspaceInfo } from '@/api/workspaces'

const props = defineProps<{
  workspaceId: number
}>()

const info = ref<WorkspaceInfo | null>(null)
const loading = ref(false)
const error = ref('')

let pollTimer: ReturnType<typeof setInterval> | null = null

async function refresh() {
  if (!props.workspaceId) return
  loading.value = !info.value
  error.value = ''
  try {
    info.value = await workspaceApi.getInfo(props.workspaceId)
  } catch (e: any) {
    error.value = e.message || 'Failed to load workspace info'
  } finally {
    loading.value = false
  }
}

function formatBytes(bytes: number): string {
  if (bytes === 0) return '0 B'
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  const i = Math.floor(Math.log(bytes) / Math.log(1024))
  const val = bytes / Math.pow(1024, i)
  return `${val.toFixed(i > 1 ? 1 : 0)} ${units[i]}`
}

function formatPercent(val: number): string {
  return val.toFixed(1) + '%'
}

onMounted(() => {
  refresh()
  pollTimer = setInterval(refresh, 5000)
})

onUnmounted(() => {
  if (pollTimer) clearInterval(pollTimer)
})
</script>

<template>
  <div class="info-panel">
    <div class="info-header">
      <span class="info-title">
        <svg class="panel-icon" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
          <rect width="20" height="8" x="2" y="2" rx="2" ry="2" />
          <rect width="20" height="8" x="2" y="14" rx="2" ry="2" />
          <line x1="6" x2="6.01" y1="6" y2="6" />
          <line x1="6" x2="6.01" y1="18" y2="18" />
        </svg>
        Workspace
      </span>
      <button class="info-refresh" @click="refresh" title="Refresh">
        <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
          <path d="M21 12a9 9 0 1 1-9-9c2.52 0 4.93 1 6.74 2.74L21 8" />
          <path d="M21 3v5h-5" />
        </svg>
      </button>
    </div>

    <div v-if="loading && !info" class="info-loading">
      <div class="info-spinner" />
      Loading…
    </div>

    <div v-else-if="error && !info" class="info-error">{{ error }}</div>

    <div v-else-if="info" class="info-content">
      <!-- Agent section -->
      <div class="info-section">
        <div class="info-section-title">Agent</div>
        <div class="info-row">
          <span class="info-label">Version</span>
          <span class="info-value mono">{{ info.agentVersion || 'unknown' }}</span>
        </div>
        <div v-if="info.agentVersion && info.expectedAgentVersion && info.agentVersion !== info.expectedAgentVersion" class="info-warning">
          Expected {{ info.expectedAgentVersion }} — restart workspace to update
        </div>
      </div>

      <!-- Resource usage section -->
      <div v-if="info.stats" class="info-section">
        <div class="info-section-title">Resource Usage</div>

        <!-- CPU -->
        <div class="info-metric">
          <div class="info-metric-header">
            <span class="info-label">CPU</span>
            <span class="info-value">{{ formatPercent(info.stats.cpuPercent) }}</span>
          </div>
          <div class="info-bar">
            <div
              class="info-bar-fill info-bar-fill--cpu"
              :style="{ width: Math.min(info.stats.cpuPercent, 100) + '%' }"
            />
          </div>
        </div>

        <!-- Memory -->
        <div class="info-metric">
          <div class="info-metric-header">
            <span class="info-label">Memory</span>
            <span class="info-value">{{ formatBytes(info.stats.memoryUsage) }} / {{ formatBytes(info.stats.memoryLimit) }}</span>
          </div>
          <div class="info-bar">
            <div
              class="info-bar-fill info-bar-fill--mem"
              :style="{ width: Math.min(info.stats.memoryPercent, 100) + '%' }"
            />
          </div>
        </div>

        <!-- PIDs -->
        <div class="info-row">
          <span class="info-label">Processes</span>
          <span class="info-value">{{ info.stats.pids }}</span>
        </div>

        <!-- Network -->
        <div class="info-row">
          <span class="info-label">Network Rx</span>
          <span class="info-value">{{ formatBytes(info.stats.networkRx) }}</span>
        </div>
        <div class="info-row">
          <span class="info-label">Network Tx</span>
          <span class="info-value">{{ formatBytes(info.stats.networkTx) }}</span>
        </div>

        <!-- Disk I/O -->
        <div class="info-row">
          <span class="info-label">Disk Read</span>
          <span class="info-value">{{ formatBytes(info.stats.blockRead) }}</span>
        </div>
        <div class="info-row">
          <span class="info-label">Disk Write</span>
          <span class="info-value">{{ formatBytes(info.stats.blockWrite) }}</span>
        </div>
      </div>

      <div v-else class="info-empty">
        Container stats unavailable
      </div>
    </div>
  </div>
</template>

<style scoped>
.info-panel {
  display: flex;
  flex-direction: column;
  height: 100%;
  overflow-y: auto;
}

.info-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: var(--space-2) var(--space-3);
  border-bottom: 1px solid var(--border-default);
  flex-shrink: 0;
}

.info-title {
  display: flex;
  align-items: center;
  gap: 6px;
  font-size: 0.75rem;
  font-weight: 600;
  text-transform: uppercase;
  letter-spacing: 0.05em;
  color: var(--text-secondary);
}

.panel-icon {
  color: var(--accent-green);
  opacity: 0.7;
  flex-shrink: 0;
}

.info-refresh {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 22px;
  height: 22px;
  border-radius: var(--radius-sm);
  color: var(--text-secondary);
  transition: all var(--transition-fast);
}

.info-refresh:hover {
  background: var(--bg-hover);
  color: var(--text-primary);
}

.info-loading {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  padding: var(--space-4) var(--space-3);
  color: var(--text-muted);
  font-size: 0.8rem;
}

.info-spinner {
  width: 14px;
  height: 14px;
  border: 2px solid var(--border-default);
  border-top-color: var(--accent-blue);
  border-radius: 50%;
  animation: spin 0.8s linear infinite;
}

@keyframes spin {
  to { transform: rotate(360deg); }
}

.info-error {
  padding: var(--space-3);
  color: var(--accent-rose);
  font-size: 0.8rem;
}

.info-content {
  padding: var(--space-2) 0;
}

.info-section {
  padding: var(--space-2) var(--space-3);
}

.info-section + .info-section {
  border-top: 1px solid var(--border-default);
  margin-top: var(--space-1);
  padding-top: var(--space-3);
}

.info-section-title {
  font-size: 0.7rem;
  font-weight: 600;
  text-transform: uppercase;
  letter-spacing: 0.05em;
  color: var(--text-muted);
  margin-bottom: var(--space-2);
}

.info-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 3px 0;
}

.info-label {
  font-size: 0.78rem;
  color: var(--text-secondary);
}

.info-value {
  font-size: 0.78rem;
  color: var(--text-primary);
}

.info-value.mono {
  font-family: var(--font-mono);
  font-size: 0.72rem;
}

.info-metric {
  margin-bottom: var(--space-2);
}

.info-metric-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 4px;
}

.info-bar {
  height: 4px;
  background: var(--bg-hover);
  border-radius: 2px;
  overflow: hidden;
}

.info-bar-fill {
  height: 100%;
  border-radius: 2px;
  transition: width var(--transition-normal);
}

.info-bar-fill--cpu {
  background: var(--accent-blue);
}

.info-bar-fill--mem {
  background: var(--accent-purple);
}

.info-empty {
  padding: var(--space-4) var(--space-3);
  color: var(--text-muted);
  font-size: 0.8rem;
  text-align: center;
}

.info-warning {
  margin-top: var(--space-1);
  padding: var(--space-1) var(--space-2);
  background: rgba(245, 158, 11, 0.08);
  border: 1px solid rgba(245, 158, 11, 0.2);
  border-radius: var(--radius-sm);
  color: var(--accent-amber);
  font-size: 0.72rem;
}
</style>
