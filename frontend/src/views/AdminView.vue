<script setup lang="ts">
import { ref, onMounted, computed } from 'vue'
import { useAdminStore } from '@/stores/admin'
import { useAuthStore } from '@/stores/auth'
import type { AdminInvite, AdminUser, AdminWorkspace, CreatedInvite } from '@/api/admin'
import { writeClipboardText } from '@/utils/clipboard'
import SelectMenu, { type SelectMenuOption } from '@/components/SelectMenu.vue'

const admin = useAdminStore()
const auth = useAuthStore()

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

// Invite modal: a form, then the new link, which is shown only this once.
const showInviteModal = ref(false)
const inviteForm = ref({ isAdmin: false, validDays: 7 })
const inviteDayOptions: SelectMenuOption<number>[] = [
  { value: 1, label: '1 day' },
  { value: 7, label: '7 days' },
  { value: 30, label: '30 days' },
]
const inviteError = ref('')
const inviteSubmitting = ref(false)
const createdInvite = ref<CreatedInvite | null>(null)
const inviteCopied = ref(false)
const revokingInviteId = ref<number | null>(null)

const inviteLink = computed(() =>
  createdInvite.value ? `${window.location.origin}/invite#${createdInvite.value.token}` : '',
)

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
  admin.fetchInvites()
})

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

function openInvite() {
  inviteForm.value = { isAdmin: false, validDays: 7 }
  inviteError.value = ''
  createdInvite.value = null
  inviteCopied.value = false
  showInviteModal.value = true
}

function closeInviteModal() {
  showInviteModal.value = false
  // Drop the token from memory once the dialog is closed.
  createdInvite.value = null
  inviteError.value = ''
}

async function handleCreateInvite() {
  inviteError.value = ''
  inviteSubmitting.value = true
  try {
    createdInvite.value = await admin.createInvite({ ...inviteForm.value })
  } catch (e) {
    inviteError.value = e instanceof Error ? e.message : 'Failed to create invite'
  } finally {
    inviteSubmitting.value = false
  }
}

async function copyInviteLink() {
  try {
    await writeClipboardText(inviteLink.value)
    inviteCopied.value = true
  } catch {
    inviteError.value = 'Could not copy; select the link and copy it yourself'
  }
}

async function revokeInvite(invite: AdminInvite) {
  revokingInviteId.value = invite.id
  admin.invitesError = ''
  try {
    await admin.revokeInvite(invite.id)
  } catch (e) {
    admin.invitesError = e instanceof Error ? e.message : 'Failed to revoke invite'
  } finally {
    revokingInviteId.value = null
  }
}

function formatDateTime(iso: string): string {
  return new Date(iso).toLocaleString(undefined, { dateStyle: 'medium', timeStyle: 'short' })
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
        <div class="header-actions">
          <button class="btn-secondary" @click="openInvite">Invite User</button>
          <button class="btn-create" @click="openCreate">+ New User</button>
        </div>
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

    <!-- Invites Section -->
    <div class="admin-container section">
      <div class="admin-header">
        <div>
          <h1 class="admin-title">Pending Invites</h1>
          <p class="admin-subtitle">Invite links nobody has used yet. Revoking one stops its link working.</p>
        </div>
      </div>

      <div v-if="admin.invitesError" class="alert alert-error">{{ admin.invitesError }}</div>

      <div v-if="admin.invitesLoading" class="loading-state">Loading invites...</div>

      <div v-else class="users-table-wrap">
        <table v-if="admin.invites.length > 0" class="users-table">
          <thead>
            <tr>
              <th>Role</th>
              <th>Invited by</th>
              <th>Expires</th>
              <th>Actions</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="invite in admin.invites" :key="invite.id">
              <td>
                <span class="badge-role" :class="invite.isAdmin ? 'admin' : 'user'">
                  {{ invite.isAdmin ? 'Admin' : 'User' }}
                </span>
              </td>
              <td>{{ invite.createdBy || 'Deleted user' }}</td>
              <td class="cell-date">
                <span v-if="invite.expired" class="badge-status expired">Expired</span>
                <template v-else>{{ formatDateTime(invite.expiresAt) }}</template>
              </td>
              <td class="cell-actions">
                <button
                  class="btn-action btn-danger"
                  :title="invite.expired ? 'Remove invite' : 'Revoke invite'"
                  :disabled="revokingInviteId === invite.id"
                  @click="revokeInvite(invite)"
                >
                  {{ invite.expired ? 'Remove' : 'Revoke' }}
                </button>
              </td>
            </tr>
          </tbody>
        </table>
        <div v-else class="empty-state">No pending invites.</div>
      </div>
    </div>

    <!-- Invite User Modal -->
    <Teleport to="body">
      <div v-if="showInviteModal" class="modal-overlay" @click.self="closeInviteModal">
        <div class="modal">
          <template v-if="!createdInvite">
            <h2 class="modal-title">Invite User</h2>
            <p class="modal-subtitle">
              Make a one-time link someone can use to create their own account, choosing their own username and
              email. Send it to them yourself.
            </p>
            <div v-if="inviteError" class="alert alert-error">{{ inviteError }}</div>
            <form @submit.prevent="handleCreateInvite">
              <div class="form-field">
                <label for="invite-valid">Link expires after</label>
                <SelectMenu
                  id="invite-valid"
                  v-model="inviteForm.validDays"
                  :options="inviteDayOptions"
                  title="Link expires after"
                  variant="field"
                />
              </div>
              <div class="form-field form-checkbox">
                <label>
                  <input v-model="inviteForm.isAdmin" type="checkbox" />
                  <span>Admin privileges</span>
                </label>
              </div>
              <div class="modal-actions">
                <button type="button" class="btn-secondary" @click="closeInviteModal">Cancel</button>
                <button type="submit" class="btn-primary" :disabled="inviteSubmitting">
                  {{ inviteSubmitting ? 'Creating...' : 'Create Invite Link' }}
                </button>
              </div>
            </form>
          </template>

          <template v-else>
            <h2 class="modal-title">Invite Link</h2>
            <p class="modal-subtitle">
              Anyone with this link can create
              <template v-if="createdInvite.isAdmin"><strong>an admin account</strong></template>
              <template v-else>an account</template>,
              once, until {{ formatDateTime(createdInvite.expiresAt) }}.
              <strong>This is the only time it's shown.</strong>
            </p>
            <div v-if="inviteError" class="alert alert-error">{{ inviteError }}</div>
            <div class="invite-link-row">
              <input
                class="invite-link"
                :value="inviteLink"
                readonly
                aria-label="Invite link"
                @focus="($event.target as HTMLInputElement).select()"
              />
              <button type="button" class="btn-primary" @click="copyInviteLink">
                {{ inviteCopied ? 'Copied' : 'Copy' }}
              </button>
            </div>
            <div class="modal-actions">
              <button type="button" class="btn-secondary" @click="closeInviteModal">Done</button>
            </div>
          </template>
        </div>
      </div>
    </Teleport>

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
    <div class="admin-container section">
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
  color: var(--text-tertiary);
  font-size: 0.85rem;
}

