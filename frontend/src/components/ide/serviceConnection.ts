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
  if (service.serviceType === 'sqlserver') {
    // ADO.NET form. SQL Server's certificate is self-signed, so clients
    // that encrypt by default have to be told to trust it.
    return `Server=${host},${config.port};Database=${config.defaultDb};User Id=${config.defaultUser};Password=${config.defaultPass};TrustServerCertificate=True`
  }

  return ''
}

export function connectionLabel(service: WorkspaceService): string {
  return service.serviceType === 'sqlserver' ? 'Connection string' : 'Connection URL'
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
