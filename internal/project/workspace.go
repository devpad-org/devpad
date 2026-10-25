package project

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"

	"github.com/devpad-org/devpad/internal/agent"
	"github.com/devpad-org/devpad/internal/workspace"
)

// The project workspace is the user's own IDE for a project. It is as
// untrusted as a sandbox, since the IDE assistant runs in it: it never
// reaches the canonical repo, and devpad moves every commit with bundles.
// Unlike a sandbox it owns no branch. It can check out any canonical branch,
// and publish any local branch except the default branch and branches owned
// by sandboxes. Publishing only fast-forwards, so it can add commits to an
// agent's or merge request's branch but never rewrite them; the default
// branch still moves only through a merge.

// Workspace returns the project's workspace, or ErrNoWorkspace before it has
// been opened.
func (s *service) Workspace(ctx context.Context, userID, projectID int64) (*workspace.Workspace, error) {
	p, err := s.Get(ctx, userID, projectID)
	if err != nil {
		return nil, err
	}
	return s.projectWorkspace(ctx, p)
}

func (s *service) projectWorkspace(ctx context.Context, p *Project) (*workspace.Workspace, error) {
	all, err := s.workspaces.ListByProject(ctx, p.UserID, p.ID)
	if err != nil {
		return nil, err
	}
	for _, ws := range all {
		if ws.IsProjectWorkspace() {
			return ws, nil
		}
	}
	return nil, ErrNoWorkspace
}

// runningWorkspace returns the project's workspace if it is running.
func (s *service) runningWorkspace(ctx context.Context, p *Project) (*workspace.Workspace, error) {
	ws, err := s.projectWorkspace(ctx, p)
	if err != nil {
		return nil, err
	}
	if ws.Status != workspace.StatusRunning {
		return nil, workspace.ErrNotRunning
	}
	return ws, nil
}

// OpenWorkspace returns the project's workspace, creating it on first use
// with the default branch checked out and the setup script started. created
// reports whether it is new. The workspace does not count against the
// sandbox limit.
func (s *service) OpenWorkspace(ctx context.Context, userID, projectID int64) (ws *workspace.Workspace, created bool, err error) {
	p, err := s.getReady(ctx, userID, projectID)
	if err != nil {
		return nil, false, err
	}
	unlock := s.lockWorkspace(p.ID)
	defer unlock()

	if ws, err := s.projectWorkspace(ctx, p); !errors.Is(err, ErrNoWorkspace) {
		return ws, false, err
	}

	c, err := s.repoAgent(ctx, p)
	if err != nil {
		return nil, false, err
	}
	ws, err = s.workspaces.CreateProjectWorkspace(ctx, userID, workspace.ProjectWorkspaceSpec{
		ProjectID:   p.ID,
		Name:        p.Name,
		Description: p.Description,
	})
	if err != nil {
		return nil, false, err
	}
	if err := s.seedSandbox(ctx, c, userID, ws.ID, p.DefaultBranch); err != nil {
		if delErr := s.workspaces.Delete(ctx, userID, ws.ID); delErr != nil {
			log.Printf("project %d: removing unseeded workspace %d: %v", p.ID, ws.ID, delErr)
		}
		return nil, false, fmt.Errorf("seeding workspace: %w", err)
	}
	if strings.TrimSpace(p.SetupScript) != "" {
		if _, err := s.workspaces.StartCommand(ctx, userID, ws.ID, p.SetupScript, ""); err != nil {
			log.Printf("project %d: starting setup script in workspace %d: %v", p.ID, ws.ID, err)
		}
	}
	return ws, true, nil
}

// lockWorkspace serializes opening a project's workspace, so concurrent
// opens create it once.
func (s *service) lockWorkspace(projectID int64) func() {
	s.mu.Lock()
	l, ok := s.workspaceLocks[projectID]
	if !ok {
		l = &sync.Mutex{}
		s.workspaceLocks[projectID] = l
	}
	s.mu.Unlock()
	l.Lock()
	return l.Unlock
}

