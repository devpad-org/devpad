import { apiClient } from './client'

export interface ServiceConfig {
  image: string
  port: number
  defaultUser: string
  defaultPass: string
  defaultDb: string
}

/** What a client reaches a service with, such as its connection URL. */
export interface ServiceConnection {
  label: string
  value: string
}

/** A name or secret a client needs, shown on its own. */
export interface ServiceCredential {
  label: string
  value: string
}

export interface WorkspaceService {
  id: number
  workspaceId: number
  /** A service type the server defines; serviceCatalog says how each looks. */
  serviceType: string
  status: 'running' | 'stopped'
  /** The service's host name on the workspace's network. */
  host: string
  connection: ServiceConnection
  credentials: ServiceCredential[]
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

/** A service type a workspace can deploy. */
export interface ServiceCatalogEntry {
  type: string
  /** The terms a user must accept to deploy it, when it has them. */
  license?: ServiceLicense
  /** Why this Docker host cannot run it, when it cannot. */
  unavailable?: string
}

interface ServiceListResponse {
  services: WorkspaceService[]
  /** Every service type the server defines, with its terms and availability. */
  catalog?: ServiceCatalogEntry[]
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
