package workspace

import (
	"context"
	"errors"
	"fmt"

	"github.com/devpad-org/devpad/internal/agent"
	"github.com/devpad-org/devpad/internal/container"
)

var (
	ErrNotFound   = errors.New("workspace not found")
	ErrForbidden  = errors.New("access denied")
	ErrNotRunning = errors.New("workspace is not running")
)

// Service defines workspace business logic.
type Service interface {
	Create(ctx context.Context, userID int64, name, description string) (*Workspace, error)
	Get(ctx context.Context, userID, workspaceID int64) (*Workspace, error)
	List(ctx context.Context, userID int64) ([]*Workspace, error)
	Update(ctx context.Context, userID, workspaceID int64, name, description string) (*Workspace, error)
	Delete(ctx context.Context, userID, workspaceID int64) error
	Start(ctx context.Context, userID, workspaceID int64) (*Workspace, error)
	Stop(ctx context.Context, userID, workspaceID int64) (*Workspace, error)

	// File operations (delegated to in-container agent)
	ListFiles(ctx context.Context, userID, workspaceID int64, path string) ([]agent.FileEntry, error)
	ReadFile(ctx context.Context, userID, workspaceID int64, path string) ([]byte, error)
	WriteFile(ctx context.Context, userID, workspaceID int64, path string, content []byte) error
	DeleteFile(ctx context.Context, userID, workspaceID int64, path string) error
	CreateDirectory(ctx context.Context, userID, workspaceID int64, path string) error
	RenameFile(ctx context.Context, userID, workspaceID int64, oldPath, newPath string) error

	// File search and command execution
	SearchFiles(ctx context.Context, userID, workspaceID int64, pattern, pathFilter string, maxResults int) ([]agent.SearchResult, error)
	RunCommand(ctx context.Context, userID, workspaceID int64, command string) (*agent.CommandResult, error)

	// Git operations
	GitStatus(ctx context.Context, userID, workspaceID int64) (*agent.GitStatus, error)
	GitLog(ctx context.Context, userID, workspaceID int64, count int) ([]agent.GitCommit, error)
	GitBranches(ctx context.Context, userID, workspaceID int64) (*agent.GitBranches, error)
	GitDiff(ctx context.Context, userID, workspaceID int64, path string, staged bool) (string, error)
	GitAction(ctx context.Context, userID, workspaceID int64, action string, files []string, message, branch, remote, userName, userEmail string) (*agent.GitActionResult, error)

	// AgentAddr returns the agent's host:port for a running workspace.
	AgentAddr(ctx context.Context, userID, workspaceID int64) (string, error)
}

type service struct {
	repo      Repository
	container container.Manager
}

// NewService creates a new workspace Service.
func NewService(repo Repository, cm container.Manager) Service {
	return &service{repo: repo, container: cm}
}

func (s *service) getAgent(ctx context.Context, userID, workspaceID int64) (*agent.Client, error) {
	ws, err := s.Get(ctx, userID, workspaceID)
	if err != nil {
		return nil, err
	}
	if ws.ContainerID == "" || ws.Status != StatusRunning {
		return nil, ErrNotRunning
	}
	ip, err := s.container.GetIP(ctx, ws.ContainerID)
	if err != nil {
		return nil, fmt.Errorf("getting container IP: %w", err)
	}
	return agent.NewClient(ip, agent.DefaultPort), nil
}

