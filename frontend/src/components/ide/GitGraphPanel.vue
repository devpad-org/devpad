<script setup lang="ts">
import { computed, onUnmounted, ref, watch } from 'vue'
import {
  gitApi,
  type GitBranch,
  type GitCommit,
  type GitCommitFile,
  type GitFileDiff,
  type GitStatus,
} from '@/api/git'
import { useFileWatcher } from '@/composables/useFileWatcher'
import MonacoDiffViewer from './MonacoDiffViewer.vue'

const props = defineProps<{
  workspaceId: number
  active: boolean
  selectedCommitHash: string | null
  diffRequest: GitCommitDiffRequest | null
}>()

const emit = defineEmits<{
  (e: 'commitSelect', commit: GitCommit): void
  (e: 'closeDiff'): void
}>()

interface GitCommitDiffRequest {
  commit: GitCommit
  file: GitCommitFile
  requestId: number
}

const status = ref<GitStatus | null>(null)
const commits = ref<GitCommit[]>([])
const branches = ref<GitBranch[]>([])
const selectedHash = ref<string | null>(null)
const loading = ref(false)
const error = ref('')
const actionLoading = ref(false)
const showPullMenu = ref(false)
const diff = ref<GitFileDiff | null>(null)
const diffLoading = ref(false)
const diffError = ref('')
let diffLoadVersion = 0

const graphLaneSpacing = 18
const graphRailPadding = 10
const graphRowHeight = 50
const graphMiddle = graphRowHeight / 2

interface GraphPath {
  id: string
  d: string
  lane: number
}

interface CommitGraphRow {
  commit: GitCommit
  nodeLane: number
  nodeX: number
  branchNames: string[]
  topPaths: GraphPath[]
  bottomPaths: GraphPath[]
  connectorPaths: GraphPath[]
}

interface CommitGraphLayout {
  rows: CommitGraphRow[]
  width: number
  height: number
}

const { connect: connectWatcher, disconnect: disconnectWatcher, onEvent } = useFileWatcher(
  () => props.workspaceId
)

let refreshTimer: ReturnType<typeof setTimeout> | null = null
let loadVersion = 0

const currentBranch = computed(() => {
  if (!status.value?.isRepo) return 'No repository'
  if (!status.value.branch || status.value.branch === 'HEAD') return 'Detached HEAD'
  return status.value.branch
})

const hasWorkingChanges = computed(() => (status.value?.files.length ?? 0) > 0)
const stagedCount = computed(() => (status.value?.files ?? []).filter((f) => f.staged).length)
const unstagedCount = computed(() => (status.value?.files ?? []).filter((f) => !f.staged).length)
const showingDiff = computed(() => props.diffRequest !== null || diff.value !== null || diffLoading.value)
const diffTitle = computed(() => {
  const file = props.diffRequest?.file
  if (!file) return ''
  return file.oldPath && file.oldPath !== file.path
    ? `${file.oldPath} → ${file.path}`
    : file.path
})
const graphSubtitle = computed(() => {
  if (showingDiff.value) {
    return props.diffRequest?.commit.shortHash ?? ''
  }
  return currentBranch.value
})

async function doAction(action: string) {
  if (!props.workspaceId) return
  actionLoading.value = true
  error.value = ''
  showPullMenu.value = false
  try {
    const result = await gitApi.action(props.workspaceId, action)
    if (!result.success) {
      error.value = result.output || result.error || `${action} failed`
    } else {
      await refresh(false)
    }
  } catch (err) {
    error.value = getErrorMessage(err)
  } finally {
    actionLoading.value = false
  }
}


const graphLayout = computed(() => buildCommitGraph(commits.value))

const graphTimelineStyle = computed(() => ({
  '--graph-column-width': `${graphLayout.value.width}px`,
}))

function branchNamesForCommit(commit: GitCommit): string[] {
  return branches.value
    .filter((branch) => branch.hash === commit.shortHash || branch.hash === commit.hash)
    .map((branch) => branch.name)
}

