package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func publishFF(t *testing.T, canonical, branch string, bundle []byte) *httptest.ResponseRecorder {
	t.Helper()
	return repoRequest(t, canonical, handleRepoImport, http.MethodPost, "/api/repo/import?ffOnly=true&branch="+branch, bytes.NewReader(bundle))
}

func TestRepoImportFastForwardOnly(t *testing.T) {
	canonical := initCanonical(t)
	work := seedSandbox(t, canonical, "feature")
	first := commitFile(t, work, "a.txt", "a")

	// A new branch is created.
	if rec := publishFF(t, canonical, "feature", bundleFrom(t, work, "feature")); rec.Code != http.StatusOK {
		t.Fatalf("first publish status %d: %s", rec.Code, rec.Body)
	}
	if got := runGit(t, canonical, "rev-parse", "feature"); got != first {
		t.Fatalf("canonical feature = %s, want %s", got, first)
	}

	// Adding commits fast-forwards it.
	second := commitFile(t, work, "b.txt", "b")
	if rec := publishFF(t, canonical, "feature", bundleFrom(t, work, "feature")); rec.Code != http.StatusOK {
		t.Fatalf("fast-forward status %d: %s", rec.Code, rec.Body)
	}
	if got := runGit(t, canonical, "rev-parse", "feature"); got != second {
		t.Fatalf("canonical feature = %s, want %s", got, second)
	}

	// Rewriting history is refused and leaves the branch alone.
	runGit(t, work, "reset", "--hard", first)
	commitFile(t, work, "c.txt", "c")
	rec := publishFF(t, canonical, "feature", bundleFrom(t, work, "feature"))
	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409 for a non-fast-forward, got %d: %s", rec.Code, rec.Body)
	}
	if got := runGit(t, canonical, "rev-parse", "feature"); got != second {
		t.Fatalf("rejected publish moved feature to %s", got)
	}
	if got := runGit(t, canonical, "for-each-ref", quarantineRefPrefix); got != "" {
		t.Fatalf("quarantine refs left behind: %s", got)
	}

	// Without ffOnly the import still force-updates, as sandboxes rely on.
	rec = repoRequest(t, canonical, handleRepoImport, http.MethodPost, "/api/repo/import?branch=feature", bytes.NewReader(bundleFrom(t, work, "feature")))
	if rec.Code != http.StatusOK {
		t.Fatalf("forced import status %d: %s", rec.Code, rec.Body)
	}

	// The default branch stays refused.
	if rec := publishFF(t, canonical, "main", bundleFrom(t, work, "feature")); rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for the default branch, got %d: %s", rec.Code, rec.Body)
	}
}

func checkout(t *testing.T, canonical, work, branch string) (int, repoCheckoutResult, string) {
	t.Helper()
	rec := repoRequest(t, work, handleRepoCheckout, http.MethodPost, "/api/repo/checkout?branch="+branch, bytes.NewReader(bundleFrom(t, canonical, branch)))
	var result repoCheckoutResult
	if rec.Code == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
			t.Fatalf("decoding checkout: %v", err)
		}
	}
	return rec.Code, result, rec.Body.String()
}

func TestRepoCheckoutCreatesAndSwitchesBranch(t *testing.T) {
	canonical := initCanonical(t)
	agentWork := seedSandbox(t, canonical, "task/1")
	taskSHA := commitFile(t, agentWork, "task.txt", "agent work")
	if rec := repoRequest(t, canonical, handleRepoImport, http.MethodPost, "/api/repo/import?branch=task/1", bytes.NewReader(bundleFrom(t, agentWork, "task/1"))); rec.Code != http.StatusOK {
		t.Fatalf("import status %d: %s", rec.Code, rec.Body)
	}

	// The project workspace is seeded onto main itself.
	work := seedSandbox(t, canonical, "main")
	code, result, body := checkout(t, canonical, work, "task/1")
	if code != http.StatusOK {
		t.Fatalf("checkout status %d: %s", code, body)
	}
	if !result.Created || result.SHA != taskSHA || result.UpstreamSHA != taskSHA || result.Upstream != "devpad/task/1" {
		t.Fatalf("unexpected checkout result: %+v", result)
	}
	if got := runGit(t, work, "branch", "--show-current"); got != "task/1" {
		t.Fatalf("expected task/1 checked out, got %q", got)
	}
	if got := runGit(t, work, "rev-parse", "refs/remotes/devpad/task/1"); got != taskSHA {
		t.Fatalf("devpad/task/1 = %s, want %s", got, taskSHA)
	}
}

