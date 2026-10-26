package project

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/devpad-org/devpad/internal/agent"
	"github.com/devpad-org/devpad/internal/workspace"
)

// The project workspace is the user's own IDE for a project. It is as
// untrusted as a sandbox, since the IDE assistant runs in it: it never
// reaches the canonical repo, and devpad moves every commit with bundles.
// Unlike a sandbox it owns no branch. It can check out any canonical branch,
// and publish any local branch except protected branches and branches owned
// by sandboxes. Publishing only fast-forwards, so it can add commits to an
// agent's or merge request's branch but never rewrite them; protected
// branches still move only through a merge.

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

// workspaceOpenTimeout bounds creating and seeding a project workspace,
// which runs on after the request that asked for it goes away.
const workspaceOpenTimeout = 15 * time.Minute

// OpenWorkspace returns the project's workspace, creating it on first use
// with the default branch checked out and the setup script started. created
// reports whether it is new. The workspace does not count against the
// sandbox limit.
//
// Creating and seeding outlive the request, so a client that goes away
// cannot leave a half-made workspace behind. One left unseeded anyway, by a
// crash or a failed cleanup, holds nothing of the user's yet, so it is
// replaced.
func (s *service) OpenWorkspace(ctx context.Context, userID, projectID int64) (ws *workspace.Workspace, created bool, err error) {
	detached := context.WithoutCancel(ctx)
	// A new workspace should not start on an image about to be replaced.
	// The wait is bounded, and comes before the time making the workspace
	// gets; after it, the workspace gets the last image that built, and can
	// be rebuilt once the build ends.
	waitCtx, cancelWait := context.WithTimeout(detached, environmentOpenWait)
	if p, err := s.getReady(waitCtx, userID, projectID); err == nil {
		if existing, err := s.projectWorkspace(waitCtx, p); errors.Is(err, ErrNoWorkspace) || (err == nil && !existing.RepoSeeded) {
			if err := s.awaitBuild(waitCtx, p.ID); err != nil {
				log.Printf("project %d: making its workspace without waiting longer for its environment build: %v", p.ID, err)
			}
		}
	}
	cancelWait()

	ctx, cancel := context.WithTimeout(detached, workspaceOpenTimeout)
	defer cancel()
	p, err := s.getReady(ctx, userID, projectID)
	if err != nil {
		return nil, false, err
	}
	unlock := s.lockWorkspace(p.ID)
	defer unlock()
	if s.isDeleting(p.ID) {
		return nil, false, ErrDeleting
	}

	existing, err := s.projectWorkspace(ctx, p)
	switch {
	case err == nil && existing.RepoSeeded:
		return existing, false, nil
	case err == nil:
		log.Printf("project %d: replacing unseeded workspace %d", p.ID, existing.ID)
		if err := s.workspaces.Delete(ctx, userID, existing.ID); err != nil {
			return nil, false, fmt.Errorf("removing unseeded workspace: %w", err)
		}
	case !errors.Is(err, ErrNoWorkspace):
		return nil, false, err
	}

	c, err := s.repoAgent(ctx, p)
	if err != nil {
		return nil, false, err
	}
	ws, err = s.workspaces.CreateProjectWorkspace(ctx, userID, workspace.ProjectWorkspaceSpec{
		ProjectID:   p.ID,
		Name:        p.Name,
		Description: p.Description,
		Image:       imageFor(p),
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
	ws.RepoSeeded = true
	if _, err := s.startSetup(ctx, p, ws); err != nil {
		log.Printf("project %d: starting setup script in workspace %d: %v", p.ID, ws.ID, err)
	}
	return ws, true, nil
}

func (s *service) isDeleting(projectID int64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.deleting[projectID]
}

// lockWorkspace serializes opening a project's workspace, so concurrent
// opens create it once, and Delete waits for an open in flight.
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

// SyncWorkspaceBase refreshes devpad/<branch> in the project workspace, the
// default branch unless given, so the user can merge in work that has
// landed since.
func (s *service) SyncWorkspaceBase(ctx context.Context, userID, projectID int64, branch string) (*agent.RepoBranch, error) {
	p, err := s.getReady(ctx, userID, projectID)
	if err != nil {
		return nil, err
	}
	if branch == "" {
		branch = p.DefaultBranch
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
	return s.workspaces.FetchSandboxBase(ctx, userID, ws.ID, branch, bundle)
}

// maxFetchBranches bounds one fetch through the devpad remote.
const maxFetchBranches = 256

// FetchWorkspaceBranches brings canonical branches into the project
// workspace as devpad/<branch>, for git fetch through the devpad remote. The
// names come from the workspace, so each is checked and must exist in the
// canonical repo; only those branches travel.
func (s *service) FetchWorkspaceBranches(ctx context.Context, userID, projectID int64, branches []string) error {
	p, err := s.getReady(ctx, userID, projectID)
	if err != nil {
		return err
	}
	if len(branches) == 0 || len(branches) > maxFetchBranches {
		return invalid("fetch between 1 and %d branches", maxFetchBranches)
	}
	unique := make([]string, 0, len(branches))
	seen := make(map[string]bool, len(branches))
	for _, branch := range branches {
		if !validBranchName(branch) {
			return invalid("branch is not a valid branch name")
		}
		if !seen[branch] {
			seen[branch] = true
			unique = append(unique, branch)
		}
	}
	ws, err := s.runningWorkspace(ctx, p)
	if err != nil {
		return err
	}
	c, err := s.repoAgent(ctx, p)
	if err != nil {
		return err
	}
	refs, err := c.RepoRefs(ctx)
	if err != nil {
		return err
	}
	exists := make(map[string]bool, len(refs.Branches))
	for _, ref := range refs.Branches {
		exists[ref.Name] = true
	}
	for _, branch := range unique {
		if !exists[branch] {
			return invalid("branch %q does not exist", branch)
		}
	}
	// One bundle carries every branch, so a wide fetch costs the repo
	// container one bundle rather than one per branch.
	bundle, err := c.RepoBundleBranches(ctx, unique)
	if err != nil {
		return err
	}
	defer bundle.Close()
	if _, err := s.workspaces.FetchProjectBranches(ctx, userID, ws.ID, unique, bundle); err != nil {
		return err
	}
	return nil
}

// PublishWorkspaceBranch copies a local branch of the project workspace into
// the canonical repo under the same name. The name comes from the request and
// is checked here, never taken from the workspace. Protected branches and
// branches owned by sandboxes are refused, and the import only
// fast-forwards an existing branch. An open merge request for the branch
// then follows the new commit.
func (s *service) PublishWorkspaceBranch(ctx context.Context, userID, projectID int64, branch string) (*agent.RepoBranch, error) {
	p, err := s.getReady(ctx, userID, projectID)
	if err != nil {
		return nil, err
	}
	if !validBranchName(branch) {
		return nil, invalid("branch is not a valid branch name")
	}
	if p.IsProtected(branch) {
		return nil, ErrPublishProtected
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
	published, err := c.RepoImportFastForward(ctx, branch, bundle)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	hook := s.publishHook
	s.mu.Unlock()
	if hook != nil {
		hook.BranchPublished(ctx, userID, p.ID, branch, published.SHA)
	}
	return published, nil
}

// WorkspaceBranchStatus compares a branch of the project workspace with the
// project's. The workspace's devpad/<branch> copy is refreshed first when it
// is stale, so the counts reflect the project as it is now. The counts come
// from the workspace and are only fit for showing the user.
func (s *service) WorkspaceBranchStatus(ctx context.Context, userID, projectID int64, branch string) (*WorkspaceBranchStatus, error) {
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
	refs, err := c.RepoRefs(ctx)
	if err != nil {
		return nil, err
	}
	canonical := ""
	for _, ref := range refs.Branches {
		if ref.Name == branch {
			canonical = ref.SHA
		}
	}

	local, err := s.workspaces.ProjectBranchStatus(ctx, userID, ws.ID, branch)
	if err != nil {
		return nil, err
	}
	if canonical != "" && local.UpstreamSHA != canonical {
		bundle, err := c.RepoBundle(ctx, branch)
		if err != nil {
			return nil, err
		}
		_, err = s.workspaces.FetchSandboxBase(ctx, userID, ws.ID, branch, bundle)
		bundle.Close()
		if err != nil {
			return nil, err
		}
		if local, err = s.workspaces.ProjectBranchStatus(ctx, userID, ws.ID, branch); err != nil {
			return nil, err
		}
	}

	status := &WorkspaceBranchStatus{
		Branch:       branch,
		Published:    canonical != "",
		SHA:          local.SHA,
		CanonicalSHA: canonical,
		Ahead:        local.Ahead,
		Behind:       local.Behind,
	}
	sandboxes, err := s.workspaces.ListByProject(ctx, userID, p.ID)
	if err != nil {
		return nil, err
	}
	for _, sb := range sandboxes {
		if sb.IsSandbox() && sb.Branch == branch {
			status.SandboxOwned = true
		}
	}
	return status, nil
}

// reservePublish refuses a protected branch, or one that a sandbox owns or
// is being created for, and holds it until releasePublish so no sandbox can claim it while the
// import is in flight. A sandbox force-publishes its branch, so it would
// otherwise discard what the user published.
func (s *service) reservePublish(ctx context.Context, p *Project, branch string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.deleting[p.ID] {
		return ErrDeleting
	}
	if err := s.checkUnprotected(ctx, p.ID, branch, ErrPublishProtected); err != nil {
		return err
	}
	if s.pending[p.ID][branch] {
		return ErrBranchInUse
	}
	if s.publishing[p.ID][branch] {
		return ErrBranchPublishing
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
