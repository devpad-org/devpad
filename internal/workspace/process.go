package workspace

import (
	"context"

	"github.com/devpad-org/devpad/internal/agent"
)

func (s *service) ListProcesses(ctx context.Context, userID, workspaceID int64) (*agent.ProcessList, error) {
	c, err := s.getAgent(ctx, userID, workspaceID)
	if err != nil {
		return nil, err
	}
	return c.ListProcesses(ctx)
}

func (s *service) KillProcess(ctx context.Context, userID, workspaceID int64, pid int) error {
	c, err := s.getAgent(ctx, userID, workspaceID)
	if err != nil {
		return err
	}
	return c.KillProcess(ctx, pid)
}