func (s *service) Create(ctx context.Context, userID int64, name, description string) (*Workspace, error) {
	// Insert the workspace record first to obtain a stable ID for naming
	// Docker resources. This decouples container/volume names from the
	// user-facing workspace name (which may contain spaces or be renamed).
	ws := &Workspace{
		UserID:      userID,
		Name:        name,
		Description: description,
		Status:      StatusCreating,
	}
	if err := s.repo.Create(ctx, ws); err != nil {
		return nil, fmt.Errorf("creating workspace: %w", err)
	}

	// Use the workspace ID for Docker resource names — stable and unique.
	volumeName := fmt.Sprintf("devpad-vol-%d", ws.ID)
	if err := s.container.CreateVolume(ctx, volumeName); err != nil {
		_ = s.repo.Delete(ctx, ws.ID)
		return nil, fmt.Errorf("creating volume: %w", err)
	}

	containerID, err := s.container.Create(ctx, fmt.Sprintf("%d", ws.ID), volumeName)
	if err != nil {
		_ = s.container.RemoveVolume(ctx, volumeName)
		_ = s.repo.Delete(ctx, ws.ID)
		return nil, fmt.Errorf("creating container: %w", err)
	}

	if err := s.container.Start(ctx, containerID); err != nil {
		_ = s.container.Remove(ctx, containerID)
		_ = s.container.RemoveVolume(ctx, volumeName)
		_ = s.repo.Delete(ctx, ws.ID)
		return nil, fmt.Errorf("starting container: %w", err)
	}

	ws.Status = StatusRunning
	ws.ContainerID = containerID
	ws.VolumeName = volumeName
	if err := s.repo.Update(ctx, ws); err != nil {
		_ = s.container.Stop(ctx, containerID)
		_ = s.container.Remove(ctx, containerID)
		_ = s.container.RemoveVolume(ctx, volumeName)
		_ = s.repo.Delete(ctx, ws.ID)
		return nil, fmt.Errorf("updating workspace: %w", err)
	}
	return ws, nil
}

func (s *service) Get(ctx context.Context, userID, workspaceID int64) (*Workspace, error) {
	ws, err := s.repo.GetByID(ctx, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("getting workspace: %w", err)
	}
	if ws == nil {
		return nil, ErrNotFound
	}
	if ws.UserID != userID {
		return nil, ErrForbidden
	}
	return ws, nil
}

func (s *service) List(ctx context.Context, userID int64) ([]*Workspace, error) {
	workspaces, err := s.repo.ListByUserID(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("listing workspaces: %w", err)
	}
	return workspaces, nil
}

func (s *service) Update(ctx context.Context, userID, workspaceID int64, name, description string) (*Workspace, error) {
	ws, err := s.Get(ctx, userID, workspaceID)
	if err != nil {
		return nil, err
	}

	ws.Name = name
	ws.Description = description

	if err := s.repo.Update(ctx, ws); err != nil {
		return nil, fmt.Errorf("updating workspace: %w", err)
	}
	return ws, nil
}

func (s *service) Delete(ctx context.Context, userID, workspaceID int64) error {
	ws, err := s.Get(ctx, userID, workspaceID)
	if err != nil {
		return err
	}

	// Remove the Docker container
	if ws.ContainerID != "" {
		_ = s.container.Stop(ctx, ws.ContainerID)
		if err := s.container.Remove(ctx, ws.ContainerID); err != nil {
			return fmt.Errorf("removing container: %w", err)
		}
	}

	// Remove the associated volume
	if ws.VolumeName != "" {
		if err := s.container.RemoveVolume(ctx, ws.VolumeName); err != nil {
			return fmt.Errorf("removing volume: %w", err)
		}
	}

	if err := s.repo.Delete(ctx, ws.ID); err != nil {
		return fmt.Errorf("deleting workspace: %w", err)
	}
	return nil
}

func (s *service) Start(ctx context.Context, userID, workspaceID int64) (*Workspace, error) {
	ws, err := s.Get(ctx, userID, workspaceID)
	if err != nil {
		return nil, err
	}
	if ws.Status == StatusRunning {
		return ws, nil
	}
	if ws.ContainerID == "" {
		return nil, fmt.Errorf("workspace has no container")
	}
	if err := s.container.Start(ctx, ws.ContainerID); err != nil {
		return nil, fmt.Errorf("starting container: %w", err)
	}
	ws.Status = StatusRunning
	if err := s.repo.Update(ctx, ws); err != nil {
		return nil, fmt.Errorf("updating workspace status: %w", err)
	}
	return ws, nil
}

func (s *service) Stop(ctx context.Context, userID, workspaceID int64) (*Workspace, error) {
	ws, err := s.Get(ctx, userID, workspaceID)
	if err != nil {
		return nil, err
	}
	if ws.Status == StatusStopped {
		return ws, nil
	}
	if ws.ContainerID == "" {
		return nil, fmt.Errorf("workspace has no container")
	}
	if err := s.container.Stop(ctx, ws.ContainerID); err != nil {
		return nil, fmt.Errorf("stopping container: %w", err)
	}
	ws.Status = StatusStopped
	if err := s.repo.Update(ctx, ws); err != nil {
		return nil, fmt.Errorf("updating workspace status: %w", err)
	}
	return ws, nil
}

