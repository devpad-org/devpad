<script setup lang="ts">
import { inject, ref, watch } from 'vue'
import { projectApi, type Sandbox } from '@/api/projects'
import { useActiveProjectStore } from '@/stores/activeProject'
import StatusBadge from './StatusBadge.vue'
import { branchActionError } from './branchErrors'
import { confirmDiscardKey } from '@/components/ide/viewTabs'

// The agents' sandboxes in the IDE side bar. Each can be opened to debug the
// agent, or its branch checked out in the project workspace.
const props = defineProps<{
  // The project workspace is running, so branches can be checked out.
  workspaceLive: boolean
  // The panel is showing, so keep the list of published branches fresh.
  active: boolean
}>()

const emit = defineEmits<{ 'checked-out': [branch: string] }>()

const store = useActiveProjectStore()
const confirmDiscard = inject(confirmDiscardKey, () => Promise.resolve(true))
const busy = ref<number | null>(null)
const error = ref('')
const notice = ref('')

// An agent's branch reaches the project once its work is collected, so a
// sandbox on its first round has nothing to check out yet.
function published(sandbox: Sandbox): boolean {
  return store.branchNames.has(sandbox.branch)
}

function checkoutTitle(sandbox: Sandbox): string {
  if (!props.workspaceLive) return 'Start your workspace to check out branches'
  if (!published(sandbox)) return 'Not published yet. The branch appears once the agent finishes a round.'
  return `Check out ${sandbox.branch} in your workspace`
}

function refreshBranches() {
  store.refreshBranches().catch(() => {
    // Keep the last known branches; Check out reports a missing one.
  })
}

// Agents publish when a round ends, which shows up as the task moving on.
watch(() => [props.active, store.tasks.map((t) => `${t.id}:${t.status}:${t.headSha}`).join()], () => {
  if (props.active) refreshBranches()
}, { immediate: true })

async function start(sandbox: Sandbox) {
  if (store.projectId === null) return
  busy.value = sandbox.id
  error.value = ''
  notice.value = ''
  try {
    await projectApi.startSandbox(store.projectId, sandbox.id)
    await store.refresh()
  } catch (e) {
    error.value = e instanceof Error ? e.message : 'Failed to start sandbox'
  } finally {
    busy.value = null
  }
}

async function checkout(sandbox: Sandbox) {
  if (store.projectId === null || !props.workspaceLive) return
  if (!(await confirmDiscard('Checking out this branch reloads your open files from it, so their unsaved changes will be lost.'))) return
  busy.value = sandbox.id
  error.value = ''
  notice.value = ''
  try {
    const res = await projectApi.checkoutBranch(store.projectId, sandbox.branch)
    notice.value = res.created
      ? `Checked out ${res.branch}.`
      : res.sha === res.upstreamSha
        ? `Switched to ${res.branch}.`
        : `Switched to ${res.branch}. Your copy differs from the agent's; merge ${res.upstream} to pick up its latest work.`
    emit('checked-out', res.branch)
  } catch (e) {
    error.value = branchActionError(e, 'Failed to check out the branch')
  } finally {
    busy.value = null
  }
}
</script>

<template>
  <div class="side-panel agents-panel">
    <div class="side-header">
      <span class="side-title">
        <svg class="panel-icon" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
          <path d="M12 8V4H8" />
          <rect width="16" height="12" x="4" y="8" rx="2" />
          <path d="M2 14h2" />
          <path d="M20 14h2" />
          <path d="M15 13v2" />
          <path d="M9 13v2" />
        </svg>
        Agents
      </span>
      <span v-if="store.project" class="sandbox-count" :title="`${store.activeSandboxes} of ${store.project.maxSandboxes} sandboxes running`">
        {{ store.activeSandboxes }} / {{ store.project.maxSandboxes }}
      </span>
    </div>

    <p v-if="error" class="notice notice-error side-notice">{{ error }}</p>
    <p v-if="notice" class="notice notice-success side-notice">{{ notice }}</p>

    <div class="side-body">
      <div class="side-section">
        <div v-for="sandbox in store.sandboxes" :key="sandbox.id" class="side-row sandbox-row">
          <span class="side-row-main">
            <span class="side-row-title">
              <template v-if="store.sandboxTask(sandbox.id)">#{{ store.sandboxTask(sandbox.id)?.id }} {{ store.sandboxTask(sandbox.id)?.title }}</template>
              <template v-else>{{ sandbox.name }}</template>
            </span>
            <span class="side-row-meta">
              <span>{{ sandbox.kind }}</span>
              <span class="mono">{{ sandbox.branch }}</span>
            </span>
          </span>
          <StatusBadge :status="sandbox.status" />
          <span class="side-row-actions sandbox-actions">
            <button
              v-if="sandbox.status === 'stopped'"
              class="btn btn-secondary btn-sm"
              :disabled="busy === sandbox.id"
              @click="start(sandbox)"
            >
              Start
            </button>
            <button
              class="btn btn-secondary btn-sm"
              :disabled="!workspaceLive || !published(sandbox) || busy === sandbox.id"
              :title="checkoutTitle(sandbox)"
              @click="checkout(sandbox)"
            >
              Check out
            </button>
            <RouterLink
              class="btn btn-secondary btn-sm"
              :to="{ name: 'ide', params: { id: sandbox.id } }"
              title="Open the agent's sandbox to debug it"
            >
              Open
            </RouterLink>
          </span>
        </div>
        <p v-if="store.sandboxes.length === 0" class="side-empty">Agents get a sandbox when they start a task.</p>
      </div>
    </div>
  </div>
</template>

<style scoped src="./shared.css"></style>
<style scoped src="./panel.css"></style>
<style scoped>
.agents-panel {
  --ide-header-icon: var(--gruvbox-orange);
}

.sandbox-count {
  font-size: 0.72rem;
  color: var(--text-muted);
}

.sandbox-row {
  flex-wrap: wrap;
  padding-top: var(--space-2);
  padding-bottom: var(--space-2);
}

.sandbox-row + .sandbox-row {
  border-top: 0.5px solid var(--border-hairline);
  border-radius: 0;
}

.sandbox-row .side-row-main {
  flex: 1;
}

.sandbox-actions {
  width: 100%;
  justify-content: flex-end;
}
</style>
