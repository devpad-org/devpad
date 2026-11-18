<script setup lang="ts">
import { computed, reactive, ref } from 'vue'
import { ApiError } from '@/api/client'
import type { ServiceCatalogEntry, WorkspaceService } from '@/api/services'
import { useServiceStore } from '@/stores/services'
import { serviceCatalogItem, serviceCatalogOrder, type ServiceCatalogItem } from './serviceCatalog'
import ServiceTile from './ServiceTile.vue'

const props = defineProps<{
  workspaceId: number
}>()

const store = useServiceStore()
const deployingType = ref<string | null>(null)
const addError = ref('')
// Licence terms accepted for this deploy, by service type. Never remembered:
// whoever deploys a service with terms accepts them each time.
const accepted = reactive<Record<string, boolean>>({})
// The service type whose licence terms are open for accepting.
const termsOpen = ref<string | null>(null)

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

const attachedByType = computed(() => new Map(store.services.map((service) => [service.serviceType, service])))

function attachedService(type: string): WorkspaceService | undefined {
  return attachedByType.value.get(type)
}

const attachedRows = computed(() => rows.value.filter(({ entry }) => attachedByType.value.has(entry.type)))
const availableRows = computed(() => rows.value.filter(({ entry }) => !attachedByType.value.has(entry.type) && !entry.unavailable))
const unavailableRows = computed(() => rows.value.filter(({ entry }) => !attachedByType.value.has(entry.type) && entry.unavailable))

function canDeploy({ entry }: CatalogRow) {
  return !attachedByType.value.has(entry.type)
    && !deployingType.value
    && !entry.unavailable
    && (!entry.license || !!accepted[entry.type])
}

// A service with licence terms opens them first; one without deploys.
function requestDeploy(row: CatalogRow) {
  if (row.entry.license && termsOpen.value !== row.entry.type) {
    termsOpen.value = row.entry.type
    return
  }
  void deployService(row)
}

