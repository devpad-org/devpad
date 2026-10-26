<script setup lang="ts">
import { ref, onMounted, computed } from 'vue'
import { useAdminStore } from '@/stores/admin'
import { useAuthStore } from '@/stores/auth'
import type { AdminUser } from '@/api/admin'
import type { AdminWorkspace } from '@/api/admin'
import { aiApi, type AIModel, type AIProvider, type BudgetDefaults, type BuiltInAgentDefaults, type TokenPrices } from '@/api/ai'
import { useAiModelStore } from '@/stores/aiModels'
import { thinkingEffortLabel } from '@/utils/aiModels'

const admin = useAdminStore()
const auth = useAuthStore()
const aiModelStore = useAiModelStore()

// Modal state
const showCreateModal = ref(false)
const showEditModal = ref(false)
const showResetModal = ref(false)
const showDeleteConfirm = ref(false)
const selectedUser = ref<AdminUser | null>(null)
const modalError = ref('')
const modalSubmitting = ref(false)

// Create form
const createForm = ref({ username: '', email: '', password: '', isAdmin: false })

// Edit form
const editForm = ref({ username: '', email: '', isAdmin: false })

// Reset password form
const resetForm = ref({ password: '', confirmPassword: '' })

// AI Providers
const aiProviders = ref<AIProvider[]>([])
const aiProviderForms = ref<Record<string, { apiKey: string; enabled: boolean }>>({})
const aiLoading = ref(false)
const aiError = ref('')
const aiSuccess = ref('')
const aiSubmitting = ref<string | null>(null)

// AI agent defaults
const agentDefaults = ref<BuiltInAgentDefaults[]>([])
const agentDefaultForms = ref<Record<string, { model: string; effort: string }>>({})
const agentDefaultsLoading = ref(false)
const agentDefaultsError = ref('')
const agentDefaultsSuccess = ref('')
const agentDefaultsSubmitting = ref<string | null>(null)

// AI agent budgets, as typed, in USD; empty or zero means no limit
type BudgetForm = Record<keyof BudgetDefaults, string>
const budgetForm = ref<BudgetForm>({ taskUsd: '', reviewUsd: '', assistantUsd: '' })
const budgetsLoading = ref(false)
const budgetsError = ref('')
const budgetsSuccess = ref('')
const budgetsSubmitting = ref(false)

// Every model, configured or not, for agent defaults and the pricing table
const allAIModels = ref<AIModel[]>([])
const modelsLoading = ref(false)
const modelsError = ref('')

// Workspace limits modal
const showLimitsModal = ref(false)
const selectedWorkspace = ref<AdminWorkspace | null>(null)
const limitsForm = ref({ memoryGB: 2, cpuCores: 2 })
const limitsError = ref('')
const limitsSubmitting = ref(false)

// Helper to find username by user ID
const usernameMap = computed(() => {
  const map: Record<number, string> = {}
  for (const u of admin.users) {
    map[u.id] = u.username
  }
  return map
})

onMounted(() => {
  admin.fetchUsers()
  admin.fetchWorkspaces()
  loadAIProviders()
  loadAIModels()
  loadAgentDefaults()
  loadBudgets()
})

async function loadAIModels() {
  modelsLoading.value = true
  try {
    allAIModels.value = (await aiApi.listModels()).models
  } catch {
    modelsError.value = 'Failed to load AI models'
  } finally {
    modelsLoading.value = false
  }
}

const priceFormat = new Intl.NumberFormat('en-US', {
  style: 'currency',
  currency: 'USD',
  minimumFractionDigits: 2,
  maximumFractionDigits: 4,
})

// A price of zero means the provider doesn't bill that kind of token on its own.
function formatPrice(prices: TokenPrices, key: keyof TokenPrices): string {
  return prices[key] > 0 ? priceFormat.format(prices[key]) : '—'
}

function formatTokenCount(tokens: number): string {
  return tokens >= 1000 && tokens % 1000 === 0 ? `${tokens / 1000}K` : tokens.toLocaleString()
}

const priceColumns: { key: keyof TokenPrices; label: string }[] = [
  { key: 'input', label: 'Input' },
  { key: 'output', label: 'Output' },
  { key: 'cacheRead', label: 'Cache read' },
  { key: 'cacheWrite', label: 'Cache write' },
]

async function loadAIProviders() {
  aiLoading.value = true
  try {
    const res = await aiApi.listProviders()
    aiProviders.value = res.providers
    for (const p of res.providers) {
      aiProviderForms.value[p.id] = { apiKey: '', enabled: p.enabled }
    }
  } catch {
    aiError.value = 'Failed to load AI providers'
  } finally {
    aiLoading.value = false
  }
}

async function saveProvider(providerId: string) {
  aiError.value = ''
  aiSuccess.value = ''
  aiSubmitting.value = providerId

  const form = aiProviderForms.value[providerId]
  if (!form) return

  try {
    await aiApi.updateProvider(providerId, form.apiKey, form.enabled)
    aiSuccess.value = 'Provider updated successfully'
    const res = await aiApi.listProviders()
    aiProviders.value = res.providers
    for (const p of res.providers) {
      aiProviderForms.value[p.id] = { apiKey: '', enabled: p.enabled }
    }
    // A provider turned on or off changes which models agents can default to.
    allAIModels.value = (await aiApi.listModels()).models
    void aiModelStore.fetchModels()
  } catch (e) {
    aiError.value = e instanceof Error ? e.message : 'Failed to update provider'
  } finally {
    aiSubmitting.value = null
  }
}

