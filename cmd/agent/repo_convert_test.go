package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func writeFile(t *testing.T, root, rel, content string) {
	t.Helper()
	path := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("writing %s: %v", rel, err)
	}
}

// newWorkRepo makes a repo with one commit on main at root/rel.
func newWorkRepo(t *testing.T, root, rel string) string {
	t.Helper()
	dir := filepath.Join(root, rel)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	runGit(t, dir, "init", "--quiet", "--initial-branch=main")
	commitFile(t, dir, "README.md", "hello")
	return dir
}

func discover(t *testing.T, root string) repoDiscovery {
	t.Helper()
	rec := repoRequest(t, root, handleRepoDiscover, http.MethodGet, "/api/repo/discover", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("discover status %d: %s", rec.Code, rec.Body)
	}
	var got repoDiscovery
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding discovery: %v", err)
	}
	return got
}

func TestRepoDiscoverFindsRepos(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	root := t.TempDir()
	app := newWorkRepo(t, root, "src/app")
	runGit(t, app, "branch", "feature")
	runGit(t, app, "remote", "add", "origin", "https://user:secret@example.com/app.git")
	runGit(t, app, "remote", "add", "devpad", "https://example.com/other.git")
	writeFile(t, app, "dirty.txt", "x")
	newWorkRepo(t, root, "node_modules/dep")
	newWorkRepo(t, root, "a/b/c/too-deep")

	got := discover(t, root)
	if len(got.Repos) != 1 {
		t.Fatalf("expected one repo, got %+v", got.Repos)
	}
	repo := got.Repos[0]
	if repo.Path != "src/app" || repo.Branch != "main" || repo.BranchCount != 2 || !repo.Dirty {
		t.Fatalf("unexpected repo: %+v", repo)
	}
	if repo.OriginURL != "https://example.com/app.git" || !repo.OriginHadCredentials {
		t.Fatalf("origin credentials not stripped: %+v", repo)
	}
	if !slices.Equal(repo.RemotesWithCredentials, []string{"origin"}) || !repo.HasDevpadRemote {
		t.Fatalf("unexpected remote findings: %+v", repo)
	}
	if got.Contents != nil {
		t.Fatalf("no contents preview expected when a repo exists")
	}
}

func TestRepoDiscoverReportsCredentialStores(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeFile(t, home, ".git-credentials", "https://u:p@example.com")
	root := t.TempDir()
	dir := newWorkRepo(t, root, ".")
	runGit(t, dir, "config", "credential.helper", "store")

	got := discover(t, root)
	if !slices.Equal(got.CredentialFiles, []string{"~/.git-credentials"}) {
		t.Fatalf("credential files = %v", got.CredentialFiles)
	}
	if len(got.Repos) != 1 || got.Repos[0].Path != "." || !slices.Equal(got.Repos[0].CredentialHelpers, []string{"store"}) {
		t.Fatalf("unexpected repos: %+v", got.Repos)
	}
}

func TestStripURLCredentials(t *testing.T) {
	cases := []struct {
		in, want string
		stripped bool
	}{
		{"https://user:pass@example.com/r.git", "https://example.com/r.git", true},
		{"https://ghp_token@github.com/r.git", "https://github.com/r.git", true},
		{"ssh://git@github.com/r.git", "ssh://git@github.com/r.git", false},
		{"git@github.com:o/r.git", "git@github.com:o/r.git", false},
		{"https://example.com/r.git", "https://example.com/r.git", false},
	}
	for _, c := range cases {
		got, stripped := stripURLCredentials(c.in)
		if got != c.want || stripped != c.stripped {
			t.Errorf("stripURLCredentials(%q) = %q, %v; want %q, %v", c.in, got, stripped, c.want, c.stripped)
		}
	}
}

