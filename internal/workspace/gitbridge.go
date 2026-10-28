package workspace

import (
	"context"
	"time"

	"github.com/devpad-org/devpad/internal/agent"
)

// GitBridgeAgent is what devpad's git bridge calls on a project workspace's
// agent. Everything it returns comes from the workspace and is untrusted.
type GitBridgeAgent interface {
	GitBridgeRequests(ctx context.Context, session string, wait time.Duration) ([]agent.GitBridgeRequest, error)
	GitBridgeRespond(ctx context.Context, session, id string, result agent.GitBridgeResult) error
	GitBridgeConfigureRemote(ctx context.Context, remotes agent.GitBridgeRemotes) error
}

// GitBridgeAgent returns devpad's current record of a running project
// workspace and its agent, for the git bridge to poll. Sandboxes and
// standalone workspaces are refused.
func (s *service) GitBridgeAgent(ctx context.Context, userID, workspaceID int64) (*Workspace, GitBridgeAgent, error) {
	ws, c, err := s.projectWorkspaceAgent(ctx, userID, workspaceID)
	if err != nil {
		return nil, nil, err
	}
	return ws, c, nil
}
