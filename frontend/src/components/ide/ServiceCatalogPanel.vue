<script setup lang="ts">
import { computed, reactive, ref } from 'vue'
import { ApiError } from '@/api/client'
import type { ServiceCatalogEntry } from '@/api/services'
import { useServiceStore } from '@/stores/services'
import { serviceCatalogItem, serviceCatalogOrder, type ServiceCatalogItem } from './serviceCatalog'

const props = defineProps<{
  workspaceId: number
}>()

const store = useServiceStore()
const deployingType = ref<string | null>(null)
const addError = ref('')
// Licence terms accepted for this deploy, by service type. Never remembered:
// whoever deploys a service with terms accepts them each time.
const accepted = reactive<Record<string, boolean>>({})

interface CatalogRow {
  // What the server says of the type: its licence terms, so the terms shown
  // are the ones it records as accepted, and whether this host can run it.
  entry: ServiceCatalogEntry
  // How the IDE shows it.
  item: ServiceCatalogItem
}

const rows = computed<CatalogRow[]>(() => store.catalog
  .map((entry) => ({ entry, item: serviceCatalogItem(entry.type) }))
  .sort((a, b) => serviceCatalogOrder(a.entry.type) - serviceCatalogOrder(b.entry.type)))

const attachedTypes = computed(() => new Set(store.services.map((service) => service.serviceType)))

function isAttached(type: string) {
  return attachedTypes.value.has(type)
}

function canDeploy({ entry }: CatalogRow) {
  return !isAttached(entry.type)
    && !deployingType.value
    && !entry.unavailable
    && (!entry.license || !!accepted[entry.type])
}

async function deployService(row: CatalogRow) {
  if (!props.workspaceId || !canDeploy(row)) return

  const { entry, item } = row
  deployingType.value = entry.type
  addError.value = ''

  try {
    await store.addService(props.workspaceId, entry.type, {
      acceptLicense: !!entry.license && !!accepted[entry.type],
    })
    accepted[entry.type] = false
  } catch (error) {
    addError.value = error instanceof ApiError && error.status < 500
      ? error.message
      : `Failed to deploy ${item.label}`
  } finally {
    deployingType.value = null
  }
}
</script>

<template>
  <div class="service-catalog-panel">
    <div class="catalog-header">
      <span class="catalog-title">
        <svg class="panel-icon" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
          <ellipse cx="12" cy="5" rx="9" ry="3" />
          <path d="M3 5v14a9 3 0 0 0 18 0V5" />
          <path d="M3 12a9 3 0 0 0 18 0" />
        </svg>
        Service Catalog
      </span>
    </div>

    <p class="catalog-copy">
      Browse workspace services and deploy one into this workspace.
    </p>

    <div v-if="addError" class="catalog-error">{{ addError }}</div>

    <div class="catalog-list">
      <p v-if="rows.length === 0" class="catalog-empty">
        {{ store.loading ? 'Loading services…' : store.error ?? 'No services available.' }}
      </p>

      <article v-for="{ entry, item } in rows" :key="entry.type" class="catalog-card">
        <div class="catalog-card-main">
          <span class="catalog-icon">{{ item.icon }}</span>
          <div class="catalog-meta">
            <h3>{{ item.label }}</h3>
            <p v-if="item.description">{{ item.description }}</p>
          </div>
        </div>

        <p v-if="entry.unavailable" class="catalog-unavailable">
          Unavailable: {{ entry.unavailable }}
        </p>

        <div v-else-if="entry.license && !isAttached(entry.type)" class="catalog-license">
          <ul>
            <li v-for="note in entry.license.notes" :key="note">{{ note }}</li>
          </ul>
          <label>
            <input v-model="accepted[entry.type]" type="checkbox" :disabled="!!deployingType" />
            <span>
              I accept the
              <a :href="entry.license.url" target="_blank" rel="noopener noreferrer">{{ entry.license.name }}</a>
            </span>
          </label>
        </div>

        <button
          class="catalog-deploy-btn"
          :class="{ attached: isAttached(entry.type) }"
          :disabled="!canDeploy({ entry, item })"
          @click="deployService({ entry, item })"
        >
          <svg v-if="deployingType === entry.type" class="spinner" width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round">
            <path d="M21 12a9 9 0 1 1-6.219-8.56" />
          </svg>
          <svg v-else-if="!isAttached(entry.type)" width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round">
            <path d="M12 5v14" />
            <path d="M5 12h14" />
          </svg>
          <svg v-else width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round">
            <path d="M20 6 9 17l-5-5" />
          </svg>
          {{ isAttached(entry.type) ? 'Attached' : deployingType === entry.type ? 'Deploying' : 'Deploy' }}
        </button>
      </article>
    </div>
  </div>