async function loadAgentDefaults() {
  agentDefaultsLoading.value = true
  try {
    const defaults = await aiApi.listAgentDefaults()
    agentDefaults.value = defaults.agents
    for (const a of defaults.agents) {
      agentDefaultForms.value[a.agentId] = { model: a.model, effort: a.effort }
    }
  } catch {
    agentDefaultsError.value = 'Failed to load AI agent defaults'
  } finally {
    agentDefaultsLoading.value = false
  }
}

function agentDefaultModel(agentId: string): AIModel | undefined {
  const model = agentDefaultForms.value[agentId]?.model
  return allAIModels.value.find((m) => m.id === model)
}

// Models from configured providers, plus the one already chosen if its
// provider has since been turned off, so the select can still show it.
function agentModelOptions(agentId: string): AIModel[] {
  const chosen = agentDefaultForms.value[agentId]?.model
  return allAIModels.value.filter((m) => m.configured || m.id === chosen)
}

function agentEffortOptions(agentId: string): string[] {
  return agentDefaultModel(agentId)?.thinking.supportedEfforts ?? []
}

function onAgentModelChange(agentId: string) {
  const form = agentDefaultForms.value[agentId]
  if (form && !agentEffortOptions(agentId).includes(form.effort)) {
    form.effort = ''
  }
}

async function saveAgentDefaults(agentId: string) {
  const form = agentDefaultForms.value[agentId]
  if (!form) return
  agentDefaultsError.value = ''
  agentDefaultsSuccess.value = ''
  agentDefaultsSubmitting.value = agentId
  try {
    const res = await aiApi.setAgentDefaults(agentId, form.model, form.model ? form.effort : '')
    const index = agentDefaults.value.findIndex((a) => a.agentId === agentId)
    if (index !== -1) agentDefaults.value[index] = res.agent
    agentDefaultForms.value[agentId] = { model: res.agent.model, effort: res.agent.effort }
    agentDefaultsSuccess.value = `Defaults for the ${res.agent.name} saved`
    // Pickers in this browser read the defaults from the models store.
    void aiModelStore.fetchModels()
  } catch (e) {
    agentDefaultsError.value = e instanceof Error ? e.message : 'Failed to save agent defaults'
  } finally {
    agentDefaultsSubmitting.value = null
  }
}

function toBudgetForm(b: BudgetDefaults): BudgetForm {
  const field = (usd: number) => (usd > 0 ? String(usd) : '')
  return { taskUsd: field(b.taskUsd), reviewUsd: field(b.reviewUsd), assistantUsd: field(b.assistantUsd) }
}

async function loadBudgets() {
  budgetsLoading.value = true
  try {
    budgetForm.value = toBudgetForm((await aiApi.getBudgets()).budgets)
  } catch {
    budgetsError.value = 'Failed to load AI agent budgets'
  } finally {
    budgetsLoading.value = false
  }
}

async function saveBudgets() {
  budgetsError.value = ''
  budgetsSuccess.value = ''
  const parse = (value: string) => (value.trim() === '' ? 0 : Number(value))
  const budgets: BudgetDefaults = {
    taskUsd: parse(budgetForm.value.taskUsd),
    reviewUsd: parse(budgetForm.value.reviewUsd),
    assistantUsd: parse(budgetForm.value.assistantUsd),
  }
  if (Object.values(budgets).some((usd) => !Number.isFinite(usd) || usd < 0)) {
    budgetsError.value = 'Each budget must be an amount in USD, or empty for no limit'
    return
  }
  budgetsSubmitting.value = true
  try {
    budgetForm.value = toBudgetForm((await aiApi.setBudgets(budgets)).budgets)
    budgetsSuccess.value = 'Budgets saved'
  } catch (e) {
    budgetsError.value = e instanceof Error ? e.message : 'Failed to save budgets'
  } finally {
    budgetsSubmitting.value = false
  }
}

function openCreate() {
  createForm.value = { username: '', email: '', password: '', isAdmin: false }
  modalError.value = ''
  showCreateModal.value = true
}

function openEdit(user: AdminUser) {
  selectedUser.value = user
  editForm.value = { username: user.username, email: user.email, isAdmin: user.isAdmin }
  modalError.value = ''
  showEditModal.value = true
}

function openResetPassword(user: AdminUser) {
  selectedUser.value = user
  resetForm.value = { password: '', confirmPassword: '' }
  modalError.value = ''
  showResetModal.value = true
}

function openDeleteConfirm(user: AdminUser) {
  selectedUser.value = user
  showDeleteConfirm.value = true
}

function closeModals() {
  showCreateModal.value = false
  showEditModal.value = false
  showResetModal.value = false
  showDeleteConfirm.value = false
  selectedUser.value = null
  modalError.value = ''
}

async function handleCreate() {
  modalError.value = ''
  if (!createForm.value.username || !createForm.value.email || !createForm.value.password) {
    modalError.value = 'All fields are required'
    return
  }
  if (createForm.value.password.length < 8) {
    modalError.value = 'Password must be at least 8 characters'
    return
  }
  modalSubmitting.value = true
  try {
    await admin.createUser(createForm.value)
    closeModals()
  } catch (e) {
    modalError.value = e instanceof Error ? e.message : 'Failed to create user'
  } finally {
    modalSubmitting.value = false
  }
}

async function handleEdit() {
  if (!selectedUser.value) return
  modalError.value = ''
  if (!editForm.value.username || !editForm.value.email) {
    modalError.value = 'Username and email are required'
    return
  }
  modalSubmitting.value = true
  try {
    await admin.updateUser(selectedUser.value.id, editForm.value)
    closeModals()
  } catch (e) {
    modalError.value = e instanceof Error ? e.message : 'Failed to update user'
  } finally {
    modalSubmitting.value = false
  }
}

