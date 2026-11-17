<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { ApiError } from '@/api/client'
import { projectApi } from '@/api/projects'
import { projectServiceApi, serviceApi, type ProjectService, type ServiceCatalogEntry } from '@/api/services'
import { useActiveProjectStore } from '@/stores/activeProject'
import ConfirmModal from '@/components/ConfirmModal.vue'
import ServiceLogo from '@/components/ide/ServiceLogo.vue'
import { serviceCatalogItem, serviceCatalogOrder, serviceTypeLabel } from '@/components/ide/serviceCatalog'

// The services each new sandbox gets its own copy of, and the project
// workspace when it is made or rebuilt.
const props = defineProps<{
  // The project workspace's status, or 'none' before it is created.
  workspaceStatus: string
  // The project workspace, when there is one.
  workspaceId: number | null
}>()

const store = useActiveProjectStore()

const services = ref<ProjectService[]>([])
const catalog = ref<ServiceCatalogEntry[]>([])
// The service types the project workspace has.
const workspaceTypes = ref<Set<string> | null>(null)
const loaded = ref(false)
const error = ref('')
const notice = ref('')
const adding = ref(false)
const removing = ref<string | null>(null)
const rebuilding = ref(false)
const confirmRebuild = ref(false)
const selected = ref('')
// Licence terms accepted for this add. Never remembered: whoever adds a
// service with terms accepts them each time.
const accepted = ref(false)

// Bumped for each load, so a slow response can't overwrite a newer one.
let seq = 0

const sorted = computed(() => [...services.value]
  .sort((a, b) => serviceCatalogOrder(a.serviceType) - serviceCatalogOrder(b.serviceType)))

const addable = computed(() => {
  const have = new Set(services.value.map((s) => s.serviceType))
  return catalog.value
    .filter((entry) => !have.has(entry.type))
    .sort((a, b) => serviceCatalogOrder(a.type) - serviceCatalogOrder(b.type))
})

const selectedEntry = computed(() => addable.value.find((e) => e.type === selected.value) ?? null)
const canAdd = computed(() => {
  const entry = selectedEntry.value
  return !!entry && !entry.unavailable && (!entry.license || accepted.value) && !adding.value && store.projectId !== null
})

// The project's services the project workspace lacks, which it gets when
// it is rebuilt.
const missing = computed(() => {
  const have = workspaceTypes.value
  if (!have || props.workspaceStatus === 'none') return []
  return sorted.value.filter((s) => !have.has(s.serviceType)).map((s) => serviceTypeLabel(s.serviceType))
})

async function load() {
  const id = store.projectId
  if (id === null) return
  const mine = ++seq
  try {
    const res = await projectServiceApi.list(id)
    let types: Set<string> | null = null
    if (props.workspaceId !== null) {
      const ws = await serviceApi.list(props.workspaceId)
      types = new Set(ws.services.map((s) => s.serviceType))
    }
    if (mine !== seq) return
    services.value = res.services
    catalog.value = res.catalog ?? []
    workspaceTypes.value = types
    error.value = ''
  } catch (e) {
    if (mine !== seq) return
    error.value = e instanceof Error ? e.message : 'Failed to load the services'
  } finally {
    if (mine === seq) loaded.value = true
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

async function rebuildWorkspace() {
  const id = store.projectId
  if (id === null) return
  rebuilding.value = true
  error.value = ''
  notice.value = ''
  try {
    await projectApi.rebuildWorkspace(id)
    notice.value = 'The workspace was rebuilt.'
    await load()
  } catch (e) {
    error.value = e instanceof Error ? e.message : 'Failed to rebuild the workspace'
  } finally {
    confirmRebuild.value = false
    rebuilding.value = false
  }
}

watch(selected, () => { accepted.value = false })
watch(() => [store.projectId, props.workspaceId, props.workspaceStatus], () => void load(), { immediate: true })
</script>

<template>
  <div v-if="loaded" class="side-section">
    <div class="side-section-title">Services</div>

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
      <p class="svc-note svc-missing">Your workspace doesn't have {{ missing.join(', ') }} yet; rebuilding it adds {{ missing.length === 1 ? 'it' : 'them' }}.</p>
      <div class="svc-actions">
        <button
          class="btn btn-secondary btn-sm"
          :disabled="rebuilding || !store.ready"
          @click="confirmRebuild = true"
        >
          Rebuild workspace
        </button>
      </div>
    </template>

    <div v-if="addable.length" class="svc-add">
      <select v-model="selected" class="form-input svc-select" aria-label="Service to add" :disabled="adding">
        <option value="" disabled>Add a service…</option>
        <option v-for="entry in addable" :key="entry.type" :value="entry.type" :disabled="!!entry.unavailable">
          {{ serviceCatalogItem(entry.type).label }}{{ entry.unavailable ? ' (unavailable)' : '' }}
        </option>
      </select>
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

    <ConfirmModal
      :show="confirmRebuild"
      title="Rebuild Workspace"
      message="Rebuilding replaces the workspace's container with one made from the current environment, and adds the project's new services. Everything in /workspace is kept, including your repo and its uncommitted changes. Anything outside it is lost: packages you installed by hand, files in your home directory, and running processes. Terminals reconnect once it's back."
      confirm-label="Rebuild"
      variant="danger"
      :loading="rebuilding"
      @confirm="rebuildWorkspace"
      @cancel="confirmRebuild = false"
    />
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

.svc-select {
  font-size: 0.75rem;
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
