package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// upstreamRig is a canonical repo cloned from an upstream, and a clone of
// the upstream that plays someone else pushing to it.
type upstreamRig struct {
	upstream, other, canonical string
}

func newUpstreamRig(t *testing.T) *upstreamRig {
	t.Helper()
	upstream := t.TempDir()
	runGit(t, upstream, "init", "--quiet", "--bare", "--initial-branch=main")
	other := t.TempDir()
	runGit(t, other, "clone", "--quiet", upstream, ".")
	commitFile(t, other, "README.md", "hello")
	runGit(t, other, "push", "--quiet", "origin", "HEAD:main", "HEAD:develop", "HEAD:feature", "HEAD:old")

	canonical := t.TempDir()
	rec := repoRequest(t, canonical, handleRepoInit, http.MethodPost, "/api/repo/init", strings.NewReader(`{"defaultBranch":"main","upstreamUrl":"`+upstream+`"}`))
	if rec.Code != http.StatusOK {
		t.Fatalf("init: %d %s", rec.Code, rec.Body)
	}
	return &upstreamRig{upstream: upstream, other: other, canonical: canonical}
}

// pushUpstream commits on branch in the other clone and pushes it.
func (r *upstreamRig) pushUpstream(t *testing.T, branch, name string) string {
	t.Helper()
	runGit(t, r.other, "checkout", "--quiet", "-B", branch, "origin/"+branch)
	sha := commitFile(t, r.other, name, name)
	runGit(t, r.other, "push", "--quiet", "origin", "HEAD:"+branch)
	return sha
}

