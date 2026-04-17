package workspace

import "time"

// Status represents the lifecycle state of a workspace.
type Status string

const (
	StatusCreating Status = "creating"
	StatusRunning  Status = "running"
	StatusStopped  Status = "stopped"
)

// Workspace represents a user's development workspace.
type Workspace struct {
	ID          int64
	UserID      int64
	Name        string
	Description string
	Status      Status
	ContainerID string
	VolumeName  string
	AgentToken  string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}
