<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { projectApi, type UpstreamBranch, type UpstreamStatus } from '@/api/projects'
import { useActiveProjectStore } from '@/stores/activeProject'
import { fullTime, timeAgo } from '@/utils/time'
import PipelineStatusIcon from '@/components/pipeline/PipelineStatusIcon.vue'
import { PIPELINE_STATUS, type StatusLook } from '@/components/pipeline/pipelineFormat'
import ProjectBranchesModal from './ProjectBranchesModal.vue'
import ProjectCard from './ProjectCard.vue'
import type { CardStatus } from './card'

const POLL_INTERVAL_MS = 3000

// The project's protected branches and, with an upstream, how each stands
// against it, with a way to sync from it and to push each branch to it.
// Syncing fast-forwards a protected branch the project has not moved past;
// one that diverged is brought in through a merge request.
const store = useActiveProjectStore()

const showBranches = ref(false)

const status = ref<UpstreamStatus | null>(null)
const syncing = ref(false)
// The branch being pushed upstream, if any.
const pushing = ref('')
const error = ref('')
const notice = ref('')

let timer: ReturnType<typeof setInterval> | null = null
// Bumped for each load, so a slow response can't overwrite a newer one.
let seq = 0

// Whether the upstream is followed: the project has one and its repo is ready.
const shown = computed(() => !!store.project?.upstreamUrl && store.ready)
const busy = computed(() => syncing.value || !!status.value?.syncing)

const cardStatus = computed<CardStatus>(() => {
  if (!store.project?.upstreamUrl) return { label: 'Local only', tone: 'neutral' }
  if (!shown.value) return { label: '', tone: 'neutral' }
  if (busy.value) return { label: 'Syncing…', tone: 'busy' }
  if (status.value?.error) return { label: 'Sync failed', tone: 'error' }
  const at = status.value?.fetchedAt
  if (!at) return { label: 'Not synced yet', tone: 'neutral' }
  const attention = status.value?.branches.some((b) => branchState(b).tone === 'attention')
  return { label: `Synced ${timeAgo(at)}`, tone: attention ? 'warn' : 'ok' }
})

// Each protected branch, the default first, with how it stands against the
// upstream once that is known.
const rows = computed(() => {
  const byName = new Map((status.value?.branches ?? []).map((b) => [b.branch, b]))
  return store.protectedBranches.map((name, i) => {
    const upstream = byName.get(name)
    const runsPipelines = store.project?.pipelineBranches?.includes(name) ?? false
    return { name, isDefault: i === 0, upstream, state: upstream ? branchState(upstream) : null, runsPipelines, pipeline: pipelineBadge(name) }
  })
})

