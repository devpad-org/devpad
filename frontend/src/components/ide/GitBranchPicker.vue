<script setup lang="ts">
import { computed, inject, nextTick, onUnmounted, ref, watch } from 'vue'
import { gitApi, type GitActionResult, type GitBranch } from '@/api/git'
import { projectApi } from '@/api/projects'
import { useGitSshHostKey } from '@/composables/useGitSshHostKey'
import { useActiveProjectStore } from '@/stores/activeProject'
import { branchActionError } from '@/components/project/branchErrors'
import ConfirmModal from '@/components/ConfirmModal.vue'
import GitSshHostKeyModal from './GitSshHostKeyModal.vue'
import { confirmDiscardKey, type BranchChange } from './ideShell'
import type { GitNotice } from './gitNotice'

// The current branch, and a palette to switch to, create, merge or delete
// branches. In a project workspace it also lists the project's branches,
// which stand in for the raw refs of the devpad remote.
const props = defineProps<{
  workspaceId: number
  // The checked-out branch; '' or 'HEAD' when detached.
  current: string
  ahead: number
  behind: number
  projectMode: boolean
}>()

const emit = defineEmits<{
  changed: [kind: BranchChange]
  notify: [notice: GitNotice]
}>()

const DEVPAD_REMOTE = 'devpad'

type Item =
  | { kind: 'create'; key: string; name: string }
  | { kind: 'local'; key: string; branch: GitBranch; inProject: boolean }
  | { kind: 'project'; key: string; name: string; agent: boolean }
  | { kind: 'remote'; key: string; branch: GitBranch }

const store = useActiveProjectStore()
const confirmDiscard = inject(confirmDiscardKey, () => Promise.resolve(true))
const {
  hostKey: sshHostKey,
  accepting: acceptingSshHostKey,
  error: sshHostKeyError,
  promptFromActionResult,
  promptFromError,
  accept: acceptSshHostKey,
  cancel: cancelSshHostKey,
} = useGitSshHostKey(() => props.workspaceId)

const root = ref<HTMLElement | null>(null)
const input = ref<HTMLInputElement | null>(null)
const list = ref<HTMLElement | null>(null)
const open = ref(false)
const creating = ref(false)
const query = ref('')
const activeIndex = ref(0)
const branches = ref<GitBranch[]>([])
const loading = ref(false)
const busyKey = ref<string | null>(null)
const deleteTarget = ref<GitBranch | null>(null)
const deleting = ref(false)

const detached = computed(() => props.current === '' || props.current === 'HEAD')
const label = computed(() => (detached.value ? 'Detached HEAD' : props.current))
const needle = computed(() => query.value.trim().toLowerCase())

function matches(name: string): boolean {
  return !needle.value || name.toLowerCase().includes(needle.value)
}

const localBranches = computed(() => branches.value.filter((b) => !b.remote))
const localNames = computed(() => new Set(localBranches.value.map((b) => b.name)))
const agentBranches = computed(() => new Set(store.sandboxes.map((s) => s.branch)))

const localItems = computed<Item[]>(() =>
  localBranches.value
    .filter((b) => matches(b.name))
    .sort((a, b) => Number(b.current) - Number(a.current))
    .map((b) => ({
      kind: 'local',
      key: `local:${b.name}`,
      branch: b,
      inProject: props.projectMode && store.branchNames.has(b.name),
    })),
)

const projectItems = computed<Item[]>(() => {
  if (!props.projectMode) return []
  return store.branches
    .filter((b) => !localNames.value.has(b.name) && matches(b.name))
    .map((b) => ({ kind: 'project', key: `project:${b.name}`, name: b.name, agent: agentBranches.value.has(b.name) }))
})

const remoteGroups = computed(() => {
  const groups = new Map<string, Item[]>()
  for (const b of branches.value) {
    if (!b.remote) continue
    const remote = b.remoteName || b.name.split('/')[0] || 'remote'
    if (props.projectMode && remote === DEVPAD_REMOTE) continue
    if (!matches(b.name)) continue
    const group = groups.get(remote) ?? []
    group.push({ kind: 'remote', key: `remote:${b.name}`, branch: b })
    groups.set(remote, group)
  }
  return Array.from(groups, ([remote, items]) => ({ remote, items }))
})

