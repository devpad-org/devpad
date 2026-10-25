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

// isolateGitConfig points HOME, and git's system and global config, at
// empty test locations, returning the home directory and the system file.
func isolateGitConfig(t *testing.T) (home, system string) {
	t.Helper()
	home = t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("GIT_CONFIG_GLOBAL", "")
	os.Unsetenv("GIT_CONFIG_GLOBAL")
	system = filepath.Join(t.TempDir(), "gitconfig")
	writeFile(t, filepath.Dir(system), "gitconfig", "")
	t.Setenv("GIT_CONFIG_SYSTEM", system)
	return home, system
}

func credentialIDs(items []credentialItem) []string {
	ids := make([]string, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.ID)
	}
	slices.Sort(ids)
	return ids
}

func TestRepoDiscoverFindsRepos(t *testing.T) {
	isolateGitConfig(t)
	root := t.TempDir()
	app := newWorkRepo(t, root, "src/app")
	runGit(t, app, "branch", "feature")
	runGit(t, app, "tag", "v1")
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
	if !slices.Equal(repo.Branches, []string{"feature", "main"}) || repo.TagCount != 1 || !slices.Equal(repo.Tags, []string{"v1"}) {
		t.Fatalf("unexpected refs: %+v", repo)
	}
	if repo.OriginURL != "https://example.com/app.git" || !repo.OriginHadCredentials || !repo.HasDevpadRemote {
		t.Fatalf("unexpected remote findings: %+v", repo)
	}
	want := []string{"remote-url:the repository at /workspace/src/app: remote.origin.url"}
	if ids := credentialIDs(got.Credentials); !slices.Equal(ids, want) {
		t.Fatalf("credentials = %v, want %v", ids, want)
	}
	if got.Contents != nil {
		t.Fatalf("no contents preview expected when a repo exists")
	}
}

func TestRepoDiscoverComparesBranchesWithOrigin(t *testing.T) {
	isolateGitConfig(t)
	upstream := newWorkRepo(t, t.TempDir(), "up")
	runGit(t, upstream, "branch", "remote-only")
	root := t.TempDir()
	runGit(t, root, "clone", "--quiet", upstream, "app")
	work := filepath.Join(root, "app")
	commitFile(t, upstream, "new.txt", "newer")
	commitFile(t, upstream, "newer.txt", "newest")
	runGit(t, work, "fetch", "--quiet", "origin")

	repo := discover(t, root).Repos[0]
	if repo.OriginDefaultBranch != "main" {
		t.Fatalf("origin default = %q", repo.OriginDefaultBranch)
	}
	if !slices.Equal(repo.RemoteOnlyBranches, []string{"remote-only"}) {
		t.Fatalf("remote-only branches = %v", repo.RemoteOnlyBranches)
	}
	if !slices.Equal(repo.BehindBranches, []branchLag{{Name: "main", Behind: 2}}) {
		t.Fatalf("behind branches = %v", repo.BehindBranches)
	}
}

// plantCredentials puts one of every kind of credential devpad looks for in
// a workspace with a repo at its root, returning the root.
func plantCredentials(t *testing.T, home, system string) string {
	t.Helper()
	writeFile(t, home, ".ssh/id_rsa", "-----BEGIN OPENSSH PRIVATE KEY-----\nsecret\n")
	writeFile(t, home, ".ssh/id_rsa.pub", "ssh-rsa AAAA user")
	writeFile(t, home, ".ssh/id_ed25519", "-----BEGIN OPENSSH PRIVATE KEY-----\ndevpad\n")
	writeFile(t, home, ".ssh/config", "Host *\n")
	writeFile(t, home, ".config/gh/hosts.yml", "github.com:\n  oauth_token: gho_secret\n")
	writeFile(t, home, ".git-credentials", "https://u:hunter2@example.com")
	writeFile(t, home, ".netrc", "machine example.com password hunter2")
	runGit(t, home, "config", "--global", "credential.helper", "!f(){ echo password=TOKEN; }; f")
	runGit(t, home, "config", "--global", "url.https://x-access-token:ghs_secret@github.com/.insteadOf", "https://github.com/")
	runGit(t, home, "config", "--global", "url.https://github.com/.insteadOf", "gh:")
	runGit(t, home, "config", "--file", system, "http.https://example.com/.extraHeader", "Authorization: Bearer secret")
	runGit(t, home, "config", "--file", system, "--add", "http.https://example.com/.extraHeader", "X-Trace: on")
	root := t.TempDir()
	dir := newWorkRepo(t, root, ".")
	runGit(t, dir, "config", "credential.https://example.com.helper", "store")
	runGit(t, dir, "remote", "add", "origin", "https://user:ghp_secret@github.com/o/app.git")
	runGit(t, dir, "remote", "set-url", "--add", "origin", "https://mirror.example.com/app.git")
	runGit(t, dir, "remote", "add", "plain", "git@github.com:o/app.git")
	return root
}

