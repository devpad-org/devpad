package project

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/devpad-org/devpad/internal/agent"
)

func (e *testEnv) upstreamFetchCount() int {
	e.repoAgent.mu.Lock()
	defer e.repoAgent.mu.Unlock()
	return len(e.repoAgent.upstreamFetches)
}

func TestSyncUpstreamFastForwardsAndMovesMergeRequests(t *testing.T) {
	e := setupTest(t)
	ctx := context.Background()
	p := e.createProject(t, CreateInput{Name: "proj", UpstreamURL: "https://example.com/repo.git"})
	e.repoAgent.branches = []agent.RepoRef{{Name: "main", SHA: "m"}, {Name: "develop", SHA: "d"}}
	if _, err := e.svc.SetBranches(ctx, e.userID, p.ID, BranchesInput{ProtectedBranches: []string{"develop"}}); err != nil {
		t.Fatalf("set branches: %v", err)
	}
	hook := &recordingPublishHook{}
	e.svc.SetPublishHook(hook)
	fetched := time.Now().UTC().Truncate(time.Second)
	e.repoAgent.upstreamStatus = agent.UpstreamStatus{FetchedAt: &fetched, Branches: []agent.UpstreamBranch{
		{Branch: "main", SHA: "m2", UpstreamSHA: "m2", FastForwarded: true},
		{Branch: "develop", SHA: "d", UpstreamSHA: "u", Ahead: 1, Behind: 2},
	}}

	status, err := e.svc.SyncUpstream(ctx, e.userID, p.ID)
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if len(status.Branches) != 2 || status.FetchedAt == nil || !status.FetchedAt.Equal(fetched) {
		t.Fatalf("status = %+v", status)
	}
	// The protected branches come from the project, never the caller.
	if got := e.repoAgent.upstreamFetches; len(got) != 1 || got[0] != "main,develop" {
		t.Fatalf("fetches named %v", got)
	}
	// Only the branch that moved has its merge request follow it.
	if strings.Join(hook.calls, " ") != "upstream:main@m2" {
		t.Fatalf("hook calls %v", hook.calls)
	}

	got, err := e.svc.UpstreamStatus(ctx, e.userID, p.ID)
	if err != nil || got.Syncing || got.Error != "" || len(got.Branches) != 2 || got.Branches[1].Behind != 2 {
		t.Fatalf("upstream status %+v, %v", got, err)
	}
}

