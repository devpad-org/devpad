package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestCredentialsFoundInNestedReposSubmodulesAndBareRepos(t *testing.T) {
	isolateGitConfig(t)
	root := t.TempDir()
	dir := newWorkRepo(t, root, ".")
	runGit(t, dir, "config", "submodule.lib.url", "https://u:ghp_sub@github.com/o/lib.git")
	// A submodule's git directory lives under the parent's .git/modules.
	runGit(t, root, "init", "--quiet", "--bare", filepath.Join(root, ".git", "modules", "lib"))
	runGit(t, root, "config", "--file", filepath.Join(root, ".git", "modules", "lib", "config"),
		"remote.origin.url", "https://u:ghp_mod@github.com/o/lib.git")
	writeFile(t, root, "lib/.git", "gitdir: ../.git/modules/lib\n")
	nested := newWorkRepo(t, root, "libs/private")
	runGit(t, nested, "remote", "add", "origin", "https://x-access-token:ghp_nested@github.com/o/private.git")
	runGit(t, root, "init", "--quiet", "--bare", filepath.Join(root, "mirror.git"))
	runGit(t, root, "config", "--file", filepath.Join(root, "mirror.git", "config"),
		"remote.origin.url", "ssh://git:ghp_bare@github.com/o/mirror.git")

	want := []string{
		"remote-url:/workspace/.git/modules/lib/config: remote.origin.url",
		"remote-url:/workspace/mirror.git/config: remote.origin.url",
		"remote-url:the repository at /workspace/libs/private: remote.origin.url",
		"remote-url:the repository at /workspace: submodule.lib.url",
	}
	got := discover(t, root)
	if ids := credentialIDs(got.Credentials); !slices.Equal(ids, want) {
		t.Fatalf("credentials =\n%s\nwant\n%s", strings.Join(ids, "\n"), strings.Join(want, "\n"))
	}

	stripped, remaining, failed := stripCredentials(t, root, want)
	if len(failed) != 0 || len(remaining) != 0 || len(stripped) != len(want) {
		t.Fatalf("stripped=%v remaining=%v failed=%+v", credentialIDs(stripped), credentialIDs(remaining), failed)
	}
	for _, c := range []struct{ file, key, want string }{
		{filepath.Join(root, ".git", "config"), "submodule.lib.url", "https://github.com/o/lib.git"},
		{filepath.Join(root, ".git", "modules", "lib", "config"), "remote.origin.url", "https://github.com/o/lib.git"},
		{filepath.Join(nested, ".git", "config"), "remote.origin.url", "https://github.com/o/private.git"},
		// An SSH URL keeps its user.
		{filepath.Join(root, "mirror.git", "config"), "remote.origin.url", "ssh://git@github.com/o/mirror.git"},
	} {
		if got := runGit(t, root, "config", "--file", c.file, c.key); got != c.want {
			t.Errorf("%s %s = %q, want %q", c.file, c.key, got, c.want)
		}
	}
}

func TestCredentialsFoundInIncludedConfig(t *testing.T) {
	home, _ := isolateGitConfig(t)
	root := t.TempDir()
	work := filepath.Join(home, ".gitconfig-work")
	writeFile(t, home, ".gitconfig-work", "")
	runGit(t, home, "config", "--file", work, "credential.helper", "store --file ~/.my-creds")
	runGit(t, home, "config", "--file", work, "url.https://tok:ghp_inc@github.com/.insteadOf", "https://github.com/")
	writeFile(t, home, ".my-creds", "https://u:ghp_store@github.com\n")
	// The condition doesn't match here, but the file is searched anyway.
	runGit(t, home, "config", "--global", "includeIf.gitdir:~/work/.path", "~/.gitconfig-work")
	runGit(t, home, "config", "--global", "alias.st", "status")

	want := []string{
		"credential-helper:~/.gitconfig-work: credential.helper (and its store ~/.my-creds)",
		"url-rewrite:~/.gitconfig-work: url.https://github.com/.insteadof",
	}
	newWorkRepo(t, root, ".")
	// An include devpad cannot read is listed, so the user strips it. It is
	// in a bare repo's config, since git itself refuses to run with one in
	// a config it reads.
	mirror := filepath.Join(root, "mirror.git")
	runGit(t, root, "init", "--quiet", "--bare", mirror)
	if os.Geteuid() != 0 {
		writeFile(t, mirror, "locked.inc", "[credential]\n\thelper = store\n")
		if err := os.Chmod(filepath.Join(mirror, "locked.inc"), 0); err != nil {
			t.Fatalf("chmod: %v", err)
		}
		runGit(t, root, "config", "--file", filepath.Join(mirror, "config"), "include.path", "locked.inc")
		want = append([]string{"config-include:/workspace/mirror.git/config: include.path (/workspace/mirror.git/locked.inc)"}, want...)
	}

	got := discover(t, root)
	if ids := credentialIDs(got.Credentials); !slices.Equal(ids, want) {
		t.Fatalf("credentials =\n%s\nwant\n%s", strings.Join(ids, "\n"), strings.Join(want, "\n"))
	}
	if !slices.Contains(got.CredentialIncludes, "~/.gitconfig includes ~/.gitconfig-work") {
		t.Fatalf("includes = %v", got.CredentialIncludes)
	}

	stripped, remaining, failed := stripCredentials(t, root, want)
	if len(failed) != 0 || len(remaining) != 0 || len(stripped) != len(want) {
		t.Fatalf("stripped=%v remaining=%v failed=%+v", credentialIDs(stripped), credentialIDs(remaining), failed)
	}
	if _, err := os.Lstat(filepath.Join(home, ".my-creds")); err == nil {
		t.Fatal("the store file was left behind")
	}
	if out := runGit(t, home, "config", "--file", work, "--list"); strings.Contains(out, "helper") || strings.Contains(out, "insteadof") {
		t.Fatalf("included config still holds credentials: %q", out)
	}
	// Only an unreadable include goes; the include that was searched, and
	// unrelated config, stay.
	if got := runGit(t, home, "config", "--global", "includeIf.gitdir:~/work/.path"); got != "~/.gitconfig-work" {
		t.Fatalf("searched include removed: %q", got)
	}
	if got := runGit(t, home, "config", "--global", "alias.st"); got != "status" {
		t.Fatalf("unrelated config removed: %q", got)
	}
}