async function handleResetPassword() {
  if (!selectedUser.value) return
  modalError.value = ''
  if (resetForm.value.password !== resetForm.value.confirmPassword) {
    modalError.value = 'Passwords do not match'
    return
  }
  if (resetForm.value.password.length < 8) {
    modalError.value = 'Password must be at least 8 characters'
    return
  }
  modalSubmitting.value = true
  try {
    await admin.resetPassword(selectedUser.value.id, resetForm.value.password)
    closeModals()
  } catch (e) {
    modalError.value = e instanceof Error ? e.message : 'Failed to reset password'
  } finally {
    modalSubmitting.value = false
  }
}

async function handleDelete() {
  if (!selectedUser.value) return
  modalSubmitting.value = true
  try {
    await admin.deleteUser(selectedUser.value.id)
    closeModals()
  } catch (e) {
    modalError.value = e instanceof Error ? e.message : 'Failed to delete user'
  } finally {
    modalSubmitting.value = false
  }
}

function openLimitsModal(ws: AdminWorkspace) {
  selectedWorkspace.value = ws
  limitsForm.value = {
    memoryGB: ws.memoryLimit / (1024 * 1024 * 1024),
    cpuCores: ws.nanoCpus / 1_000_000_000,
  }
  limitsError.value = ''
  showLimitsModal.value = true
}

function closeLimitsModal() {
  showLimitsModal.value = false
  selectedWorkspace.value = null
  limitsError.value = ''
}

async function handleUpdateLimits() {
  if (!selectedWorkspace.value) return
  limitsError.value = ''

  if (limitsForm.value.memoryGB < 0.25) {
    limitsError.value = 'Memory must be at least 0.25 GB'
    return
  }
  if (limitsForm.value.cpuCores < 0.1) {
    limitsError.value = 'CPU must be at least 0.1 cores'
    return
  }

  limitsSubmitting.value = true
  try {
    await admin.updateWorkspaceLimits(selectedWorkspace.value.id, {
      memoryLimit: Math.round(limitsForm.value.memoryGB * 1024 * 1024 * 1024),
      nanoCpus: Math.round(limitsForm.value.cpuCores * 1_000_000_000),
    })
    closeLimitsModal()
  } catch (e) {
    limitsError.value = e instanceof Error ? e.message : 'Failed to update limits'
  } finally {
    limitsSubmitting.value = false
  }
}

function formatBytes(bytes: number): string {
  const gb = bytes / (1024 * 1024 * 1024)
  return gb % 1 === 0 ? `${gb} GB` : `${gb.toFixed(2)} GB`
}

function formatCPU(nanoCpus: number): string {
  const cores = nanoCpus / 1_000_000_000
  return cores % 1 === 0 ? `${cores} cores` : `${cores.toFixed(1)} cores`
}

function isSelf(user: AdminUser): boolean {
  return user.id === auth.user?.id
}
</script>

