package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestMain lets the test binary stand in for git-remote-devpad: git runs a
// wrapper script that execs this binary with the variable set.
func TestMain(m *testing.M) {
	if os.Getenv("DEVPAD_TEST_REMOTE_HELPER") == "1" {
		os.Exit(runRemoteHelper(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
	}
	os.Exit(m.Run())
}

// helperPath puts a git-remote-devpad wrapper for the test binary first on
// PATH.
func helperPath(t *testing.T) string {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	script := "#!/bin/sh\nDEVPAD_TEST_REMOTE_HELPER=1 exec '" + exe + "' \"$@\"\n"
	if err := os.WriteFile(filepath.Join(dir, remoteHelperName), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir + string(os.PathListSeparator) + os.Getenv("PATH")
}

// startBridge serves a fresh bridge on a socket the helper finds through
// DEVPAD_GIT_SOCKET.
func startBridge(t *testing.T) *gitBridge {
	t.Helper()
	b := newGitBridge()
	// Unix socket paths are short-limited, so avoid the long test temp dir.
	dir, err := os.MkdirTemp("", "gb")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	socket := filepath.Join(dir, "git.sock")
	srv, err := serveGitBridgeSocket(b, socket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { srv.Shutdown(context.Background()) })
	t.Setenv("DEVPAD_GIT_SOCKET", socket)
	return b
}

// fakeHost plays devpad: it polls the bridge and serves requests against a
// canonical repo, publishing fast-forward only and refusing main, the way
// devpad's project service does.
type fakeHost struct {
	t         *testing.T
	canonical string
	work      string
	mu        sync.Mutex
	pushes    []string
	fetches   []int
	// lists records, for each list of the devpad remote, whether it was
	// the list before a push.
	lists []bool
	// warning is added to each list of the devpad remote.
	warning string
}

func (h *fakeHost) run(ctx context.Context, b *gitBridge) {
	for ctx.Err() == nil {
		for _, req := range b.poll(ctx, "host", time.Second) {
			b.complete("host", req.ID, h.serve(req))
		}
	}
}

func gitIn(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

func (h *fakeHost) serve(req gitBridgeRequest) gitBridgeResult {
	if req.Remote == remoteUpstream {
		return h.serveUpstream(req)
	}
	switch req.Op {
	case "list":
		h.mu.Lock()
		h.lists = append(h.lists, req.ForPush)
		h.mu.Unlock()
		out, err := gitIn(h.canonical, "for-each-ref", "--format=%(refname:strip=2) %(objectname)", "refs/heads/")
		if err != nil {
			return gitBridgeResult{Error: out}
		}
		res := gitBridgeResult{DefaultBranch: "main", Warning: h.warning}
		for _, line := range strings.Split(out, "\n") {
			name, sha, _ := strings.Cut(line, " ")
			res.Refs = append(res.Refs, repoRef{Name: name, SHA: sha})
		}
		return res
	case "fetch":
		h.mu.Lock()
		h.fetches = append(h.fetches, len(req.Branches))
		h.mu.Unlock()
		// devpad refuses wider fetches, as here.
		if len(req.Branches) > gitBridgeMaxBranches {
			return gitBridgeResult{Error: "too many branches"}
		}
		for _, branch := range req.Branches {
			bundle := filepath.Join(h.t.TempDir(), "b.bundle")
			if out, err := gitIn(h.canonical, "bundle", "create", bundle, "refs/heads/"+branch); err != nil {
				return gitBridgeResult{Error: out}
			}
			if out, err := gitIn(h.work, "fetch", "--no-tags", "--force", bundle, "refs/heads/"+branch+":refs/remotes/devpad/"+branch); err != nil {
				return gitBridgeResult{Error: out}
			}
		}
		return gitBridgeResult{}
	case "push":
		branch := req.Branches[0]
		h.mu.Lock()
		h.pushes = append(h.pushes, branch)
		h.mu.Unlock()
		if branch == "main" || req.Force {
			return gitBridgeResult{Error: "refused"}
		}
		bundle := filepath.Join(h.t.TempDir(), "p.bundle")
		if out, err := gitIn(h.work, "bundle", "create", bundle, "refs/heads/"+branch); err != nil {
			return gitBridgeResult{Error: out}
		}
		// A fetch without + only fast-forwards.
		if out, err := gitIn(h.canonical, "fetch", bundle, "refs/heads/"+branch+":refs/heads/"+branch); err != nil {
			return gitBridgeResult{Error: "not a fast-forward: " + out}
		}
		sha, _ := gitIn(h.canonical, "rev-parse", branch)
		return gitBridgeResult{SHA: sha}
	}
	return gitBridgeResult{Error: "unknown op"}
}

// serveUpstream answers the origin remote from the canonical repo's copy of
// the upstream, the way devpad does after syncing.
func (h *fakeHost) serveUpstream(req gitBridgeRequest) gitBridgeResult {
	switch req.Op {
	case "list":
		out, err := gitIn(h.canonical, "for-each-ref", "--format=%(refname:strip=3) %(objectname)", upstreamRefPrefix)
		if err != nil {
			return gitBridgeResult{Error: out}
		}
		res := gitBridgeResult{DefaultBranch: "main"}
		for _, line := range strings.Split(out, "\n") {
			if name, sha, ok := strings.Cut(line, " "); ok {
				res.Refs = append(res.Refs, repoRef{Name: name, SHA: sha})
			}
		}
		return res
	case "fetch":
		for _, branch := range req.Branches {
			bundle := filepath.Join(h.t.TempDir(), "u.bundle")
			if out, err := gitIn(h.canonical, "bundle", "create", bundle, upstreamRefPrefix+branch); err != nil {
				return gitBridgeResult{Error: out}
			}
			if out, err := gitIn(h.work, "fetch", "--no-tags", "--force", bundle, upstreamRefPrefix+branch+":refs/remotes/origin/"+branch); err != nil {
				return gitBridgeResult{Error: out}
			}
		}
		return gitBridgeResult{}
	}
	h.mu.Lock()
	h.pushes = append(h.pushes, "upstream:"+strings.Join(req.Branches, ","))
	h.mu.Unlock()
	return gitBridgeResult{Error: "unexpected upstream " + req.Op}
}

type bridgeRig struct {
	canonical, work, path string
	host                  *fakeHost
}

func newBridgeRig(t *testing.T) *bridgeRig {
	t.Helper()
	canonical := initCanonical(t)
	work := seedSandbox(t, canonical, "main")
	withWorkspaceRoot(t, work)
	t.Setenv("DEVPAD_WORKSPACE_ROOT", work)

	remoteHelperDir = t.TempDir()
	t.Cleanup(func() { remoteHelperDir = "" })
	rec := httptest.NewRecorder()
	handleGitBridgeRemote(rec, httptest.NewRequest(http.MethodPost, "/api/git-bridge/remote", nil))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("configuring the remote: %d %s", rec.Code, rec.Body)
	}

	b := startBridge(t)
	host := &fakeHost{t: t, canonical: canonical, work: work}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { host.run(ctx, b); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	// The bridge only accepts requests once devpad polls.
	deadline := time.Now().Add(5 * time.Second)
	for {
		b.mu.Lock()
		ok := b.serving()
		b.mu.Unlock()
		if ok || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	return &bridgeRig{canonical: canonical, work: work, path: helperPath(t), host: host}
}

// git runs git in the workspace with the helper on PATH.
func (r *bridgeRig) git(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = r.work
	cmd.Env = append(os.Environ(), "PATH="+r.path,
		"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.com")
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

func TestGitBridgeRemoteIsConfigured(t *testing.T) {
	rig := newBridgeRig(t)
	if got := runGit(t, rig.work, "config", "remote.devpad.url"); got != devpadRemoteURL {
		t.Fatalf("remote url = %q", got)
	}
	if got := runGit(t, rig.work, "config", "remote.devpad.fetch"); got != "+refs/heads/*:refs/remotes/devpad/*" {
		t.Fatalf("remote fetch = %q", got)
	}
	if got := runGit(t, rig.work, "config", "remote.devpad.push"); got != "HEAD" {
		t.Fatalf("remote push = %q", got)
	}
	exe, _ := os.Executable()
	exe, _ = filepath.EvalSymlinks(exe)
	if target, err := os.Readlink(filepath.Join(remoteHelperDir, remoteHelperName)); err != nil || target != exe {
		t.Fatalf("helper link = %q, %v", target, err)
	}

	// Running it again changes nothing.
	rec := httptest.NewRecorder()
	handleGitBridgeRemote(rec, httptest.NewRequest(http.MethodPost, "/api/git-bridge/remote", nil))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("second configure: %d %s", rec.Code, rec.Body)
	}
}

func TestGitBridgeRemoteLeavesAnotherDevpadRemoteAlone(t *testing.T) {
	canonical := initCanonical(t)
	work := seedSandbox(t, canonical, "main")
	runGit(t, work, "remote", "add", "devpad", "https://example.com/x.git")
	withWorkspaceRoot(t, work)
	remoteHelperDir = t.TempDir()
	t.Cleanup(func() { remoteHelperDir = "" })

	rec := httptest.NewRecorder()
	handleGitBridgeRemote(rec, httptest.NewRequest(http.MethodPost, "/api/git-bridge/remote", nil))
	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d %s", rec.Code, rec.Body)
	}
	if got := runGit(t, work, "config", "remote.devpad.url"); got != "https://example.com/x.git" {
		t.Fatalf("remote was changed to %q", got)
	}
}

func TestGitPushDevpadPublishesThroughTheHost(t *testing.T) {
	rig := newBridgeRig(t)
	runGit(t, rig.work, "checkout", "-b", "feature")
	sha := commitFile(t, rig.work, "a.txt", "a")

	if out, err := rig.git(t, "push", "devpad", "feature"); err != nil {
		t.Fatalf("push: %v\n%s", err, out)
	}
	if got := runGit(t, rig.canonical, "rev-parse", "feature"); got != sha {
		t.Fatalf("canonical feature = %s, want %s", got, sha)
	}
	// git records the push in the remote-tracking ref.
	if got := runGit(t, rig.work, "rev-parse", "refs/remotes/devpad/feature"); got != sha {
		t.Fatalf("devpad/feature = %s, want %s", got, sha)
	}
	// The first push has the branch track devpad.
	if got := runGit(t, rig.work, "config", "branch.feature.remote"); got != "devpad" {
		t.Fatalf("feature tracks %q", got)
	}
	// The list before a push says so, so devpad does not sync for it.
	rig.host.mu.Lock()
	lists := slices.Clone(rig.host.lists)
	rig.host.lists = nil
	rig.host.mu.Unlock()
	if !slices.Equal(lists, []bool{true}) {
		t.Fatalf("lists before the push: %v", lists)
	}
	if out, err := rig.git(t, "fetch", "devpad"); err != nil {
		t.Fatalf("fetch: %v\n%s", err, out)
	}
	rig.host.mu.Lock()
	lists = slices.Clone(rig.host.lists)
	rig.host.mu.Unlock()
	if !slices.Equal(lists, []bool{false}) {
		t.Fatalf("lists for a fetch: %v", lists)
	}

	// A plain `git push devpad` pushes the current branch.
	sha = commitFile(t, rig.work, "b.txt", "b")
	if out, err := rig.git(t, "push", "devpad"); err != nil {
		t.Fatalf("second push: %v\n%s", err, out)
	}
	if got := runGit(t, rig.canonical, "rev-parse", "feature"); got != sha {
		t.Fatalf("canonical feature = %s after the second push, want %s", got, sha)
	}
}

func TestGitPushDevpadRefusals(t *testing.T) {
	rig := newBridgeRig(t)
	runGit(t, rig.work, "checkout", "-b", "feature")
	first := commitFile(t, rig.work, "a.txt", "a")
	if out, err := rig.git(t, "push", "devpad", "feature"); err != nil {
		t.Fatalf("push: %v\n%s", err, out)
	}

	cases := []struct {
		name string
		args []string
		want string
	}{
		{"force", []string{"push", "--force", "devpad", "feature"}, "refuses force pushes"},
		{"rename", []string{"push", "devpad", "feature:other"}, "under its own name"},
		{"delete", []string{"push", "devpad", ":feature"}, "does not delete"},
		{"tag", []string{"push", "devpad", "refs/tags/v1"}, "only accepts branches"},
		{"host refusal", []string{"push", "devpad", "main"}, "refused"},
	}
	runGit(t, rig.work, "tag", "v1")
	// main gets a commit to push, so the host is asked about it.
	runGit(t, rig.work, "checkout", "-q", "main")
	commitFile(t, rig.work, "m.txt", "m")
	runGit(t, rig.work, "checkout", "-q", "feature")
	runGit(t, rig.work, "reset", "--hard", "HEAD~1")
	commitFile(t, rig.work, "c.txt", "c")
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := rig.git(t, tc.args...)
			if err == nil {
				t.Fatalf("expected the push to fail:\n%s", out)
			}
			if !strings.Contains(out, tc.want) {
				t.Fatalf("expected %q in:\n%s", tc.want, out)
			}
		})
	}
	// A rewritten branch is not a fast-forward, and the host refuses it.
	out, err := rig.git(t, "push", "devpad", "feature")
	if err == nil || !(strings.Contains(out, "not a fast-forward") || strings.Contains(out, "non-fast-forward")) {
		t.Fatalf("expected a non-fast-forward refusal: %v\n%s", err, out)
	}
	if got := runGit(t, rig.canonical, "rev-parse", "feature"); got != first {
		t.Fatalf("a refused push moved feature to %s", got)
	}

	// Force, rename, delete and tag pushes never reach the host.
	rig.host.mu.Lock()
	defer rig.host.mu.Unlock()
	for _, branch := range rig.host.pushes {
		if branch != "feature" && branch != "main" {
			t.Fatalf("host saw a push for %q", branch)
		}
	}
}

func TestGitFetchDevpadUpdatesRemoteTrackingRefs(t *testing.T) {
	rig := newBridgeRig(t)
	// Another branch lands in the canonical repo.
	other := seedSandbox(t, rig.canonical, "agent-work")
	sha := commitFile(t, other, "x.txt", "x")
	runGit(t, other, "bundle", "create", filepath.Join(other, "../x.bundle"), "refs/heads/agent-work")
	runGit(t, rig.canonical, "fetch", filepath.Join(other, "../x.bundle"), "refs/heads/agent-work:refs/heads/agent-work")

	if out, err := rig.git(t, "fetch", "devpad"); err != nil {
		t.Fatalf("fetch: %v\n%s", err, out)
	}
	if got := runGit(t, rig.work, "rev-parse", "refs/remotes/devpad/agent-work"); got != sha {
		t.Fatalf("devpad/agent-work = %s, want %s", got, sha)
	}
	if out, err := rig.git(t, "ls-remote", "devpad"); err != nil || !strings.Contains(out, "refs/heads/agent-work") {
		t.Fatalf("ls-remote: %v\n%s", err, out)
	}
}

func TestGitBridgeRefusesOtherRepos(t *testing.T) {
	rig := newBridgeRig(t)
	elsewhere := t.TempDir()
	runGit(t, elsewhere, "init", "-q")
	runGit(t, elsewhere, "remote", "add", "devpad", devpadRemoteURL)
	cmd := exec.Command("git", "fetch", "devpad")
	cmd.Dir = elsewhere
	cmd.Env = append(os.Environ(), "PATH="+rig.path)
	out, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(out), "only works in the project repository") {
		t.Fatalf("expected a refusal: %v\n%s", err, out)
	}
}

