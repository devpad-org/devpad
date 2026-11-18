<script setup lang="ts">
import DocLink from '@/components/DocLink.vue'
import { computed, ref, onMounted, onUnmounted, watch } from 'vue'
import { ApiError } from '@/api/client'
import { useServiceStore } from '@/stores/services'
import type { WorkspaceService } from '@/api/services'
import ConfirmModal from '@/components/ConfirmModal.vue'
import WorkspaceServiceCard from './WorkspaceServiceCard.vue'
import { serviceTypeLabel } from './serviceCatalog'

const props = defineProps<{
  workspaceId: number
  // The project, when this is its project workspace: its sandboxes get the
  // project's services, which this workspace's may differ from.
  projectId?: number | null
}>()

const store = useServiceStore()
const actionError = ref('')
const deleteTarget = ref<WorkspaceService | null>(null)
const deleting = ref(false)
const togglingId = ref<number | null>(null)

let pollTimer: ReturnType<typeof setInterval> | null = null

const runningCount = computed(() => store.services.filter((s) => s.status === 'running').length)
const stoppedCount = computed(() => store.services.length - runningCount.value)

const adopting = ref(false)

// This workspace's services that the project's sandboxes don't get.
const notInProject = computed(() => {
  const projectId = props.projectId ?? null
  if (projectId === null || store.projectLoadedFor !== projectId || store.loadedFor !== props.workspaceId) return []
  const project = new Set(store.projectServices.map((s) => s.serviceType))
  return store.services.filter((s) => !project.has(s.serviceType)).map((s) => serviceTypeLabel(s.serviceType))
})

const notInProjectNotice = computed(() => {
  const names = notInProject.value
  if (!names.length) return ''
  const list = names.length === 1 ? names[0] : `${names.slice(0, -1).join(', ')} and ${names[names.length - 1]}`
  return `New sandboxes don't get ${list}: only the project's services, set in the Project panel, are copied to them.`
})

function loadProjectServices() {
  const projectId = props.projectId ?? null
  if (projectId === null) return
  // A failure only hides the notice; the Project panel says why.
  store.fetchProjectServices(projectId).catch(() => {})
}

async function adoptServices() {
  const projectId = props.projectId ?? null
  if (projectId === null || adopting.value) return
  adopting.value = true
  actionError.value = ''
  try {
    await store.adoptWorkspaceServices(projectId)
  } catch (e) {
    actionError.value = e instanceof ApiError && e.status < 500 ? e.message : 'Failed to add the services to the project'
  } finally {
    adopting.value = false
  }
}

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
  loadProjectServices()
  pollTimer = setInterval(refresh, 10000)
})

watch(() => props.projectId, loadProjectServices)

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
        <DocLink topic="services" label="Services" />
      </span>
      <span v-if="store.services.length > 0" class="services-summary">
        <span v-if="runningCount" class="summary-item is-running">
          <span class="summary-dot" />{{ runningCount }} running
        </span>
        <span v-if="stoppedCount" class="summary-item">
          <span class="summary-dot" />{{ stoppedCount }} stopped
        </span>
      </span>
      <button class="services-refresh" @click="refresh" title="Refresh workspace services" aria-label="Refresh workspace services">
        <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
          <path d="M21 12a9 9 0 1 1-9-9c2.52 0 4.93 1 6.74 2.74L21 8" />
          <path d="M21 3v5h-5" />
        </svg>
      </button>
    </header>

    <div v-if="actionError" class="services-error" role="alert">{{ actionError }}</div>

    <div v-if="store.loading && store.services.length === 0" class="services-loading">
      <div class="services-spinner" />
      Loading services…
    </div>

    <div v-else-if="store.services.length === 0" class="services-empty">
      <div class="empty-icon">
        <svg width="22" height="22" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round">
          <ellipse cx="12" cy="5" rx="9" ry="3" />
          <path d="M3 5v14a9 3 0 0 0 18 0V5" />
          <path d="M3 12a9 3 0 0 0 18 0" />
        </svg>
      </div>
      <h3>No services yet</h3>
      <p>Deploy a database or search engine from the Service Catalog. Its connection details show up here and in new terminals.</p>
    </div>

    <div v-else class="services-body">
      <div v-if="notInProjectNotice" class="services-callout">
        <svg class="callout-icon" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
          <circle cx="12" cy="12" r="10" />
          <path d="M12 16v-4" />
          <path d="M12 8h.01" />
        </svg>
        <span class="callout-text">{{ notInProjectNotice }}</span>
        <button class="callout-btn" :disabled="adopting" @click="adoptServices">
          {{ adopting ? 'Adding…' : 'Give sandboxes a copy' }}
        </button>
      </div>

      <div class="services-list">
        <WorkspaceServiceCard
          v-for="service in store.services"
          :key="service.id"
          :service="service"
          :toggling="togglingId === service.id"
          @toggle="toggleService"
          @remove="confirmDelete"
        />
      </div>

      <p class="services-note">
        <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
          <polyline points="4 17 10 11 4 5" />
          <line x1="12" y1="19" x2="20" y2="19" />
        </svg>
        <span>
          New terminals and commands get each service's connection details as environment variables, such as <code>POSTGRES_HOST</code>. Terminals opened before a service was added or removed still have the old ones.
        </span>
      </p>
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
  --ide-header-icon: var(--hue-aqua);
  display: flex;
  flex-direction: column;
  height: 100%;
  min-height: 0;
  overflow-y: auto;
  background: var(--ide-surface-bg);
}

