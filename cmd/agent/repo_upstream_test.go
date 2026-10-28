package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
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

func upstreamFetch(t *testing.T, canonical, query string) upstreamStatus {
	t.Helper()
	rec := repoRequest(t, canonical, handleRepoUpstreamFetch, http.MethodPost, "/api/repo/upstream/fetch"+query, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("upstream fetch: %d %s", rec.Code, rec.Body)
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
	if rec := repoRequest(t, local, handleRepoUpstreamFetch, http.MethodPost, "/api/repo/upstream/fetch", nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("fetch without upstream: %d %s", rec.Code, rec.Body)
	}
	rig := newUpstreamRig(t)
	if rec := repoRequest(t, rig.canonical, handleRepoUpstreamFetch, http.MethodPost, "/api/repo/upstream/fetch?protected=--all", nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad protected name: %d %s", rec.Code, rec.Body)
	}
}

func TestRepoPushRecordsTheUpstreamCopy(t *testing.T) {
	rig := newUpstreamRig(t)
	merged := advanceMain(t, rig.canonical, "merged.txt", "merged")
	if rec := repoRequest(t, rig.canonical, handleRepoPush, http.MethodPost, "/api/repo/push?branch=main", nil); rec.Code != http.StatusOK {
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