func TestUpstreamSyncsAreShared(t *testing.T) {
	e := setupTest(t)
	ctx := context.Background()
	p := e.createProject(t, CreateInput{Name: "proj", UpstreamURL: "https://example.com/repo.git"})
	gate := make(chan struct{})
	e.repoAgent.mu.Lock()
	e.repoAgent.fetchGate = gate
	e.repoAgent.mu.Unlock()

	// Syncs asked for while one runs wait for it.
	var wg sync.WaitGroup
	for range 3 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := e.svc.SyncUpstream(ctx, e.userID, p.ID); err != nil {
				t.Errorf("sync: %v", err)
			}
		}()
	}
	// git fetch devpad does not wait long for a sync.
	deadline := time.Now().Add(2 * time.Second)
	for {
		e.svc.mu.Lock()
		started := e.svc.upstreamSyncs[p.ID] != nil
		e.svc.mu.Unlock()
		if started || time.Now().After(deadline) {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if warning := e.svc.SyncUpstreamIfStale(ctx, e.userID, p.ID, 10*time.Millisecond); !strings.Contains(warning, "still fetching") {
		t.Fatalf("warning %q", warning)
	}
	if status, err := e.svc.UpstreamStatus(ctx, e.userID, p.ID); err != nil || !status.Syncing {
		t.Fatalf("status while syncing %+v, %v", status, err)
	}
	close(gate)
	wg.Wait()
	if n := e.upstreamFetchCount(); n != 1 {
		t.Fatalf("expected one fetch, got %d", n)
	}

	// A sync that just finished is reused, by a forced sync and a stale one.
	if _, err := e.svc.SyncUpstream(ctx, e.userID, p.ID); err != nil {
		t.Fatalf("sync: %v", err)
	}
	if warning := e.svc.SyncUpstreamIfStale(ctx, e.userID, p.ID, time.Second); warning != "" {
		t.Fatalf("warning %q", warning)
	}
	if n := e.upstreamFetchCount(); n != 1 {
		t.Fatalf("expected the sync reused, got %d fetches", n)
	}

	// Once stale, git fetch devpad syncs again.
	e.svc.mu.Lock()
	e.svc.upstreamSyncs[p.ID].at = time.Now().Add(-2 * upstreamStaleAfter)
	e.svc.mu.Unlock()
	if warning := e.svc.SyncUpstreamIfStale(ctx, e.userID, p.ID, time.Second); warning != "" {
		t.Fatalf("warning %q", warning)
	}
	if n := e.upstreamFetchCount(); n != 2 {
		t.Fatalf("expected a second fetch, got %d", n)
	}
}

func TestFailedUpstreamSync(t *testing.T) {
	e := setupTest(t)
	ctx := context.Background()
	p := e.createProject(t, CreateInput{Name: "proj", UpstreamURL: "git@example.com:repo.git"})
	e.repoAgent.upstreamErr = "fetching from upstream: Permission denied (publickey)"

	_, err := e.svc.SyncUpstream(ctx, e.userID, p.ID)
	var agentErr *agent.AgentError
	if !errors.As(err, &agentErr) || agentErr.StatusCode != 502 {
		t.Fatalf("expected the repo's 502, got %v", err)
	}
	status, err := e.svc.UpstreamStatus(ctx, e.userID, p.ID)
	if err != nil || !strings.Contains(status.Error, "Permission denied") || status.ErrorAt == nil {
		t.Fatalf("status %+v, %v", status, err)
	}
	// git fetch devpad is warned without the detail, and does not retry at
	// once; a forced sync does.
	if warning := e.svc.SyncUpstreamIfStale(ctx, e.userID, p.ID, time.Second); warning != errUpstreamFetch {
		t.Fatalf("warning %q", warning)
	}
	if n := e.upstreamFetchCount(); n != 1 {
		t.Fatalf("a stale sync retried a failed one: %d fetches", n)
	}
	e.repoAgent.mu.Lock()
	e.repoAgent.upstreamErr = ""
	e.repoAgent.mu.Unlock()
	if _, err := e.svc.SyncUpstream(ctx, e.userID, p.ID); err != nil {
		t.Fatalf("retry: %v", err)
	}
	if status, _ := e.svc.UpstreamStatus(ctx, e.userID, p.ID); status.Error != "" {
		t.Fatalf("error kept after a good sync: %q", status.Error)
	}
}

func TestProjectsWithoutUpstreamDoNotSync(t *testing.T) {
	e := setupTest(t)
	ctx := context.Background()
	p := e.createProject(t, CreateInput{Name: "local"})
	if _, err := e.svc.SyncUpstream(ctx, e.userID, p.ID); !errors.Is(err, ErrNoUpstream) {
		t.Fatalf("sync: %v", err)
	}
	if _, err := e.svc.UpstreamStatus(ctx, e.userID, p.ID); !errors.Is(err, ErrNoUpstream) {
		t.Fatalf("status: %v", err)
	}
	if warning := e.svc.SyncUpstreamIfStale(ctx, e.userID, p.ID, time.Second); warning != "" {
		t.Fatalf("warning %q", warning)
	}
	if n := e.upstreamFetchCount(); n != 0 {
		t.Fatalf("%d fetches", n)
	}
	if _, err := e.svc.SyncUpstream(ctx, e.otherID, p.ID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("another user's sync: %v", err)
	}
}

func TestFetchWorkspaceUpstreamBranches(t *testing.T) {
	e := setupTest(t)
	ctx := context.Background()
	p := e.createProject(t, CreateInput{Name: "proj", UpstreamURL: "https://example.com/repo.git"})
	ws, _, err := e.svc.OpenWorkspace(ctx, e.userID, p.ID)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	e.repoAgent.mu.Lock()
	e.repoAgent.upstream = []agent.RepoRef{{Name: "main", SHA: "m"}, {Name: "theirs", SHA: "t"}, {Name: "über", SHA: "x"}}
	e.repoAgent.bundled = nil
	e.repoAgent.mu.Unlock()

	// Names devpad would not act on are not offered.
	refs, err := e.svc.UpstreamBranches(ctx, e.userID, p.ID)
	if err != nil || len(refs.Branches) != 2 || refs.DefaultBranch != "main" {
		t.Fatalf("upstream branches %+v, %v", refs, err)
	}

	if err := e.svc.FetchWorkspaceUpstreamBranches(ctx, e.userID, p.ID, []string{"theirs", "main", "theirs"}); err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if got := e.workspaces.seeds[ws.ID]; got != "origin/{theirs,main} <- bundle-of-upstream:theirs,main" {
		t.Fatalf("fetched %q", got)
	}
	e.repoAgent.mu.Lock()
	e.repoAgent.bundled = nil
	e.repoAgent.mu.Unlock()
	for _, branches := range [][]string{nil, {"--all"}, {"main", "missing"}} {
		if err := e.svc.FetchWorkspaceUpstreamBranches(ctx, e.userID, p.ID, branches); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("fetch %v: expected ErrInvalidInput, got %v", branches, err)
		}
	}
	if len(e.repoAgent.bundled) != 0 {
		t.Fatalf("a refused fetch bundled %v", e.repoAgent.bundled)
	}
}

func TestPushThatUpstreamRefusesSaysHowToCatchUp(t *testing.T) {
	e := setupTest(t)
	ctx := context.Background()
	p := e.createProject(t, CreateInput{Name: "proj", UpstreamURL: "https://example.com/repo.git"})
	e.repoAgent.pushErr = "pushing to upstream: ! [rejected] main -> main (fetch first)"
	_, err := e.svc.Push(ctx, e.userID, p.ID, "")
	if !errors.Is(err, ErrInvalidInput) || !strings.Contains(err.Error(), "sync from the upstream") {
		t.Fatalf("push: %v", err)
	}
	e.repoAgent.pushErr = "pushing to upstream: Permission denied (publickey)"
	var agentErr *agent.AgentError
	if _, err := e.svc.Push(ctx, e.userID, p.ID, ""); !errors.As(err, &agentErr) {
		t.Fatalf("push: %v", err)
	}
}
