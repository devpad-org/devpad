package workspace

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/devpad-org/devpad/internal/agent"
)

var (
	// ErrNotSandbox is returned for sandbox-only operations on other workspaces.
	ErrNotSandbox = errors.New("workspace is not a project sandbox")
	// ErrNotProjectWorkspace is returned for project workspace operations on
	// other workspaces.
	ErrNotProjectWorkspace = errors.New("workspace is not a project workspace")
)

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

// projectAgent returns a running workspace that belongs to a project, either
// a sandbox or the project workspace, and its agent client.
func (s *service) projectAgent(ctx context.Context, userID, workspaceID int64) (*Workspace, *agent.Client, error) {
	ws, err := s.Get(ctx, userID, workspaceID)
	if err != nil {
		return nil, nil, err
	}
	if ws.ProjectID == 0 {
		return nil, nil, ErrNotSandbox
	}
	c, err := s.getAgent(ctx, userID, workspaceID)
	if err != nil {
		return nil, nil, err
	}
	return ws, c, nil
}

// sandboxAgent returns a running sandbox and its agent client.
func (s *service) sandboxAgent(ctx context.Context, userID, workspaceID int64) (*Workspace, *agent.Client, error) {
	ws, c, err := s.projectAgent(ctx, userID, workspaceID)
	if err != nil {
		return nil, nil, err
	}
	if !ws.IsSandbox() {
		return nil, nil, ErrNotSandbox
	}
	return ws, c, nil
}

// projectWorkspaceAgent returns a running project workspace and its agent
// client.
func (s *service) projectWorkspaceAgent(ctx context.Context, userID, workspaceID int64) (*Workspace, *agent.Client, error) {
	ws, c, err := s.projectAgent(ctx, userID, workspaceID)
	if err != nil {
		if errors.Is(err, ErrNotSandbox) {
			return nil, nil, ErrNotProjectWorkspace
		}
		return nil, nil, err
	}
	if !ws.IsProjectWorkspace() {
		return nil, nil, ErrNotProjectWorkspace
	}
	return ws, c, nil
}

// SeedSandboxRepo clones a bundle of the project's base branch into a sandbox
// or project workspace, committing as the user, and records that the repo is
// seeded. A sandbox checks out the branch it owns; a project workspace, which
// owns none, stays on base.
func (s *service) SeedSandboxRepo(ctx context.Context, userID, workspaceID int64, base string, bundle io.Reader) (*agent.RepoBranch, error) {
	ws, c, err := s.projectAgent(ctx, userID, workspaceID)
	if err != nil {
		return nil, err
	}
	branch := ws.Branch
	if branch == "" {
		branch = base
	}
	var name, email string
	if user, err := s.users.GetByID(ctx, ws.UserID); err == nil && user != nil {
		name, email = user.Username, user.Email
	}
	seeded, err := c.RepoSeed(ctx, base, branch, name, email, bundle)
	if err != nil {
		return nil, err
	}
	if err := s.repo.MarkRepoSeeded(ctx, ws.ID); err != nil {
		return nil, err
	}
	ws.RepoSeeded = true
	return seeded, nil
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

// FetchSandboxBase refreshes devpad/<branch> in a sandbox or project
// workspace from a bundle of a canonical branch, so work that landed after
// the workspace was seeded can be merged in.
func (s *service) FetchSandboxBase(ctx context.Context, userID, workspaceID int64, branch string, bundle io.Reader) (*agent.RepoBranch, error) {
	_, c, err := s.projectAgent(ctx, userID, workspaceID)
	if err != nil {
		return nil, err
	}
	return c.RepoFetchBase(ctx, branch, bundle)
}

// FetchProjectBranches refreshes devpad/<branch> in a project workspace for
// each branch, from one bundle of them.
func (s *service) FetchProjectBranches(ctx context.Context, userID, workspaceID int64, branches []string, bundle io.Reader) ([]agent.RepoBranch, error) {
	_, c, err := s.projectWorkspaceAgent(ctx, userID, workspaceID)
	if err != nil {
		return nil, err
	}
	return c.RepoFetchBases(ctx, branches, bundle)
}

// FetchProjectUpstreamBranches refreshes origin/<branch> in a project
// workspace for each branch, from one bundle of the upstream's copies.
func (s *service) FetchProjectUpstreamBranches(ctx context.Context, userID, workspaceID int64, branches []string, bundle io.Reader) ([]agent.RepoBranch, error) {
	_, c, err := s.projectWorkspaceAgent(ctx, userID, workspaceID)
	if err != nil {
		return nil, err
	}
	return c.RepoFetchUpstreamBases(ctx, branches, bundle)
}

// CheckoutProjectBranch brings a canonical branch into a project workspace
// from a bundle and switches to it, creating the local branch if it is new.
// An existing local branch keeps its commits; only devpad/<branch> moves.
func (s *service) CheckoutProjectBranch(ctx context.Context, userID, workspaceID int64, branch string, bundle io.Reader) (*agent.RepoCheckout, error) {
	_, c, err := s.projectWorkspaceAgent(ctx, userID, workspaceID)
	if err != nil {
		return nil, err
	}
	return c.RepoCheckout(ctx, branch, bundle)
}

// ProjectBranchStatus compares a local branch of a project workspace with
// its devpad/<branch> copy. The answer comes from the workspace, so it is
// only fit for showing the user.
func (s *service) ProjectBranchStatus(ctx context.Context, userID, workspaceID int64, branch string) (*agent.RepoBranchStatus, error) {
	_, c, err := s.projectWorkspaceAgent(ctx, userID, workspaceID)
	if err != nil {
		return nil, err
	}
	return c.RepoBranchStatus(ctx, branch)
}

// BundleProjectBranch streams a bundle of a local branch of a project
// workspace. The caller picks the branch and must treat the bundle as
// untrusted, and must close it.
func (s *service) BundleProjectBranch(ctx context.Context, userID, workspaceID int64, branch string) (io.ReadCloser, error) {
	_, c, err := s.projectWorkspaceAgent(ctx, userID, workspaceID)
	if err != nil {
		return nil, err
	}
	return c.RepoBundle(ctx, branch)
}
