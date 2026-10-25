<script setup lang="ts">
import { onUnmounted, watch } from 'vue'
import { useActiveProjectStore } from '@/stores/activeProject'
import type { GitPushEvent } from '@/api/projects'

// Shows each `git push devpad` from the project workspace, so a push the IDE
// assistant made is as visible as one made with the Publish button.

const OK_MS = 8000
const REFUSED_MS = 15000

const store = useActiveProjectStore()
const timers = new Map<number, ReturnType<typeof setTimeout>>()

watch(
  () => store.gitPushes,
  (events) => {
    for (const event of events) {
      if (timers.has(event.seq)) continue
      timers.set(
        event.seq,
        setTimeout(() => dismiss(event.seq), event.ok ? OK_MS : REFUSED_MS),
      )
    }
  },
  { immediate: true },
)

function dismiss(seq: number) {
  const timer = timers.get(seq)
  if (timer) clearTimeout(timer)
  timers.delete(seq)
  store.dismissGitPush(seq)
}

function shortSha(event: GitPushEvent): string {
  return event.sha ? event.sha.slice(0, 7) : ''
}

onUnmounted(() => {
  for (const timer of timers.values()) clearTimeout(timer)
  timers.clear()
})
</script>

<template>
  <div v-if="store.gitPushes.length" class="git-push-toasts" role="status" aria-live="polite">
    <div
      v-for="event in store.gitPushes"
      :key="event.seq"
      class="git-push-toast"
      :class="event.ok ? 'git-push-toast--ok' : 'git-push-toast--refused'"
    >
      <div class="git-push-toast__body">
        <template v-if="event.ok">
          <span class="git-push-toast__title">Published <code>{{ event.branch }}</code></span>
          <span class="git-push-toast__detail">git push devpad · {{ shortSha(event) }}</span>
        </template>
        <template v-else>
          <span class="git-push-toast__title">Refused git push of <code>{{ event.branch }}</code></span>
          <span class="git-push-toast__detail">{{ event.error }}</span>
        </template>
      </div>
      <button class="git-push-toast__close" type="button" aria-label="Dismiss" @click="dismiss(event.seq)">×</button>
    </div>
  </div>
</template>

<style scoped>
.git-push-toasts {
  position: fixed;
  right: var(--space-4);
  bottom: calc(var(--space-8) + var(--space-2));
  z-index: 900;
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
  max-width: min(380px, calc(100vw - 2 * var(--space-4)));
}

.git-push-toast {
  display: flex;
  align-items: flex-start;
  gap: var(--space-2);
  padding: var(--space-2) var(--space-3);
  border: 1px solid var(--border-strong);
  border-radius: var(--radius-lg);
  background: var(--bg-elevated);
  box-shadow: var(--shadow-strong);
  font-size: 0.8rem;
}

.git-push-toast--ok {
  border-color: var(--success-border);
}

.git-push-toast--refused {
  border-color: var(--error-border);
}

.git-push-toast__body {
  display: flex;
  flex: 1;
  flex-direction: column;
  gap: 2px;
  min-width: 0;
}

.git-push-toast__title {
  color: var(--text-primary);
}

.git-push-toast--ok .git-push-toast__title {
  color: var(--success);
}

.git-push-toast--refused .git-push-toast__title {
  color: var(--error);
}

.git-push-toast__title code {
  font-family: var(--font-mono);
  color: var(--text-primary);
  overflow-wrap: anywhere;
}

.git-push-toast__detail {
  color: var(--text-secondary);
  overflow-wrap: anywhere;
}

.git-push-toast__close {
  border: none;
  background: none;
  color: var(--text-tertiary);
  cursor: pointer;
  font-size: 1rem;
  line-height: 1;
  padding: 0 2px;
  transition: color var(--transition-fast);
}

.git-push-toast__close:hover {
  color: var(--text-primary);
}
</style>
