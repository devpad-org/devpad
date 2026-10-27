package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"
)

// publish imports a sandbox branch into the canonical repo.
func publish(t *testing.T, canonical, sandbox, branch string) {
	t.Helper()
	bundle := bundleFrom(t, sandbox, branch)
	rec := repoRequest(t, canonical, handleRepoImport, http.MethodPost, "/api/repo/import?branch="+branch, bytes.NewReader(bundle))
	if rec.Code != http.StatusOK {
		t.Fatalf("import status %d: %s", rec.Code, rec.Body)
	}
}

// advanceMain commits a file to the canonical main branch, as an earlier
// merge would have.
func advanceMain(t *testing.T, canonical, name, content string) string {
	t.Helper()
	clone := t.TempDir()
	runGit(t, clone, "clone", "--quiet", canonical, ".")
	sha := commitFile(t, clone, name, content)
	runGit(t, clone, "push", "--quiet", "origin", "HEAD:main")
	return sha
}

func mergeRequest(t *testing.T, canonical string, req repoMergeRequest) *httptest.ResponseRecorder {
	t.Helper()
	if req.Target == "" {
		req.Target = "main"
	}
	body, _ := json.Marshal(req)
	return repoRequest(t, canonical, handleRepoMerge, http.MethodPost, "/api/repo/merge", bytes.NewReader(body))
}

func TestRepoDiffListsChangesSinceMergeBase(t *testing.T) {
	canonical := initCanonical(t)
	sandbox := seedSandbox(t, canonical, "task/1")
	commitFile(t, sandbox, "a.txt", "one\ntwo\n")
	if err := os.WriteFile(filepath.Join(sandbox, "logo.bin"), []byte{0, 1, 2, 0, 3}, 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, sandbox, "add", "logo.bin")
	runGit(t, sandbox, "commit", "-qm", "add logo")
	runGit(t, sandbox, "mv", "a.txt", "b.txt")
	runGit(t, sandbox, "commit", "-qm", "rename")
	publish(t, canonical, sandbox, "task/1")
	// Work merged to main after the branch was cut is not part of its diff.
	advanceMain(t, canonical, "other.txt", "main work")

	rec := repoRequest(t, canonical, handleRepoDiff, http.MethodGet, "/api/repo/diff?base=main&head=task/1", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("diff status %d: %s", rec.Code, rec.Body)
	}
	var diff repoDiff
	if err := json.Unmarshal(rec.Body.Bytes(), &diff); err != nil {
		t.Fatalf("decoding diff: %v", err)
	}
	want := []repoDiffFile{
		{Path: "b.txt", Status: "A", Additions: 2},
		{Path: "logo.bin", Status: "A", Binary: true},
	}
	if !slices.Equal(diff.Files, want) {
		t.Fatalf("files = %+v, want %+v", diff.Files, want)
	}
	if diff.MergeBase == diff.BaseSHA || diff.HeadSHA != runGit(t, canonical, "rev-parse", "task/1") {
		t.Fatalf("unexpected shas: %+v", diff)
	}

	// Against its own parent, the rename shows as one.
	parent := runGit(t, canonical, "rev-parse", "task/1~1")
	rec = repoRequest(t, canonical, handleRepoDiff, http.MethodGet, "/api/repo/diff?base="+parent+"&head=task/1", nil)
	json.Unmarshal(rec.Body.Bytes(), &diff)
	if len(diff.Files) != 1 || diff.Files[0] != (repoDiffFile{Path: "b.txt", OldPath: "a.txt", Status: "R"}) {
		t.Fatalf("expected one rename, got %+v", diff.Files)
	}
}

