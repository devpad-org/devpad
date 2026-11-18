<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { ApiError } from '@/api/client'
import { projectServiceApi } from '@/api/services'
import { useActiveProjectStore } from '@/stores/activeProject'
import { useServiceStore } from '@/stores/services'
import ServiceLogo from '@/components/ide/ServiceLogo.vue'
import SelectMenu, { type SelectMenuOption } from '@/components/SelectMenu.vue'
import ProjectCard from './ProjectCard.vue'
import type { CardStatus } from './card'
import { serviceCatalogItem, serviceCatalogOrder, serviceTypeLabel } from '@/components/ide/serviceCatalog'

// The services each new sandbox gets its own copy of, and the project
// workspace when it is made or rebuilt or asks for them.
const props = defineProps<{
  // The project workspace's status, or 'none' before it is created.
  workspaceStatus: string
  // The project workspace, when there is one.
  workspaceId: number | null
  step?: number
}>()

const store = useActiveProjectStore()
// Shared with the project workspace's services panel, which can add the
// workspace's services to the project.
const serviceStore = useServiceStore()

const loaded = ref(false)
const error = ref('')
const notice = ref('')
const adding = ref(false)
const removing = ref<string | null>(null)
const addingToWorkspace = ref(false)
const selected = ref('')
// Licence terms accepted for this add. Never remembered: whoever adds a
// service with terms accepts them each time.
const accepted = ref(false)

const services = computed(() => (serviceStore.projectLoadedFor === store.projectId ? serviceStore.projectServices : []))

const sorted = computed(() => [...services.value]
  .sort((a, b) => serviceCatalogOrder(a.serviceType) - serviceCatalogOrder(b.serviceType)))

const addable = computed(() => {
  const have = new Set(services.value.map((s) => s.serviceType))
  return serviceStore.catalog
    .filter((entry) => !have.has(entry.type))
    .sort((a, b) => serviceCatalogOrder(a.type) - serviceCatalogOrder(b.type))
})

const addOptions = computed<SelectMenuOption[]>(() => addable.value.map((entry) => ({
  value: entry.type,
  label: serviceCatalogItem(entry.type).label,
  description: entry.unavailable ? `Unavailable: ${entry.unavailable}` : undefined,
  disabled: !!entry.unavailable,
})))

const selectedEntry = computed(() => addable.value.find((e) => e.type === selected.value) ?? null)
const canAdd = computed(() => {
  const entry = selectedEntry.value
  return !!entry && !entry.unavailable && (!entry.license || accepted.value) && !adding.value && store.projectId !== null
})

// The project's services the project workspace lacks, such as ones added
// to the project since it was made.
const missing = computed(() => {
  if (props.workspaceId === null || props.workspaceStatus === 'none' || serviceStore.loadedFor !== props.workspaceId) return []
  const have = new Set(serviceStore.services.map((s) => s.serviceType))
  return sorted.value.filter((s) => !have.has(s.serviceType)).map((s) => serviceTypeLabel(s.serviceType))
})

const status = computed<CardStatus>(() => {
  if (missing.value.length) return { label: 'Workspace missing some', tone: 'warn' }
  const n = sorted.value.length
  return { label: n === 0 ? 'None' : `${n} ${n === 1 ? 'service' : 'services'}`, tone: n === 0 ? 'neutral' : 'ok' }
})

async function load() {
  const id = store.projectId
  if (id === null) return
  try {
    const loads = [serviceStore.fetchProjectServices(id)]
    if (props.workspaceId !== null) {
      loads.push(serviceStore.fetchServices(props.workspaceId))
    }
    await Promise.all(loads)
    error.value = ''
  } catch (e) {
    error.value = e instanceof Error ? e.message : 'Failed to load the services'
  } finally {
    loaded.value = true
  }
}

function failure(e: unknown, fallback: string) {
  return e instanceof ApiError && e.status < 500 ? e.message : fallback
}

