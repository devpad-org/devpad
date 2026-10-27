<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import type { RouteLocationRaw } from 'vue-router'
import type { AgentRun, AttentionItem } from '@/api/ai'
import { attentionItemKey, useAttentionStore } from '@/stores/attention'
import { useProjectStore } from '@/stores/projects'
import { useWorkspaceStore } from '@/stores/workspaces'
import { fullTime, timeAgo } from '@/utils/time'
import AttentionItemCard from '@/components/ide/AttentionItemCard.vue'

// Agents waiting for the user, across every project and workspace, a row each
// under the project it works in. A row opens to answer it in place and links
// to where its work lives.
const props = defineProps<{
  // Where the user is: its items lead, and its own assistant opens in place.
  currentProjectId?: number | null
  currentWorkspaceId?: number | null
}>()

// navigate: a link was followed, so a panel holding the list can close.
const emit = defineEmits<{ navigate: []; 'open-chat': [run: AgentRun] }>()

interface ItemSource {
  // The project, or the workspace outside any project, the item is under.
  groupKey: string
  groupLabel: string
  projectId: number | null
  detail: string
  to: RouteLocationRaw
  toLabel: string
  // The sandbox itself, for a closer look at what the agent was doing.
  sandbox: RouteLocationRaw | null
}

interface Row {
  key: string
  item: AttentionItem
  source: ItemSource
}

interface Group {
  key: string
  label: string
  current: boolean
  rows: Row[]
}

const attention = useAttentionStore()
const projects = useProjectStore()
const workspaces = useWorkspaceStore()

const projectNames = computed(() => new Map(projects.projects.map((p) => [p.id, p.name])))
const workspacesById = computed(() => new Map(workspaces.workspaces.map((ws) => [ws.id, ws])))

function source(item: AttentionItem): ItemSource {
  const workspaceId = item.run.workspaceId
  const ide: RouteLocationRaw = { name: 'ide', params: { id: workspaceId } }
  const link = projects.workspaceLinks.get(workspaceId)
  if (!link) {
    return {
      groupKey: `workspace-${workspaceId}`,
      groupLabel: workspacesById.value.get(workspaceId)?.name ?? `Workspace ${workspaceId}`,
      projectId: null,
      detail: 'workspace',
      to: ide,
      toLabel: 'Open workspace',
      sandbox: null,
    }
  }
  const group = {
    groupKey: `project-${link.projectId}`,
    groupLabel: projectNames.value.get(link.projectId) ?? `Project ${link.projectId}`,
    projectId: link.projectId,
  }
  const project = { name: 'project', params: { id: link.projectId } } as const
  if (link.kind === 'interactive') {
    return { ...group, detail: 'your workspace', to: project, toLabel: 'Open project', sandbox: null }
  }
  if (link.kind === 'task' && link.taskId) {
    return {
      ...group,
      detail: `task #${link.taskId}`,
      to: { ...project, query: { task: String(link.taskId) } },
      toLabel: 'Open task',
      sandbox: ide,
    }
  }
  if (link.kind === 'review' && link.mergeRequestId) {
    return {
      ...group,
      detail: `review of !${link.mergeRequestId}`,
      to: { ...project, query: { mr: String(link.mergeRequestId) } },
      toLabel: 'Open merge request',
      sandbox: ide,
    }
  }
  return { ...group, detail: `${link.kind} sandbox`, to: ide, toLabel: 'Open sandbox', sandbox: null }
}

// Groups keep the order their first item came in, except that the one the
// user is in leads.
const groups = computed<Group[]>(() => {
  const byKey = new Map<string, Group>()
  for (const item of attention.items) {
    const s = source(item)
    let group = byKey.get(s.groupKey)
    if (!group) {
      const current = s.projectId !== null
        ? s.projectId === props.currentProjectId
        : item.run.workspaceId === props.currentWorkspaceId
      group = { key: s.groupKey, label: s.groupLabel, current, rows: [] }
      byKey.set(s.groupKey, group)
    }
    group.rows.push({ key: attentionItemKey(item), item, source: s })
  }
  return [...byKey.values()].sort((a, b) => Number(b.current) - Number(a.current))
})

// Items arrive from workspaces the stores may not know yet, such as a task's
// new sandbox; fetch them so each row names where it comes from.
const unknownSource = computed(() =>
  attention.items.some((item) => {
    const link = projects.workspaceLinks.get(item.run.workspaceId)
    if (link) return !projectNames.value.has(link.projectId)
    const ws = workspacesById.value.get(item.run.workspaceId)
    return !ws || ws.projectId != null
  }),
)

watch(
  unknownSource,
  (unknown) => {
    if (!unknown) return
    projects.fetchProjects().catch(() => {})
    projects.fetchOverview().catch(() => {})
    workspaces.fetchWorkspaces().catch(() => {})
  },
  { immediate: true },
)

const expanded = ref(new Set<string>())

function toggle(key: string) {
  const next = new Set(expanded.value)
  if (!next.delete(key)) next.add(key)
  expanded.value = next
}

