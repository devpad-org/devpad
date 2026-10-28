package project

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/devpad-org/devpad/internal/agent"
	"github.com/devpad-org/devpad/internal/workspace"
)

// Syncing from the upstream fetches its branches into the canonical repo's
// own namespace for them, never into the project's branches, then
// fast-forwards each protected branch the project has not moved past. No
// other branch changes, none is made or deleted, and nothing is forced: a
// protected branch that diverged is only reported, for the user to merge
// the upstream's work in through a merge request.
//
// The project workspace sees the upstream as its read-only origin remote.
// git fetch origin syncs, and git fetch devpad syncs when the last sync is
// stale. The workspace only ever asks for a sync: it chooses neither the
// URL nor the refs, and syncs are shared, so asking often costs one fetch.
// There is no background sync, so devpad never uses the user's key
// unattended.

const (
	// upstreamSyncReuse is how recent a sync must be for a forced one to
	// reuse it instead of fetching again.
	upstreamSyncReuse = 10 * time.Second
	// upstreamStaleAfter is how old a sync may be before git fetch devpad
	// starts another.
	upstreamStaleAfter = time.Minute
	// upstreamSyncTimeout bounds one sync, which outlives the request that
	// started it. The repo agent gives up on the fetch itself sooner, within
	// the agent client's own timeout, so a slow fetch fails with its reason.
	upstreamSyncTimeout = 6 * time.Minute
	// maxUpstreamRefs bounds the upstream branches read for the workspace;
	// the git bridge offers it fewer.
	maxUpstreamRefs = 5000

	errUpstreamFetch = "could not fetch from the upstream; the project panel says why"
)

// errUpstreamChanged fails a sync whose fetch was from an upstream the
// project no longer has.
var errUpstreamChanged = errors.New("the project's upstream changed during the sync; sync again")

// UpstreamStatus is how the project's protected branches stand against the
// upstream as last fetched, with the last sync's error, if it failed, and
// whether one is running.
type UpstreamStatus struct {
	FetchedAt *time.Time             `json:"fetchedAt,omitempty"`
	Syncing   bool                   `json:"syncing"`
	Error     string                 `json:"error,omitempty"`
	ErrorAt   *time.Time             `json:"errorAt,omitempty"`
	Branches  []agent.UpstreamBranch `json:"branches"`
}

// upstreamSync is one sync, shared by everyone who asks while it runs or
// soon after.
type upstreamSync struct {
	done   chan struct{}
	at     time.Time
	status *agent.UpstreamStatus
	err    error
}

func (u *upstreamSync) finished() bool {
	select {
	case <-u.done:
		return true
	default:
		return false
	}
}

// SyncUpstream fetches the upstream and fast-forwards the protected branches
// it can, reusing a sync that is running or just finished.
func (s *service) SyncUpstream(ctx context.Context, userID, projectID int64) (*UpstreamStatus, error) {
	p, err := s.upstreamProject(ctx, userID, projectID)
	if err != nil {
		return nil, err
	}
	run := s.startUpstreamSync(p, upstreamSyncReuse, false)
	select {
	case <-run.done:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	if run.err != nil {
		return nil, run.err
	}
	status := &UpstreamStatus{FetchedAt: run.status.FetchedAt, Branches: run.status.Branches}
	if status.Branches == nil {
		status.Branches = []agent.UpstreamBranch{}
	}
	return status, nil
}

// SyncUpstreamIfStale starts a sync if the last one is older than
// upstreamStaleAfter and waits up to wait for it, for git fetch devpad. It
// returns a warning for git to show when the sync failed or is still
// running; a project without an upstream gets none.
func (s *service) SyncUpstreamIfStale(ctx context.Context, userID, projectID int64, wait time.Duration) string {
	p, err := s.getReady(ctx, userID, projectID)
	if err != nil || p.UpstreamURL == "" {
		return ""
	}
	run := s.startUpstreamSync(p, upstreamStaleAfter, true)
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-run.done:
	case <-timer.C:
		return "still fetching from the upstream; its newest commits may be missing"
	case <-ctx.Done():
		return ""
	}
	if run.err != nil {
		return errUpstreamFetch
	}
	return ""
}

// UpstreamStatus compares the protected branches with the upstream as last
// fetched, without fetching.
func (s *service) UpstreamStatus(ctx context.Context, userID, projectID int64) (*UpstreamStatus, error) {
	p, err := s.upstreamProject(ctx, userID, projectID)
	if err != nil {
		return nil, err
	}
	c, err := s.repoAgent(ctx, p)
	if err != nil {
		return nil, err
	}
	repo, err := c.RepoUpstreamStatus(ctx, p.Protected())
	if err != nil {
		return nil, err
	}
	status := &UpstreamStatus{FetchedAt: repo.FetchedAt, Branches: repo.Branches}
	if status.Branches == nil {
		status.Branches = []agent.UpstreamBranch{}
	}
	s.mu.Lock()
	if run := s.upstreamSyncs[p.ID]; run != nil {
		if !run.finished() {
			status.Syncing = true
		} else if run.err != nil {
			at := run.at
			status.Error, status.ErrorAt = upstreamErrorText(run.err), &at
		}
	}
	s.mu.Unlock()
	return status, nil
}

