<script setup lang="ts">
import { ref, onMounted, onUnmounted } from 'vue'
import { useServiceStore } from '@/stores/services'
import type { WorkspaceService } from '@/api/services'
import ConfirmModal from '@/components/ConfirmModal.vue'

const props = defineProps<{
  workspaceId: number
}>()

const store = useServiceStore()

const adding = ref(false)
const addError = ref('')
const selectedType = ref('')
const deleteTarget = ref<WorkspaceService | null>(null)
const deleting = ref(false)
const togglingId = ref<number | null>(null)

const serviceTypes = [
  { value: 'postgres', label: 'PostgreSQL', icon: '🐘' },
  { value: 'mongodb', label: 'MongoDB', icon: '🍃' },
  { value: 'mariadb', label: 'MariaDB', icon: '🐬' },
  { value: 'couchdb', label: 'CouchDB', icon: '🛋️' },
]

let pollTimer: ReturnType<typeof setInterval> | null = null

function refresh() {
  if (!props.workspaceId) return
  store.fetchServices(props.workspaceId)
}

async function handleAdd() {
  if (!selectedType.value || adding.value) return
  adding.value = true
  addError.value = ''
  try {
    await store.addService(props.workspaceId, selectedType.value)
    selectedType.value = ''
  } catch (e: any) {
    const msg = e.message || ''
    if (msg.includes('409')) {
      addError.value = 'Already attached'
    } else {
      addError.value = 'Failed to add service'
    }
  } finally {
    adding.value = false
  }
}

async function toggleService(svc: WorkspaceService) {
  if (togglingId.value) return
  togglingId.value = svc.id
  try {
    if (svc.status === 'running') {
      await store.stopService(props.workspaceId, svc.id)
    } else {
      await store.startService(props.workspaceId, svc.id)
    }
  } catch {
    // refresh to get actual state
    refresh()
  } finally {
    togglingId.value = null
  }
}

function confirmDelete(svc: WorkspaceService) {
  deleteTarget.value = svc
}

async function handleDelete() {
  if (!deleteTarget.value || deleting.value) return
  deleting.value = true
  try {
    await store.removeService(props.workspaceId, deleteTarget.value.id)
  } catch {
    // error handled by store
  } finally {
    deleting.value = false
    deleteTarget.value = null
  }
}

function typeLabel(type: string) {
  return serviceTypes.find((t) => t.value === type)?.label ?? type
}

function typeIcon(type: string) {
  return serviceTypes.find((t) => t.value === type)?.icon ?? '📦'
}

// Filter out types already attached
function availableTypes() {
  const attached = new Set(store.services.map((s) => s.serviceType))
  return serviceTypes.filter((t) => !attached.has(t.value as any))
}

function copyToClipboard(text: string) {
  navigator.clipboard.writeText(text)
}

function connectionString(svc: WorkspaceService): string {
  const c = svc.config
  const host = `devpad-svc-${svc.workspaceId}-${svc.serviceType}`
  if (svc.serviceType === 'postgres') {
    return `postgresql://${c.defaultUser}:${c.defaultPass}@${host}:${c.port}/${c.defaultDb}`
  }
  if (svc.serviceType === 'mongodb') {
    return `mongodb://${c.defaultUser}:${c.defaultPass}@${host}:${c.port}/${c.defaultDb}?authSource=admin`
  }
  if (svc.serviceType === 'mariadb') {
    return `mysql://${c.defaultUser}:${c.defaultPass}@${host}:${c.port}/${c.defaultDb}`
  }
  if (svc.serviceType === 'couchdb') {
    return `http://${c.defaultUser}:${c.defaultPass}@${host}:${c.port}/${c.defaultDb}`
  }
  return ''
}

onMounted(() => {
  refresh()
  pollTimer = setInterval(refresh, 10000)
})

onUnmounted(() => {
  if (pollTimer) clearInterval(pollTimer)
})
</script>