func TestCredentialsSSHKeysInSubdirectoriesPuttyAndSymlinks(t *testing.T) {
	home, _ := isolateGitConfig(t)
	root := t.TempDir()
	outside := t.TempDir()
	pem := "-----BEGIN OPENSSH PRIVATE KEY-----\nsecret\n"
	writeFile(t, home, ".ssh/work/deploy", pem)
	writeFile(t, home, ".ssh/putty.ppk", "PuTTY-User-Key-File-3: ssh-ed25519\nsecret\n")
	writeFile(t, root, "keys/k", pem)
	writeFile(t, outside, "k", pem)
	if err := os.Symlink(filepath.Join(root, "keys", "k"), filepath.Join(home, ".ssh", "linked")); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	if err := os.Symlink(filepath.Join(outside, "k"), filepath.Join(home, ".ssh", "elsewhere")); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	want := []string{"ssh-key:~/.ssh/elsewhere", "ssh-key:~/.ssh/linked", "ssh-key:~/.ssh/putty.ppk", "ssh-key:~/.ssh/work/deploy"}
	if ids := credentialIDs(discover(t, root).Credentials); !slices.Equal(ids, want) {
		t.Fatalf("credentials = %v, want %v", ids, want)
	}

	stripped, remaining, failed := stripCredentials(t, root, want)
	if got := credentialIDs(stripped); !slices.Equal(got, []string{"ssh-key:~/.ssh/linked", "ssh-key:~/.ssh/putty.ppk", "ssh-key:~/.ssh/work/deploy"}) {
		t.Fatalf("stripped %v", got)
	}
	// A link out of the home directory and workspace is reported, and
	// neither it nor its target is touched.
	if len(failed) != 1 || failed[0].ID != "ssh-key:~/.ssh/elsewhere" || !strings.Contains(failed[0].Reason, "outside") {
		t.Fatalf("failed = %+v", failed)
	}
	if got := credentialIDs(remaining); !slices.Equal(got, []string{"ssh-key:~/.ssh/elsewhere"}) {
		t.Fatalf("remaining %v", got)
	}
	for _, gone := range []string{filepath.Join(home, ".ssh", "linked"), filepath.Join(root, "keys", "k")} {
		if _, err := os.Lstat(gone); err == nil {
			t.Errorf("%s was not removed", gone)
		}
	}
	if _, err := os.Lstat(filepath.Join(outside, "k")); err != nil {
		t.Errorf("the key outside was removed: %v", err)
	}
}

func TestCredentialStripFailureHoldsNoSecret(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can write a read-only directory")
	}
	isolateGitConfig(t)
	root := t.TempDir()
	dir := newWorkRepo(t, root, ".")
	runGit(t, dir, "remote", "add", "origin", "https://user:ghp_secret@github.com/o/app.git")
	runGit(t, dir, "config", "credential.https://user:ghp_key@example.com.helper", "store")
	gitDir := filepath.Join(dir, ".git")
	if err := os.Chmod(gitDir, 0o555); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { os.Chmod(gitDir, 0o755) })

	ids := []string{
		"credential-helper:the repository at /workspace: credential.https://example.com.helper",
		"remote-url:the repository at /workspace: remote.origin.url",
	}
	stripped, _, failed := stripCredentials(t, root, ids)
	if len(stripped) != 0 || len(failed) != 2 {
		t.Fatalf("stripped=%v failed=%+v", credentialIDs(stripped), failed)
	}
	for _, f := range failed {
		if strings.Contains(f.Reason, "ghp_") || strings.Contains(f.Location, "ghp_") || f.Reason == "" {
			t.Fatalf("failure leaks or says nothing: %+v", f)
		}
	}
	// The remote still has its URL: nothing was half-written.
	if got := runGit(t, dir, "config", "remote.origin.url"); got != "https://user:ghp_secret@github.com/o/app.git" {
		t.Fatalf("remote url = %q", got)
	}
}
