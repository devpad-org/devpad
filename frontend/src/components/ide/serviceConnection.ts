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
  if (service.serviceType === 'meilisearch') {
    return `http://${host}:${config.port}`
  }

  return ''
}

export interface ServiceCredentialField {
  label: string
  value: string | number
  copyLabel: string
}

export function serviceCredentialFields(service: WorkspaceService): ServiceCredentialField[] {
  if (service.serviceType === 'meilisearch') {
    return [
      {
        label: 'Master key',
        value: service.config.defaultPass,
        copyLabel: 'Meilisearch master key',
      },
    ]
  }

  return [
    {
      label: 'User',
      value: service.config.defaultUser,
      copyLabel: 'service user',
    },
    {
      label: 'Password',
      value: service.config.defaultPass,
      copyLabel: 'service password',
    },
    {
      label: 'Database',
      value: service.config.defaultDb,
      copyLabel: 'service database',
    },
  ]
}
