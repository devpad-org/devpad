<script setup lang="ts">
import DocLink from '@/components/DocLink.vue'
import { ref, watch } from 'vue'
import { formatUsd } from '@/utils/cost'
import MrIcon from './MrIcon.vue'

// A budget an agent has spent: whose it is, as a sentence names it ("Task
// #3", "This review"), what the field to raise it is called, and the amounts.
export interface SpentBudget {
  key: string
  owner: string
  label: string
  spentUsd: number
  budgetUsd: number
}

// Says which budgets stopped an agent and lets the user raise them in place,
// then do what the agent was stopped from doing.
const props = defineProps<{
  budgets: SpentBudget[]
  // The button, such as "Raise and continue".
  action: string
  busy?: boolean
  hint?: string
  error?: string
}>()

const emit = defineEmits<{
  raise: [budgets: Record<string, number>]
}>()

// Each new budget as typed, by key; empty means no limit. A number input's
// v-model stores a number once anything is typed.
const amounts = ref<Record<string, number | string>>({})
const invalid = ref('')

// Another budget's worth on top of what is spent, to the cent.
function suggested(b: SpentBudget): string {
  return String(Math.ceil((b.spentUsd + b.budgetUsd) * 100) / 100)
}

watch(
  () => props.budgets.map((b) => `${b.key}:${b.budgetUsd}`).join(','),
  () => {
    amounts.value = Object.fromEntries(props.budgets.map((b) => [b.key, suggested(b)]))
    invalid.value = ''
  },
  { immediate: true },
)

function submit() {
  if (props.busy) return
  const raised: Record<string, number> = {}
  for (const b of props.budgets) {
    const typed = String(amounts.value[b.key] ?? '').trim()
    const usd = typed === '' ? 0 : Number(typed)
    if (!Number.isFinite(usd) || usd < 0) {
      invalid.value = 'A budget must be an amount in USD, or empty for no limit'
      return
    }
    if (usd > 0 && usd <= b.spentUsd) {
      invalid.value = `Raise the ${b.label.toLowerCase()} above the ${formatUsd(b.spentUsd)} already spent, or leave it empty for no limit`
      return
    }
    raised[b.key] = usd
  }
  invalid.value = ''
  emit('raise', raised)
}
</script>

<template>
  <div class="notice notice-warning budget-reached" role="alert">
    <p class="budget-head">
      <MrIcon name="warning" :size="14" />
      <strong>Budget reached</strong>
      <DocLink topic="budgets" label="Budgets" />
    </p>
    <p v-for="b in budgets" :key="b.key" class="budget-line">
      {{ b.owner }} has spent {{ formatUsd(b.spentUsd) }} of its {{ formatUsd(b.budgetUsd) }} budget.
    </p>
    <p v-if="hint" class="budget-line">{{ hint }}</p>
    <form class="budget-form" @submit.prevent="submit">
      <label v-for="b in budgets" :key="b.key" class="budget-field">
        <span class="budget-label">{{ b.label }} (USD)</span>
        <input
          v-model="amounts[b.key]"
          class="form-input budget-input"
          type="number"
          min="0"
          step="any"
          inputmode="decimal"
          placeholder="No limit"
          :disabled="busy"
        />
      </label>
      <button type="submit" class="btn btn-primary btn-sm" :disabled="busy">
        <span v-if="busy" class="spinner spinner-sm spinner-on-accent" />
        {{ action }}
      </button>
    </form>
    <p v-if="invalid || error" class="budget-error">{{ invalid || error }}</p>
  </div>
</template>

<style scoped src="./shared.css"></style>
<style scoped>
.budget-reached {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
}

.budget-head {
  display: flex;
  align-items: center;
  gap: var(--space-2);
}

.budget-line {
  color: var(--text-secondary);
}

.budget-form {
  display: flex;
  flex-wrap: wrap;
  align-items: flex-end;
  gap: var(--space-2) var(--space-3);
}

.budget-field {
  display: flex;
  flex-direction: column;
  gap: var(--space-1);
}

.budget-label {
  font-size: 0.75rem;
  color: var(--text-secondary);
}

.budget-input {
  width: 9rem;
}

.budget-error {
  color: var(--error);
}
</style>
