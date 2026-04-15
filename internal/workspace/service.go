package workspace

import (
	"context"
	"errors"
	"fmt"
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
}

type service struct {
	repo Repository
}

// NewService creates a new workspace Service.
func NewService(repo Repository) Service {
	return &service{repo: repo}
}

func (s *service) Create(ctx context.Context, userID int64, name, description string) (*Workspace, error) {
	ws := &Workspace{
		UserID:      userID,
		Name:        name,
		Description: description,
		Status:      StatusStopped,
	}
	if err := s.repo.Create(ctx, ws); err != nil {
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

	if err := s.repo.Delete(ctx, ws.ID); err != nil {
		return fmt.Errorf("deleting workspace: %w", err)
	}
	return nil
}