func TestRepoDiscoverPreviewsContents(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	root := t.TempDir()
	writeFile(t, root, "index.html", "<html></html>")
	writeFile(t, root, "src/app.js", "console.log(1)")
	writeFile(t, root, "node_modules/dep/index.js", "junk")
	writeFile(t, root, ".env", "SECRET=1")
	writeFile(t, root, "keys/id_ed25519", "key")

	got := discover(t, root)
	if len(got.Repos) != 0 || got.Contents == nil {
		t.Fatalf("expected a contents preview, got %+v", got)
	}
	c := got.Contents
	if c.Files != 2 || c.Bytes != int64(len("<html></html>")+len("console.log(1)")) {
		t.Fatalf("unexpected preview: %+v", c)
	}
	slices.Sort(c.SensitiveFiles)
	if c.SensitiveCount != 2 || !slices.Equal(c.SensitiveFiles, []string{".env", "keys/id_ed25519"}) {
		t.Fatalf("unexpected sensitive files: %+v", c)
	}
	if _, err := os.Stat(filepath.Join(root, ".git")); err == nil {
		t.Fatalf("preview wrote a repo into the workspace")
	}
}

func initContents(t *testing.T, root string, includeSensitive bool) (int, map[string]any) {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"defaultBranch": "main", "includeSensitive": includeSensitive, "name": "Owner", "email": "owner@example.com"})
	rec := repoRequest(t, root, handleRepoInitContents, http.MethodPost, "/api/repo/init-contents", bytes.NewReader(body))
	var got map[string]any
	json.Unmarshal(rec.Body.Bytes(), &got)
	return rec.Code, got
}

func TestRepoInitContentsCommitsWithoutJunkOrSecrets(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "index.html", "<html></html>")
	writeFile(t, root, "node_modules/dep/index.js", "junk")
	writeFile(t, root, ".env", "SECRET=1")

	code, got := initContents(t, root, false)
	if code != http.StatusOK || got["files"] != float64(1) {
		t.Fatalf("init status %d: %v", code, got)
	}
	if files := runGit(t, root, "ls-files"); files != "index.html" {
		t.Fatalf("committed %q, want only index.html", files)
	}
	if author := runGit(t, root, "log", "-1", "--format=%an <%ae>"); author != "Owner <owner@example.com>" {
		t.Fatalf("author = %q", author)
	}
	// Left-out files are untouched and stay untracked, and no file in the
	// workspace is added for the exclusions.
	for _, rel := range []string{".env", "node_modules/dep/index.js"} {
		if _, err := os.Stat(filepath.Join(root, rel)); err != nil {
			t.Fatalf("%s was removed: %v", rel, err)
		}
	}
	if status := runGit(t, root, "status", "--porcelain"); status != "" {
		t.Fatalf("expected a clean status, got %q", status)
	}
	if _, err := os.Stat(filepath.Join(root, ".gitignore")); err == nil {
		t.Fatalf("init wrote a .gitignore into the workspace")
	}

	if code, _ := initContents(t, root, false); code != http.StatusConflict {
		t.Fatalf("expected 409 for an existing repo, got %d", code)
	}
}

func TestRepoInitContentsIncludesSecretsOnlyWhenAsked(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "index.html", "x")
	writeFile(t, root, ".env", "SECRET=1")

	if code, got := initContents(t, root, true); code != http.StatusOK {
		t.Fatalf("init status %d: %v", code, got)
	}
	if files := runGit(t, root, "ls-files"); files != ".env\nindex.html" {
		t.Fatalf("committed %q", files)
	}
}

func TestRepoInitContentsRefusesNestedRepo(t *testing.T) {
	root := t.TempDir()
	newWorkRepo(t, root, "app")
	if code, _ := initContents(t, root, false); code != http.StatusConflict {
		t.Fatalf("expected 409 when the workspace has a repo, got %d", code)
	}
	if _, err := os.Stat(filepath.Join(root, ".git")); err == nil {
		t.Fatalf("a repo was made at the root anyway")
	}
}

