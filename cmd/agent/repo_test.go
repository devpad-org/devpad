package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.com",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func repoRequest(t *testing.T, root string, handler http.HandlerFunc, method, target string, body io.Reader) *httptest.ResponseRecorder {
	t.Helper()
	withWorkspaceRoot(t, root)
	rec := httptest.NewRecorder()
	handler(rec, httptest.NewRequest(method, target, body))
	return rec
}

// upstreamRequest is repoRequest for an endpoint that uses the upstream,
// naming origin's URL as the one expected, as devpad does.
func upstreamRequest(t *testing.T, root string, handler http.HandlerFunc, method, target string, body io.Reader) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, body)
	if out, err := exec.Command("git", "-C", root, "config", "--get", "remote.origin.url").Output(); err == nil {
		req.Header.Set(upstreamHeader, strings.TrimSpace(string(out)))
	}
	withWorkspaceRoot(t, root)
	rec := httptest.NewRecorder()
	handler(rec, req)
	return rec
}

// initCanonical creates a canonical bare repo through the init endpoint.
func initCanonical(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	rec := repoRequest(t, root, handleRepoInit, http.MethodPost, "/api/repo/init", strings.NewReader(`{"defaultBranch":"main"}`))
	if rec.Code != http.StatusOK {
		t.Fatalf("init status %d: %s", rec.Code, rec.Body)
	}
	return root
}

func bundleFrom(t *testing.T, root, branch string) []byte {
	t.Helper()
	rec := repoRequest(t, root, handleRepoBundle, http.MethodGet, "/api/repo/bundle?branch="+branch, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("bundle status %d: %s", rec.Code, rec.Body)
	}
	return rec.Body.Bytes()
}

// seedSandbox seeds an empty sandbox from the canonical repo's main branch.
func seedSandbox(t *testing.T, canonical, branch string) string {
	t.Helper()
	bundle := bundleFrom(t, canonical, "main")
	sandbox := t.TempDir()
	rec := repoRequest(t, sandbox, handleRepoSeed, http.MethodPost, "/api/repo/seed?base=main&branch="+branch+"&name=Agent&email=agent@example.com", bytes.NewReader(bundle))
	if rec.Code != http.StatusOK {
		t.Fatalf("seed status %d: %s", rec.Code, rec.Body)
	}
	return sandbox
}

func commitFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatalf("writing %s: %v", name, err)
	}
	runGit(t, dir, "add", name)
	runGit(t, dir, "commit", "-m", "add "+name)
	return runGit(t, dir, "rev-parse", "HEAD")
}

func TestRepoInitCreatesBareRepoWithInitialCommit(t *testing.T) {
	root := initCanonical(t)

	if got := runGit(t, root, "rev-parse", "--is-bare-repository"); got != "true" {
		t.Fatalf("expected bare repo, got %q", got)
	}
	if got := runGit(t, root, "config", "core.hooksPath"); got != "/dev/null" {
		t.Fatalf("expected hooks disabled, got %q", got)
	}
	if got := runGit(t, root, "symbolic-ref", "--short", "HEAD"); got != "main" {
		t.Fatalf("expected HEAD on main, got %q", got)
	}
	runGit(t, root, "rev-parse", "--verify", "refs/heads/main")

	// A second init must not fail or move main.
	before := runGit(t, root, "rev-parse", "main")
	rec := repoRequest(t, root, handleRepoInit, http.MethodPost, "/api/repo/init", strings.NewReader(`{"defaultBranch":"main"}`))
	if rec.Code != http.StatusOK {
		t.Fatalf("re-init status %d: %s", rec.Code, rec.Body)
	}
	if after := runGit(t, root, "rev-parse", "main"); after != before {
		t.Fatalf("re-init moved main from %s to %s", before, after)
	}
}

