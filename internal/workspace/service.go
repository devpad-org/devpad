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
	// Create a named volume for persistent workspace data
	volumeName := fmt.Sprintf("devpad-vol-%d-%s", userID, name)
	if err := s.container.CreateVolume(ctx, volumeName); err != nil {
		return nil, fmt.Errorf("creating volume: %w", err)
	}

	// Create the Docker container with the volume mounted
	containerID, err := s.container.Create(ctx, fmt.Sprintf("%d-%s", userID, name), volumeName)
	if err != nil {
		_ = s.container.RemoveVolume(ctx, volumeName)
		return nil, fmt.Errorf("creating container: %w", err)
	}

	// Start the container
	if err := s.container.Start(ctx, containerID); err != nil {
		_ = s.container.Remove(ctx, containerID)
		_ = s.container.RemoveVolume(ctx, volumeName)
		return nil, fmt.Errorf("starting container: %w", err)
	}

	ws := &Workspace{
		UserID:      userID,
		Name:        name,
		Description: description,
		Status:      StatusRunning,
		ContainerID: containerID,
		VolumeName:  volumeName,
	}
	if err := s.repo.Create(ctx, ws); err != nil {
		_ = s.container.Stop(ctx, containerID)
		_ = s.container.Remove(ctx, containerID)
		_ = s.container.RemoveVolume(ctx, volumeName)
		return nil, fmt.Errorf("creating workspace: %w", err)
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
