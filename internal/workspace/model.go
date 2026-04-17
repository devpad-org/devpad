package workspace

import "time"

// Status represents the lifecycle state of a workspace.
type Status string

const (
	StatusCreating Status = "creating"
	StatusRunning  Status = "running"
	StatusStopped  Status = "stopped"
)

// Default resource limits for new workspaces.
const (
	DefaultMemoryLimit int64 = 2 * 1024 * 1024 * 1024 // 2 GB
	DefaultNanoCPUs    int64 = 2_000_000_000          // 2 cores
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
	MemoryLimit int64 // bytes; 0 means use default
	NanoCPUs    int64 // billionths of a CPU; 0 means use default
	CreatedAt   time.Time
	UpdatedAt   time.Time
}