// UpstreamBranches lists the upstream's branches as last fetched, leaving
// out names devpad would not act on, with the project's default branch. The
// protected branches come first, then the rest, the most recently committed
// to first, so a caller that keeps only the first few keeps those.
func (s *service) UpstreamBranches(ctx context.Context, userID, projectID int64) (*agent.RepoRefs, error) {
	p, err := s.upstreamProject(ctx, userID, projectID)
	if err != nil {
		return nil, err
	}
	c, err := s.repoAgent(ctx, p)
	if err != nil {
		return nil, err
	}
	refs, err := c.RepoUpstreamRefs(ctx)
	if err != nil {
		return nil, err
	}
	out := &agent.RepoRefs{DefaultBranch: p.DefaultBranch, Branches: make([]agent.RepoRef, 0, len(refs.Branches))}
	protected := make(map[string]bool)
	for _, branch := range p.Protected() {
		protected[branch] = true
	}
	var rest []agent.RepoRef
	for _, ref := range refs.Branches {
		switch {
		case !validBranchName(ref.Name):
		case protected[ref.Name]:
			out.Branches = append(out.Branches, ref)
		default:
			rest = append(rest, ref)
		}
	}
	out.Branches = append(out.Branches, rest...)
	if len(out.Branches) > maxUpstreamRefs {
		log.Printf("project %d: reading the first %d of the upstream's branches", p.ID, maxUpstreamRefs)
		out.Branches = out.Branches[:maxUpstreamRefs]
	}
	return out, nil
}

// FetchWorkspaceUpstreamBranches brings the upstream's copies of branches,
// as last fetched, into the project workspace as origin/<branch>, for git
// fetch through the origin remote. The names come from the workspace, so
// each is checked, and the repo refuses one the upstream lacks.
func (s *service) FetchWorkspaceUpstreamBranches(ctx context.Context, userID, projectID int64, branches []string) error {
	p, err := s.upstreamProject(ctx, userID, projectID)
	if err != nil {
		return err
	}
	unique, err := uniqueBranches(branches)
	if err != nil {
		return err
	}
	ws, err := s.runningWorkspace(ctx, p)
	if err != nil {
		return err
	}
	c, err := s.repoAgent(ctx, p)
	if err != nil {
		return err
	}
	bundle, err := c.RepoBundleUpstream(ctx, unique)
	var agentErr *agent.AgentError
	if errors.As(err, &agentErr) && agentErr.StatusCode == http.StatusNotFound {
		return invalid("the upstream has no such branch (%s)", agentErr.Message)
	}
	if err != nil {
		return err
	}
	defer bundle.Close()
	if _, err := s.workspaces.FetchProjectUpstreamBranches(ctx, userID, ws.ID, unique, bundle); err != nil {
		return err
	}
	return nil
}

// upstreamProject returns the project if it is ready and has an upstream.
func (s *service) upstreamProject(ctx context.Context, userID, projectID int64) (*Project, error) {
	p, err := s.getReady(ctx, userID, projectID)
	if err != nil {
		return nil, err
	}
	if p.UpstreamURL == "" {
		return nil, ErrNoUpstream
	}
	return p, nil
}

// startUpstreamSync returns the project's running sync, or the last one if
// it finished within maxAge, or starts a new one. A failed sync is only
// reused when reuseFailed is set.
func (s *service) startUpstreamSync(p *Project, maxAge time.Duration, reuseFailed bool) *upstreamSync {
	s.mu.Lock()
	defer s.mu.Unlock()
	if run := s.upstreamSyncs[p.ID]; run != nil {
		if !run.finished() || (time.Since(run.at) < maxAge && (run.err == nil || reuseFailed)) {
			return run
		}
	}
	run := &upstreamSync{done: make(chan struct{})}
	s.upstreamSyncs[p.ID] = run
	s.background.Add(1)
	go func() {
		defer s.background.Done()
		ctx, cancel := context.WithTimeout(context.Background(), upstreamSyncTimeout)
		defer cancel()
		status, err := s.syncUpstream(ctx, p.UserID, p.ID)
		if err != nil {
			log.Printf("project %d: syncing from the upstream: %v", p.ID, err)
		}
		s.mu.Lock()
		run.status, run.err, run.at = status, err, time.Now()
		s.mu.Unlock()
		close(run.done)
	}()
	return run
}