const createItem = computed<Extract<Item, { kind: 'create' }> | null>(() => {
  const name = query.value.trim()
  if (!name || localNames.value.has(name)) return null
  return { kind: 'create', key: 'create', name }
})

const sections = computed(() => {
  const out: { title: string; tag?: string; items: Item[] }[] = []
  if (localItems.value.length) out.push({ title: 'Local', items: localItems.value })
  if (projectItems.value.length) out.push({ title: 'Project', items: projectItems.value })
  for (const group of remoteGroups.value) out.push({ title: 'Remote', tag: group.remote, items: group.items })
  return out
})

// Every row the keyboard moves through, in the order shown.
const items = computed<Item[]>(() => [
  ...(createItem.value ? [createItem.value] : []),
  ...sections.value.flatMap((s) => s.items),
])
const activeKey = computed(() => items.value[activeIndex.value]?.key ?? null)

const deleteMessage = computed(() => {
  const b = deleteTarget.value
  if (!b) return ''
  return b.remote
    ? `Delete '${remoteBranchName(b)}' from the '${b.remoteName || 'remote'}' remote? This cannot be undone.`
    : `Delete the local branch '${b.name}'? Git refuses if it has commits that aren't merged anywhere.`
})

watch(query, () => {
  activeIndex.value = 0
})

watch(activeKey, () => {
  void nextTick(() => {
    list.value?.querySelector('[data-active="true"]')?.scrollIntoView({ block: 'nearest' })
  })
})

async function load() {
  loading.value = true
  try {
    const [res] = await Promise.all([
      gitApi.branches(props.workspaceId),
      props.projectMode ? store.refreshBranches().catch(() => undefined) : undefined,
    ])
    branches.value = res.branches
  } catch (e) {
    emit('notify', { tone: 'error', text: e instanceof Error ? e.message : 'Failed to load branches' })
  } finally {
    loading.value = false
  }
}

function show(mode: 'switch' | 'create' = 'switch') {
  creating.value = mode === 'create'
  query.value = ''
  activeIndex.value = 0
  if (!open.value) {
    open.value = true
    document.addEventListener('pointerdown', onPointerDown, true)
    void load()
  }
  void nextTick(() => input.value?.focus())
}

function close() {
  if (!open.value) return
  open.value = false
  creating.value = false
  document.removeEventListener('pointerdown', onPointerDown, true)
}

function toggle() {
  if (open.value) close()
  else show()
}

function onPointerDown(e: PointerEvent) {
  // A confirmation dialog the picker opened sits outside it.
  if (deleteTarget.value || sshHostKey.value) return
  if (root.value && !root.value.contains(e.target as Node)) close()
}

function onKeydown(e: KeyboardEvent) {
  const count = items.value.length
  if (e.key === 'ArrowDown') {
    e.preventDefault()
    if (count) activeIndex.value = (activeIndex.value + 1) % count
  } else if (e.key === 'ArrowUp') {
    e.preventDefault()
    if (count) activeIndex.value = (activeIndex.value - 1 + count) % count
  } else if (e.key === 'Enter') {
    e.preventDefault()
    const item = items.value[activeIndex.value]
    if (item) void choose(item)
  } else if (e.key === 'Escape') {
    e.preventDefault()
    close()
  }
}

function remoteBranchName(b: GitBranch): string {
  return b.remoteBranch || b.name.split('/').slice(1).join('/') || b.name
}

function actionPayload(b: GitBranch): { branch: string; remote?: string } {
  if (!b.remote) return { branch: b.name }
  return { branch: remoteBranchName(b), remote: b.remoteName || b.name.split('/')[0] }
}

function itemName(item: Item): string {
  switch (item.kind) {
    case 'create':
    case 'project':
      return item.name
    default:
      return item.branch.name
  }
}

function isCurrent(item: Item): boolean {
  return item.kind === 'local' && item.branch.current
}

function gitFailure(result: GitActionResult, text: string): GitNotice {
  return { tone: 'error', text, detail: result.output || result.error || undefined }
}

