<script setup lang="ts">
import { computed, ref } from 'vue'
import { useRouter } from 'vue-router'
import { useWorkspaceStore } from '@/stores/workspaces'
import WorkspaceCard from '@/components/WorkspaceCard.vue'
import WorkspaceModal from '@/components/WorkspaceModal.vue'
import ConfirmModal from '@/components/ConfirmModal.vue'
import type { Workspace } from '@/api/workspaces'

// Workspaces that don't belong to a project. The launcher fetches the
// workspace list; this section only manages these workspaces.

const store = useWorkspaceStore()
const router = useRouter()

const showCreateModal = ref(false)
const showEditModal = ref(false)
const showDeleteModal = ref(false)
const deleting = ref(false)
const editTarget = ref<Workspace | null>(null)
const deleteTarget = ref<Workspace | null>(null)

const workspaces = computed(() => store.workspaces.filter((ws) => !ws.projectId))

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

async function handleStart(workspace: Workspace) {
  try {
    await store.startWorkspace(workspace.id)
  } catch {
    // error handled by store
  }
}

async function handleStop(workspace: Workspace) {
  try {
    await store.stopWorkspace(workspace.id)
  } catch {
    // error handled by store
  }
}

async function handleDelete() {
  if (!deleteTarget.value) return
  deleting.value = true
  try {
    await store.deleteWorkspace(deleteTarget.value.id)
    showDeleteModal.value = false
    deleteTarget.value = null
  } catch {
    // error handled by store
  } finally {
    deleting.value = false
  }
}
</script>

<template>
  <section class="section">
    <div class="section-header">
      <div>
        <h2 class="section-title">Workspaces without a project</h2>
        <p class="section-hint">Plain development environments, outside any project's board and merge requests.</p>
      </div>
      <button class="btn btn-secondary btn-sm" @click="showCreateModal = true">New workspace</button>
    </div>

    <p v-if="store.loading && workspaces.length === 0" class="empty-text">Loading workspaces...</p>
    <p v-else-if="store.error && workspaces.length === 0" class="notice notice-error">{{ store.error }}</p>
    <p v-else-if="workspaces.length === 0" class="empty-text">None.</p>

    <div v-else class="workspace-grid">
      <WorkspaceCard
        v-for="ws in workspaces"
        :key="ws.id"
        :workspace="ws"
        @open="openWorkspace"
        @edit="openEdit"
        @delete="openDelete"
        @start="handleStart"
        @stop="handleStop"
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
      :loading="deleting"
      @confirm="handleDelete"
      @cancel="showDeleteModal = false"
    />
  </section>
</template>

<style scoped src="@/components/project/shared.css"></style>
<style scoped>
.section-header {
  align-items: flex-start;
}

.section-hint {
  margin-top: 2px;
  font-size: 0.75rem;
  color: var(--text-muted);
}

.workspace-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(min(300px, 100%), 1fr));
  gap: var(--space-4);
}
</style>
