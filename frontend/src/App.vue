<script setup lang="ts">
import { RouterView, RouterLink } from 'vue-router'
import { useRouter, useRoute } from 'vue-router'
import { computed } from 'vue'
import { useAuthStore } from '@/stores/auth'

const auth = useAuthStore()
const router = useRouter()
const route = useRoute()

const hideChrome = computed(() => route.meta.hideChrome === true)

async function handleLogout() {
  await auth.logout()
  router.push({ name: 'login' })
}
</script>

<template>
  <header v-if="auth.isAuthenticated && !hideChrome" class="app-header">
    <div class="header-left">
      <RouterLink to="/" class="header-brand">Devpad</RouterLink>
      <nav class="header-nav">
        <RouterLink to="/settings" class="nav-link">Settings</RouterLink>
        <RouterLink v-if="auth.isAdmin" to="/admin" class="nav-link">Admin</RouterLink>
      </nav>
    </div>
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

.header-left {
  display: flex;
  align-items: center;
  gap: var(--space-4);
}

.header-brand {
  font-weight: 700;
  font-size: 0.95rem;
  background: linear-gradient(135deg, var(--accent-blue), var(--accent-purple));
  -webkit-background-clip: text;
  background-clip: text;
  -webkit-text-fill-color: transparent;
}

.header-nav {
  display: flex;
  align-items: center;
  gap: var(--space-1);
}

.nav-link {
  padding: var(--space-1) var(--space-3);
  font-size: 0.8rem;
  color: var(--text-secondary);
  border-radius: var(--radius-md);
  transition: all var(--transition-fast);
}

.nav-link:hover {
  color: var(--text-primary);
  background: var(--bg-hover);
}

.nav-link.router-link-active {
  color: var(--accent-blue);
  background: rgba(0, 212, 255, 0.08);
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