<template>
  <div class="services-panel">
    <div class="services-header">
      <span class="services-title">
        <svg class="panel-icon" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
          <ellipse cx="12" cy="5" rx="9" ry="3" />
          <path d="M3 5v14a9 3 0 0 0 18 0V5" />
          <path d="M3 12a9 3 0 0 0 18 0" />
        </svg>
        Services
      </span>
      <button class="services-refresh" @click="refresh" title="Refresh">
        <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
          <path d="M21 12a9 9 0 1 1-9-9c2.52 0 4.93 1 6.74 2.74L21 8" />
          <path d="M21 3v5h-5" />
        </svg>
      </button>
    </div>

    <!-- Add service -->
    <div class="services-add" v-if="availableTypes().length > 0">
      <select v-model="selectedType" class="services-select" :disabled="adding">
        <option value="" disabled>Add a service…</option>
        <option v-for="t in availableTypes()" :key="t.value" :value="t.value">
          {{ t.icon }} {{ t.label }}
        </option>
      </select>
      <button
        class="services-add-btn"
        :disabled="!selectedType || adding"
        @click="handleAdd"
        title="Add service"
      >
        <svg v-if="!adding" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
          <path d="M12 5v14" />
          <path d="M5 12h14" />
        </svg>
        <svg v-else class="spinner" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
          <path d="M21 12a9 9 0 1 1-6.219-8.56" />
        </svg>
      </button>
    </div>
    <div v-if="adding" class="services-adding">
      <div class="services-spinner" />
      Creating &amp; starting service…
    </div>
    <div v-if="addError" class="services-error">{{ addError }}</div>

    <!-- Loading -->
    <div v-if="store.loading && store.services.length === 0" class="services-loading">
      <div class="services-spinner" />
      Loading…
    </div>

    <!-- Empty state -->
    <div v-else-if="store.services.length === 0" class="services-empty">
      No database services attached
    </div>

    <!-- Service list -->
    <div v-else class="services-list">
      <div
        v-for="svc in store.services"
        :key="svc.id"
        class="service-card"
      >
        <div class="service-card-header">
          <span class="service-icon">{{ typeIcon(svc.serviceType) }}</span>
          <span class="service-name">{{ typeLabel(svc.serviceType) }}</span>
          <span v-if="togglingId === svc.id" class="service-status status-toggling">
            <svg class="spinner" width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round">
              <path d="M21 12a9 9 0 1 1-6.219-8.56" />
            </svg>
            {{ svc.status === 'running' ? 'Stopping…' : 'Starting…' }}
          </span>
          <span v-else class="service-status" :class="`status-${svc.status}`">
            <span class="status-dot" />
            {{ svc.status }}
          </span>
        </div>

        <!-- Connection info -->
        <div class="service-info">
          <div class="info-row">
            <span class="info-label">Host</span>
            <span class="info-value mono">devpad-svc-{{ svc.workspaceId }}-{{ svc.serviceType }}</span>
          </div>
          <div class="info-row">
            <span class="info-label">Port</span>
            <span class="info-value mono">{{ svc.config.port }}</span>
          </div>
          <div class="info-row">
            <span class="info-label">User</span>
            <span class="info-value mono">{{ svc.config.defaultUser }}</span>
          </div>
          <div class="info-row">
            <span class="info-label">Password</span>
            <span class="info-value mono">{{ svc.config.defaultPass }}</span>
          </div>
          <div class="info-row">
            <span class="info-label">Database</span>
            <span class="info-value mono">{{ svc.config.defaultDb }}</span>
          </div>
        </div>

        <!-- Connection string -->
        <div class="service-connstr">
          <button
            class="connstr-copy"
            @click="copyToClipboard(connectionString(svc))"
            title="Copy connection string"
          >
            <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
              <rect width="14" height="14" x="8" y="8" rx="2" ry="2" />
              <path d="M4 16c-1.1 0-2-.9-2-2V4c0-1.1.9-2 2-2h10c1.1 0 2 .9 2 2" />
            </svg>
            Copy URL
          </button>
        </div>

        <!-- Actions -->
        <div class="service-actions">
          <button
            class="service-toggle-btn"
            :class="svc.status === 'running' ? 'toggle-stop' : 'toggle-start'"
            :disabled="togglingId === svc.id"
            @click="toggleService(svc)"
            :title="svc.status === 'running' ? 'Stop' : 'Start'"
          >
            <svg v-if="svc.status === 'running'" width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
              <rect x="6" y="4" width="4" height="16" />
              <rect x="14" y="4" width="4" height="16" />
            </svg>
            <svg v-else width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
              <polygon points="6 3 20 12 6 21 6 3" />
            </svg>
            {{ svc.status === 'running' ? 'Stop' : 'Start' }}
          </button>
          <button
            class="service-delete-btn"
            :disabled="togglingId === svc.id"
            @click="confirmDelete(svc)"
            title="Remove service"
          >
            <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
              <path d="M3 6h18" />
              <path d="M19 6v14c0 1-1 2-2 2H7c-1 0-2-1-2-2V6" />
              <path d="M8 6V4c0-1 1-2 2-2h4c1 0 2 1 2 2v2" />
            </svg>
            Remove
          </button>
        </div>
      </div>
    </div>

    <ConfirmModal
      :show="!!deleteTarget"
      title="Remove Service"
      :message="`Remove ${deleteTarget ? typeLabel(deleteTarget.serviceType) : ''} and delete its data volume? This cannot be undone.`"
      confirm-label="Remove"
      :loading="deleting"
      @confirm="handleDelete"
      @cancel="deleteTarget = null"
    />
  </div>
</template>

<style scoped>
.services-panel {
  display: flex;
  flex-direction: column;
  height: 100%;
  overflow-y: auto;
}

.services-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 0 var(--space-3);
  border-bottom: 0.5px solid var(--border-default);
  height: 38px;
  flex-shrink: 0;
}

.services-title {
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
  color: var(--accent-purple);
  opacity: 0.7;
  flex-shrink: 0;
}

.services-refresh {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 22px;
  height: 22px;
  border-radius: var(--radius-sm);
  color: var(--text-secondary);
  transition: all var(--transition-fast);
}

.services-refresh:hover {
  background: var(--bg-hover);
  color: var(--text-primary);
}

