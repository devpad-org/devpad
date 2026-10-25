package workspace

import (
	"context"
	"io"

	"github.com/devpad-org/devpad/internal/agent"
)

// LifecycleService defines workspace lifecycle and CRUD operations.
type LifecycleService interface {
	Create(ctx context.Context, userID int64, name, description string) (*Workspace, error)
	List(ctx context.Context, userID int64) ([]*Workspace, error)
	Update(ctx context.Context, userID, workspaceID int64, name, description string) (*Workspace, error)
	SetDefaultAgent(ctx context.Context, userID, workspaceID int64, agentID string) (*Workspace, error)
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
	ListFiles(ctx context.Context, userID, workspaceID int64, path string, opts agent.ListFilesOptions) (*agent.FileList, error)
	ReadFile(ctx context.Context, userID, workspaceID int64, path string) ([]byte, error)
	WriteFile(ctx context.Context, userID, workspaceID int64, path string, content []byte) error
	DeleteFile(ctx context.Context, userID, workspaceID int64, path string) error
}

// FileUploadService defines streamed workspace file uploads.
type FileUploadService interface {
	UploadFile(ctx context.Context, userID, workspaceID int64, path string, content io.Reader) error
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
	FileUploadService
	FileTreeService
	FileSearchService
}

// CommandService defines workspace command execution.
type CommandService interface {
	RunCommand(ctx context.Context, userID, workspaceID int64, command string) (*agent.CommandResult, error)
	StartCommand(ctx context.Context, userID, workspaceID int64, command, cwd string) (*agent.ManagedCommand, error)
	CommandStatus(ctx context.Context, userID, workspaceID int64, commandID string) (*agent.ManagedCommand, error)
	ReadCommandOutput(ctx context.Context, userID, workspaceID int64, commandID string, cursor int64, maxBytes, waitMS int) (*agent.CommandOutput, error)
	StopCommand(ctx context.Context, userID, workspaceID int64, commandID string) (*agent.ManagedCommand, error)
}

// ProcessService defines workspace process inspection and termination.
type ProcessService interface {
	ListProcesses(ctx context.Context, userID, workspaceID int64) (*agent.ProcessList, error)
	KillProcess(ctx context.Context, userID, workspaceID int64, pid int) error
}

// GitService defines workspace Git operations.
type GitService interface {
	GitStatus(ctx context.Context, userID, workspaceID int64) (*agent.GitStatus, error)
	GitLog(ctx context.Context, userID, workspaceID int64, count int, opts agent.GitLogOptions) ([]agent.GitCommit, error)
	GitCommitFiles(ctx context.Context, userID, workspaceID int64, commit string) ([]agent.GitCommitFile, error)
	GitCommitFileDiff(ctx context.Context, userID, workspaceID int64, commit, path, oldPath string) (*agent.GitFileDiff, error)
	GitFileDiff(ctx context.Context, userID, workspaceID int64, path string, staged bool) (*agent.GitFileDiff, error)
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

// SandboxService defines project sandbox creation and branch transfer. The
// project service uses it to seed sandboxes and collect their work.
type SandboxService interface {
	CreateSandbox(ctx context.Context, userID int64, spec SandboxSpec) (*Workspace, error)
	ListByProject(ctx context.Context, userID, projectID int64) ([]*Workspace, error)
	SeedSandboxRepo(ctx context.Context, userID, workspaceID int64, base string, bundle io.Reader) (*agent.RepoBranch, error)
	BundleSandboxBranch(ctx context.Context, userID, workspaceID int64) (branch string, bundle io.ReadCloser, err error)
	FetchSandboxBase(ctx context.Context, userID, workspaceID int64, branch string, bundle io.Reader) (*agent.RepoBranch, error)
}

// ProjectWorkspaceService defines a project's own interactive workspace and
// the branch transfers devpad drives for it. Seeding and base refreshes go
// through SandboxService, which serves both.
type ProjectWorkspaceService interface {
	CreateProjectWorkspace(ctx context.Context, userID int64, spec ProjectWorkspaceSpec) (*Workspace, error)
	CheckoutProjectBranch(ctx context.Context, userID, workspaceID int64, branch string, bundle io.Reader) (*agent.RepoCheckout, error)
	BundleProjectBranch(ctx context.Context, userID, workspaceID int64, branch string) (io.ReadCloser, error)
}

// ConversionService turns a standalone workspace into a project's
// workspace. The project service drives it: it reads the workspace's repos,
// moves their branches into a new canonical repo, then attaches the
// workspace to the project.
type ConversionService interface {
	DiscoverRepos(ctx context.Context, userID, workspaceID int64) (*agent.RepoDiscovery, error)
	BeginConversion(ctx context.Context, userID, workspaceID int64) (release func(), err error)
	InitRepoFromContents(ctx context.Context, userID, workspaceID int64, branch string, includeSensitive bool) (*agent.RepoContentsInit, error)
	UndoInitRepoFromContents(ctx context.Context, userID, workspaceID int64, sha string) error
	BundleAllBranches(ctx context.Context, userID, workspaceID int64, dir string) (io.ReadCloser, error)
	AttachToProject(ctx context.Context, userID, workspaceID, projectID int64) (*agent.SSHCleanup, error)
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
	ProcessService
	GitService
	AgentResolver
	InfoService
	SandboxService
	ProjectWorkspaceService
	ConversionService
	SidecarBinder
}

// HandlerService is the workspace HTTP handler's required service surface.
type HandlerService interface {
	LifecycleService
	AccessService
	FileContentService
	FileUploadService
	FileTreeService
	ProcessService
	GitService
	AgentResolver
	InfoService
}
