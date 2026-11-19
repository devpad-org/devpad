<script setup lang="ts">
import { computed, ref } from 'vue'
import { useRouter } from 'vue-router'
import type { ProjectInput } from '@/api/projects'
import { useActiveProjectStore } from '@/stores/activeProject'
import { useProjectStore } from '@/stores/projects'
import ConfirmModal from '@/components/ConfirmModal.vue'
import DocLink from '@/components/DocLink.vue'
import ConversionNotice from './ConversionNotice.vue'
import ProjectBranchesCard from './ProjectBranchesCard.vue'
import ProjectEmblem from './ProjectEmblem.vue'
import ProjectEnvironment from './ProjectEnvironment.vue'
import ProjectModal from './ProjectModal.vue'
import ProjectServices from './ProjectServices.vue'
import ProjectSetup from './ProjectSetup.vue'
import StatusBadge from './StatusBadge.vue'
import { TASK_STATUS } from './taskStatus'

// The project's details and settings, on a surface of their own: what it is,
// how its work stands, its protected branches, and how each new workspace is
// made (environment, then services, then the setup script).
const props = defineProps<{
  // The project workspace's status, or 'none' before it is created.
  workspaceStatus: string
  // The project workspace, when there is one.
  workspaceId?: number | null
}>()

const emit = defineEmits<{
  // Opens another of the project's surfaces.
  navigate: [tab: 'tasks' | 'merge-requests' | 'agents']
}>()

const router = useRouter()
const store = useActiveProjectStore()
const projects = useProjectStore()

const showEdit = ref(false)
const savingProject = ref(false)
const editError = ref('')
const showDelete = ref(false)
const deleting = ref(false)
const actionError = ref('')

const openTasks = computed(() => store.tasks.filter((t) => TASK_STATUS[t.status].step < 3))
const working = computed(() => store.tasks.filter((t) => t.status === 'in_progress' || t.status === 'queued').length)
const needsYou = computed(() => store.tasks.filter((t) => t.status === 'in_review' || t.status === 'failed').length)
const reviewing = computed(() => store.openMergeRequests.filter((mr) => mr.agentReviewing).length)

const sandboxShare = computed(() => {
  const max = store.project?.maxSandboxes ?? 0
  return max > 0 ? Math.min(100, (store.activeSandboxes / max) * 100) : 0
})

const workspaceLabel = computed(() => {
  const status = props.workspaceStatus === 'none' ? 'not created' : props.workspaceStatus
  return status.charAt(0).toUpperCase() + status.slice(1)
})
const workspaceTone = computed(() => {
  switch (props.workspaceStatus) {
    case 'running': return 'ok'
    case 'error': return 'error'
    case 'none':
    case 'stopped': return 'neutral'
    default: return 'busy'
  }
})

function openEdit() {
  editError.value = ''
  showEdit.value = true
}

