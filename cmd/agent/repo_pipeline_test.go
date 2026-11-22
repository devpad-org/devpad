package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/devpad-org/devpad/internal/agent"
	"github.com/devpad-org/devpad/internal/pipelineid"
)

func pinPipeline(t *testing.T, canonical string, req repoPipelinePin) (int, repoPipelinePinResult, string) {
	t.Helper()
	body, _ := json.Marshal(req)
	rec := repoRequest(t, canonical, handleRepoPinPipeline, http.MethodPost, "/api/repo/pipeline-ref", bytes.NewReader(body))
	var got repoPipelinePinResult
	json.Unmarshal(rec.Body.Bytes(), &got)
	return rec.Code, got, rec.Body.String()
}

func mustPin(t *testing.T, canonical string, req repoPipelinePin) repoPipelinePinResult {
	t.Helper()
	code, got, body := pinPipeline(t, canonical, req)
	if code != http.StatusOK || got.Conflict != nil {
		t.Fatalf("pin %+v: %d %s", req, code, body)
	}
	return got
}

// branchRefs is every branch and where it points.
func branchRefs(t *testing.T, repo string) string {
	t.Helper()
	return runGit(t, repo, "for-each-ref", "--format=%(refname) %(objectname)", "refs/heads/")
}

// cloneLikeAJob clones a pipeline's bundle into a fresh repo the way a job's
// container does, and returns where.
func cloneLikeAJob(t *testing.T, canonical, id string) string {
	t.Helper()
	rec := repoRequest(t, canonical, handleRepoPipelineBundle, http.MethodGet, "/api/repo/pipeline-bundle?id="+id, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("bundle status %d: %s", rec.Code, rec.Body)
	}
	bundle := filepath.Join(t.TempDir(), "pipeline.bundle")
	if err := os.WriteFile(bundle, rec.Body.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	job := t.TempDir()
	runGit(t, job, "init", "-q")
	runGit(t, job, "fetch", "-q", "--no-tags", bundle, pipelineid.Ref(id))
	runGit(t, job, "checkout", "-q", "-b", "tested", "FETCH_HEAD")
	return job
}

func TestPinPipelineCommitPinsItAsItIs(t *testing.T) {
	canonical := initCanonical(t)
	sandbox := seedSandbox(t, canonical, "task/1")
	head := commitFile(t, sandbox, "a.txt", "one")
	publish(t, canonical, sandbox, "task/1")
	before := branchRefs(t, canonical)

	got := mustPin(t, canonical, repoPipelinePin{ID: "12", Commit: head})
	if got.Ref != "refs/devpad/pipelines/12" || got.SHA != head {
		t.Fatalf("pinned %+v, want %s", got, head)
	}
	if pinned := runGit(t, canonical, "rev-parse", got.Ref); pinned != head {
		t.Fatalf("ref at %s, want %s", pinned, head)
	}
	if after := branchRefs(t, canonical); after != before {
		t.Fatalf("branches moved:\n%s\nwant\n%s", after, before)
	}

	// A job clones exactly that commit, with its history.
	job := cloneLikeAJob(t, canonical, "12")
	if sha := runGit(t, job, "rev-parse", "HEAD"); sha != head {
		t.Fatalf("job has %s, want %s", sha, head)
	}
	if content, err := os.ReadFile(filepath.Join(job, "a.txt")); err != nil || string(content) != "one" {
		t.Fatalf("job's a.txt: %q %v", content, err)
	}

	// Pinning the id again replaces it.
	main := runGit(t, canonical, "rev-parse", "main")
	if got := mustPin(t, canonical, repoPipelinePin{ID: "12", Commit: main}); got.SHA != main || runGit(t, canonical, "rev-parse", got.Ref) != main {
		t.Fatalf("re-pinned %+v, want %s", got, main)
	}

	// Deleting it is done, and done again when there is nothing left.
	for range 2 {
		rec := repoRequest(t, canonical, handleRepoDeletePipelineRef, http.MethodDelete, "/api/repo/pipeline-ref?id=12", nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("delete status %d: %s", rec.Code, rec.Body)
		}
	}
	if out, err := gitOutputIn(context.Background(), canonical, "rev-parse", "--verify", "--quiet", "refs/devpad/pipelines/12"); err == nil {
		t.Fatalf("expected the ref gone, got %s", out)
	}
	if rec := repoRequest(t, canonical, handleRepoPipelineBundle, http.MethodGet, "/api/repo/pipeline-bundle?id=12", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("expected no bundle once deleted, got %d", rec.Code)
	}
	if after := branchRefs(t, canonical); after != before {
		t.Fatalf("branches moved:\n%s\nwant\n%s", after, before)
	}
}

func TestPinPipelineMergeTestsWhatAMergeLands(t *testing.T) {
	canonical := initCanonical(t)
	sandbox := seedSandbox(t, canonical, "task/1")
	source := commitFile(t, sandbox, "a.txt", "task work")
	publish(t, canonical, sandbox, "task/1")
	target := advanceMain(t, canonical, "other.txt", "main work")
	before := branchRefs(t, canonical)

	got := mustPin(t, canonical, repoPipelinePin{ID: "7", Source: source, Target: target})
	if got.SHA == source || got.SHA == target {
		t.Fatalf("expected a merge commit, got %s", got.SHA)
	}
	if parents := runGit(t, canonical, "rev-parse", got.SHA+"^1", got.SHA+"^2"); parents != target+"\n"+source {
		t.Fatalf("parents %q, want target then source", parents)
	}
	if after := branchRefs(t, canonical); after != before {
		t.Fatalf("pinning moved branches:\n%s\nwant\n%s", after, before)
	}
	tested := runGit(t, canonical, "rev-parse", got.SHA+"^{tree}")

	// The same pair always makes the same commit.
	if again := mustPin(t, canonical, repoPipelinePin{ID: "8", Source: source, Target: target}); again.SHA != got.SHA {
		t.Fatalf("pinning the pair again made %s, want %s", again.SHA, got.SHA)
	}

	// A job's clone has both sides.
	job := cloneLikeAJob(t, canonical, "7")
	for _, f := range []string{"a.txt", "other.txt"} {
		if _, err := os.Stat(filepath.Join(job, f)); err != nil {
			t.Errorf("job lacks %s: %v", f, err)
		}
	}

	// A merge and a squash land the tree that was tested.
	for _, strategy := range []string{"merge", "squash"} {
		runGit(t, canonical, "update-ref", "refs/heads/main", target)
		rec := mergeRequest(t, canonical, repoMergeRequest{Source: "task/1", Strategy: strategy})
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: status %d: %s", strategy, rec.Code, rec.Body)
		}
		if landed := runGit(t, canonical, "rev-parse", "main^{tree}"); landed != tested {
			t.Errorf("%s landed tree %s, tested %s", strategy, landed, tested)
		}
	}
}