.header-actions {
  display: flex;
  gap: var(--space-2);
}

.section {
  margin-top: var(--space-6);
}

.invite-link-row {
  display: flex;
  gap: var(--space-2);
}

.invite-link {
  flex: 1;
  min-width: 0;
  padding: var(--space-2) var(--space-3);
  background: var(--bg-base);
  border: 0.5px solid var(--border-subtle);
  border-radius: var(--radius-md);
  color: var(--text-primary);
  font-family: var(--font-mono);
  font-size: 0.8rem;
}

.invite-link:focus {
  outline: none;
  border-color: var(--accent-border);
  box-shadow: 0 0 0 3px var(--accent-glow);
}

.badge-status.expired {
  background: var(--bg-neutral);
  color: var(--text-secondary);
  border: 0.5px solid var(--border-subtle);
  text-transform: none;
}

.btn-create {
  padding: var(--space-2) var(--space-4);
  background: var(--accent);
  color: var(--text-on-accent);
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
  color: var(--error);
}

.loading-state {
  text-align: center;
  padding: var(--space-8);
  color: var(--text-tertiary);
}

.users-table-wrap {
  background: var(--bg-elevated);
  border: 0.5px solid var(--border-subtle);
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
  color: var(--text-tertiary);
  background: var(--bg-raised);
  border-bottom: 0.5px solid var(--border-subtle);
}

.users-table td {
  padding: var(--space-3) var(--space-4);
  font-size: 0.85rem;
  color: var(--text-secondary);
  border-bottom: 0.5px solid var(--border-subtle);
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
  color: var(--text-tertiary);
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
  color: var(--accent);
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
  border: 0.5px solid var(--border-subtle);
}

.cell-actions {
  display: flex;
  gap: var(--space-1);
}

.btn-action {
  padding: var(--space-1) var(--space-2);
  font-size: 0.75rem;
  color: var(--text-secondary);
  border: 0.5px solid var(--border-subtle);
  border-radius: var(--radius-sm);
  transition: all var(--transition-fast);
  white-space: nowrap;
}

.btn-action:hover:not(:disabled) {
  color: var(--text-primary);
  border-color: var(--border-strong);
  background: var(--bg-hover);
}

.btn-action.btn-danger:hover:not(:disabled) {
  color: var(--error);
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
  color: var(--text-tertiary);
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
  background: var(--bg-elevated);
  border: 0.5px solid var(--border-subtle);
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
  background: var(--bg-base);
  border: 0.5px solid var(--border-subtle);
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
  color: var(--text-tertiary);
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
  accent-color: var(--accent);
  cursor: pointer;
}

.form-checkbox span {
  font-size: 0.85rem;
  color: var(--text-secondary);
}

.field-hint {
  margin-top: var(--space-1);
  font-size: 0.75rem;
  color: var(--text-tertiary);
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
  color: var(--text-on-accent);
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
  border: 0.5px solid var(--border-subtle);
  border-radius: var(--radius-md);
  transition: all var(--transition-fast);
}

.btn-secondary:hover {
  color: var(--text-primary);
  border-color: var(--border-strong);
  background: var(--bg-hover);
}

.btn-delete {
  padding: var(--space-2) var(--space-4);
  font-weight: 600;
  font-size: 0.85rem;
  border-radius: var(--radius-md);
  background: var(--error-bg);
  color: var(--error);
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
  color: var(--success);
  border: 0.5px solid var(--success-border);
}

.badge-status.stopped {
  background: var(--bg-neutral);
  color: var(--text-secondary);
  border: 0.5px solid var(--border-subtle);
}

.badge-status.creating {
  background: var(--warning-bg);
  color: var(--warning);
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
  background: var(--bg-base);
  border: 0.5px solid var(--border-subtle);
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