// Runs one branch action at a time, keyed so its row shows a spinner.
async function run(key: string, action: () => Promise<void>) {
  if (busyKey.value) return
  busyKey.value = key
  try {
    await action()
  } finally {
    busyKey.value = null
  }
}

async function choose(item: Item) {
  if (busyKey.value || isCurrent(item)) {
    if (isCurrent(item)) close()
    return
  }
  if (item.kind === 'create') return createBranch(item.name)
  if (item.kind === 'project') return checkoutProjectBranch(item)
  return checkoutBranch(item.branch, item.key)
}

const SWITCH_WARNING = 'Switching branches reloads your open files from the new branch, so their unsaved changes will be lost.'
const MERGE_WARNING = 'Merging reloads your open files from disk, so their unsaved changes will be lost.'

async function checkoutBranch(b: GitBranch, key: string) {
  if (!(await confirmDiscard(SWITCH_WARNING))) return
  await run(key, async () => {
    try {
      const result = await gitApi.action(props.workspaceId, 'checkout', actionPayload(b))
      if (promptFromActionResult(result, () => checkoutBranch(b, key))) return
      if (!result.success) {
        emit('notify', gitFailure(result, `Couldn't switch to ${b.remote ? remoteBranchName(b) : b.name}`))
        return
      }
      emit('notify', { tone: 'success', text: `Switched to ${b.remote ? remoteBranchName(b) : b.name}` })
      close()
      emit('changed', 'checkout')
    } catch (e) {
      if (promptFromError(e, () => checkoutBranch(b, key))) return
      emit('notify', { tone: 'error', text: e instanceof Error ? e.message : 'Failed to switch branches' })
    }
  })
}

async function checkoutProjectBranch(item: Extract<Item, { kind: 'project' }>) {
  if (store.projectId === null) return
  const projectId = store.projectId
  if (!(await confirmDiscard(SWITCH_WARNING))) return
  await run(item.key, async () => {
    try {
      const res = await projectApi.checkoutBranch(projectId, item.name)
      emit('notify', { tone: 'success', text: `Switched to ${res.branch}` })
      close()
      emit('changed', 'checkout')
    } catch (e) {
      emit('notify', { tone: 'error', text: branchActionError(e, 'Failed to switch branches') })
    }
  })
}

async function createBranch(name: string) {
  await run('create', async () => {
    try {
      const result = await gitApi.action(props.workspaceId, 'checkout-new', { branch: name })
      if (!result.success) {
        emit('notify', gitFailure(result, `Couldn't create ${name}`))
        return
      }
      emit('notify', {
        tone: 'success',
        text: detached.value ? `Created ${name}` : `Created ${name} from ${props.current}`,
      })
      close()
      emit('changed', 'head')
    } catch (e) {
      emit('notify', { tone: 'error', text: e instanceof Error ? e.message : 'Failed to create the branch' })
    }
  })
}

async function mergeBranch(b: GitBranch, key: string) {
  if (detached.value) return
  if (!(await confirmDiscard(MERGE_WARNING))) return
  const source = b.name
  await run(`merge:${key}`, async () => {
    try {
      const result = await gitApi.action(props.workspaceId, 'merge', actionPayload(b))
      if (promptFromActionResult(result, () => mergeBranch(b, key))) return
      if (!result.success) {
        emit('notify', gitFailure(result, `Couldn't merge ${source} into ${props.current}`))
        emit('changed', 'checkout')
        return
      }
      emit('notify', /up to date/i.test(result.output)
        ? { tone: 'info', text: `${props.current} already has everything in ${source}` }
        : { tone: 'success', text: `Merged ${source} into ${props.current}` })
      close()
      emit('changed', 'checkout')
    } catch (e) {
      if (promptFromError(e, () => mergeBranch(b, key))) return
      emit('notify', { tone: 'error', text: e instanceof Error ? e.message : 'Failed to merge' })
    }
  })
}

