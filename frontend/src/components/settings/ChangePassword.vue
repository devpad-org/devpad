<script setup lang="ts">
import { ref } from 'vue'
import { settingsApi } from '@/api/settings'
import { passwordProblem } from '@/utils/password'

const currentPassword = ref('')
const newPassword = ref('')
const confirmPassword = ref('')
const error = ref('')
const success = ref('')
const submitting = ref(false)

async function save() {
  error.value = ''
  success.value = ''

  if (!currentPassword.value || !newPassword.value || !confirmPassword.value) {
    error.value = 'All fields are required'
    return
  }
  const problem = passwordProblem(newPassword.value)
  if (problem) {
    error.value = problem
    return
  }
  if (newPassword.value !== confirmPassword.value) {
    error.value = 'Passwords do not match'
    return
  }

  submitting.value = true
  try {
    await settingsApi.changePassword(currentPassword.value, newPassword.value)
    success.value = 'Password changed successfully'
    currentPassword.value = ''
    newPassword.value = ''
    confirmPassword.value = ''
  } catch (e) {
    error.value = e instanceof Error ? e.message : 'Failed to change password'
  } finally {
    submitting.value = false
  }
}
</script>

<template>
  <section class="card">
    <div class="card-header">
      <h3 class="card-name">Password</h3>
    </div>
    <p class="card-desc">Update your account password. It must be at least 8 characters.</p>

    <div v-if="error" class="alert alert-error">{{ error }}</div>
    <div v-if="success" class="alert alert-success">{{ success }}</div>

    <form @submit.prevent="save">
      <div class="form-field">
        <label for="current-password">Current password</label>
        <input
          id="current-password"
          v-model="currentPassword"
          type="password"
          autocomplete="current-password"
          required
          placeholder="Enter current password"
        />
      </div>
      <div class="card-fields">
        <div class="form-field">
          <label for="new-password">New password</label>
          <input
            id="new-password"
            v-model="newPassword"
            type="password"
            autocomplete="new-password"
            required
            placeholder="At least 8 characters"
          />
        </div>
        <div class="form-field">
          <label for="confirm-password">Confirm new password</label>
          <input
            id="confirm-password"
            v-model="confirmPassword"
            type="password"
            autocomplete="new-password"
            required
            placeholder="Repeat new password"
          />
        </div>
      </div>
      <div class="card-actions">
        <div class="card-buttons">
          <button type="submit" class="btn-primary" :disabled="submitting">
            {{ submitting ? 'Changing...' : 'Change password' }}
          </button>
        </div>
      </div>
    </form>
  </section>
</template>

<style scoped src="./settings.css"></style>
