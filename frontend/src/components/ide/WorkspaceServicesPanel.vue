<script setup lang="ts">
import { ref, onMounted, onUnmounted } from 'vue'
import { useServiceStore } from '@/stores/services'
import type { WorkspaceService } from '@/api/services'
import ConfirmModal from '@/components/ConfirmModal.vue'
import WorkspaceServiceCard from './WorkspaceServiceCard.vue'
import { serviceTypeLabel } from './serviceCatalog'

const props = defineProps<{
  workspaceId: number
}>()

const store = useServiceStore()
const actionError = ref('')
const deleteTarget = ref<WorkspaceService | null>(null)
const deleting = ref(false)
const togglingId = ref<number | null>(null)

let pollTimer: ReturnType<typeof setInterval> | null = null

function refresh() {
  if (!props.workspaceId) return
  store.fetchServices(props.workspaceId)
}

async function toggleService(service: WorkspaceService) {
  if (togglingId.value) return

  togglingId.value = service.id
  actionError.value = ''

  try {
    if (service.status === 'running') {
      await store.stopService(props.workspaceId, service.id)
    } else {
      await store.startService(props.workspaceId, service.id)
    }
  } catch {
    actionError.value = `Failed to ${service.status === 'running' ? 'stop' : 'start'} ${serviceTypeLabel(service.serviceType)}`
    refresh()
  } finally {
    togglingId.value = null
  }
}

function confirmDelete(service: WorkspaceService) {
  actionError.value = ''
  deleteTarget.value = service
}

async function handleDelete() {
  if (!deleteTarget.value || deleting.value) return

  const service = deleteTarget.value
  deleting.value = true
  actionError.value = ''

  try {
    await store.removeService(props.workspaceId, service.id)
    deleteTarget.value = null
  } catch {
    actionError.value = `Failed to remove ${serviceTypeLabel(service.serviceType)}`
  } finally {
    deleting.value = false
  }
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
  <div class="workspace-services-panel">
    <header class="services-surface-header">
      <span class="services-title">
        <svg class="panel-icon" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
          <ellipse cx="12" cy="5" rx="9" ry="3" />
          <path d="M3 5v14a9 3 0 0 0 18 0V5" />
          <path d="M3 12a9 3 0 0 0 18 0" />
        </svg>
        Workspace Services
        <span v-if="store.services.length > 0" class="services-count">{{ store.services.length }}</span>
      </span>
      <button class="services-refresh" @click="refresh" title="Refresh workspace services">
        <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
          <path d="M21 12a9 9 0 1 1-9-9c2.52 0 4.93 1 6.74 2.74L21 8" />
          <path d="M21 3v5h-5" />
        </svg>
      </button>
    </header>

    <div v-if="actionError" class="services-error">{{ actionError }}</div>

    <div v-if="store.loading && store.services.length === 0" class="services-loading">
      <div class="services-spinner" />
      Loading services…
    </div>

    <div v-else-if="store.services.length === 0" class="services-empty">
      <div class="empty-icon">
        <svg width="26" height="26" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round">
          <ellipse cx="12" cy="5" rx="9" ry="3" />
          <path d="M3 5v14a9 3 0 0 0 18 0V5" />
          <path d="M3 12a9 3 0 0 0 18 0" />
        </svg>
      </div>
      <h3>No services attached</h3>
      <p>Select a database from the service catalog to deploy it into this workspace.</p>
    </div>

    <div v-else class="services-grid">
      <WorkspaceServiceCard
        v-for="service in store.services"
        :key="service.id"
        :service="service"
        :toggling="togglingId === service.id"
        @toggle="toggleService"
        @remove="confirmDelete"
      />
    </div>

    <ConfirmModal
      :show="!!deleteTarget"
      title="Remove Service"
      :message="`Remove ${deleteTarget ? serviceTypeLabel(deleteTarget.serviceType) : ''} and delete its data volume? This cannot be undone.`"
      confirm-label="Remove"
      :loading="deleting"
      @confirm="handleDelete"
      @cancel="deleteTarget = null"
    />
  </div>
</template>

<style scoped>
.workspace-services-panel {
  display: flex;
  flex-direction: column;
  height: 100%;
  min-height: 0;
  overflow-y: auto;
  background: var(--bg-base);
}

.services-surface-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-3);
  height: 38px;
  padding: 0 var(--space-3);
  border-bottom: 0.5px solid var(--border-default);
  background: var(--bg-surface);
  flex-shrink: 0;
}

.services-title {
  display: flex;
  align-items: center;
  gap: 6px;
  min-width: 0;
  color: var(--text-secondary);
  font-size: 0.75rem;
  font-weight: 600;
  letter-spacing: 0.05em;
  text-transform: uppercase;
}

.panel-icon {
  color: var(--accent-purple);
  opacity: 0.7;
  flex-shrink: 0;
}

.services-count {
  min-width: 18px;
  height: 18px;
  padding: 0 6px;
  border-radius: 999px;
  background: var(--bg-raised);
  color: var(--text-muted);
  font-size: 0.68rem;
  line-height: 18px;
  text-align: center;
}

.services-refresh {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 22px;
  height: 22px;
  color: var(--text-secondary);
  border-radius: var(--radius-sm);
  transition: all var(--transition-fast);
}

.services-refresh:hover {
  color: var(--text-primary);
  background: var(--bg-hover);
}

.services-error {
  padding: var(--space-2) var(--space-6);
  color: var(--accent-rose);
  background: var(--error-bg);
  border-bottom: 0.5px solid var(--error-border);
  font-size: 0.78rem;
}

.services-loading,
.services-empty {
  display: flex;
  flex: 1;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: var(--space-2);
  padding: var(--space-8);
  color: var(--text-muted);
  text-align: center;
}

.services-loading {
  flex-direction: row;
  font-size: 0.85rem;
}

.services-empty h3 {
  color: var(--text-primary);
  font-size: 1rem;
}

.services-empty p {
  max-width: 360px;
  font-size: 0.82rem;
}

.empty-icon {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 54px;
  height: 54px;
  color: var(--accent-purple);
  background: var(--accent-glow);
  border: 0.5px solid var(--accent-border);
  border-radius: var(--radius-xl);
}

.services-spinner {
  width: 16px;
  height: 16px;
  border: 2px solid var(--border-default);
  border-top-color: var(--accent-blue);
  border-radius: 50%;
  animation: spin 0.8s linear infinite;
}

.services-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(360px, 1fr));
  gap: var(--space-4);
  padding: var(--space-6);
}

@keyframes spin {
  to { transform: rotate(360deg); }
}
</style>
