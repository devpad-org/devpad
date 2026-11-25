package workspace

import (
	"context"
	"time"

	"github.com/devpad-org/devpad/internal/agent"
)

func (s *service) StartCommand(ctx context.Context, userID, workspaceID int64, command, cwd string) (*agent.ManagedCommand, error) {
	c, err := s.getAgent(ctx, userID, workspaceID)
	if err != nil {
		return nil, err
	}
	return c.StartCommand(ctx, command, cwd)
}

func (s *service) CommandStatus(ctx context.Context, userID, workspaceID int64, commandID string) (*agent.ManagedCommand, error) {
	c, err := s.getAgent(ctx, userID, workspaceID)
	if err != nil {
		return nil, err
	}
	return c.CommandStatus(ctx, commandID)
}

func (s *service) WaitCommand(ctx context.Context, userID, workspaceID int64, commandID string, wait time.Duration) (*agent.ManagedCommand, error) {
	c, err := s.getAgent(ctx, userID, workspaceID)
	if err != nil {
		return nil, err
	}
	return c.WaitCommand(ctx, commandID, wait)
}

func (s *service) ReadCommandOutput(ctx context.Context, userID, workspaceID int64, commandID string, opts agent.CommandOutputOptions) (*agent.CommandOutput, error) {
	c, err := s.getAgent(ctx, userID, workspaceID)
	if err != nil {
		return nil, err
	}
	return c.ReadCommandOutput(ctx, commandID, opts)
}

func (s *service) StopCommand(ctx context.Context, userID, workspaceID int64, commandID string) (*agent.ManagedCommand, error) {
	c, err := s.getAgent(ctx, userID, workspaceID)
	if err != nil {
		return nil, err
	}
	return c.StopCommand(ctx, commandID)
}
