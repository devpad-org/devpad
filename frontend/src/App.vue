<script setup lang="ts">
import { RouterView } from 'vue-router'
import { useAuthStore } from '@/stores/auth'

const auth = useAuthStore()

async function handleLogout() {
  await auth.logout()
}
</script>

<template>
  <header v-if="auth.isAuthenticated" class="app-header">
    <div class="header-brand">Devpad</div>
    <div class="header-user">
      <span class="header-username">{{ auth.user?.username }}</span>
      <button class="btn-logout" @click="handleLogout">Sign out</button>
    </div>
  </header>
  <main class="app-main">
    <RouterView />
  </main>
</template>

<style scoped>
.app-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: var(--space-2) var(--space-4);
  background: var(--bg-surface);
  border-bottom: 1px solid var(--border-default);
  flex-shrink: 0;
}

.header-brand {
  font-weight: 700;
  font-size: 0.95rem;
  background: linear-gradient(135deg, var(--accent-blue), var(--accent-purple));
  -webkit-background-clip: text;
  background-clip: text;
  -webkit-text-fill-color: transparent;
}

.header-user {
  display: flex;
  align-items: center;
  gap: var(--space-3);
}

.header-username {
  color: var(--text-secondary);
  font-size: 0.85rem;
}

.btn-logout {
  padding: var(--space-1) var(--space-3);
  font-size: 0.8rem;
  color: var(--text-secondary);
  border: 1px solid var(--border-default);
  border-radius: var(--radius-md);
  transition: all var(--transition-fast);
}

.btn-logout:hover {
  color: var(--text-primary);
  border-color: var(--border-active);
  background: var(--bg-hover);
}

.app-main {
  flex: 1;
  overflow: auto;
}
</style>