</template>

<style scoped>
.service-catalog-panel {
  --ide-header-icon: var(--hue-aqua);
  display: flex;
  flex-direction: column;
  height: 100%;
  overflow-y: auto;
  background: var(--ide-panel-bg);
}

.catalog-header {
  display: flex;
  align-items: center;
  padding: 0 var(--space-3);
  border-bottom: 0.5px solid var(--border-subtle);
  background: var(--ide-header-bg);
  height: 38px;
  flex-shrink: 0;
}

.catalog-title {
  display: flex;
  align-items: center;
  gap: 6px;
  font-size: var(--ide-header-title-size);
  font-weight: 600;
  color: var(--text-primary);
}

.panel-icon {
  color: var(--ide-header-icon);
  flex-shrink: 0;
}

.catalog-copy {
  padding: var(--space-3);
  color: var(--text-tertiary);
  font-size: 0.75rem;
  line-height: 1.45;
  border-bottom: 0.5px solid var(--border-subtle);
}

.catalog-error {
  padding: var(--space-2) var(--space-3);
  font-size: 0.72rem;
  color: var(--error);
  background: var(--error-bg);
  border-bottom: 0.5px solid var(--error-border);
}

.catalog-list {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
  padding: var(--space-3);
}

.catalog-card {
  display: flex;
  flex-direction: column;
  gap: var(--space-3);
  padding: var(--space-3);
  background: var(--bg-elevated);
  border: 0.5px solid var(--border-subtle);
  border-radius: var(--radius-md);
  transition: border-color var(--transition-fast), background var(--transition-fast);
}

.catalog-card:hover {
  background: var(--bg-hover);
  border-color: var(--border-strong);
}

.catalog-card-main {
  display: flex;
  gap: var(--space-2);
}

.catalog-icon {
  font-size: 1rem;
  line-height: 1;
  flex-shrink: 0;
}

.catalog-meta {
  min-width: 0;
}

.catalog-meta h3 {
  color: var(--text-primary);
  font-size: 0.8rem;
  font-weight: 600;
}

.catalog-meta p {
  margin-top: 2px;
  color: var(--text-tertiary);
  font-size: 0.72rem;
  line-height: 1.4;
}

.catalog-deploy-btn {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 5px;
  align-self: stretch;
  padding: var(--space-1) var(--space-2);
  color: var(--success);
  background: var(--success-bg);
  border: 0.5px solid var(--success-border);
  border-radius: var(--radius-sm);
  font-size: 0.74rem;
  font-weight: 600;
  transition: all var(--transition-fast);
}

.catalog-deploy-btn:hover:not(:disabled) {
  border-color: var(--success);
}

.catalog-deploy-btn:disabled {
  cursor: default;
  opacity: 0.55;
}

.catalog-deploy-btn.attached {
  color: var(--text-tertiary);
  background: var(--bg-neutral);
  border-color: var(--border-subtle);
}

.catalog-empty {
  color: var(--text-tertiary);
  font-size: 0.75rem;
}

.catalog-unavailable {
  color: var(--text-tertiary);
  font-size: 0.72rem;
  line-height: 1.4;
}

.catalog-license {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
  font-size: 0.72rem;
  line-height: 1.4;
}

.catalog-license ul {
  margin: 0;
  padding-left: var(--space-4);
  color: var(--text-secondary);
}

.catalog-license label {
  display: flex;
  align-items: flex-start;
  gap: var(--space-2);
  color: var(--text-primary);
  cursor: pointer;
}

.catalog-license input {
  margin-top: 2px;
  accent-color: var(--accent);
}

.catalog-license a {
  color: var(--accent);
}

.spinner {
  animation: spin 1s linear infinite;
}
</style>