<template>
  <div class="admin-page">
    <div class="admin-container">
      <div class="admin-header">
        <div>
          <h1 class="admin-title">User Management</h1>
          <p class="admin-subtitle">Manage user accounts and permissions</p>
        </div>
        <button class="btn-create" @click="openCreate">+ New User</button>
      </div>

      <div v-if="admin.error" class="alert alert-error">{{ admin.error }}</div>

      <div v-if="admin.loading" class="loading-state">Loading users...</div>

      <div v-else class="users-table-wrap">
        <table class="users-table">
          <thead>
            <tr>
              <th>Username</th>
              <th>Email</th>
              <th>Role</th>
              <th>Created</th>
              <th>Actions</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="user in admin.users" :key="user.id">
              <td class="cell-username">
                {{ user.username }}
                <span v-if="isSelf(user)" class="badge-you">you</span>
              </td>
              <td class="cell-email">{{ user.email }}</td>
              <td>
                <span class="badge-role" :class="user.isAdmin ? 'admin' : 'user'">
                  {{ user.isAdmin ? 'Admin' : 'User' }}
                </span>
              </td>
              <td class="cell-date">{{ new Date(user.createdAt).toLocaleDateString() }}</td>
              <td class="cell-actions">
                <button class="btn-action" title="Edit user" @click="openEdit(user)">Edit</button>
                <button class="btn-action" title="Reset password" @click="openResetPassword(user)">Reset PW</button>
                <button
                  class="btn-action btn-danger"
                  title="Delete user"
                  :disabled="isSelf(user)"
                  @click="openDeleteConfirm(user)"
                >
                  Delete
                </button>
              </td>
            </tr>
          </tbody>
        </table>
        <div v-if="admin.users.length === 0 && !admin.loading" class="empty-state">
          No users found.
        </div>
      </div>
    </div>

    <!-- Create User Modal -->
    <Teleport to="body">
      <div v-if="showCreateModal" class="modal-overlay" @click.self="closeModals">
        <div class="modal">
          <h2 class="modal-title">Create User</h2>
          <div v-if="modalError" class="alert alert-error">{{ modalError }}</div>
          <form @submit.prevent="handleCreate">
            <div class="form-field">
              <label for="create-username">Username</label>
              <input id="create-username" v-model="createForm.username" type="text" required autofocus placeholder="Username" />
            </div>
            <div class="form-field">
              <label for="create-email">Email</label>
              <input id="create-email" v-model="createForm.email" type="email" required placeholder="user@example.com" />
            </div>
            <div class="form-field">
              <label for="create-password">Password</label>
              <input id="create-password" v-model="createForm.password" type="password" required placeholder="At least 8 characters" />
            </div>
            <div class="form-field form-checkbox">
              <label>
                <input v-model="createForm.isAdmin" type="checkbox" />
                <span>Admin privileges</span>
              </label>
            </div>
            <div class="modal-actions">
              <button type="button" class="btn-secondary" @click="closeModals">Cancel</button>
              <button type="submit" class="btn-primary" :disabled="modalSubmitting">
                {{ modalSubmitting ? 'Creating...' : 'Create User' }}
              </button>
            </div>
          </form>
        </div>
      </div>
    </Teleport>

    <!-- Edit User Modal -->
    <Teleport to="body">
      <div v-if="showEditModal" class="modal-overlay" @click.self="closeModals">
        <div class="modal">
          <h2 class="modal-title">Edit User</h2>
          <div v-if="modalError" class="alert alert-error">{{ modalError }}</div>
          <form @submit.prevent="handleEdit">
            <div class="form-field">
              <label for="edit-username">Username</label>
              <input id="edit-username" v-model="editForm.username" type="text" required placeholder="Username" />
            </div>
            <div class="form-field">
              <label for="edit-email">Email</label>
              <input id="edit-email" v-model="editForm.email" type="email" required placeholder="user@example.com" />
            </div>
            <div class="form-field form-checkbox">
              <label>
                <input v-model="editForm.isAdmin" type="checkbox" :disabled="isSelf(selectedUser!)" />
                <span>Admin privileges</span>
              </label>
              <p v-if="isSelf(selectedUser!)" class="field-hint">You cannot change your own admin status</p>
            </div>
            <div class="modal-actions">
              <button type="button" class="btn-secondary" @click="closeModals">Cancel</button>
              <button type="submit" class="btn-primary" :disabled="modalSubmitting">
                {{ modalSubmitting ? 'Saving...' : 'Save Changes' }}
              </button>
            </div>
          </form>
        </div>
      </div>
    </Teleport>

    <!-- Reset Password Modal -->
    <Teleport to="body">
      <div v-if="showResetModal" class="modal-overlay" @click.self="closeModals">
        <div class="modal">
          <h2 class="modal-title">Reset Password</h2>
          <p class="modal-subtitle">Set a new password for <strong>{{ selectedUser?.username }}</strong></p>
          <div v-if="modalError" class="alert alert-error">{{ modalError }}</div>
          <form @submit.prevent="handleResetPassword">
            <div class="form-field">
              <label for="reset-password">New Password</label>
              <input id="reset-password" v-model="resetForm.password" type="password" required autofocus placeholder="At least 8 characters" />
            </div>
            <div class="form-field">
              <label for="reset-confirm">Confirm Password</label>
              <input id="reset-confirm" v-model="resetForm.confirmPassword" type="password" required placeholder="Repeat password" />
            </div>
            <div class="modal-actions">
              <button type="button" class="btn-secondary" @click="closeModals">Cancel</button>
              <button type="submit" class="btn-primary" :disabled="modalSubmitting">
                {{ modalSubmitting ? 'Resetting...' : 'Reset Password' }}
              </button>
            </div>
          </form>
        </div>
      </div>
    </Teleport>

    <!-- Delete Confirmation Modal -->
    <Teleport to="body">
      <div v-if="showDeleteConfirm" class="modal-overlay" @click.self="closeModals">
        <div class="modal" role="alertdialog" aria-label="Delete user">
          <h2 class="modal-title">Delete User</h2>
          <p class="modal-subtitle">
            Are you sure you want to delete <strong>{{ selectedUser?.username }}</strong>?
            This action cannot be undone. All of their workspaces will also be deleted.
          </p>
          <div v-if="modalError" class="alert alert-error">{{ modalError }}</div>
          <div class="modal-actions">
            <button type="button" class="btn-secondary" @click="closeModals">Cancel</button>
            <button class="btn-delete" :disabled="modalSubmitting" @click="handleDelete">
              {{ modalSubmitting ? 'Deleting...' : 'Delete User' }}
            </button>
          </div>
        </div>
      </div>
    </Teleport>

    <!-- Workspaces Section -->
    <div class="admin-container" style="margin-top: var(--space-6);">
      <div class="admin-header">
        <div>
          <h1 class="admin-title">Workspaces</h1>
          <p class="admin-subtitle">Manage resource limits for all workspaces</p>
        </div>
      </div>

      <div v-if="admin.workspacesError" class="alert alert-error">{{ admin.workspacesError }}</div>

      <div v-if="admin.workspacesLoading" class="loading-state">Loading workspaces...</div>

      <div v-else class="users-table-wrap">
        <table class="users-table">
          <thead>
            <tr>
              <th>Name</th>
              <th>Owner</th>
              <th>Status</th>
              <th>Memory</th>
              <th>CPU</th>
              <th>Actions</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="ws in admin.workspaces" :key="ws.id">
              <td class="cell-username">{{ ws.name }}</td>
              <td class="cell-email">{{ usernameMap[ws.userId] || `User #${ws.userId}` }}</td>
              <td>
                <span class="badge-status" :class="ws.status">{{ ws.status }}</span>
              </td>
              <td class="cell-limit">{{ formatBytes(ws.memoryLimit) }}</td>
              <td class="cell-limit">{{ formatCPU(ws.nanoCpus) }}</td>
              <td class="cell-actions">
                <button class="btn-action" title="Edit resource limits" @click="openLimitsModal(ws)">Limits</button>
              </td>
            </tr>
          </tbody>
        </table>
        <div v-if="admin.workspaces.length === 0 && !admin.workspacesLoading" class="empty-state">
          No workspaces found.
        </div>
      </div>
    </div>

    <!-- Workspace Limits Modal -->
    <Teleport to="body">
      <div v-if="showLimitsModal" class="modal-overlay" @click.self="closeLimitsModal">
        <div class="modal">
          <h2 class="modal-title">Resource Limits</h2>
          <p class="modal-subtitle">Configure limits for <strong>{{ selectedWorkspace?.name }}</strong></p>
          <div v-if="limitsError" class="alert alert-error">{{ limitsError }}</div>
          <form @submit.prevent="handleUpdateLimits">
            <div class="form-field">
              <label for="limit-memory">Memory (GB)</label>
              <input
                id="limit-memory"
                v-model.number="limitsForm.memoryGB"
                type="number"
                min="0.25"
                step="0.25"
                required
              />
            </div>
            <div class="form-field">
              <label for="limit-cpu">CPU Cores</label>
              <input
                id="limit-cpu"
                v-model.number="limitsForm.cpuCores"
                type="number"
                min="0.1"
                step="0.1"
                required
              />
            </div>
            <p class="field-hint" style="margin-bottom: var(--space-4);">
              Changes apply immediately to running containers.
            </p>
            <div class="modal-actions">
              <button type="button" class="btn-secondary" @click="closeLimitsModal">Cancel</button>
              <button type="submit" class="btn-primary" :disabled="limitsSubmitting">
                {{ limitsSubmitting ? 'Saving...' : 'Save Limits' }}
              </button>
            </div>
          </form>
        </div>
      </div>
    </Teleport>

    <!-- AI Providers Section -->
    <div class="admin-container" style="margin-top: var(--space-6);">
      <div class="admin-header">
        <div>
          <h1 class="admin-title">AI Providers</h1>
          <p class="admin-subtitle">Configure API keys and enable AI chat providers</p>
        </div>
      </div>

      <div v-if="aiError" class="alert alert-error">{{ aiError }}</div>
      <div v-if="aiSuccess" class="alert alert-success">{{ aiSuccess }}</div>

      <div v-if="aiLoading" class="loading-state">Loading providers...</div>

      <div v-else class="ai-providers-list">
        <div v-for="provider in aiProviders" :key="provider.id" class="ai-provider-card">
          <div class="ai-provider-header">
            <span class="ai-provider-name">{{ provider.name }}</span>
            <span v-if="provider.enabled && provider.hasApiKey" class="badge-role admin">Active</span>
            <span v-else class="badge-role user">Inactive</span>
          </div>
          <form @submit.prevent="saveProvider(provider.id)">
            <div class="form-field">
              <label :for="'apikey-' + provider.id">API Key</label>
              <input
                :id="'apikey-' + provider.id"
                v-model="aiProviderForms[provider.id].apiKey"
                type="password"
                :placeholder="provider.hasApiKey ? '••••••••  (leave blank to keep current)' : 'Enter API key'"
              />
            </div>
            <div class="ai-provider-actions">
              <label class="toggle-label">
                <input
                  v-model="aiProviderForms[provider.id].enabled"
                  type="checkbox"
                  class="toggle-input"
                />
                <span class="toggle-switch"></span>
                Enabled
              </label>
              <button type="submit" class="btn-primary" :disabled="aiSubmitting === provider.id">
                {{ aiSubmitting === provider.id ? 'Saving...' : 'Save' }}
              </button>
            </div>
          </form>
        </div>
      </div>
    </div>

    <!-- AI Model Pricing Section -->
    <div class="admin-container" style="margin-top: var(--space-6);">
      <div class="admin-header">
        <div>
          <h1 class="admin-title">AI Model Pricing</h1>
          <p class="admin-subtitle">USD per million tokens, used to work out what each agent run costs. Prices are built into devpad.</p>
        </div>
      </div>

      <div v-if="modelsError" class="alert alert-error">{{ modelsError }}</div>

      <div v-if="modelsLoading" class="loading-state">Loading models...</div>

      <div v-else-if="!modelsError" class="users-table-wrap">
        <table class="users-table">
          <thead>
            <tr>
              <th>Model</th>
              <th>Provider</th>
              <th v-for="col in priceColumns" :key="col.key" class="cell-price">{{ col.label }}</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="model in allAIModels" :key="model.providerId + '/' + model.id">
              <td class="cell-username">
                {{ model.name }}
                <div class="cell-model-id">{{ model.id }}</div>
              </td>
              <td>
                {{ model.providerName }}
                <span v-if="!model.configured" class="badge-role user">Inactive</span>
              </td>
              <template v-if="model.pricing">
                <td v-for="col in priceColumns" :key="col.key" class="cell-limit cell-price">
                  {{ formatPrice(model.pricing, col.key) }}
                  <div v-if="model.pricing.longContext && model.pricing.longContextThreshold" class="cell-long-context">
                    {{ formatPrice(model.pricing.longContext, col.key) }} over {{ formatTokenCount(model.pricing.longContextThreshold) }}
                  </div>
                </td>
              </template>
              <td v-else :colspan="priceColumns.length" class="cell-date">Prices unknown, so runs aren't costed</td>
            </tr>
          </tbody>
        </table>
        <div v-if="allAIModels.length === 0" class="empty-state">
          No models found.
        </div>
      </div>
      <p class="field-hint">
        — means the provider doesn't bill that kind of token separately. Where a second price is shown, it replaces the first for a request whose prompt is over that many tokens.
      </p>
    </div>

    <!-- AI Agent Defaults Section -->
    <div class="admin-container" style="margin-top: var(--space-6);">
      <div class="admin-header">
        <div>
          <h1 class="admin-title">AI Agent Defaults</h1>
          <p class="admin-subtitle">The model and reasoning effort each built-in agent runs with unless whoever starts it picks another, including agents started by other agents.</p>
        </div>
      </div>

      <div v-if="agentDefaultsError" class="alert alert-error">{{ agentDefaultsError }}</div>
      <div v-if="agentDefaultsSuccess" class="alert alert-success">{{ agentDefaultsSuccess }}</div>

      <div v-if="agentDefaultsLoading" class="loading-state">Loading agents...</div>

      <div v-else class="ai-providers-list">
        <div v-for="agent in agentDefaults" :key="agent.agentId" class="ai-provider-card">
          <div class="ai-provider-header">
            <span class="ai-provider-name">{{ agent.name }}</span>
            <span v-if="agent.model" class="badge-role admin">Custom</span>
            <span v-else class="badge-role user">Devpad default</span>
          </div>
          <p v-if="agent.purpose" class="agent-purpose">{{ agent.purpose }}</p>
          <form v-if="agentDefaultForms[agent.agentId]" @submit.prevent="saveAgentDefaults(agent.agentId)">
            <div class="agent-default-fields">
              <div class="form-field">
                <label :for="'agent-model-' + agent.agentId">Model</label>
                <select
                  :id="'agent-model-' + agent.agentId"
                  v-model="agentDefaultForms[agent.agentId].model"
                  @change="onAgentModelChange(agent.agentId)"
                >
                  <option value="">Devpad default</option>
                  <option v-for="m in agentModelOptions(agent.agentId)" :key="m.id" :value="m.id">
                    {{ m.name }} · {{ m.providerName }}{{ m.configured ? '' : ' (not configured)' }}
                  </option>
                </select>
              </div>
              <div v-if="agentEffortOptions(agent.agentId).length > 0" class="form-field">
                <label :for="'agent-effort-' + agent.agentId">Reasoning effort</label>
                <select :id="'agent-effort-' + agent.agentId" v-model="agentDefaultForms[agent.agentId].effort">
                  <option value="">Model default</option>
                  <option v-for="e in agentEffortOptions(agent.agentId)" :key="e" :value="e">{{ thinkingEffortLabel(e) }}</option>
                </select>
              </div>
            </div>
            <div class="ai-provider-actions">
              <span class="agent-default-hint">
                <template v-if="agentDefaultModel(agent.agentId) && !agentDefaultModel(agent.agentId)?.configured">
                  This model's provider is not configured, so the default is skipped until it is.
                </template>
              </span>
              <button type="submit" class="btn-primary" :disabled="agentDefaultsSubmitting === agent.agentId">
                {{ agentDefaultsSubmitting === agent.agentId ? 'Saving...' : 'Save' }}
              </button>
            </div>
          </form>
        </div>
      </div>
    </div>

    <!-- AI Agent Budgets Section -->
    <div class="admin-container" style="margin-top: var(--space-6);">
      <div class="admin-header">
        <div>
          <h1 class="admin-title">AI Agent Budgets</h1>
          <p class="admin-subtitle">What agents may spend before they stop, counting the agents they start. Leave a budget empty for no limit. Requests to models without prices aren't counted, so those runs stop only at devpad's limit of 500 model requests per run.</p>
        </div>
      </div>

      <div v-if="budgetsError" class="alert alert-error">{{ budgetsError }}</div>
      <div v-if="budgetsSuccess" class="alert alert-success">{{ budgetsSuccess }}</div>

      <div v-if="budgetsLoading" class="loading-state">Loading budgets...</div>

      <form v-else class="ai-provider-card" @submit.prevent="saveBudgets">
        <div class="agent-default-fields">
          <div class="form-field">
            <label for="budget-task">Per task (USD)</label>
            <input id="budget-task" v-model="budgetForm.taskUsd" type="number" min="0" step="any" placeholder="No limit" />
            <p class="field-hint">Every run of a task's agent, across retries and rounds of feedback. New tasks start with this; it can be raised on each task.</p>
          </div>
          <div class="form-field">
            <label for="budget-review">Per review (USD)</label>
            <input id="budget-review" v-model="budgetForm.reviewUsd" type="number" min="0" step="any" placeholder="No limit" />
            <p class="field-hint">One review agent's run on a merge request.</p>
          </div>
          <div class="form-field">
            <label for="budget-assistant">Per assistant reply (USD)</label>
            <input id="budget-assistant" v-model="budgetForm.assistantUsd" type="number" min="0" step="any" placeholder="No limit" />
            <p class="field-hint">The assistant's work on one message in a workspace.</p>
          </div>
        </div>
        <div class="ai-provider-actions">
          <span class="agent-default-hint">A run that reaches its budget stops with an error after its current request.</span>
          <button type="submit" class="btn-primary" :disabled="budgetsSubmitting">
            {{ budgetsSubmitting ? 'Saving...' : 'Save' }}
          </button>
        </div>
      </form>
    </div>
  </div>
