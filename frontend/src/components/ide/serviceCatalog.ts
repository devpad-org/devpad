import {
  couchdbLogo,
  databaseLogo,
  mariadbLogo,
  meilisearchLogo,
  mongodbLogo,
  packageLogo,
  postgresLogo,
  type ServiceLogo,
} from './serviceLogos'

/** How the IDE shows a service type the server defines. */
export interface ServiceCatalogItem {
  value: string
  label: string
  logo: ServiceLogo
  /** The theme hue token the logo is drawn in. */
  hue: `--${string}`
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
    logo: postgresLogo,
    hue: '--hue-blue',
    description: 'Relational database for SQL-backed applications.',
  },
  {
    value: 'mongodb',
    label: 'MongoDB',
    logo: mongodbLogo,
    hue: '--hue-green',
    description: 'Document database with an admin-authenticated default database.',
  },
  {
    value: 'mariadb',
    label: 'MariaDB',
    logo: mariadbLogo,
    hue: '--hue-aqua',
    description: 'MySQL-compatible relational database service.',
  },
  {
    value: 'couchdb',
    label: 'CouchDB',
    logo: couchdbLogo,
    hue: '--hue-red',
    description: 'HTTP document database with built-in JSON storage.',
  },
  {
    value: 'meilisearch',
    label: 'Meilisearch',
    logo: meilisearchLogo,
    hue: '--hue-purple',
    description: 'Fast full-text search engine with a workspace-local master key.',
  },
  {
    value: 'sqlserver',
    label: 'SQL Server',
    logo: databaseLogo,
    hue: '--hue-orange',
    description: 'Microsoft SQL Server 2025, free Enterprise Developer edition. Needs 2 GB of memory.',
  },
]

/** How to show a service type, with plain defaults for one not listed. */
export function serviceCatalogItem(type: string): ServiceCatalogItem {
  return serviceCatalog.find((service) => service.value === type)
    ?? { value: type, label: type, logo: packageLogo, hue: '--text-secondary', description: '' }
}

/** Where a service type comes in the catalog; unlisted types come last. */
export function serviceCatalogOrder(type: string): number {
  const index = serviceCatalog.findIndex((service) => service.value === type)
  return index === -1 ? serviceCatalog.length : index
}

export function serviceTypeLabel(type: string) {
  return serviceCatalogItem(type).label
}
