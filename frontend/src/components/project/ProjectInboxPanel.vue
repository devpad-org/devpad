<script setup lang="ts">
import { watch } from 'vue'
import type { AgentRun, AttentionItem } from '@/api/ai'
import { attentionItemKey } from '@/stores/attention'
import { useActiveProjectStore } from '@/stores/activeProject'
import { useProjectStore } from '@/stores/projects'
import { useOpenMergeRequest } from '@/components/ide/viewTabs'
import AttentionItemCard from '@/components/ide/AttentionItemCard.vue'

// The agents in this project waiting for the user, answered in place. Each
// item opens where its work lives without leaving the shell.
const props = defineProps<{
  items: AttentionItem[]
  // The project workspace, whose runs are the user's own assistant.
  workspaceId: number | null
}>()

// Opening the chat focuses the run that is waiting.
const emit = defineEmits<{ 'open-chat': [run: AgentRun] }>()

const store = useActiveProjectStore()
const projects = useProjectStore()
const openMergeRequest = useOpenMergeRequest()

interface ItemSource {
  detail: string
  taskId: number | null
  mergeRequestId: number | null
  // The agent's sandbox, for a closer look at what it was doing.
  sandboxId: number | null
}

function source(item: AttentionItem): ItemSource {
  const id = item.run.workspaceId
  if (id === props.workspaceId) return { detail: 'your workspace', taskId: null, mergeRequestId: null, sandboxId: null }
  const sandbox = store.sandboxes.find((s) => s.id === id)
  const task = store.sandboxTask(id)
  if (task) return { detail: `task #${task.id}`, taskId: task.id, mergeRequestId: null, sandboxId: id }
  const link = projects.workspaceLinks.get(id)
  if (link?.kind === 'review' && link.mergeRequestId) {
    return { detail: `review of !${link.mergeRequestId}`, taskId: null, mergeRequestId: link.mergeRequestId, sandboxId: id }
  }
  return { detail: `${sandbox?.kind ?? 'agent'} sandbox`, taskId: null, mergeRequestId: null, sandboxId: id }
}

// Only the launcher's overview knows which merge request a review sandbox
// reviews, so it is fetched when a review item shows up.
watch(
  () => props.items.some((item) => {
    const sandbox = store.sandboxes.find((s) => s.id === item.run.workspaceId)
    return sandbox?.kind === 'review' && !projects.workspaceLinks.has(sandbox.id)
  }),
  (missing) => {
    if (missing) projects.fetchOverview().catch(() => {})
  },
  { immediate: true },
)
</script>

<template>
  <div class="side-panel inbox-panel">
    <div class="side-header">
      <span class="side-title">
        <svg class="panel-icon" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
          <path d="M22 12h-6l-2 3h-4l-2-3H2" />
          <path d="M5.45 5.11 2 12v6a2 2 0 0 0 2 2h16a2 2 0 0 0 2-2v-6l-3.45-6.89A2 2 0 0 0 16.76 4H7.24a2 2 0 0 0-1.79 1.11z" />
        </svg>
        Needs you
      </span>
      <RouterLink :to="{ name: 'home', hash: '#inbox' }" class="all-link">All projects</RouterLink>
    </div>
    <div class="side-body inbox-body">
      <p v-if="items.length === 0" class="side-empty">Nothing in this project needs you right now.</p>
      <AttentionItemCard v-for="item in items" :key="attentionItemKey(item)" :item="item">
        <template #source>
          <span>{{ source(item).detail }}</span>
        </template>
        <template #links>
          <button v-if="source(item).taskId" class="btn btn-secondary btn-sm" @click="store.selectedTaskId = source(item).taskId">
            Open task
          </button>
          <button
            v-else-if="source(item).mergeRequestId"
            class="btn btn-secondary btn-sm"
            @click="openMergeRequest(source(item).mergeRequestId!)"
          >
            Open merge request
          </button>
          <button v-else-if="item.run.workspaceId === workspaceId" class="btn btn-secondary btn-sm" @click="emit('open-chat', item.run)">
            Open chat
          </button>
          <RouterLink
            v-if="source(item).sandboxId"
            class="btn btn-secondary btn-sm"
            :to="{ name: 'ide', params: { id: source(item).sandboxId! } }"
          >
            Sandbox
          </RouterLink>
        </template>
      </AttentionItemCard>
    </div>
  </div>
</template>

<style scoped src="./panel.css"></style>
<style scoped src="./shared.css"></style>
<style scoped>
.inbox-body {
  display: flex;
  flex-direction: column;
  gap: var(--space-3);
  padding: var(--space-3);
}

.side-empty {
  padding: var(--space-4) 0;
  text-align: center;
}

.all-link {
  font-size: 0.75rem;
  color: var(--text-secondary);
}

.all-link:hover {
  color: var(--text-primary);
}

@media (max-width: 768px), (pointer: coarse) and (max-height: 500px) {
  .inbox-body {
    padding: var(--space-4);
  }

  .all-link {
    display: inline-flex;
    align-items: center;
    min-height: 40px;
  }
}
</style>
