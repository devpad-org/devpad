<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { gitApi, type GitActionResult } from '@/api/git'
import { projectApi } from '@/api/projects'
import { taskApi, type Task } from '@/api/tasks'
import { useActiveProjectStore } from '@/stores/activeProject'
import AgentModelPicker from './AgentModelPicker.vue'
import { branchActionError } from './branchErrors'

// Hands the assistant conversation off to a background task. The agent's
// sandbox starts from a project branch, so work that only exists in the
// project workspace is committed and published first, with the user's say.
const props = defineProps<{
  show: boolean
  projectId: number
  workspaceId: number
  conversationId: number
  defaultTitle: string
  defaultModel?: string
  defaultEffort?: string
}>()

const emit = defineEmits<{
  close: []
  created: [task: Task]
  // The workspace's branch moved: a new branch, a commit, or a publish.
  'branch-changed': []
}>()

const store = useActiveProjectStore()

const title = ref('')
const instructions = ref('')
const model = ref('')
const effort = ref('')
const base = ref<'current' | 'default'>('current')
const commitMessage = ref('')
const newBranch = ref('')

const loading = ref(false)
const busy = ref(false)
const step = ref('')
const error = ref('')

const currentBranch = ref('')
const changedFiles = ref(0)
const localHash = ref('')

const defaultBranch = computed(() => store.project?.defaultBranch ?? 'main')
const canonicalHash = computed(() => store.branches.find((b) => b.name === currentBranch.value)?.sha ?? '')
const onDefault = computed(() => currentBranch.value === defaultBranch.value)
const dirty = computed(() => changedFiles.value > 0)
// Local commits the project doesn't have yet. Hashes may be short.
const unpublished = computed(() => {
  if (!localHash.value) return false
  if (!canonicalHash.value) return true
  return !canonicalHash.value.startsWith(localHash.value) && !localHash.value.startsWith(canonicalHash.value)
})
// The default branch only moves by merging, so work on it goes to a new branch.
const needsNewBranch = computed(() => base.value === 'current' && onDefault.value && (dirty.value || unpublished.value))
const handoffBranch = computed(() => {
  if (base.value === 'default') return defaultBranch.value
  return needsNewBranch.value ? newBranch.value.trim() : currentBranch.value
})
const needsCommit = computed(() => base.value === 'current' && dirty.value)
const needsPublish = computed(() => base.value === 'current' && handoffBranch.value !== defaultBranch.value)

const plan = computed(() => {
  const steps: string[] = []
  if (needsNewBranch.value) steps.push(`Create the branch ${handoffBranch.value || '…'} from your ${defaultBranch.value}`)
  if (needsCommit.value) steps.push(`Commit your ${changedFiles.value} changed ${changedFiles.value === 1 ? 'file' : 'files'} to ${handoffBranch.value || '…'}`)
  if (needsPublish.value) steps.push(`Publish ${handoffBranch.value || '…'} to the project`)
  steps.push(`Start an agent on a new task branch made from ${handoffBranch.value || '…'}`)
  return steps
})

const leftBehind = computed(() => {
  if (base.value !== 'default' || (!dirty.value && !unpublished.value)) return ''
  return `Your uncommitted or unpublished work on ${currentBranch.value} stays in your workspace; the agent starts from ${defaultBranch.value} without it.`
})

function slug(text: string): string {
  return text.toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/^-+|-+$/g, '').slice(0, 40) || 'work'
}

async function load() {
  loading.value = true
  error.value = ''
  try {
    const [status, branches] = await Promise.all([
      gitApi.status(props.workspaceId),
      gitApi.branches(props.workspaceId),
      store.refreshBranches(),
    ])
    currentBranch.value = status.branch
    changedFiles.value = status.files.length
    localHash.value = branches.branches.find((b) => b.current && !b.remote)?.hash ?? ''
  } catch (e) {
    error.value = e instanceof Error ? e.message : 'Failed to read your workspace'
  } finally {
    loading.value = false
  }
}

