<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { useAdminStore } from '@/stores/admin'
import { useAuthStore } from '@/stores/auth'
import type { AdminUser } from '@/api/admin'

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

onMounted(() => {
  admin.fetchUsers()
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
        <div class="modal">
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
  margin-bottom: var(--space-6);
}

.admin-title {
  font-size: 1.5rem;
  font-weight: 700;
  color: var(--text-primary);
  margin-bottom: var(--space-1);
}

.admin-subtitle {
  color: var(--text-muted);
  font-size: 0.85rem;
}

.btn-create {
  padding: var(--space-2) var(--space-4);
  background: linear-gradient(135deg, var(--accent-blue), var(--accent-purple));
  color: white;
  font-weight: 600;
  font-size: 0.85rem;
  border-radius: var(--radius-md);
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
  background: rgba(244, 63, 94, 0.1);
  border: 1px solid rgba(244, 63, 94, 0.3);
  color: var(--accent-rose);
}

.loading-state {
  text-align: center;
  padding: var(--space-8);
  color: var(--text-muted);
}

.users-table-wrap {
  background: var(--bg-surface);
  border: 1px solid var(--border-default);
  border-radius: var(--radius-lg);
  overflow: hidden;
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
  border-bottom: 1px solid var(--border-default);
}

.users-table td {
  padding: var(--space-3) var(--space-4);
  font-size: 0.85rem;
  color: var(--text-secondary);
  border-bottom: 1px solid var(--border-default);
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
  display: flex;
  align-items: center;
  gap: var(--space-2);
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
  padding: 1px 6px;
  font-size: 0.65rem;
  font-weight: 600;
  text-transform: uppercase;
  letter-spacing: 0.05em;
  border-radius: var(--radius-sm);
  background: rgba(0, 212, 255, 0.1);
  color: var(--accent-blue);
  border: 1px solid rgba(0, 212, 255, 0.2);
}

.badge-role {
  display: inline-block;
  padding: 2px 8px;
  font-size: 0.75rem;
  font-weight: 500;
  border-radius: var(--radius-sm);
}

.badge-role.admin {
  background: rgba(124, 58, 237, 0.15);
  color: var(--accent-purple);
  border: 1px solid rgba(124, 58, 237, 0.3);
}

.badge-role.user {
  background: rgba(161, 161, 170, 0.1);
  color: var(--text-secondary);
  border: 1px solid var(--border-default);
}

.cell-actions {
  display: flex;
  gap: var(--space-1);
}

.btn-action {
  padding: var(--space-1) var(--space-2);
  font-size: 0.75rem;
  color: var(--text-secondary);
  border: 1px solid var(--border-default);
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
  border-color: rgba(244, 63, 94, 0.4);
  background: rgba(244, 63, 94, 0.08);
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
  position: fixed;
  inset: 0;
  background: rgba(0, 0, 0, 0.6);
  display: flex;
  align-items: center;
  justify-content: center;
  z-index: 1000;
  backdrop-filter: blur(4px);
}

.modal {
  width: 100%;
  max-width: 420px;
  padding: var(--space-6);
  background: var(--bg-surface);
  border: 1px solid var(--border-default);
  border-radius: var(--radius-lg);
  box-shadow: 0 16px 48px rgba(0, 0, 0, 0.4);
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
  border: 1px solid var(--border-default);
  border-radius: var(--radius-md);
  color: var(--text-primary);
  font-family: inherit;
  font-size: 0.9rem;
  transition: border-color var(--transition-fast);
}

.form-field input:focus {
  outline: none;
  border-color: var(--accent-blue);
  box-shadow: 0 0 0 2px rgba(0, 212, 255, 0.15);
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
  background: linear-gradient(135deg, var(--accent-blue), var(--accent-purple));
  color: white;
  font-weight: 600;
  font-size: 0.85rem;
  border-radius: var(--radius-md);
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
  border: 1px solid var(--border-default);
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
  background: rgba(244, 63, 94, 0.15);
  color: var(--accent-rose);
  border: 1px solid rgba(244, 63, 94, 0.3);
  transition: all var(--transition-fast);
}

.btn-delete:hover:not(:disabled) {
  background: rgba(244, 63, 94, 0.25);
}

.btn-delete:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}
</style>
