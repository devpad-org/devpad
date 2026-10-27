<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { useAttentionStore } from '@/stores/attention'
import { useProjectStore } from '@/stores/projects'
import type { Project, ProjectInput } from '@/api/projects'
import { projectsOpenedAt } from '@/utils/recentProjects'
import { fullTime, timeAgo } from '@/utils/time'
import ProjectModal from '@/components/project/ProjectModal.vue'
import StatusBadge from '@/components/project/StatusBadge.vue'
import WorkspacesToConvert from '@/components/home/WorkspacesToConvert.vue'

// The launcher: every project with what needs the user in it, and workspaces
// that belong to no project. The inbox opens from the header.

const POLL_INTERVAL_MS = 5000

const router = useRouter()
const attention = useAttentionStore()
const projects = useProjectStore()

const showCreate = ref(false)
const saving = ref(false)
const createError = ref('')
const openedAt = projectsOpenedAt()
let timer: ReturnType<typeof setInterval> | null = null

// Agents waiting on the user, per project.
const waitingAgents = computed(() => {
  const counts = new Map<number, number>()
  for (const item of attention.items) {
    const link = projects.workspaceLinks.get(item.run.workspaceId)
    if (link) counts.set(link.projectId, (counts.get(link.projectId) ?? 0) + 1)
  }
  return counts
})

interface Card {
  project: Project
  needsYou: number
  waiting: number
  awaitingReview: number
  runningAgents: number
  activeTasks: number
  openMergeRequests: number
  lastActivityAt: string
  sortKey: string
}

const cards = computed<Card[]>(() =>
  projects.projects
    .map((project) => {
      const overview = projects.overviews.get(project.id)
      const waiting = waitingAgents.value.get(project.id) ?? 0
      const awaitingReview = overview?.awaitingReview ?? 0
      const lastActivityAt = overview?.lastActivityAt ?? project.updatedAt
      const opened = openedAt.get(project.id) ?? ''
      return {
        project,
        needsYou: waiting + awaitingReview,
        waiting,
        awaitingReview,
        runningAgents: overview?.runningAgents ?? 0,
        activeTasks: overview?.activeTasks ?? 0,
        openMergeRequests: overview?.openMergeRequests ?? 0,
        lastActivityAt,
        sortKey: latest(lastActivityAt, opened),
      }
    })
    .sort((a, b) => b.sortKey.localeCompare(a.sortKey)),
)

function latest(a: string, b: string): string {
  return new Date(Math.max(timeOf(a), timeOf(b))).toISOString()
}

function timeOf(value: string): number {
  const t = value ? Date.parse(value) : 0
  return Number.isFinite(t) ? t : 0
}

function needsYouTitle(card: Card): string {
  const parts: string[] = []
  if (card.waiting) parts.push(`${card.waiting} agent${card.waiting === 1 ? '' : 's'} waiting for you`)
  if (card.awaitingReview) parts.push(`${card.awaitingReview} merge request${card.awaitingReview === 1 ? '' : 's'} to review`)
  return parts.join(', ')
}

async function refresh() {
  await Promise.all([
    projects.fetchProjects().catch(() => {}),
    projects.fetchOverview().catch(() => {}),
  ])
}

onMounted(() => {
  void refresh()
  timer = setInterval(() => {
    if (document.visibilityState === 'visible') void refresh()
  }, POLL_INTERVAL_MS)
})

onUnmounted(() => {
  if (timer !== null) clearInterval(timer)
})

async function handleCreate(input: ProjectInput) {
  saving.value = true
  createError.value = ''
  try {
    const project = await projects.createProject(input)
    showCreate.value = false
    router.push({ name: 'project', params: { id: project.id } })
  } catch (e) {
    createError.value = e instanceof Error ? e.message : 'Failed to create project'
  } finally {
    saving.value = false
  }
}
</script>

