<script setup lang="ts">
import { computed } from 'vue'
import type { WorkspaceService } from '@/api/services'
import ServiceCopyButton from './ServiceCopyButton.vue'
import { serviceTypeLabel } from './serviceCatalog'
import ServiceTile from './ServiceTile.vue'

const props = defineProps<{
  service: WorkspaceService
  toggling: boolean
}>()

const emit = defineEmits<{
  toggle: [service: WorkspaceService]
  remove: [service: WorkspaceService]
}>()

const label = computed(() => serviceTypeLabel(props.service.serviceType))
const running = computed(() => props.service.status === 'running')

const fields = computed(() => [
  { label: 'Host', value: props.service.host, copyLabel: `${label.value} host` },
  { label: 'Port', value: String(props.service.config.port), copyLabel: `${label.value} port` },
  ...props.service.credentials.map((field) => ({
    label: field.label,
    value: field.value,
    copyLabel: `${label.value} ${field.label.toLowerCase()}`,
  })),
])
</script>

<template>
  <article class="service-card" :class="running ? 'is-running' : 'is-stopped'">
    <header class="card-head">
      <ServiceTile class="card-tile" :type="service.serviceType" :muted="!running" />
      <div class="card-title">
        <div class="card-name-row">
          <h3>{{ label }}</h3>
          <span v-if="toggling" class="card-status is-toggling">
            <svg class="spinner" width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="3" stroke-linecap="round" stroke-linejoin="round">
              <path d="M21 12a9 9 0 1 1-6.219-8.56" />
            </svg>
            {{ running ? 'Stopping' : 'Starting' }}
          </span>
          <span v-else class="card-status">
            <span class="status-dot" />
            {{ running ? 'Running' : 'Stopped' }}
          </span>
        </div>
        <span class="card-image" :title="service.config.image">{{ service.config.image }}</span>
      </div>

      <div class="card-actions">
        <button
          class="card-btn"
          :class="running ? 'btn-stop' : 'btn-start'"
          :disabled="toggling"
          :title="`${running ? 'Stop' : 'Start'} ${label}`"
          @click="emit('toggle', service)"
        >
          <svg v-if="running" width="12" height="12" viewBox="0 0 24 24" fill="currentColor" stroke="none">
            <rect x="6" y="6" width="12" height="12" rx="1.5" />
          </svg>
          <svg v-else width="12" height="12" viewBox="0 0 24 24" fill="currentColor" stroke="currentColor" stroke-width="2" stroke-linejoin="round">
            <polygon points="7 4 19 12 7 20 7 4" />
          </svg>
          <span class="card-btn-label">{{ running ? 'Stop' : 'Start' }}</span>
        </button>
        <button
          class="card-btn btn-remove"
          :disabled="toggling"
          :title="`Remove ${label} and its data`"
          :aria-label="`Remove ${label} and its data`"
          @click="emit('remove', service)"
        >
          <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
            <path d="M3 6h18" />
            <path d="M19 6v14c0 1-1 2-2 2H7c-1 0-2-1-2-2V6" />
            <path d="M8 6V4c0-1 1-2 2-2h4c1 0 2 1 2 2v2" />
          </svg>
        </button>
      </div>
    </header>

    <dl class="card-fields">
      <div v-for="field in fields" :key="field.label" class="card-field" :class="{ wide: field.value.length > 16 }">
        <dt>{{ field.label }}</dt>
        <dd>
          <span class="card-value" :title="field.value">{{ field.value }}</span>
          <ServiceCopyButton :value="field.value" :label="field.copyLabel" />
        </dd>
      </div>
    </dl>

    <div class="card-connection">
      <span class="card-connection-label">{{ service.connection.label }}</span>
      <code class="card-connection-value" :title="service.connection.value">{{ service.connection.value }}</code>
      <ServiceCopyButton class="always" :value="service.connection.value" :label="service.connection.label.toLowerCase()" />
    </div>
  </article>
</template>

<style scoped>
.service-card {
  container-type: inline-size;
  overflow: hidden;
  background: var(--bg-elevated);
  border: 0.5px solid var(--border-subtle);
  border-radius: var(--radius-lg);
  transition: border-color var(--transition-fast);
}

.service-card:hover {
  border-color: var(--border-strong);
}

/* ── Head ─────────────────────────────────────────────────────────────── */

.card-head {
  display: flex;
  align-items: center;
  gap: var(--space-3);
  padding: var(--space-3) var(--space-3) var(--space-3) 14px;
}

.card-tile {
  font-size: 1.05rem;
}

.card-title {
  display: flex;
  flex: 1;
  flex-direction: column;
  gap: 2px;
  min-width: 0;
}

.card-name-row {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  min-width: 0;
}

.card-name-row h3 {
  overflow: hidden;
  color: var(--text-primary);
  font-size: 0.86rem;
  font-weight: 600;
  line-height: 1.3;
  white-space: nowrap;
  text-overflow: ellipsis;
}