// upstreamFetch fetches from the upstream, then fast-forwards the protected
// branches query names, as devpad does.
func upstreamFetch(t *testing.T, canonical, query string) upstreamStatus {
	t.Helper()
	rec := upstreamRequest(t, canonical, handleRepoUpstreamFetch, http.MethodPost, "/api/repo/upstream/fetch", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("upstream fetch: %d %s", rec.Code, rec.Body)
	}
	rec = upstreamRequest(t, canonical, handleRepoUpstreamFastForward, http.MethodPost, "/api/repo/upstream/fast-forward"+query, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("upstream fast-forward: %d %s", rec.Code, rec.Body)
	}
	var status upstreamStatus
	if err := json.Unmarshal(rec.Body.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	return status
}

func TestRepoUpstreamFetchFastForwardsOnlyProtectedBranches(t *testing.T) {
	rig := newUpstreamRig(t)
	upstreamFetch(t, rig.canonical, "")
	if runGit(t, rig.canonical, "rev-parse", "refs/upstream/heads/old") == "" {
		t.Fatal("old was not fetched")
	}
	main := rig.pushUpstream(t, "main", "main.txt")
	feature := rig.pushUpstream(t, "feature", "feature.txt")
	featureBefore := runGit(t, rig.canonical, "rev-parse", "feature")
	// develop moves on both sides.
	theirs := rig.pushUpstream(t, "develop", "theirs.txt")
	ours := runGit(t, rig.canonical, "commit-tree", runGit(t, rig.canonical, "rev-parse", "develop^{tree}"), "-p", "develop", "-m", "ours")
	runGit(t, rig.canonical, "update-ref", "refs/heads/develop", ours)
	runGit(t, rig.other, "push", "--quiet", "origin", "HEAD:refs/heads/new", ":old")

	status := upstreamFetch(t, rig.canonical, "?protected=main&protected=develop")
	if status.FetchedAt == nil || len(status.Branches) != 2 {
		t.Fatalf("status = %+v", status)
	}
	if b := status.Branches[0]; b.Branch != "main" || !b.FastForwarded || b.SHA != main || b.UpstreamSHA != main || b.Ahead != 0 || b.Behind != 0 {
		t.Fatalf("main = %+v", b)
	}
	if b := status.Branches[1]; b.Branch != "develop" || b.FastForwarded || b.SHA != ours || b.UpstreamSHA != theirs || b.Ahead != 1 || b.Behind != 1 {
		t.Fatalf("develop = %+v", b)
	}
	if got := runGit(t, rig.canonical, "rev-parse", "main"); got != main {
		t.Fatalf("canonical main = %s, want %s", got, main)
	}
	if got := runGit(t, rig.canonical, "rev-parse", "develop"); got != ours {
		t.Fatal("a diverged branch moved")
	}
	// Unprotected branches stay as they are, and none is made or deleted.
	if got := runGit(t, rig.canonical, "rev-parse", "feature"); got != featureBefore {
		t.Fatal("an unprotected branch moved")
	}
	if got := runGit(t, rig.canonical, "rev-parse", "refs/upstream/heads/feature"); got != feature {
		t.Fatalf("upstream feature = %s, want %s", got, feature)
	}
	if gitExec(t.Context(), "-C", rig.canonical, "rev-parse", "--verify", "--quiet", "refs/heads/new") == nil {
		t.Fatal("an upstream-only branch was created")
	}
	if gitExec(t.Context(), "-C", rig.canonical, "rev-parse", "--verify", "--quiet", "refs/heads/old") != nil {
		t.Fatal("a branch deleted upstream was deleted")
	}
	// The upstream's own namespace mirrors it, deletions included.
	rec := repoRequest(t, rig.canonical, handleRepoUpstreamRefs, http.MethodGet, "/api/repo/upstream/refs", nil)
	var refs repoRefs
	if err := json.Unmarshal(rec.Body.Bytes(), &refs); err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, ref := range refs.Branches {
		names[ref.Name] = true
	}
	if !names["new"] || names["old"] || !names["main"] || refs.DefaultBranch != "main" {
		t.Fatalf("upstream refs = %+v", refs)
	}

	// The status endpoint reports the same without fetching.
	rig.pushUpstream(t, "main", "later.txt")
	rec = repoRequest(t, rig.canonical, handleRepoUpstreamStatus, http.MethodGet, "/api/repo/upstream/status?protected=main", nil)
	var again upstreamStatus
	if err := json.Unmarshal(rec.Body.Bytes(), &again); err != nil || len(again.Branches) != 1 || again.Branches[0].UpstreamSHA != main || again.FetchedAt == nil {
		t.Fatalf("status = %s", rec.Body)
	}
}

func TestRepoUpstreamFetchRejections(t *testing.T) {
	local := initCanonical(t)
	if rec := upstreamRequest(t, local, handleRepoUpstreamFetch, http.MethodPost, "/api/repo/upstream/fetch", nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("fetch without upstream: %d %s", rec.Code, rec.Body)
	}
	rig := newUpstreamRig(t)
	if rec := upstreamRequest(t, rig.canonical, handleRepoUpstreamFastForward, http.MethodPost, "/api/repo/upstream/fast-forward?protected=--all", nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad protected name: %d %s", rec.Code, rec.Body)
	}
	// The fetch alone moves no project branch.
	main := runGit(t, rig.canonical, "rev-parse", "main")
	moved := rig.pushUpstream(t, "main", "moved.txt")
	upstreamFetch(t, rig.canonical, "")
	if got := runGit(t, rig.canonical, "rev-parse", "main"); got != main {
		t.Fatal("a fetch moved a branch")
	}
	if got := runGit(t, rig.canonical, "rev-parse", "refs/upstream/heads/main"); got != moved {
		t.Fatalf("upstream main = %s, want %s", got, moved)
	}
	// Fetches run one at a time: one that cannot start before devpad gives
	// up on it fails.
	upstreamBusy <- struct{}{}
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	rec := httptest.NewRecorder()
	handleRepoUpstreamFetch(rec, httptest.NewRequestWithContext(ctx, http.MethodPost, "/api/repo/upstream/fetch", nil))
	<-upstreamBusy
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("overlapping fetch: %d %s", rec.Code, rec.Body)
	}
}

func TestRepoPushRecordsTheUpstreamCopy(t *testing.T) {
	rig := newUpstreamRig(t)
	merged := advanceMain(t, rig.canonical, "merged.txt", "merged")
	if rec := upstreamRequest(t, rig.canonical, handleRepoPush, http.MethodPost, "/api/repo/push?branch=main", nil); rec.Code != http.StatusOK {
		t.Fatalf("push: %d %s", rec.Code, rec.Body)
	}
	if got := runGit(t, rig.canonical, "rev-parse", "refs/upstream/heads/main"); got != merged {
		t.Fatalf("upstream main = %s, want %s", got, merged)
	}
}