func TestRepoUndoInitContents(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "index.html", "x")
	_, got := initContents(t, root, false)
	sha := got["sha"].(string)

	undo := func() int {
		return repoRequest(t, root, handleRepoUndoInitContents, http.MethodPost, "/api/repo/init-contents/undo?sha="+sha, nil).Code
	}

	// A repo that gained a commit since is left in place.
	commitFile(t, root, "more.txt", "y")
	if code := undo(); code != http.StatusConflict {
		t.Fatalf("expected 409 after a new commit, got %d", code)
	}
	runGit(t, root, "reset", "--hard", sha)
	runGit(t, root, "reflog", "expire", "--expire=now", "--all")
	runGit(t, root, "gc", "--prune=now", "--quiet")

	if code := undo(); code != http.StatusOK {
		t.Fatalf("undo status %d", code)
	}
	if _, err := os.Stat(filepath.Join(root, ".git")); err == nil {
		t.Fatalf("repo still there after undo")
	}
	if _, err := os.Stat(filepath.Join(root, "index.html")); err != nil {
		t.Fatalf("undo removed the user's file: %v", err)
	}
}

func bundleAll(t *testing.T, root, dir string) (int, []byte) {
	t.Helper()
	rec := repoRequest(t, root, handleRepoBundleAll, http.MethodGet, "/api/repo/bundle-all?dir="+dir, nil)
	return rec.Code, rec.Body.Bytes()
}

func initEmptyCanonical(t *testing.T, upstream string) string {
	t.Helper()
	root := t.TempDir()
	body, _ := json.Marshal(map[string]any{"defaultBranch": "main", "upstreamUrl": upstream, "empty": true})
	rec := repoRequest(t, root, handleRepoInit, http.MethodPost, "/api/repo/init", bytes.NewReader(body))
	if rec.Code != http.StatusOK {
		t.Fatalf("empty init status %d: %s", rec.Code, rec.Body)
	}
	return root
}

func importInitial(t *testing.T, canonical, defaultBranch string, bundle []byte) (int, string) {
	t.Helper()
	rec := repoRequest(t, canonical, handleRepoImportInitial, http.MethodPost, "/api/repo/import-initial?defaultBranch="+defaultBranch, bytes.NewReader(bundle))
	return rec.Code, rec.Body.String()
}

func TestRepoInitEmptyRecordsUpstreamWithoutCommits(t *testing.T) {
	canonical := initEmptyCanonical(t, "git@github.com:o/r.git")
	if refs := runGit(t, canonical, "for-each-ref"); refs != "" {
		t.Fatalf("empty init made refs: %s", refs)
	}
	if url := runGit(t, canonical, "config", "remote.origin.url"); url != "git@github.com:o/r.git" {
		t.Fatalf("origin = %q", url)
	}
	if head := runGit(t, canonical, "symbolic-ref", "HEAD"); head != "refs/heads/main" {
		t.Fatalf("HEAD = %q", head)
	}
}

func TestRepoBundleAllImportsEveryBranchIntoNewRepo(t *testing.T) {
	root := t.TempDir()
	work := newWorkRepo(t, root, "app")
	runGit(t, work, "switch", "--quiet", "-c", "feature")
	feature := commitFile(t, work, "f.txt", "f")
	runGit(t, work, "tag", "v1")
	runGit(t, work, "switch", "--quiet", "main")
	main := runGit(t, work, "rev-parse", "main")

	code, bundle := bundleAll(t, root, "app")
	if code != http.StatusOK {
		t.Fatalf("bundle-all status %d: %s", code, bundle)
	}
	canonical := initEmptyCanonical(t, "")
	if code, body := importInitial(t, canonical, "main", bundle); code != http.StatusOK {
		t.Fatalf("import status %d: %s", code, body)
	}
	if got := runGit(t, canonical, "rev-parse", "main", "feature", "v1^{commit}"); got != main+"\n"+feature+"\n"+feature {
		t.Fatalf("canonical refs = %q", got)
	}
	if head := runGit(t, canonical, "symbolic-ref", "HEAD"); head != "refs/heads/main" {
		t.Fatalf("HEAD = %q", head)
	}
	if got := runGit(t, canonical, "for-each-ref", quarantineRefPrefix); got != "" {
		t.Fatalf("quarantine refs left behind: %s", got)
	}

	// Once the repo has branches, the initial import is refused for good.
	if code, _ := importInitial(t, canonical, "main", bundle); code != http.StatusConflict {
		t.Fatalf("expected 409 for a second initial import, got %d", code)
	}
}

