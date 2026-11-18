<script setup lang="ts">
import DocLink from '@/components/DocLink.vue'
import { computed, ref, watch } from 'vue'
import { ApiError } from '@/api/client'
import { projectServiceApi } from '@/api/services'
import { useActiveProjectStore } from '@/stores/activeProject'
import { useServiceStore } from '@/stores/services'
import ServiceLogo from '@/components/ide/ServiceLogo.vue'
import SelectMenu, { type SelectMenuOption } from '@/components/SelectMenu.vue'
import { serviceCatalogItem, serviceCatalogOrder, serviceTypeLabel } from '@/components/ide/serviceCatalog'

// The services each new sandbox gets its own copy of, and the project
// workspace when it is made or rebuilt or asks for them.
const props = defineProps<{
  // The project workspace's status, or 'none' before it is created.
  workspaceStatus: string
  // The project workspace, when there is one.
  workspaceId: number | null
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
  <div v-if="loaded" class="side-section">
    <div class="side-section-title">Services<DocLink topic="projectServices" label="Project services" /></div>

    <ul v-if="sorted.length" class="svc-list">
      <li v-for="svc in sorted" :key="svc.serviceType" class="svc-row">
        <ServiceLogo class="svc-logo" :type="svc.serviceType" />
        <span class="svc-name">{{ serviceTypeLabel(svc.serviceType) }}</span>
        <button
          class="icon-btn svc-remove"
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
    <p class="svc-note">
      {{ sorted.length
        ? 'Each new sandbox gets its own copy, started empty before the setup script runs, and stops and goes with it.'
        : 'None. Add the databases and search engines your code needs, and each new sandbox gets its own copy before the setup script runs.' }}
    </p>

    <template v-if="missing.length">
      <p class="svc-note svc-missing">Your workspace doesn't have {{ missing.join(', ') }} yet.</p>
      <div class="svc-actions">
        <button
          class="btn btn-secondary btn-sm"
          :disabled="addingToWorkspace || !store.ready"
          @click="addToWorkspace"
        >
          {{ addingToWorkspace ? 'Adding…' : 'Add to workspace' }}
        </button>
      </div>
    </template>

    <div v-if="addable.length" class="svc-add">
      <SelectMenu
        v-model="selected"
        :options="addOptions"
        title="Service to add"
        placeholder="Add a service…"
        variant="field"
        :disabled="adding"
      />
      <template v-if="selectedEntry">
        <p v-if="serviceCatalogItem(selectedEntry.type).description" class="svc-note">{{ serviceCatalogItem(selectedEntry.type).description }}</p>
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
        <div class="svc-actions">
          <button class="btn btn-secondary btn-sm" :disabled="!canAdd" @click="add">
            {{ adding ? 'Adding…' : 'Add' }}
          </button>
        </div>
      </template>
    </div>

    <p v-if="error" class="notice notice-error svc-message">{{ error }}</p>
    <p v-if="notice" class="notice notice-success svc-message">{{ notice }}</p>

  </div>
</template>

<style scoped src="./shared.css"></style>
<style scoped src="./panel.css"></style>
<style scoped>
.svc-list {
  display: flex;
  flex-direction: column;
  gap: 2px;
  margin: 0 0 var(--space-1);
  padding: 0;
  list-style: none;
}

.svc-row {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  font-size: 0.78rem;
  color: var(--text-primary);
}

.svc-logo {
  font-size: 0.9rem;
  flex-shrink: 0;
}

.svc-name {
  flex: 1;
  min-width: 0;
  overflow-wrap: anywhere;
}

.svc-remove {
  flex-shrink: 0;
}

.svc-note {
  margin-top: var(--space-1);
  font-size: 0.72rem;
  line-height: 1.4;
  color: var(--text-tertiary);
}

.svc-missing {
  color: var(--warning);
}

.svc-add {
  display: flex;
  flex-direction: column;
  gap: var(--space-1);
  margin-top: var(--space-2);
}

.svc-license {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
  margin-top: var(--space-1);
  font-size: 0.72rem;
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

.svc-actions {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-1);
  margin-top: var(--space-2);
}

.svc-message {
  margin: var(--space-2) 0 0;
}
</style>