func TestRepoLogListsCommitsBaseLacks(t *testing.T) {
	canonical := initCanonical(t)
	sandbox := seedSandbox(t, canonical, "task/1")
	first := commitFile(t, sandbox, "a.txt", "one")
	second := commitFile(t, sandbox, "b.txt", "two")
	publish(t, canonical, sandbox, "task/1")
	// Work merged to main after the branch was cut is not listed.
	advanceMain(t, canonical, "other.txt", "main work")

	get := func(query string) (int, repoLog) {
		rec := repoRequest(t, canonical, handleRepoLog, http.MethodGet, "/api/repo/log?"+query, nil)
		var log repoLog
		json.Unmarshal(rec.Body.Bytes(), &log)
		return rec.Code, log
	}
	code, log := get("base=main&head=task/1")
	if code != http.StatusOK || log.Truncated || len(log.Commits) != 2 {
		t.Fatalf("log: %d %+v", code, log)
	}
	if c := log.Commits[0]; c.SHA != second || c.Subject != "add b.txt" || !slices.Equal(c.Parents, []string{first}) || c.Author == "" || c.AuthoredAt.IsZero() {
		t.Fatalf("unexpected newest commit: %+v", c)
	}
	if log.Commits[1].SHA != first {
		t.Fatalf("expected the older commit last, got %+v", log.Commits[1])
	}

	// A commit hash pins either side.
	if code, log := get("base=" + first + "&head=" + second); code != http.StatusOK || len(log.Commits) != 1 || log.Commits[0].SHA != second {
		t.Fatalf("log between commits: %d %+v", code, log)
	}
	if code, log := get("base=task/1&head=task/1"); code != http.StatusOK || log.Commits == nil || len(log.Commits) != 0 {
		t.Fatalf("expected an empty list, got %d %+v", code, log)
	}
	for _, query := range []string{"base=main&head=nope", "base=--all&head=task/1"} {
		if code, _ := get(query); code == http.StatusOK {
			t.Errorf("%s: expected an error", query)
		}
	}
}

func TestParseRepoLogCapsSubjects(t *testing.T) {
	long := strings.Repeat("é", maxCommitSubjectBytes)
	out := "abc\x1fp1 p2\x1fAda\x1f1700000000\x1f" + long + "\x00\ndef\x1f\x1fBo\x1fnot-a-time\x1fsubject\x00"
	log := parseRepoLog(out)
	if len(log.Commits) != 2 {
		t.Fatalf("expected two commits, got %+v", log.Commits)
	}
	if c := log.Commits[0]; len(c.Subject) > maxCommitSubjectBytes || !utf8.ValidString(c.Subject) || !slices.Equal(c.Parents, []string{"p1", "p2"}) || c.AuthoredAt.Unix() != 1700000000 {
		t.Fatalf("unexpected first commit: %+v", c)
	}
	if c := log.Commits[1]; c.SHA != "def" || c.Parents == nil || len(c.Parents) != 0 || !c.AuthoredAt.IsZero() {
		t.Fatalf("unexpected root commit: %+v", c)
	}
}

func TestRepoFileReturnsContentAtRevision(t *testing.T) {
	canonical := initCanonical(t)
	sandbox := seedSandbox(t, canonical, "task/1")
	if err := os.Mkdir(filepath.Join(sandbox, "notes"), 0o755); err != nil {
		t.Fatal(err)
	}
	first := commitFile(t, sandbox, "notes/a.txt", "v1")
	commitFile(t, sandbox, "notes/a.txt", "v2")
	publish(t, canonical, sandbox, "task/1")

	get := func(query string) (int, string) {
		rec := repoRequest(t, canonical, handleRepoFile, http.MethodGet, "/api/repo/file?"+query, nil)
		return rec.Code, rec.Body.String()
	}
	if code, body := get("rev=task/1&path=notes/a.txt"); code != http.StatusOK || body != "v2" {
		t.Fatalf("at branch: %d %q", code, body)
	}
	if code, body := get("rev=" + first + "&path=notes/a.txt"); code != http.StatusOK || body != "v1" {
		t.Fatalf("at commit: %d %q", code, body)
	}

	tests := []struct {
		query string
		want  int
	}{
		{"rev=task/1&path=missing.txt", http.StatusNotFound},
		{"rev=nope&path=notes/a.txt", http.StatusNotFound},
		{"rev=" + strings.Repeat("0", 40) + "&path=notes/a.txt", http.StatusNotFound},
		{"rev=--all&path=notes/a.txt", http.StatusBadRequest},
		{"rev=task/1&path=../etc/passwd", http.StatusBadRequest},
		{"rev=task/1&path=/etc/passwd", http.StatusBadRequest},
		{"rev=task/1&path=notes/../notes/a.txt", http.StatusBadRequest},
		{"rev=task/1&path=", http.StatusBadRequest},
	}
	for _, tt := range tests {
		if code, body := get(tt.query); code != tt.want {
			t.Errorf("%s: expected %d, got %d %s", tt.query, tt.want, code, body)
		}
	}
}

