package workspace

import (
	"context"
	"time"

	"github.com/devpad-org/devpad/internal/agent"
)

// ConfigureGitBridge sets up the devpad remote in a project workspace.
func (s *service) ConfigureGitBridge(ctx context.Context, userID, workspaceID int64) error {
	_, c, err := s.projectWorkspaceAgent(ctx, userID, workspaceID)
	if err != nil {
		return err
	}
	return c.GitBridgeConfigureRemote(ctx)
}

// GitBridgeRequests long-polls a project workspace for git requests. The
// requests come from the workspace and are untrusted.
func (s *service) GitBridgeRequests(ctx context.Context, userID, workspaceID int64, wait time.Duration) ([]agent.GitBridgeRequest, error) {
	_, c, err := s.projectWorkspaceAgent(ctx, userID, workspaceID)
	if err != nil {
		return nil, err
	}
	return c.GitBridgeRequests(ctx, wait)
}

// GitBridgeRespond answers a git request in a project workspace.
func (s *service) GitBridgeRespond(ctx context.Context, userID, workspaceID int64, id string, result agent.GitBridgeResult) error {
	_, c, err := s.projectWorkspaceAgent(ctx, userID, workspaceID)
	if err != nil {
		return err
	}
	return c.GitBridgeRespond(ctx, id, result)
}
