package project

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/devpad-org/devpad/internal/agent"
)

func (e *testEnv) upstreamFetchCount() int {
	e.repoAgent.mu.Lock()
	defer e.repoAgent.mu.Unlock()
	return e.repoAgent.upstreamFetches
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
	if got := e.repoAgent.upstreamFFs; e.upstreamFetchCount() != 1 || len(got) != 1 || got[0] != "main,develop" {
		t.Fatalf("%d fetches, fast-forwards named %v", e.upstreamFetchCount(), got)
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

func TestUpstreamFetchDoesNotHoldTheBranchLock(t *testing.T) {
	e := setupTest(t)
	ctx := context.Background()
	p := e.createProject(t, CreateInput{Name: "proj", UpstreamURL: "https://example.com/repo.git"})
	e.repoAgent.branches = []agent.RepoRef{{Name: "main", SHA: "m"}, {Name: "develop", SHA: "d"}}
	gate := make(chan struct{})
	e.repoAgent.mu.Lock()
	e.repoAgent.fetchGate = gate
	e.repoAgent.mu.Unlock()

	done := make(chan error, 1)
	go func() {
		_, err := e.svc.SyncUpstream(ctx, e.userID, p.ID)
		done <- err
	}()
	deadline := time.Now().Add(2 * time.Second)
	for e.upstreamFetchCount() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the fetch never started")
		}
		time.Sleep(time.Millisecond)
	}
	// Changing the protected branches does not wait for the fetch...
	set := make(chan error, 1)
	go func() {
		_, err := e.svc.SetBranches(ctx, e.userID, p.ID, BranchesInput{ProtectedBranches: []string{"develop"}})
		set <- err
	}()
	select {
	case err := <-set:
		if err != nil {
			t.Fatalf("set branches during a fetch: %v", err)
		}
	case <-time.After(2 * time.Second):
		close(gate)
		t.Fatal("setting the branches waited for the fetch")
	}
	close(gate)
	if err := <-done; err != nil {
		t.Fatalf("sync: %v", err)
	}
	// ...and the fast-forward after it uses the new set.
	e.repoAgent.mu.Lock()
	defer e.repoAgent.mu.Unlock()
	if got := e.repoAgent.upstreamFFs; len(got) != 1 || got[0] != "main,develop" {
		t.Fatalf("fast-forwards named %v", got)
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
	e.repoAgent.upstream = []agent.RepoRef{{Name: "theirs", SHA: "t"}, {Name: "main", SHA: "m"}, {Name: "über", SHA: "x"}}
	e.repoAgent.bundled = nil
	e.repoAgent.mu.Unlock()

	// Names devpad would not act on are not offered, and the protected
	// branches come first.
	refs, err := e.svc.UpstreamBranches(ctx, e.userID, p.ID)
	if err != nil || len(refs.Branches) != 2 || refs.DefaultBranch != "main" || refs.Branches[0].Name != "main" {
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
	// The repo answers 409 when the upstream has commits the branch lacks.
	e.repoAgent.pushErr, e.repoAgent.pushStatus = "pushing to upstream: ! [rejected] main -> main (fetch first)", http.StatusConflict
	_, err := e.svc.Push(ctx, e.userID, p.ID, "")
	if !errors.Is(err, ErrInvalidInput) || !strings.Contains(err.Error(), "sync from the upstream") {
		t.Fatalf("push: %v", err)
	}
	// Anything else, whatever its wording, is the repo's error.
	e.repoAgent.pushErr, e.repoAgent.pushStatus = "pushing to upstream: [rejected] non-fast-forward", 0
	var agentErr *agent.AgentError
	if _, err := e.svc.Push(ctx, e.userID, p.ID, ""); !errors.As(err, &agentErr) {
		t.Fatalf("push: %v", err)
	}
}

func TestSetUpstream(t *testing.T) {
	e := setupTest(t)
	ctx := context.Background()
	p := e.createProject(t, CreateInput{Name: "proj"})
	hook := &fakeUpstreamHook{}
	e.svc.SetUpstreamHook(hook)
	setUpstreams := func() []string {
		e.repoAgent.mu.Lock()
		defer e.repoAgent.mu.Unlock()
		return slices.Clone(e.repoAgent.setUpstreams)
	}

	// A project made without an upstream can be given one.
	const url = "git@github.com:example/project.git"
	got, err := e.svc.SetUpstream(ctx, e.userID, p.ID, "  "+url+" ")
	if err != nil || got.UpstreamURL != url || got.UpstreamVersion != 1 {
		t.Fatalf("set upstream: %+v, %v", got, err)
	}
	if stored, _ := e.svc.Get(ctx, e.userID, p.ID); stored.UpstreamURL != url {
		t.Fatalf("stored upstream %q", stored.UpstreamURL)
	}
	if calls := setUpstreams(); len(calls) != 1 || calls[0] != url {
		t.Fatalf("repo pointed at %v", calls)
	}
	if changed := hook.changes(); len(changed) != 1 || changed[0] != p.ID {
		t.Fatalf("hook told of %v", changed)
	}

	// The same URL changes nothing recorded, though the repo is pointed at
	// it again, in case a failed change left it elsewhere.
	if got, err := e.svc.SetUpstream(ctx, e.userID, p.ID, url); err != nil || got.UpstreamVersion != 1 {
		t.Fatalf("same upstream: %+v, %v", got, err)
	}
	if calls := setUpstreams(); len(calls) != 2 || calls[1] != url {
		t.Fatalf("repo pointed at %v", calls)
	}
	if changed := hook.changes(); len(changed) != 1 {
		t.Fatalf("hook told of %v for an unchanged upstream", changed)
	}

	// A sync with the old upstream is forgotten with it.
	e.repoAgent.mu.Lock()
	e.repoAgent.upstreamErr = "Permission denied (publickey)"
	e.repoAgent.mu.Unlock()
	if _, err := e.svc.SyncUpstream(ctx, e.userID, p.ID); err == nil {
		t.Fatal("expected the sync to fail")
	}
	if status, _ := e.svc.UpstreamStatus(ctx, e.userID, p.ID); status.Error == "" {
		t.Fatal("the failed sync was not reported")
	}
	const next = "git@github.com:example/next.git"
	if _, err := e.svc.SetUpstream(ctx, e.userID, p.ID, next); err != nil {
		t.Fatalf("change upstream: %v", err)
	}
	if status, err := e.svc.UpstreamStatus(ctx, e.userID, p.ID); err != nil || status.Error != "" {
		t.Fatalf("status after the change %+v, %v", status, err)
	}
	e.repoAgent.mu.Lock()
	e.repoAgent.upstreamErr = ""
	e.repoAgent.mu.Unlock()
	if _, err := e.svc.SyncUpstream(ctx, e.userID, p.ID); err != nil {
		t.Fatalf("sync with the new upstream: %v", err)
	}

	for _, bad := range []string{"--upload-pack=x", "https://user:token@github.com/example/project.git", "git@github.com:a\nb"} {
		if _, err := e.svc.SetUpstream(ctx, e.userID, p.ID, bad); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("%q: %v", bad, err)
		}
	}

	// When the repo refuses, the project keeps its upstream.
	e.repoAgent.mu.Lock()
	e.repoAgent.setUpstreamErr = "a fetch from the upstream is still running"
	e.repoAgent.mu.Unlock()
	var agentErr *agent.AgentError
	if _, err := e.svc.SetUpstream(ctx, e.userID, p.ID, url); !errors.As(err, &agentErr) || agentErr.StatusCode != http.StatusConflict {
		t.Fatalf("refused by the repo: %v", err)
	}
	if stored, _ := e.svc.Get(ctx, e.userID, p.ID); stored.UpstreamURL != next {
		t.Fatalf("upstream after a refusal %q", stored.UpstreamURL)
	}

	// An empty URL removes the upstream.
	if got, err := e.svc.SetUpstream(ctx, e.userID, p.ID, ""); err != nil || got.UpstreamURL != "" {
		t.Fatalf("remove upstream: %+v, %v", got, err)
	}
	if calls := setUpstreams(); calls[len(calls)-1] != "" {
		t.Fatalf("repo pointed at %v", calls)
	}
	if _, err := e.svc.SyncUpstream(ctx, e.userID, p.ID); !errors.Is(err, ErrNoUpstream) {
		t.Fatalf("sync without an upstream: %v", err)
	}
}

// fakeUpstreamHook records the projects whose upstream changed.
type fakeUpstreamHook struct {
	mu      sync.Mutex
	changed []int64
}

func (h *fakeUpstreamHook) UpstreamChanged(projectID int64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.changed = append(h.changed, projectID)
}

func (h *fakeUpstreamHook) changes() []int64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	return slices.Clone(h.changed)
}

