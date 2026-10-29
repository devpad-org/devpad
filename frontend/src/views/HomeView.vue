<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { useAttentionStore } from '@/stores/attention'
import { useProjectStore } from '@/stores/projects'
import type { Project, ProjectInput } from '@/api/projects'
import { projectsOpenedAt } from '@/utils/recentProjects'
import { fullTime, timeAgo } from '@/utils/time'
import { projectEmblem } from '@/utils/projectEmblem'
import MrIcon from '@/components/project/MrIcon.vue'
import ProjectEmblem from '@/components/project/ProjectEmblem.vue'
import ProjectModal from '@/components/project/ProjectModal.vue'
import StatusBadge from '@/components/project/StatusBadge.vue'
import WorkspacesToConvert from '@/components/home/WorkspacesToConvert.vue'

// The launcher: every project with what needs the user in it, and workspaces
// that belong to no project. The inbox opens from the header.

const POLL_INTERVAL_MS = 5000
// A card draws one slot per sandbox up to this many, and only counts past it.
const MAX_SLOTS = 8
// Example names for the empty state's emblems.
const EXAMPLE_NAMES = ['lab', 'spark', 'sketchbook']

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
  hue: string
  origin: string
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
        hue: projectEmblem(project.name).hue,
        origin: upstreamLabel(project.upstreamUrl),
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

// Across every project, for the header.
const totals = computed(() =>
  cards.value.reduce(
    (sum, card) => ({
      needsYou: sum.needsYou + card.needsYou,
      runningAgents: sum.runningAgents + card.runningAgents,
      activeTasks: sum.activeTasks + card.activeTasks,
      openMergeRequests: sum.openMergeRequests + card.openMergeRequests,
    }),
    { needsYou: 0, runningAgents: 0, activeTasks: 0, openMergeRequests: 0 },
  ),
)

const busy = computed(() => Object.values(totals.value).some((n) => n > 0))

// host/owner/repo from an https, ssh:// or scp-style git URL, without any
// credentials in it; the URL as given if it parses as none of those.
function upstreamLabel(url: string): string {
  if (!url) return ''
  let host = ''
  let path = ''
  const scp = /^(?:[^@/\s]+@)?([^:/\s]+):(?!\/\/)(.+)$/.exec(url)
  if (scp) {
    host = scp[1]
    path = scp[2]
  } else {
    try {
      const parsed = new URL(url)
      host = parsed.host
      path = parsed.pathname
    } catch {
      return url
    }
  }
  return `${host}/${path.replace(/^\/+/, '').replace(/\.git$/, '')}`
}

function plural(n: number, one: string, many = `${one}s`): string {
  return `${n} ${n === 1 ? one : many}`
}

function slotsTitle(card: Card): string {
  return `${card.runningAgents} of ${plural(card.project.maxSandboxes, 'sandbox', 'sandboxes')} running`
}

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

function openCreate() {
  createError.value = ''
  showCreate.value = true
}

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
        <p v-if="busy" class="page-subtitle summary">
          <span v-if="totals.needsYou" class="summary-item tone-attention">
            {{ totals.needsYou }} need{{ totals.needsYou === 1 ? 's' : '' }} you
          </span>
          <span v-if="totals.runningAgents" class="summary-item">{{ plural(totals.runningAgents, 'agent') }} running</span>
          <span v-if="totals.activeTasks" class="summary-item">{{ plural(totals.activeTasks, 'task') }} in progress</span>
          <span v-if="totals.openMergeRequests" class="summary-item">
            {{ plural(totals.openMergeRequests, 'open merge request') }}
          </span>
        </p>
        <p v-else class="page-subtitle">Open a project to work in it, alongside the agents working on its tasks</p>
      </div>
      <button class="btn btn-primary" @click="openCreate">
        <MrIcon name="plus" :size="16" />
        New project
      </button>
    </div>

    <section class="section">
      <p v-if="!projects.loaded && !projects.error" class="empty-text">Loading projects...</p>
      <p v-else-if="projects.error && projects.projects.length === 0" class="notice notice-error">{{ projects.error }}</p>
      <div v-else-if="projects.projects.length === 0" class="panel empty-state">
        <div class="empty-emblems" aria-hidden="true">
          <ProjectEmblem v-for="name in EXAMPLE_NAMES" :key="name" :name="name" :size="32" />
        </div>
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
          :style="{ '--project-hue': card.hue }"
        >
          <div class="card-top">
            <ProjectEmblem :name="card.project.name" :size="36" />
            <div class="card-heading">
              <h2 class="card-title">{{ card.project.name }}</h2>
              <span class="mono card-origin" :title="card.project.upstreamUrl || undefined">
                {{ card.origin || 'local only' }}
              </span>
            </div>
            <span v-if="card.needsYou > 0" class="needs-badge" :title="needsYouTitle(card)">
              {{ card.needsYou }} need{{ card.needsYou === 1 ? 's' : '' }} you
            </span>
            <StatusBadge v-else-if="card.project.status !== 'ready'" :status="card.project.status" />
          </div>

          <p v-if="card.project.description" class="card-description">{{ card.project.description }}</p>
          <p v-if="card.project.status === 'error' && card.project.error" class="card-error">{{ card.project.error }}</p>

          <div class="card-stats">
            <span v-if="card.waiting" class="chip tone-attention">
              <MrIcon name="comment" :size="12" />
              {{ card.waiting }} waiting
            </span>
            <span v-if="card.awaitingReview" class="chip tone-attention">
              <MrIcon name="eye" :size="12" />
              {{ card.awaitingReview }} to review
            </span>
            <span v-if="card.runningAgents" class="chip tone-hue">
              <span class="pulse" />
              {{ plural(card.runningAgents, 'agent') }} running
            </span>
            <span v-if="card.activeTasks" class="chip">
              <MrIcon name="task" :size="12" />
              {{ plural(card.activeTasks, 'task') }} in progress
            </span>
            <span v-if="card.openMergeRequests" class="chip">
              <MrIcon name="open" :size="12" />
              {{ plural(card.openMergeRequests, 'open MR', 'open MRs') }}
            </span>
          </div>

          <div class="card-footer">
            <span class="foot-item">
              <MrIcon name="branch" :size="12" />
              <span class="mono foot-branch">{{ card.project.defaultBranch }}</span>
            </span>
            <span class="foot-item" :title="fullTime(card.lastActivityAt)">
              <MrIcon name="clock" :size="12" />
              {{ timeAgo(card.lastActivityAt) }}
            </span>
            <span class="slots" :title="slotsTitle(card)" :aria-label="slotsTitle(card)" role="img">
              <span
                v-for="n in Math.min(card.project.maxSandboxes, MAX_SLOTS)"
                :key="n"
                class="slot"
                :class="{ on: n <= card.runningAgents }"
              />
              <span v-if="card.project.maxSandboxes > MAX_SLOTS" class="slots-more">
                {{ card.runningAgents }}/{{ card.project.maxSandboxes }}
              </span>
            </span>
          </div>
        </RouterLink>

        <button class="new-card" @click="openCreate">
          <MrIcon name="plus" :size="16" />
          New project
        </button>
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

