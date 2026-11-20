import { defineStore } from 'pinia'
import { ref } from 'vue'
import {
  projectServiceApi,
  serviceApi,
  type CreateServiceOptions,
  type ProjectService,
  type ProjectServiceListResponse,
  type ServiceCatalogEntry,
  type WorkspaceService,
} from '@/api/services'

export const useServiceStore = defineStore('services', () => {
  const services = ref<WorkspaceService[]>([])
  const catalog = ref<ServiceCatalogEntry[]>([])
  const loading = ref(false)
  const error = ref<string | null>(null)
  // The workspace whose services were last loaded, null before any are.
  const loadedFor = ref<number | null>(null)
  // The services each new workspace of a project gets a copy of, shared by
  // the project panel and the project workspace's services panel.
  const projectServices = ref<ProjectService[]>([])
  const projectLoadedFor = ref<number | null>(null)
  // Bumped for each load of a project's services, so a slow response can't
  // overwrite a newer one.
  let projectSeq = 0

  async function fetchServices(workspaceId: number) {
    loading.value = true
    error.value = null
    try {
      const response = await serviceApi.list(workspaceId)
      services.value = response.services
      catalog.value = response.catalog ?? []
      loadedFor.value = workspaceId
    } catch {
      error.value = 'Failed to load services'
    } finally {
      loading.value = false
    }
  }

  async function addService(workspaceId: number, serviceType: string, options: CreateServiceOptions = {}) {
    const response = await serviceApi.create(workspaceId, serviceType, options)
    const index = services.value.findIndex((s) => s.id === response.service.id)
    if (index !== -1) {
      services.value[index] = response.service
    } else {
      services.value.push(response.service)
    }
    return response.service
  }

  async function startService(workspaceId: number, serviceId: number) {
    const response = await serviceApi.start(workspaceId, serviceId)
    const index = services.value.findIndex((s) => s.id === serviceId)
    if (index !== -1) {
      services.value[index] = response.service
    }
    return response.service
  }

  async function stopService(workspaceId: number, serviceId: number) {
    const response = await serviceApi.stop(workspaceId, serviceId)
    const index = services.value.findIndex((s) => s.id === serviceId)
    if (index !== -1) {
      services.value[index] = response.service
    }
    return response.service
  }

  async function removeService(workspaceId: number, serviceId: number) {
    await serviceApi.delete(workspaceId, serviceId)
    services.value = services.value.filter((s) => s.id !== serviceId)
  }

  // fetchProjectServices loads a project's services, throwing when it
  // can't.
  async function fetchProjectServices(projectId: number) {
    const mine = ++projectSeq
    const response = await projectServiceApi.list(projectId)
    if (mine === projectSeq) setProjectServices(projectId, response)
  }

  function setProjectServices(projectId: number, response: ProjectServiceListResponse) {
    projectServices.value = response.services
    if (response.catalog?.length) catalog.value = response.catalog
    projectLoadedFor.value = projectId
  }

  async function adoptWorkspaceServices(projectId: number) {
    const mine = ++projectSeq
    const response = await projectServiceApi.adoptFromWorkspace(projectId)
    if (mine === projectSeq) setProjectServices(projectId, response)
  }

  function $reset() {
    services.value = []
    catalog.value = []
    loading.value = false
    error.value = null
    loadedFor.value = null
    projectServices.value = []
    projectLoadedFor.value = null
    projectSeq++
  }

  return {
    services,
    catalog,
    loading,
    error,
    loadedFor,
    projectServices,
    projectLoadedFor,
    fetchServices,
    fetchProjectServices,
    adoptWorkspaceServices,
    addService,
    startService,
    stopService,
    removeService,
    $reset,
  }
})