function buildCommitGraph(commitList: GitCommit[]): CommitGraphLayout {
  const activeLanes: string[] = []
  const rows: CommitGraphRow[] = []
  let maxLaneCount = 1

  commitList.forEach((commit) => {
    const activeBefore = [...activeLanes]
    let nodeLane = activeLanes.indexOf(commit.hash)

    if (nodeLane === -1) {
      nodeLane = firstAvailableLane(activeLanes)
      activeLanes[nodeLane] = commit.hash
    }

    const laneKey = commit.shortHash || commit.hash
    const topPaths = activeBefore
      .map((hash, lane) => hash ? makePath(`top-${laneKey}-${lane}`, lane, 0, graphMiddle) : null)
      .filter((path): path is GraphPath => path !== null)

    const nextLanes = [...activeLanes]
    nextLanes[nodeLane] = ''
    const connectorTargetLanes = new Set<number>()

    const parents = normalizedParents(commit)
    const connectorPaths = parents.flatMap((parent, parentIndex) => {
      let targetLane = nextLanes.indexOf(parent)
      if (targetLane === -1) {
        targetLane = parentIndex === 0 && !nextLanes[nodeLane]
          ? nodeLane
          : firstAvailableLane(nextLanes, nodeLane + 1)
        nextLanes[targetLane] = parent
      }

      if (targetLane === nodeLane) return []
      if (parents.length > 1 && parentIndex > 0) {
        connectorTargetLanes.add(targetLane)
      }
      return [{
        id: `connector-${laneKey}-${parentIndex}-${targetLane}`,
        d: makeConnectorPath(nodeLane, targetLane),
        lane: targetLane,
      }]
    })

    trimTrailingEmptyLanes(nextLanes)
    activeLanes.splice(0, activeLanes.length, ...nextLanes)

    const bottomPaths = activeLanes
      .map((hash, lane) => hash && !connectorTargetLanes.has(lane)
        ? makePath(`bottom-${laneKey}-${lane}`, lane, graphMiddle, graphRowHeight)
        : null)
      .filter((path): path is GraphPath => path !== null)

    maxLaneCount = Math.max(maxLaneCount, activeBefore.length, activeLanes.length, nodeLane + 1)

    rows.push({
      commit,
      nodeLane,
      nodeX: laneX(nodeLane),
      branchNames: branchNamesForCommit(commit),
      topPaths,
      bottomPaths,
      connectorPaths,
    })
  })

  return {
    rows,
    width: Math.max(42, graphRailPadding * 2 + (maxLaneCount - 1) * graphLaneSpacing),
    height: graphRowHeight,
  }
}

function normalizedParents(commit: GitCommit): string[] {
  const seen = new Set<string>()
  return (commit.parents ?? []).filter((parent) => {
    if (!parent || seen.has(parent)) return false
    seen.add(parent)
    return true
  })
}

function firstAvailableLane(lanes: string[], start = 0): number {
  for (let lane = start; lane < lanes.length; lane += 1) {
    if (!lanes[lane]) return lane
  }
  for (let lane = 0; lane < start && lane < lanes.length; lane += 1) {
    if (!lanes[lane]) return lane
  }
  return lanes.length
}

function trimTrailingEmptyLanes(lanes: string[]) {
  while (lanes.length > 0 && !lanes[lanes.length - 1]) {
    lanes.pop()
  }
}

function laneX(lane: number): number {
  return graphRailPadding + lane * graphLaneSpacing
}

function makePath(id: string, lane: number, startY: number, endY: number): GraphPath {
  const x = laneX(lane)
  return { id, d: `M ${x} ${startY} V ${endY}`, lane }
}

function makeConnectorPath(fromLane: number, toLane: number): string {
  const fromX = laneX(fromLane)
  const toX = laneX(toLane)
  return `M ${fromX} ${graphMiddle} C ${fromX} ${graphMiddle + 10} ${toX} ${graphRowHeight - 10} ${toX} ${graphRowHeight}`
}