async function deleteBranch(b: GitBranch): Promise<boolean> {
  try {
    const result = await gitApi.action(props.workspaceId, 'delete-branch', actionPayload(b))
    const retry = async () => {
      if (await deleteBranch(b)) deleteTarget.value = null
    }
    if (promptFromActionResult(result, retry)) return false
    if (!result.success) {
      emit('notify', gitFailure(result, `Couldn't delete ${b.name}`))
      return false
    }
    emit('notify', { tone: 'success', text: `Deleted ${b.name}` })
    await load()
    return true
  } catch (e) {
    if (promptFromError(e, async () => {
      if (await deleteBranch(b)) deleteTarget.value = null
    })) return false
    emit('notify', { tone: 'error', text: e instanceof Error ? e.message : 'Failed to delete the branch' })
    return false
  }
}

async function confirmDelete() {
  const b = deleteTarget.value
  if (!b || deleting.value) return
  deleting.value = true
  const ok = await deleteBranch(b)
  deleting.value = false
  // A failure is reported as a notice, so the dialog closes either way.
  if (ok || !sshHostKey.value) deleteTarget.value = null
}

function cancelDelete() {
  if (!deleting.value) deleteTarget.value = null
}

onUnmounted(() => {
  document.removeEventListener('pointerdown', onPointerDown, true)
})

defineExpose({ show })
</script>

