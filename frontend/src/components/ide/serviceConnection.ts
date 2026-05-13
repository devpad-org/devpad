import type { WorkspaceService } from '@/api/services'

export function serviceHost(service: WorkspaceService): string {
  return `devpad-svc-${service.workspaceId}-${service.serviceType}`
}

export function connectionString(service: WorkspaceService): string {
  const config = service.config
  const host = serviceHost(service)

  if (service.serviceType === 'postgres') {
    return `postgresql://${config.defaultUser}:${config.defaultPass}@${host}:${config.port}/${config.defaultDb}`
  }
  if (service.serviceType === 'mongodb') {
    return `mongodb://${config.defaultUser}:${config.defaultPass}@${host}:${config.port}/${config.defaultDb}?authSource=admin`
  }
  if (service.serviceType === 'mariadb') {
    return `mysql://${config.defaultUser}:${config.defaultPass}@${host}:${config.port}/${config.defaultDb}`
  }
  if (service.serviceType === 'couchdb') {
    return `http://${config.defaultUser}:${config.defaultPass}@${host}:${config.port}/${config.defaultDb}`
  }

  return ''
}