async function saveProject(input: ProjectInput) {
  if (store.projectId === null) return
  savingProject.value = true
  editError.value = ''
  try {
    const { project, upstreamError } = await projects.updateProject(store.projectId, input)
    store.setProject(project)
    if (upstreamError) {
      editError.value = `Saved, but the upstream was not changed: ${upstreamError}`
      return
    }
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
  <section class="overview" aria-label="Project">
    <header class="overview-header">
      <div class="overview-header-title">
        <svg class="overview-header-icon" width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
          <path d="M11 21.73a2 2 0 0 0 2 0l7-4A2 2 0 0 0 21 16V8a2 2 0 0 0-1-1.73l-7-4a2 2 0 0 0-2 0l-7 4A2 2 0 0 0 3 8v8a2 2 0 0 0 1 1.73z" />
          <path d="M12 22V12" />
          <path d="m3.3 7 7.703 4.734a2 2 0 0 0 1.994 0L20.7 7" />
          <path d="m7.5 4.27 9 5.15" />
        </svg>
        <span class="overview-header-text">Project</span>
        <DocLink topic="projects" label="Projects" />
      </div>
      <button v-if="store.project" class="btn btn-secondary btn-sm" type="button" @click="openEdit">Edit project</button>
    </header>

    <div v-if="store.project" class="overview-scroll">
      <div class="overview-page">
        <div class="hero">
          <ProjectEmblem class="hero-emblem" :name="store.project.name" :size="52" />
          <div class="hero-text">
            <div class="hero-name-row">
              <h1 class="hero-name">{{ store.project.name }}</h1>
              <StatusBadge v-if="store.project.status !== 'ready'" :status="store.project.status" />
            </div>
            <p v-if="store.project.description" class="hero-description">{{ store.project.description }}</p>
            <button v-else class="hero-add-description" type="button" @click="openEdit">Add a description</button>
          </div>
        </div>

        <p v-if="store.project.status === 'creating'" class="notice notice-warning page-notice">Setting up the project repository...</p>
        <p v-if="store.project.status === 'error'" class="notice notice-error page-notice">{{ store.project.error || 'The project failed to set up.' }}</p>
        <p v-if="store.loadError" class="notice notice-error page-notice">{{ store.loadError }}</p>
        <p v-if="actionError" class="notice notice-error page-notice">{{ actionError }}</p>

        <div v-if="store.conversion" class="conversion-box">
          <ConversionNotice :conversion="store.conversion" @dismiss="dismissConversion" />
        </div>

        <div class="stats">
          <button class="stat stat-link" type="button" :disabled="!store.ready" @click="emit('navigate', 'tasks')">
            <span class="stat-label">Open tasks</span>
            <span class="stat-value">{{ openTasks.length }}</span>
            <span class="stat-sub">
              <template v-if="working || needsYou">
                <span v-if="working" class="sub-busy">{{ working }} with agents</span>
                <span v-if="needsYou" class="sub-warn">{{ needsYou }} {{ needsYou === 1 ? 'needs' : 'need' }} you</span>
              </template>
              <span v-else>{{ store.tasks.length ? 'none at work' : 'no tasks yet' }}</span>
            </span>
          </button>
          <button class="stat stat-link" type="button" :disabled="!store.ready" @click="emit('navigate', 'merge-requests')">
            <span class="stat-label">Open merge requests</span>
            <span class="stat-value">{{ store.openMergeRequests.length }}</span>
            <span class="stat-sub">
              <span v-if="reviewing" class="sub-busy">{{ reviewing }} being reviewed</span>
              <span v-else>into {{ store.protectedBranches.join(', ') }}</span>
            </span>
          </button>
          <button class="stat stat-link" type="button" :disabled="!store.ready" @click="emit('navigate', 'agents')">
            <span class="stat-label">Sandboxes</span>
            <span class="stat-value">{{ store.activeSandboxes }}<span class="stat-of"> / {{ store.project.maxSandboxes }}</span></span>
            <span class="stat-meter" aria-hidden="true"><span class="stat-meter-fill" :style="{ width: `${sandboxShare}%` }" /></span>
          </button>
          <div class="stat">
            <span class="stat-label">Your workspace</span>
            <span class="stat-value stat-value-text">
              <span class="stat-dot" :class="`dot-${workspaceTone}`" aria-hidden="true" />
              {{ workspaceLabel }}
            </span>
            <span class="stat-sub">{{ workspaceStatus === 'running' ? 'your IDE for the project' : 'start it from the AI panel' }}</span>
          </div>
        </div>

        <ProjectBranchesCard />

        <div class="section-head">
          <h2 class="section-heading">How new workspaces are made</h2>
          <p class="section-sub">Your workspace and every task and review sandbox go through these steps, in order.</p>
        </div>
        <div class="steps">
          <ProjectEnvironment :step="1" :workspace-status="workspaceStatus" @edit="openEdit" />
          <ProjectServices :step="2" :workspace-status="workspaceStatus" :workspace-id="workspaceId ?? null" />
          <ProjectSetup :step="3" :workspace-status="workspaceStatus" @edit="openEdit" />
        </div>

        <div class="danger">
          <div class="danger-text">
            <h2 class="danger-title">Delete project</h2>
            <p class="danger-sub">Deletes its workspace, its sandboxes and its repository. Branches that were not pushed upstream are lost.</p>
          </div>
          <button class="btn btn-danger btn-sm" type="button" @click="showDelete = true">Delete project</button>
        </div>
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
  </section>
</template>

<style scoped src="./shared.css"></style>
<style scoped>
.overview {
  display: flex;
  flex-direction: column;
  height: 100%;
  overflow: hidden;
  background: var(--ide-surface-bg);
  container-type: inline-size;
}

/* Header, matching the tasks and merge requests surfaces' */
.overview-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-3);
  height: 38px;
  padding: 0 var(--space-3);
  border-bottom: 0.5px solid var(--border-subtle);
  background: var(--ide-header-bg);
  flex-shrink: 0;
}

.overview-header-title {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  min-width: 0;
  font-size: var(--ide-header-title-size);
  color: var(--text-primary);
}

.overview-header-icon {
  color: var(--hue-yellow);
  flex-shrink: 0;
}

.overview-header-text {
  font-weight: 600;
}

.overview-scroll {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
}

.overview-page {
  display: flex;
  flex-direction: column;
  gap: var(--space-4);
  max-width: 1080px;
  margin: 0 auto;
  padding: var(--space-6) var(--space-6) var(--space-8);
}

/* Who the project is */
.hero {
  display: flex;
  align-items: center;
  gap: var(--space-4);
}

.hero-emblem {
  flex-shrink: 0;
  border-radius: var(--radius-lg);
  box-shadow: 0 0 0 0.5px var(--border-subtle);
}

.hero-text {
  display: flex;
  flex-direction: column;
  gap: var(--space-1);
  min-width: 0;
}

