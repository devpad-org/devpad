<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { gitApi, type GitStatus } from '@/api/git'
import { useFileWatcher } from '@/composables/useFileWatcher'

const props = defineProps<{
  workspaceId: number
}>()

const emit = defineEmits<{
  (e: 'open-git'): void
}>()

const status = ref<GitStatus | null>(null)
const loading = ref(false)
const error = ref('')

const { connect: connectWatcher, disconnect: disconnectWatcher, onEvent } = useFileWatcher(
  () => props.workspaceId
)
let refreshTimer: ReturnType<typeof setTimeout> | null = null

const stagedCount = computed(() => (status.value?.files ?? []).filter((file) => file.staged).length)
const unstagedCount = computed(() => (status.value?.files ?? []).filter((file) => !file.staged).length)
const changeCount = computed(() => status.value?.files.length ?? 0)

const branchLabel = computed(() => {
  if (error.value) return 'Git unavailable'
  if (!status.value) return loading.value ? 'Loading git...' : 'Git status'
  if (!status.value.isRepo) return 'No git repository'
  if (!status.value.branch) return 'Unknown branch'
  return status.value.branch === 'HEAD' ? 'Detached HEAD' : status.value.branch
})

const changeLabel = computed(() => {
  if (!status.value?.isRepo) return ''
  if (changeCount.value === 0) return 'Clean'

  const parts: string[] = []
  if (stagedCount.value > 0) parts.push(`${stagedCount.value} staged`)
  if (unstagedCount.value > 0) parts.push(`${unstagedCount.value} unstaged`)
  return parts.join(', ')
})

const syncLabel = computed(() => {
  if (!status.value?.isRepo) return ''

  const parts: string[] = []
  if (status.value.ahead > 0) parts.push(`↑ ${status.value.ahead}`)
  if (status.value.behind > 0) parts.push(`↓ ${status.value.behind}`)
  if (parts.length > 0) return parts.join('  ')
  return status.value.remotes.length > 0 ? 'Synced' : 'No remote'
})

const statusTone = computed(() => {
  if (error.value) return 'error'
  if (!status.value?.isRepo) return 'muted'
  return changeCount.value > 0 ? 'warning' : 'success'
})

function getErrorMessage(err: unknown): string {
  return err instanceof Error ? err.message : 'Failed to load git status'
}

async function refresh(showLoading = false) {
  if (!props.workspaceId) return

  const currentWorkspaceId = props.workspaceId
  if (showLoading) loading.value = true
  error.value = ''

  try {
    const nextStatus = await gitApi.status(currentWorkspaceId)
    if (props.workspaceId === currentWorkspaceId) {
      status.value = nextStatus
    }
  } catch (err) {
    if (props.workspaceId === currentWorkspaceId) {
      error.value = getErrorMessage(err)
    }
  } finally {
    if (props.workspaceId === currentWorkspaceId) {
      loading.value = false
    }
  }
}

function debouncedRefresh() {
  if (refreshTimer) clearTimeout(refreshTimer)
  refreshTimer = setTimeout(() => {
    void refresh()
  }, 1000)
}

function openGitPanel() {
  emit('open-git')
}

onEvent(() => {
  debouncedRefresh()
})

watch(
  () => props.workspaceId,
  (workspaceId) => {
    status.value = null
    error.value = ''
    if (workspaceId) {
      connectWatcher()
      void refresh(true)
    } else {
      disconnectWatcher()
    }
  }
)

onMounted(() => {
  if (!props.workspaceId) return
  connectWatcher()
  void refresh(true)
})

onUnmounted(() => {
  if (refreshTimer) clearTimeout(refreshTimer)
})
</script>

<template>
  <footer class="git-status-bar" role="contentinfo" aria-label="Git status">
    <button
      type="button"
      class="git-status-item git-status-branch"
      :class="`tone-${statusTone}`"
      :title="error || 'Open source control'"
      @click="openGitPanel"
    >
      <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
        <circle cx="18" cy="18" r="3" />
        <circle cx="6" cy="6" r="3" />
        <path d="M6 21V9a9 9 0 0 0 9 9" />
      </svg>
      <span>{{ branchLabel }}</span>
    </button>

    <button
      v-if="status?.isRepo"
      type="button"
      class="git-status-item"
      :class="`tone-${statusTone}`"
      title="Open source control changes"
      @click="openGitPanel"
    >
      <span class="status-dot" aria-hidden="true" />
      <span>{{ changeLabel }}</span>
    </button>

    <button
      v-if="status?.isRepo"
      type="button"
      class="git-status-item"
      title="Open source control sync status"
      @click="openGitPanel"
    >
      <span>{{ syncLabel }}</span>
    </button>

    <span v-if="error" class="git-status-message" role="status">{{ error }}</span>

    <button
      type="button"
      class="git-status-refresh"
      :disabled="loading || !workspaceId"
      title="Refresh git status"
      @click="refresh(true)"
    >
      <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" :class="{ spinning: loading }" aria-hidden="true">
        <path d="M21 12a9 9 0 1 1-9-9c2.52 0 4.93 1 6.74 2.74L21 8" />
        <path d="M21 3v5h-5" />
      </svg>
    </button>
  </footer>
</template>

<style scoped>
.git-status-bar {
  display: flex;
  align-items: center;
  gap: var(--space-1);
  min-height: 28px;
  padding: 0 var(--space-2);
  background: var(--bg-surface);
  border-top: 0.5px solid var(--border-default);
  color: var(--text-secondary);
  font-family: var(--font-mono);
  font-size: 0.75rem;
  flex-shrink: 0;
  overflow: hidden;
}

.git-status-item,
.git-status-refresh {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 6px;
  min-height: 22px;
  padding: 0 var(--space-2);
  border-radius: var(--radius-sm);
  color: var(--text-secondary);
  transition: color var(--transition-fast), background var(--transition-fast);
}

.git-status-item:hover,
.git-status-refresh:hover:not(:disabled) {
  background: var(--bg-hover);
  color: var(--text-primary);
}

.git-status-branch {
  max-width: 260px;
}

.git-status-branch span {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.git-status-message {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  color: var(--accent-rose);
}

.git-status-refresh {
  margin-left: auto;
  width: 24px;
  padding: 0;
  color: var(--text-muted);
}

.git-status-refresh:disabled {
  cursor: default;
  opacity: 0.5;
}

.status-dot {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: currentColor;
}

.tone-success {
  color: var(--accent-green);
}

.tone-warning {
  color: var(--accent-amber);
}

.tone-error {
  color: var(--accent-rose);
}

.tone-muted {
  color: var(--text-muted);
}

.spinning {
  animation: spin 0.8s linear infinite;
}

@keyframes spin {
  to {
    transform: rotate(360deg);
  }
}
</style>