func TestRepoImportInitialRequiresDefaultBranch(t *testing.T) {
	root := t.TempDir()
	newWorkRepo(t, root, ".")
	_, bundle := bundleAll(t, root, ".")
	canonical := initEmptyCanonical(t, "")

	if code, body := importInitial(t, canonical, "trunk", bundle); code != http.StatusBadRequest {
		t.Fatalf("expected 400 without the default branch, got %d: %s", code, body)
	}
	if refs := runGit(t, canonical, "for-each-ref"); refs != "" {
		t.Fatalf("a failed import left refs: %s", refs)
	}
	if code, _ := importInitial(t, canonical, "main", []byte("not a bundle")); code != http.StatusBadRequest {
		t.Fatalf("expected 400 for a bad bundle, got %d", code)
	}
}

func TestRepoBundleAllStaysInsideWorkspace(t *testing.T) {
	outside := t.TempDir()
	newWorkRepo(t, outside, "repo")
	root := t.TempDir()
	work := newWorkRepo(t, root, "app")
	if err := os.MkdirAll(filepath.Join(work, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "repo"), filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}

	for _, dir := range []string{"../" + filepath.Base(outside), "/etc", "link", "app/sub", "missing", "-x"} {
		if code, body := bundleAll(t, root, dir); code == http.StatusOK {
			t.Errorf("bundle-all of %q succeeded: %s", dir, body)
		}
	}
	if code, body := bundleAll(t, root, "app"); code != http.StatusOK {
		t.Fatalf("bundle-all of the repo failed: %d %s", code, body)
	}
	if code, _ := bundleAll(t, t.TempDir(), "."); code != http.StatusNotFound {
		t.Fatalf("expected 404 for a non-repo, got %d", code)
	}
}

func TestSSHRemoveManagedOnlyRemovesDevpadFiles(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	sshDir := filepath.Join(home, ".ssh")
	writeFile(t, sshDir, "id_ed25519", "devpad key\n")
	writeFile(t, sshDir, "known_hosts", "devpad hosts\n")
	writeFile(t, sshDir, "config", "the user's own config\n")
	writeFile(t, sshDir, "id_rsa", "the user's own key\n")

	hash := func(s string) string {
		sum := sha256.Sum256([]byte(s))
		return hex.EncodeToString(sum[:])
	}
	body, _ := json.Marshal(map[string]any{"files": []map[string]string{
		{"name": "id_ed25519", "sha256": hash("devpad key\n")},
		{"name": "id_ed25519.pub", "sha256": hash("devpad pub\n")},
		{"name": "known_hosts", "sha256": strings.ToUpper(hash("devpad hosts\n"))},
		{"name": "config", "sha256": hash("devpad config\n")},
	}})
	rec := repoRequest(t, t.TempDir(), handleSSHRemoveManaged, http.MethodPost, "/api/ssh/remove-managed", bytes.NewReader(body))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	var got sshCleanupResult
	json.Unmarshal(rec.Body.Bytes(), &got)
	if !slices.Equal(got.Removed, []string{"~/.ssh/id_ed25519", "~/.ssh/known_hosts"}) ||
		!slices.Equal(got.Kept, []string{"~/.ssh/config"}) ||
		!slices.Equal(got.Absent, []string{"~/.ssh/id_ed25519.pub"}) {
		t.Fatalf("unexpected cleanup: %+v", got)
	}
	for _, name := range []string{"config", "id_rsa"} {
		if _, err := os.Stat(filepath.Join(sshDir, name)); err != nil {
			t.Fatalf("user's %s was removed", name)
		}
	}

	bad, _ := json.Marshal(map[string]any{"files": []map[string]string{{"name": "../.bashrc", "sha256": "x"}}})
	if rec := repoRequest(t, t.TempDir(), handleSSHRemoveManaged, http.MethodPost, "/api/ssh/remove-managed", bytes.NewReader(bad)); rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for a file devpad does not manage, got %d", rec.Code)
	}
}
