<script setup lang="ts">
import { computed, reactive, watch } from 'vue'
import { aiApi, type UserQuestion, type UserQuestionAnswer, type UserQuestionRequest, type UserQuestionResult } from '@/api/ai'

const props = defineProps<{
  request: UserQuestionRequest
  status: 'pending' | UserQuestionResult['status']
  answers: UserQuestionAnswer[]
  error?: string
}>()

const emit = defineEmits<{
  (e: 'answered', answers: UserQuestionAnswer[]): void
  (e: 'error', message: string): void
}>()

const selections = reactive<Record<string, string[]>>({})
const customAnswers = reactive<Record<string, string>>({})

watch(
  () => [props.request, props.answers] as const,
  () => initializeAnswers(),
  { immediate: true, deep: true },
)

const ready = computed(() => (
  props.request.questions.every((question) => hasAnswer(question))
))

function initializeAnswers(): void {
  const activeQuestionIDs = new Set(props.request.questions.map((question) => question.id))
  for (const key of Object.keys(selections)) {
    if (!activeQuestionIDs.has(key)) delete selections[key]
  }
  for (const key of Object.keys(customAnswers)) {
    if (!activeQuestionIDs.has(key)) delete customAnswers[key]
  }

  for (const question of props.request.questions) {
    const answer = props.answers.find((candidate) => candidate.questionId === question.id)
    selections[question.id] = answer?.values ? [...answer.values] : selections[question.id] ?? []
    customAnswers[question.id] = answer?.custom ?? customAnswers[question.id] ?? ''
  }
}

function toggleOption(question: UserQuestion, value: string): void {
  if (props.status !== 'pending') return
  const current = selections[question.id] ?? []
  if (question.type === 'multiple_choice') {
    selections[question.id] = current.includes(value)
      ? current.filter((candidate) => candidate !== value)
      : [...current, value]
    return
  }
  selections[question.id] = current.includes(value) ? [] : [value]
}

function isSelected(question: UserQuestion, value: string): boolean {
  return (selections[question.id] ?? []).includes(value)
}

function updateCustomAnswer(question: UserQuestion, event: Event): void {
  customAnswers[question.id] = (event.target as HTMLTextAreaElement).value
}

function hasAnswer(question: UserQuestion): boolean {
  return (selections[question.id] ?? []).length > 0 || Boolean((customAnswers[question.id] ?? '').trim())
}

function buildAnswers(): UserQuestionAnswer[] {
  return props.request.questions.map((question) => {
    const values = selections[question.id] ?? []
    const custom = (customAnswers[question.id] ?? '').trim()
    return {
      questionId: question.id,
      values: values.length > 0 ? values : undefined,
      custom: custom || undefined,
      skipped: values.length === 0 && custom === '',
    }
  })
}

async function submit(): Promise<void> {
  if (props.status !== 'pending' || !ready.value) return
  const answers = buildAnswers()
  try {
    await aiApi.answerQuestion(props.request.id, answers)
    emit('answered', answers)
  } catch (err) {
    emit('error', err instanceof Error ? err.message : 'Failed to submit answer')
  }
}

function statusLabel(status: 'pending' | UserQuestionResult['status']): string {
  switch (status) {
    case 'answered':
      return 'Answered'
    case 'expired':
      return 'Expired'
    case 'failed':
      return 'Failed'
    default:
      return 'Waiting for your answer'
  }
}
</script>

<template>
  <div class="question-card">
    <div class="question-card-header">
      <span class="question-card-icon">
        <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
          <path d="M21 15a4 4 0 0 1-4 4H8l-5 3V7a4 4 0 0 1 4-4h10a4 4 0 0 1 4 4z" />
          <path d="M9.09 9a3 3 0 0 1 5.83 1c0 2-3 2-3 4" />
          <path d="M12 17h.01" />
        </svg>
      </span>
      <div class="question-card-heading">
        <span class="question-card-title">{{ request.title || 'Clarification needed' }}</span>
        <span class="question-card-subtitle">{{ statusLabel(status) }}</span>
      </div>
    </div>

    <div class="question-list">
      <div
        v-for="question in request.questions"
        :key="question.id"
        class="question-item"
      >
        <div class="question-prompt">{{ question.prompt }}</div>
        <div v-if="question.type !== 'text' && (question.options?.length ?? 0) > 0" class="question-options">
          <button
            v-for="option in question.options"
            :key="option.value"
            type="button"
            class="question-option"
            :class="{ selected: isSelected(question, option.value) }"
            :disabled="status !== 'pending'"
            @click="toggleOption(question, option.value)"
          >
            <span class="question-option-marker">
              <svg v-if="isSelected(question, option.value)" width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="3" stroke-linecap="round" stroke-linejoin="round">
                <polyline points="20 6 9 17 4 12" />
              </svg>
            </span>
            <span>{{ option.label }}</span>
          </button>
        </div>
        <label class="question-custom">
          <span class="question-custom-label">
            {{ question.type === 'text' ? 'Your answer' : 'Something else' }}
          </span>
          <textarea
            class="question-custom-input"
            rows="2"
            :value="customAnswers[question.id] ?? ''"
            :placeholder="question.placeholder || (question.type === 'text' ? 'Type your answer...' : 'Type a custom answer if none of the choices fit...')"
            :disabled="status !== 'pending'"
            @input="updateCustomAnswer(question, $event)"
          />
        </label>
      </div>
    </div>

    <div v-if="status === 'pending'" class="question-actions">
      <button
        class="question-submit"
        type="button"
        :disabled="!ready"
        @click="submit"
      >
        Submit answer
      </button>
      <span class="question-hint">Custom answers are accepted.</span>
    </div>
    <div v-else class="question-resolved">
      <span
        class="question-badge"
        :class="{ answered: status === 'answered', expired: status === 'expired', failed: status === 'failed' }"
      >
        {{ statusLabel(status) }}
      </span>
    </div>
    <div v-if="error" class="question-error">
      {{ error }}
    </div>
  </div>
