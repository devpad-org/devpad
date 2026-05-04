package workspace

import (
	"context"
	"fmt"
)

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

func (s *service) ListAll(ctx context.Context) ([]*Workspace, error) {
	workspaces, err := s.repo.ListAll(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing all workspaces: %w", err)
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

func (s *service) UpdateResourceLimits(ctx context.Context, workspaceID, memoryLimit, nanoCPUs int64) (*Workspace, error) {
	ws, err := s.repo.GetByID(ctx, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("getting workspace: %w", err)
	}
	if ws == nil {
		return nil, ErrNotFound
	}

	ws.MemoryLimit = memoryLimit
	ws.NanoCPUs = nanoCPUs

	if err := s.repo.Update(ctx, ws); err != nil {
		return nil, fmt.Errorf("updating workspace limits: %w", err)
	}

	// Apply limits to running container immediately.
	if ws.ContainerID != "" && ws.Status == StatusRunning {
		if err := s.container.UpdateResources(ctx, ws.ContainerID, memoryLimit, nanoCPUs); err != nil {
			return nil, fmt.Errorf("applying resource limits: %w", err)
		}
	}

	return ws, nil
}
