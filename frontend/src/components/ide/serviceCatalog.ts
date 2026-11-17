import type { WorkspaceService } from '@/api/services'

export interface ServiceLicense {
  /** Link text for the terms, read as "I accept the …". */
  name: string
  url: string
  /** What accepting them means here, shown beside the checkbox. */
  notes: string[]
}

export interface ServiceCatalogItem {
  value: WorkspaceService['serviceType']
  label: string
  icon: string
  description: string
  /** Terms the user must accept before the service is deployed. */
  license?: ServiceLicense
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
  {
    value: 'meilisearch',
    label: 'Meilisearch',
    icon: '🔎',
    description: 'Fast full-text search engine with a workspace-local master key.',
  },
  {
    value: 'sqlserver',
    label: 'SQL Server',
    icon: '🗄️',
    description: 'Microsoft SQL Server 2025, free Enterprise Developer edition. Needs 2 GB of memory and an x86-64 host.',
    license: {
      name: 'Microsoft SQL Server licence terms',
      // The link SQL Server itself prints; the server records it with the acceptance.
      url: 'https://go.microsoft.com/fwlink/?LinkId=746388',
      notes: [
        'Developer edition is for development and testing only, not production.',
        'SQL Server sends usage data to Microsoft, which free editions cannot turn off.',
      ],
    },
  },
]

export function serviceTypeLabel(type: WorkspaceService['serviceType'] | string) {
  return serviceCatalog.find((service) => service.value === type)?.label ?? type
}

export function serviceTypeIcon(type: WorkspaceService['serviceType'] | string) {
  return serviceCatalog.find((service) => service.value === type)?.icon ?? '📦'
}