.services-surface-header {
  position: sticky;
  top: 0;
  z-index: 1;
  display: flex;
  align-items: center;
  gap: var(--space-3);
  height: 38px;
  padding: 0 var(--space-2) 0 var(--space-3);
  border-bottom: 0.5px solid var(--border-subtle);
  background: var(--ide-header-bg);
  flex-shrink: 0;
}

.services-title {
  display: flex;
  align-items: center;
  gap: 6px;
  min-width: 0;
  color: var(--text-primary);
  font-size: var(--ide-header-title-size);
  font-weight: 600;
  white-space: nowrap;
}

.panel-icon {
  color: var(--ide-header-icon);
  flex-shrink: 0;
}

.services-summary {
  display: flex;
  flex: 1;
  align-items: center;
  gap: var(--space-3);
  min-width: 0;
  overflow: hidden;
}

.summary-item {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  color: var(--text-tertiary);
  font-size: 0.7rem;
  white-space: nowrap;
}

.summary-dot {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: var(--text-dim);
}

.summary-item.is-running .summary-dot {
  background: var(--success);
}

.services-refresh {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  margin-left: auto;
  width: 26px;
  height: 26px;
  flex-shrink: 0;
  color: var(--text-secondary);
  border-radius: var(--radius-md);
  transition: all var(--transition-fast);
}

.services-refresh:hover {
  color: var(--text-primary);
  background: var(--bg-hover);
}

.services-error {
  padding: var(--space-2) var(--space-4);
  color: var(--error);
  background: var(--error-bg);
  border-bottom: 0.5px solid var(--error-border);
  font-size: 0.75rem;
}

/* ── Loading and empty ────────────────────────────────────────────────── */

.services-loading,
.services-empty {
  display: flex;
  flex: 1;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: var(--space-2);
  padding: var(--space-8) var(--space-4);
  color: var(--text-tertiary);
  text-align: center;
}

.services-loading {
  flex-direction: row;
  font-size: 0.8rem;
}

.services-empty h3 {
  margin-top: var(--space-1);
  color: var(--text-primary);
  font-size: 0.9rem;
  font-weight: 600;
}

.services-empty p {
  max-width: 340px;
  font-size: 0.76rem;
  line-height: 1.5;
}

.empty-icon {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 44px;
  height: 44px;
  color: var(--ide-header-icon);
  background: var(--bg-elevated);
  border: 0.5px solid var(--border-subtle);
  border-radius: var(--radius-xl);
}

.services-spinner {
  width: 16px;
  height: 16px;
  border: 2px solid var(--border-subtle);
  border-top-color: var(--accent);
  border-radius: 50%;
  animation: spin 0.8s linear infinite;
}

/* ── Body ─────────────────────────────────────────────────────────────── */

.services-body {
  display: flex;
  flex-direction: column;
  gap: var(--space-3);
  width: 100%;
  max-width: 1080px;
  margin: 0 auto;
  padding: var(--space-4);
}

.services-list {
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.services-callout {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-2) var(--space-3);
  padding: 10px var(--space-3);
  background: var(--attention-bg);
  border: 0.5px solid var(--attention-border);
  border-radius: var(--radius-lg);
  color: var(--text-secondary);
  font-size: 0.74rem;
  line-height: 1.45;
}

.callout-icon {
  flex-shrink: 0;
  color: var(--attention);
}

.callout-text {
  flex: 1;
  min-width: 14rem;
}

.callout-btn {
  flex-shrink: 0;
  height: 26px;
  margin-left: auto;
  padding: 0 10px;
  border: 0.5px solid var(--attention-border);
  border-radius: var(--radius-md);
  background: var(--bg-elevated);
  color: var(--text-primary);
  font-size: 0.72rem;
  font-weight: 600;
  transition: all var(--transition-fast);
}

.callout-btn:hover:not(:disabled) {
  border-color: var(--attention);
}

.callout-btn:disabled {
  opacity: 0.6;
  cursor: default;
}

.services-note {
  display: flex;
  align-items: flex-start;
  gap: var(--space-2);
  margin: var(--space-1) 0 0;
  padding: 0 2px;
  color: var(--text-tertiary);
  font-size: 0.7rem;
  line-height: 1.55;
}

.services-note svg {
  flex-shrink: 0;
  margin-top: 3px;
}

.services-note code {
  font-family: var(--font-mono);
  color: var(--text-secondary);
}

@media (max-width: 640px) {
  .services-body {
    padding: var(--space-3);
  }
}
</style>