func TestRepoMergeCreatesMergeCommit(t *testing.T) {
	canonical := initCanonical(t)
	sandbox := seedSandbox(t, canonical, "task/1")
	head := commitFile(t, sandbox, "feature.txt", "feature")
	publish(t, canonical, sandbox, "task/1")
	mainBefore := advanceMain(t, canonical, "other.txt", "main work")

	rec := mergeRequest(t, canonical, repoMergeRequest{Source: "task/1", ExpectedSourceSHA: head, Name: "Reviewer", Email: "reviewer@example.com", Message: "Merge task 1"})
	if rec.Code != http.StatusOK {
		t.Fatalf("merge status %d: %s", rec.Code, rec.Body)
	}
	var result repoMergeResult
	json.Unmarshal(rec.Body.Bytes(), &result)
	if got := runGit(t, canonical, "rev-parse", "main"); got != result.SHA {
		t.Fatalf("main = %s, merge result %s", got, result.SHA)
	}
	if parents := runGit(t, canonical, "log", "-1", "--format=%P", "main"); parents != mainBefore+" "+head {
		t.Fatalf("merge parents = %q", parents)
	}
	if files := runGit(t, canonical, "ls-tree", "--name-only", "main"); files != "feature.txt\nother.txt" {
		t.Fatalf("merged tree = %q", files)
	}
	if author := runGit(t, canonical, "log", "-1", "--format=%an <%ae>|%s", "main"); author != "Reviewer <reviewer@example.com>|Merge task 1" {
		t.Fatalf("merge commit = %q", author)
	}
	if got := runGit(t, canonical, "rev-parse", "task/1"); got != head {
		t.Fatal("merge moved the source branch")
	}

	rec = mergeRequest(t, canonical, repoMergeRequest{Source: "task/1"})
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "nothing to merge") {
		t.Fatalf("expected nothing to merge, got %d %s", rec.Code, rec.Body)
	}
}

func TestRepoMergeSquashAndFastForward(t *testing.T) {
	canonical := initCanonical(t)
	sandbox := seedSandbox(t, canonical, "task/1")
	commitFile(t, sandbox, "a.txt", "a")
	head := commitFile(t, sandbox, "b.txt", "b")
	publish(t, canonical, sandbox, "task/1")
	mainBefore := runGit(t, canonical, "rev-parse", "main")

	rec := mergeRequest(t, canonical, repoMergeRequest{Source: "task/1", Strategy: "fast-forward"})
	if rec.Code != http.StatusOK || runGit(t, canonical, "rev-parse", "main") != head {
		t.Fatalf("fast-forward: %d %s", rec.Code, rec.Body)
	}

	other := seedSandbox(t, canonical, "task/2")
	commitFile(t, other, "c.txt", "c")
	commitFile(t, other, "d.txt", "d")
	publish(t, canonical, other, "task/2")
	advanceMain(t, canonical, "e.txt", "e")
	if rec := mergeRequest(t, canonical, repoMergeRequest{Source: "task/2", Strategy: "fast-forward"}); rec.Code != http.StatusConflict {
		t.Fatalf("diverged fast-forward: expected 409, got %d %s", rec.Code, rec.Body)
	}
	mainBefore = runGit(t, canonical, "rev-parse", "main")
	rec = mergeRequest(t, canonical, repoMergeRequest{Source: "task/2", Strategy: "squash", Message: "Squash task 2"})
	if rec.Code != http.StatusOK {
		t.Fatalf("squash: %d %s", rec.Code, rec.Body)
	}
	if parents := runGit(t, canonical, "log", "-1", "--format=%P", "main"); parents != mainBefore {
		t.Fatalf("squash commit should have one parent, got %q", parents)
	}
	if files := runGit(t, canonical, "ls-tree", "--name-only", "main"); files != "a.txt\nb.txt\nc.txt\nd.txt\ne.txt" {
		t.Fatalf("squashed tree = %q", files)
	}
}