<template>
  <div ref="root" class="branch-picker" :class="{ open }">
    <button
      type="button"
      class="branch-trigger"
      :class="{ detached }"
      aria-haspopup="listbox"
      :aria-expanded="open"
      :title="detached ? 'Not on a branch. Pick one to commit on.' : `On ${current}. Switch or create a branch.`"
      @click="toggle"
    >
      <svg class="branch-trigger-icon" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
        <line x1="6" x2="6" y1="3" y2="15" /><circle cx="18" cy="6" r="3" /><circle cx="6" cy="18" r="3" /><path d="M18 9a9 9 0 0 1-9 9" />
      </svg>
      <span class="branch-trigger-name">{{ label }}</span>
      <span v-if="!projectMode && (ahead > 0 || behind > 0)" class="branch-trigger-sync" :title="`${ahead} to push, ${behind} to pull`">
        <span v-if="ahead > 0">↑{{ ahead }}</span>
        <span v-if="behind > 0">↓{{ behind }}</span>
      </span>
      <svg class="branch-trigger-chevron" width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
        <path d="m6 9 6 6 6-6" />
      </svg>
    </button>

    <div v-if="open" class="branch-popover" @keydown="onKeydown">
      <div class="branch-search">
        <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
          <template v-if="creating"><path d="M12 5v14" /><path d="M5 12h14" /></template>
          <template v-else><circle cx="11" cy="11" r="7" /><path d="m20 20-3.5-3.5" /></template>
        </svg>
        <input
          ref="input"
          v-model="query"
          type="text"
          spellcheck="false"
          autocomplete="off"
          :placeholder="creating ? 'Name the new branch' : 'Find a branch, or type a new name'"
          aria-label="Find or create a branch"
          role="combobox"
          aria-controls="git-branch-list"
          :aria-activedescendant="activeKey ? `git-branch-${activeKey}` : undefined"
        />
        <kbd class="branch-kbd">esc</kbd>
      </div>

      <div id="git-branch-list" ref="list" class="branch-list" role="listbox" aria-label="Branches">
        <button
          v-if="createItem"
          :id="`git-branch-${createItem.key}`"
          type="button"
          role="option"
          class="branch-item branch-item--create"
          :data-active="activeKey === createItem.key"
          :aria-selected="activeKey === createItem.key"
          :disabled="busyKey !== null"
          @mouseenter="activeIndex = 0"
          @click="choose(createItem)"
        >
          <span class="branch-item-icon">
            <span v-if="busyKey === 'create'" class="spinner" />
            <svg v-else width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
              <path d="M12 5v14" /><path d="M5 12h14" />
            </svg>
          </span>
          <span class="branch-item-main">
            <span class="branch-item-name">Create <strong>{{ createItem.name }}</strong></span>
            <span class="branch-item-meta">{{ detached ? 'from the current commit' : `from ${current}` }}</span>
          </span>
        </button>

        <div v-for="section in sections" :key="section.title + (section.tag ?? '')" class="branch-section" role="group" :aria-label="section.tag ? `${section.title} ${section.tag}` : section.title">
          <div class="branch-section-title">
            {{ section.title }}
            <span v-if="section.tag" class="branch-section-tag">{{ section.tag }}</span>
          </div>
          <div
            v-for="item in section.items"
            :id="`git-branch-${item.key}`"
            :key="item.key"
            role="option"
            class="branch-item"
            :class="{ current: isCurrent(item) }"
            :data-active="activeKey === item.key"
            :aria-selected="activeKey === item.key"
            :aria-current="isCurrent(item) ? 'true' : undefined"
            :title="isCurrent(item) ? 'The current branch' : `Switch to ${itemName(item)}`"
            @mouseenter="activeIndex = items.indexOf(item)"
            @click="choose(item)"
          >
            <span class="branch-item-icon">
              <span v-if="busyKey === item.key" class="spinner" />
              <svg v-else-if="isCurrent(item)" width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.6" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
                <path d="M20 6 9 17l-5-5" />
              </svg>
              <svg v-else-if="item.kind === 'project'" width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
                <path d="M12 3v12" /><path d="m7 10 5 5 5-5" /><path d="M5 21h14" />
              </svg>
              <svg v-else-if="item.kind === 'remote'" width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
                <circle cx="12" cy="12" r="10" /><path d="M2 12h20" /><path d="M12 2a15.3 15.3 0 0 1 4 10 15.3 15.3 0 0 1-4 10 15.3 15.3 0 0 1-4-10 15.3 15.3 0 0 1 4-10z" />
              </svg>
              <svg v-else width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
                <line x1="6" x2="6" y1="3" y2="15" /><circle cx="18" cy="6" r="3" /><circle cx="6" cy="18" r="3" /><path d="M18 9a9 9 0 0 1-9 9" />
              </svg>
            </span>
            <span class="branch-item-main">
              <span class="branch-item-name">{{ item.kind === 'remote' ? remoteBranchName(item.branch) : itemName(item) }}</span>
            </span>
            <span v-if="item.kind === 'local' && item.inProject" class="branch-tag" title="The project has this branch">project</span>
            <span v-else-if="item.kind === 'project' && item.agent" class="branch-tag branch-tag--agent" title="An agent is working on this branch">agent</span>
            <span v-else-if="item.kind === 'local' && item.branch.upstream" class="branch-tag" :title="`Tracks ${item.branch.upstream}`">{{ item.branch.upstream }}</span>

            <span v-if="(item.kind === 'local' || item.kind === 'remote') && !isCurrent(item)" class="branch-item-actions">
              <button
                type="button"
                class="branch-action"
                :disabled="busyKey !== null || detached"
                :title="detached ? 'Switch to a branch to merge into it' : `Merge into ${current}`"
                :aria-label="`Merge ${item.branch.name} into ${current}`"
                @click.stop="mergeBranch(item.branch, item.key)"
              >
                <span v-if="busyKey === `merge:${item.key}`" class="spinner" />
                <svg v-else width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
                  <circle cx="18" cy="18" r="3" /><circle cx="6" cy="6" r="3" /><path d="M6 21V9a9 9 0 0 0 9 9" />
                </svg>
              </button>
              <button
                type="button"
                class="branch-action branch-action--danger"
                :disabled="busyKey !== null"
                :title="item.kind === 'remote' ? `Delete from ${item.branch.remoteName || 'the remote'}` : 'Delete branch'"
                :aria-label="`Delete ${item.branch.name}`"
                @click.stop="deleteTarget = item.branch"
              >
                <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
                  <path d="M3 6h18" /><path d="M8 6V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2" /><path d="m19 6-1 14a2 2 0 0 1-2 2H8a2 2 0 0 1-2-2L5 6" />
                </svg>
              </button>
            </span>
          </div>
        </div>

        <p v-if="loading && branches.length === 0" class="branch-empty">Loading branches…</p>
        <p v-else-if="items.length === 0" class="branch-empty">
          {{ needle ? 'No branches match.' : 'No branches yet. Type a name to create one.' }}
        </p>
      </div>

      <div class="branch-footer">
        <span><kbd>↑</kbd><kbd>↓</kbd> move</span>
        <span><kbd>↵</kbd> {{ createItem && activeKey === 'create' ? 'create' : 'switch' }}</span>
      </div>
    </div>

    <ConfirmModal
      :show="deleteTarget !== null"
      title="Delete branch"
      :message="deleteMessage"
      confirm-label="Delete"
      variant="danger"
      :loading="deleting"
      @confirm="confirmDelete"
      @cancel="cancelDelete"
    />
    <GitSshHostKeyModal
      :show="sshHostKey !== null"
      :host-key="sshHostKey"
      :loading="acceptingSshHostKey"
      :error="sshHostKeyError"
      @accept="acceptSshHostKey"
      @cancel="cancelSshHostKey"
    />
  </div>
