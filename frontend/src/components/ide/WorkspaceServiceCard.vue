<script setup lang="ts">
import type { WorkspaceService } from '@/api/services'
import ServiceCopyButton from './ServiceCopyButton.vue'
import { serviceTypeIcon, serviceTypeLabel } from './serviceCatalog'

defineProps<{
  service: WorkspaceService
  toggling: boolean
}>()

const emit = defineEmits<{
  toggle: [service: WorkspaceService]
  remove: [service: WorkspaceService]
}>()
</script>

<template>
  <article class="service-card">
    <div class="service-card-header">
      <div class="service-heading">
        <span class="service-icon">{{ serviceTypeIcon(service.serviceType) }}</span>
        <div class="service-title-block">
          <h3>{{ serviceTypeLabel(service.serviceType) }}</h3>
          <p>{{ service.config.image }}</p>
        </div>
      </div>
      <span v-if="toggling" class="service-status status-toggling">
        <svg class="spinner" width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round">
          <path d="M21 12a9 9 0 1 1-6.219-8.56" />
        </svg>
        {{ service.status === 'running' ? 'Stopping' : 'Starting' }}
      </span>
      <span v-else class="service-status" :class="`status-${service.status}`">
        <span class="status-dot" />
        {{ service.status }}
      </span>
    </div>

    <dl class="service-info">
      <div class="info-row">
        <dt>Host</dt>
        <dd>
          <span class="mono">{{ service.host }}</span>
          <ServiceCopyButton :value="service.host" label="service host" />
        </dd>
      </div>
      <div class="info-row">
        <dt>Port</dt>
        <dd>
          <span class="mono">{{ service.config.port }}</span>
          <ServiceCopyButton :value="service.config.port" label="service port" />
        </dd>
      </div>
      <div v-for="field in service.credentials" :key="field.label" class="info-row">
        <dt>{{ field.label }}</dt>
        <dd>
          <span class="mono">{{ field.value }}</span>
          <ServiceCopyButton :value="field.value" :label="`${serviceTypeLabel(service.serviceType)} ${field.label.toLowerCase()}`" />
        </dd>
      </div>
    </dl>

    <div class="connection-block">
      <div>
        <span>{{ service.connection.label }}</span>
        <code>{{ service.connection.value }}</code>
      </div>
      <ServiceCopyButton :value="service.connection.value" :label="service.connection.label.toLowerCase()" />
    </div>

    <div class="service-actions">
      <button
        class="service-toggle-btn"
        :class="service.status === 'running' ? 'toggle-stop' : 'toggle-start'"
        :disabled="toggling"
        @click="emit('toggle', service)"
      >
        <svg v-if="service.status === 'running'" width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
          <rect x="6" y="4" width="4" height="16" />
          <rect x="14" y="4" width="4" height="16" />
        </svg>
        <svg v-else width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
          <polygon points="6 3 20 12 6 21 6 3" />
        </svg>
        {{ service.status === 'running' ? 'Stop service' : 'Start service' }}
      </button>
      <button class="service-delete-btn" :disabled="toggling" @click="emit('remove', service)">
        <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
          <path d="M3 6h18" />
          <path d="M19 6v14c0 1-1 2-2 2H7c-1 0-2-1-2-2V6" />
          <path d="M8 6V4c0-1 1-2 2-2h4c1 0 2 1 2 2v2" />
        </svg>
        Remove
      </button>
    </div>
  </article>
</template>

<style scoped>
.service-card {
  display: flex;
  flex-direction: column;
  gap: var(--space-4);
  padding: var(--space-4);
  background: var(--bg-elevated);
  border: 0.5px solid var(--border-subtle);
  border-radius: var(--radius-lg);
  box-shadow: var(--shadow-popover);
}

.service-card-header,
.service-heading,
.service-status,
.service-toggle-btn,
.service-delete-btn {
  display: flex;
  align-items: center;
}

.service-card-header { justify-content: space-between; align-items: flex-start; gap: var(--space-3); }
.service-heading { align-items: flex-start; gap: var(--space-3); min-width: 0; }

.service-icon { font-size: 1.35rem; line-height: 1; }

.service-heading h3 {
  color: var(--text-primary);
  font-size: 0.95rem;
  font-weight: 700;
}

.service-title-block p, .info-row dd, .connection-block code { overflow-wrap: anywhere; }

.service-title-block p {
  color: var(--text-tertiary);
  font-family: var(--font-mono);
  font-size: 0.72rem;
}

.info-row:hover :deep(.service-copy-btn),
.info-row:focus-within :deep(.service-copy-btn),
.connection-block:hover :deep(.service-copy-btn),
.connection-block:focus-within :deep(.service-copy-btn) {
  opacity: 1;
}

.service-status {
  gap: 5px;
  padding: 2px var(--space-2);
  border: 0.5px solid var(--border-subtle);
  border-radius: 999px;
  font-size: 0.72rem;
  font-weight: 600;
  text-transform: capitalize;
}

.status-dot { width: 6px; height: 6px; border-radius: 50%; }

.status-running {
  color: var(--success);
  border-color: var(--success-border);
  background: var(--success-bg);
}

.status-running .status-dot { background: var(--success); box-shadow: 0 0 5px var(--success); }

.status-stopped {
  color: var(--text-tertiary);
  background: var(--bg-neutral);
}

.status-stopped .status-dot { background: var(--text-tertiary); }

.status-toggling {
  color: var(--warning);
  border-color: var(--warning-border);
  background: var(--warning-bg);
}

.service-info { display: grid; grid-template-columns: repeat(auto-fit, minmax(150px, 1fr)); gap: var(--space-2); }

.info-row,
.connection-block {
  background: var(--bg-base);
  border: 0.5px solid var(--border-subtle);
  border-radius: var(--radius-sm);
}

.info-row { min-width: 0; padding: var(--space-2); }

.info-row dt,
.connection-block span {
  color: var(--text-tertiary);
  font-size: 0.7rem;
  font-weight: 600;
  text-transform: uppercase;
  letter-spacing: 0.04em;
}

.info-row dd {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-2);
  margin-top: 2px;
  color: var(--text-secondary);
  font-size: 0.78rem;
}

.info-row dd span {
  min-width: 0;
}

.mono { font-family: var(--font-mono); }

.connection-block {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: var(--space-3);
  padding: var(--space-3);
  border-color: var(--accent-border);
}

.connection-block code {
  display: block;
  margin-top: 3px;
  color: var(--text-primary);
  font-size: 0.76rem;
}

.service-toggle-btn,
.service-delete-btn {
  justify-content: center;
  gap: 5px;
  border-radius: var(--radius-sm);
  font-size: 0.76rem;
  font-weight: 600;
  transition: all var(--transition-fast);
}

.service-actions { display: flex; justify-content: flex-end; gap: var(--space-2); }

.service-toggle-btn, .service-delete-btn { padding: var(--space-1) var(--space-3); }

.toggle-start {
  color: var(--success);
  background: var(--success-bg);
  border: 0.5px solid var(--success-border);
}

.toggle-stop {
  color: var(--warning);
  background: var(--warning-bg);
  border: 0.5px solid var(--warning-border);
}

.service-delete-btn { color: var(--text-tertiary); border: 0.5px solid var(--border-subtle); }

.toggle-start:hover:not(:disabled),
.toggle-stop:hover:not(:disabled) {
  border-color: currentColor;
}

.service-delete-btn:hover:not(:disabled) {
  color: var(--error);
  background: var(--error-bg);
  border-color: var(--error-border);
}

button:disabled { cursor: default; opacity: 0.4; }

.spinner { animation: spin 1s linear infinite; }
</style>
