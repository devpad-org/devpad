package gitbridge

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/devpad-org/devpad/internal/agent"
	"github.com/devpad-org/devpad/internal/auth"
	"github.com/devpad-org/devpad/internal/project"
	"github.com/devpad-org/devpad/internal/workspace"
)

type fakeWorkspaces struct {
	mu         sync.Mutex
	all        []*workspace.Workspace
	queued     map[int64][]agent.GitBridgeRequest
	polledBy   map[int64]int
	configured map[int64]int
	responses  map[string]agent.GitBridgeResult
	pollErr    error
}

func newFakeWorkspaces(all ...*workspace.Workspace) *fakeWorkspaces {
	return &fakeWorkspaces{
		all:        all,
		queued:     map[int64][]agent.GitBridgeRequest{},
		polledBy:   map[int64]int{},
		configured: map[int64]int{},
		responses:  map[string]agent.GitBridgeResult{},
	}
}

func (f *fakeWorkspaces) ListAll(ctx context.Context) ([]*workspace.Workspace, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]*workspace.Workspace, len(f.all))
	for i, ws := range f.all {
		copied := *ws
		out[i] = &copied
	}
	return out, nil
}

func (f *fakeWorkspaces) set(id int64, change func(ws *workspace.Workspace)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, ws := range f.all {
		if ws.ID == id {
			change(ws)
		}
	}
}

func (f *fakeWorkspaces) ConfigureGitBridge(ctx context.Context, userID, workspaceID int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.configured[workspaceID]++
	return nil
}

func (f *fakeWorkspaces) GitBridgeRequests(ctx context.Context, userID, workspaceID int64, wait time.Duration) ([]agent.GitBridgeRequest, error) {
	f.mu.Lock()
	f.polledBy[workspaceID]++
	reqs := f.queued[workspaceID]
	delete(f.queued, workspaceID)
	err := f.pollErr
	f.mu.Unlock()
	if err != nil {
		return nil, err
	}
	if len(reqs) == 0 {
		select {
		case <-ctx.Done():
		case <-time.After(5 * time.Millisecond):
		}
	}
	return reqs, nil
}

func (f *fakeWorkspaces) GitBridgeRespond(ctx context.Context, userID, workspaceID int64, id string, result agent.GitBridgeResult) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.responses[id] = result
	return nil
}