</template>

<style scoped>
.admin-page {
  height: 100%;
  overflow-y: auto;
  padding: var(--space-6);
}

.admin-container {
  max-width: 960px;
  margin: 0 auto;
}

.admin-header {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  flex-wrap: wrap;
  gap: var(--space-3);
  margin-bottom: var(--space-6);
}

.admin-title {
  font-size: 1.5rem;
  font-weight: 500;
  color: var(--text-primary);
  margin-bottom: var(--space-1);
}

.admin-subtitle {
  color: var(--text-muted);
  font-size: 0.85rem;
}

.btn-create {
  padding: var(--space-2) var(--space-4);
  background: var(--accent);
  color: var(--bg-base);
  font-weight: 500;
  font-size: 0.85rem;
  border-radius: var(--radius-lg);
  transition: opacity var(--transition-fast);
  white-space: nowrap;
}

.btn-create:hover {
  opacity: 0.9;
}

.alert {
  padding: var(--space-3);
  margin-bottom: var(--space-4);
  border-radius: var(--radius-md);
  font-size: 0.85rem;
}

.alert-error {
  background: var(--error-bg);
  border: 0.5px solid var(--error-border);
  color: var(--accent-rose);
}

.loading-state {
  text-align: center;
  padding: var(--space-8);
  color: var(--text-muted);
}

