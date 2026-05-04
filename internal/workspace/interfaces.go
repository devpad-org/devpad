package workspace

import (
	"context"

	"github.com/devpad-org/devpad/internal/agent"
)

// LifecycleService defines workspace lifecycle and CRUD operations.
type LifecycleService interface {
	Create(ctx context.Context, userID int64, name, description string) (*Workspace, error)
	List(ctx context.Context, userID int64) ([]*Workspace, error)
	Update(ctx context.Context, userID, workspaceID int64, name, description string) (*Workspace, error)
	Delete(ctx context.Context, userID, workspaceID int64) error
	Start(ctx context.Context, userID, workspaceID int64) (*Workspace, error)
	Stop(ctx context.Context, userID, workspaceID int64) (*Workspace, error)
}

// AccessService checks workspace access and ownership.
type AccessService interface {
	Get(ctx context.Context, userID, workspaceID int64) (*Workspace, error)
}

// AdminService defines workspace operations used by admin workflows.
type AdminService interface {
	ListAll(ctx context.Context) ([]*Workspace, error)
	UpdateResourceLimits(ctx context.Context, workspaceID, memoryLimit, nanoCPUs int64) (*Workspace, error)
}

// FileContentService defines workspace file content operations.
type FileContentService interface {
	ListFiles(ctx context.Context, userID, workspaceID int64, path string) ([]agent.FileEntry, error)
	ReadFile(ctx context.Context, userID, workspaceID int64, path string) ([]byte, error)
	WriteFile(ctx context.Context, userID, workspaceID int64, path string, content []byte) error
	DeleteFile(ctx context.Context, userID, workspaceID int64, path string) error
}

// FileTreeService defines workspace file tree mutation operations.
type FileTreeService interface {
	CreateDirectory(ctx context.Context, userID, workspaceID int64, path string) error
	RenameFile(ctx context.Context, userID, workspaceID int64, oldPath, newPath string) error
}

// FileSearchService defines workspace file search operations.
type FileSearchService interface {
	SearchFiles(ctx context.Context, userID, workspaceID int64, pattern, pathFilter string, maxResults int) ([]agent.SearchResult, error)
}

// FileService defines all workspace file operations and search.
type FileService interface {
	FileContentService
	FileTreeService
	FileSearchService
}

// CommandService defines workspace command execution.
type CommandService interface {
	RunCommand(ctx context.Context, userID, workspaceID int64, command string) (*agent.CommandResult, error)
}

// GitService defines workspace Git operations.
type GitService interface {
	GitStatus(ctx context.Context, userID, workspaceID int64) (*agent.GitStatus, error)
	GitLog(ctx context.Context, userID, workspaceID int64, count int) ([]agent.GitCommit, error)
	GitBranches(ctx context.Context, userID, workspaceID int64) (*agent.GitBranches, error)
	GitDiff(ctx context.Context, userID, workspaceID int64, path string, staged bool) (string, error)
	GitRemotes(ctx context.Context, userID, workspaceID int64) ([]agent.GitRemote, error)
	GitAction(ctx context.Context, userID, workspaceID int64, req GitActionRequest) (*agent.GitActionResult, error)
}

// AgentResolver resolves a running workspace's in-container agent endpoint.
type AgentResolver interface {
	AgentAddr(ctx context.Context, userID, workspaceID int64) (addr, agentToken string, err error)
}

// InfoService returns workspace agent and container information.
type InfoService interface {
	Info(ctx context.Context, userID, workspaceID int64) (*WorkspaceInfo, error)
}

// SidecarBinder injects the sidecar service lifecycle dependency.
type SidecarBinder interface {
	SetSidecarService(sidecar SidecarService)
}

// Service is the full workspace capability set implemented by the concrete
// workspace service. Consumers should depend on the smallest capability
// interface they need instead of this aggregate.
type Service interface {
	LifecycleService
	AccessService
	AdminService
	FileService
	CommandService
	GitService
	AgentResolver
	InfoService
	SidecarBinder
}

// HandlerService is the workspace HTTP handler's required service surface.
type HandlerService interface {
	LifecycleService
	AccessService
	FileContentService
	FileTreeService
	GitService
	AgentResolver
	InfoService
}