var plantedCredentialIDs = []string{
	"credential-helper:the repository at /workspace: credential.https://example.com.helper",
	"credential-helper:~/.gitconfig: credential.helper",
	"gh-token:~/.config/gh/hosts.yml",
	"git-credentials:~/.git-credentials",
	"http-header:system git config: http.https://example.com/.extraheader",
	"netrc:~/.netrc",
	"remote-url:the repository at /workspace: remote.origin.url",
	"ssh-key:~/.ssh/id_rsa",
	"url-rewrite:~/.gitconfig: url.https://github.com/.insteadof",
}

func TestRepoDiscoverReportsCredentialsWithoutSecrets(t *testing.T) {
	home, system := isolateGitConfig(t)
	root := plantCredentials(t, home, system)

	rec := repoRequest(t, root, handleRepoDiscover, http.MethodGet, "/api/repo/discover", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("discover status %d: %s", rec.Code, rec.Body)
	}
	for _, secret := range []string{"secret", "hunter2", "TOKEN", "ghp_", "ghs_", "gho_", "Bearer"} {
		if strings.Contains(rec.Body.String(), secret) {
			t.Fatalf("discovery leaked %q: %s", secret, rec.Body)
		}
	}
	var got repoDiscovery
	json.Unmarshal(rec.Body.Bytes(), &got)
	if ids := credentialIDs(got.Credentials); !slices.Equal(ids, plantedCredentialIDs) {
		t.Fatalf("credentials =\n%s\nwant\n%s", strings.Join(ids, "\n"), strings.Join(plantedCredentialIDs, "\n"))
	}
}

