package workspace

import (
	"context"

	"github.com/devpad-org/devpad/internal/agent"
)

func (s *service) RunCommand(ctx context.Context, userID, workspaceID int64, command string) (*agent.CommandResult, error) {
	c, err := s.getAgent(ctx, userID, workspaceID)
	if err != nil {
		return nil, err
	}
	return c.RunCommand(ctx, command)
}

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

func (s *service) ReadCommandOutput(ctx context.Context, userID, workspaceID int64, commandID string, cursor int64, maxBytes, waitMS int) (*agent.CommandOutput, error) {
	c, err := s.getAgent(ctx, userID, workspaceID)
	if err != nil {
		return nil, err
	}
	return c.ReadCommandOutput(ctx, commandID, cursor, maxBytes, waitMS)
}

func (s *service) StopCommand(ctx context.Context, userID, workspaceID int64, commandID string) (*agent.ManagedCommand, error) {
	c, err := s.getAgent(ctx, userID, workspaceID)
	if err != nil {
		return nil, err
	}
	return c.StopCommand(ctx, commandID)
}
