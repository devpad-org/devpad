package wsservice

import "time"

// ServiceType identifies the kind of database service.
type ServiceType string

const (
	ServicePostgres ServiceType = "postgres"
	ServiceMongoDB  ServiceType = "mongodb"
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
		DefaultPass: "devpad",
		DefaultDB:   "devpad",
	},
	ServiceMongoDB: {
		Image:       "mongo:8",
		Port:        27017,
		DefaultUser: "devpad",
		DefaultPass: "devpad",
		DefaultDB:   "devpad",
	},
}

// ValidServiceType returns true if the given type is supported.
func ValidServiceType(t ServiceType) bool {
	_, ok := DefaultConfigs[t]
	return ok
}

// WorkspaceService represents a database sidecar attached to a workspace.
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
