<script setup lang="ts">
import { computed, ref } from 'vue'
import type { RouteLocationRaw } from 'vue-router'
import { aiApi, type AttentionItem } from '@/api/ai'
import { useAttentionStore } from '@/stores/attention'
import { useProjectStore } from '@/stores/projects'
import { useWorkspaceStore } from '@/stores/workspaces'
import { BOARD_TAB, mergeRequestTab } from '@/components/ide/viewTabs'
import AiQuestionCard from '@/components/ide/AiQuestionCard.vue'

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

const busy = ref<string | null>(null)
const errors = ref<Record<string, string>>({})

const projectNames = computed(() => new Map(projects.projects.map((p) => [p.id, p.name])))
const workspaceNames = computed(() => new Map(workspaces.workspaces.map((ws) => [ws.id, ws.name])))

function itemKey(item: AttentionItem): string {
  return item.approval?.id ?? item.question?.id ?? `run-${item.run.id}`
}

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
    const tab = mergeRequestTab(link.mergeRequestId)
    return {
      label,
      detail: `review of !${link.mergeRequestId}`,
      to: { ...project, query: { tabs: `${BOARD_TAB},${tab}`, tab } },
      toLabel: 'Open merge request',
      sandbox: ide,
    }
  }
  return { label, detail: `${link.kind} sandbox`, to: ide, toLabel: 'Open sandbox', sandbox: null }
}

async function decide(item: AttentionItem, approved: boolean) {
  if (!item.approval) return
  const key = itemKey(item)
  busy.value = key
  delete errors.value[key]
  try {
    await aiApi.approveCommand(item.approval.id, approved)
    await attention.refresh()
  } catch (e) {
    errors.value[key] = e instanceof Error ? e.message : 'Request failed'
  } finally {
    busy.value = null
  }
}

function questionError(item: AttentionItem, message: string) {
  errors.value[itemKey(item)] = message
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

    <div v-for="item in attention.items" :key="itemKey(item)" class="panel item">
      <div class="item-header">
        <div class="item-source">
          <span class="item-kind">{{ item.approval ? 'Approve a command' : 'Answer a question' }}</span>
          <span class="meta-row">
            <span>{{ source(item).label }}</span>
            <span>&middot;</span>
            <span>{{ source(item).detail }}</span>
            <span>&middot;</span>
            <span>run #{{ item.run.id }}</span>
            <span v-if="item.run.model">&middot; {{ item.run.model }}</span>
          </span>
        </div>
        <div class="item-links">
          <RouterLink v-if="source(item).sandbox" class="btn btn-secondary btn-sm" :to="source(item).sandbox!">
            Sandbox
          </RouterLink>
          <RouterLink class="btn btn-secondary btn-sm" :to="source(item).to">{{ source(item).toLabel }}</RouterLink>
        </div>
      </div>

      <p v-if="item.run.promptPreview" class="item-prompt">{{ item.run.promptPreview }}</p>

      <template v-if="item.approval">
        <pre class="command">{{ item.approval.command }}</pre>
        <div class="item-actions">
          <button class="btn btn-secondary" :disabled="busy === itemKey(item)" @click="decide(item, false)">Deny</button>
          <button class="btn btn-primary" :disabled="busy === itemKey(item)" @click="decide(item, true)">Approve</button>
        </div>
      </template>
      <AiQuestionCard
        v-else-if="item.question"
        :request="item.question"
        status="pending"
        :answers="[]"
        @answered="attention.refresh()"
        @error="questionError(item, $event)"
      />

      <p v-if="errors[itemKey(item)]" class="form-error">{{ errors[itemKey(item)] }}</p>
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

.item {
  display: flex;
  flex-direction: column;
  gap: var(--space-3);
  margin-bottom: var(--space-3);
  min-width: 0;
}

.item .form-error {
  margin-bottom: 0;
}

.item-header {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  flex-wrap: wrap;
  gap: var(--space-3);
}

.item-source {
  display: flex;
  flex-direction: column;
  gap: 2px;
  min-width: 0;
}

.item-kind {
  font-size: 0.85rem;
  font-weight: 500;
  color: var(--attention);
}

.item-links {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-2);
}

.item-prompt {
  font-size: 0.8rem;
  color: var(--text-secondary);
  display: -webkit-box;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
  overflow: hidden;
}

.command {
  padding: var(--space-2) var(--space-3);
  background: var(--bg-void);
  border: 0.5px solid var(--border-default);
  border-radius: var(--radius-md);
  font-family: var(--font-mono);
  font-size: 0.78rem;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
  max-height: 240px;
  overflow-y: auto;
}

.item-actions {
  display: flex;
  justify-content: flex-end;
  gap: var(--space-2);
}
</style>