// syncUpstream fetches the upstream and fast-forwards the protected
// branches, then has the merge requests of those it moved follow them.
func (s *service) syncUpstream(ctx context.Context, userID, projectID int64) (*agent.UpstreamStatus, error) {
	status, err := s.fetchUpstream(ctx, userID, projectID)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	hook := s.publishHook
	s.mu.Unlock()
	for _, b := range status.Branches {
		if b.FastForwarded && hook != nil {
			hook.BranchFastForwarded(ctx, userID, projectID, b.Branch, b.SHA)
		}
	}
	return status, nil
}

// fetchUpstream does a sync's work in the repo container. The fetch, which
// can take minutes, moves no project branch and runs without the project's
// branch lock; the fast-forward after it holds the lock, so the protected
// set cannot change meanwhile. A merge that moves a branch first wins: the
// repo only fast-forwards a branch still where it was.
func (s *service) fetchUpstream(ctx context.Context, userID, projectID int64) (*agent.UpstreamStatus, error) {
	p, err := s.upstreamProject(ctx, userID, projectID)
	if err != nil {
		return nil, err
	}
	c, err := s.repoAgent(ctx, p)
	if err != nil {
		return nil, err
	}
	if err := s.writeUpstreamKeys(ctx, c, p); err != nil {
		return nil, err
	}
	if err := c.RepoUpstreamFetch(ctx); err != nil {
		return nil, err
	}

	unlock := s.lockBranches(projectID)
	defer unlock()
	fetched := p.UpstreamURL
	if p, err = s.upstreamProject(ctx, userID, projectID); err != nil {
		return nil, err
	}
	if p.UpstreamURL != fetched {
		return nil, errUpstreamChanged
	}
	return c.RepoUpstreamFastForward(ctx, p.Protected())
}

// SetUpstream points the project at another upstream, or at none when
// rawURL is empty. The repo forgets what it fetched from the old one, and
// devpad its last sync, so nothing of the old upstream is fast-forwarded
// into the project or shown as the new one's. It refuses while a sync runs
// rather than wait for one. Workspaces pick up the change from the git
// bridge.
func (s *service) SetUpstream(ctx context.Context, userID, projectID int64, rawURL string) (*Project, error) {
	upstream, err := upstreamURL(rawURL)
	if err != nil {
		return nil, err
	}
	p, err := s.getReady(ctx, userID, projectID)
	if err != nil {
		return nil, err
	}
	if p.UpstreamURL == upstream {
		return p, nil
	}
	c, err := s.repoAgent(ctx, p)
	if err != nil {
		return nil, err
	}

	// The branch lock orders this with the fast-forward that ends a sync,
	// which checks the upstream it fetched is still the project's.
	unlock := s.lockBranches(projectID)
	defer unlock()
	if p, err = s.getReady(ctx, userID, projectID); err != nil {
		return nil, err
	}
	if p.UpstreamURL == upstream {
		return p, nil
	}
	s.mu.Lock()
	if run := s.upstreamSyncs[p.ID]; run != nil && !run.finished() {
		s.mu.Unlock()
		return nil, fmt.Errorf("%w: wait for the sync from the upstream to finish", ErrBusy)
	}
	delete(s.upstreamSyncs, p.ID)
	s.mu.Unlock()

	if err := c.RepoSetUpstream(ctx, upstream); err != nil {
		return nil, err
	}
	if err := s.repo.SetUpstreamURL(ctx, p.ID, upstream); err != nil {
		// Put the repo back, so it and the project agree.
		if undoErr := c.RepoSetUpstream(context.WithoutCancel(ctx), p.UpstreamURL); undoErr != nil {
			log.Printf("project %d: restoring the upstream after a failed update: %v", p.ID, undoErr)
		}
		return nil, err
	}
	return s.Get(ctx, userID, projectID)
}

// writeUpstreamKeys writes the user's SSH key into the repo container, the
// only container that holds it, which never runs project code. It is
// written on every use, so a rotated key takes effect in every project.
func (s *service) writeUpstreamKeys(ctx context.Context, c *agent.Client, p *Project) error {
	user, err := s.users.GetByID(ctx, p.UserID)
	if err != nil {
		return fmt.Errorf("looking up user: %w", err)
	}
	if err := workspace.WriteSSHKeys(ctx, c, user, s.cipher); err != nil {
		return fmt.Errorf("injecting SSH keys into repo container: %w", err)
	}
	return nil
}

// upstreamErrorText is a failed sync's error as the user sees it.
func upstreamErrorText(err error) string {
	var agentErr *agent.AgentError
	switch {
	case errors.As(err, &agentErr):
		return agentErr.Message
	case errors.Is(err, ErrNotReady), errors.Is(err, ErrNoUpstream), errors.Is(err, ErrNotFound), errors.Is(err, errUpstreamChanged):
		return err.Error()
	}
	return "devpad could not fetch from the upstream"
}