async function add() {
  const id = store.projectId
  const entry = selectedEntry.value
  if (id === null || !entry || !canAdd.value) return
  adding.value = true
  error.value = ''
  notice.value = ''
  try {
    await projectServiceApi.add(id, entry.type, { acceptLicense: !!entry.license && accepted.value })
    selected.value = ''
    accepted.value = false
    await load()
  } catch (e) {
    error.value = failure(e, `Failed to add ${serviceTypeLabel(entry.type)}`)
  } finally {
    adding.value = false
  }
}

async function remove(type: string) {
  const id = store.projectId
  if (id === null) return
  removing.value = type
  error.value = ''
  notice.value = ''
  try {
    await projectServiceApi.remove(id, type)
    await load()
  } catch (e) {
    error.value = failure(e, `Failed to remove ${serviceTypeLabel(type)}`)
  } finally {
    removing.value = null
  }
}

async function addToWorkspace() {
  const id = store.projectId
  const workspaceId = props.workspaceId
  if (id === null || workspaceId === null) return
  const names = missing.value
  addingToWorkspace.value = true
  error.value = ''
  notice.value = ''
  try {
    await projectServiceApi.addToWorkspace(id)
    await serviceStore.fetchServices(workspaceId)
    notice.value = props.workspaceStatus === 'running'
      ? `Added ${names.join(', ')} to your workspace. New terminals have ${names.length === 1 ? 'its' : 'their'} connection variables.`
      : `Added ${names.join(', ')} to your workspace. ${names.length === 1 ? 'It starts' : 'They start'} with the workspace.`
  } catch (e) {
    error.value = failure(e, 'Failed to add the services to your workspace')
  } finally {
    addingToWorkspace.value = false
  }
}

watch(selected, () => { accepted.value = false })
watch(() => [store.projectId, props.workspaceId, props.workspaceStatus], () => void load(), { immediate: true })
</script>

<template>
  <ProjectCard
    title="Services"
    doc="projectServices"
    doc-label="Project services"
    :step="step"
    :status="loaded ? status.label : ''"
    :tone="status.tone"
  >
    <template #icon>
      <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round">
        <ellipse cx="12" cy="5" rx="9" ry="3" />
        <path d="M3 5v14a9 3 0 0 0 18 0V5" />
        <path d="M3 12a9 3 0 0 0 18 0" />
      </svg>
    </template>

    <p class="card-lead">
      {{ sorted.length
        ? 'Each new workspace and sandbox gets its own empty copy, started before the setup script runs, which stops and goes with it.'
        : 'None yet. Add the databases and search engines your code needs, and each new sandbox gets its own empty copy before the setup script runs.' }}
    </p>

    <ul v-if="sorted.length" class="svc-list">
      <li v-for="svc in sorted" :key="svc.serviceType" class="svc-row">
        <ServiceLogo class="svc-logo" :type="svc.serviceType" />
        <span class="svc-name">{{ serviceTypeLabel(svc.serviceType) }}</span>
        <button
          class="svc-remove"
          :disabled="removing !== null"
          :title="`Remove ${serviceTypeLabel(svc.serviceType)}; workspaces keep the copies they have`"
          :aria-label="`Remove ${serviceTypeLabel(svc.serviceType)}`"
          @click="remove(svc.serviceType)"
        >
          <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round">
            <path d="M18 6 6 18" />
            <path d="m6 6 12 12" />
          </svg>
        </button>
      </li>
    </ul>

    <div v-if="missing.length" class="svc-missing">
      <p class="card-warn">Your workspace doesn't have {{ missing.join(', ') }} yet.</p>
      <button
        class="btn btn-secondary btn-sm"
        :disabled="addingToWorkspace || !store.ready"
        @click="addToWorkspace"
      >
        {{ addingToWorkspace ? 'Adding…' : 'Add to workspace' }}
      </button>
    </div>

    <div v-if="addable.length" class="svc-add">
      <div class="svc-add-row">
        <SelectMenu
          v-model="selected"
          class="svc-select"
          :options="addOptions"
          title="Service to add"
          placeholder="Add a service…"
          variant="field"
          :disabled="adding"
        />
        <button v-if="selectedEntry" class="btn btn-secondary btn-sm" :disabled="!canAdd" @click="add">
          {{ adding ? 'Adding…' : 'Add' }}
        </button>
      </div>
      <template v-if="selectedEntry">
        <p v-if="serviceCatalogItem(selectedEntry.type).description" class="card-note">{{ serviceCatalogItem(selectedEntry.type).description }}</p>
        <div v-if="selectedEntry.license" class="svc-license">
          <ul>
            <li v-for="note in selectedEntry.license.notes" :key="note">{{ note }}</li>
            <li>Accepting them here accepts them for every copy devpad deploys for this project.</li>
          </ul>
          <label>
            <input v-model="accepted" type="checkbox" :disabled="adding" />
            <span>
              I accept the
              <a :href="selectedEntry.license.url" target="_blank" rel="noopener noreferrer">{{ selectedEntry.license.name }}</a>
            </span>
          </label>
        </div>
      </template>
    </div>

    <p v-if="error" class="notice notice-error card-message">{{ error }}</p>
    <p v-if="notice" class="notice notice-success card-message">{{ notice }}</p>
  </ProjectCard>