func TestRepoMergeRefusesConflictsAndMovedSource(t *testing.T) {
	canonical := initCanonical(t)
	sandbox := seedSandbox(t, canonical, "task/1")
	head := commitFile(t, sandbox, "shared.txt", "from the task")
	publish(t, canonical, sandbox, "task/1")
	advanceMain(t, canonical, "shared.txt", "from main")
	mainBefore := runGit(t, canonical, "rev-parse", "main")

	rec := mergeRequest(t, canonical, repoMergeRequest{Source: "task/1"})
	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409 for a conflict, got %d %s", rec.Code, rec.Body)
	}
	var body struct {
		Conflicts []string `json:"conflicts"`
	}
	json.Unmarshal(rec.Body.Bytes(), &body)
	if !slices.Equal(body.Conflicts, []string{"shared.txt"}) {
		t.Fatalf("conflicts = %v", body.Conflicts)
	}

	rec = mergeRequest(t, canonical, repoMergeRequest{Source: "task/1", ExpectedSourceSHA: strings.Repeat("a", 40)})
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "since it was reviewed") {
		t.Fatalf("expected 409 for a moved source, got %d %s", rec.Code, rec.Body)
	}
	if got := runGit(t, canonical, "rev-parse", "main"); got != mainBefore {
		t.Fatal("a refused merge moved main")
	}
	_ = head

	tests := []struct {
		name string
		req  repoMergeRequest
		want int
	}{
		{"same branch", repoMergeRequest{Source: "main"}, http.StatusBadRequest},
		{"flag source", repoMergeRequest{Source: "--all"}, http.StatusBadRequest},
		{"unknown source", repoMergeRequest{Source: "nope"}, http.StatusNotFound},
		{"hash source", repoMergeRequest{Source: head}, http.StatusNotFound},
		{"bad strategy", repoMergeRequest{Source: "task/1", Strategy: "octopus"}, http.StatusBadRequest},
		{"newline in name", repoMergeRequest{Source: "task/1", Name: "x\ny"}, http.StatusBadRequest},
		{"brackets in email", repoMergeRequest{Source: "task/1", Email: "a>b@example.com"}, http.StatusBadRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if rec := mergeRequest(t, canonical, tt.req); rec.Code != tt.want {
				t.Fatalf("expected %d, got %d %s", tt.want, rec.Code, rec.Body)
			}
		})
	}
}

func TestRepoPushUpdatesUpstreamWithoutForcing(t *testing.T) {
	upstream := t.TempDir()
	runGit(t, upstream, "init", "--quiet", "--bare", "--initial-branch=main")
	seed := t.TempDir()
	runGit(t, seed, "clone", "--quiet", upstream, ".")
	commitFile(t, seed, "README.md", "hello")
	runGit(t, seed, "push", "--quiet", "origin", "HEAD:main")

	canonical := t.TempDir()
	rec := repoRequest(t, canonical, handleRepoInit, http.MethodPost, "/api/repo/init", strings.NewReader(`{"defaultBranch":"main","upstreamUrl":"`+upstream+`"}`))
	if rec.Code != http.StatusOK {
		t.Fatalf("init: %d %s", rec.Code, rec.Body)
	}
	merged := advanceMain(t, canonical, "merged.txt", "merged")

	rec = repoRequest(t, canonical, handleRepoPush, http.MethodPost, "/api/repo/push?branch=main", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("push: %d %s", rec.Code, rec.Body)
	}
	if got := runGit(t, upstream, "rev-parse", "main"); got != merged {
		t.Fatalf("upstream main = %s, want %s", got, merged)
	}

	// Someone else pushes upstream; devpad must not overwrite their work.
	commitFile(t, seed, "theirs.txt", "theirs")
	runGit(t, seed, "pull", "--quiet", "--no-rebase", "origin", "main")
	runGit(t, seed, "push", "--quiet", "origin", "HEAD:main")
	theirs := runGit(t, upstream, "rev-parse", "main")
	advanceMain(t, canonical, "ours.txt", "ours")
	rec = repoRequest(t, canonical, handleRepoPush, http.MethodPost, "/api/repo/push?branch=main", nil)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("diverged push: expected 502, got %d %s", rec.Code, rec.Body)
	}
	if got := runGit(t, upstream, "rev-parse", "main"); got != theirs {
		t.Fatal("push overwrote upstream work")
	}

	local := initCanonical(t)
	if rec := repoRequest(t, local, handleRepoPush, http.MethodPost, "/api/repo/push?branch=main", nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("push without upstream: expected 400, got %d %s", rec.Code, rec.Body)
	}
}

