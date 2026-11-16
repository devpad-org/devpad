<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { aiApi, type BudgetDefaults } from '@/api/ai'

// Budgets as typed, in USD; empty or zero means no limit. v-model on a
// number input stores a number once anything is typed.
type BudgetForm = Record<keyof BudgetDefaults, number | string>

const form = ref<BudgetForm>({ taskUsd: '', reviewUsd: '', assistantUsd: '' })
const loading = ref(true)
const error = ref('')
const success = ref('')
const submitting = ref(false)

function toForm(b: BudgetDefaults): BudgetForm {
  const field = (usd: number) => (usd > 0 ? String(usd) : '')
  return { taskUsd: field(b.taskUsd), reviewUsd: field(b.reviewUsd), assistantUsd: field(b.assistantUsd) }
}

onMounted(async () => {
  try {
    form.value = toForm((await aiApi.getBudgets()).budgets)
  } catch {
    error.value = 'Failed to load agent budgets'
  } finally {
    loading.value = false
  }
})

async function save() {
  error.value = ''
  success.value = ''
  const parse = (value: number | string) => (String(value).trim() === '' ? 0 : Number(value))
  const budgets: BudgetDefaults = {
    taskUsd: parse(form.value.taskUsd),
    reviewUsd: parse(form.value.reviewUsd),
    assistantUsd: parse(form.value.assistantUsd),
  }
  if (Object.values(budgets).some((usd) => !Number.isFinite(usd) || usd < 0)) {
    error.value = 'Each budget must be an amount in USD, or empty for no limit'
    return
  }
  submitting.value = true
  try {
    form.value = toForm((await aiApi.setBudgets(budgets)).budgets)
    success.value = 'Budgets saved'
  } catch (e) {
    error.value = e instanceof Error ? e.message : 'Failed to save budgets'
  } finally {
    submitting.value = false
  }
}
</script>

<template>
  <div>
    <header class="page-header">
      <h2 class="page-title">Agent Budgets</h2>
      <p class="page-desc">
        What your agents may spend before they stop, counting the agents they start. Leave a budget empty for no limit.
        Requests to models without prices aren't counted, so those runs stop only at devpad's limit of 500 model requests
        per run.
      </p>
    </header>

    <div v-if="error" class="alert alert-error">{{ error }}</div>
    <div v-if="success" class="alert alert-success">{{ success }}</div>

    <div v-if="loading" class="loading-state">Loading budgets...</div>

    <form v-else class="card" @submit.prevent="save">
      <div class="form-field">
        <label for="budget-task">Per task (USD)</label>
        <input id="budget-task" v-model="form.taskUsd" type="number" min="0" step="any" placeholder="No limit" />
        <p class="field-hint">
          Every run of a task's agent, across retries and rounds of feedback, and every review of its merge request. New
          tasks start with this; it can be raised on each task.
        </p>
      </div>
      <div class="form-field">
        <label for="budget-review">Per review (USD)</label>
        <input id="budget-review" v-model="form.reviewUsd" type="number" min="0" step="any" placeholder="No limit" />
        <p class="field-hint">
          One review agent on a merge request. A review of a task's change also spends from the task's budget. It can be
          raised on a review that reached it.
        </p>
      </div>
      <div class="form-field">
        <label for="budget-assistant">Per assistant reply (USD)</label>
        <input
          id="budget-assistant"
          v-model="form.assistantUsd"
          type="number"
          min="0"
          step="any"
          placeholder="No limit"
        />
        <p class="field-hint">The assistant's work on one message in a workspace.</p>
      </div>
      <div class="card-actions">
        <span class="field-hint">
          A run that reaches its budget stops after its current request; raising the budget lets a task's agent or a
          review continue.
        </span>
        <div class="card-buttons">
          <button type="submit" class="btn-primary" :disabled="submitting">
            {{ submitting ? 'Saving...' : 'Save' }}
          </button>
        </div>
      </div>
    </form>
  </div>
</template>

<style scoped src="./settings.css"></style>
