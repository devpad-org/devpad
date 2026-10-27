<script setup lang="ts">
import { computed } from 'vue'
import type { RouteLocationRaw } from 'vue-router'
import type { AttentionItem } from '@/api/ai'
import { attentionItemKey, useAttentionStore } from '@/stores/attention'
import { useProjectStore } from '@/stores/projects'
import { useWorkspaceStore } from '@/stores/workspaces'
import AttentionItemCard from '@/components/ide/AttentionItemCard.vue'

// Agents waiting for the user, across every project and workspace. Each item
// can be answered in place, and links to where its work lives.

interface ItemSource {
  label: string
  detail: string
  to: RouteLocationRaw
  toLabel: string
  // The sandbox itself, for a closer look at what the agent was doing.
  sandbox: RouteLocationRaw | null
}

const attention = useAttentionStore()
const projects = useProjectStore()
const workspaces = useWorkspaceStore()

const projectNames = computed(() => new Map(projects.projects.map((p) => [p.id, p.name])))
const workspaceNames = computed(() => new Map(workspaces.workspaces.map((ws) => [ws.id, ws.name])))

function source(item: AttentionItem): ItemSource {
  const workspaceId = item.run.workspaceId
  const ide: RouteLocationRaw = { name: 'ide', params: { id: workspaceId } }
  const link = projects.workspaceLinks.get(workspaceId)
  if (!link) {
    return {
      label: workspaceNames.value.get(workspaceId) ?? `Workspace ${workspaceId}`,
      detail: 'workspace',
      to: ide,
      toLabel: 'Open workspace',
      sandbox: null,
    }
  }
  const project = { name: 'project', params: { id: link.projectId } } as const
  const label = projectNames.value.get(link.projectId) ?? `Project ${link.projectId}`
  if (link.kind === 'interactive') {
    return { label, detail: 'your workspace', to: project, toLabel: 'Open project', sandbox: null }
  }
  if (link.kind === 'task' && link.taskId) {
    return {
      label,
      detail: `task #${link.taskId}`,
      to: { ...project, query: { task: String(link.taskId) } },
      toLabel: 'Open task',
      sandbox: ide,
    }
  }
  if (link.kind === 'review' && link.mergeRequestId) {
    return {
      label,
      detail: `review of !${link.mergeRequestId}`,
      to: { ...project, query: { mr: String(link.mergeRequestId) } },
      toLabel: 'Open merge request',
      sandbox: ide,
    }
  }
  return { label, detail: `${link.kind} sandbox`, to: ide, toLabel: 'Open sandbox', sandbox: null }
}
</script>

<template>
  <section id="inbox" class="section inbox">
    <div class="section-header">
      <h2 class="section-title">
        Inbox
        <span v-if="attention.count > 0" class="count-badge">{{ attention.count }}</span>
      </h2>
    </div>

    <p v-if="!attention.loaded" class="empty-text">Loading...</p>
    <p v-else-if="attention.items.length === 0" class="empty-text">Nothing needs you right now.</p>

    <div class="items">
      <AttentionItemCard v-for="item in attention.items" :key="attentionItemKey(item)" :item="item">
        <template #source>
          <span>{{ source(item).label }}</span>
          <span>&middot;</span>
          <span>{{ source(item).detail }}</span>
        </template>
        <template #links>
          <RouterLink v-if="source(item).sandbox" class="btn btn-secondary btn-sm" :to="source(item).sandbox!">
            Sandbox
          </RouterLink>
          <RouterLink class="btn btn-secondary btn-sm" :to="source(item).to">{{ source(item).toLabel }}</RouterLink>
        </template>
      </AttentionItemCard>
    </div>
  </section>
</template>

<style scoped src="@/components/project/shared.css"></style>
<style scoped>
.inbox {
  scroll-margin-top: var(--space-4);
}

.section-title {
  display: inline-flex;
  align-items: center;
  gap: var(--space-2);
}

.count-badge {
  min-width: 18px;
  padding: 0 var(--space-1);
  border-radius: var(--radius-sm);
  background: var(--attention);
  color: var(--text-on-accent);
  font-size: 0.7rem;
  font-weight: 600;
  line-height: 18px;
  text-align: center;
}

.items {
  display: flex;
  flex-direction: column;
  gap: var(--space-3);
}
</style>