.users-table-wrap {
  background: var(--bg-surface);
  border: 0.5px solid var(--border-default);
  border-radius: var(--radius-lg);
  overflow-x: auto;
}

.users-table {
  width: 100%;
  border-collapse: collapse;
}

.users-table th {
  text-align: left;
  padding: var(--space-3) var(--space-4);
  font-size: 0.75rem;
  font-weight: 600;
  text-transform: uppercase;
  letter-spacing: 0.05em;
  color: var(--text-muted);
  background: var(--bg-surface-alt);
  border-bottom: 0.5px solid var(--border-default);
}

.users-table td {
  padding: var(--space-3) var(--space-4);
  font-size: 0.85rem;
  color: var(--text-secondary);
  border-bottom: 0.5px solid var(--border-default);
}

.users-table tr:last-child td {
  border-bottom: none;
}

.users-table tr:hover td {
  background: var(--bg-hover);
}

.cell-username {
  color: var(--text-primary);
  font-weight: 500;
}

.cell-email {
  font-family: var(--font-mono);
  font-size: 0.8rem;
}

.cell-date {
  font-size: 0.8rem;
  color: var(--text-muted);
}

.badge-you {
  display: inline-block;
  margin-left: var(--space-2);
  vertical-align: middle;
  padding: 1px 6px;
  font-size: 0.72rem;
  font-weight: 600;
  text-transform: uppercase;
  letter-spacing: 0.05em;
  border-radius: var(--radius-sm);
  background: var(--accent-glow);
  color: var(--accent-blue);
  border: 0.5px solid var(--accent-border);
}