</template>

<style scoped src="./shared.css"></style>
<style scoped src="./card.css"></style>
<style scoped>
.svc-list {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-2);
  margin: 0;
  padding: 0;
  list-style: none;
}

.svc-row {
  display: inline-flex;
  align-items: center;
  gap: var(--space-2);
  height: 30px;
  padding: 0 var(--space-1) 0 var(--space-2);
  background: var(--bg-elevated);
  border: 0.5px solid var(--border-subtle);
  border-radius: var(--radius-md);
  font-size: 0.78rem;
  color: var(--text-primary);
}

.svc-logo {
  font-size: 0.95rem;
  flex-shrink: 0;
}

.svc-name {
  white-space: nowrap;
}

.svc-remove {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 22px;
  height: 22px;
  border-radius: var(--radius-sm);
  color: var(--text-tertiary);
  transition: all var(--transition-fast);
}

.svc-remove:hover:not(:disabled) {
  background: var(--bg-hover);
  color: var(--error);
}

.svc-remove:disabled {
  opacity: 0.4;
  cursor: default;
}

.svc-missing {
  display: flex;
  align-items: center;
  justify-content: space-between;
  flex-wrap: wrap;
  gap: var(--space-2);
  padding: var(--space-2) var(--space-3);
  background: var(--warning-bg);
  border: 0.5px solid var(--warning-border);
  border-radius: var(--radius-md);
}

.svc-add {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
  margin-top: auto;
  padding-top: var(--space-2);
}

.svc-add-row {
  display: flex;
  align-items: center;
  gap: var(--space-2);
}

.svc-select {
  flex: 1;
  min-width: 0;
}

.svc-license {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
  font-size: 0.75rem;
  line-height: 1.4;
}

.svc-license ul {
  margin: 0;
  padding-left: var(--space-4);
  color: var(--text-secondary);
}

.svc-license label {
  display: flex;
  align-items: flex-start;
  gap: var(--space-2);
  color: var(--text-primary);
  cursor: pointer;
}

.svc-license input {
  margin-top: 2px;
  accent-color: var(--accent);
}

.svc-license a {
  color: var(--accent);
}

/* Matches MOBILE_MEDIA_QUERY in useMediaQuery.ts. */
@media (max-width: 768px), (pointer: coarse) and (max-height: 500px) {
  .svc-row {
    height: 40px;
  }

  .svc-remove {
    width: 36px;
    height: 36px;
  }
}
</style>