func stripCredentials(t *testing.T, root string, ids []string) (stripped, remaining []credentialItem, failed []credentialFailure) {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"ids": ids})
	rec := repoRequest(t, root, handleCredentialsStrip, http.MethodPost, "/api/credentials/strip", bytes.NewReader(body))
	if rec.Code != http.StatusOK {
		t.Fatalf("strip status %d: %s", rec.Code, rec.Body)
	}
	var got struct {
		Stripped  []credentialItem    `json:"stripped"`
		Remaining []credentialItem    `json:"remaining"`
		Failed    []credentialFailure `json:"failed"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	return got.Stripped, got.Remaining, got.Failed
}

func TestCredentialsStripRemovesExactlyWhatWasNamed(t *testing.T) {
	home, system := isolateGitConfig(t)
	root := plantCredentials(t, home, system)

	keep := "netrc:~/.netrc"
	var ids []string
	for _, id := range plantedCredentialIDs {
		if id != keep {
			ids = append(ids, id)
		}
	}
	stripped, remaining, failed := stripCredentials(t, root, append(ids, "netrc:~/.not-there"))
	if len(failed) != 0 {
		t.Fatalf("failed: %+v", failed)
	}
	if got := credentialIDs(stripped); !slices.Equal(got, ids) {
		t.Fatalf("stripped %v, want %v", got, ids)
	}
	if got := credentialIDs(remaining); !slices.Equal(got, []string{keep}) {
		t.Fatalf("remaining %v", got)
	}

	for _, gone := range []string{".ssh/id_rsa", ".config/gh/hosts.yml", ".git-credentials"} {
		if _, err := os.Lstat(filepath.Join(home, gone)); err == nil {
			t.Errorf("%s was not removed", gone)
		}
	}
	// Files that are not credentials, and devpad's own key, are not touched
	// here; devpad's key goes with the SSH cleanup.
	for _, kept := range []string{".ssh/id_rsa.pub", ".ssh/config", ".ssh/id_ed25519", ".netrc"} {
		if _, err := os.Lstat(filepath.Join(home, kept)); err != nil {
			t.Errorf("%s was removed", kept)
		}
	}
	// The remote keeps both URLs without the token; other config stays.
	if got := runGit(t, root, "config", "--get-all", "remote.origin.url"); got != "https://github.com/o/app.git\nhttps://mirror.example.com/app.git" {
		t.Fatalf("origin urls = %q", got)
	}
	if got := runGit(t, root, "config", "remote.plain.url"); got != "git@github.com:o/app.git" {
		t.Fatalf("plain remote changed: %q", got)
	}
	if got := runGit(t, home, "config", "--global", "url.https://github.com/.insteadOf"); got != "gh:" {
		t.Fatalf("a rewrite without credentials was removed: %q", got)
	}
	// Any extra header may carry a token, so all of them go.
	if out, err := gitCommandIn(t.Context(), home, "config", "--file", system, "--get-all", "http.https://example.com/.extraHeader").Output(); err == nil {
		t.Fatalf("extra headers left: %q", out)
	}
}

func TestCredentialsStripWithNoIDsOnlyLists(t *testing.T) {
	home, system := isolateGitConfig(t)
	root := plantCredentials(t, home, system)

	stripped, remaining, _ := stripCredentials(t, root, nil)
	if len(stripped) != 0 || !slices.Equal(credentialIDs(remaining), plantedCredentialIDs) {
		t.Fatalf("stripped=%v remaining=%v", credentialIDs(stripped), credentialIDs(remaining))
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
		// Go cannot parse these, but they still hold a password.
		{"https://user:pa%zzss@github.com/a.git", "https://github.com/a.git", true},
		{"https://user:p#ss@github.com/a.git", "https://github.com/a.git", true},
		// An SSH URL keeps its user; only the password goes.
		{"ssh://git:tok@github.com/a", "ssh://git@github.com/a", true},
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

	// Reading the repo, as the IDE does, leaves it undoable.
	runGit(t, root, "status")
	runGit(t, root, "log", "--oneline")
	if code := undoContents(t, root, sha); code != http.StatusOK {
		t.Fatalf("undo status %d", code)
	}
	if _, err := os.Stat(filepath.Join(root, ".git")); err == nil {
		t.Fatalf("repo still there after undo")
	}
	if _, err := os.Stat(filepath.Join(root, "index.html")); err != nil {
		t.Fatalf("undo removed the user's file: %v", err)
	}
}

func undoContents(t *testing.T, root, sha string) int {
	t.Helper()
	return repoRequest(t, root, handleRepoUndoInitContents, http.MethodPost, "/api/repo/init-contents/undo?sha="+sha, nil).Code
}

// The undo leaves a repo the user did anything with, even when its only
// commit is still devpad's.
func TestRepoUndoInitContentsLeavesAChangedRepo(t *testing.T) {
	cases := map[string]func(t *testing.T, root, sha string){
		"new commit": func(t *testing.T, root, _ string) { commitFile(t, root, "more.txt", "y") },
		"commit then reset": func(t *testing.T, root, sha string) {
			commitFile(t, root, "more.txt", "y")
			runGit(t, root, "reset", "--hard", sha)
			runGit(t, root, "reflog", "expire", "--expire=now", "--all")
			runGit(t, root, "gc", "--prune=now", "--quiet")
		},
		"remote added": func(t *testing.T, root, _ string) {
			runGit(t, root, "remote", "add", "origin", "https://example.com/r.git")
		},
		"config changed":  func(t *testing.T, root, _ string) { runGit(t, root, "config", "core.autocrlf", "input") },
		"hook added":      func(t *testing.T, root, _ string) { writeFile(t, root, ".git/hooks/pre-commit", "#!/bin/sh\n") },
		"exclude changed": func(t *testing.T, root, _ string) { writeFile(t, root, ".git/info/exclude", "dist/\n") },
		"file staged": func(t *testing.T, root, _ string) {
			writeFile(t, root, "new.txt", "z")
			runGit(t, root, "add", "new.txt")
		},
		"state record removed": func(t *testing.T, root, _ string) {
			if err := os.Remove(filepath.Join(root, ".git", initStateFile)); err != nil {
				t.Fatal(err)
			}
		},
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			writeFile(t, root, "index.html", "x")
			_, got := initContents(t, root, false)
			sha := got["sha"].(string)
			change(t, root, sha)

			if code := undoContents(t, root, sha); code != http.StatusConflict {
				t.Fatalf("expected 409, got %d", code)
			}
			if _, err := os.Stat(filepath.Join(root, ".git")); err != nil {
				t.Fatalf("the changed repo was removed: %v", err)
			}
		})
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

func TestSSHRemoveManagedAlwaysRemovesDevpadKeyAndOnlyMatchingOthers(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	sshDir := filepath.Join(home, ".ssh")
	writeFile(t, sshDir, "id_ed25519", "devpad key, rotated or tampered with\n")
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

// hostileBundle bundles refs made directly in a repo with one commit on
// main, the way a process inside a workspace could, past git's own checks.
func hostileBundle(t *testing.T, refs func(dir, head string)) []byte {
	t.Helper()
	dir := newWorkRepo(t, t.TempDir(), "evil")
	head := runGit(t, dir, "rev-parse", "HEAD")
	refs(dir, head)
	path := filepath.Join(t.TempDir(), "evil.bundle")
	runGit(t, dir, "bundle", "create", path, "--all")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestRepoImportInitialRefusesHostileBundles(t *testing.T) {
	writeRef := func(dir, ref, sha string) {
		writeFile(t, filepath.Join(dir, ".git"), ref, sha+"\n")
	}
	cases := []struct {
		name string
		refs func(dir, head string)
	}{
		{"branch pointing at a blob", func(dir, _ string) {
			blob := runGit(t, dir, "hash-object", "-w", "README.md")
			writeRef(dir, "refs/heads/blobby", blob)
		}},
		{"branch pointing at a tree", func(dir, head string) {
			writeRef(dir, "refs/heads/tree", runGit(t, dir, "rev-parse", head+"^{tree}"))
		}},
		{"tag with a leading dash", func(dir, head string) { writeRef(dir, "refs/tags/-bad", head) }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			canonical := initEmptyCanonical(t, "")
			if code, body := importInitial(t, canonical, "main", hostileBundle(t, c.refs)); code != http.StatusBadRequest {
				t.Fatalf("expected 400, got %d: %s", code, body)
			}
			if refs := runGit(t, canonical, "for-each-ref"); refs != "" {
				t.Fatalf("a refused import left refs: %s", refs)
			}
		})
	}

	t.Run("refs outside branches and tags are dropped", func(t *testing.T) {
		canonical := initEmptyCanonical(t, "")
		bundle := hostileBundle(t, func(dir, head string) {
			writeRef(dir, "refs/notes/commits", head)
			writeRef(dir, "refs/remotes/origin/main", head)
			writeRef(dir, "refs/devpad-quarantine/x/heads/main", head)
		})
		if code, body := importInitial(t, canonical, "main", bundle); code != http.StatusOK {
			t.Fatalf("import status %d: %s", code, body)
		}
		if refs := runGit(t, canonical, "for-each-ref", "--format=%(refname)"); refs != "refs/heads/main" {
			t.Fatalf("canonical refs = %q", refs)
		}
	})
}

func TestRepoImportInitialRefusesARepoWithOnlyTags(t *testing.T) {
	canonical := initEmptyCanonical(t, "")
	tree := runGit(t, canonical, "hash-object", "-t", "tree", "-w", "--stdin")
	commit := runGit(t, canonical, "commit-tree", tree, "-m", "planted")
	runGit(t, canonical, "update-ref", "refs/tags/planted", commit)

	_, bundle := bundleAll(t, func() string { root := t.TempDir(); newWorkRepo(t, root, "."); return root }(), ".")
	if code, body := importInitial(t, canonical, "main", bundle); code != http.StatusConflict {
		t.Fatalf("expected 409 for a repo that has a tag, got %d: %s", code, body)
	}
	if refs := runGit(t, canonical, "for-each-ref", "--format=%(refname)"); refs != "refs/tags/planted" {
		t.Fatalf("canonical refs = %q", refs)
	}
}
