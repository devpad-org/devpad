/** How the IDE shows a service type the server defines. */
export interface ServiceCatalogItem {
  value: string
  label: string
  icon: string
  description: string
}

// Every service type internal/wsservice defines, in the order the catalog
// lists them: TestServiceCatalogMatchesFrontend fails when the two differ.
// What a service is and needs (its terms, its host architecture, how to
// connect) comes from the server.
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
    description: 'Microsoft SQL Server 2025, free Enterprise Developer edition. Needs 2 GB of memory.',
  },
]

/** How to show a service type, with plain defaults for one not listed. */
export function serviceCatalogItem(type: string): ServiceCatalogItem {
  return serviceCatalog.find((service) => service.value === type)
    ?? { value: type, label: type, icon: '📦', description: '' }
}

/** Where a service type comes in the catalog; unlisted types come last. */
export function serviceCatalogOrder(type: string): number {
  const index = serviceCatalog.findIndex((service) => service.value === type)
  return index === -1 ? serviceCatalog.length : index
}

export function serviceTypeLabel(type: string) {
  return serviceCatalogItem(type).label
}

export function serviceTypeIcon(type: string) {
  return serviceCatalogItem(type).icon
}