// failingUpstreamRepo fails recording an upstream, then has the repo refuse
// the next change of upstream, as one that started a fetch would.
type failingUpstreamRepo struct {
	Repository
	agent *fakeRepoAgent
}

func (r failingUpstreamRepo) SetUpstreamURL(context.Context, int64, string) error {
	r.agent.mu.Lock()
	r.agent.setUpstreamErr = "a fetch from or push to the upstream is still running"
	r.agent.mu.Unlock()
	return errors.New("disk full")
}

func TestSetUpstreamRepairsARepoAFailedChangeLeftElsewhere(t *testing.T) {
	e := setupTest(t)
	ctx := context.Background()
	const url, next = "https://example.com/repo.git", "https://example.com/other.git"
	p := e.createProject(t, CreateInput{Name: "proj", UpstreamURL: url})
	repo := e.svc.repo
	e.svc.repo = failingUpstreamRepo{Repository: repo, agent: e.repoAgent}

	// The record fails and so does putting the repo back.
	if _, err := e.svc.SetUpstream(ctx, e.userID, p.ID, next); err == nil {
		t.Fatal("expected the change to fail")
	}
	e.svc.repo = repo
	e.repoAgent.mu.Lock()
	origin := e.repoAgent.origin
	e.repoAgent.mu.Unlock()
	if origin != next {
		t.Fatalf("repo origin %q; the test expects the failed change to leave it at the new upstream", origin)
	}

	// The repo, pointing elsewhere than the project, is never synced from
	// or pushed to.
	if _, err := e.svc.SyncUpstream(ctx, e.userID, p.ID); err == nil {
		t.Fatal("synced from an upstream the project does not have")
	}
	if ffs := e.repoAgent.upstreamFFs; len(ffs) != 0 {
		t.Fatalf("fast-forwarded %v", ffs)
	}
	if _, err := e.svc.Push(ctx, e.userID, p.ID, ""); err == nil {
		t.Fatal("pushed to an upstream the project does not have")
	}

	// Setting the project's own upstream again puts the repo right.
	if _, err := e.svc.SetUpstream(ctx, e.userID, p.ID, url); err != nil {
		t.Fatalf("set the upstream again: %v", err)
	}
	e.repoAgent.mu.Lock()
	origin = e.repoAgent.origin
	e.repoAgent.mu.Unlock()
	if origin != url {
		t.Fatalf("repo origin %q after setting the upstream again", origin)
	}
	if _, err := e.svc.SyncUpstream(ctx, e.userID, p.ID); err != nil {
		t.Fatalf("sync after the repair: %v", err)
	}
}

