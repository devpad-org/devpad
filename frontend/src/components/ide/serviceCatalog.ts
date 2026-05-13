import type { WorkspaceService } from '@/api/services'

export interface ServiceCatalogItem {
  value: WorkspaceService['serviceType']
  label: string
  icon: string
  description: string
}

export const serviceCatalog: ServiceCatalogItem[] = [
  {
    value: 'postgres',
    label: 'PostgreSQL',
    icon: '🐘',
    description: 'Relational database for SQL-backed applications.',
  },
  {
    value: 'mongodb',
    label: 'MongoDB',
    icon: '🍃',
    description: 'Document database with an admin-authenticated default database.',
  },
  {
    value: 'mariadb',
    label: 'MariaDB',
    icon: '🐬',
    description: 'MySQL-compatible relational database service.',
  },
  {
    value: 'couchdb',
    label: 'CouchDB',
    icon: '🛋️',
    description: 'HTTP document database with built-in JSON storage.',
  },
]

export function serviceTypeLabel(type: WorkspaceService['serviceType'] | string) {
  return serviceCatalog.find((service) => service.value === type)?.label ?? type
}

export function serviceTypeIcon(type: WorkspaceService['serviceType'] | string) {
  return serviceCatalog.find((service) => service.value === type)?.icon ?? '📦'
}

