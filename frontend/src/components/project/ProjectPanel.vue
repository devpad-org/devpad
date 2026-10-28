<script setup lang="ts">
import { ref } from 'vue'
import { useRouter } from 'vue-router'
import type { ProjectInput } from '@/api/projects'
import { useActiveProjectStore } from '@/stores/activeProject'
import { useProjectStore } from '@/stores/projects'
import ConfirmModal from '@/components/ConfirmModal.vue'
import ConversionNotice from './ConversionNotice.vue'
import ProjectBranchesModal from './ProjectBranchesModal.vue'
import ProjectModal from './ProjectModal.vue'
import ProjectEnvironment from './ProjectEnvironment.vue'
import ProjectSetup from './ProjectSetup.vue'
import ProjectUpstream from './ProjectUpstream.vue'
import StatusBadge from './StatusBadge.vue'

// The project's details and settings in the IDE side bar.
defineProps<{
  // The project workspace's status, or 'none' before it is created.
  workspaceStatus: string
}>()

const router = useRouter()
const store = useActiveProjectStore()
const projects = useProjectStore()

const showEdit = ref(false)
const showBranches = ref(false)
const savingProject = ref(false)
const editError = ref('')
const showDelete = ref(false)
const deleting = ref(false)
const actionError = ref('')

async function saveProject(input: ProjectInput) {
  if (store.projectId === null) return
  savingProject.value = true
  editError.value = ''
  try {
    store.setProject(await projects.updateProject(store.projectId, input))
    showEdit.value = false
  } catch (e) {
    editError.value = e instanceof Error ? e.message : 'Failed to save project'
  } finally {
    savingProject.value = false
  }
}

async function deleteProject() {
  if (store.projectId === null) return
  deleting.value = true
  try {
    await projects.deleteProject(store.projectId)
    router.push({ name: 'home' })
  } catch (e) {
    actionError.value = e instanceof Error ? e.message : 'Failed to delete project'
    showDelete.value = false
  } finally {
    deleting.value = false
  }
}

async function dismissConversion() {
  actionError.value = ''
  try {
    await store.dismissConversion()
  } catch (e) {
    actionError.value = e instanceof Error ? e.message : 'Failed to dismiss'
  }
}
</script>

<template>
  <div class="side-panel project-panel">
    <div class="side-header">
      <span class="side-title">
        <svg class="panel-icon" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
          <path d="M11 21.73a2 2 0 0 0 2 0l7-4A2 2 0 0 0 21 16V8a2 2 0 0 0-1-1.73l-7-4a2 2 0 0 0-2 0l-7 4A2 2 0 0 0 3 8v8a2 2 0 0 0 1 1.73z" />
          <path d="M12 22V12" />
          <path d="m3.3 7 7.703 4.734a2 2 0 0 0 1.994 0L20.7 7" />
          <path d="m7.5 4.27 9 5.15" />
        </svg>
        Project
      </span>
    </div>

    <div v-if="store.project" class="side-body">
      <div class="side-section">
        <div class="project-name-row">
          <span class="project-name">{{ store.project.name }}</span>
          <StatusBadge :status="store.project.status" />
        </div>
        <p v-if="store.project.description" class="project-description">{{ store.project.description }}</p>
      </div>

      <p v-if="store.project.status === 'creating'" class="notice notice-warning side-notice">Setting up the project repository...</p>
      <p v-if="store.project.status === 'error'" class="notice notice-error side-notice">{{ store.project.error || 'The project failed to set up.' }}</p>
      <p v-if="store.loadError" class="notice notice-error side-notice">{{ store.loadError }}</p>
      <p v-if="actionError" class="notice notice-error side-notice">{{ actionError }}</p>

      <ConversionNotice v-if="store.conversion" :conversion="store.conversion" @dismiss="dismissConversion" />

      <div class="side-section">
        <div class="side-section-title">Repository</div>
        <dl class="facts">
          <dt>Default branch</dt>
          <dd class="mono">{{ store.project.defaultBranch }}</dd>
          <template v-if="store.protectedBranches.length > 1">
            <dt>Protected</dt>
            <dd class="mono">{{ store.protectedBranches.slice(1).join(', ') }}</dd>
          </template>
          <dt>Upstream</dt>
          <dd class="mono">{{ store.project.upstreamUrl || 'local only' }}</dd>
          <dt>Sandboxes</dt>
          <dd>{{ store.activeSandboxes }} / {{ store.project.maxSandboxes }} running</dd>
          <dt>Workspace</dt>
          <dd>{{ workspaceStatus === 'none' ? 'not created' : workspaceStatus }}</dd>
        </dl>
      </div>

      <ProjectUpstream />
      <ProjectEnvironment :workspace-status="workspaceStatus" />
      <ProjectSetup :workspace-status="workspaceStatus" />

      <div class="side-section actions">
        <button v-if="store.ready" class="btn btn-secondary btn-sm" @click="showBranches = true">Branches</button>
        <button class="btn btn-secondary btn-sm" @click="editError = ''; showEdit = true">Edit project</button>
        <button class="btn btn-danger btn-sm" @click="showDelete = true">Delete project</button>
      </div>
    </div>

    <ProjectModal
      :show="showEdit"
      :project="store.project"
      :saving="savingProject"
      :error="editError"
      @close="showEdit = false"
      @submit="saveProject"
    />

    <ProjectBranchesModal :show="showBranches" @close="showBranches = false" />

    <ConfirmModal
      :show="showDelete"
      title="Delete Project"
      :message="`Delete &quot;${store.project?.name}&quot;, its workspace, its sandboxes and its repository? Branches that were not pushed upstream are lost.`"
      confirm-label="Delete"
      variant="danger"
      :loading="deleting"
      @confirm="deleteProject"
      @cancel="showDelete = false"
    />
  </div>
</template>

<style scoped src="./shared.css"></style>
<style scoped src="./panel.css"></style>
<style scoped>
.project-panel {
  --ide-header-icon: var(--gruvbox-yellow);
}

.project-name-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-2);
}

.project-name {
  font-size: 0.9rem;
  font-weight: 600;
  overflow-wrap: anywhere;
}

.project-description {
  margin-top: var(--space-1);
  font-size: 0.78rem;
  color: var(--text-secondary);
  overflow-wrap: anywhere;
}

.facts {
  display: grid;
  grid-template-columns: auto minmax(0, 1fr);
  gap: var(--space-1) var(--space-3);
  font-size: 0.75rem;
}

.facts dt {
  color: var(--text-muted);
}

.facts dd {
  color: var(--text-primary);
  overflow-wrap: anywhere;
  text-transform: none;
}

.actions {
  display: flex;
  flex-direction: column;
  align-items: stretch;
  gap: var(--space-2);
}
</style>
