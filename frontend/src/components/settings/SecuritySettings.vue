<script setup lang="ts">
import ChangePassword from './ChangePassword.vue'
import TwoFactorAuth from './TwoFactorAuth.vue'
</script>

<template>
  <div>
    <header class="page-header">
      <h2 class="page-title">Security</h2>
      <p class="page-desc">How you sign in to devpad.</p>
    </header>

    <div class="card-list">
      <ChangePassword />
      <TwoFactorAuth />
    </div>
  </div>
</template>

<style scoped src="./settings.css"></style>
