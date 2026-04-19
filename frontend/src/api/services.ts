import { apiClient } from './client'

export interface ServiceConfig {
  image: string
  port: number
  defaultUser: string
  defaultPass: string
  defaultDb: string
}

export interface WorkspaceService {
  id: number
  workspaceId: number
  serviceType: 'postgres' | 'mongodb'
  status: 'running' | 'stopped'
  config: ServiceConfig
  createdAt: string
  updatedAt: string
}

interface ServiceListResponse {
  services: WorkspaceService[]
}

interface ServiceResponse {
  service: WorkspaceService
}

export const serviceApi = {
  list(workspaceId: number): Promise<ServiceListResponse> {
    return apiClient.get<ServiceListResponse>(`/api/workspaces/${workspaceId}/services`)
  },

  create(workspaceId: number, serviceType: string): Promise<ServiceResponse> {
    return apiClient.post<ServiceResponse>(`/api/workspaces/${workspaceId}/services`, { serviceType })
  },

  start(workspaceId: number, serviceId: number): Promise<ServiceResponse> {
    return apiClient.post<ServiceResponse>(`/api/workspaces/${workspaceId}/services/${serviceId}/start`, {})
  },

  stop(workspaceId: number, serviceId: number): Promise<ServiceResponse> {
    return apiClient.post<ServiceResponse>(`/api/workspaces/${workspaceId}/services/${serviceId}/stop`, {})
  },

  delete(workspaceId: number, serviceId: number): Promise<void> {
    return apiClient.delete(`/api/workspaces/${workspaceId}/services/${serviceId}`)
  },
}