func TestSetUpstreamWaitsForNoSync(t *testing.T) {
	e := setupTest(t)
	ctx := context.Background()
	p := e.createProject(t, CreateInput{Name: "proj", UpstreamURL: "https://example.com/repo.git"})
	gate := make(chan struct{})
	e.repoAgent.mu.Lock()
	e.repoAgent.fetchGate = gate
	e.repoAgent.mu.Unlock()

	done := make(chan error, 1)
	go func() {
		_, err := e.svc.SyncUpstream(ctx, e.userID, p.ID)
		done <- err
	}()
	deadline := time.Now().Add(2 * time.Second)
	for e.upstreamFetchCount() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the sync never started")
		}
		time.Sleep(time.Millisecond)
	}
	if _, err := e.svc.SetUpstream(ctx, e.userID, p.ID, "https://example.com/other.git"); !errors.Is(err, ErrBusy) {
		t.Fatalf("set upstream during a sync: %v", err)
	}
	close(gate)
	if err := <-done; err != nil {
		t.Fatalf("sync: %v", err)
	}
	if _, err := e.svc.SetUpstream(ctx, e.userID, p.ID, "https://example.com/other.git"); err != nil {
		t.Fatalf("set upstream after the sync: %v", err)
	}
}

func TestSyncFailsWhenTheUpstreamChangesDuringTheFetch(t *testing.T) {
	e := setupTest(t)
	ctx := context.Background()
	p := e.createProject(t, CreateInput{Name: "proj", UpstreamURL: "https://example.com/repo.git"})
	gate := make(chan struct{})
	e.repoAgent.mu.Lock()
	e.repoAgent.fetchGate = gate
	e.repoAgent.mu.Unlock()

	done := make(chan error, 1)
	go func() {
		_, err := e.svc.fetchUpstream(ctx, e.userID, p.ID)
		done <- err
	}()
	deadline := time.Now().Add(2 * time.Second)
	for e.upstreamFetchCount() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the fetch never started")
		}
		time.Sleep(time.Millisecond)
	}
	// The real repo refuses while it fetches; this changes the record
	// alone, as a change landing between the fetch and the fast-forward
	// would look.
	if err := e.svc.repo.SetUpstreamURL(ctx, p.ID, "https://example.com/other.git"); err != nil {
		t.Fatal(err)
	}
	close(gate)
	if err := <-done; !errors.Is(err, errUpstreamChanged) {
		t.Fatalf("fetch across a change: %v", err)
	}
	if ffs := e.repoAgent.upstreamFFs; len(ffs) != 0 {
		t.Fatalf("fast-forwarded %v from the old upstream", ffs)
	}
}
