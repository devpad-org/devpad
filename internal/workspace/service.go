package workspace

import (
	"context"
	"errors"
	"fmt"

	"github.com/devpad-org/devpad/internal/container"
)

var (
	ErrNotFound  = errors.New("workspace not found")
	ErrForbidden = errors.New("access denied")
)

// Service defines workspace business logic.
type Service interface {
	Create(ctx context.Context, userID int64, name, description string) (*Workspace, error)
	Get(ctx context.Context, userID, workspaceID int64) (*Workspace, error)
	List(ctx context.Context, userID int64) ([]*Workspace, error)
	Update(ctx context.Context, userID, workspaceID int64, name, description string) (*Workspace, error)
	Delete(ctx context.Context, userID, workspaceID int64) error
	ContainerManager() container.Manager
}

type service struct {
	repo      Repository
	container container.Manager
}

// NewService creates a new workspace Service.
func NewService(repo Repository, cm container.Manager) Service {
	return &service{repo: repo, container: cm}
}

func (s *service) ContainerManager() container.Manager {
	return s.container
}

func (s *service) Create(ctx context.Context, userID int64, name, description string) (*Workspace, error) {
	// Create the Docker container
	containerID, err := s.container.Create(ctx, fmt.Sprintf("%d-%s", userID, name))
	if err != nil {
		return nil, fmt.Errorf("creating container: %w", err)
	}

	// Start the container
	if err := s.container.Start(ctx, containerID); err != nil {
		// Clean up the created container on failure
		_ = s.container.Remove(ctx, containerID)
		return nil, fmt.Errorf("starting container: %w", err)
	}

	ws := &Workspace{
		UserID:      userID,
		Name:        name,
		Description: description,
		Status:      StatusRunning,
		ContainerID: containerID,
	}
	if err := s.repo.Create(ctx, ws); err != nil {
		// Clean up container on DB failure
		_ = s.container.Stop(ctx, containerID)
		_ = s.container.Remove(ctx, containerID)
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

	if err := s.repo.Delete(ctx, ws.ID); err != nil {
		return fmt.Errorf("deleting workspace: %w", err)
	}
	return nil
}