.badge-role {
  display: inline-block;
  padding: 2px 8px;
  font-size: 0.75rem;
  font-weight: 500;
  border-radius: var(--radius-sm);
}

.badge-role.admin {
  background: var(--accent-glow);
  color: var(--accent);
  border: 0.5px solid var(--accent-border);
}

.badge-role.user {
  background: var(--bg-neutral);
  color: var(--text-secondary);
  border: 0.5px solid var(--border-default);
}

.cell-actions {
  display: flex;
  gap: var(--space-1);
}

.btn-action {
  padding: var(--space-1) var(--space-2);
  font-size: 0.75rem;
  color: var(--text-secondary);
  border: 0.5px solid var(--border-default);
  border-radius: var(--radius-sm);
  transition: all var(--transition-fast);
  white-space: nowrap;
}

.btn-action:hover:not(:disabled) {
  color: var(--text-primary);
  border-color: var(--border-active);
  background: var(--bg-hover);
}

.btn-action.btn-danger:hover:not(:disabled) {
  color: var(--accent-rose);
  border-color: var(--error-border);
  background: var(--error-bg);
}

.btn-action:disabled {
  opacity: 0.3;
  cursor: not-allowed;
}

.empty-state {
  text-align: center;
  padding: var(--space-8);
  color: var(--text-muted);
  font-size: 0.9rem;
}

/* Modal styles */
.modal-overlay {
  backdrop-filter: blur(4px);
}

.modal {
  width: 100%;
  max-width: 420px;
  padding: var(--space-6);
  background: var(--bg-surface);
  border: 0.5px solid var(--border-default);
  border-radius: var(--radius-lg);
  box-shadow: var(--shadow-strong);
}

.modal-title {
  font-size: 1.1rem;
  font-weight: 600;
  color: var(--text-primary);
  margin-bottom: var(--space-2);
}

.modal-subtitle {
  color: var(--text-secondary);
  font-size: 0.85rem;
  margin-bottom: var(--space-4);
  line-height: 1.5;
}

.modal-subtitle strong {
  color: var(--text-primary);
}

.form-field {
  margin-bottom: var(--space-4);
}

.form-field label {
  display: block;
  margin-bottom: var(--space-1);
  font-size: 0.85rem;
  font-weight: 500;
  color: var(--text-secondary);
}

.form-field input[type='text'],
.form-field input[type='email'],
.form-field input[type='password'] {
  width: 100%;
  padding: var(--space-2) var(--space-3);
  background: var(--bg-primary);
  border: 0.5px solid var(--border-default);
  border-radius: var(--radius-md);
  color: var(--text-primary);
  font-family: inherit;
  font-size: 0.9rem;
  transition: border-color var(--transition-fast);
}

.form-field input:focus {
  outline: none;
  border-color: var(--accent-border);
  box-shadow: 0 0 0 3px var(--accent-glow);
}

.form-field input::placeholder {
  color: var(--text-muted);
}

.form-checkbox label {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  cursor: pointer;
}

.form-checkbox input[type='checkbox'] {
  width: 16px;
  height: 16px;
  accent-color: var(--accent-purple);
  cursor: pointer;
}

.form-checkbox span {
  font-size: 0.85rem;
  color: var(--text-secondary);
}

.field-hint {
  margin-top: var(--space-1);
  font-size: 0.75rem;
  color: var(--text-muted);
}

.modal-actions {
  display: flex;
  justify-content: flex-end;
  gap: var(--space-2);
  margin-top: var(--space-5);
}

.btn-primary {
  padding: var(--space-2) var(--space-4);
  background: var(--accent);
  color: var(--bg-base);
  font-weight: 500;
  font-size: 0.85rem;
  border-radius: var(--radius-lg);
  transition: opacity var(--transition-fast);
}