watch(
  () => props.show,
  (visible) => {
    if (!visible) return
    title.value = props.defaultTitle
    instructions.value = 'Pick up where we left off in the conversation and finish the work.'
    model.value = props.defaultModel ?? ''
    effort.value = props.defaultEffort ?? ''
    base.value = 'current'
    commitMessage.value = `WIP: ${props.defaultTitle}`
    newBranch.value = `handoff/${slug(props.defaultTitle)}`
    step.value = ''
    void load()
  },
  { immediate: true },
)

function checkGit(result: GitActionResult, what: string) {
  if (!result.success) throw new Error(`${what}: ${result.output || result.error || 'git failed'}`)
}

async function handoff() {
  if (busy.value || loading.value) return
  if (!title.value.trim()) {
    error.value = 'Title is required'
    return
  }
  if (!model.value) {
    error.value = 'Pick a model for the agent'
    return
  }
  if (needsNewBranch.value && !newBranch.value.trim()) {
    error.value = 'Name the branch for your work'
    return
  }
  if (needsCommit.value && !commitMessage.value.trim()) {
    error.value = 'A commit message is required'
    return
  }
  busy.value = true
  error.value = ''
  const branch = handoffBranch.value
  let moved = false
  try {
    if (needsNewBranch.value) {
      step.value = `Creating ${branch}…`
      checkGit(await gitApi.action(props.workspaceId, 'checkout-new', { branch }), 'Creating the branch')
      moved = true
    }
    if (needsCommit.value) {
      step.value = 'Committing your changes…'
      checkGit(await gitApi.action(props.workspaceId, 'stage'), 'Staging your changes')
      checkGit(await gitApi.action(props.workspaceId, 'commit', { message: commitMessage.value.trim() }), 'Committing')
      moved = true
    }
    if (needsPublish.value) {
      step.value = `Publishing ${branch}…`
      try {
        await projectApi.publishBranch(props.projectId, branch)
      } catch (e) {
        throw new Error(branchActionError(e, `Failed to publish ${branch}`))
      }
      moved = true
    }
    step.value = 'Starting the agent…'
    const res = await taskApi.handoff(props.projectId, {
      conversationId: props.conversationId,
      title: title.value.trim(),
      instructions: instructions.value.trim(),
      baseBranch: branch,
      model: model.value,
      effort: effort.value || undefined,
    })
    store.upsertTask(res.task)
    emit('created', res.task)
  } catch (e) {
    error.value = e instanceof Error ? e.message : 'Failed to hand off'
    // Whatever finished stays done; going again picks up from there.
    if (moved) void load()
  } finally {
    if (moved) emit('branch-changed')
    busy.value = false
    step.value = ''
  }
}
</script>