function kind(item: AttentionItem): string {
  return item.approval ? item.approval.title || 'Approve a command' : 'Answer a question'
}

// One line of what is asked, for the closed row.
function preview(item: AttentionItem): string {
  if (item.approval) return item.approval.command.split('\n', 1)[0]
  return item.question?.title || item.question?.questions[0]?.prompt || ''
}

// The current workspace's own assistant has its chat right here.
function opensChat(item: AttentionItem): boolean {
  return props.currentWorkspaceId != null && item.run.workspaceId === props.currentWorkspaceId
}

function openChat(run: AgentRun) {
  emit('open-chat', run)
  emit('navigate')
}
</script>

<template>
  <div class="inbox-list">
    <p v-if="!attention.loaded" class="empty-text inbox-empty">Loading...</p>
    <p v-else-if="groups.length === 0" class="empty-text inbox-empty">Nothing needs you right now.</p>

    <section v-for="group in groups" :key="group.key" class="group" :aria-label="group.label">
      <h3 class="group-title">
        <span class="group-name">{{ group.label }}</span>
        <span v-if="group.current" class="group-here">here</span>
        <span class="group-count">{{ group.rows.length }}</span>
      </h3>

      <div v-for="row in group.rows" :key="row.key" class="row" :class="{ open: expanded.has(row.key) }">
        <button class="row-summary" :aria-expanded="expanded.has(row.key)" @click="toggle(row.key)">
          <span class="row-top">
            <span class="row-kind">{{ kind(row.item) }}</span>
            <span class="row-time" :title="fullTime(row.item.run.updatedAt)">{{ timeAgo(row.item.run.updatedAt) }}</span>
            <svg class="row-chevron" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
              <path d="m6 9 6 6 6-6" />
            </svg>
          </span>
          <span class="row-detail">{{ row.source.detail }}</span>
          <span
            v-if="!expanded.has(row.key) && preview(row.item)"
            class="row-preview"
            :class="{ mono: row.item.approval && !row.item.approval.title }"
          >{{ preview(row.item) }}</span>
        </button>

        <AttentionItemCard v-if="expanded.has(row.key)" :item="row.item" embedded class="row-card">
          <template #links>
            <button v-if="opensChat(row.item)" class="btn btn-secondary btn-sm" @click="openChat(row.item.run)">
              Open chat
            </button>
            <template v-else>
              <RouterLink v-if="row.source.sandbox" class="btn btn-secondary btn-sm" :to="row.source.sandbox" @click="emit('navigate')">
                Sandbox
              </RouterLink>
              <RouterLink class="btn btn-secondary btn-sm" :to="row.source.to" @click="emit('navigate')">
                {{ row.source.toLabel }}
              </RouterLink>
            </template>
          </template>
        </AttentionItemCard>
      </div>
    </section>
  </div>
</template>

<style scoped src="@/components/project/shared.css"></style>
<style scoped>
.inbox-list {
  display: flex;
  flex-direction: column;
  gap: var(--space-4);
}

.inbox-empty {
  padding: var(--space-6) 0;
  text-align: center;
}

.group {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
}

.group-title {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  margin: 0;
  font-size: 0.75rem;
  font-weight: 500;
  color: var(--text-secondary);
}

.group-name {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.group-here {
  padding: 0 var(--space-1);
  border-radius: var(--radius-sm);
  background: var(--accent-glow);
  color: var(--accent-blue);
  font-size: 0.68rem;
}

.group-count {
  margin-left: auto;
  color: var(--text-muted);
}

.row {
  border: 0.5px solid var(--border-default);
  border-radius: var(--radius-md);
  background: var(--bg-surface);
  transition: border-color var(--transition-fast);
}

.row.open {
  border-color: var(--border-active);
}

.row-summary {
  display: flex;
  flex-direction: column;
  gap: 2px;
  width: 100%;
  padding: var(--space-2) var(--space-3);
  border-radius: var(--radius-md);
  text-align: left;
  transition: background var(--transition-fast);
}

.row-summary:hover {
  background: var(--bg-hover);
}

.row-top {
  display: flex;
  align-items: center;
  gap: var(--space-2);
}

.row-kind {
  flex: 1;
  min-width: 0;
  font-size: 0.85rem;
  font-weight: 500;
  color: var(--attention);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.row-time {
  flex-shrink: 0;
  font-size: 0.72rem;
  color: var(--text-muted);
}

.row-chevron {
  flex-shrink: 0;
  color: var(--text-muted);
  transition: transform var(--transition-fast);
}

.row.open .row-chevron {
  transform: rotate(180deg);
}

.row-detail {
  font-size: 0.75rem;
  color: var(--text-secondary);
}

.row-preview {
  font-size: 0.75rem;
  color: var(--text-secondary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.row-card {
  padding: var(--space-1) var(--space-3) var(--space-3);
}

/* Matches MOBILE_MEDIA_QUERY in useMediaQuery.ts: touch-sized rows. */
@media (max-width: 768px), (pointer: coarse) and (max-height: 500px) {
  .row-summary {
    min-height: 44px;
    padding: var(--space-3);
  }
}
</style>