func TestPinPipelineMergeOfAFastForwardPinsTheSource(t *testing.T) {
	canonical := initCanonical(t)
	target := runGit(t, canonical, "rev-parse", "main")
	sandbox := seedSandbox(t, canonical, "task/1")
	commitFile(t, sandbox, "a.txt", "one")
	source := commitFile(t, sandbox, "b.txt", "two")
	publish(t, canonical, sandbox, "task/1")

	got := mustPin(t, canonical, repoPipelinePin{ID: "1", Source: source, Target: target})
	if got.SHA != source {
		t.Fatalf("expected the source itself, got %s", got.SHA)
	}
	tested := runGit(t, canonical, "rev-parse", got.SHA+"^{tree}")
	for _, strategy := range []string{"fast-forward", "merge", "squash"} {
		runGit(t, canonical, "update-ref", "refs/heads/main", target)
		if rec := mergeRequest(t, canonical, repoMergeRequest{Source: "task/1", Strategy: strategy}); rec.Code != http.StatusOK {
			t.Fatalf("%s: status %d: %s", strategy, rec.Code, rec.Body)
		}
		if landed := runGit(t, canonical, "rev-parse", "main^{tree}"); landed != tested {
			t.Errorf("%s landed tree %s, tested %s", strategy, landed, tested)
		}
	}

	// A source the target already has tests the target.
	runGit(t, canonical, "update-ref", "refs/heads/main", source)
	later := advanceMain(t, canonical, "c.txt", "three")
	if got := mustPin(t, canonical, repoPipelinePin{ID: "2", Source: source, Target: later}); got.SHA != later {
		t.Fatalf("expected the target, got %s", got.SHA)
	}
}

