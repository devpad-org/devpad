package wsservice

import "time"

// ServiceType identifies the kind of sidecar service.
type ServiceType string

const (
	ServicePostgres    ServiceType = "postgres"
	ServiceMongoDB     ServiceType = "mongodb"
	ServiceMariaDB     ServiceType = "mariadb"
	ServiceCouchDB     ServiceType = "couchdb"
	ServiceMeilisearch ServiceType = "meilisearch"
	ServiceSQLServer   ServiceType = "sqlserver"
)

// ServiceStatus represents the lifecycle state of a service.
type ServiceStatus string

const (
	StatusRunning ServiceStatus = "running"
	StatusStopped ServiceStatus = "stopped"
)

// ServiceConfig holds default connection info for each service type.
type ServiceConfig struct {
	Image       string `json:"image"`
	Port        int    `json:"port"`
	DefaultUser string `json:"defaultUser"`
	DefaultPass string `json:"defaultPass"`
	DefaultDB   string `json:"defaultDb"`

	// LicenseAcceptance records who accepted a service's licence terms, for
	// a service that has them (see definition.license). Its container is
	// created only once they are accepted.
	LicenseAcceptance *LicenseAcceptance `json:"licenseAcceptance,omitempty"`
}

// LicenseAcceptance is one user's acceptance of a service's licence terms.
type LicenseAcceptance struct {
	UserID     int64     `json:"userId"`
	AcceptedAt time.Time `json:"acceptedAt"`
	TermsURL   string    `json:"termsUrl"`
	// Image is the image the terms were accepted for, as the service names
	// it: the terms URL is a redirect whose target can change, so it alone
	// does not say what was deployed.
	Image string `json:"image"`
}

// WorkspaceService represents a sidecar service attached to a workspace.
type WorkspaceService struct {
	ID          int64
	WorkspaceID int64
	ServiceType ServiceType
	ContainerID string
	VolumeName  string
	Status      ServiceStatus
	Config      string // JSON string of ServiceConfig
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// ProjectService is a service every new workspace of a project gets its own
// copy of: each new sandbox, and the project workspace when it is created or
// rebuilt. No two workspaces ever share one.
type ProjectService struct {
	ProjectID   int64
	ServiceType ServiceType
	// LicenseAcceptance is the user accepting the service's licence terms,
	// for a service that has them; each copy is deployed under it.
	LicenseAcceptance *LicenseAcceptance
	CreatedAt         time.Time
}