<template>
  <div class="page launcher">
    <div class="page-header">
      <div>
        <h1 class="page-title">Projects</h1>
        <p class="page-subtitle">Open a project to work in it, alongside the agents working on its tasks</p>
      </div>
      <button class="btn btn-primary" @click="createError = ''; showCreate = true">
        <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
          <path d="M12 5v14" />
          <path d="M5 12h14" />
        </svg>
        New project
      </button>
    </div>

    <section class="section">
      <p v-if="!projects.loaded && !projects.error" class="empty-text">Loading projects...</p>
      <p v-else-if="projects.error && projects.projects.length === 0" class="notice notice-error">{{ projects.error }}</p>
      <div v-else-if="projects.projects.length === 0" class="panel empty-state">
        <p class="empty-title">No projects yet</p>
        <p class="empty-text">Create a project from a repository, or a scratch one from nothing, then work in it and give agents tasks to do in parallel.</p>
      </div>

      <div v-else class="project-grid">
        <RouterLink
          v-for="card in cards"
          :key="card.project.id"
          :to="{ name: 'project', params: { id: card.project.id } }"
          class="panel project-card"
          :class="{ 'needs-you': card.needsYou > 0 }"
        >
          <div class="card-header">
            <h2 class="card-title">{{ card.project.name }}</h2>
            <span v-if="card.needsYou > 0" class="needs-badge" :title="needsYouTitle(card)">
              {{ card.needsYou }} need{{ card.needsYou === 1 ? 's' : '' }} you
            </span>
            <StatusBadge v-else-if="card.project.status !== 'ready'" :status="card.project.status" />
          </div>
          <p v-if="card.project.description" class="card-description">{{ card.project.description }}</p>
          <p v-if="card.project.status === 'error' && card.project.error" class="card-error">{{ card.project.error }}</p>

          <div class="card-stats">
            <span v-if="card.waiting" class="stat stat-attention">
              {{ card.waiting }} waiting
            </span>
            <span v-if="card.awaitingReview" class="stat stat-attention">
              {{ card.awaitingReview }} to review
            </span>
            <span v-if="card.runningAgents" class="stat stat-active">
              <span class="pulse" />
              {{ card.runningAgents }} agent{{ card.runningAgents === 1 ? '' : 's' }} running
            </span>
            <span v-if="card.activeTasks" class="stat">
              {{ card.activeTasks }} task{{ card.activeTasks === 1 ? '' : 's' }} in progress
            </span>
            <span v-if="card.openMergeRequests" class="stat">
              {{ card.openMergeRequests }} open MR{{ card.openMergeRequests === 1 ? '' : 's' }}
            </span>
          </div>

          <div class="meta-row">
            <span class="mono">{{ card.project.defaultBranch }}</span>
            <span>&middot;</span>
            <span class="mono card-upstream">{{ card.project.upstreamUrl || 'local only' }}</span>
            <span>&middot;</span>
            <span>{{ card.project.maxSandboxes }} sandbox{{ card.project.maxSandboxes === 1 ? '' : 'es' }} max</span>
            <span>&middot;</span>
            <span :title="fullTime(card.lastActivityAt)">{{ timeAgo(card.lastActivityAt) }}</span>
          </div>
        </RouterLink>
      </div>
    </section>

    <WorkspacesToConvert />

    <ProjectModal
      :show="showCreate"
      :saving="saving"
      :error="createError"
      @close="showCreate = false"
      @submit="handleCreate"
    />
  </div>
</template>

<style scoped src="@/components/project/shared.css"></style>
<style scoped>
.launcher {
  max-width: 1200px;
}

.project-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(min(300px, 100%), 1fr));
  gap: var(--space-4);
}

.project-card {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
  min-width: 0;
  color: var(--text-primary);
  transition: border-color var(--transition-fast);
}

.project-card:hover {
  color: var(--text-primary);
  border-color: var(--border-active);
}

.project-card.needs-you {
  border-color: var(--attention);
}

.card-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-2);
}

.card-title {
  font-size: 0.95rem;
  font-weight: 500;
  overflow-wrap: anywhere;
}

.needs-badge {
  flex-shrink: 0;
  padding: 1px var(--space-2);
  border-radius: var(--radius-sm);
  background: var(--attention);
  color: var(--text-on-accent);
  font-size: 0.7rem;
  font-weight: 600;
  white-space: nowrap;
}

.card-description {
  font-size: 0.8rem;
  color: var(--text-secondary);
  display: -webkit-box;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
  overflow: hidden;
}

.card-error {
  font-size: 0.75rem;
  color: var(--error);
  overflow-wrap: anywhere;
}

.card-stats {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-1) var(--space-3);
  font-size: 0.75rem;
  color: var(--text-secondary);
}

.card-stats:empty {
  display: none;
}

.stat {
  display: inline-flex;
  align-items: center;
  gap: var(--space-1);
}

.stat-attention {
  color: var(--attention);
}

.stat-active {
  color: var(--accent-blue);
}

.pulse {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: currentColor;
  animation: pulse 1.6s ease-in-out infinite;
}

@keyframes pulse {
  50% {
    opacity: 0.3;
  }
}

@media (prefers-reduced-motion: reduce) {
  .pulse {
    animation: none;
  }
}

.card-upstream {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  min-width: 0;
  max-width: 100%;
}

.empty-state {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: var(--space-1);
  padding: var(--space-8) var(--space-4);
  text-align: center;
}

.empty-title {
  color: var(--text-secondary);
  font-size: 0.95rem;
}
</style>