.summary {
  display: flex;
  flex-wrap: wrap;
  gap: 0 var(--space-3);
}

.tone-attention {
  color: var(--attention);
}

.project-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(min(300px, 100%), 1fr));
  gap: var(--space-4);
}

/* --project-hue is the card's emblem colour, shown on its hover border, its
   running agents and its sandbox slots. */
.project-card {
  display: flex;
  flex-direction: column;
  gap: var(--space-3);
  min-width: 0;
  color: var(--text-primary);
  transition: border-color var(--transition-fast);
}

.project-card:hover {
  color: var(--text-primary);
  border-color: color-mix(in srgb, var(--project-hue) 50%, transparent);
}

.project-card.needs-you {
  border-color: var(--attention);
}

.card-top {
  display: flex;
  align-items: center;
  gap: var(--space-3);
  min-width: 0;
}

.card-heading {
  display: flex;
  flex-direction: column;
  flex: 1;
  min-width: 0;
}

.card-title {
  font-size: 0.95rem;
  font-weight: 500;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.card-origin {
  font-size: 0.7rem;
  color: var(--text-muted);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.needs-badge {
  align-self: flex-start;
  flex-shrink: 0;
  padding: 1px var(--space-2);
  border-radius: var(--radius-sm);
  background: var(--attention);
  color: var(--text-on-accent);
  font-size: 0.7rem;
  font-weight: 600;
  white-space: nowrap;
}

.card-top > :deep(.status-badge) {
  align-self: flex-start;
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
  gap: var(--space-1);
}

.card-stats:empty {
  display: none;
}

.chip {
  display: inline-flex;
  align-items: center;
  gap: var(--space-1);
  padding: 1px var(--space-2);
  border-radius: var(--radius-sm);
  font-size: 0.7rem;
  color: var(--text-secondary);
  background: var(--bg-neutral);
}

.chip.tone-attention {
  background: var(--attention-bg);
}

.chip.tone-hue {
  color: var(--project-hue);
  background: color-mix(in srgb, var(--project-hue) 12%, transparent);
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

.card-footer {
  display: flex;
  align-items: center;
  gap: var(--space-3);
  margin-top: auto;
  padding-top: var(--space-3);
  border-top: 0.5px solid var(--border-hairline);
  font-size: 0.75rem;
  color: var(--text-muted);
  min-width: 0;
}

.foot-item {
  display: inline-flex;
  align-items: center;
  gap: var(--space-1);
  min-width: 0;
  white-space: nowrap;
}

.foot-branch {
  overflow: hidden;
  text-overflow: ellipsis;
}

/* One slot per sandbox the project may run, filled while one runs. */
.slots {
  display: inline-flex;
  align-items: center;
  gap: 2px;
  margin-left: auto;
  flex-shrink: 0;
}

.slot {
  width: 4px;
  height: 10px;
  border-radius: 1px;
  background: var(--bg-raised);
}

.slot.on {
  background: var(--project-hue);
}

.slots-more {
  margin-left: var(--space-1);
  font-family: var(--font-mono);
  font-size: 0.7rem;
}

.new-card {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: var(--space-2);
  min-height: 120px;
  border: 0.5px dashed var(--border-active);
  border-radius: var(--radius-lg);
  color: var(--text-muted);
  font-size: 0.8rem;
  transition: all var(--transition-fast);
}

.new-card:hover {
  color: var(--text-primary);
  background: var(--bg-hover);
}

.empty-state {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: var(--space-1);
  padding: var(--space-8) var(--space-4);
  text-align: center;
}

.empty-emblems {
  display: flex;
  gap: var(--space-2);
  margin-bottom: var(--space-3);
  opacity: 0.8;
}

.empty-title {
  color: var(--text-secondary);
  font-size: 0.95rem;
}
</style>