func TestRepoCheckoutKeepsLocalWork(t *testing.T) {
	canonical := initCanonical(t)
	agentWork := seedSandbox(t, canonical, "task/1")
	commitFile(t, agentWork, "task.txt", "v1")
	importTask := func() string {
		t.Helper()
		if rec := repoRequest(t, canonical, handleRepoImport, http.MethodPost, "/api/repo/import?branch=task/1", bytes.NewReader(bundleFrom(t, agentWork, "task/1"))); rec.Code != http.StatusOK {
			t.Fatalf("import status %d: %s", rec.Code, rec.Body)
		}
		return runGit(t, canonical, "rev-parse", "task/1")
	}
	importTask()

	work := seedSandbox(t, canonical, "main")
	if code, _, body := checkout(t, canonical, work, "task/1"); code != http.StatusOK {
		t.Fatalf("checkout status %d: %s", code, body)
	}
	local := commitFile(t, work, "mine.txt", "local commit")

	// The agent moves on; checking out again refreshes devpad/task/1 but
	// leaves the local branch and its commit alone.
	commitFile(t, agentWork, "more.txt", "v2")
	upstream := importTask()
	code, result, body := checkout(t, canonical, work, "task/1")
	if code != http.StatusOK {
		t.Fatalf("second checkout status %d: %s", code, body)
	}
	if result.Created || result.SHA != local || result.UpstreamSHA != upstream {
		t.Fatalf("unexpected second checkout: %+v (local %s, upstream %s)", result, local, upstream)
	}

	// Switching away with uncommitted changes that would be overwritten is
	// refused, and the changes survive.
	runGit(t, work, "switch", "main")
	if err := os.WriteFile(filepath.Join(work, "task.txt"), []byte("uncommitted"), 0o644); err != nil {
		t.Fatalf("writing file: %v", err)
	}
	if code, _, body := checkout(t, canonical, work, "task/1"); code != http.StatusConflict {
		t.Fatalf("expected 409 over uncommitted changes, got %d: %s", code, body)
	}
	if got, _ := os.ReadFile(filepath.Join(work, "task.txt")); string(got) != "uncommitted" {
		t.Fatalf("uncommitted change lost: %q", got)
	}
	if got := runGit(t, work, "branch", "--show-current"); got != "main" {
		t.Fatalf("expected to stay on main, got %q", got)
	}
}

func TestRepoCheckoutRejections(t *testing.T) {
	canonical := initCanonical(t)
	bundle := bundleFrom(t, canonical, "main")

	if rec := repoRequest(t, t.TempDir(), handleRepoCheckout, http.MethodPost, "/api/repo/checkout?branch=main", bytes.NewReader(bundle)); rec.Code != http.StatusConflict {
		t.Fatalf("expected 409 without a repo, got %d: %s", rec.Code, rec.Body)
	}
	work := seedSandbox(t, canonical, "main")
	for _, branch := range []string{"-f", "a..b", ""} {
		if rec := repoRequest(t, work, handleRepoCheckout, http.MethodPost, "/api/repo/checkout?branch="+branch, bytes.NewReader(bundle)); rec.Code != http.StatusBadRequest {
			t.Fatalf("branch %q: expected 400, got %d: %s", branch, rec.Code, rec.Body)
		}
	}
	if rec := repoRequest(t, work, handleRepoCheckout, http.MethodPost, "/api/repo/checkout?branch=elsewhere", bytes.NewReader(bundle)); rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for a branch missing from the bundle, got %d: %s", rec.Code, rec.Body)
	}
}