// File operations — delegate to the in-container agent.

func (s *service) ListFiles(ctx context.Context, userID, workspaceID int64, path string) ([]agent.FileEntry, error) {
	c, err := s.getAgent(ctx, userID, workspaceID)
	if err != nil {
		return nil, err
	}
	return c.ListFiles(ctx, path)
}

func (s *service) ReadFile(ctx context.Context, userID, workspaceID int64, path string) ([]byte, error) {
	c, err := s.getAgent(ctx, userID, workspaceID)
	if err != nil {
		return nil, err
	}
	return c.ReadFile(ctx, path)
}

func (s *service) WriteFile(ctx context.Context, userID, workspaceID int64, path string, content []byte) error {
	c, err := s.getAgent(ctx, userID, workspaceID)
	if err != nil {
		return err
	}
	return c.WriteFile(ctx, path, content)
}

func (s *service) DeleteFile(ctx context.Context, userID, workspaceID int64, path string) error {
	c, err := s.getAgent(ctx, userID, workspaceID)
	if err != nil {
		return err
	}
	return c.DeleteFile(ctx, path)
}

func (s *service) CreateDirectory(ctx context.Context, userID, workspaceID int64, path string) error {
	c, err := s.getAgent(ctx, userID, workspaceID)
	if err != nil {
		return err
	}
	return c.CreateDirectory(ctx, path)
}

func (s *service) RenameFile(ctx context.Context, userID, workspaceID int64, oldPath, newPath string) error {
	c, err := s.getAgent(ctx, userID, workspaceID)
	if err != nil {
		return err
	}
	return c.RenameFile(ctx, oldPath, newPath)
}

func (s *service) SearchFiles(ctx context.Context, userID, workspaceID int64, pattern, pathFilter string, maxResults int) ([]agent.SearchResult, error) {
	c, err := s.getAgent(ctx, userID, workspaceID)
	if err != nil {
		return nil, err
	}
	return c.SearchFiles(ctx, pattern, pathFilter, maxResults)
}

func (s *service) RunCommand(ctx context.Context, userID, workspaceID int64, command string) (*agent.CommandResult, error) {
	c, err := s.getAgent(ctx, userID, workspaceID)
	if err != nil {
		return nil, err
	}
	return c.RunCommand(ctx, command)
}

func (s *service) GitStatus(ctx context.Context, userID, workspaceID int64) (*agent.GitStatus, error) {
	c, err := s.getAgent(ctx, userID, workspaceID)
	if err != nil {
		return nil, err
	}
	return c.GitStatus(ctx)
}

func (s *service) GitLog(ctx context.Context, userID, workspaceID int64, count int) ([]agent.GitCommit, error) {
	c, err := s.getAgent(ctx, userID, workspaceID)
	if err != nil {
		return nil, err
	}
	return c.GitLog(ctx, count)
}

func (s *service) GitBranches(ctx context.Context, userID, workspaceID int64) (*agent.GitBranches, error) {
	c, err := s.getAgent(ctx, userID, workspaceID)
	if err != nil {
		return nil, err
	}
	return c.GitBranches(ctx)
}

func (s *service) GitDiff(ctx context.Context, userID, workspaceID int64, path string, staged bool) (string, error) {
	c, err := s.getAgent(ctx, userID, workspaceID)
	if err != nil {
		return "", err
	}
	return c.GitDiff(ctx, path, staged)
}

func (s *service) GitAction(ctx context.Context, userID, workspaceID int64, action string, files []string, message, branch, remote, userName, userEmail string) (*agent.GitActionResult, error) {
	c, err := s.getAgent(ctx, userID, workspaceID)
	if err != nil {
		return nil, err
	}
	return c.GitAction(ctx, action, files, message, branch, remote, userName, userEmail)
}

func (s *service) AgentAddr(ctx context.Context, userID, workspaceID int64) (string, error) {
	ws, err := s.Get(ctx, userID, workspaceID)
	if err != nil {
		return "", err
	}
	if ws.ContainerID == "" || ws.Status != StatusRunning {
		return "", ErrNotRunning
	}
	ip, err := s.container.GetIP(ctx, ws.ContainerID)
	if err != nil {
		return "", fmt.Errorf("getting container IP: %w", err)
	}
	return fmt.Sprintf("%s:%d", ip, agent.DefaultPort), nil
}
