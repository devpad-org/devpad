import { defineStore } from 'pinia'
import { ref } from 'vue'
import { serviceApi, type CreateServiceOptions, type ServiceCatalogEntry, type WorkspaceService } from '@/api/services'

export const useServiceStore = defineStore('services', () => {
  const services = ref<WorkspaceService[]>([])
  const catalog = ref<ServiceCatalogEntry[]>([])
  const loading = ref(false)
  const error = ref<string | null>(null)
  // The workspace whose services were last loaded, null before any are.
  const loadedFor = ref<number | null>(null)

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

  function $reset() {
    services.value = []
    catalog.value = []
    loading.value = false
    error.value = null
    loadedFor.value = null
  }

  return { services, catalog, loading, error, loadedFor, fetchServices, addService, startService, stopService, removeService, $reset }
})