// A project with no upstream starts from devpad's empty initial commit, and
// its project workspace is seeded on the default branch itself: a scratch
// project the user can commit to and publish from straight away.
func TestScratchProjectWorkspaceSeedsFromInitialCommit(t *testing.T) {
	canonical := initCanonical(t)
	initial := runGit(t, canonical, "rev-parse", "main")

	work := t.TempDir()
	rec := repoRequest(t, work, handleRepoSeed, http.MethodPost, "/api/repo/seed?base=main&branch=main&name=User&email=user@example.com", bytes.NewReader(bundleFrom(t, canonical, "main")))
	if rec.Code != http.StatusOK {
		t.Fatalf("seed status %d: %s", rec.Code, rec.Body)
	}
	if got := runGit(t, work, "branch", "--show-current"); got != "main" {
		t.Fatalf("expected main checked out, got %q", got)
	}
	if got := runGit(t, work, "rev-parse", "HEAD"); got != initial {
		t.Fatalf("workspace HEAD = %s, want the initial commit %s", got, initial)
	}

	runGit(t, work, "switch", "-c", "first-feature")
	sha := commitFile(t, work, "main.go", "package main")
	if rec := publishFF(t, canonical, "first-feature", bundleFrom(t, work, "first-feature")); rec.Code != http.StatusOK {
		t.Fatalf("publish status %d: %s", rec.Code, rec.Body)
	}
	if got := runGit(t, canonical, "rev-parse", "first-feature"); got != sha {
		t.Fatalf("canonical first-feature = %s, want %s", got, sha)
	}
}

func branchStatus(t *testing.T, work, branch string) (int, repoBranchStatus) {
	t.Helper()
	rec := repoRequest(t, work, handleRepoBranchStatus, http.MethodGet, "/api/repo/branch-status?branch="+branch, nil)
	var result repoBranchStatus
	if rec.Code == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
			t.Fatalf("decoding branch status: %v", err)
		}
	}
	return rec.Code, result
}

func TestRepoBranchStatusCountsAheadAndBehind(t *testing.T) {
	canonical := initCanonical(t)
	work := seedSandbox(t, canonical, "main")
	fetchBase := func(branch string) {
		t.Helper()
		rec := repoRequest(t, work, handleRepoFetchBase, http.MethodPost, "/api/repo/fetch-base?branch="+branch, bytes.NewReader(bundleFrom(t, canonical, branch)))
		if rec.Code != http.StatusOK {
			t.Fatalf("fetch-base status %d: %s", rec.Code, rec.Body)
		}
	}
	fetchBase("main")

	if code, st := branchStatus(t, work, "main"); code != http.StatusOK || st.Ahead != 0 || st.Behind != 0 || st.UpstreamSHA != st.SHA {
		t.Fatalf("fresh main: %d %+v", code, st)
	}

	// Local main gets a commit; the project's main gets two others.
	commitFile(t, work, "local.txt", "local")
	other := seedSandbox(t, canonical, "side")
	commitFile(t, other, "x.txt", "x")
	commitFile(t, other, "y.txt", "y")
	runGit(t, canonical, "fetch", other, "side:main")
	fetchBase("main")
	if code, st := branchStatus(t, work, "main"); code != http.StatusOK || st.Ahead != 1 || st.Behind != 2 {
		t.Fatalf("diverged main: %d %+v", code, st)
	}

	// A branch the project does not have counts only its own commits.
	runGit(t, work, "switch", "-c", "mine")
	commitFile(t, work, "mine.txt", "mine")
	if code, st := branchStatus(t, work, "mine"); code != http.StatusOK || st.Ahead != 2 || st.UpstreamSHA != "" {
		t.Fatalf("unpublished branch: %d %+v", code, st)
	}

	if code, _ := branchStatus(t, work, "nope"); code != http.StatusNotFound {
		t.Fatalf("expected 404 for an unknown branch, got %d", code)
	}
	if code, _ := branchStatus(t, work, "--all"); code != http.StatusBadRequest {
		t.Fatalf("expected 400 for a flag, got %d", code)
	}
}
