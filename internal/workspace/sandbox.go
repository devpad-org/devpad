package workspace

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/devpad-org/devpad/internal/agent"
)

// ErrNotSandbox is returned for sandbox-only operations on other workspaces.
var ErrNotSandbox = errors.New("workspace is not a project sandbox")

func (s *service) ListByProject(ctx context.Context, userID, projectID int64) ([]*Workspace, error) {
	all, err := s.repo.ListByProjectID(ctx, projectID)
	if err != nil {
		return nil, fmt.Errorf("listing project sandboxes: %w", err)
	}
	owned := make([]*Workspace, 0, len(all))
	for _, ws := range all {
		if ws.UserID == userID {
			owned = append(owned, ws)
		}
	}
	return owned, nil
}

// sandboxAgent returns a running sandbox and its agent client.
func (s *service) sandboxAgent(ctx context.Context, userID, workspaceID int64) (*Workspace, *agent.Client, error) {
	ws, err := s.Get(ctx, userID, workspaceID)
	if err != nil {
		return nil, nil, err
	}
	if !ws.IsSandbox() || ws.ProjectID == 0 {
		return nil, nil, ErrNotSandbox
	}
	c, err := s.getAgent(ctx, userID, workspaceID)
	if err != nil {
		return nil, nil, err
	}
	return ws, c, nil
}

// SeedSandboxRepo clones a bundle of the project's base branch into the
// sandbox and checks out the branch the sandbox owns, committing as the user.
func (s *service) SeedSandboxRepo(ctx context.Context, userID, workspaceID int64, base string, bundle io.Reader) (*agent.RepoBranch, error) {
	ws, c, err := s.sandboxAgent(ctx, userID, workspaceID)
	if err != nil {
		return nil, err
	}
	var name, email string
	if user, err := s.users.GetByID(ctx, ws.UserID); err == nil && user != nil {
		name, email = user.Username, user.Email
	}
	return c.RepoSeed(ctx, base, ws.Branch, name, email, bundle)
}

// BundleSandboxBranch streams a bundle of the branch the sandbox owns. The
// branch comes from devpad's record, never from the sandbox, and the bundle
// is untrusted. The caller must close it.
func (s *service) BundleSandboxBranch(ctx context.Context, userID, workspaceID int64) (string, io.ReadCloser, error) {
	ws, c, err := s.sandboxAgent(ctx, userID, workspaceID)
	if err != nil {
		return "", nil, err
	}
	bundle, err := c.RepoBundle(ctx, ws.Branch)
	if err != nil {
		return "", nil, err
	}
	return ws.Branch, bundle, nil
}