func (f *fakeWorkspaces) response(t *testing.T, id string) agent.GitBridgeResult {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		f.mu.Lock()
		res, ok := f.responses[id]
		f.mu.Unlock()
		if ok {
			return res
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("no response to %s", id)
	return agent.GitBridgeResult{}
}

type fakeProjects struct {
	// block, when set, holds every publish until closed.
	block      chan struct{}
	mu         sync.Mutex
	published  []string
	fetched    [][]string
	publishErr error
	listed     []int64
}

func (f *fakeProjects) Branches(ctx context.Context, userID, projectID int64) (*agent.RepoRefs, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.listed = append(f.listed, projectID)
	return &agent.RepoRefs{DefaultBranch: "main", Branches: []agent.RepoRef{{Name: "main", SHA: "aaa"}}}, nil
}

func (f *fakeProjects) FetchWorkspaceBranches(ctx context.Context, userID, projectID int64, branches []string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fetched = append(f.fetched, branches)
	return nil
}

func (f *fakeProjects) PublishWorkspaceBranch(ctx context.Context, userID, projectID int64, branch string) (*agent.RepoBranch, error) {
	if f.block != nil {
		<-f.block
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.published = append(f.published, fmt.Sprintf("%d:%d:%s", userID, projectID, branch))
	if f.publishErr != nil {
		return nil, f.publishErr
	}
	return &agent.RepoBranch{Branch: branch, SHA: "bbb"}, nil
}

func projectWorkspace(id, projectID int64) *workspace.Workspace {
	return &workspace.Workspace{ID: id, UserID: 7, ProjectID: projectID, Kind: workspace.KindInteractive,
		Status: workspace.StatusRunning, ContainerID: fmt.Sprintf("c%d", id), RepoSeeded: true}
}

func newTestBridge(ws *fakeWorkspaces, ps *fakeProjects) *Bridge {
	b := New(ws, ps)
	b.pollWait = 10 * time.Millisecond
	b.backoffMin = time.Millisecond
	b.backoffMax = 5 * time.Millisecond
	return b
}

// stopAll cancels the bridge's pollers and waits for them.
func stopAll(t *testing.T, b *Bridge, cancel context.CancelFunc) {
	t.Helper()
	cancel()
	done := make(chan struct{})
	go func() { b.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("pollers did not stop")
	}
}

func TestReconcilePollsOnlyRunningProjectWorkspaces(t *testing.T) {
	pw := projectWorkspace(1, 10)
	sandbox := &workspace.Workspace{ID: 2, UserID: 7, ProjectID: 10, Kind: workspace.KindTask, Branch: "task/1",
		Status: workspace.StatusRunning, ContainerID: "c2", RepoSeeded: true}
	standalone := &workspace.Workspace{ID: 3, UserID: 7, Kind: workspace.KindInteractive, Status: workspace.StatusRunning, ContainerID: "c3"}
	stopped := projectWorkspace(4, 11)
	stopped.Status = workspace.StatusStopped
	unseeded := projectWorkspace(5, 12)
	unseeded.RepoSeeded = false
	fw := newFakeWorkspaces(pw, sandbox, standalone, stopped, unseeded)
	b := newTestBridge(fw, &fakeProjects{})

	ctx, cancel := context.WithCancel(context.Background())
	defer stopAll(t, b, cancel)
	b.reconcile(ctx)
	if got := b.polled(); len(got) != 1 || !got[1] {
		t.Fatalf("polled %v, want only workspace 1", got)
	}
	time.Sleep(30 * time.Millisecond)
	fw.mu.Lock()
	for id := range fw.polledBy {
		if id != 1 {
			t.Errorf("workspace %d was polled", id)
		}
	}
	if fw.configured[1] == 0 {
		t.Error("the devpad remote was never set up")
	}
	fw.mu.Unlock()

	// A new container gets a new poller; a stopped workspace loses it.
	fw.set(1, func(ws *workspace.Workspace) { ws.ContainerID = "c1b" })
	b.reconcile(ctx)
	b.mu.Lock()
	if p := b.pollers[1]; p == nil || p.containerID != "c1b" {
		t.Fatalf("poller not restarted for the new container: %+v", p)
	}
	b.mu.Unlock()
	fw.set(1, func(ws *workspace.Workspace) { ws.Status = workspace.StatusStopped })
	b.reconcile(ctx)
	if got := b.polled(); len(got) != 0 {
		t.Fatalf("still polling %v after the workspace stopped", got)
	}
}

func TestRunStopsPollersOnShutdown(t *testing.T) {
	fw := newFakeWorkspaces(projectWorkspace(1, 10))
	b := newTestBridge(fw, &fakeProjects{})
	b.scanEvery = time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { b.Run(ctx); close(done) }()
	time.Sleep(20 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return")
	}
	if got := b.polled(); len(got) != 0 {
		t.Fatalf("pollers left after shutdown: %v", got)
	}
}

func TestPollerSurvivesAgentErrors(t *testing.T) {
	fw := newFakeWorkspaces(projectWorkspace(1, 10))
	fw.pollErr = errors.New("connection refused")
	ps := &fakeProjects{}
	b := newTestBridge(fw, ps)
	ctx, cancel := context.WithCancel(context.Background())
	defer stopAll(t, b, cancel)
	b.reconcile(ctx)
	time.Sleep(20 * time.Millisecond)

	// The agent comes back and a request gets through.
	fw.mu.Lock()
	fw.pollErr = nil
	fw.queued[1] = []agent.GitBridgeRequest{{ID: "aa", Op: "list"}}
	fw.mu.Unlock()
	if res := fw.response(t, "aa"); res.Error != "" || res.DefaultBranch != "main" {
		t.Fatalf("list result %+v", res)
	}
}

func TestPushesGoThroughThePublishRules(t *testing.T) {
	fw := newFakeWorkspaces(projectWorkspace(1, 10))
	ps := &fakeProjects{}
	b := newTestBridge(fw, ps)
	ws := projectWorkspace(1, 10)
	ctx := context.Background()

	if res := b.handle(ctx, ws, agent.GitBridgeRequest{ID: "a1", Op: "push", Branches: []string{"feature"}}); res.Error != "" || res.SHA != "bbb" {
		t.Fatalf("push result %+v", res)
	}
	// The project and user come from devpad's record, not the request.
	if len(ps.published) != 1 || ps.published[0] != "7:10:feature" {
		t.Fatalf("published %v", ps.published)
	}

	// Force pushes never reach the project service.
	if res := b.handle(ctx, ws, agent.GitBridgeRequest{ID: "a2", Op: "push", Branches: []string{"feature"}, Force: true}); !strings.Contains(res.Error, "force") {
		t.Fatalf("force push result %+v", res)
	}
	if len(ps.published) != 1 {
		t.Fatalf("a force push was published: %v", ps.published)
	}

	cases := []struct {
		err  error
		want string
	}{
		{project.ErrPublishDefault, "merge request"},
		{project.ErrBranchInUse, "sandbox owns"},
		{project.ErrBranchPublishing, "already being published"},
		{&agent.AgentError{StatusCode: 409, Message: "feature has commits the published branch lacks; merge devpad/feature first"}, "merge devpad/feature first"},
		{errors.New("docker: container abc123 exploded"), "could not complete"},
	}
	for _, tc := range cases {
		ps.publishErr = tc.err
		res := b.handle(ctx, ws, agent.GitBridgeRequest{ID: "a3", Op: "push", Branches: []string{"feature"}})
		if !strings.Contains(res.Error, tc.want) {
			t.Errorf("%v: result %+v, want %q", tc.err, res, tc.want)
		}
		if strings.Contains(res.Error, "abc123") {
			t.Errorf("internal detail reached the workspace: %q", res.Error)
		}
	}

	// Malformed requests are refused.
	for _, req := range []agent.GitBridgeRequest{
		{Op: "push"},
		{Op: "push", Branches: []string{"a", "b"}},
		{Op: "fetch"},
		{Op: "delete", Branches: []string{"feature"}},
	} {
		if res := b.handle(ctx, ws, req); res.Error == "" {
			t.Errorf("request %+v was accepted", req)
		}
	}
}

func TestFetchAndListUseTheWorkspacesProject(t *testing.T) {
	ps := &fakeProjects{}
	b := newTestBridge(newFakeWorkspaces(), ps)
	ws := projectWorkspace(1, 10)
	if res := b.handle(context.Background(), ws, agent.GitBridgeRequest{Op: "fetch", Branches: []string{"main", "task/1"}}); res.Error != "" {
		t.Fatalf("fetch result %+v", res)
	}
	if len(ps.fetched) != 1 || strings.Join(ps.fetched[0], ",") != "main,task/1" {
		t.Fatalf("fetched %v", ps.fetched)
	}
	res := b.handle(context.Background(), ws, agent.GitBridgeRequest{Op: "list"})
	if res.DefaultBranch != "main" || len(res.Refs) != 1 || len(ps.listed) != 1 || ps.listed[0] != 10 {
		t.Fatalf("list result %+v, listed %v", res, ps.listed)
	}
}

func TestPollerIgnoresMalformedRequestIDs(t *testing.T) {
	fw := newFakeWorkspaces(projectWorkspace(1, 10))
	ps := &fakeProjects{}
	b := newTestBridge(fw, ps)
	fw.queued[1] = []agent.GitBridgeRequest{
		{ID: "../../api/file", Op: "push", Branches: []string{"x"}},
		{ID: "bb", Op: "push", Branches: []string{"y"}},
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer stopAll(t, b, cancel)
	b.reconcile(ctx)
	fw.response(t, "bb")
	ps.mu.Lock()
	defer ps.mu.Unlock()
	if len(ps.published) != 1 || !strings.HasSuffix(ps.published[0], ":y") {
		t.Fatalf("published %v", ps.published)
	}
}

func TestActivityIsPerUserAndProject(t *testing.T) {
	b := newTestBridge(newFakeWorkspaces(), &fakeProjects{})
	ws := projectWorkspace(1, 10)
	b.handle(context.Background(), ws, agent.GitBridgeRequest{Op: "push", Branches: []string{"feature"}})

	h := NewHandler(b)
	get := func(userID int64, target string) string {
		req := httptest.NewRequest(http.MethodGet, target, nil)
		req.SetPathValue("id", "10")
		req = req.WithContext(context.WithValue(req.Context(), auth.UserContextKey, &auth.User{ID: userID}))
		rec := httptest.NewRecorder()
		h.HandleActivity(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status %d: %s", rec.Code, rec.Body)
		}
		return rec.Body.String()
	}
	if body := get(7, "/api/projects/10/git-activity"); !strings.Contains(body, `"events":[]`) || !strings.Contains(body, `"latest":1`) {
		t.Fatalf("first load replayed events: %s", body)
	}
	if body := get(7, "/api/projects/10/git-activity?after=0"); !strings.Contains(body, `"branch":"feature"`) || !strings.Contains(body, `"ok":true`) {
		t.Fatalf("activity %s", body)
	}
	if body := get(8, "/api/projects/10/git-activity?after=0"); strings.Contains(body, "feature") {
		t.Fatalf("another user saw the push: %s", body)
	}
}

func TestASlowPushDoesNotHoldUpOtherRequests(t *testing.T) {
	fw := newFakeWorkspaces(projectWorkspace(1, 10))
	ps := &fakeProjects{block: make(chan struct{})}
	b := newTestBridge(fw, ps)
	fw.queued[1] = []agent.GitBridgeRequest{{ID: "a1", Op: "push", Branches: []string{"big"}}}
	ctx, cancel := context.WithCancel(context.Background())
	defer stopAll(t, b, cancel)
	defer close(ps.block)
	b.reconcile(ctx)

	time.Sleep(20 * time.Millisecond)
	fw.mu.Lock()
	fw.queued[1] = []agent.GitBridgeRequest{{ID: "a2", Op: "list"}}
	fw.mu.Unlock()
	if res := fw.response(t, "a2"); res.DefaultBranch != "main" {
		t.Fatalf("list result %+v", res)
	}
}