func TestGitBridgeFailsFastWhenDevpadIsNotPolling(t *testing.T) {
	canonical := initCanonical(t)
	work := seedSandbox(t, canonical, "main")
	withWorkspaceRoot(t, work)
	t.Setenv("DEVPAD_WORKSPACE_ROOT", work)
	runGit(t, work, "remote", "add", "devpad", devpadRemoteURL)
	startBridge(t)

	cmd := exec.Command("git", "fetch", "devpad")
	cmd.Dir = work
	cmd.Env = append(os.Environ(), "PATH="+helperPath(t))
	start := time.Now()
	out, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(out), "not serving git requests") {
		t.Fatalf("expected a not-serving error: %v\n%s", err, out)
	}
	if time.Since(start) > 10*time.Second {
		t.Fatalf("the refusal took %s", time.Since(start))
	}
}

func TestGitBridgeQueueLimitsAndDroppedHelpers(t *testing.T) {
	b := newGitBridge()
	b.lastSeen = time.Now()

	// A helper that goes away is forgotten, and its late result discarded.
	ctx, cancel := context.WithCancel(context.Background())
	errc := make(chan error, 1)
	go func() {
		_, err := b.submit(ctx, gitBridgeRequest{Op: "list"})
		errc <- err
	}()
	reqs := b.poll(context.Background(), "s1", time.Second)
	if len(reqs) != 1 {
		t.Fatalf("polled %d requests", len(reqs))
	}
	cancel()
	if err := <-errc; err == nil {
		t.Fatal("expected the cancelled submit to fail")
	}
	if b.complete("s1", reqs[0].ID, gitBridgeResult{}) {
		t.Fatal("a result for a dropped request was delivered")
	}

	// Outstanding requests are capped.
	b.lastSeen = time.Now()
	block, stop := context.WithCancel(context.Background())
	defer stop()
	for i := 0; i < gitBridgeMaxOutstanding; i++ {
		go b.submit(block, gitBridgeRequest{Op: "list"})
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		b.mu.Lock()
		n := len(b.pending)
		b.mu.Unlock()
		if n == gitBridgeMaxOutstanding || time.Now().After(deadline) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if _, err := b.submit(context.Background(), gitBridgeRequest{Op: "list"}); err == nil || !strings.Contains(err.Error(), "too many") {
		t.Fatalf("expected the queue to be full, got %v", err)
	}
}

func TestGitBridgeHTTPEndpoints(t *testing.T) {
	b := newGitBridge()
	b.lastSeen = time.Now()
	type answer struct {
		res gitBridgeResult
		err error
	}
	ans := make(chan answer, 1)
	go func() {
		res, err := b.submit(context.Background(), gitBridgeRequest{Op: "push", Branches: []string{"feature"}})
		ans <- answer{res, err}
	}()

	rec := httptest.NewRecorder()
	b.handlePoll(rec, httptest.NewRequest(http.MethodGet, "/api/git-bridge/requests?wait=2s&session=aa", nil))
	var polled struct {
		Requests []gitBridgeRequest `json:"requests"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &polled); err != nil || len(polled.Requests) != 1 {
		t.Fatalf("poll: %v %s", err, rec.Body)
	}
	if got := polled.Requests[0]; got.Op != "push" || len(got.Branches) != 1 || got.Branches[0] != "feature" {
		t.Fatalf("polled %+v", got)
	}

	post := func(id, session string) int {
		req := httptest.NewRequest(http.MethodPost, "/api/git-bridge/requests/"+id+"/result?session="+session, strings.NewReader(`{"sha":"abc"}`))
		req.SetPathValue("id", id)
		rec := httptest.NewRecorder()
		b.handleResult(rec, req)
		return rec.Code
	}
	// Only the session that picked the request up may answer it.
	if code := post(polled.Requests[0].ID, "bb"); code != http.StatusGone {
		t.Fatalf("another session's result got %d", code)
	}
	if code := post(polled.Requests[0].ID, "aa"); code != http.StatusNoContent {
		t.Fatalf("result status %d", code)
	}
	if a := <-ans; a.err != nil || a.res.SHA != "abc" {
		t.Fatalf("submit got %+v %v", a.res, a.err)
	}
	if code := post(polled.Requests[0].ID, "aa"); code != http.StatusGone {
		t.Fatalf("a second result got %d", code)
	}

	// An empty poll returns an empty list, not null.
	rec = httptest.NewRecorder()
	b.handlePoll(rec, httptest.NewRequest(http.MethodGet, "/api/git-bridge/requests?wait=10ms&session=aa", nil))
	if !strings.Contains(rec.Body.String(), `"requests":[]`) {
		t.Fatalf("empty poll body %s", rec.Body)
	}

	// A poll must name its session.
	rec = httptest.NewRecorder()
	b.handlePoll(rec, httptest.NewRequest(http.MethodGet, "/api/git-bridge/requests?wait=10ms", nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("a poll without a session got %d", rec.Code)
	}
}

func TestGitBridgeNewSessionFailsEarlierRequests(t *testing.T) {
	b := newGitBridge()
	b.lastSeen = time.Now()
	type answer struct {
		res gitBridgeResult
		err error
	}
	ans := make(chan answer, 1)
	go func() {
		res, err := b.submit(context.Background(), gitBridgeRequest{Op: "push", Branches: []string{"feature"}})
		ans <- answer{res, err}
	}()
	reqs := b.poll(context.Background(), "old", time.Second)
	if len(reqs) != 1 {
		t.Fatalf("polled %d requests", len(reqs))
	}

	// devpad restarts: its new poller has a new session, and the request the
	// old one took is failed rather than left until the helper gives up.
	b.poll(context.Background(), "new", time.Millisecond)
	select {
	case a := <-ans:
		if a.err != nil || a.res.Error != errDevpadRestarted {
			t.Fatalf("submit got %+v %v", a.res, a.err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the request from the old session was left waiting")
	}
	if b.complete("old", reqs[0].ID, gitBridgeResult{SHA: "late"}) {
		t.Fatal("the old session answered a request it no longer owns")
	}
}

func TestGitBridgeSocketRefusesForcePushes(t *testing.T) {
	b := newGitBridge()
	b.lastSeen = time.Now()
	rec := httptest.NewRecorder()
	b.handleHelperRequest(rec, httptest.NewRequest(http.MethodPost, "/request", strings.NewReader(`{"op":"push","branches":["feature"],"force":true}`)))
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "force") {
		t.Fatalf("force push got %d %s", rec.Code, rec.Body)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.pending) != 0 {
		t.Fatal("a force push was queued for devpad")
	}
}

func TestGitFetchDevpadBatchesManyBranches(t *testing.T) {
	rig := newBridgeRig(t)
	// Each branch gets a commit the workspace lacks, so git asks the helper
	// to fetch every one.
	n := gitBridgeMaxBranches + 6
	tree := runGit(t, rig.canonical, "rev-parse", "main^{tree}")
	parent := runGit(t, rig.canonical, "rev-parse", "main")
	var last string
	for i := 0; i < n; i++ {
		last = runGit(t, rig.canonical, "-c", "user.name=t", "-c", "user.email=t@example.com", "commit-tree", tree, "-p", parent, "-m", fmt.Sprintf("b-%02d", i))
		runGit(t, rig.canonical, "update-ref", fmt.Sprintf("refs/heads/b-%02d", i), last)
	}

	if out, err := rig.git(t, "fetch", "devpad"); err != nil {
		t.Fatalf("fetch: %v\n%s", err, out)
	}
	if got, want := runGit(t, rig.work, "rev-parse", fmt.Sprintf("refs/remotes/devpad/b-%02d", n-1)), last; got != want {
		t.Fatalf("last branch = %s, want %s", got, want)
	}
	rig.host.mu.Lock()
	defer rig.host.mu.Unlock()
	total := 0
	for _, size := range rig.host.fetches {
		if size > gitBridgeMaxBranches {
			t.Fatalf("a fetch named %d branches", size)
		}
		total += size
	}
	if len(rig.host.fetches) < 2 || total < n {
		t.Fatalf("fetches %v, want batches covering %d branches", rig.host.fetches, n)
	}
}

func configureRemotes(t *testing.T, body string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/git-bridge/remote", strings.NewReader(body))
	handleGitBridgeRemote(rec, req)
	return rec
}

func TestGitBridgeRemoteSetsUpOriginAndTracking(t *testing.T) {
	rig := newBridgeRig(t)
	runGit(t, rig.work, "config", "checkout.defaultRemote", "mine")
	if rec := configureRemotes(t, `{"upstream":true}`); rec.Code != http.StatusNoContent {
		t.Fatalf("configure: %d %s", rec.Code, rec.Body)
	}
	for key, want := range map[string]string{
		"remote.origin.url":      upstreamRemoteURL,
		"remote.origin.fetch":    "+refs/heads/*:refs/remotes/origin/*",
		"remote.pushDefault":     "devpad",
		"checkout.defaultRemote": "mine",
		// The seeded branch has a devpad/main copy to track.
		"branch.main.remote": "devpad",
		"branch.main.merge":  "refs/heads/main",
	} {
		if got := runGit(t, rig.work, "config", key); got != want {
			t.Fatalf("%s = %q, want %q", key, got, want)
		}
	}
	if rec := configureRemotes(t, `{"upstream":true}`); rec.Code != http.StatusNoContent {
		t.Fatalf("second configure: %d %s", rec.Code, rec.Body)
	}
	// Once the project has no upstream, devpad's origin goes and its own
	// remote stays.
	if rec := configureRemotes(t, `{}`); rec.Code != http.StatusNoContent {
		t.Fatalf("configure without upstream: %d %s", rec.Code, rec.Body)
	}
	if _, err := gitIn(rig.work, "remote", "get-url", "origin"); err == nil {
		t.Fatal("devpad's origin was kept without an upstream")
	}
	if got := runGit(t, rig.work, "config", "remote.devpad.url"); got != devpadRemoteURL {
		t.Fatalf("devpad remote = %q", got)
	}
}

func TestGitBridgeRemoteForgetsAChangedUpstreamsBranches(t *testing.T) {
	rig := newBridgeRig(t)
	if rec := configureRemotes(t, `{"upstream":true}`); rec.Code != http.StatusNoContent {
		t.Fatalf("configure: %d %s", rec.Code, rec.Body)
	}
	head := runGit(t, rig.work, "rev-parse", "HEAD")
	runGit(t, rig.work, "update-ref", "refs/remotes/origin/main", head)
	branches := func() string {
		return runGit(t, rig.work, "for-each-ref", "--format=%(refname)", "refs/remotes/origin/")
	}

	// The same upstream keeps what was fetched from it, whether or not the
	// origin recorded its version.
	for _, body := range []string{`{"upstream":true}`, `{"upstream":true,"upstreamVersion":0}`} {
		if rec := configureRemotes(t, body); rec.Code != http.StatusNoContent {
			t.Fatalf("configure %s: %d %s", body, rec.Code, rec.Body)
		}
		if branches() == "" {
			t.Fatalf("configure %s forgot the upstream's branches", body)
		}
	}
	// Another upstream's version forgets them.
	if rec := configureRemotes(t, `{"upstream":true,"upstreamVersion":2}`); rec.Code != http.StatusNoContent {
		t.Fatalf("configure the new upstream: %d %s", rec.Code, rec.Body)
	}
	if got := branches(); got != "" {
		t.Fatalf("the old upstream's branches were kept: %s", got)
	}
	if got := runGit(t, rig.work, "config", upstreamVersionKey); got != "2" {
		t.Fatalf("recorded version %q", got)
	}
	runGit(t, rig.work, "update-ref", "refs/remotes/origin/main", head)
	if rec := configureRemotes(t, `{"upstream":true,"upstreamVersion":2}`); rec.Code != http.StatusNoContent {
		t.Fatalf("configure again: %d %s", rec.Code, rec.Body)
	}
	if branches() == "" {
		t.Fatal("the new upstream's branches were forgotten")
	}
}

func TestGitBridgeRemoteReplacesOnlyTheUpstreamsOrigin(t *testing.T) {
	const url = "git@github.com:example/project.git"
	sum := sha256.Sum256([]byte(url))
	body := `{"upstream":true,"upstreamUrlHash":"` + hex.EncodeToString(sum[:]) + `"}`

	rig := newBridgeRig(t)
	runGit(t, rig.work, "remote", "add", "origin", url)
	runGit(t, rig.work, "config", "remote.origin.pushurl", url)
	// The converted workspace's branches tracked the upstream.
	runGit(t, rig.work, "config", "branch.main.remote", "origin")
	runGit(t, rig.work, "branch", "--quiet", "other")
	runGit(t, rig.work, "update-ref", "refs/remotes/devpad/other", "main")
	runGit(t, rig.work, "config", "branch.other.remote", "origin")
	runGit(t, rig.work, "config", "branch.other.merge", "refs/heads/elsewhere")
	if rec := configureRemotes(t, body); rec.Code != http.StatusNoContent {
		t.Fatalf("configure: %d %s", rec.Code, rec.Body)
	}
	if got := runGit(t, rig.work, "config", "remote.origin.url"); got != upstreamRemoteURL {
		t.Fatalf("origin = %q", got)
	}
	if _, err := gitIn(rig.work, "config", "remote.origin.pushurl"); err == nil {
		t.Fatal("the old push URL was kept")
	}
	// A branch that tracked its namesake on the old origin now tracks
	// devpad; one that tracked another branch is left alone.
	if got := runGit(t, rig.work, "config", "branch.main.remote"); got != "devpad" {
		t.Fatalf("main tracks %q after the conversion", got)
	}
	if got := runGit(t, rig.work, "config", "branch.other.remote"); got != "origin" {
		t.Fatalf("other tracks %q", got)
	}
	// Once origin is devpad's, tracking it is the user's choice, and kept.
	runGit(t, rig.work, "config", "branch.main.remote", "origin")
	if rec := configureRemotes(t, body); rec.Code != http.StatusNoContent {
		t.Fatalf("configure again: %d %s", rec.Code, rec.Body)
	}
	if got := runGit(t, rig.work, "config", "branch.main.remote"); got != "origin" {
		t.Fatalf("main tracks %q after the user chose origin", got)
	}

	runGit(t, rig.work, "remote", "set-url", "origin", "https://example.com/elsewhere.git")
	if rec := configureRemotes(t, body); rec.Code != http.StatusConflict {
		t.Fatalf("another origin: expected 409, got %d %s", rec.Code, rec.Body)
	}
	if got := runGit(t, rig.work, "config", "remote.origin.url"); got != "https://example.com/elsewhere.git" {
		t.Fatalf("another origin was changed to %q", got)
	}
	// Without an upstream, no origin is touched or made.
	if rec := configureRemotes(t, `{}`); rec.Code != http.StatusNoContent {
		t.Fatalf("configure without upstream: %d %s", rec.Code, rec.Body)
	}
	if got := runGit(t, rig.work, "config", "remote.origin.url"); got != "https://example.com/elsewhere.git" {
		t.Fatalf("the user's origin was changed to %q", got)
	}
}

func TestGitFetchOriginIsReadOnly(t *testing.T) {
	rig := newBridgeRig(t)
	if rec := configureRemotes(t, `{"upstream":true}`); rec.Code != http.StatusNoContent {
		t.Fatalf("configure: %d %s", rec.Code, rec.Body)
	}
	// The canonical repo's copy of the upstream has a branch of its own.
	tree := runGit(t, rig.canonical, "rev-parse", "main^{tree}")
	sha := runGit(t, rig.canonical, "commit-tree", tree, "-p", "main", "-m", "theirs")
	runGit(t, rig.canonical, "update-ref", upstreamRefPrefix+"main", runGit(t, rig.canonical, "rev-parse", "main"))
	runGit(t, rig.canonical, "update-ref", upstreamRefPrefix+"theirs", sha)

	if out, err := rig.git(t, "fetch", "origin"); err != nil {
		t.Fatalf("fetch origin: %v\n%s", err, out)
	}
	if got := runGit(t, rig.work, "rev-parse", "refs/remotes/origin/theirs"); got != sha {
		t.Fatalf("origin/theirs = %s, want %s", got, sha)
	}
	// Checking it out tracks origin, and pushing goes to devpad.
	if out, err := rig.git(t, "switch", "theirs"); err != nil {
		t.Fatalf("switch: %v\n%s", err, out)
	}
	if got := runGit(t, rig.work, "config", "branch.theirs.remote"); got != "origin" {
		t.Fatalf("theirs tracks %q", got)
	}
	commitFile(t, rig.work, "mine.txt", "mine")
	if out, err := rig.git(t, "push"); err != nil {
		t.Fatalf("push: %v\n%s", err, out)
	}
	if got, want := runGit(t, rig.canonical, "rev-parse", "theirs"), runGit(t, rig.work, "rev-parse", "HEAD"); got != want {
		t.Fatalf("canonical theirs = %s, want %s", got, want)
	}

	for _, args := range [][]string{{"push", "origin", "theirs"}, {"push", "origin", "HEAD:refs/heads/x"}} {
		out, err := rig.git(t, args...)
		if err == nil || !strings.Contains(out, "read-only") {
			t.Fatalf("%v: expected a read-only refusal: %v\n%s", args, err, out)
		}
	}
	rig.host.mu.Lock()
	defer rig.host.mu.Unlock()
	for _, push := range rig.host.pushes {
		if strings.HasPrefix(push, "upstream:") {
			t.Fatalf("host saw %s", push)
		}
	}
}

func TestGitFetchDevpadShowsTheHostsWarning(t *testing.T) {
	rig := newBridgeRig(t)
	rig.host.mu.Lock()
	rig.host.warning = "could not reach the upstream"
	rig.host.mu.Unlock()
	out, err := rig.git(t, "fetch", "devpad")
	if err != nil || !strings.Contains(out, "warning: devpad: could not reach the upstream") {
		t.Fatalf("fetch: %v\n%s", err, out)
	}
}

func TestGitBridgeSocketRefusesUpstreamPushes(t *testing.T) {
	b := newGitBridge()
	b.lastSeen = time.Now()
	for _, body := range []string{
		`{"op":"push","remote":"upstream","branches":["main"]}`,
		`{"op":"list","remote":"elsewhere"}`,
	} {
		rec := httptest.NewRecorder()
		b.handleHelperRequest(rec, httptest.NewRequest(http.MethodPost, "/request", strings.NewReader(body)))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s got %d %s", body, rec.Code, rec.Body)
		}
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.pending) != 0 {
		t.Fatal("a refused request was queued for devpad")
	}
}

func TestIDEGitActionsUseTheBranchsRemote(t *testing.T) {
	rig := newBridgeRig(t)
	if rec := configureRemotes(t, `{"upstream":true}`); rec.Code != http.StatusNoContent {
		t.Fatalf("configure: %d %s", rec.Code, rec.Body)
	}
	t.Setenv("PATH", rig.path)
	ctx := context.Background()
	if got := defaultRemote(ctx, false); got != "devpad" {
		t.Fatalf("main fetches from %q", got)
	}

	// The Push button publishes a new branch to devpad, never origin.
	runGit(t, rig.work, "checkout", "-q", "-b", "feature")
	sha := commitFile(t, rig.work, "f.txt", "f")
	if out, err := actionPush(ctx, gitActionRequest{Action: "push"}); err != nil {
		t.Fatalf("push: %v\n%s", err, out)
	}
	if got := runGit(t, rig.canonical, "rev-parse", "feature"); got != sha {
		t.Fatalf("canonical feature = %s, want %s", got, sha)
	}
	// The first push set up tracking, so Pull uses devpad too.
	if got := defaultRemote(ctx, false); got != "devpad" {
		t.Fatalf("feature pulls from %q", got)
	}
	if out, err := actionPull(ctx, gitActionRequest{Action: "pull"}); err != nil {
		t.Fatalf("pull: %v\n%s", err, out)
	}

	// A standalone repo keeps using origin.
	plain := t.TempDir()
	runGit(t, plain, "init", "-q")
	withWorkspaceRoot(t, plain)
	if got := defaultRemote(ctx, true); got != "origin" {
		t.Fatalf("a plain repo pushes to %q", got)
	}
}