function closeTerms(type: string) {
  accepted[type] = false
  if (termsOpen.value === type) termsOpen.value = null
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
    closeTerms(entry.type)
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

    <div v-if="addError" class="catalog-error" role="alert">{{ addError }}</div>

    <p v-if="rows.length === 0" class="catalog-empty">
      {{ store.loading ? 'Loading services…' : store.error ?? 'No services available.' }}
    </p>

    <section v-if="attachedRows.length" class="catalog-section" aria-labelledby="catalog-attached">
      <h2 id="catalog-attached" class="catalog-section-title">
        In this workspace
        <span class="catalog-section-count">{{ attachedRows.length }}</span>
      </h2>
      <ul class="catalog-list">
        <li v-for="{ entry, item } in attachedRows" :key="entry.type" class="catalog-row">
          <ServiceTile :type="entry.type" :muted="attachedService(entry.type)?.status !== 'running'" />
          <div class="catalog-meta">
            <span class="catalog-name"><span class="catalog-label">{{ item.label }}</span></span>
            <span class="catalog-desc" :title="item.description">{{ item.description }}</span>
          </div>
          <span
            class="catalog-status"
            :class="attachedService(entry.type)?.status === 'running' ? 'is-running' : 'is-stopped'"
            :title="`${item.label} is ${attachedService(entry.type)?.status === 'running' ? 'running' : 'stopped'}`"
          >
            <span class="status-dot" />
            {{ attachedService(entry.type)?.status === 'running' ? 'Running' : 'Stopped' }}
          </span>
        </li>
      </ul>
    </section>

    <section v-if="availableRows.length" class="catalog-section" aria-labelledby="catalog-available">
      <h2 id="catalog-available" class="catalog-section-title">
        Available
        <span class="catalog-section-count">{{ availableRows.length }}</span>
      </h2>
      <ul class="catalog-list">
        <li
          v-for="row in availableRows"
          :key="row.entry.type"
          class="catalog-row"
          :class="{ 'terms-open': termsOpen === row.entry.type }"
        >
          <ServiceTile :type="row.entry.type" />
          <div class="catalog-meta">
            <span class="catalog-name">
              <span class="catalog-label">{{ row.item.label }}</span>
              <span v-if="row.entry.license" class="catalog-tag" title="Deploying this service means accepting its licence terms">Terms</span>
            </span>
            <span class="catalog-desc" :title="row.item.description">{{ row.item.description }}</span>
          </div>
          <button
            v-if="termsOpen !== row.entry.type"
            class="catalog-deploy-btn"
            :disabled="!!deployingType"
            :aria-expanded="row.entry.license ? false : undefined"
            :title="`Deploy ${row.item.label} into this workspace`"
            :aria-label="`Deploy ${row.item.label}`"
            @click="requestDeploy(row)"
          >
            <svg v-if="deployingType === row.entry.type" class="spinner" width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round">
              <path d="M21 12a9 9 0 1 1-6.219-8.56" />
            </svg>
            <svg v-else width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round">
              <path d="M12 5v14" />
              <path d="M5 12h14" />
            </svg>
            <span class="catalog-deploy-label">{{ deployingType === row.entry.type ? 'Deploying' : 'Deploy' }}</span>
          </button>

          <div v-if="row.entry.license && termsOpen === row.entry.type" class="catalog-terms">
            <ul class="catalog-terms-notes">
              <li v-for="note in row.entry.license.notes" :key="note">{{ note }}</li>
            </ul>
            <label class="catalog-terms-accept">
              <input v-model="accepted[row.entry.type]" type="checkbox" :disabled="!!deployingType" />
              <span>
                I accept the
                <a :href="row.entry.license.url" target="_blank" rel="noopener noreferrer">{{ row.entry.license.name }}</a>
              </span>
            </label>
            <div class="catalog-terms-actions">
              <button class="catalog-cancel-btn" :disabled="!!deployingType" @click="closeTerms(row.entry.type)">Cancel</button>
              <button class="catalog-deploy-btn" :disabled="!canDeploy(row)" @click="deployService(row)">
                <svg v-if="deployingType === row.entry.type" class="spinner" width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round">
                  <path d="M21 12a9 9 0 1 1-6.219-8.56" />
                </svg>
                {{ deployingType === row.entry.type ? 'Deploying' : `Deploy ${row.item.label}` }}
              </button>
            </div>
          </div>
        </li>
      </ul>
    </section>

    <section v-if="unavailableRows.length" class="catalog-section" aria-labelledby="catalog-unavailable">
      <h2 id="catalog-unavailable" class="catalog-section-title">
        Unavailable on this host
        <span class="catalog-section-count">{{ unavailableRows.length }}</span>
      </h2>
      <ul class="catalog-list">
        <li v-for="{ entry, item } in unavailableRows" :key="entry.type" class="catalog-row is-unavailable">
          <ServiceTile :type="entry.type" muted />
          <div class="catalog-meta">
            <span class="catalog-name"><span class="catalog-label">{{ item.label }}</span></span>
            <span class="catalog-desc catalog-reason" :title="entry.unavailable">
              {{ entry.unavailable ? entry.unavailable.charAt(0).toUpperCase() + entry.unavailable.slice(1) : '' }}
            </span>
          </div>
        </li>
      </ul>
    </section>
  </div>
</template>

<style scoped>
.service-catalog-panel {
  --ide-header-icon: var(--hue-aqua);
  container-type: inline-size;
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

.catalog-error {
  padding: var(--space-2) var(--space-3);
  font-size: 0.72rem;
  line-height: 1.4;
  color: var(--error);
  background: var(--error-bg);
  border-bottom: 0.5px solid var(--error-border);
}

.catalog-empty {
  padding: var(--space-4) var(--space-3);
  color: var(--text-tertiary);
  font-size: 0.75rem;
}

/* ── Sections ─────────────────────────────────────────────────────────── */

.catalog-section {
  padding: var(--space-3) 0 var(--space-1);
}

.catalog-section + .catalog-section {
  border-top: 0.5px solid var(--border-hairline);
}

.catalog-section-title {
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 0 var(--space-3) var(--space-1);
  color: var(--text-tertiary);
  font-size: 0.66rem;
  font-weight: 600;
  letter-spacing: 0.06em;
  text-transform: uppercase;
}

.catalog-section-count {
  min-width: 16px;
  padding: 0 5px;
  border-radius: 999px;
  background: var(--bg-neutral);
  color: var(--text-tertiary);
  font-size: 0.64rem;
  line-height: 15px;
  text-align: center;
  letter-spacing: 0;
}

.catalog-list {
  list-style: none;
  margin: 0;
  padding: 0;
}

/* ── Rows ─────────────────────────────────────────────────────────────── */

.catalog-row {
  display: grid;
  grid-template-columns: auto minmax(0, 1fr) auto;
  align-items: center;
  column-gap: 10px;
  padding: 7px var(--space-3);
  transition: background var(--transition-fast);
}

.catalog-row:hover,
.catalog-row.terms-open {
  background: var(--bg-hover);
}

.catalog-meta {
  display: flex;
  flex-direction: column;
  gap: 1px;
  min-width: 0;
}

.catalog-name {
  display: flex;
  align-items: center;
  gap: 6px;
  min-width: 0;
  color: var(--text-primary);
  font-size: 0.78rem;
  font-weight: 600;
  line-height: 1.3;
}

.catalog-label {
  overflow: hidden;
  min-width: 0;
  white-space: nowrap;
  text-overflow: ellipsis;
}

.catalog-desc {
  overflow: hidden;
  color: var(--text-tertiary);
  font-size: 0.7rem;
  line-height: 1.35;
  white-space: nowrap;
  text-overflow: ellipsis;
}

.catalog-row.is-unavailable .catalog-name {
  color: var(--text-secondary);
}

.catalog-reason {
  display: -webkit-box;
  -webkit-box-orient: vertical;
  -webkit-line-clamp: 2;
  line-clamp: 2;
  white-space: normal;
}

.catalog-tag {
  flex-shrink: 0;
  padding: 0 5px;
  border: 0.5px solid var(--border-subtle);
  border-radius: var(--radius-sm);
  color: var(--text-tertiary);
  font-size: 0.62rem;
  font-weight: 500;
  line-height: 14px;
}

/* ── Status and actions ───────────────────────────────────────────────── */

.catalog-status {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  color: var(--text-tertiary);
  font-size: 0.7rem;
  font-weight: 500;
  white-space: nowrap;
}

.status-dot {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: var(--text-dim);
}

.catalog-status.is-running {
  color: var(--success);
}

.catalog-status.is-running .status-dot {
  background: var(--success);
}

.catalog-deploy-btn,
.catalog-cancel-btn {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 4px;
  height: 24px;
  padding: 0 var(--space-2);
  border-radius: var(--radius-md);
  font-size: 0.72rem;
  font-weight: 600;
  white-space: nowrap;
  transition: background var(--transition-fast), border-color var(--transition-fast), color var(--transition-fast), opacity var(--transition-fast);
}

.catalog-deploy-btn {
  color: var(--accent);
  background: var(--accent-glow);
  border: 0.5px solid var(--accent-border);
}

.catalog-deploy-btn:hover:not(:disabled) {
  border-color: var(--accent);
}

.catalog-cancel-btn {
  color: var(--text-secondary);
  border: 0.5px solid transparent;
}

.catalog-cancel-btn:hover:not(:disabled) {
  color: var(--text-primary);
  background: var(--bg-hover);
}

.catalog-deploy-btn:disabled,
.catalog-cancel-btn:disabled {
  cursor: default;
  opacity: 0.5;
}

/* ── Licence terms ────────────────────────────────────────────────────── */

.catalog-terms {
  grid-column: 2 / -1;
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
  margin-top: var(--space-2);
  padding: var(--space-2) 10px;
  background: var(--bg-elevated);
  border: 0.5px solid var(--border-subtle);
  border-radius: var(--radius-md);
  font-size: 0.7rem;
  line-height: 1.45;
}

.catalog-terms-notes {
  margin: 0;
  padding-left: var(--space-3);
  color: var(--text-secondary);
}

.catalog-terms-notes li + li {
  margin-top: 2px;
}

.catalog-terms-accept {
  display: flex;
  align-items: flex-start;
  gap: 6px;
  color: var(--text-primary);
  cursor: pointer;
}

.catalog-terms-accept input {
  margin-top: 2px;
  accent-color: var(--accent);
}

.catalog-terms-accept a {
  color: var(--accent);
}

.catalog-terms-actions {
  display: flex;
  justify-content: flex-end;
  gap: var(--space-1);
}

.spinner {
  animation: spin 1s linear infinite;
}

/* A narrow sidebar keeps the names readable: the deploy button shows only
   its icon, and an attached service only its status dot. */
@container (max-width: 280px) {
  .catalog-row > .catalog-deploy-btn {
    width: 24px;
    padding: 0;
  }

  .catalog-row > .catalog-deploy-btn .catalog-deploy-label {
    display: none;
  }

  .catalog-status {
    font-size: 0;
    gap: 0;
  }
}
</style>
