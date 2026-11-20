<script setup lang="ts">
import { computed } from 'vue'
import type { TaskStatus } from '@/api/tasks'
import { TASK_STAGES, TASK_STATUS } from './taskStatus'

// A task's place on the to do → agent → review → done track, as a row of
// dots filled up to its stage. The current dot spins while an agent works
// on the task or reviews its merge request (reviewing), and pulses while the
// task waits for a sandbox; a cancelled task skipped the stages before it.
const props = withDefaults(defineProps<{ status: TaskStatus; label?: boolean; reviewing?: boolean }>(), {
  label: true,
  reviewing: false,
})

const info = computed(() => TASK_STATUS[props.status])
const reviewStage = computed(() => props.reviewing && info.value.step === TASK_STAGES.indexOf('Review'))
const motion = computed(() =>
  props.status === 'in_progress' || reviewStage.value ? 'working' : props.status === 'queued' ? 'waiting' : '',
)
const description = computed(
  () =>
    `${info.value.label}${reviewStage.value ? ', an agent is reviewing' : ''}, stage ${info.value.step + 1} of ${TASK_STAGES.length}`,
)

const skipped = computed(() => props.status === 'cancelled')

function dotState(i: number): string {
  if (i < info.value.step) return skipped.value ? 'skipped' : 'reached'
  if (i === info.value.step) return `current ${motion.value}`
  return ''
}
</script>

<template>
  <span class="task-progress" :class="`tone-${info.tone}`" role="img" :aria-label="description" :title="description">
    <span class="track" aria-hidden="true">
      <template v-for="(stage, i) in TASK_STAGES" :key="stage">
        <span v-if="i > 0" class="link" :class="{ reached: i <= info.step && !skipped, skipped: i <= info.step && skipped }" />
        <span class="dot" :class="dotState(i)" />
      </template>
    </span>
    <span v-if="label" class="label">{{ info.label }}</span>
  </span>
</template>

<style scoped>
.task-progress {
  --tone: var(--text-muted);
  --tone-ring: var(--bg-neutral);
  display: inline-flex;
  align-items: center;
  gap: var(--space-2);
  min-width: 0;
}

.tone-active {
  --tone: var(--accent-blue);
  --tone-ring: color-mix(in srgb, var(--accent-blue) 22%, transparent);
}

.tone-review {
  --tone: var(--accent-purple);
  --tone-ring: color-mix(in srgb, var(--accent-purple) 22%, transparent);
}

.tone-attention {
  --tone: var(--attention);
  --tone-ring: var(--attention-bg);
}

.tone-error {
  --tone: var(--error);
  --tone-ring: var(--error-bg);
}

.tone-success {
  --tone: var(--success);
  --tone-ring: var(--success-bg);
}

.track {
  display: inline-flex;
  align-items: center;
  flex-shrink: 0;
}

.dot {
  position: relative;
  width: 8px;
  height: 8px;
  border-radius: 50%;
  border: 1.5px solid var(--border-strong);
  background: transparent;
  flex-shrink: 0;
}

.dot.reached {
  border-color: var(--tone);
  background: var(--tone);
}

.dot.skipped {
  border-style: dashed;
  border-color: var(--text-dim);
}

.dot.current {
  width: 10px;
  height: 10px;
  border-color: var(--tone);
  background: var(--tone);
  box-shadow: 0 0 0 3px var(--tone-ring);
}

/* An agent at work: an arc circling the current dot. */
.dot.working {
  background: transparent;
}

.dot.working::after {
  content: '';
  position: absolute;
  inset: -5px;
  border-radius: 50%;
  border: 1.5px solid transparent;
  border-top-color: var(--tone);
  border-right-color: var(--tone);
  animation: spin 1s linear infinite;
}

.dot.waiting {
  animation: waiting 1.6s ease-in-out infinite;
}

@keyframes waiting {
  50% {
    opacity: 0.35;
  }
}

@media (prefers-reduced-motion: reduce) {
  .dot.working::after,
  .dot.waiting {
    animation: none;
  }
}

.link {
  width: 12px;
  height: 2px;
  margin: 0 2px;
  border-radius: 1px;
  background: var(--border-default);
}

.link.reached {
  background: var(--tone);
}

.link.skipped {
  background: none;
  border-top: 1.5px dashed var(--text-dim);
  height: 0;
}

.label {
  font-size: 0.75rem;
  color: var(--tone);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.tone-neutral .label {
  color: var(--text-secondary);
}
</style>