/* Add service row */
.services-add {
  display: flex;
  align-items: center;
  gap: var(--space-1);
  padding: var(--space-2) var(--space-3);
  border-bottom: 0.5px solid var(--border-default);
}

.services-select {
  flex: 1;
  min-width: 0;
  padding: var(--space-1) var(--space-2);
  font-size: 0.75rem;
  background: var(--bg-primary);
  color: var(--text-primary);
  border: 0.5px solid var(--border-default);
  border-radius: var(--radius-sm);
  outline: none;
  transition: border-color var(--transition-fast);
}

.services-select:focus {
  border-color: var(--accent-blue);
}

.services-add-btn {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 26px;
  height: 26px;
  border-radius: var(--radius-sm);
  color: var(--accent-green);
  background: var(--success-bg);
  transition: all var(--transition-fast);
}

.services-add-btn:hover:not(:disabled) {
  background: var(--success-bg);
}

.services-add-btn:disabled {
  opacity: 0.3;
  cursor: default;
}

.services-error {
  padding: var(--space-1) var(--space-3);
  font-size: 0.7rem;
  color: var(--accent-rose);
}

.services-loading {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: var(--space-2);
  padding: var(--space-6) var(--space-3);
  color: var(--text-muted);
  font-size: 0.75rem;
}

.services-spinner {
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

.services-empty {
  padding: var(--space-6) var(--space-3);
  text-align: center;
  color: var(--text-muted);
  font-size: 0.75rem;
}

/* Service list */
.services-list {
  display: flex;
  flex-direction: column;
  gap: var(--space-1);
  padding: var(--space-2) var(--space-3);
}

.service-card {
  background: var(--bg-primary);
  border: 0.5px solid var(--border-default);
  border-radius: var(--radius-md);
  padding: var(--space-3);
  transition: border-color var(--transition-fast);
}

.service-card:hover {
  border-color: var(--border-active);
}

.service-card-header {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  margin-bottom: var(--space-2);
}

.service-icon {
  font-size: 0.9rem;
  line-height: 1;
}

.service-name {
  font-size: 0.8rem;
  font-weight: 600;
  color: var(--text-primary);
}

.service-status {
  margin-left: auto;
  display: inline-flex;
  align-items: center;
  gap: 4px;
  font-size: 0.72rem;
  font-weight: 500;
  text-transform: capitalize;
}

.status-dot {
  width: 5px;
  height: 5px;
  border-radius: 50%;
}

.status-running {
  color: var(--accent-green);
}

.status-running .status-dot {
  background: var(--accent-green);
  box-shadow: 0 0 4px var(--accent-green);
}

.status-stopped {
  color: var(--text-muted);
}

.status-stopped .status-dot {
  background: var(--text-muted);
}

/* Connection info */
.service-info {
  display: flex;
  flex-direction: column;
  gap: 2px;
  margin-bottom: var(--space-2);
}

.info-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-2);
  padding: 1px 0;
}

.info-label {
  font-size: 0.7rem;
  color: var(--text-muted);
  flex-shrink: 0;
}

.info-value {
  font-size: 0.7rem;
  color: var(--text-secondary);
  text-align: right;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.info-value.mono {
  font-family: var(--font-mono);
  font-size: 0.72rem;
}

/* Connection string copy */
.service-connstr {
  margin-bottom: var(--space-2);
}

.connstr-copy {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  padding: 2px var(--space-2);
  font-size: 0.72rem;
  color: var(--accent-blue);
  background: var(--accent-glow);
  border: 0.5px solid var(--accent-border);
  border-radius: var(--radius-sm);
  transition: all var(--transition-fast);
}

.connstr-copy:hover {
  background: var(--accent-glow);
  border-color: var(--accent-border);
}

/* Actions */
.service-actions {
  display: flex;
  justify-content: flex-end;
  gap: var(--space-2);
}

.service-toggle-btn {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  padding: 2px var(--space-2);
  font-size: 0.72rem;
  border-radius: var(--radius-sm);
  transition: all var(--transition-fast);
}

.service-toggle-btn:disabled {
  opacity: 0.4;
  cursor: default;
}

.toggle-start {
  color: var(--accent-green);
}

.toggle-start:hover:not(:disabled) {
  background: var(--success-bg);
}

.toggle-stop {
  color: var(--accent-amber);
}

.toggle-stop:hover:not(:disabled) {
  background: var(--warning-bg);
}

.service-delete-btn {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  padding: 2px var(--space-2);
  font-size: 0.72rem;
  color: var(--text-muted);
  border-radius: var(--radius-sm);
  transition: all var(--transition-fast);
}

.service-delete-btn:disabled {
  opacity: 0.4;
  cursor: default;
}

.service-delete-btn:hover:not(:disabled) {
  color: var(--accent-rose);
  background: var(--error-bg);
}

/* Spinner */
.spinner {
  animation: spin 1s linear infinite;
}

.status-toggling {
  color: var(--accent-amber);
}

.services-adding {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  padding: var(--space-2) var(--space-3);
  color: var(--accent-blue);
  font-size: 0.72rem;
  border-bottom: 0.5px solid var(--border-default);
}
</style>
