package gitbridge

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
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

// fakeAgent plays a workspace agent's git bridge.
type fakeAgent struct {
	mu         sync.Mutex
	queued     []agent.GitBridgeRequest
	polls      int
	configured int
	remotes    agent.GitBridgeRemotes
	responses  map[string]agent.GitBridgeResult
	sessions   map[string]string
	pollErr    error
	// instant answers empty polls at once instead of holding them, as a
	// broken or hostile agent might.
	instant bool
}

func newFakeAgent() *fakeAgent {
	return &fakeAgent{responses: map[string]agent.GitBridgeResult{}, sessions: map[string]string{}}
}

func (a *fakeAgent) queue(reqs ...agent.GitBridgeRequest) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.queued = append(a.queued, reqs...)
}

func (a *fakeAgent) GitBridgeRequests(ctx context.Context, session string, wait time.Duration) ([]agent.GitBridgeRequest, error) {
	a.mu.Lock()
	a.polls++
	reqs := a.queued
	a.queued = nil
	err, instant := a.pollErr, a.instant
	a.mu.Unlock()
	if err != nil {
		return nil, err
	}
	if len(reqs) == 0 && !instant {
		select {
		case <-ctx.Done():
		case <-time.After(wait):
		}
	}
	return reqs, nil
}

func (a *fakeAgent) GitBridgeRespond(ctx context.Context, session, id string, result agent.GitBridgeResult) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.responses[id] = result
	a.sessions[id] = session
	return nil
}

func (a *fakeAgent) GitBridgeConfigureRemote(ctx context.Context, remotes agent.GitBridgeRemotes) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.configured++
	a.remotes = remotes
	return nil
}

func (a *fakeAgent) response(t *testing.T, id string) agent.GitBridgeResult {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		a.mu.Lock()
		res, ok := a.responses[id]
		a.mu.Unlock()
		if ok {
			return res
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("no response to %s", id)
	return agent.GitBridgeResult{}
}

func (a *fakeAgent) pollCount() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.polls
}

type fakeWorkspaces struct {
	mu       sync.Mutex
	all      []*workspace.Workspace
	agents   map[int64]*fakeAgent
	resolved map[int64]int
}

func newFakeWorkspaces(all ...*workspace.Workspace) *fakeWorkspaces {
	f := &fakeWorkspaces{all: all, agents: map[int64]*fakeAgent{}, resolved: map[int64]int{}}
	for _, ws := range all {
		f.agents[ws.ID] = newFakeAgent()
	}
	return f
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

// GitBridgeAgent refuses anything but a running project workspace, as the
// workspace service does.
func (f *fakeWorkspaces) GitBridgeAgent(ctx context.Context, userID, workspaceID int64) (*workspace.Workspace, workspace.GitBridgeAgent, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.resolved[workspaceID]++
	for _, ws := range f.all {
		if ws.ID != workspaceID {
			continue
		}
		if !ws.IsProjectWorkspace() {
			return nil, nil, workspace.ErrNotProjectWorkspace
		}
		if ws.Status != workspace.StatusRunning {
			return nil, nil, workspace.ErrNotRunning
		}
		copied := *ws
		return &copied, f.agents[workspaceID], nil
	}
	return nil, nil, workspace.ErrNotFound
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

func (f *fakeWorkspaces) resolveCount(id int64) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.resolved[id]
}

type fakeProjects struct {
	// block, when set, holds every publish until closed.
	block      chan struct{}
	mu         sync.Mutex
	published  []string
	fetched    [][]string
	publishErr error
	listed     []int64
	// upstreamURL is every project's upstream; warning what a stale sync
	// returns; syncErr what a forced sync fails with.
	upstreamURL     string
	warning         string
	syncErr         error
	synced          int
	upstreamFetched [][]string
}

func (f *fakeProjects) Get(ctx context.Context, userID, projectID int64) (*project.Project, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return &project.Project{ID: projectID, UserID: userID, DefaultBranch: "main", UpstreamURL: f.upstreamURL}, nil
}

func (f *fakeProjects) SyncUpstream(ctx context.Context, userID, projectID int64) (*project.UpstreamStatus, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.synced++
	if f.syncErr != nil {
		return nil, f.syncErr
	}
	return &project.UpstreamStatus{}, nil
}

func (f *fakeProjects) SyncUpstreamIfStale(ctx context.Context, userID, projectID int64, wait time.Duration) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.warning
}