.card-image {
  overflow: hidden;
  color: var(--text-tertiary);
  font-family: var(--font-mono);
  font-size: 0.68rem;
  white-space: nowrap;
  text-overflow: ellipsis;
}

.card-status {
  display: inline-flex;
  flex-shrink: 0;
  align-items: center;
  gap: 5px;
  height: 18px;
  padding: 0 7px;
  border: 0.5px solid var(--border-subtle);
  border-radius: 999px;
  background: var(--bg-neutral);
  color: var(--text-tertiary);
  font-size: 0.66rem;
  font-weight: 600;
}

.status-dot {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: var(--text-dim);
}

.is-running .card-status:not(.is-toggling) {
  color: var(--success);
  background: var(--success-bg);
  border-color: var(--success-border);
}

.is-running .card-status .status-dot {
  background: var(--success);
}

.card-status.is-toggling {
  color: var(--warning);
  background: var(--warning-bg);
  border-color: var(--warning-border);
}

/* ── Actions ──────────────────────────────────────────────────────────── */

.card-actions {
  display: flex;
  flex-shrink: 0;
  align-items: center;
  gap: var(--space-1);
}

.card-btn {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 5px;
  height: 26px;
  min-width: 26px;
  padding: 0 10px;
  border: 0.5px solid var(--border-subtle);
  border-radius: var(--radius-md);
  color: var(--text-secondary);
  font-size: 0.72rem;
  font-weight: 600;
  transition: background var(--transition-fast), border-color var(--transition-fast), color var(--transition-fast), opacity var(--transition-fast);
}

.card-btn:hover:not(:disabled) {
  color: var(--text-primary);
  background: var(--bg-hover);
  border-color: var(--border-strong);
}

.btn-start {
  color: var(--success);
  background: var(--success-bg);
  border-color: var(--success-border);
}

.btn-start:hover:not(:disabled) {
  color: var(--success);
  background: var(--success-bg);
  border-color: var(--success);
}

.btn-remove {
  padding: 0;
  color: var(--text-tertiary);
  border-color: transparent;
}

.btn-remove:hover:not(:disabled) {
  color: var(--error);
  background: var(--error-bg);
  border-color: var(--error-border);
}

.card-btn:disabled {
  cursor: default;
  opacity: 0.45;
}

/* ── Fields ───────────────────────────────────────────────────────────── */

.card-fields {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(136px, 1fr));
  gap: var(--space-2) var(--space-4);
  margin: 0;
  padding: 10px 14px var(--space-3);
  border-top: 0.5px solid var(--border-hairline);
}

.card-field {
  min-width: 0;
}

/* A long value, such as a host name or key, takes two columns rather than
   being cut short, and the columns still line up from card to card. */
.card-field.wide {
  grid-column: span 2;
}

.card-field dt {
  color: var(--text-tertiary);
  font-size: 0.66rem;
  font-weight: 500;
  line-height: 1.5;
}

.card-field dd {
  display: flex;
  align-items: center;
  gap: var(--space-1);
  min-width: 0;
  margin: 0;
}

.card-value {
  overflow: hidden;
  min-width: 0;
  color: var(--text-primary);
  font-family: var(--font-mono);
  font-size: 0.74rem;
  line-height: 22px;
  white-space: nowrap;
  text-overflow: ellipsis;
}

.card-field:hover :deep(.service-copy-btn),
.card-field:focus-within :deep(.service-copy-btn) {
  opacity: 1;
}

/* ── Connection ───────────────────────────────────────────────────────── */

.card-connection {
  display: flex;
  align-items: center;
  gap: var(--space-3);
  padding: 6px var(--space-2) 6px 14px;
  background: var(--bg-base);
  border-top: 0.5px solid var(--border-hairline);
}

.card-connection-label {
  flex-shrink: 0;
  color: var(--text-tertiary);
  font-size: 0.66rem;
  font-weight: 500;
  line-height: 22px;
}

.card-connection-value {
  flex: 1;
  overflow: hidden;
  min-width: 0;
  color: var(--text-secondary);
  font-family: var(--font-mono);
  font-size: 0.72rem;
  line-height: 22px;
  white-space: nowrap;
  text-overflow: ellipsis;
}

/* ── Narrow cards ─────────────────────────────────────────────────────── */

@container (max-width: 420px) {
  .card-head {
    padding-left: var(--space-3);
  }

  .card-btn-label {
    display: none;
  }

  .card-btn {
    padding: 0;
  }

  .card-fields {
    grid-template-columns: repeat(2, minmax(0, 1fr));
    padding-left: var(--space-3);
  }

  .card-connection {
    flex-wrap: wrap;
    row-gap: 0;
    padding-left: var(--space-3);
  }

  .card-connection-label {
    flex-basis: 100%;
    line-height: 1.6;
    padding-top: 2px;
  }
}

.spinner {
  animation: spin 1s linear infinite;
}
</style>
