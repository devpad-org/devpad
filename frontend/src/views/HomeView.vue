<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { useRouter } from 'vue-router'
import { useWorkspaceStore } from '@/stores/workspaces'
import WorkspaceCard from '@/components/WorkspaceCard.vue'
import WorkspaceModal from '@/components/WorkspaceModal.vue'
import ConfirmModal from '@/components/ConfirmModal.vue'
import type { Workspace } from '@/api/workspaces'

const store = useWorkspaceStore()
const router = useRouter()

const showCreateModal = ref(false)
const showEditModal = ref(false)
const showDeleteModal = ref(false)
const editTarget = ref<Workspace | null>(null)
const deleteTarget = ref<Workspace | null>(null)

onMounted(() => {
  store.fetchWorkspaces()
})

async function handleCreate(name: string, description: string) {
  try {
    await store.createWorkspace(name, description)
    showCreateModal.value = false
  } catch {
    // error handled by store
  }
}

function openEdit(workspace: Workspace) {
  editTarget.value = workspace
  showEditModal.value = true
}

async function handleEdit(name: string, description: string) {
  if (!editTarget.value) return
  try {
    await store.updateWorkspace(editTarget.value.id, name, description)
    showEditModal.value = false
    editTarget.value = null
  } catch {
    // error handled by store
  }
}

function openDelete(workspace: Workspace) {
  deleteTarget.value = workspace
  showDeleteModal.value = true
}

function openWorkspace(workspace: Workspace) {
  router.push({ name: 'ide', params: { id: workspace.id } })
}

async function handleDelete() {
  if (!deleteTarget.value) return
  try {
    await store.deleteWorkspace(deleteTarget.value.id)
  } catch {
    // error handled by store
  } finally {
    showDeleteModal.value = false
    deleteTarget.value = null
  }
}
</script>

<template>
  <div class="workspaces-page">
    <div class="page-header">
      <div>
        <h1 class="page-title">Workspaces</h1>
        <p class="page-subtitle">Your development environments</p>
      </div>
      <button class="btn-create" @click="showCreateModal = true">
        <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
          <path d="M12 5v14" />
          <path d="M5 12h14" />
        </svg>
        New Workspace
      </button>
    </div>

    <div v-if="store.loading" class="empty-state">
      <p class="empty-text">Loading workspaces...</p>
    </div>

    <div v-else-if="store.error" class="empty-state">
      <p class="empty-text error-text">{{ store.error }}</p>
    </div>

    <div v-else-if="store.workspaces.length === 0" class="empty-state">
      <div class="empty-icon">
        <svg width="48" height="48" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round">
          <rect width="18" height="18" x="3" y="3" rx="2" />
          <path d="M12 8v8" />
          <path d="M8 12h8" />
        </svg>
      </div>
      <p class="empty-text">No workspaces yet</p>
      <p class="empty-hint">Create your first workspace to get started</p>
    </div>

    <div v-else class="workspace-grid">
      <WorkspaceCard
        v-for="ws in store.workspaces"
        :key="ws.id"
        :workspace="ws"
        @open="openWorkspace"
        @edit="openEdit"
        @delete="openDelete"
      />
    </div>

    <WorkspaceModal
      :show="showCreateModal"
      title="Create Workspace"
      @close="showCreateModal = false"
      @submit="handleCreate"
    />

    <WorkspaceModal
      :show="showEditModal"
      title="Edit Workspace"
      :name="editTarget?.name"
      :description="editTarget?.description"
      @close="showEditModal = false"
      @submit="handleEdit"
    />

    <ConfirmModal
      :show="showDeleteModal"
      title="Delete Workspace"
      :message="`Are you sure you want to delete &quot;${deleteTarget?.name}&quot;? This action cannot be undone.`"
      confirm-label="Delete"
      variant="danger"
      @confirm="handleDelete"
      @cancel="showDeleteModal = false"
    />
  </div>
</template>

<style scoped>
.workspaces-page {
  max-width: 960px;
  margin: 0 auto;
  padding: var(--space-6) var(--space-4);
}

.page-header {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  margin-bottom: var(--space-6);
}

.page-title {
  font-size: 1.5rem;
  font-weight: 700;
  margin-bottom: var(--space-1);
}

.page-subtitle {
  color: var(--text-secondary);
  font-size: 0.85rem;
}

.btn-create {
  display: inline-flex;
  align-items: center;
  gap: var(--space-2);
  padding: var(--space-2) var(--space-4);
  background: var(--accent-blue);
  color: var(--bg-primary);
  font-size: 0.8rem;
  font-weight: 500;
  border-radius: var(--radius-md);
  transition: background var(--transition-fast);
}

.btn-create:hover {
  background: color-mix(in srgb, var(--accent-blue) 85%, white);
}

.workspace-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(300px, 1fr));
  gap: var(--space-4);
}

.empty-state {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  padding: var(--space-8) 0;
}

.empty-icon {
  color: var(--text-muted);
  margin-bottom: var(--space-4);
  opacity: 0.5;
}

.empty-text {
  color: var(--text-secondary);
  font-size: 0.95rem;
  margin-bottom: var(--space-1);
}

.empty-hint {
  color: var(--text-muted);
  font-size: 0.8rem;
}

.error-text {
  color: var(--accent-rose);
}
</style>
