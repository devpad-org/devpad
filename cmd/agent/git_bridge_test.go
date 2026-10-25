package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestMain lets the test binary stand in for git-remote-devpad: git runs a
// wrapper script that execs this binary with the variable set.
func TestMain(m *testing.M) {
	if os.Getenv("DEVPAD_TEST_REMOTE_HELPER") == "1" {
		os.Exit(runRemoteHelper(os.Stdin, os.Stdout, os.Stderr))
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
	switch req.Op {
	case "list":
		out, err := gitIn(h.canonical, "for-each-ref", "--format=%(refname:strip=2) %(objectname)", "refs/heads/")
		if err != nil {
			return gitBridgeResult{Error: out}
		}
		res := gitBridgeResult{DefaultBranch: "main"}
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