function laneColor(lane: number): string {
  return `var(--graph-lane-${lane % 6})`
}

function getErrorMessage(err: unknown): string {
  return err instanceof Error ? err.message : 'Failed to load git graph'
}

async function refresh(showLoading = true) {
  if (!props.workspaceId) return

  const currentWorkspaceId = props.workspaceId
  const currentLoad = ++loadVersion
  if (showLoading) loading.value = true
  error.value = ''

  try {
    const nextStatus = await gitApi.status(currentWorkspaceId)
    let nextCommits: GitCommit[] = []
    let nextBranches: GitBranch[] = []

    if (nextStatus.isRepo) {
      const [logResult, branchResult] = await Promise.all([
        gitApi.log(currentWorkspaceId, 200, { allBranches: true }),
        gitApi.branches(currentWorkspaceId),
      ])
      nextCommits = logResult.commits
      nextBranches = branchResult.branches
    }

    if (props.workspaceId !== currentWorkspaceId || currentLoad !== loadVersion) return

    status.value = nextStatus
    commits.value = nextCommits
    branches.value = nextBranches

    if (!nextCommits.some((commit) => commit.hash === selectedHash.value)) {
      selectedHash.value = nextCommits[0]?.hash ?? null
    }
  } catch (err) {
    if (props.workspaceId === currentWorkspaceId && currentLoad === loadVersion) {
      error.value = getErrorMessage(err)
    }
  } finally {
    if (props.workspaceId === currentWorkspaceId && currentLoad === loadVersion) {
      loading.value = false
    }
  }
}

function debouncedRefresh() {
  if (!props.active) return
  if (refreshTimer) clearTimeout(refreshTimer)
  refreshTimer = setTimeout(() => {
    void refresh(false)
  }, 1000)
}

function selectCommit(commit: GitCommit) {
  selectedHash.value = commit.hash
  emit('commitSelect', commit)
}

function closeCommitDiff() {
  diff.value = null
  diffError.value = ''
  diffLoading.value = false
  emit('closeDiff')
}

async function loadCommitDiff(request: GitCommitDiffRequest) {
  if (!props.workspaceId) return

  const currentLoad = ++diffLoadVersion
  selectedHash.value = request.commit.hash
  diff.value = null
  diffError.value = ''
  diffLoading.value = true

  try {
    const nextDiff = await gitApi.commitFileDiff(
      props.workspaceId,
      request.commit.hash,
      request.file.path,
      request.file.oldPath
    )
    if (currentLoad !== diffLoadVersion) return
    diff.value = nextDiff
  } catch (err) {
    if (currentLoad === diffLoadVersion) {
      diffError.value = getErrorMessage(err)
    }
  } finally {
    if (currentLoad === diffLoadVersion) {
      diffLoading.value = false
    }
  }
}

function formatTime(timestamp: string): string {
  const date = new Date(Number.parseInt(timestamp, 10) * 1000)
  if (Number.isNaN(date.getTime())) return 'unknown'

  const diff = Date.now() - date.getTime()
  if (diff < 60_000) return 'just now'
  if (diff < 3_600_000) return `${Math.floor(diff / 60_000)}m ago`
  if (diff < 86_400_000) return `${Math.floor(diff / 3_600_000)}h ago`
  if (diff < 604_800_000) return `${Math.floor(diff / 86_400_000)}d ago`
  return date.toLocaleDateString('en-US', { month: 'short', day: 'numeric' })
}

onEvent(() => {
  debouncedRefresh()
})

watch(
  () => [props.workspaceId, props.active] as const,
  ([workspaceId, active]) => {
    if (refreshTimer) clearTimeout(refreshTimer)

    if (!workspaceId || !active) {
      disconnectWatcher()
      return
    }

    connectWatcher()
    void refresh()
  },
  { immediate: true }
)

watch(
  () => props.selectedCommitHash,
  (hash) => {
    if (hash) selectedHash.value = hash
  }
)

