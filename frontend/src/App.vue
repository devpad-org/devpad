<script setup lang="ts">
import { RouterView, RouterLink } from 'vue-router'
import { useRouter, useRoute } from 'vue-router'
import { computed, watch } from 'vue'
import { useAuthStore } from '@/stores/auth'
import { useAttentionStore } from '@/stores/attention'

const auth = useAuthStore()
const router = useRouter()
const route = useRoute()

const attention = useAttentionStore()

const hideChrome = computed(() => route.meta.hideChrome === true)

// The header, and the IDE's title bar where the header is hidden, show how
// many agents are waiting for the user.
watch(
  () => auth.isAuthenticated,
  (showing, wasShowing) => {
    if (showing) attention.watch()
    else if (wasShowing) attention.unwatch()
  },
  { immediate: true },
)

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
        <RouterLink
          v-if="attention.count > 0"
          :to="{ name: 'home', hash: '#inbox' }"
          class="nav-badge"
          :title="`${attention.count} waiting for you`"
          :aria-label="`Inbox, ${attention.count} waiting for you`"
        >
          {{ attention.count }}
        </RouterLink>
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
  border-bottom: 0.5px solid var(--border-default);
  flex-shrink: 0;
}

.header-left {
  display: flex;
  align-items: center;
  gap: var(--space-4);
}

.header-brand {
  font-weight: 500;
  font-size: 0.85rem;
  color: var(--text-primary);
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

/* How many agents are waiting for the user; links to the launcher's inbox. */
.nav-badge {
  display: inline-block;
  min-width: 20px;
  margin-right: var(--space-2);
  padding: 0 var(--space-1);
  border-radius: var(--radius-sm);
  background: var(--attention);
  color: var(--text-on-accent);
  font-size: 0.75rem;
  font-weight: 600;
  line-height: 20px;
  text-align: center;
}

.nav-badge:hover {
  color: var(--text-on-accent);
  filter: brightness(1.1);
}

.nav-link.router-link-active {
  color: var(--accent-blue);
  background: var(--accent-glow);
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
  border: 0.5px solid var(--border-default);
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

@media (max-width: 768px) {
  .app-header {
    gap: var(--space-2);
    padding: var(--space-2) var(--space-3);
  }

  .header-left {
    gap: var(--space-2);
    min-width: 0;
  }

  .nav-link,
  .btn-logout {
    padding: var(--space-2) var(--space-3);
  }
}

@media (max-width: 480px) {
  .header-username {
    display: none;
  }

  .nav-link {
    padding: var(--space-2);
  }
}
</style>