</template>

<style scoped>
.question-card {
  display: flex;
  flex-direction: column;
  gap: var(--space-3);
  margin: 8px 0;
  padding: 12px;
  border: 0.5px solid var(--accent-border);
  border-radius: var(--radius-lg);
  background:
    radial-gradient(circle at top left, var(--accent-glow), transparent 44%),
    color-mix(in srgb, var(--bg-raised) 82%, var(--bg-elevated));
}

.question-card-header {
  display: flex;
  align-items: center;
  gap: var(--space-2);
}

.question-card-icon {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 26px;
  height: 26px;
  flex-shrink: 0;
  border: 0.5px solid var(--accent-border);
  border-radius: var(--radius-md);
  background: var(--accent-glow);
  color: var(--accent);
}

.question-card-heading {
  display: flex;
  flex-direction: column;
  min-width: 0;
}

.question-card-title {
  color: var(--text-primary);
  font-size: 0.82rem;
  font-weight: 700;
}

.question-card-subtitle {
  color: var(--text-muted);
  font-size: 0.7rem;
}

.question-list {
  display: flex;
  flex-direction: column;
  gap: var(--space-3);
}

.question-item {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
  padding: var(--space-3);
  border: 0.5px solid var(--border-hairline);
  border-radius: var(--radius-md);
  background: color-mix(in srgb, var(--bg-void) 24%, transparent);
}

.question-prompt {
  color: var(--text-primary);
  font-size: 0.8rem;
  font-weight: 600;
}

.question-options {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-2);
}

.question-option {
  display: inline-flex;
  align-items: center;
  gap: 7px;
  min-height: 30px;
  padding: 5px 10px;
  border: 0.5px solid var(--border-default);
  border-radius: var(--radius-md);
  background: var(--bg-elevated);
  color: var(--text-secondary);
  font-size: 0.75rem;
  transition: border-color var(--transition-fast), background var(--transition-fast), color var(--transition-fast), transform var(--transition-fast);
}

.question-option:hover:not(:disabled) {
  border-color: var(--border-strong);
  background: var(--bg-hover);
  color: var(--text-primary);
}

.question-option.selected {
  border-color: var(--accent-border);
  background: var(--accent-glow);
  color: var(--accent);
}

.question-option:disabled {
  cursor: default;
  opacity: 0.72;
}

.question-option-marker {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 14px;
  height: 14px;
  border: 0.5px solid currentColor;
  border-radius: 50%;
  color: inherit;
  opacity: 0.85;
}

.question-custom {
  display: flex;
  flex-direction: column;
  gap: 5px;
}

.question-custom-label {
  color: var(--text-muted);
  font-size: 0.7rem;
  font-weight: 600;
  text-transform: uppercase;
  letter-spacing: 0.04em;
}

.question-custom-input {
  width: 100%;
  min-height: 54px;
  padding: 8px 10px;
  border: 0.5px solid var(--border-default);
  border-radius: var(--radius-md);
  background: var(--bg-elevated);
  color: var(--text-primary);
  font-family: var(--font-sans);
  font-size: 0.78rem;
  line-height: 1.45;
  resize: vertical;
  outline: none;
  transition: border-color var(--transition-fast), box-shadow var(--transition-fast), background var(--transition-fast);
}

.question-custom-input:focus {
  border-color: var(--accent-border);
  box-shadow: 0 0 0 3px var(--accent-glow);
}

.question-custom-input:disabled {
  cursor: default;
  opacity: 0.72;
}

.question-actions {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  flex-wrap: wrap;
}

.question-submit {
  min-height: 30px;
  padding: 5px 14px;
  border: 0.5px solid var(--accent-border);
  border-radius: var(--radius-md);
  background: var(--accent);
  color: var(--bg-base);
  font-size: 0.75rem;
  font-weight: 700;
  transition: opacity var(--transition-fast), transform var(--transition-fast), background var(--transition-fast);
}

.question-submit:hover:not(:disabled) {
  transform: translateY(-1px);
}

.question-submit:disabled {
  cursor: not-allowed;
  opacity: 0.45;
}

.question-hint {
  color: var(--text-muted);
  font-size: 0.72rem;
}

.question-resolved {
  display: flex;
  align-items: center;
}

.question-badge {
  display: inline-flex;
  align-items: center;
  padding: 2px 8px;
  border-radius: 999px;
  font-size: 0.72rem;
  font-weight: 700;
}

.question-badge.answered {
  border: 0.5px solid var(--success-border);
  background: var(--success-bg);
  color: var(--accent-green);
}

.question-badge.expired,
.question-badge.failed {
  border: 0.5px solid var(--warning-border);
  background: var(--warning-bg);
  color: var(--accent-amber);
}

.question-error {
  color: var(--accent-rose);
  font-size: 0.72rem;
}
</style>