.btn-primary:hover:not(:disabled) {
  opacity: 0.9;
}

.btn-primary:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

.btn-secondary {
  padding: var(--space-2) var(--space-4);
  font-size: 0.85rem;
  color: var(--text-secondary);
  border: 0.5px solid var(--border-default);
  border-radius: var(--radius-md);
  transition: all var(--transition-fast);
}

.btn-secondary:hover {
  color: var(--text-primary);
  border-color: var(--border-active);
  background: var(--bg-hover);
}

.btn-delete {
  padding: var(--space-2) var(--space-4);
  font-weight: 600;
  font-size: 0.85rem;
  border-radius: var(--radius-md);
  background: var(--error-bg);
  color: var(--accent-rose);
  border: 0.5px solid var(--error-border);
  transition: all var(--transition-fast);
}

.btn-delete:hover:not(:disabled) {
  background: var(--error-bg);
}

.btn-delete:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

.alert-success {
  background: var(--success-bg);
  border: 0.5px solid var(--success-border);
  color: var(--accent-green);
  padding: var(--space-3);
  margin-bottom: var(--space-4);
  border-radius: var(--radius-md);
  font-size: 0.85rem;
}

/* AI model pricing */
.cell-price {
  text-align: right;
  white-space: nowrap;
}

.cell-model-id {
  font-family: var(--font-mono);
  font-size: 0.75rem;
  font-weight: 400;
  color: var(--text-muted);
}

.cell-long-context {
  font-size: 0.72rem;
  color: var(--text-muted);
}

/* AI agent defaults */
.agent-purpose {
  margin: 0 0 var(--space-3);
  font-size: 0.85rem;
  color: var(--text-muted);
}

.agent-default-fields {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(220px, 1fr));
  column-gap: var(--space-3);
}

.form-field select {
  width: 100%;
  padding: var(--space-2) var(--space-3);
  background: var(--bg-primary);
  border: 0.5px solid var(--border-default);
  border-radius: var(--radius-md);
  color: var(--text-primary);
  font-family: inherit;
  font-size: 0.9rem;
  transition: border-color var(--transition-fast);
}

.form-field select:focus {
  outline: none;
  border-color: var(--accent-border);
  box-shadow: 0 0 0 3px var(--accent-glow);
}

.agent-default-hint {
  font-size: 0.8rem;
  color: var(--accent-amber);
}

/* AI Providers */
.ai-providers-list {
  display: flex;
  flex-direction: column;
  gap: var(--space-4);
}

.ai-provider-card {
  padding: var(--space-4);
  background: var(--bg-surface);
  border: 0.5px solid var(--border-default);
  border-radius: var(--radius-lg);
}

.ai-provider-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: var(--space-3);
}

.ai-provider-name {
  font-weight: 600;
  font-size: 0.95rem;
  color: var(--text-primary);
}

.ai-provider-actions {
  display: flex;
  align-items: center;
  justify-content: space-between;
  flex-wrap: wrap;
  gap: var(--space-2);
}

.toggle-label {
  display: inline-flex;
  align-items: center;
  gap: var(--space-2);
  font-size: 0.85rem;
  color: var(--text-secondary);
  cursor: pointer;
  user-select: none;
}

.toggle-input {
  position: absolute;
  opacity: 0;
  width: 0;
  height: 0;
}

.toggle-switch {
  position: relative;
  width: 36px;
  height: 20px;
  background: var(--bg-surface-alt);
  border-radius: 10px;
  border: 0.5px solid var(--border-default);
  transition: background var(--transition-fast);
}

.toggle-switch::after {
  content: '';
  position: absolute;
  top: 2px;
  left: 2px;
  width: 14px;
  height: 14px;
  border-radius: 50%;
  background: var(--text-muted);
  transition: transform var(--transition-fast), background var(--transition-fast);
}

.toggle-input:checked + .toggle-switch {
  background: var(--success-bg);
  border-color: var(--success-border);
}

.toggle-input:checked + .toggle-switch::after {
  transform: translateX(16px);
  background: var(--accent-green);
}

.badge-status {
  display: inline-block;
  padding: 2px 8px;
  font-size: 0.75rem;
  font-weight: 500;
  border-radius: var(--radius-sm);
  text-transform: capitalize;
}

.badge-status.running {
  background: var(--success-bg);
  color: var(--accent-green);
  border: 0.5px solid var(--success-border);
}

.badge-status.stopped {
  background: var(--bg-neutral);
  color: var(--text-secondary);
  border: 0.5px solid var(--border-default);
}

.badge-status.creating {
  background: var(--warning-bg);
  color: var(--accent-amber);
  border: 0.5px solid var(--warning-border);
}

.cell-limit {
  font-family: var(--font-mono);
  font-size: 0.8rem;
  color: var(--text-secondary);
}

.form-field input[type='number'] {
  width: 100%;
  padding: var(--space-2) var(--space-3);
  background: var(--bg-primary);
  border: 0.5px solid var(--border-default);
  border-radius: var(--radius-md);
  color: var(--text-primary);
  font-family: var(--font-mono);
  font-size: 0.9rem;
  transition: border-color var(--transition-fast);
}

.form-field input[type='number']:focus {
  outline: none;
  border-color: var(--accent-border);
  box-shadow: 0 0 0 3px var(--accent-glow);
}

@media (max-width: 768px) {
  .admin-page {
    padding: var(--space-4) var(--space-3);
  }

  .users-table th,
  .users-table td {
    padding: var(--space-2) var(--space-3);
    white-space: nowrap;
  }
}
</style>
