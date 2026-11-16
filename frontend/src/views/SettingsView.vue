<script setup lang="ts">
import { nextTick, ref, watch } from 'vue'
import { RouterLink, RouterView, useRoute } from 'vue-router'

// Each page is a child route of /settings, so it has its own URL and the
// back button moves between pages.
const groups = [
  {
    label: 'Account',
    pages: [
      { name: 'settings-security', label: 'Security' },
      { name: 'settings-ssh-key', label: 'SSH Key' },
    ],
  },
  {
    label: 'AI',
    pages: [
      { name: 'settings-ai-keys', label: 'Provider Keys' },
      { name: 'settings-ai-models', label: 'Default Models' },
      { name: 'settings-ai-budgets', label: 'Budgets' },
      { name: 'settings-ai-pricing', label: 'Model Pricing' },
    ],
  },
]

// On narrow screens the nav is a strip of tabs that scrolls sideways, so
// bring the open page's tab into view, as when a link opens a later page.
const route = useRoute()
const nav = ref<HTMLElement | null>(null)
watch(
  () => route.name,
  async () => {
    await nextTick()
    nav.value?.querySelector('.active')?.scrollIntoView({ block: 'nearest', inline: 'nearest' })
  },
  { immediate: true },
)
</script>

<template>
  <div class="settings-page">
    <div class="settings-layout">
      <aside class="settings-sidebar">
        <h1 class="settings-title">Settings</h1>
        <nav ref="nav" class="settings-nav" aria-label="Settings">
          <div v-for="group in groups" :key="group.label" class="nav-group">
            <span class="nav-group-label">{{ group.label }}</span>
            <RouterLink
              v-for="page in group.pages"
              :key="page.name"
              :to="{ name: page.name }"
              class="nav-item"
              active-class="active"
            >
              {{ page.label }}
            </RouterLink>
          </div>
        </nav>
      </aside>

      <div class="settings-content">
        <RouterView />
      </div>
    </div>
  </div>
</template>

<style scoped>
.settings-page {
  height: 100%;
  overflow-y: auto;
  padding: var(--space-6);
}

.settings-layout {
  display: flex;
  align-items: flex-start;
  gap: var(--space-8);
  max-width: 960px;
  margin: 0 auto;
}

.settings-sidebar {
  position: sticky;
  top: 0;
  flex: 0 0 190px;
}

.settings-title {
  font-size: 1.5rem;
  font-weight: 500;
  color: var(--text-primary);
  margin-bottom: var(--space-4);
}

.settings-nav {
  display: flex;
  flex-direction: column;
  gap: var(--space-4);
}

.nav-group {
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.nav-group-label {
  padding: 0 var(--space-3) var(--space-1);
  font-size: 0.7rem;
  font-weight: 600;
  text-transform: uppercase;
  letter-spacing: 0.06em;
  color: var(--text-muted);
}

.nav-item {
  position: relative;
  padding: 6px var(--space-3);
  font-size: 0.875rem;
  color: var(--text-secondary);
  text-decoration: none;
  border-radius: var(--radius-md);
  transition:
    background var(--transition-fast),
    color var(--transition-fast);
}

.nav-item:hover {
  color: var(--text-primary);
  background: var(--bg-hover);
}

.nav-item.active {
  color: var(--accent);
  background: var(--bg-selected);
  font-weight: 500;
}

.settings-content {
  flex: 1;
  min-width: 0;
  max-width: 720px;
  /* Lines the page title up with the "Settings" heading. */
  padding-top: 2px;
}

/* On narrow screens the nav becomes a strip of tabs above the page. */
@media (max-width: 768px) {
  .settings-page {
    padding: var(--space-4) var(--space-3);
  }

  .settings-layout {
    flex-direction: column;
    align-items: stretch;
    gap: var(--space-4);
  }

  .settings-sidebar {
    position: static;
    flex: none;
    width: 100%;
  }

  .settings-title {
    margin-bottom: var(--space-3);
  }

  .settings-nav {
    flex-direction: row;
    gap: 2px;
    overflow-x: auto;
    scrollbar-width: none;
    border-bottom: 0.5px solid var(--border-default);
  }

  .nav-group {
    flex-direction: row;
    flex-shrink: 0;
  }

  .nav-group + .nav-group {
    margin-left: var(--space-2);
    padding-left: var(--space-2);
    border-left: 0.5px solid var(--border-default);
  }

  .nav-group-label {
    display: none;
  }

  .nav-item {
    white-space: nowrap;
    padding: var(--space-2) var(--space-3);
    border-radius: var(--radius-md) var(--radius-md) 0 0;
  }

  .nav-item.active {
    background: none;
    box-shadow: inset 0 -2px 0 var(--accent);
  }
}
</style>