func TestRepoSetHeadMovesTheDefaultBranch(t *testing.T) {
	canonical := initCanonical(t)
	head := runGit(t, canonical, "rev-parse", "main")
	runGit(t, canonical, "branch", "develop", head)

	rec := repoRequest(t, canonical, handleRepoSetHead, http.MethodPost, "/api/repo/head?branch=develop", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("set head: %d %s", rec.Code, rec.Body)
	}
	if got := runGit(t, canonical, "symbolic-ref", "--short", "HEAD"); got != "develop" {
		t.Fatalf("HEAD = %s, want develop", got)
	}
	// Imports into the new default branch are refused.
	rec = repoRequest(t, canonical, handleRepoImport, http.MethodPost, "/api/repo/import?branch=develop", strings.NewReader(""))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("import into develop: expected 403, got %d %s", rec.Code, rec.Body)
	}

	for _, branch := range []string{"nope", "-x", "a..b"} {
		rec := repoRequest(t, canonical, handleRepoSetHead, http.MethodPost, "/api/repo/head?branch="+branch, nil)
		if rec.Code != http.StatusNotFound && rec.Code != http.StatusBadRequest {
			t.Fatalf("branch %q: expected 400 or 404, got %d %s", branch, rec.Code, rec.Body)
		}
	}
	if got := runGit(t, canonical, "symbolic-ref", "--short", "HEAD"); got != "develop" {
		t.Fatalf("refused requests moved HEAD to %s", got)
	}
}

func TestRepoFetchBaseRefreshesSandboxCopy(t *testing.T) {
	canonical := initCanonical(t)
	sandbox := seedSandbox(t, canonical, "task/1")
	work := commitFile(t, sandbox, "work.txt", "work")
	latest := advanceMain(t, canonical, "merged.txt", "merged")

	rec := repoRequest(t, sandbox, handleRepoFetchBase, http.MethodPost, "/api/repo/fetch-base?branch=main", bytes.NewReader(bundleFrom(t, canonical, "main")))
	if rec.Code != http.StatusOK {
		t.Fatalf("fetch-base: %d %s", rec.Code, rec.Body)
	}
	if got := runGit(t, sandbox, "rev-parse", "devpad/main"); got != latest {
		t.Fatalf("devpad/main = %s, want %s", got, latest)
	}
	if got := runGit(t, sandbox, "rev-parse", "HEAD"); got != work || runGit(t, sandbox, "branch", "--show-current") != "task/1" {
		t.Fatal("fetch-base touched the sandbox's own branch")
	}
	if remotes := runGit(t, sandbox, "remote"); remotes != "" {
		t.Fatalf("fetch-base added a remote: %q", remotes)
	}

	empty := t.TempDir()
	if rec := repoRequest(t, empty, handleRepoFetchBase, http.MethodPost, "/api/repo/fetch-base?branch=main", bytes.NewReader(nil)); rec.Code != http.StatusConflict {
		t.Fatalf("fetch-base without a repo: expected 409, got %d %s", rec.Code, rec.Body)
	}
}