func TestRepoInitClonesUpstream(t *testing.T) {
	upstream := t.TempDir()
	runGit(t, upstream, "init", "--initial-branch=trunk")
	sha := commitFile(t, upstream, "README.md", "hello")

	root := t.TempDir()
	body := `{"defaultBranch":"trunk","upstreamUrl":"` + upstream + `"}`
	rec := repoRequest(t, root, handleRepoInit, http.MethodPost, "/api/repo/init", strings.NewReader(body))
	if rec.Code != http.StatusOK {
		t.Fatalf("init status %d: %s", rec.Code, rec.Body)
	}
	var refs repoRefs
	if err := json.Unmarshal(rec.Body.Bytes(), &refs); err != nil {
		t.Fatalf("decoding refs: %v", err)
	}
	if refs.DefaultBranch != "trunk" || len(refs.Branches) != 1 || refs.Branches[0].SHA != sha {
		t.Fatalf("unexpected refs: %+v", refs)
	}
}

func TestRepoInitRejectsBadInput(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{"missing branch", `{}`},
		{"flag branch", `{"defaultBranch":"-x"}`},
		{"invalid branch", `{"defaultBranch":"a..b"}`},
		{"flag upstream", `{"defaultBranch":"main","upstreamUrl":"--upload-pack=evil"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := repoRequest(t, t.TempDir(), handleRepoInit, http.MethodPost, "/api/repo/init", strings.NewReader(tt.body))
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body)
			}
		})
	}
}

func TestRepoSeedCreatesBranchWithoutRemote(t *testing.T) {
	canonical := initCanonical(t)
	sandbox := seedSandbox(t, canonical, "sandbox-1")

	if got := runGit(t, sandbox, "branch", "--show-current"); got != "sandbox-1" {
		t.Fatalf("expected sandbox-1 checked out, got %q", got)
	}
	if got := runGit(t, sandbox, "remote"); got != "" {
		t.Fatalf("expected no remotes, got %q", got)
	}
	// The base is kept as the project's copy of it.
	if got, want := runGit(t, sandbox, "rev-parse", "refs/remotes/devpad/main"), runGit(t, canonical, "rev-parse", "main"); got != want {
		t.Fatalf("devpad/main = %s, want %s", got, want)
	}
	if got := runGit(t, sandbox, "config", "user.email"); got != "agent@example.com" {
		t.Fatalf("expected identity set, got %q", got)
	}
}

func TestRepoSeedResumesExistingBranch(t *testing.T) {
	canonical := initCanonical(t)
	first := seedSandbox(t, canonical, "feature")
	sha := commitFile(t, first, "feature.txt", "work")
	rec := repoRequest(t, canonical, handleRepoImport, http.MethodPost, "/api/repo/import?branch=feature", bytes.NewReader(bundleFrom(t, first, "feature")))
	if rec.Code != http.StatusOK {
		t.Fatalf("import status %d: %s", rec.Code, rec.Body)
	}

	second := t.TempDir()
	rec = repoRequest(t, second, handleRepoSeed, http.MethodPost, "/api/repo/seed?base=feature&branch=feature", bytes.NewReader(bundleFrom(t, canonical, "feature")))
	if rec.Code != http.StatusOK {
		t.Fatalf("seed status %d: %s", rec.Code, rec.Body)
	}
	if got := runGit(t, second, "branch", "--show-current"); got != "feature" {
		t.Fatalf("expected feature checked out, got %q", got)
	}
	if got := runGit(t, second, "rev-parse", "HEAD"); got != sha {
		t.Fatalf("expected HEAD %s, got %s", sha, got)
	}
}

func TestRepoSeedRefusesExistingRepo(t *testing.T) {
	canonical := initCanonical(t)
	sandbox := seedSandbox(t, canonical, "sandbox-1")

	bundle := bundleFrom(t, canonical, "main")
	rec := repoRequest(t, sandbox, handleRepoSeed, http.MethodPost, "/api/repo/seed?base=main&branch=again", bytes.NewReader(bundle))
	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", rec.Code, rec.Body)
	}
}

func TestRepoImportPublishesSandboxBranch(t *testing.T) {
	canonical := initCanonical(t)
	sandbox := seedSandbox(t, canonical, "sandbox-1")
	mainBefore := runGit(t, canonical, "rev-parse", "main")
	sha := commitFile(t, sandbox, "feature.txt", "work")

	bundle := bundleFrom(t, sandbox, "sandbox-1")
	rec := repoRequest(t, canonical, handleRepoImport, http.MethodPost, "/api/repo/import?branch=sandbox-1", bytes.NewReader(bundle))
	if rec.Code != http.StatusOK {
		t.Fatalf("import status %d: %s", rec.Code, rec.Body)
	}
	var result repoBranchResult
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatalf("decoding result: %v", err)
	}
	if result.SHA != sha {
		t.Fatalf("expected imported sha %s, got %s", sha, result.SHA)
	}
	if got := runGit(t, canonical, "rev-parse", "sandbox-1"); got != sha {
		t.Fatalf("canonical sandbox-1 = %s, want %s", got, sha)
	}
	if got := runGit(t, canonical, "rev-parse", "main"); got != mainBefore {
		t.Fatalf("import moved main")
	}
	if got := runGit(t, canonical, "for-each-ref", quarantineRefPrefix); got != "" {
		t.Fatalf("quarantine refs left behind: %s", got)
	}
}

func TestRepoImportOnlyWritesNamedBranch(t *testing.T) {
	canonical := initCanonical(t)
	sandbox := seedSandbox(t, canonical, "sandbox-1")
	commitFile(t, sandbox, "feature.txt", "work")
	mainBefore := runGit(t, canonical, "rev-parse", "main")

	// The sandbox moves its own main and tags a bundle with every ref. Only
	// the branch devpad names may land in the canonical repo.
	runGit(t, sandbox, "branch", "-f", "main", "sandbox-1")
	runGit(t, sandbox, "branch", "other")
	path := filepath.Join(t.TempDir(), "all.bundle")
	runGit(t, sandbox, "bundle", "create", path, "--all")
	all, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading bundle: %v", err)
	}

	rec := repoRequest(t, canonical, handleRepoImport, http.MethodPost, "/api/repo/import?branch=sandbox-1", bytes.NewReader(all))
	if rec.Code != http.StatusOK {
		t.Fatalf("import status %d: %s", rec.Code, rec.Body)
	}
	if got := runGit(t, canonical, "rev-parse", "main"); got != mainBefore {
		t.Fatalf("import moved main to %s", got)
	}
	if got := runGit(t, canonical, "for-each-ref", "--format=%(refname)", "refs/heads/"); got != "refs/heads/main\nrefs/heads/sandbox-1" {
		t.Fatalf("unexpected canonical branches:\n%s", got)
	}
}

func TestRepoImportRejections(t *testing.T) {
	canonical := initCanonical(t)
	sandbox := seedSandbox(t, canonical, "sandbox-1")
	commitFile(t, sandbox, "feature.txt", "work")
	good := bundleFrom(t, sandbox, "sandbox-1")

	tests := []struct {
		name   string
		branch string
		body   []byte
		want   int
	}{
		{"default branch", "main", good, http.StatusForbidden},
		{"flag branch", "-f", good, http.StatusBadRequest},
		{"invalid branch", "a..b", good, http.StatusBadRequest},
		{"branch missing from bundle", "elsewhere", good, http.StatusBadRequest},
		{"corrupt bundle", "sandbox-1", []byte("# v2 git bundle\nnot really\n"), http.StatusBadRequest},
		{"truncated bundle", "sandbox-1", good[:len(good)/2], http.StatusBadRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := repoRequest(t, canonical, handleRepoImport, http.MethodPost, "/api/repo/import?branch="+tt.branch, bytes.NewReader(tt.body))
			if rec.Code != tt.want {
				t.Fatalf("expected %d, got %d: %s", tt.want, rec.Code, rec.Body)
			}
			if got := runGit(t, canonical, "for-each-ref", "--format=%(refname)", "refs/"); got != "refs/heads/main" {
				t.Fatalf("rejected import changed refs:\n%s", got)
			}
		})
	}
}

func TestRepoImportRejectsOversizedBundle(t *testing.T) {
	canonical := initCanonical(t)
	body := io.LimitReader(zeroReader{}, maxBundleBytes+1)
	rec := repoRequest(t, canonical, handleRepoImport, http.MethodPost, "/api/repo/import?branch=sandbox-1", body)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body)
	}
}

type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) {
	clear(p)
	return len(p), nil
}

func TestRepoBundleUnknownBranch(t *testing.T) {
	canonical := initCanonical(t)
	rec := repoRequest(t, canonical, handleRepoBundle, http.MethodGet, "/api/repo/bundle?branch=nope", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rec.Code, rec.Body)
	}
}

func TestRepoCompareCountsDivergence(t *testing.T) {
	canonical := initCanonical(t)
	sandbox := seedSandbox(t, canonical, "task/1")

	importBranch := func() {
		t.Helper()
		bundle := bundleFrom(t, sandbox, "task/1")
		rec := repoRequest(t, canonical, handleRepoImport, http.MethodPost, "/api/repo/import?branch=task/1", bytes.NewReader(bundle))
		if rec.Code != http.StatusOK {
			t.Fatalf("import status %d: %s", rec.Code, rec.Body)
		}
	}
	compare := func() repoComparison {
		t.Helper()
		rec := repoRequest(t, canonical, handleRepoCompare, http.MethodGet, "/api/repo/compare?base=main&head=task/1", nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("compare status %d: %s", rec.Code, rec.Body)
		}
		var cmp repoComparison
		if err := json.Unmarshal(rec.Body.Bytes(), &cmp); err != nil {
			t.Fatalf("decoding comparison: %v", err)
		}
		return cmp
	}

	importBranch()
	if cmp := compare(); cmp.Ahead != 0 || cmp.Behind != 0 || cmp.HeadSHA != cmp.BaseSHA {
		t.Fatalf("fresh branch should match main: %+v", cmp)
	}

	commitFile(t, sandbox, "a.txt", "a")
	head := commitFile(t, sandbox, "b.txt", "b")
	importBranch()
	cmp := compare()
	if cmp.Ahead != 2 || cmp.Behind != 0 || cmp.HeadSHA != head || cmp.MergeBase != cmp.BaseSHA {
		t.Fatalf("unexpected comparison after two commits: %+v", cmp)
	}
}

func TestRepoCompareAgainstACommit(t *testing.T) {
	canonical := initCanonical(t)
	sandbox := seedSandbox(t, canonical, "base")
	commitFile(t, sandbox, "a.txt", "a")
	importAs := func(branch string) {
		t.Helper()
		rec := repoRequest(t, canonical, handleRepoImport, http.MethodPost, "/api/repo/import?branch="+branch, bytes.NewReader(bundleFrom(t, sandbox, branch)))
		if rec.Code != http.StatusOK {
			t.Fatalf("import status %d: %s", rec.Code, rec.Body)
		}
	}
	importAs("base")
	baseSHA := runGit(t, canonical, "rev-parse", "base")

	// A task branch made from base, with nothing new, is ahead of main but
	// not of the commit it was made from.
	runGit(t, sandbox, "branch", "task/1", "base")
	importAs("task/1")
	compare := func(base string) repoComparison {
		t.Helper()
		rec := repoRequest(t, canonical, handleRepoCompare, http.MethodGet, "/api/repo/compare?base="+base+"&head=task/1", nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("compare status %d: %s", rec.Code, rec.Body)
		}
		var cmp repoComparison
		if err := json.Unmarshal(rec.Body.Bytes(), &cmp); err != nil {
			t.Fatalf("decoding comparison: %v", err)
		}
		return cmp
	}
	if cmp := compare("main"); cmp.Ahead != 1 {
		t.Fatalf("expected task/1 ahead of main by the base's commit, got %+v", cmp)
	}
	if cmp := compare(baseSHA); cmp.Ahead != 0 || cmp.BaseSHA != baseSHA {
		t.Fatalf("expected task/1 level with its base commit, got %+v", cmp)
	}
}

func TestRepoCompareRejections(t *testing.T) {
	canonical := initCanonical(t)
	tests := []struct {
		name   string
		query  string
		status int
	}{
		{"missing head", "base=main", http.StatusBadRequest},
		{"flag", "base=main&head=--all", http.StatusBadRequest},
		{"unknown head", "base=main&head=nope", http.StatusNotFound},
		{"unknown base", "base=nope&head=main", http.StatusNotFound},
		{"unknown commit", "base=" + strings.Repeat("ab", 20) + "&head=main", http.StatusNotFound},
		{"short hash is a branch name", "base=abc123&head=main", http.StatusNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := repoRequest(t, canonical, handleRepoCompare, http.MethodGet, "/api/repo/compare?"+tt.query, nil)
			if rec.Code != tt.status {
				t.Fatalf("expected %d, got %d: %s", tt.status, rec.Code, rec.Body)
			}
		})
	}
}

func TestRepoBundleAndFetchSeveralBranchesAtOnce(t *testing.T) {
	canonical := initCanonical(t)
	work := seedSandbox(t, canonical, "main")
	tree := runGit(t, canonical, "rev-parse", "main^{tree}")
	parent := runGit(t, canonical, "rev-parse", "main")
	want := map[string]string{}
	for _, branch := range []string{"a", "b/c"} {
		sha := runGit(t, canonical, "commit-tree", tree, "-p", parent, "-m", branch)
		runGit(t, canonical, "update-ref", "refs/heads/"+branch, sha)
		want[branch] = sha
	}

	// One bundle carries both branches, named twice or not.
	rec := repoRequest(t, canonical, handleRepoBundle, http.MethodGet, "/api/repo/bundle?branch=a&branch=b/c&branch=a", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("bundle status %d: %s", rec.Code, rec.Body)
	}
	bundle := rec.Body.Bytes()

	rec = repoRequest(t, work, handleRepoFetchBases, http.MethodPost, "/api/repo/fetch-bases?branch=a&branch=b/c", bytes.NewReader(bundle))
	if rec.Code != http.StatusOK {
		t.Fatalf("fetch-bases status %d: %s", rec.Code, rec.Body)
	}
	var got struct {
		Branches []repoBranchResult `json:"branches"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || len(got.Branches) != 2 || got.Branches[0].Branch != "devpad/a" {
		t.Fatalf("fetch-bases result %s: %v", rec.Body, err)
	}
	for branch, sha := range want {
		if ref := runGit(t, work, "rev-parse", "refs/remotes/devpad/"+branch); ref != sha {
			t.Fatalf("devpad/%s = %s, want %s", branch, ref, sha)
		}
	}
	// The local branches are untouched.
	if head := runGit(t, work, "rev-parse", "main"); head != parent {
		t.Fatalf("main moved to %s", head)
	}

	// Every name is checked, and an unknown branch fails the whole bundle.
	for _, target := range []string{
		"/api/repo/bundle",
		"/api/repo/bundle?branch=a&branch=nope",
		"/api/repo/bundle?branch=a&branch=--all",
	} {
		if rec := repoRequest(t, canonical, handleRepoBundle, http.MethodGet, target, nil); rec.Code == http.StatusOK {
			t.Fatalf("%s was bundled", target)
		}
	}
	if rec := repoRequest(t, work, handleRepoFetchBases, http.MethodPost, "/api/repo/fetch-bases?branch=--all", bytes.NewReader(bundle)); rec.Code != http.StatusBadRequest {
		t.Fatalf("an invalid name got %d", rec.Code)
	}
}