// A ticked branch's latest pipeline that counts. One of a commit the branch
// has moved on from, by the tip the server read with it, shows as out of
// date. When nothing of its current commit and jobs counts but a pipeline of
// them was cancelled or couldn't run, the server says so, and why.
function pipelineBadge(name: string) {
  const settled = store.pipelineBranchStandings[name]?.settled
  if (settled) {
    const look: StatusLook = settled.pipeline.status === 'cancelled' ? PIPELINE_STATUS.cancelled : { ...PIPELINE_STATUS.error, label: "Couldn't run" }
    return { look, title: settled.reason }
  }
  const p = store.branchPipelines.get(name)
  if (!p) return null
  const tip = store.pipelineBranchTips[name]
  const status = PIPELINE_STATUS[p.status]
  if (tip && tip !== p.headSha) {
    return { look: { label: 'Out of date', tone: 'neutral', icon: 'clock' } as StatusLook, title: `Latest pipeline #${p.id} ${status.label.toLowerCase()} on ${p.headSha.slice(0, 8)}, an older commit` }
  }
  return { look: status, title: `Latest pipeline #${p.id}: ${status.label.toLowerCase()} on ${p.headSha.slice(0, 8)}, ${timeAgo(p.createdAt)}` }
}

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
  <ProjectCard
    title="Protected branches"
    doc="protectedBranches"
    doc-label="Protected branches"
    :status="cardStatus.label"
    :tone="cardStatus.tone"
  >
    <template #icon>
      <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round">
        <line x1="6" x2="6" y1="3" y2="15" />
        <circle cx="18" cy="6" r="3" />
        <circle cx="6" cy="18" r="3" />
        <path d="M18 9a9 9 0 0 1-9 9" />
      </svg>
    </template>

    <p class="card-lead">
      These change only through merge requests, and are the branches merge requests target{{ store.project?.upstreamUrl ? ' and devpad pushes upstream' : '' }}.
    </p>

    <ul class="branch-list">
      <li v-for="row in rows" :key="row.name" class="branch-item">
        <div class="branch-row">
          <svg class="branch-lock" width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
            <rect width="18" height="11" x="3" y="11" rx="2" ry="2" />
            <path d="M7 11V7a5 5 0 0 1 10 0v4" />
          </svg>
          <span class="mono branch-name" :title="row.name">{{ row.name }}</span>
          <span v-if="row.isDefault" class="branch-tag">default</span>
          <span
            v-if="row.runsPipelines"
            class="branch-pipeline"
            :title="row.pipeline ? row.pipeline.title : 'No pipeline has run on this branch yet'"
          >
            <PipelineStatusIcon v-if="row.pipeline" :look="row.pipeline.look" :size="16" :title="row.pipeline.title" />
            <span class="branch-pipeline-text">{{ row.pipeline ? row.pipeline.look.label : 'no pipeline yet' }}</span>
          </span>
          <span v-if="row.state" class="branch-state" :class="`state-${row.state.tone}`">{{ row.state.text }}</span>
          <button
            v-if="row.state?.push"
            class="btn btn-secondary btn-sm branch-push"
            :disabled="!!pushing || busy"
            :title="`Push ${row.name} to the upstream`"
            @click="push(row.name)"
          >
            {{ pushing === row.name ? 'Pushing…' : 'Push' }}
          </button>
        </div>
        <p v-if="row.state?.hint" class="card-note branch-hint">{{ row.state.hint }}</p>
      </li>
    </ul>

    <template v-if="store.project?.upstreamUrl">
      <p class="card-note">
        Upstream <span class="mono" :title="fullTime(status?.fetchedAt)">{{ store.project.upstreamUrl }}</span>.
        In the workspace, <code>git fetch origin</code> syncs too, and the upstream's branches show as <code>origin/&lt;branch&gt;</code>.
      </p>
    </template>
    <p v-else class="card-note">The project has no upstream: its branches live only in devpad. You can add one in the project's settings.</p>

    <p v-if="notice" class="notice notice-success card-message">{{ notice }}</p>
    <p v-if="error" class="notice notice-error card-message">{{ error }}</p>

    <template #actions>
      <button v-if="store.ready" class="btn btn-secondary btn-sm" @click="showBranches = true">Manage branches</button>
      <button
        v-if="shown"
        class="btn btn-secondary btn-sm"
        :disabled="busy"
        title="Fetch the upstream and fast-forward the protected branches"
        @click="sync"
      >
        {{ busy ? 'Syncing…' : 'Sync from upstream' }}
      </button>
    </template>

    <ProjectBranchesModal :show="showBranches" @close="showBranches = false" />
  </ProjectCard>
</template>

<style scoped src="./shared.css"></style>
<style scoped src="./card.css"></style>
<style scoped>
.branch-list {
  display: flex;
  flex-direction: column;
  margin: 0;
  padding: 0;
  list-style: none;
  border: 0.5px solid var(--border-hairline);
  border-radius: var(--radius-md);
  overflow: hidden;
}

.branch-item + .branch-item {
  border-top: 0.5px solid var(--border-hairline);
}

.branch-item {
  padding: var(--space-2) var(--space-3);
}

.branch-row {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  min-height: 26px;
}

.branch-lock {
  flex-shrink: 0;
  color: var(--text-tertiary);
}

.branch-name {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  color: var(--text-primary);
  font-size: 0.78rem;
}

.branch-tag {
  flex-shrink: 0;
  padding: 0 6px;
  border-radius: var(--radius-sm);
  background: var(--accent-glow);
  color: var(--accent);
  font-size: 0.68rem;
  font-weight: 500;
}

.branch-pipeline {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  flex-shrink: 0;
  font-size: 0.7rem;
  color: var(--text-tertiary);
}

.branch-state {
  flex: 1;
  min-width: 0;
  text-align: right;
  font-size: 0.75rem;
  color: var(--text-secondary);
}

.branch-push {
  flex-shrink: 0;
}

.state-ok {
  color: var(--success);
}

.state-pending {
  color: var(--warning);
}

.state-attention {
  color: var(--error);
}

.state-muted {
  color: var(--text-tertiary);
}

.branch-hint {
  margin: var(--space-1) 0 0 20px;
}
</style>