func TestPinPipelineMergeReportsWhatDoesNotMerge(t *testing.T) {
	canonical := initCanonical(t)
	sandbox := seedSandbox(t, canonical, "task/1")
	commitFile(t, sandbox, "shared.txt", "base")
	publish(t, canonical, sandbox, "task/1")
	mergeRequest(t, canonical, repoMergeRequest{Source: "task/1", Strategy: "fast-forward"})
	runGit(t, sandbox, "fetch", "-q", canonical, "main")
	runGit(t, sandbox, "reset", "-q", "--hard", "FETCH_HEAD")
	source := commitFile(t, sandbox, "shared.txt", "task's version")
	publish(t, canonical, sandbox, "task/1")
	target := advanceMain(t, canonical, "shared.txt", "main's version")
	before := branchRefs(t, canonical)

	// An earlier pin of the id goes, so nothing stale is bundled for it.
	mustPin(t, canonical, repoPipelinePin{ID: "3", Commit: target})
	code, got, body := pinPipeline(t, canonical, repoPipelinePin{ID: "3", Source: source, Target: target})
	if code != http.StatusOK || got.Conflict == nil || !slices.Equal(got.Conflict.Paths, []string{"shared.txt"}) || got.Ref != "" || got.SHA != "" {
		t.Fatalf("expected a conflict in shared.txt, got %d %s", code, body)
	}
	if out, err := gitOutputIn(context.Background(), canonical, "rev-parse", "--verify", "--quiet", "refs/devpad/pipelines/3"); err == nil {
		t.Fatalf("expected nothing pinned, got %s", out)
	}
	if rec := repoRequest(t, canonical, handleRepoPipelineBundle, http.MethodGet, "/api/repo/pipeline-bundle?id=3", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("expected no bundle after a conflict, got %d", rec.Code)
	}
	if after := branchRefs(t, canonical); after != before {
		t.Fatalf("branches moved:\n%s\nwant\n%s", after, before)
	}

	// Nor does a branch with no history in common.
	runGit(t, sandbox, "checkout", "-q", "--orphan", "lone")
	runGit(t, sandbox, "rm", "-rqf", ".")
	lone := commitFile(t, sandbox, "lone.txt", "alone")
	publish(t, canonical, sandbox, "lone")
	code, got, body = pinPipeline(t, canonical, repoPipelinePin{ID: "4", Source: lone, Target: target})
	if code != http.StatusOK || got.Conflict == nil || !strings.Contains(got.Conflict.Message, "no common history") {
		t.Fatalf("expected no common history, got %d %s", code, body)
	}
}

