package workspace

import (
	"context"
	"time"
)

// Status represents the lifecycle state of a workspace.
type Status string

const (
	StatusCreating Status = "creating"
	StatusRunning  Status = "running"
	StatusStopped  Status = "stopped"
)

// Kind distinguishes a user's own workspace from project sandboxes that AI
// agents work in.
type Kind string

const (
	KindInteractive Kind = "interactive"
	KindTask        Kind = "task"
	KindReview      Kind = "review"
)

// Valid reports whether k is a known workspace kind.
func (k Kind) Valid() bool {
	switch k {
	case KindInteractive, KindTask, KindReview:
		return true
	}
	return false
}

// DefaultAgentID is the baked-in AI agent identifier used when a workspace has
// no custom default agent selected.
const DefaultAgentID = "default"

// builtInAgentIDs mirrors the built-in agents in internal/ai/domain, which a
// workspace can pick as its default without an ai_agents row.
var builtInAgentIDs = []string{DefaultAgentID, "review"}

// Default resource limits for new workspaces.
const (
	DefaultMemoryLimit int64 = 2 * 1024 * 1024 * 1024 // 2 GB
	DefaultNanoCPUs    int64 = 2_000_000_000          // 2 cores
)

// SidecarService defines the lifecycle interface for workspace sidecar
// services. Implemented by the wsservice package; injected into
// the workspace service via SetSidecarService to break the import cycle.
type SidecarService interface {
	StartAll(ctx context.Context, workspaceID int64, networkName string) error
	StopAll(ctx context.Context, workspaceID int64) error
	DeleteAll(ctx context.Context, workspaceID int64) error
	EnvVars(ctx context.Context, workspaceID int64, networkName string) ([]string, error)
}

// Workspace represents a user's development workspace.
type Workspace struct {
	ID             int64
	UserID         int64
	Name           string
	Description    string
	Status         Status
	ContainerID    string
	VolumeName     string
	NetworkName    string
	AgentToken     string
	DefaultAgentID string
	MemoryLimit    int64 // bytes; 0 means use default
	NanoCPUs       int64 // billionths of a CPU; 0 means use default
	ProjectID      int64 // 0 when the workspace belongs to no project
	Kind           Kind
	Branch         string // the project branch a sandbox owns; empty otherwise
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// IsSandbox reports whether the workspace is a project sandbox run by an AI
// agent. Sandboxes are untrusted: they never receive the user's SSH keys.
func (ws *Workspace) IsSandbox() bool {
	return ws.Kind != KindInteractive
}

// SandboxSpec describes a project sandbox to create.
type SandboxSpec struct {
	ProjectID   int64
	Kind        Kind
	Branch      string
	Name        string
	Description string
}
