<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { aiApi, type AttentionItem } from '@/api/ai'
import { useAttentionStore } from '@/stores/attention'
import { useWorkspaceStore } from '@/stores/workspaces'
import AiQuestionCard from '@/components/ide/AiQuestionCard.vue'

const attention = useAttentionStore()
const workspaces = useWorkspaceStore()

const busy = ref<string | null>(null)
const errors = ref<Record<string, string>>({})

const workspaceNames = computed(() => new Map(workspaces.workspaces.map((ws) => [ws.id, ws.name])))

onMounted(() => {
  attention.watch()
  void workspaces.fetchWorkspaces().catch(() => {})
})

onUnmounted(() => attention.unwatch())

function itemKey(item: AttentionItem): string {
  return item.approval?.id ?? item.question?.id ?? `run-${item.run.id}`
}

function workspaceName(item: AttentionItem): string {
  return workspaceNames.value.get(item.run.workspaceId) ?? `Workspace ${item.run.workspaceId}`
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
  <div class="page inbox-page">
    <div class="page-header">
      <div>
        <h1 class="page-title">Inbox</h1>
        <p class="page-subtitle">Agents waiting for you, across all workspaces and sandboxes</p>
      </div>
    </div>

    <p v-if="!attention.loaded" class="empty-text">Loading...</p>
    <p v-else-if="attention.items.length === 0" class="empty-text">Nothing needs you right now.</p>

    <div v-for="item in attention.items" :key="itemKey(item)" class="panel item">
      <div class="item-header">
        <div class="item-source">
          <span class="item-kind">{{ item.approval ? 'Approve a command' : 'Answer a question' }}</span>
          <span class="meta-row">
            <span>{{ workspaceName(item) }}</span>
            <span>&middot;</span>
            <span>run #{{ item.run.id }}</span>
            <span v-if="item.run.model">&middot; {{ item.run.model }}</span>
          </span>
        </div>
        <RouterLink class="btn btn-secondary btn-sm" :to="{ name: 'ide', params: { id: item.run.workspaceId } }">
          Open workspace
        </RouterLink>
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
  </div>
</template>

<style scoped src="@/components/project/shared.css"></style>
<style scoped>
.inbox-page {
  max-width: 820px;
}

.item {
  display: flex;
  flex-direction: column;
  gap: var(--space-3);
  margin-bottom: var(--space-3);
}

.item .form-error {
  margin-bottom: 0;
}

.item-header {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
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
