<script setup lang="ts">
import { RouterView, RouterLink } from 'vue-router'
import { useRouter, useRoute } from 'vue-router'
import { computed, watch } from 'vue'
import { useAuthStore } from '@/stores/auth'
import { useAttentionStore } from '@/stores/attention'
import InboxPopover from '@/components/inbox/InboxPopover.vue'

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
        <RouterLink to="/usage" class="nav-link">Usage</RouterLink>
        <RouterLink to="/settings" class="nav-link">Settings</RouterLink>
        <RouterLink v-if="auth.isAdmin" to="/admin" class="nav-link">Admin</RouterLink>
      </nav>
    </div>
    <div class="header-user">
      <InboxPopover>
        <template #trigger="{ open, toggle }">
          <button
            class="nav-inbox"
            :class="{ 'has-items': attention.count > 0, active: open }"
            :title="attention.count > 0 ? `Inbox: ${attention.count} waiting for you` : 'Inbox'"
            :aria-label="attention.count > 0 ? `Inbox, ${attention.count} waiting for you` : 'Inbox'"
            :aria-expanded="open"
            aria-haspopup="dialog"
            @click="toggle"
          >
            <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round">
              <path d="M22 12h-6l-2 3h-4l-2-3H2" />
              <path d="M5.45 5.11 2 12v6a2 2 0 0 0 2 2h16a2 2 0 0 0 2-2v-6l-3.45-6.89A2 2 0 0 0 16.76 4H7.24a2 2 0 0 0-1.79 1.11z" />
            </svg>
            <span v-if="attention.count > 0" class="nav-inbox-badge">{{ attention.count }}</span>
          </button>
        </template>
      </InboxPopover>
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

/* Opens the inbox, with how many agents are waiting for the user. */
.nav-inbox {
  position: relative;
  display: flex;
  align-items: center;
  justify-content: center;
  width: 30px;
  height: 30px;
  border-radius: var(--radius-md);
  color: var(--text-secondary);
  transition: all var(--transition-fast);
}

.nav-inbox:hover,
.nav-inbox.active {
  color: var(--text-primary);
  background: var(--bg-hover);
}

.nav-inbox.has-items {
  color: var(--attention);
}

.nav-inbox-badge {
  position: absolute;
  top: 1px;
  right: 0;
  min-width: 14px;
  height: 14px;
  padding: 0 3px;
  border-radius: 999px;
  background: var(--attention);
  color: var(--text-on-accent);
  font-size: 0.6rem;
  font-weight: 700;
  line-height: 14px;
  text-align: center;
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
