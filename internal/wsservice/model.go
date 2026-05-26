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