</template>

<style scoped>
.branch-picker {
  position: relative;
}

.branch-trigger {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  width: 100%;
  height: 32px;
  padding: 0 var(--space-2) 0 10px;
  border: 0.5px solid var(--border-default);
  border-radius: var(--radius-md);
  background: var(--bg-elevated);
  color: var(--text-primary);
  text-align: left;
  transition: border-color var(--transition-fast), background var(--transition-fast);
}

.branch-trigger:hover {
  border-color: var(--border-strong);
  background: color-mix(in srgb, var(--bg-elevated) 70%, var(--bg-raised));
}

.branch-picker.open .branch-trigger {
  border-color: var(--accent-border);
  box-shadow: 0 0 0 3px var(--accent-glow);
}

.branch-trigger-icon {
  flex-shrink: 0;
  color: var(--accent-purple);
}

.branch-trigger.detached .branch-trigger-icon,
.branch-trigger.detached .branch-trigger-name {
  color: var(--accent-amber);
}

.branch-trigger-name {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-family: var(--font-mono);
  font-size: 0.76rem;
  font-weight: 500;
}

.branch-trigger-sync {
  display: inline-flex;
  gap: 5px;
  flex-shrink: 0;
  font-family: var(--font-mono);
  font-size: 0.7rem;
  color: var(--accent-amber);
}

.branch-trigger-chevron {
  flex-shrink: 0;
  color: var(--text-muted);
  transition: transform var(--transition-fast);
}

.branch-picker.open .branch-trigger-chevron {
  transform: rotate(180deg);
}

/* Popover */
.branch-popover {
  position: absolute;
  top: calc(100% + 6px);
  left: 0;
  right: 0;
  z-index: 30;
  display: flex;
  flex-direction: column;
  max-height: min(440px, 65vh);
  border: 0.5px solid var(--border-strong);
  border-radius: var(--radius-lg);
  background: var(--bg-elevated);
  box-shadow: var(--shadow-popover);
  overflow: hidden;
  animation: pop-in 120ms ease-out;
}

@keyframes pop-in {
  from {
    opacity: 0;
    transform: translateY(-4px);
  }
}

.branch-search {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  padding: 0 var(--space-2) 0 10px;
  border-bottom: 0.5px solid var(--border-default);
  color: var(--text-muted);
  flex-shrink: 0;
}

.branch-search input {
  flex: 1;
  min-width: 0;
  height: 34px;
  border: none;
  outline: none;
  background: transparent;
  color: var(--text-primary);
  font-family: var(--font-mono);
  font-size: 0.76rem;
}

.branch-search input:focus-visible {
  box-shadow: none;
}

.branch-search input::placeholder {
  color: var(--text-muted);
  font-family: var(--font-sans);
}

kbd {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  min-width: 16px;
  height: 16px;
  padding: 0 4px;
  border: 0.5px solid var(--border-default);
  border-bottom-width: 1px;
  border-radius: var(--radius-sm);
  background: var(--bg-base);
  color: var(--text-muted);
  font-family: var(--font-mono);
  font-size: 0.62rem;
  line-height: 1;
}

.branch-list {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  padding: var(--space-1);
}

.branch-section + .branch-section,
.branch-item--create + .branch-section {
  margin-top: var(--space-1);
  padding-top: var(--space-1);
  border-top: 0.5px solid var(--border-hairline);
}

.branch-section-title {
  display: flex;
  align-items: center;
  gap: 6px;
  padding: var(--space-1) var(--space-2) 2px;
  font-size: 0.64rem;
  font-weight: 600;
  letter-spacing: 0.06em;
  text-transform: uppercase;
  color: var(--text-muted);
}

