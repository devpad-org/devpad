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
// the section hides once none are left. Its dialogs stay mounted regardless,
// because a workspace converted in place leaves the list mid-conversion.

const POLL_INTERVAL_MS = 5000

const store = useWorkspaceStore()
const router = useRouter()

const showDeleteModal = ref(false)
const deleting = ref(false)
const deleteTarget = ref<Workspace | null>(null)
const convertTargetId = ref<number | null>(null)
const convertSnapshot = ref<Workspace | null>(null)
const actionError = ref('')

const workspaces = computed(() => store.workspaces.filter((ws) => !ws.projectId))
// Follows the store, so the dialog sees the workspace start, and falls back
// to the workspace as it was picked if the store drops it mid-conversion.
const convertTarget = computed(
  () => store.workspaces.find((ws) => ws.id === convertTargetId.value) ?? convertSnapshot.value,
)
const visible = computed(() => workspaces.value.length > 0 || store.error !== null)

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

function openConvert(workspace: Workspace) {
  convertSnapshot.value = workspace
  convertTargetId.value = workspace.id
}

function closeConvert() {
  convertTargetId.value = null
  convertSnapshot.value = null
}

function errorMessage(e: unknown, fallback: string): string {
  return e instanceof Error && e.message ? e.message : fallback
}

function openWorkspace(workspace: Workspace) {
  router.push({ name: 'ide', params: { id: workspace.id } })
}

async function handleStart(workspace: Workspace) {
  actionError.value = ''
  try {
    await store.startWorkspace(workspace.id)
  } catch (e) {
    actionError.value = errorMessage(e, `Couldn't start "${workspace.name}"`)
  }
}

async function handleStop(workspace: Workspace) {
  actionError.value = ''
  try {
    await store.stopWorkspace(workspace.id)
  } catch (e) {
    actionError.value = errorMessage(e, `Couldn't stop "${workspace.name}"`)
  }
}

// A converted workspace opens as its project; a project seeded from a repo
// in a subdirectory opens with a fresh workspace.
function handleConverted(result: ConvertResult) {
  closeConvert()
  refresh()
  router.push({ name: 'project', params: { id: result.project.id } })
}

async function handleDelete() {
  const target = deleteTarget.value
  if (!target) return
  actionError.value = ''
  deleting.value = true
  try {
    await store.deleteWorkspace(target.id)
    showDeleteModal.value = false
    deleteTarget.value = null
  } catch (e) {
    showDeleteModal.value = false
    actionError.value = errorMessage(e, `Couldn't delete "${target.name}"`)
  } finally {
    deleting.value = false
  }
}
</script>

<template>
  <section v-if="visible" class="section">
    <div class="section-header">
      <div>
        <h2 class="section-title">Workspaces to convert</h2>
        <p class="section-hint">
          Left from before projects. Convert one to keep working in it with a board, agents and merge requests, or delete
          it once you've moved what you need.
        </p>
      </div>
    </div>

    <p v-if="store.error" class="notice notice-error">Couldn't load your workspaces: {{ store.error }}</p>
    <p v-if="actionError" class="notice notice-error">{{ actionError }}</p>

    <div v-if="workspaces.length > 0" class="workspace-grid">
      <WorkspaceCard
        v-for="ws in workspaces"
        :key="ws.id"
        :workspace="ws"
        @open="openWorkspace"
        @delete="openDelete"
        @start="handleStart"
        @stop="handleStop"
        @convert="openConvert"
      />
    </div>
  </section>

  <ConvertWorkspaceModal
    :show="convertTargetId !== null"
    :workspace="convertTarget"
    @close="closeConvert"
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