.hero-name-row {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  min-width: 0;
}

.hero-name {
  font-size: 1.35rem;
  font-weight: 600;
  line-height: 1.2;
  color: var(--text-primary);
  overflow-wrap: anywhere;
}

.hero-description {
  max-width: 72ch;
  font-size: 0.85rem;
  line-height: 1.5;
  color: var(--text-secondary);
  overflow-wrap: anywhere;
  white-space: pre-line;
}

.hero-add-description {
  align-self: flex-start;
  font-size: 0.8rem;
  color: var(--text-tertiary);
  text-decoration: underline dotted var(--text-decoration-muted);
  text-underline-offset: 3px;
}

.hero-add-description:hover {
  color: var(--text-primary);
}

.page-notice {
  margin: 0;
}

.conversion-box {
  background: var(--bg-base);
  border: 0.5px solid var(--warning-border);
  border-radius: var(--radius-lg);
  padding: var(--space-2);
}

/* How the project's work stands, at a glance */
.stats {
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  gap: var(--space-3);
}

.stat {
  display: flex;
  flex-direction: column;
  gap: 4px;
  min-width: 0;
  padding: var(--space-3) var(--space-4);
  background: var(--bg-base);
  border: 0.5px solid var(--border-subtle);
  border-radius: var(--radius-lg);
  text-align: left;
  color: var(--text-primary);
}

.stat-link {
  transition: border-color var(--transition-fast), background var(--transition-fast);
}

.stat-link:hover:not(:disabled) {
  border-color: var(--border-strong);
  background: var(--bg-elevated);
}

.stat-link:disabled {
  cursor: default;
}

.stat-label {
  font-size: 0.7rem;
  font-weight: 600;
  letter-spacing: 0.05em;
  text-transform: uppercase;
  color: var(--text-tertiary);
}

.stat-value {
  font-size: 1.5rem;
  font-weight: 600;
  line-height: 1.2;
  font-variant-numeric: tabular-nums;
}

.stat-value-text {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  font-size: 1.05rem;
  line-height: 1.6;
}

.stat-of {
  font-size: 0.95rem;
  font-weight: 500;
  color: var(--text-tertiary);
}

.stat-sub {
  display: flex;
  flex-wrap: wrap;
  gap: 0 var(--space-2);
  min-width: 0;
  font-size: 0.75rem;
  color: var(--text-tertiary);
  overflow-wrap: anywhere;
}

.stat-sub .mono {
  font-size: 0.72rem;
}

.sub-busy {
  color: var(--hue-blue);
}

.sub-warn {
  color: var(--attention);
}

.stat-meter {
  display: block;
  height: 4px;
  margin-top: 6px;
  border-radius: 2px;
  background: var(--bg-neutral);
  overflow: hidden;
}

.stat-meter-fill {
  display: block;
  height: 100%;
  border-radius: 2px;
  background: var(--accent);
  transition: width var(--transition-normal);
}

.stat-dot {
  width: 8px;
  height: 8px;
  flex-shrink: 0;
  border-radius: 50%;
  background: var(--text-tertiary);
}

.dot-ok { background: var(--success); }
.dot-error { background: var(--error); }
.dot-busy { background: var(--warning); }

/* How new workspaces are made */
.section-head {
  display: flex;
  flex-direction: column;
  gap: 2px;
  margin-top: var(--space-4);
}

.section-heading {
  font-size: 0.95rem;
  font-weight: 600;
  color: var(--text-primary);
}

.section-sub {
  font-size: 0.8rem;
  color: var(--text-tertiary);
}

.steps {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: var(--space-3);
  align-items: stretch;
}

/* Delete */
.danger {
  display: flex;
  align-items: center;
  justify-content: space-between;
  flex-wrap: wrap;
  gap: var(--space-3);
  margin-top: var(--space-6);
  padding: var(--space-3) var(--space-4);
  border: 0.5px solid var(--error-border);
  border-radius: var(--radius-lg);
}

.danger-title {
  font-size: 0.85rem;
  font-weight: 600;
  color: var(--text-primary);
}

.danger-sub {
  font-size: 0.78rem;
  color: var(--text-tertiary);
}

@container (max-width: 980px) {
  .steps {
    grid-template-columns: minmax(0, 1fr);
  }
}

@container (max-width: 720px) {
  .stats {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }

  .overview-page {
    padding: var(--space-4) var(--space-4) var(--space-6);
  }
}

@container (max-width: 380px) {
  .hero-emblem {
    width: 40px;
    height: 40px;
  }

  .stat {
    padding: var(--space-2) var(--space-3);
  }

  .stat-value {
    font-size: 1.25rem;
  }
}

/* Matches MOBILE_MEDIA_QUERY in useMediaQuery.ts. */
@media (max-width: 768px), (pointer: coarse) and (max-height: 500px) {
  .overview-header {
    height: 44px;
    padding: 0 var(--space-4);
  }
}
</style>