.branch-section-tag {
  font-family: var(--font-mono);
  font-weight: 500;
  letter-spacing: 0;
  text-transform: none;
  color: var(--accent-blue);
}

.branch-item {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  width: 100%;
  min-height: 28px;
  padding: 2px var(--space-1) 2px var(--space-2);
  border-radius: var(--radius-sm);
  color: var(--text-secondary);
  text-align: left;
  cursor: pointer;
}

.branch-item[data-active='true'] {
  background: var(--bg-hover);
  color: var(--text-primary);
}

.branch-item.current {
  color: var(--text-primary);
  cursor: default;
}

.branch-item:disabled {
  cursor: default;
}

.branch-item-icon {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 14px;
  flex-shrink: 0;
  color: var(--text-muted);
}

.branch-item.current .branch-item-icon {
  color: var(--accent-green);
}

.branch-item--create .branch-item-icon {
  color: var(--accent);
}

.branch-item-main {
  display: flex;
  flex: 1;
  flex-direction: column;
  min-width: 0;
}

.branch-item-name {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-family: var(--font-mono);
  font-size: 0.74rem;
}

.branch-item.current .branch-item-name {
  font-weight: 600;
}

.branch-item--create .branch-item-name {
  font-family: var(--font-sans);
}

.branch-item--create strong {
  font-family: var(--font-mono);
  font-weight: 600;
  color: var(--text-primary);
}

.branch-item-meta {
  font-size: 0.66rem;
  color: var(--text-muted);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.branch-tag {
  flex-shrink: 1;
  min-width: 0;
  max-width: 45%;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  padding: 0 5px;
  border-radius: var(--radius-sm);
  background: var(--bg-neutral);
  color: var(--text-muted);
  font-family: var(--font-mono);
  font-size: 0.62rem;
  line-height: 16px;
}

.branch-tag--agent {
  background: var(--attention-bg);
  color: var(--attention);
}

.branch-item-actions {
  display: none;
  gap: 2px;
  flex-shrink: 0;
}

.branch-item[data-active='true'] .branch-item-actions {
  display: flex;
}

.branch-item[data-active='true'] .branch-tag {
  display: none;
}

.branch-action {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 22px;
  height: 22px;
  border-radius: var(--radius-sm);
  color: var(--text-muted);
  transition: color var(--transition-fast), background var(--transition-fast);
}

.branch-action:hover:not(:disabled) {
  background: var(--bg-raised);
  color: var(--text-primary);
}

.branch-action--danger:hover:not(:disabled) {
  background: var(--error-bg);
  color: var(--accent-rose);
}

.branch-action:disabled {
  opacity: 0.4;
  cursor: default;
}

.branch-empty {
  padding: var(--space-3) var(--space-2);
  font-size: 0.74rem;
  color: var(--text-muted);
  text-align: center;
}

.branch-footer {
  display: flex;
  gap: var(--space-3);
  padding: 5px 10px;
  border-top: 0.5px solid var(--border-default);
  background: var(--bg-base);
  color: var(--text-muted);
  font-size: 0.66rem;
  flex-shrink: 0;
}

.branch-footer span {
  display: inline-flex;
  align-items: center;
  gap: 3px;
}

.spinner {
  width: 11px;
  height: 11px;
  border: 1.5px solid var(--spinner-track);
  border-top-color: currentColor;
  border-radius: 50%;
  animation: spin 0.6s linear infinite;
}

@media (hover: none) {
  .branch-item-actions {
    display: flex;
  }

  .branch-footer,
  .branch-kbd {
    display: none;
  }
}

/* Matches MOBILE_MEDIA_QUERY in useMediaQuery.ts: touch-sized rows. */
@media (max-width: 768px), (pointer: coarse) and (max-height: 500px) {
  .branch-trigger {
    height: 44px;
  }

  .branch-trigger-name,
  .branch-item-name {
    font-size: 0.85rem;
  }

  .branch-search input {
    height: 44px;
  }

  .branch-item {
    min-height: 44px;
  }

  .branch-action {
    width: 40px;
    height: 40px;
  }
}
</style>
