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
	// a service that has them (see Licenses). Its container is created only
	// once they are accepted.
	LicenseAcceptance *LicenseAcceptance `json:"licenseAcceptance,omitempty"`
}

// LicenseAcceptance is one user's acceptance of a service's licence terms.
type LicenseAcceptance struct {
	UserID     int64     `json:"userId"`
	AcceptedAt time.Time `json:"acceptedAt"`
	TermsURL   string    `json:"termsUrl"`
}

// License describes terms a user must accept before devpad deploys a
// service: devpad accepts them on the image's behalf (SQL Server's
// ACCEPT_EULA), so the user deploying it has to agree to them first.
type License struct {
	Name string
	URL  string
}

// Licenses lists the services whose images carry licence terms.
var Licenses = map[ServiceType]License{
	ServiceSQLServer: {
		Name: "Microsoft SQL Server licence terms",
		// The link SQL Server itself prints when its EULA isn't accepted.
		URL: "https://go.microsoft.com/fwlink/?LinkId=746388",
	},
}

// RequiredArch names the only Docker daemon architecture a service's image
// supports, for an image that does not support every one devpad runs on.
var RequiredArch = map[ServiceType]string{
	// Microsoft publishes SQL Server images for x86-64 only, and supports
	// them under emulation on no other architecture.
	ServiceSQLServer: "amd64",
}

// DefaultConfigs maps each supported service type to its default configuration.
var DefaultConfigs = map[ServiceType]ServiceConfig{
	ServicePostgres: {
		Image:       "postgres:17",
		Port:        5432,
		DefaultUser: "devpad",
		DefaultDB:   "devpad",
	},
	ServiceMongoDB: {
		Image:       "mongo:8",
		Port:        27017,
		DefaultUser: "devpad",
		DefaultDB:   "devpad",
	},
	ServiceMariaDB: {
		Image:       "mariadb:11",
		Port:        3306,
		DefaultUser: "devpad",
		DefaultDB:   "devpad",
	},
	ServiceCouchDB: {
		Image:       "couchdb:3",
		Port:        5984,
		DefaultUser: "devpad",
		DefaultDB:   "devpad",
	},
	ServiceMeilisearch: {
		Image: "getmeili/meilisearch:v1.45",
		Port:  7700,
	},
	ServiceSQLServer: {
		Image:       "mcr.microsoft.com/mssql/server:2025-latest",
		Port:        1433,
		DefaultUser: "devpad",
		DefaultDB:   "devpad",
	},
}

// ValidServiceType returns true if the given type is supported.
func ValidServiceType(t ServiceType) bool {
	_, ok := DefaultConfigs[t]
	return ok
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