func TestPinPipelineRunsNothingFromTheRepo(t *testing.T) {
	canonical := initCanonical(t)
	marker := filepath.Join(t.TempDir(), "ran")
	sandbox := seedSandbox(t, canonical, "task/1")
	commitFile(t, sandbox, ".gitattributes", "*.txt merge=evil\n")
	base := commitFile(t, sandbox, "f.txt", "base")
	publish(t, canonical, sandbox, "task/1")
	if rec := mergeRequest(t, canonical, repoMergeRequest{Source: "task/1", Strategy: "fast-forward"}); rec.Code != http.StatusOK {
		t.Fatalf("fast-forward: %d %s", rec.Code, rec.Body)
	}
	conflicting := commitFile(t, sandbox, "f.txt", "task")
	publish(t, canonical, sandbox, "task/1")
	runGit(t, sandbox, "checkout", "-q", "-b", "task/2", base)
	clean := commitFile(t, sandbox, "g.txt", "clean")
	publish(t, canonical, sandbox, "task/2")
	target := advanceMain(t, canonical, "f.txt", "main")

	// Hooks the repo would run, with the canonical repo's own setting that
	// turns them off gone, and a merge driver the commits' .gitattributes
	// pick, which a git reading attributes from HEAD (attr.tree) would use.
	runGit(t, canonical, "config", "--unset", "core.hooksPath")
	for _, hook := range []string{"reference-transaction", "post-commit", "pre-commit"} {
		script := "#!/bin/sh\necho " + hook + " >> " + marker + "\n"
		if err := os.WriteFile(filepath.Join(canonical, "hooks", hook), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	runGit(t, canonical, "config", "merge.evil.driver", "echo driver >> "+marker+"; cp %B %A")
	runGit(t, canonical, "config", "attr.tree", "HEAD")

	// The driver would have resolved the conflict; it stays, as it does
	// for the merge itself.
	code, got, body := pinPipeline(t, canonical, repoPipelinePin{ID: "1", Source: conflicting, Target: target})
	if code != http.StatusOK || got.Conflict == nil {
		t.Fatalf("expected the conflict a merge reports, got %d %s", code, body)
	}
	if rec := mergeRequest(t, canonical, repoMergeRequest{Source: "task/1"}); rec.Code != http.StatusConflict {
		t.Fatalf("expected the merge to conflict as well, got %d %s", rec.Code, rec.Body)
	}
	// A clean merge makes a commit and moves a ref, a commit moves a ref,
	// and both refs are deleted.
	mustPin(t, canonical, repoPipelinePin{ID: "2", Source: clean, Target: target})
	mustPin(t, canonical, repoPipelinePin{ID: "3", Commit: conflicting})
	for _, id := range []string{"2", "3"} {
		if rec := repoRequest(t, canonical, handleRepoDeletePipelineRef, http.MethodDelete, "/api/repo/pipeline-ref?id="+id, nil); rec.Code != http.StatusOK {
			t.Fatalf("delete %s: %d %s", id, rec.Code, rec.Body)
		}
	}
	if ran, err := os.ReadFile(marker); err == nil {
		t.Fatalf("repo content ran: %s", ran)
	}
}

func TestPinPipelineRefusesWhatItCannotPin(t *testing.T) {
	canonical := initCanonical(t)
	main := runGit(t, canonical, "rev-parse", "main")
	runGit(t, canonical, "tag", "-a", "v1", "-m", "v1", main)
	tag := runGit(t, canonical, "rev-parse", "refs/tags/v1")
	missing := strings.Repeat("ab", 20)

	tests := []struct {
		name string
		req  repoPipelinePin
		want int
	}{
		{"no id", repoPipelinePin{Commit: main}, http.StatusBadRequest},
		{"upper-case id", repoPipelinePin{ID: "A1", Commit: main}, http.StatusBadRequest},
		{"id with a slash", repoPipelinePin{ID: "a/b", Commit: main}, http.StatusBadRequest},
		{"id climbing out", repoPipelinePin{ID: "../heads/main", Commit: main}, http.StatusBadRequest},
		{"id starting with a hyphen", repoPipelinePin{ID: "-1", Commit: main}, http.StatusBadRequest},
		{"long id", repoPipelinePin{ID: strings.Repeat("1", 65), Commit: main}, http.StatusBadRequest},
		{"nothing to pin", repoPipelinePin{ID: "1"}, http.StatusBadRequest},
		{"commit and merge", repoPipelinePin{ID: "1", Commit: main, Source: main, Target: main}, http.StatusBadRequest},
		{"source alone", repoPipelinePin{ID: "1", Source: main}, http.StatusBadRequest},
		{"short hash", repoPipelinePin{ID: "1", Commit: main[:12]}, http.StatusBadRequest},
		{"branch name", repoPipelinePin{ID: "1", Commit: "main"}, http.StatusBadRequest},
		{"upper-case hash", repoPipelinePin{ID: "1", Commit: strings.ToUpper(main)}, http.StatusBadRequest},
		{"option", repoPipelinePin{ID: "1", Commit: "--all"}, http.StatusBadRequest},
		{"tag", repoPipelinePin{ID: "1", Commit: tag}, http.StatusBadRequest},
		{"tag as a target", repoPipelinePin{ID: "1", Source: main, Target: tag}, http.StatusBadRequest},
		{"missing commit", repoPipelinePin{ID: "1", Commit: missing}, http.StatusNotFound},
		{"missing source", repoPipelinePin{ID: "1", Source: missing, Target: main}, http.StatusNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if code, _, body := pinPipeline(t, canonical, tt.req); code != tt.want {
				t.Fatalf("status %d, want %d: %s", code, tt.want, body)
			}
		})
	}
	if refs := runGit(t, canonical, "for-each-ref", "refs/devpad/"); refs != "" {
		t.Fatalf("expected nothing pinned, got %s", refs)
	}
	for _, target := range []string{"/api/repo/pipeline-bundle?id=", "/api/repo/pipeline-bundle?id=A", "/api/repo/pipeline-bundle?id=1/2"} {
		if rec := repoRequest(t, canonical, handleRepoPipelineBundle, http.MethodGet, target, nil); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status %d", target, rec.Code)
		}
	}
	if rec := repoRequest(t, canonical, handleRepoDeletePipelineRef, http.MethodDelete, "/api/repo/pipeline-ref?id=../heads/main", nil); rec.Code != http.StatusBadRequest {
		t.Errorf("delete outside the pipelines' refs: status %d", rec.Code)
	}
	if got := runGit(t, canonical, "rev-parse", "main"); got != main {
		t.Fatalf("main moved to %s", got)
	}
}

