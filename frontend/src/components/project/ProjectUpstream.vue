<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { projectApi, type UpstreamBranch, type UpstreamStatus } from '@/api/projects'
import { useActiveProjectStore } from '@/stores/activeProject'
import { fullTime, timeAgo } from '@/utils/time'

const POLL_INTERVAL_MS = 3000

// How the project's protected branches stand against its upstream, with a
// way to sync from it and to push each branch to it. Syncing fast-forwards
// a protected branch the project has not moved past; one that diverged is
// brought in through a merge request.
const store = useActiveProjectStore()

const status = ref<UpstreamStatus | null>(null)
const syncing = ref(false)
// The branch being pushed upstream, if any.
const pushing = ref('')
const error = ref('')
const notice = ref('')

let timer: ReturnType<typeof setInterval> | null = null
// Bumped for each load, so a slow response can't overwrite a newer one.
let seq = 0

const shown = computed(() => !!store.project?.upstreamUrl && store.ready)
const busy = computed(() => syncing.value || !!status.value?.syncing)

const syncLabel = computed(() => {
  if (busy.value) return 'Syncing…'
  const at = status.value?.fetchedAt
  return at ? `Synced ${timeAgo(at)}` : 'Not synced yet'
})

type BranchState = { text: string; tone: 'ok' | 'pending' | 'attention' | 'muted'; push: boolean; hint?: string }

function branchState(b: UpstreamBranch): BranchState {
  if (!b.sha) return { text: 'not in the project', tone: 'muted', push: false }
  if (!b.upstreamSha) {
    return status.value?.fetchedAt
      ? { text: 'not on the upstream', tone: 'pending', push: true }
      : { text: 'not synced', tone: 'muted', push: true }
  }
  if (b.ahead > 0 && b.behind > 0) {
    return {
      text: `diverged: ${b.ahead} here, ${b.behind} upstream`,
      tone: 'attention',
      push: false,
      hint: `Merge origin/${b.branch} into a branch in the workspace and open a merge request into ${b.branch}.`,
    }
  }
  if (b.ahead > 0) return { text: `${b.ahead} to push`, tone: 'pending', push: true }
  if (b.behind > 0) return { text: `${b.behind} behind; sync to fast-forward`, tone: 'pending', push: false }
  return { text: b.fastForwarded ? 'fast-forwarded' : 'in step', tone: 'ok', push: false }
}

async function load() {
  const id = store.projectId
  if (id === null || !shown.value) return
  const mine = ++seq
  try {
    const res = await projectApi.upstreamStatus(id)
    if (mine !== seq) return
    status.value = res
    error.value = res.error ? `Last sync failed: ${res.error}` : ''
  } catch (e) {
    if (mine !== seq) return
    error.value = e instanceof Error ? e.message : 'Failed to load the upstream'
  }
}

async function sync() {
  const id = store.projectId
  if (id === null) return
  syncing.value = true
  error.value = ''
  notice.value = ''
  try {
    const res = await projectApi.syncUpstream(id)
    seq++
    status.value = res
    const moved = res.branches.filter((b) => b.fastForwarded).map((b) => b.branch)
    notice.value = moved.length ? `Fast-forwarded ${moved.join(', ')} from the upstream.` : ''
  } catch (e) {
    error.value = e instanceof Error ? e.message : 'Sync failed'
  } finally {
    syncing.value = false
  }
}

async function push(branch: string) {
  const id = store.projectId
  if (id === null) return
  pushing.value = branch
  error.value = ''
  notice.value = ''
  try {
    const res = await projectApi.push(id, branch)
    notice.value = `Pushed ${res.branch} (${res.sha.slice(0, 8)}) upstream.`
    await load()
  } catch (e) {
    error.value = e instanceof Error ? e.message : 'Push failed'
  } finally {
    pushing.value = ''
  }
}

function syncTimer() {
  const polling = !!status.value?.syncing
  if (polling && timer === null) {
    timer = setInterval(() => {
      if (document.visibilityState === 'visible') void load()
    }, POLL_INTERVAL_MS)
  } else if (!polling && timer !== null) {
    clearInterval(timer)
    timer = null
  }
}

watch(() => status.value?.syncing, syncTimer)
watch(
  () => [store.projectId, shown.value, store.project?.upstreamUrl, store.protectedBranches.join(',')],
  () => {
    status.value = null
    notice.value = ''
    error.value = ''
    void load()
  },
)

onMounted(() => void load())
onUnmounted(() => {
  if (timer !== null) clearInterval(timer)
})
</script>

<template>
  <div v-if="shown" class="side-section">
    <div class="side-section-title">Upstream</div>
    <p class="upstream-sync" :title="fullTime(status?.fetchedAt)">{{ syncLabel }}</p>

    <ul v-if="status" class="upstream-branches">
      <li v-for="b in status.branches" :key="b.branch" class="upstream-branch">
        <div class="upstream-branch-row">
          <span class="mono upstream-branch-name">{{ b.branch }}</span>
          <span class="upstream-state" :class="`upstream-${branchState(b).tone}`">{{ branchState(b).text }}</span>
          <button
            v-if="branchState(b).push"
            class="btn btn-secondary btn-sm"
            :disabled="!!pushing || busy"
            :title="`Push ${b.branch} to the upstream`"
            @click="push(b.branch)"
          >
            {{ pushing === b.branch ? 'Pushing…' : 'Push' }}
          </button>
        </div>
        <p v-if="branchState(b).hint" class="upstream-hint">{{ branchState(b).hint }}</p>
      </li>
    </ul>

    <div class="upstream-actions">
      <button class="btn btn-secondary btn-sm" :disabled="busy" title="Fetch the upstream and fast-forward the protected branches" @click="sync">
        {{ busy ? 'Syncing…' : 'Sync from upstream' }}
      </button>
    </div>
    <p class="upstream-hint">In the workspace, <code>git fetch origin</code> syncs too, and the upstream's branches show as <code>origin/&lt;branch&gt;</code>.</p>
    <p v-if="notice" class="notice notice-success upstream-notice">{{ notice }}</p>
    <p v-if="error" class="notice notice-error upstream-notice">{{ error }}</p>
  </div>
</template>

<style scoped src="./shared.css"></style>
<style scoped src="./panel.css"></style>
<style scoped>
.upstream-sync {
  font-size: 0.75rem;
  color: var(--text-secondary);
}

.upstream-branches {
  display: flex;
  flex-direction: column;
  gap: var(--space-1);
  margin-top: var(--space-2);
  list-style: none;
  padding: 0;
}

.upstream-branch-row {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  min-height: 24px;
  font-size: 0.75rem;
}

.upstream-branch-name {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  color: var(--text-primary);
}

.upstream-state {
  flex: 1;
  min-width: 0;
  color: var(--text-secondary);
  overflow-wrap: anywhere;
}

.upstream-ok {
  color: var(--accent-green);
}

.upstream-pending {
  color: var(--accent-amber);
}

.upstream-attention {
  color: var(--accent-rose);
}

.upstream-muted {
  color: var(--text-muted);
}

.upstream-hint {
  margin-top: var(--space-1);
  font-size: 0.72rem;
  color: var(--text-muted);
  overflow-wrap: anywhere;
}

.upstream-hint code {
  font-family: var(--font-mono);
}

.upstream-actions {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-1);
  margin-top: var(--space-2);
}

.upstream-notice {
  margin: var(--space-2) 0 0;
}
</style>
