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
  serviceType: 'postgres' | 'mongodb' | 'mariadb' | 'couchdb' | 'meilisearch' | 'sqlserver'
  status: 'running' | 'stopped'
  config: ServiceConfig
  createdAt: string
  updatedAt: string
}

/** Terms a user must accept before devpad deploys a service. */
export interface ServiceLicense {
  /** Link text for the terms, read as "I accept the …". */
  name: string
  url: string
  /** What accepting them means here, shown beside the checkbox. */
  notes: string[]
}

interface ServiceListResponse {
  services: WorkspaceService[]
  /** Service types this Docker host cannot run, each with the reason. */
  unavailable?: Partial<Record<WorkspaceService['serviceType'], string>>
  /** The licence terms of each service type that has them. */
  licenses?: Partial<Record<WorkspaceService['serviceType'], ServiceLicense>>
}

export interface CreateServiceOptions {
  /** The user accepting the service's licence terms, for a service that has them. */
  acceptLicense?: boolean
}

interface ServiceResponse {
  service: WorkspaceService
}

export const serviceApi = {
  list(workspaceId: number): Promise<ServiceListResponse> {
    return apiClient.get<ServiceListResponse>(`/api/workspaces/${workspaceId}/services`)
  },

  create(workspaceId: number, serviceType: string, options: CreateServiceOptions = {}): Promise<ServiceResponse> {
    return apiClient.post<ServiceResponse>(`/api/workspaces/${workspaceId}/services`, {
      serviceType,
      acceptLicense: options.acceptLicense ?? false,
    })
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