// TestPipelineClientAgainstTheAgent drives the agent's pipeline endpoints
// through devpad's client.
func TestPipelineClientAgainstTheAgent(t *testing.T) {
	canonical := initCanonical(t)
	sandbox := seedSandbox(t, canonical, "task/1")
	commitFile(t, sandbox, "shared.txt", "task")
	publish(t, canonical, sandbox, "task/1")
	source := runGit(t, canonical, "rev-parse", "task/1")
	target := advanceMain(t, canonical, "shared.txt", "main")

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/repo/pipeline-ref", handleRepoPinPipeline)
	mux.HandleFunc("DELETE /api/repo/pipeline-ref", handleRepoDeletePipelineRef)
	mux.HandleFunc("GET /api/repo/pipeline-bundle", handleRepoPipelineBundle)
	withWorkspaceRoot(t, canonical)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	host, portStr, _ := net.SplitHostPort(strings.TrimPrefix(srv.URL, "http://"))
	port, _ := strconv.Atoi(portStr)
	c := agent.NewClient(host, port, "")
	ctx := context.Background()

	pinned, err := c.RepoPinPipeline(ctx, agent.PipelinePin{ID: "5", Commit: source})
	if err != nil || pinned.SHA != source || pinned.Ref != pipelineid.Ref("5") {
		t.Fatalf("pin: %+v %v", pinned, err)
	}
	var bundle bytes.Buffer
	n, err := c.RepoPipelineBundle(ctx, "5", &bundle)
	if err != nil || n == 0 || int64(bundle.Len()) != n || !strings.HasPrefix(bundle.String(), "# v2 git bundle") {
		t.Fatalf("bundle: %d %v", n, err)
	}
	conflict, err := c.RepoPinPipeline(ctx, agent.PipelinePin{ID: "6", Source: source, Target: target})
	if err != nil || conflict.Conflict == nil || !slices.Equal(conflict.Conflict.Paths, []string{"shared.txt"}) {
		t.Fatalf("expected a conflict, not an error: %+v %v", conflict, err)
	}
	if err := c.RepoDeletePipelineRef(ctx, "5"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := c.RepoPipelineBundle(ctx, "5", &bundle); err == nil {
		t.Fatal("expected no bundle once deleted")
	}
}

// Only git's no is no: a commit it cannot read is an error, never a
// conflict or "not an ancestor".
func TestGitAnswerTellsNoFromFailure(t *testing.T) {
	canonical := initCanonical(t)
	withWorkspaceRoot(t, canonical)
	ctx := context.Background()
	main := runGit(t, canonical, "rev-parse", "main")
	if yes, err := isAncestor(ctx, main, main); err != nil || !yes {
		t.Fatalf("a commit is its own ancestor: %v %v", yes, err)
	}
	missing := strings.Repeat("ab", 20)
	if _, err := isAncestor(ctx, missing, main); err == nil {
		t.Fatal("expected an unreadable commit to be an error")
	}
	if _, err := haveMergeBase(ctx, main, missing); err == nil {
		t.Fatal("expected an unreadable commit to be an error")
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := isAncestor(cancelled, main, main); err == nil {
		t.Fatal("expected a stopped check to be an error")
	}
}
