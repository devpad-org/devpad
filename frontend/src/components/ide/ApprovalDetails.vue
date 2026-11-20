<script setup lang="ts">
import { ref } from 'vue'
import type { ApprovalDetail } from '@/api/ai'

// Everything an action awaiting approval carries, in full. Text the model
// wrote is quoted verbatim in its own block, so it reads as the model's
// words, not the app's; long text collapses but is never cut.
defineProps<{ details: ApprovalDetail[] }>()

const COLLAPSED_CHARS = 600
const COLLAPSED_LINES = 12

const expanded = ref<Set<number>>(new Set())

function isLong(value: string): boolean {
  return value.length > COLLAPSED_CHARS || value.split('\n').length > COLLAPSED_LINES
}

function toggle(index: number) {
  const next = new Set(expanded.value)
  if (next.has(index)) next.delete(index)
  else next.add(index)
  expanded.value = next
}
</script>

<template>
  <dl class="approval-details">
    <template v-for="(detail, index) in details" :key="index">
      <div v-if="detail.text" class="detail detail--text">
        <dt>{{ detail.label }}</dt>
        <dd>
          <pre
            v-if="detail.value.trim()"
            class="quoted"
            :class="{ collapsed: isLong(detail.value) && !expanded.has(index) }"
          >{{ detail.value }}</pre>
          <span v-else class="empty">Empty</span>
          <button v-if="isLong(detail.value)" type="button" class="expand" @click="toggle(index)">
            {{ expanded.has(index) ? 'Show less' : `Show all ${detail.value.length.toLocaleString()} characters` }}
          </button>
        </dd>
      </div>
      <div v-else class="detail">
        <dt>{{ detail.label }}</dt>
        <dd><code>{{ detail.value }}</code></dd>
      </div>
    </template>
  </dl>
</template>

<style scoped>
.approval-details {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
  margin: var(--space-2) 0 0;
  font-size: 0.78rem;
}

.detail {
  display: flex;
  align-items: baseline;
  gap: var(--space-2);
  min-width: 0;
}

.detail--text {
  flex-direction: column;
  gap: var(--space-1);
}

dt {
  flex-shrink: 0;
  color: var(--text-muted);
}

dd {
  margin: 0;
  min-width: 0;
  overflow-wrap: anywhere;
}

dd > code {
  font-family: var(--font-mono);
  color: var(--text-primary);
}

.detail--text dd {
  width: 100%;
}

.quoted {
  margin: 0;
  padding: var(--space-2);
  border-left: 2px solid var(--border-strong);
  border-radius: var(--radius-sm);
  background: var(--bg-neutral);
  color: var(--text-primary);
  font-family: var(--font-mono);
  font-size: 0.76rem;
  line-height: 1.5;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}

.quoted.collapsed {
  max-height: 12em;
  overflow: hidden;
  mask-image: linear-gradient(to bottom, black 70%, transparent);
}

.empty {
  color: var(--text-muted);
  font-style: italic;
}

.expand {
  margin-top: var(--space-1);
  padding: 0;
  font-size: 0.74rem;
  color: var(--accent-blue);
}

.expand:hover {
  text-decoration: underline;
}

/* Matches MOBILE_MEDIA_QUERY in useMediaQuery.ts: a touch-sized target. */
@media (max-width: 768px), (pointer: coarse) and (max-height: 500px) {
  .expand {
    min-height: 40px;
    font-size: 0.8rem;
  }
}
</style>