<template>
  <Teleport to="body">
    <div v-if="show" class="modal-overlay" @click.self="!busy && emit('close')">
      <div class="modal">
        <div class="modal-header">
          <h2 class="modal-title">Continue in background</h2>
          <button class="modal-close" aria-label="Close" :disabled="busy" @click="emit('close')">&times;</button>
        </div>
        <form class="modal-body" @submit.prevent="handoff">
          <p class="form-hint intro">
            A background agent picks this conversation up as a new task, in its own sandbox. It opens a merge request when it's done.
          </p>
          <div class="form-group">
            <label class="form-label" for="handoff-title">Task title</label>
            <input id="handoff-title" v-model="title" class="form-input" type="text" autocomplete="off" />
          </div>
          <div class="form-group">
            <label class="form-label" for="handoff-instructions">Instructions</label>
            <textarea id="handoff-instructions" v-model="instructions" class="form-input form-textarea" rows="3" />
            <p class="form-hint">The agent also gets the conversation so far, without tool output.</p>
          </div>
          <div class="form-group">
            <span class="form-label">Agent</span>
            <AgentModelPicker v-model:model="model" v-model:effort="effort" />
          </div>

          <div class="form-group">
            <span class="form-label">Start from</span>
            <p v-if="loading" class="form-hint">Reading your workspace…</p>
            <template v-else>
              <label class="form-check">
                <input v-model="base" type="radio" value="current" />
                Your work on <span class="mono">{{ currentBranch || '…' }}</span>
                <span v-if="dirty" class="badge">{{ changedFiles }} uncommitted</span>
                <span v-else-if="unpublished" class="badge">unpublished commits</span>
              </label>
              <label v-if="!onDefault" class="form-check">
                <input v-model="base" type="radio" value="default" />
                <span class="mono">{{ defaultBranch }}</span>
              </label>
            </template>
          </div>

          <div v-if="needsNewBranch" class="form-group">
            <label class="form-label" for="handoff-branch">New branch for your work</label>
            <input id="handoff-branch" v-model="newBranch" class="form-input form-mono" type="text" autocomplete="off" />
            <p class="form-hint">{{ defaultBranch }} only changes by merging, so your work moves to a branch first.</p>
          </div>
          <div v-if="needsCommit" class="form-group">
            <label class="form-label" for="handoff-commit">Commit message</label>
            <input id="handoff-commit" v-model="commitMessage" class="form-input" type="text" autocomplete="off" />
            <p class="form-hint">All {{ changedFiles }} changed {{ changedFiles === 1 ? 'file is' : 'files are' }} committed, including new ones, so the agent sees them.</p>
          </div>

          <div v-if="!loading" class="plan">
            <span class="form-label">What happens</span>
            <ol class="plan-steps">
              <li v-for="item in plan" :key="item">{{ item }}</li>
            </ol>
            <p v-if="leftBehind" class="form-hint">{{ leftBehind }}</p>
          </div>

          <p v-if="error" class="form-error">{{ error }}</p>
          <div class="modal-actions">
            <span v-if="step" class="step">{{ step }}</span>
            <button type="button" class="btn btn-secondary" :disabled="busy" @click="emit('close')">Cancel</button>
            <button type="submit" class="btn btn-primary" :disabled="busy || loading">
              {{ busy ? 'Working…' : needsCommit ? 'Commit and hand off' : 'Hand off' }}
            </button>
          </div>
        </form>
      </div>
    </div>
  </Teleport>
</template>

<style scoped src="./shared.css"></style>
<style scoped>
.modal {
  background: var(--bg-surface);
  border: 0.5px solid var(--border-default);
  border-radius: var(--radius-lg);
  width: 100%;
  max-width: 540px;
  box-shadow: var(--shadow-strong);
}

.modal-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: var(--space-4) var(--space-5);
  border-bottom: 0.5px solid var(--border-default);
}

.modal-title {
  font-size: 1rem;
  font-weight: 600;
}

.modal-close {
  font-size: 1.25rem;
  color: var(--text-muted);
  padding: var(--space-1);
  transition: color var(--transition-fast);
}

.modal-close:hover:not(:disabled) {
  color: var(--text-primary);
}

.modal-body {
  padding: var(--space-5);
}

.intro {
  margin-top: 0;
  margin-bottom: var(--space-4);
}

.badge {
  margin-left: var(--space-1);
  padding: 0 6px;
  border: 0.5px solid var(--warning-border);
  border-radius: var(--radius-sm);
  background: var(--warning-bg);
  color: var(--accent-amber);
  font-size: 0.7rem;
}

.plan {
  margin-bottom: var(--space-3);
  padding: var(--space-3);
  border: 0.5px solid var(--border-subtle);
  border-radius: var(--radius-md);
  background: var(--bg-neutral);
}

.plan-steps {
  margin: var(--space-1) 0 0;
  padding-left: var(--space-5);
  font-size: 0.82rem;
  line-height: 1.6;
  color: var(--text-secondary);
}

.modal-actions {
  display: flex;
  align-items: center;
  justify-content: flex-end;
  flex-wrap: wrap;
  gap: var(--space-2);
  padding-top: var(--space-2);
}

.step {
  margin-right: auto;
  font-size: 0.78rem;
  color: var(--text-muted);
}
</style>