// CheckoutWorkspaceBranch brings a canonical branch into the project
// workspace as devpad/<branch> and switches to it, creating the local branch
// if the workspace does not have one. An existing local branch keeps its
// commits.
func (s *service) CheckoutWorkspaceBranch(ctx context.Context, userID, projectID int64, branch string) (*agent.RepoCheckout, error) {
	p, err := s.getReady(ctx, userID, projectID)
	if err != nil {
		return nil, err
	}
	if !validBranchName(branch) {
		return nil, invalid("branch is not a valid branch name")
	}
	ws, err := s.runningWorkspace(ctx, p)
	if err != nil {
		return nil, err
	}
	c, err := s.repoAgent(ctx, p)
	if err != nil {
		return nil, err
	}
	bundle, err := c.RepoBundle(ctx, branch)
	if err != nil {
		return nil, err
	}
	defer bundle.Close()
	return s.workspaces.CheckoutProjectBranch(ctx, userID, ws.ID, branch, bundle)
}

// SyncWorkspaceBase refreshes devpad/<default branch> in the project
// workspace, so the user can merge in work that has landed since.
func (s *service) SyncWorkspaceBase(ctx context.Context, userID, projectID int64) (*agent.RepoBranch, error) {
	p, err := s.getReady(ctx, userID, projectID)
	if err != nil {
		return nil, err
	}
	ws, err := s.runningWorkspace(ctx, p)
	if err != nil {
		return nil, err
	}
	c, err := s.repoAgent(ctx, p)
	if err != nil {
		return nil, err
	}
	bundle, err := c.RepoBundle(ctx, p.DefaultBranch)
	if err != nil {
		return nil, err
	}
	defer bundle.Close()
	return s.workspaces.FetchSandboxBase(ctx, userID, ws.ID, p.DefaultBranch, bundle)
}

// PublishWorkspaceBranch copies a local branch of the project workspace into
// the canonical repo under the same name. The name comes from the request and
// is checked here, never taken from the workspace. The default branch and
// branches owned by sandboxes are refused, and the import only
// fast-forwards an existing branch.
func (s *service) PublishWorkspaceBranch(ctx context.Context, userID, projectID int64, branch string) (*agent.RepoBranch, error) {
	p, err := s.getReady(ctx, userID, projectID)
	if err != nil {
		return nil, err
	}
	if !validBranchName(branch) {
		return nil, invalid("branch is not a valid branch name")
	}
	if branch == p.DefaultBranch {
		return nil, ErrPublishDefault
	}
	ws, err := s.runningWorkspace(ctx, p)
	if err != nil {
		return nil, err
	}
	if err := s.reservePublish(ctx, p, branch); err != nil {
		return nil, err
	}
	defer s.releasePublish(p.ID, branch)

	c, err := s.repoAgent(ctx, p)
	if err != nil {
		return nil, err
	}
	bundle, err := s.workspaces.BundleProjectBranch(ctx, userID, ws.ID, branch)
	if err != nil {
		return nil, err
	}
	defer bundle.Close()
	return c.RepoImportFastForward(ctx, branch, bundle)
}

// reservePublish refuses a branch that a sandbox owns or is being created
// for, and holds it until releasePublish so no sandbox can claim it while the
// import is in flight. A sandbox force-publishes its branch, so it would
// otherwise discard what the user published.
func (s *service) reservePublish(ctx context.Context, p *Project, branch string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.pending[p.ID][branch] || s.publishing[p.ID][branch] {
		return ErrBranchInUse
	}
	existing, err := s.workspaces.ListByProject(ctx, p.UserID, p.ID)
	if err != nil {
		return err
	}
	for _, ws := range existing {
		if ws.IsSandbox() && ws.Branch == branch {
			return ErrBranchInUse
		}
	}
	if s.publishing[p.ID] == nil {
		s.publishing[p.ID] = make(map[string]bool)
	}
	s.publishing[p.ID][branch] = true
	return nil
}

func (s *service) releasePublish(projectID int64, branch string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.publishing[projectID], branch)
	if len(s.publishing[projectID]) == 0 {
		delete(s.publishing, projectID)
	}
}
