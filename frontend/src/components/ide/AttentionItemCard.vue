<script setup lang="ts">
import { ref } from 'vue'
import { aiApi, type AttentionItem } from '@/api/ai'
import { useAttentionStore } from '@/stores/attention'
import AiQuestionCard from '@/components/ide/AiQuestionCard.vue'
import ApprovalDetails from '@/components/ide/ApprovalDetails.vue'

// One agent run waiting for the user: an approval to grant or deny, or a
// question to answer, in place. The caller says where the run comes from and
// where its work lives through the source and links slots. Embedded, it sits
// under a row that already names it, so it drops its own frame and heading
// and puts its links last.
const props = defineProps<{ item: AttentionItem; embedded?: boolean }>()

const attention = useAttentionStore()
const busy = ref(false)
const error = ref('')

async function decide(approved: boolean) {
  const approval = props.item.approval
  if (!approval) return
  busy.value = true
  error.value = ''
  try {
    await aiApi.approveCommand(approval.id, approved)
    await attention.refresh()
  } catch (e) {
    error.value = e instanceof Error ? e.message : 'Request failed'
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <div class="item" :class="{ panel: !embedded }">
    <div v-if="!embedded" class="item-header">
      <div class="item-source">
        <span class="item-kind">{{ item.approval ? item.approval.title || 'Approve a command' : 'Answer a question' }}</span>
        <span class="meta-row">
          <slot name="source" />
          <span>&middot;</span>
          <span>run #{{ item.run.id }}</span>
          <span v-if="item.run.model">&middot; {{ item.run.model }}</span>
        </span>
      </div>
      <div class="item-links">
        <slot name="links" />
      </div>
    </div>

    <p v-if="item.run.promptPreview" class="item-prompt">{{ item.run.promptPreview }}</p>

    <template v-if="item.approval">
      <p v-if="item.approval.title" class="approval-summary">{{ item.approval.command }}</p>
      <pre v-else class="command">{{ item.approval.command }}</pre>
      <ApprovalDetails v-if="item.approval.details?.length" :details="item.approval.details" />
      <div class="item-actions">
        <button class="btn btn-secondary" :disabled="busy" @click="decide(false)">Deny</button>
        <button class="btn btn-primary" :disabled="busy" @click="decide(true)">Approve</button>
      </div>
    </template>
    <AiQuestionCard
      v-else-if="item.question"
      :request="item.question"
      status="pending"
      :answers="[]"
      @answered="attention.refresh()"
      @error="error = $event"
    />

    <p v-if="error" class="form-error">{{ error }}</p>

    <div v-if="embedded && $slots.links" class="item-links item-links-end">
      <slot name="links" />
    </div>
  </div>
</template>

<style scoped src="@/components/project/shared.css"></style>
<style scoped>
.item {
  display: flex;
  flex-direction: column;
  gap: var(--space-3);
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

.item-links-end {
  padding-top: var(--space-3);
  border-top: 0.5px solid var(--border-default);
}

.item-prompt {
  font-size: 0.8rem;
  color: var(--text-secondary);
  display: -webkit-box;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
  overflow: hidden;
}

.approval-summary {
  margin: 0;
  font-size: 0.85rem;
  line-height: 1.45;
  overflow-wrap: anywhere;
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

/* Matches MOBILE_MEDIA_QUERY in useMediaQuery.ts. Approving is the action a
   phone is most often picked up for, so the buttons span the card. */
@media (max-width: 768px), (pointer: coarse) and (max-height: 500px) {
  .item-actions .btn {
    flex: 1;
    min-height: 44px;
    font-size: 0.9rem;
  }
}
</style>
