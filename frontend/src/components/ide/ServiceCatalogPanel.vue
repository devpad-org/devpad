<script setup lang="ts">
import { computed, ref } from 'vue'
import { useServiceStore } from '@/stores/services'
import { serviceCatalog, type ServiceCatalogItem } from './serviceCatalog'

const props = defineProps<{
  workspaceId: number
}>()

const store = useServiceStore()
const deployingType = ref<ServiceCatalogItem['value'] | null>(null)
const addError = ref('')

const attachedTypes = computed(() => new Set(store.services.map((service) => service.serviceType)))

function isAttached(type: ServiceCatalogItem['value']) {
  return attachedTypes.value.has(type)
}

async function deployService(service: ServiceCatalogItem) {
  if (!props.workspaceId || deployingType.value || isAttached(service.value)) return

  deployingType.value = service.value
  addError.value = ''

  try {
    await store.addService(props.workspaceId, service.value)
  } catch (error) {
    const message = error instanceof Error ? error.message : ''
    addError.value = message.includes('409') ? 'Already attached' : `Failed to deploy ${service.label}`
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
      <article v-for="service in serviceCatalog" :key="service.value" class="catalog-card">
        <div class="catalog-card-main">
          <span class="catalog-icon">{{ service.icon }}</span>
          <div class="catalog-meta">
            <h3>{{ service.label }}</h3>
            <p>{{ service.description }}</p>
          </div>
        </div>

        <button
          class="catalog-deploy-btn"
          :class="{ attached: isAttached(service.value) }"
          :disabled="isAttached(service.value) || !!deployingType"
          @click="deployService(service)"
        >
          <svg v-if="deployingType === service.value" class="spinner" width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round">
            <path d="M21 12a9 9 0 1 1-6.219-8.56" />
          </svg>
          <svg v-else-if="!isAttached(service.value)" width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round">
            <path d="M12 5v14" />
            <path d="M5 12h14" />
          </svg>
          <svg v-else width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round">
            <path d="M20 6 9 17l-5-5" />
          </svg>
          {{ isAttached(service.value) ? 'Attached' : deployingType === service.value ? 'Deploying' : 'Deploy' }}
        </button>
      </article>
    </div>
  </div>
</template>

<style scoped>
.service-catalog-panel {
  --ide-header-icon: var(--gruvbox-aqua);
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
  border-bottom: 0.5px solid var(--border-default);
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
  color: var(--text-muted);
  font-size: 0.75rem;
  line-height: 1.45;
  border-bottom: 0.5px solid var(--border-default);
}

.catalog-error {
  padding: var(--space-2) var(--space-3);
  font-size: 0.72rem;
  color: var(--accent-rose);
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
  border: 0.5px solid var(--border-default);
  border-radius: var(--radius-md);
  transition: border-color var(--transition-fast), background var(--transition-fast);
}

.catalog-card:hover {
  background: var(--bg-hover);
  border-color: var(--border-active);
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
  color: var(--text-muted);
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
  color: var(--accent-green);
  background: var(--success-bg);
  border: 0.5px solid var(--success-border);
  border-radius: var(--radius-sm);
  font-size: 0.74rem;
  font-weight: 600;
  transition: all var(--transition-fast);
}

.catalog-deploy-btn:hover:not(:disabled) {
  border-color: var(--accent-green);
}

.catalog-deploy-btn:disabled {
  cursor: default;
  opacity: 0.55;
}

.catalog-deploy-btn.attached {
  color: var(--text-muted);
  background: var(--bg-neutral);
  border-color: var(--border-default);
}

.spinner {
  animation: spin 1s linear infinite;
}

@keyframes spin {
  to { transform: rotate(360deg); }
}
</style>