func (f *fakeProjects) UpstreamBranches(ctx context.Context, userID, projectID int64) (*agent.RepoRefs, error) {
	return &agent.RepoRefs{DefaultBranch: "main", Branches: []agent.RepoRef{{Name: "main", SHA: "ccc"}, {Name: "theirs", SHA: "ddd"}}}, nil
}

func (f *fakeProjects) FetchWorkspaceUpstreamBranches(ctx context.Context, userID, projectID int64, branches []string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.upstreamFetched = append(f.upstreamFetched, branches)
	return nil
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
		select {
		case <-f.block:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
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
	b.minPollGap = 0
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

// captureLog collects the standard logger's output for the test.
func captureLog(t *testing.T) *syncBuffer {
	t.Helper()
	buf := &syncBuffer{}
	prev := log.Writer()
	log.SetOutput(buf)
	t.Cleanup(func() { log.SetOutput(prev) })
	return buf
}

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
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
	for id, a := range fw.agents {
		if id != 1 && a.pollCount() != 0 {
			t.Errorf("workspace %d was polled", id)
		}
	}
	fw.agents[1].mu.Lock()
	if fw.agents[1].configured == 0 {
		t.Error("the devpad remote was never set up")
	}
	fw.agents[1].mu.Unlock()

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
	a := fw.agents[1]
	a.pollErr = errors.New("connection refused")
	captureLog(t)
	b := newTestBridge(fw, &fakeProjects{})
	ctx, cancel := context.WithCancel(context.Background())
	defer stopAll(t, b, cancel)
	b.reconcile(ctx)
	time.Sleep(20 * time.Millisecond)

	// The agent comes back and a request gets through.
	a.mu.Lock()
	a.pollErr = nil
	a.mu.Unlock()
	a.queue(agent.GitBridgeRequest{ID: "aa", Op: "list"})
	if res := a.response(t, "aa"); res.Error != "" || res.DefaultBranch != "main" {
		t.Fatalf("list result %+v", res)
	}
}

// An agent that answers every poll at once must not make devpad spin: polls
// are paced on devpad's side, and the workspace record is not re-read on
// every empty poll.
func TestAnInstantAgentDoesNotMakeDevpadSpin(t *testing.T) {
	fw := newFakeWorkspaces(projectWorkspace(1, 10))
	a := fw.agents[1]
	a.instant = true
	b := newTestBridge(fw, &fakeProjects{})
	b.pollWait = time.Second
	b.minPollGap = 10 * time.Millisecond
	b.backoffMin = 10 * time.Millisecond
	b.backoffMax = 80 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	b.reconcile(ctx)
	time.Sleep(400 * time.Millisecond)
	stopAll(t, b, cancel)

	// Unpaced, this is many thousands of polls.
	if n := a.pollCount(); n > 20 || n == 0 {
		t.Fatalf("%d polls in 400ms", n)
	}
	if n := fw.resolveCount(1); n > 2 {
		t.Fatalf("the workspace was looked up %d times for empty polls", n)
	}
}

func TestPushesGoThroughThePublishRules(t *testing.T) {
	ps := &fakeProjects{}
	b := newTestBridge(newFakeWorkspaces(), ps)
	captureLog(t)
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
		{project.ErrPublishProtected, "merge request"},
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

	// Malformed requests are refused before any work.
	ps.publishErr = nil
	for _, req := range []agent.GitBridgeRequest{
		{Op: "push"},
		{Op: "push", Branches: []string{"a", "b"}},
		{Op: "push", Branches: []string{"--all"}},
		{Op: "push", Branches: []string{strings.Repeat("x", maxBranchName+1)}},
		{Op: "fetch"},
		{Op: "fetch", Branches: []string{"main", "../x"}},
		{Op: "fetch", Branches: make([]string, maxBranches+1)},
		{Op: "delete", Branches: []string{"feature"}},
	} {
		if res := b.handle(ctx, ws, req); res.Error == "" {
			t.Errorf("request %+v was accepted", req)
		}
	}
	if len(ps.published) != 6 || len(ps.fetched) != 0 {
		t.Fatalf("malformed requests reached the project: published %v fetched %v", ps.published, ps.fetched)
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
	a := fw.agents[1]
	ps := &fakeProjects{}
	b := newTestBridge(fw, ps)
	a.queue(
		agent.GitBridgeRequest{ID: "../../api/file", Op: "push", Branches: []string{"x"}},
		agent.GitBridgeRequest{ID: "bb", Op: "push", Branches: []string{"y"}},
	)
	ctx, cancel := context.WithCancel(context.Background())
	defer stopAll(t, b, cancel)
	b.reconcile(ctx)
	a.response(t, "bb")
	ps.mu.Lock()
	defer ps.mu.Unlock()
	if len(ps.published) != 1 || !strings.HasSuffix(ps.published[0], ":y") {
		t.Fatalf("published %v", ps.published)
	}
}

// A poll may carry at most a handful of requests, and a workspace only so
// many requests and fetched branches a minute; the rest are refused without
// any work.
func TestRequestLimitsAreEnforcedByDevpad(t *testing.T) {
	ps := &fakeProjects{}
	fw := newFakeWorkspaces(projectWorkspace(1, 10))
	a := fw.agents[1]
	b := newTestBridge(fw, ps)
	at := time.Unix(1000, 0)
	b.now = func() time.Time { return at }
	st := &pollState{
		ws:       projectWorkspace(1, 10),
		agent:    a,
		session:  "s",
		requests: newBucket(requestBurst, requestsPerMinute, at),
		branches: newBucket(branchBurst, branchesPerMinute, at),
	}
	ctx := context.Background()
	slots := make(chan struct{}, maxConcurrent)

	var ids []string
	for i := 0; i < requestBurst+4; i++ {
		id := fmt.Sprintf("%02x", i)
		ids = append(ids, id)
		b.dispatch(ctx, st, slots, []agent.GitBridgeRequest{{ID: id, Op: "list"}})
	}
	b.wg.Wait()
	refused := 0
	for _, id := range ids {
		if a.response(t, id).Error == errTooMany {
			refused++
		}
	}
	if refused != 4 {
		t.Fatalf("%d requests refused past the burst of %d, want 4", refused, requestBurst)
	}

	// Branches are counted too.
	st.requests = newBucket(1000, 1000, at)
	branches := make([]string, maxBranches)
	for i := range branches {
		branches[i] = fmt.Sprintf("b%d", i)
	}
	var fetches []string
	for i := 0; i < branchBurst/maxBranches+1; i++ {
		id := fmt.Sprintf("f%x", i)
		fetches = append(fetches, id)
		b.dispatch(ctx, st, slots, []agent.GitBridgeRequest{{ID: id, Op: "fetch", Branches: branches}})
	}
	b.wg.Wait()
	if res := a.response(t, fetches[len(fetches)-1]); res.Error != errTooManyRefs {
		t.Fatalf("the fetch past the branch limit got %+v", res)
	}
	if len(ps.fetched) != branchBurst/maxBranches {
		t.Fatalf("%d fetches ran, want %d", len(ps.fetched), branchBurst/maxBranches)
	}
}

func TestPollerTakesAtMostAHandfulOfRequestsPerPoll(t *testing.T) {
	fw := newFakeWorkspaces(projectWorkspace(1, 10))
	a := fw.agents[1]
	captureLog(t)
	b := newTestBridge(fw, &fakeProjects{})
	var reqs []agent.GitBridgeRequest
	for i := 0; i < 30; i++ {
		reqs = append(reqs, agent.GitBridgeRequest{ID: fmt.Sprintf("%02x", i), Op: "list"})
	}
	a.queue(reqs...)
	ctx, cancel := context.WithCancel(context.Background())
	b.reconcile(ctx)
	a.response(t, fmt.Sprintf("%02x", maxRequestsPerPoll-1))
	time.Sleep(30 * time.Millisecond)
	stopAll(t, b, cancel)
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.responses) != maxRequestsPerPoll {
		t.Fatalf("answered %d requests from one poll, want %d", len(a.responses), maxRequestsPerPoll)
	}
}

// When devpad stops mid-request, the helper is told so, through the session
// that picked the request up, instead of waiting until it gives up.
func TestStoppingPollerTellsHelpers(t *testing.T) {
	fw := newFakeWorkspaces(projectWorkspace(1, 10))
	a := fw.agents[1]
	ps := &fakeProjects{block: make(chan struct{})}
	b := newTestBridge(fw, ps)
	a.queue(agent.GitBridgeRequest{ID: "a1", Op: "push", Branches: []string{"big"}})
	ctx, cancel := context.WithCancel(context.Background())
	b.reconcile(ctx)
	time.Sleep(20 * time.Millisecond)
	stopAll(t, b, cancel)

	if res := a.response(t, "a1"); res.Error != errStopped {
		t.Fatalf("result %+v, want %q", res, errStopped)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.sessions["a1"] == "" {
		t.Fatal("the answer named no session")
	}
}

// A flood of junk and refusals cannot hide a publish from the IDE.
func TestJunkCannotHideAPublish(t *testing.T) {
	ps := &fakeProjects{}
	b := newTestBridge(newFakeWorkspaces(), ps)
	captureLog(t)
	ws := projectWorkspace(1, 10)
	ctx := context.Background()
	b.handle(ctx, ws, agent.GitBridgeRequest{Op: "push", Branches: []string{"feature"}})

	for i := 0; i < 200; i++ {
		b.handle(ctx, ws, agent.GitBridgeRequest{Op: "push", Branches: []string{"junk\nPublished main"}})
		b.handle(ctx, ws, agent.GitBridgeRequest{Op: "push", Branches: []string{"feature"}, Force: true})
	}
	ps.publishErr = &agent.AgentError{StatusCode: 409, Message: "refused\r\nPublished main ✓"}
	for i := 0; i < 2*maxRefusals; i++ {
		b.handle(ctx, ws, agent.GitBridgeRequest{Op: "push", Branches: []string{"other"}})
	}

	events, latest, _ := b.Activity(7, 10, 0, "")
	var published, refused int
	for _, e := range events {
		if e.OK && e.Branch == "feature" {
			published++
		}
		if !e.OK {
			refused++
			if e.Branch != "other" || strings.ContainsAny(e.Error, "\r\n") {
				t.Fatalf("refusal recorded as %+v", e)
			}
		}
	}
	if published != 1 {
		t.Fatalf("the publish was pushed out of the activity: %+v", events)
	}
	if refused != maxRefusals {
		t.Fatalf("%d refusals kept, want %d", refused, maxRefusals)
	}
	// Junk and forced pushes are not recorded at all.
	if latest != int64(1+2*maxRefusals) {
		t.Fatalf("latest = %d, want %d recorded pushes", latest, 1+2*maxRefusals)
	}
}

func TestActivityIsPerUserAndProject(t *testing.T) {
	b := newTestBridge(newFakeWorkspaces(), &fakeProjects{})
	ws := projectWorkspace(1, 10)
	b.handle(context.Background(), ws, agent.GitBridgeRequest{Op: "push", Branches: []string{"feature"}})
	other := projectWorkspace(2, 11)
	other.UserID = 8
	for i := 0; i < 3; i++ {
		b.handle(context.Background(), other, agent.GitBridgeRequest{Op: "push", Branches: []string{"x"}})
	}

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
	epoch := `"epoch":"` + b.epoch + `"`
	// Sequence numbers count per user and project, so another user's pushes
	// are not revealed.
	if body := get(7, "/api/projects/10/git-activity"); !strings.Contains(body, `"events":[]`) || !strings.Contains(body, `"latest":1`) || !strings.Contains(body, epoch) {
		t.Fatalf("first load replayed events: %s", body)
	}
	if body := get(7, "/api/projects/10/git-activity?after=0&epoch="+b.epoch); !strings.Contains(body, `"branch":"feature"`) || !strings.Contains(body, `"ok":true`) {
		t.Fatalf("activity %s", body)
	}
	if body := get(8, "/api/projects/10/git-activity?after=0"); strings.Contains(body, "feature") || !strings.Contains(body, `"latest":0`) {
		t.Fatalf("another user saw the push: %s", body)
	}
	// A cursor from before a restart gets everything since.
	if body := get(7, "/api/projects/10/git-activity?after=5&epoch=stale"); !strings.Contains(body, `"branch":"feature"`) {
		t.Fatalf("a stale cursor missed events: %s", body)
	}
	if body := get(7, "/api/projects/10/git-activity?after=5&epoch="+b.epoch); strings.Contains(body, `"branch":"feature"`) {
		t.Fatalf("a current cursor replayed events: %s", body)
	}
}

func TestReconcileForgetsDeletedProjects(t *testing.T) {
	fw := newFakeWorkspaces(projectWorkspace(1, 10))
	b := newTestBridge(fw, &fakeProjects{})
	b.handle(context.Background(), projectWorkspace(1, 10), agent.GitBridgeRequest{Op: "push", Branches: []string{"feature"}})
	b.handle(context.Background(), projectWorkspace(2, 11), agent.GitBridgeRequest{Op: "push", Branches: []string{"feature"}})
	ctx, cancel := context.WithCancel(context.Background())
	defer stopAll(t, b, cancel)
	b.reconcile(ctx)
	if events, _, _ := b.Activity(7, 10, 0, ""); len(events) != 1 {
		t.Fatalf("a live project lost its activity: %v", events)
	}
	if events, _, _ := b.Activity(7, 11, 0, ""); len(events) != 0 {
		t.Fatalf("a deleted project kept its activity: %v", events)
	}
}

func TestASlowPushDoesNotHoldUpOtherRequests(t *testing.T) {
	fw := newFakeWorkspaces(projectWorkspace(1, 10))
	a := fw.agents[1]
	ps := &fakeProjects{block: make(chan struct{})}
	b := newTestBridge(fw, ps)
	a.queue(agent.GitBridgeRequest{ID: "a1", Op: "push", Branches: []string{"big"}})
	ctx, cancel := context.WithCancel(context.Background())
	defer stopAll(t, b, cancel)
	defer close(ps.block)
	b.reconcile(ctx)

	time.Sleep(20 * time.Millisecond)
	a.queue(agent.GitBridgeRequest{ID: "a2", Op: "list"})
	if res := a.response(t, "a2"); res.DefaultBranch != "main" {
		t.Fatalf("list result %+v", res)
	}
}

// An agent from before the bridge is reported once, not on every retry.
func TestAnOldAgentIsLoggedOnce(t *testing.T) {
	fw := newFakeWorkspaces(projectWorkspace(1, 10))
	fw.agents[1].pollErr = &agent.AgentError{StatusCode: 404, Message: "not found"}
	logs := captureLog(t)
	b := newTestBridge(fw, &fakeProjects{})
	ctx, cancel := context.WithCancel(context.Background())
	b.reconcile(ctx)
	time.Sleep(60 * time.Millisecond)
	stopAll(t, b, cancel)
	if fw.agents[1].pollCount() < 3 {
		t.Fatalf("only %d polls", fw.agents[1].pollCount())
	}
	if n := strings.Count(logs.String(), "has no git bridge"); n != 1 {
		t.Fatalf("logged %d times:\n%s", n, logs)
	}
}

// Text from the workspace is made safe to log, and a workspace cannot flood
// the log.
func TestBridgeLogsAreCleanAndLimited(t *testing.T) {
	logs := captureLog(t)
	b := newTestBridge(newFakeWorkspaces(), &fakeProjects{})
	at := time.Unix(1000, 0)
	b.now = func() time.Time { return at }
	for i := 0; i < 100; i++ {
		b.logf(1, "agent said: %s", "line one\nfake: git bridge: workspace 2: injected\x1b[31m"+strings.Repeat("z", 1000))
	}
	out := logs.String()
	if lines := strings.Count(out, "\n"); lines != logBurst {
		t.Fatalf("%d lines logged, want %d:\n%s", lines, logBurst, out)
	}
	if strings.Contains(out, "\x1b") || strings.Contains(out, "\nfake") {
		t.Fatalf("control characters reached the log:\n%q", out)
	}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if len(line) > maxLogText*4+200 {
			t.Fatalf("an unbounded line was logged (%d bytes)", len(line))
		}
	}

	// Once the limit refills, the next line says how many were dropped.
	at = at.Add(time.Minute)
	b.logf(1, "later")
	if !strings.Contains(logs.String(), "later (90 earlier lines suppressed)") {
		t.Fatalf("suppressed lines not counted:\n%s", logs)
	}
}

func TestRemotesAreSetUpForTheProjectsUpstream(t *testing.T) {
	for _, upstream := range []string{"", "git@github.com:example/project.git"} {
		fw := newFakeWorkspaces(projectWorkspace(1, 10))
		b := newTestBridge(fw, &fakeProjects{upstreamURL: upstream})
		ctx, cancel := context.WithCancel(context.Background())
		b.reconcile(ctx)
		deadline := time.Now().Add(2 * time.Second)
		for {
			a := fw.agents[1]
			a.mu.Lock()
			configured, remotes := a.configured, a.remotes
			a.mu.Unlock()
			if configured > 0 {
				if remotes.Upstream != (upstream != "") {
					t.Errorf("upstream %q: remotes %+v", upstream, remotes)
				}
				// The workspace gets a hash of the URL, never the URL.
				if upstream != "" && (len(remotes.UpstreamURLHash) != 64 || strings.Contains(remotes.UpstreamURLHash, "github")) {
					t.Errorf("upstream hash %q", remotes.UpstreamURLHash)
				}
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("the remotes were never set up")
			}
			time.Sleep(2 * time.Millisecond)
		}
		stopAll(t, b, cancel)
	}
}

func TestOriginIsTheReadOnlyUpstream(t *testing.T) {
	ps := &fakeProjects{upstreamURL: "git@github.com:example/project.git"}
	b := newTestBridge(newFakeWorkspaces(), ps)
	captureLog(t)
	ws := projectWorkspace(1, 10)
	ctx := context.Background()
	up := func(op string, branches ...string) agent.GitBridgeResult {
		return b.handle(ctx, ws, agent.GitBridgeRequest{ID: "u1", Op: op, Branches: branches, Remote: agent.GitBridgeUpstream})
	}

	// Listing origin syncs, then lists the upstream's branches.
	if res := up("list"); res.Error != "" || len(res.Refs) != 2 || res.Refs[1].Name != "theirs" || ps.synced != 1 {
		t.Fatalf("list result %+v, synced %d", res, ps.synced)
	}
	if res := up("fetch", "theirs"); res.Error != "" || len(ps.upstreamFetched) != 1 || ps.upstreamFetched[0][0] != "theirs" {
		t.Fatalf("fetch result %+v, fetched %v", res, ps.upstreamFetched)
	}
	if len(ps.fetched) != 0 {
		t.Fatal("an origin fetch reached the devpad remote")
	}
	// Pushes are refused, and are not recorded as the IDE's pushes.
	if res := up("push", "main"); !strings.Contains(res.Error, "read-only") {
		t.Fatalf("push result %+v", res)
	}
	if events, _, _ := b.Activity(7, 10, 0, ""); len(events) != 0 || len(ps.published) != 0 {
		t.Fatalf("an origin push was recorded: %v %v", events, ps.published)
	}
	for _, req := range []agent.GitBridgeRequest{
		{Op: "fetch", Remote: agent.GitBridgeUpstream},
		{Op: "fetch", Remote: agent.GitBridgeUpstream, Branches: []string{"--all"}},
		{Op: "list", Remote: "elsewhere"},
	} {
		if res := b.handle(ctx, ws, req); res.Error == "" {
			t.Errorf("request %+v was accepted", req)
		}
	}

	// A failed sync fails the list without its detail.
	ps.syncErr = &agent.AgentError{StatusCode: 502, Message: "fetching from upstream: Permission denied (publickey) for /home/devpad/.ssh/id_ed25519"}
	if res := up("list"); res.Error != errSync {
		t.Fatalf("failed sync result %+v", res)
	}
	ps.syncErr = project.ErrNoUpstream
	if res := up("list"); !strings.Contains(res.Error, "no upstream") {
		t.Fatalf("no upstream result %+v", res)
	}
}

func TestListingDevpadWarnsOfAFailedSync(t *testing.T) {
	ps := &fakeProjects{warning: "could not fetch from the upstream"}
	b := newTestBridge(newFakeWorkspaces(), ps)
	res := b.handle(context.Background(), projectWorkspace(1, 10), agent.GitBridgeRequest{Op: "list"})
	if res.Error != "" || res.Warning != ps.warning || len(res.Refs) != 1 {
		t.Fatalf("list result %+v", res)
	}
}