watch(
  () => props.diffRequest?.requestId ?? null,
  () => {
    if (!props.diffRequest) {
      diffLoadVersion += 1
      diff.value = null
      diffError.value = ''
      diffLoading.value = false
      return
    }
    void loadCommitDiff(props.diffRequest)
  },
  { immediate: true }
)

onUnmounted(() => {
  if (refreshTimer) clearTimeout(refreshTimer)
  disconnectWatcher()
})
</script>

<template>
  <section class="git-graph-panel" aria-label="Git graph">
    <header class="graph-header">
      <div class="graph-title">
        <span class="graph-title-icon">
          <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
            <circle cx="18" cy="18" r="3" />
            <circle cx="6" cy="6" r="3" />
            <path d="M6 21V9a9 9 0 0 0 9 9" />
          </svg>
        </span>
        <span class="graph-title-text">{{ showingDiff ? 'Commit Diff' : 'Git Graph' }}</span>
        <span class="graph-branch">{{ graphSubtitle }}</span>
      </div>

      <div v-if="showingDiff" class="graph-actions">
        <span class="diff-file-title">{{ diffTitle }}</span>
        <button class="graph-action-btn" type="button" title="Close diff" @click="closeCommitDiff">
          <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
            <path d="M18 6 6 18" /><path d="m6 6 12 12" />
          </svg>
          Close
        </button>
      </div>

      <div v-else class="graph-actions">
        <button
          class="graph-action-btn"
          type="button"
          title="Push"
          :disabled="actionLoading || loading || !status?.isRepo"
          @click="doAction('push')"
        >
          <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
            <path d="M12 19V5M5 12l7-7 7 7" />
          </svg>
          Push
          <span v-if="status?.ahead" class="action-badge">{{ status.ahead }}</span>
        </button>

        <div class="pull-split-wrapper">
          <div class="pull-dropdown-container">
            <button
              class="graph-action-btn pull-main-btn"
              type="button"
              title="Pull"
              :disabled="actionLoading || loading || !status?.isRepo"
              @click="doAction('pull')"
            >
              <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
                <path d="M12 5v14M5 12l7 7 7-7" />
              </svg>
              Pull
              <span v-if="status?.behind" class="action-badge">{{ status.behind }}</span>
            </button>
            <button
              class="graph-action-btn pull-chevron-btn"
              type="button"
              title="Pull options"
              :disabled="actionLoading || loading || !status?.isRepo"
              @click.stop="showPullMenu = !showPullMenu"
            >
              <svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
                <path d="M6 9l6 6 6-6" />
              </svg>
            </button>

            <div v-if="showPullMenu" class="dropdown-overlay" @click="showPullMenu = false" />
            <div v-if="showPullMenu" class="pull-menu" role="menu">
              <button class="pull-menu-item" role="menuitem" type="button" @click="doAction('fetch')">
                <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
                  <path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4" />
                  <polyline points="7 10 12 15 17 10" />
                  <line x1="12" y1="15" x2="12" y2="3" />
                </svg>
                Fetch
              </button>
            </div>
          </div>
        </div>
      </div>

      <button class="refresh-btn" type="button" :disabled="loading || actionLoading || !workspaceId" @click="refresh()">
        <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" :class="{ spinning: loading || actionLoading }" aria-hidden="true">
          <path d="M21 12a9 9 0 1 1-9-9c2.52 0 4.93 1 6.74 2.74L21 8" />
          <path d="M21 3v5h-5" />
        </svg>
      </button>
    </header>

    <div v-if="showingDiff" class="diff-shell">
      <div v-if="diffError" class="graph-state graph-state--error" role="status">{{ diffError }}</div>
      <div v-else-if="diffLoading || !diff" class="graph-state" role="status">
        <span class="graph-loader" />
        Loading diff…
      </div>
      <MonacoDiffViewer
        v-else
        :old-content="diff.oldContent"
        :new-content="diff.newContent"
        :old-file-name="diff.oldFileName"
        :new-file-name="diff.newFileName"
      />
    </div>

    <div v-else-if="error" class="graph-state graph-state--error" role="status">{{ error }}</div>

    <div v-else-if="loading && commits.length === 0" class="graph-state" role="status">
      <span class="graph-loader" />
      Painting git graph…
    </div>

    <div v-else-if="status && !status.isRepo" class="graph-empty">
      <div class="empty-orb" aria-hidden="true" />
      <h3>No repository initialized</h3>
      <p>Use the Source Control sidebar to initialize or clone a repository, then the graph will bloom here.</p>
    </div>

    <div v-else-if="commits.length === 0" class="graph-empty">
      <div class="empty-orb" aria-hidden="true" />
      <h3>No commits yet</h3>
      <p>Create your first commit and Devpad will render its history here.</p>
    </div>

    <div v-else class="graph-shell">
      <div class="graph-timeline" :style="graphTimelineStyle" aria-label="Commit timeline">
        <div
          v-if="hasWorkingChanges"
          class="commit-row commit-row--ghost"
          aria-label="Uncommitted changes"
        >
          <span class="graph-rail" aria-hidden="true">
            <svg class="rail-svg" :viewBox="`0 0 ${graphLayout.width} ${graphLayout.height}`" focusable="false">
              <path
                class="rail-path rail-path--ghost"
                :d="`M ${graphRailPadding} ${graphMiddle} V ${graphRowHeight}`"
                :style="{ stroke: laneColor(0) }"
              />
              <circle
                class="commit-node commit-node--ghost"
                :cx="graphRailPadding"
                :cy="graphMiddle"
                r="5"
                :style="{ stroke: laneColor(0) }"
              />
            </svg>
          </span>
          <span class="commit-card commit-card--ghost">
            <span class="commit-topline">
              <code>working tree</code>
              <span>now</span>
            </span>
            <strong>Uncommitted changes</strong>
            <span class="commit-meta">
              <span v-if="stagedCount > 0" class="change-pill change-pill--staged">{{ stagedCount }} staged</span>
              <span v-if="unstagedCount > 0" class="change-pill change-pill--unstaged">{{ unstagedCount }} unstaged</span>
            </span>
          </span>
        </div>

        <button
          v-for="row in graphLayout.rows"
          :key="row.commit.hash"
          type="button"
          class="commit-row"
          :class="{ selected: row.commit.hash === selectedHash }"
          @click="selectCommit(row.commit)"
        >
          <span class="graph-rail" aria-hidden="true">
            <svg class="rail-svg" :viewBox="`0 0 ${graphLayout.width} ${graphLayout.height}`" focusable="false">
              <path
                v-for="path in row.topPaths"
                :key="path.id"
                class="rail-path"
                :d="path.d"
                :style="{ stroke: laneColor(path.lane) }"
              />
              <path
                v-for="path in row.bottomPaths"
                :key="path.id"
                class="rail-path"
                :d="path.d"
                :style="{ stroke: laneColor(path.lane) }"
              />
              <path
                v-for="path in row.connectorPaths"
                :key="path.id"
                class="rail-path rail-path--connector"
                :d="path.d"
                :style="{ stroke: laneColor(path.lane) }"
              />
              <circle
                class="commit-node"
                :cx="row.nodeX"
                :cy="graphMiddle"
                r="5"
                :style="{ fill: laneColor(row.nodeLane) }"
              />
            </svg>
          </span>

          <span class="commit-card">
            <span class="commit-topline">
              <code>{{ row.commit.shortHash }}</code>
              <span>{{ formatTime(row.commit.timestamp) }}</span>
            </span>
            <strong>{{ row.commit.message }}</strong>
            <span class="commit-meta">
              <span>{{ row.commit.author }}</span>
              <span v-for="branch in row.branchNames" :key="branch" class="branch-pill">
                {{ branch }}
              </span>
            </span>
          </span>
        </button>
      </div>
    </div>
  </section>
</template>

<style src="../../assets/styles/git-graph.css" scoped></style>
<style src="../../assets/styles/git-graph-timeline.css" scoped></style>