func TestUpstreamBranchesReachTheWorkspaceAsOrigin(t *testing.T) {
	rig := newUpstreamRig(t)
	feature := rig.pushUpstream(t, "feature", "feature.txt")
	upstreamFetch(t, rig.canonical, "")
	work := seedSandbox(t, rig.canonical, "main")

	rec := repoRequest(t, rig.canonical, handleRepoBundle, http.MethodGet, "/api/repo/bundle?ns=upstream&branch=feature", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("bundle: %d %s", rec.Code, rec.Body)
	}
	bundle := rec.Body.Bytes()
	rec = repoRequest(t, work, handleRepoFetchBases, http.MethodPost, "/api/repo/fetch-bases?source=upstream&branch=feature", bytes.NewReader(bundle))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"origin/feature"`) {
		t.Fatalf("fetch-bases: %d %s", rec.Code, rec.Body)
	}
	if got := runGit(t, work, "rev-parse", "refs/remotes/origin/feature"); got != feature {
		t.Fatalf("origin/feature = %s, want %s", got, feature)
	}

	for _, target := range []string{"/api/repo/bundle?ns=other&branch=feature", "/api/repo/bundle?ns=upstream&branch=nope"} {
		if rec := repoRequest(t, rig.canonical, handleRepoBundle, http.MethodGet, target, nil); rec.Code == http.StatusOK {
			t.Fatalf("%s was bundled", target)
		}
	}
	if rec := repoRequest(t, work, handleRepoFetchBases, http.MethodPost, "/api/repo/fetch-bases?source=other&branch=feature", bytes.NewReader(bundle)); rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown source: %d", rec.Code)
	}
}

func TestRepoSetUpstream(t *testing.T) {
	rig := newUpstreamRig(t)
	upstreamFetch(t, rig.canonical, "")
	setUpstream := func(dir, url string) *httptest.ResponseRecorder {
		t.Helper()
		body, _ := json.Marshal(map[string]string{"url": url})
		return repoRequest(t, dir, handleRepoSetUpstream, http.MethodPut, "/api/repo/upstream", bytes.NewReader(body))
	}

	// Another upstream, with a history of its own.
	next := t.TempDir()
	runGit(t, next, "init", "--quiet", "--bare", "--initial-branch=main")
	work := t.TempDir()
	runGit(t, work, "clone", "--quiet", next, ".")
	nextMain := commitFile(t, work, "OTHER.md", "other")
	runGit(t, work, "push", "--quiet", "origin", "HEAD:main", "HEAD:elsewhere")

	main := runGit(t, rig.canonical, "rev-parse", "main")
	if rec := setUpstream(rig.canonical, next); rec.Code != http.StatusOK {
		t.Fatalf("set upstream: %d %s", rec.Code, rec.Body)
	}
	if got := runGit(t, rig.canonical, "config", "remote.origin.url"); got != next {
		t.Fatalf("origin = %q", got)
	}
	// The old upstream's branches and fetch time are gone, so nothing of it
	// passes for the new upstream's.
	if out := runGit(t, rig.canonical, "for-each-ref", "refs/upstream/"); out != "" {
		t.Fatalf("old upstream refs kept: %s", out)
	}
	rec := repoRequest(t, rig.canonical, handleRepoUpstreamStatus, http.MethodGet, "/api/repo/upstream/status?protected=main", nil)
	var status upstreamStatus
	if err := json.Unmarshal(rec.Body.Bytes(), &status); err != nil || status.FetchedAt != nil || status.Branches[0].UpstreamSHA != "" {
		t.Fatalf("status after the change = %s", rec.Body)
	}
	// Fetching brings the new upstream's branches; an unrelated main is
	// only compared, never moved.
	status = upstreamFetch(t, rig.canonical, "?protected=main")
	if b := status.Branches[0]; b.UpstreamSHA != nextMain || b.FastForwarded || b.SHA != main {
		t.Fatalf("main against the new upstream = %+v", b)
	}
	if runGit(t, rig.canonical, "rev-parse", "refs/upstream/heads/elsewhere") != nextMain {
		t.Fatal("the new upstream's branches were not fetched")
	}

	// Setting the same URL again keeps what was fetched.
	if rec := setUpstream(rig.canonical, next); rec.Code != http.StatusOK {
		t.Fatalf("same upstream: %d %s", rec.Code, rec.Body)
	}
	if runGit(t, rig.canonical, "rev-parse", "refs/upstream/heads/main") != nextMain {
		t.Fatal("setting the same upstream forgot its branches")
	}

	// An empty URL removes the upstream.
	if rec := setUpstream(rig.canonical, ""); rec.Code != http.StatusOK {
		t.Fatalf("remove upstream: %d %s", rec.Code, rec.Body)
	}
	if gitExec(t.Context(), "-C", rig.canonical, "remote", "get-url", "origin") == nil {
		t.Fatal("origin kept")
	}
	if rec := upstreamRequest(t, rig.canonical, handleRepoUpstreamFetch, http.MethodPost, "/api/repo/upstream/fetch", nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("fetch without upstream: %d %s", rec.Code, rec.Body)
	}

	// A project made without an upstream can be given one.
	local := initCanonical(t)
	if rec := setUpstream(local, rig.upstream); rec.Code != http.StatusOK {
		t.Fatalf("add upstream: %d %s", rec.Code, rec.Body)
	}
	upstreamFetch(t, local, "")
	if runGit(t, local, "rev-parse", "refs/upstream/heads/develop") == "" {
		t.Fatal("the added upstream was not fetched")
	}

	for _, url := range []string{"--upload-pack=touch /tmp/x", "a\nb"} {
		if rec := setUpstream(local, url); rec.Code != http.StatusBadRequest {
			t.Fatalf("%q: %d %s", url, rec.Code, rec.Body)
		}
	}
	// It does not wait for a fetch that is running.
	upstreamBusy <- struct{}{}
	rec = setUpstream(local, next)
	<-upstreamBusy
	if rec.Code != http.StatusConflict {
		t.Fatalf("during a fetch: %d %s", rec.Code, rec.Body)
	}
}

func TestRepoRefusesAnUpstreamDevpadDoesNotExpect(t *testing.T) {
	rig := newUpstreamRig(t)
	rig.pushUpstream(t, "main", "moved.txt")
	elsewhere := func(handler http.HandlerFunc, method, target string) *httptest.ResponseRecorder {
		t.Helper()
		withWorkspaceRoot(t, rig.canonical)
		req := httptest.NewRequest(method, target, nil)
		req.Header.Set(upstreamHeader, "git@github.com:example/other.git")
		rec := httptest.NewRecorder()
		handler(rec, req)
		return rec
	}
	for _, tc := range []struct {
		handler http.HandlerFunc
		target  string
	}{
		{handleRepoUpstreamFetch, "/api/repo/upstream/fetch"},
		{handleRepoUpstreamFastForward, "/api/repo/upstream/fast-forward?protected=main"},
		{handleRepoPush, "/api/repo/push?branch=main"},
	} {
		if rec := elsewhere(tc.handler, http.MethodPost, tc.target); rec.Code != http.StatusPreconditionFailed {
			t.Fatalf("%s: %d %s", tc.target, rec.Code, rec.Body)
		}
	}
	if out := runGit(t, rig.canonical, "for-each-ref", "refs/upstream/"); out != "" {
		t.Fatalf("fetched from an upstream devpad did not expect: %s", out)
	}
}

func TestRepoPushWaitsForAFetch(t *testing.T) {
	rig := newUpstreamRig(t)
	advanceMain(t, rig.canonical, "merged.txt", "merged")
	upstreamBusy <- struct{}{}
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	withWorkspaceRoot(t, rig.canonical)
	req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/api/repo/push?branch=main", nil)
	req.Header.Set(upstreamHeader, rig.upstream)
	rec := httptest.NewRecorder()
	handleRepoPush(rec, req)
	<-upstreamBusy
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("push during a fetch: %d %s", rec.Code, rec.Body)
	}
}
