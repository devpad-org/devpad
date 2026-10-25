<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { useWorkspaceStore } from '@/stores/workspaces'
import WorkspaceCard from '@/components/WorkspaceCard.vue'
import ConfirmModal from '@/components/ConfirmModal.vue'
import ConvertWorkspaceModal from '@/components/home/ConvertWorkspaceModal.vue'
import type { Workspace } from '@/api/workspaces'
import type { ConvertResult } from '@/api/projects'

// Workspaces left from before projects. Devpad no longer makes these; each
// can still be opened, started and stopped until it is converted into a
// project or deleted. The list stays fresh while the launcher is showing, and
// the section hides once none are left.

const POLL_INTERVAL_MS = 5000

const store = useWorkspaceStore()
const router = useRouter()

const showDeleteModal = ref(false)
const deleting = ref(false)
const deleteTarget = ref<Workspace | null>(null)
const convertTargetId = ref<number | null>(null)

const workspaces = computed(() => store.workspaces.filter((ws) => !ws.projectId))
// Follows the store, so the dialog sees the workspace start.
const convertTarget = computed(() => store.workspaces.find((ws) => ws.id === convertTargetId.value) ?? null)

let timer: ReturnType<typeof setInterval> | null = null

function refresh() {
  store.fetchWorkspaces().catch(() => {})
}

onMounted(() => {
  refresh()
  timer = setInterval(() => {
    if (document.visibilityState === 'visible') refresh()
  }, POLL_INTERVAL_MS)
})

onUnmounted(() => {
  if (timer !== null) clearInterval(timer)
})

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

// A converted workspace opens as its project; a project seeded from a repo
// in a subdirectory opens with a fresh workspace.
function handleConverted(result: ConvertResult) {
  convertTargetId.value = null
  refresh()
  router.push({ name: 'project', params: { id: result.project.id } })
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
  <section v-if="workspaces.length > 0" class="section">
    <div class="section-header">
      <div>
        <h2 class="section-title">Workspaces to convert</h2>
        <p class="section-hint">
          Left from before projects. Convert one to keep working in it with a board, agents and merge requests, or delete
          it once you've moved what you need.
        </p>
      </div>
    </div>

    <div class="workspace-grid">
      <WorkspaceCard
        v-for="ws in workspaces"
        :key="ws.id"
        :workspace="ws"
        @open="openWorkspace"
        @delete="openDelete"
        @start="handleStart"
        @stop="handleStop"
        @convert="convertTargetId = $event.id"
      />
    </div>

    <ConvertWorkspaceModal
      :show="convertTargetId !== null"
      :workspace="convertTarget"
      @close="convertTargetId = null"
      @start="handleStart"
      @converted="handleConverted"
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
