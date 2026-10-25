package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// A standalone workspace may hold credentials the user put there: SSH keys,
// tokens in git config or remote URLs, credential stores. Converting it in
// place makes it a project workspace, where the IDE assistant runs and no
// credential may stay, so devpad lists them for the user and, with their
// consent, strips exactly those. Values never leave the container; only
// their kind and where they are.

// Credential kinds.
const (
	credSSHKey         = "ssh-key"
	credGHToken        = "gh-token"
	credGitCredentials = "git-credentials"
	credNetrc          = "netrc"
	credHelper         = "credential-helper"
	credURLRewrite     = "url-rewrite"
	credHTTPHeader     = "http-header"
	credRemoteURL      = "remote-url"
)

// authHeaderPattern matches an Authorization header among http.extraHeader
// values, as a git config value pattern (POSIX ERE, so no (?i)).
const authHeaderPattern = `^[[:space:]]*[Aa][Uu][Tt][Hh][Oo][Rr][Ii][Zz][Aa][Tt][Ii][Oo][Nn]:`

var authHeaderRe = regexp.MustCompile(`(?i)^\s*authorization:`)

// devpadSSHFiles are always removed from a converted workspace, whatever
// they hold, so they are not listed as the user's own credentials.
var devpadSSHFiles = []string{"id_ed25519", "id_ed25519.pub"}

type credentialItem struct {
	// ID is stable across detections, so the user's consent names exactly
	// the items they saw.
	ID       string `json:"id"`
	Kind     string `json:"kind"`
	Location string `json:"location"`

	// How to strip it; never sent.
	path         string
	scope        configScope
	key          string
	valuePattern string
	remoteURL    bool
}

// configScope is one git config file: system, a global file, or a repo's.
type configScope struct {
	label  string
	args   []string
	dir    string
	system bool
}

func newCredential(kind, location string) credentialItem {
	return credentialItem{ID: kind + ":" + location, Kind: kind, Location: location}
}

// detectCredentials lists the credentials in the home directory, in git's
// system and global config, and in the config of each repo in repoDirs.
func detectCredentials(ctx context.Context, root string, repoDirs []string) []credentialItem {
	items := []credentialItem{}
	home, err := os.UserHomeDir()
	if err == nil {
		items = append(items, homeCredentials(home)...)
	}
	for _, scope := range configScopes(root, home, repoDirs) {
		items = append(items, configCredentials(ctx, scope)...)
	}
	return items
}

func xdgConfigHome(home string) string {
	if dir := os.Getenv("XDG_CONFIG_HOME"); dir != "" {
		return dir
	}
	return filepath.Join(home, ".config")
}

func homeCredentials(home string) []credentialItem {
	var items []credentialItem
	sshDir := filepath.Join(home, ".ssh")
	if entries, err := os.ReadDir(sshDir); err == nil {
		for _, e := range entries {
			if e.IsDir() || slices.Contains(devpadSSHFiles, e.Name()) || strings.HasSuffix(e.Name(), ".pub") {
				continue
			}
			if isPrivateKey(filepath.Join(sshDir, e.Name())) {
				item := newCredential(credSSHKey, "~/.ssh/"+e.Name())
				item.path = filepath.Join(sshDir, e.Name())
				items = append(items, item)
			}
		}
	}
	xdg := xdgConfigHome(home)
	files := []struct{ kind, path, display string }{
		{credGHToken, filepath.Join(xdg, "gh", "hosts.yml"), "~/.config/gh/hosts.yml"},
		{credGitCredentials, filepath.Join(home, ".git-credentials"), "~/.git-credentials"},
		{credGitCredentials, filepath.Join(xdg, "git", "credentials"), "~/.config/git/credentials"},
		{credNetrc, filepath.Join(home, ".netrc"), "~/.netrc"},
		{credNetrc, filepath.Join(home, "_netrc"), "~/_netrc"},
	}
	for _, f := range files {
		if _, err := os.Lstat(f.path); err == nil {
			item := newCredential(f.kind, f.display)
			item.path = f.path
			items = append(items, item)
		}
	}
	return items
}

// isPrivateKey reports whether a file in ~/.ssh looks like a private key.
func isPrivateKey(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	head := make([]byte, 4096)
	n, _ := f.Read(head)
	return strings.Contains(string(head[:n]), "PRIVATE KEY")
}

func configScopes(root, home string, repoDirs []string) []configScope {
	scopes := []configScope{{label: "system git config", args: []string{"--system"}, system: true}}
	var globals []string
	if path := os.Getenv("GIT_CONFIG_GLOBAL"); path != "" {
		globals = []string{path}
	} else if home != "" {
		globals = []string{filepath.Join(home, ".gitconfig"), filepath.Join(xdgConfigHome(home), "git", "config")}
	}
	for _, path := range globals {
		if _, err := os.Stat(path); err != nil {
			continue
		}
		label := path
		if home != "" {
			if rel, err := filepath.Rel(home, path); err == nil && !strings.HasPrefix(rel, "..") {
				label = "~/" + filepath.ToSlash(rel)
			}
		}
		scopes = append(scopes, configScope{label: label, args: []string{"--file", path}})
	}
	for _, dir := range repoDirs {
		rel, err := filepath.Rel(root, dir)
		if err != nil {
			continue
		}
		label := "the repository at /workspace"
		if rel != "." {
			label = "the repository at /workspace/" + filepath.ToSlash(rel)
		}
		scopes = append(scopes, configScope{label: label, args: []string{"--local"}, dir: dir})
	}
	return scopes
}

// configEntries lists a scope's keys and values matching pattern.
func configEntries(ctx context.Context, scope configScope, pattern string) [][2]string {
	dir := scope.dir
	if dir == "" {
		dir = "/"
	}
	args := append(append([]string{"config"}, scope.args...), "--get-regexp", pattern)
	out, err := gitOutputIn(ctx, dir, args...)
	if err != nil {
		return nil
	}
	var entries [][2]string
	for _, line := range nonEmptyLines(out) {
		key, value, _ := strings.Cut(line, " ")
		entries = append(entries, [2]string{key, value})
	}
	return entries
}

func configCredentials(ctx context.Context, scope configScope) []credentialItem {
	var items []credentialItem
	add := func(item credentialItem, key string) {
		item.scope, item.key = scope, key
		items = append(items, item)
	}
	seen := map[string]bool{}
	for _, e := range configEntries(ctx, scope, `^credential\..*helper$`) {
		if seen[e[0]] {
			continue
		}
		seen[e[0]] = true
		add(newCredential(credHelper, scope.label+": "+e[0]), e[0])
	}
	for _, e := range configEntries(ctx, scope, `^url\..*\.(insteadof|pushinsteadof)$`) {
		base, suffix := urlRewriteParts(e[0])
		clean, hadCredentials := stripURLCredentials(base)
		if !hadCredentials || seen[e[0]] {
			continue
		}
		seen[e[0]] = true
		add(newCredential(credURLRewrite, scope.label+": url."+clean+"."+suffix), e[0])
	}
	for _, e := range configEntries(ctx, scope, `^http\.(.*\.)?extraheader$`) {
		if !authHeaderRe.MatchString(e[1]) || seen[e[0]] {
			continue
		}
		seen[e[0]] = true
		item := newCredential(credHTTPHeader, scope.label+": "+e[0])
		item.valuePattern = authHeaderPattern
		add(item, e[0])
	}
	for _, e := range configEntries(ctx, scope, `^remote\..*\.(url|pushurl)$`) {
		if _, hadCredentials := stripURLCredentials(e[1]); !hadCredentials || seen[e[0]] {
			continue
		}
		seen[e[0]] = true
		item := newCredential(credRemoteURL, scope.label+": "+e[0])
		item.remoteURL = true
		add(item, e[0])
	}
	return items
}

// urlRewriteParts splits url.<base>.insteadof into its base and suffix. The
// base can hold dots, so git takes the last one as the separator.
func urlRewriteParts(key string) (base, suffix string) {
	rest := strings.TrimPrefix(key, "url.")
	i := strings.LastIndex(rest, ".")
	if i < 0 {
		return rest, ""
	}
	return rest[:i], rest[i+1:]
}

// stripCredential removes one detected credential.
func stripCredential(ctx context.Context, item credentialItem) error {
	if item.path != "" {
		if err := os.Remove(item.path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		return nil
	}
	if !item.remoteURL {
		args := []string{"--unset-all", item.key}
		if item.valuePattern != "" {
			args = append(args, item.valuePattern)
		}
		return runConfig(ctx, item.scope, args...)
	}
	// A remote keeps its URLs, without their credentials.
	dir := item.scope.dir
	if dir == "" {
		dir = "/"
	}
	out, err := gitOutputIn(ctx, dir, append(append([]string{"config"}, item.scope.args...), "--get-all", item.key)...)
	if err != nil {
		return fmt.Errorf("reading %s: %s", item.key, strings.TrimSpace(out))
	}
	if err := runConfig(ctx, item.scope, "--unset-all", item.key); err != nil {
		return err
	}
	for _, value := range nonEmptyLines(out) {
		clean, _ := stripURLCredentials(value)
		if err := runConfig(ctx, item.scope, "--add", item.key, clean); err != nil {
			return err
		}
	}
	return nil
}

// runConfig changes a scope's git config. The system file belongs to root,
// so a refused write there is retried with sudo.
func runConfig(ctx context.Context, scope configScope, args ...string) error {
	dir := scope.dir
	if dir == "" {
		dir = "/"
	}
	full := append(append([]string{"config"}, scope.args...), args...)
	out, err := gitOutputIn(ctx, dir, full...)
	if err == nil {
		return nil
	}
	if scope.system {
		cmd := exec.CommandContext(ctx, "sudo", append([]string{"-n", "git"}, full...)...)
		cmd.Dir = dir
		more, sudoErr := cmd.CombinedOutput()
		if sudoErr == nil {
			return nil
		}
		out += string(more)
	}
	return fmt.Errorf("git config %s: %s", strings.Join(args, " "), strings.TrimSpace(out))
}

// credentialRepoDirs lists the repos whose config is searched: every repo
// in the workspace, since a converted workspace keeps all of them.
func credentialRepoDirs(root string) []string {
	dirs, err := findRepos(root)
	if err != nil {
		return nil
	}
	return dirs
}

type credentialFailure struct {
	credentialItem
	Error string `json:"error"`
}

// handleCredentialsStrip strips the credentials named by id, then lists what
// is still there. Ids no longer detected are skipped: they are already gone.
func handleCredentialsStrip(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxGitRequestBody)
	var req struct {
		IDs []string `json:"ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request body")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), repoTimeout)
	defer cancel()

	root, err := filepath.EvalSymlinks(workspaceRoot)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "resolving workspace root: "+err.Error())
		return
	}
	dirs := credentialRepoDirs(root)
	stripped := []credentialItem{}
	failed := []credentialFailure{}
	for _, item := range detectCredentials(ctx, root, dirs) {
		if !slices.Contains(req.IDs, item.ID) {
			continue
		}
		if err := stripCredential(ctx, item); err != nil {
			failed = append(failed, credentialFailure{item, err.Error()})
			continue
		}
		stripped = append(stripped, item)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"stripped":  stripped,
		"failed":    failed,
		"remaining": detectCredentials(ctx, root, dirs),
	})
}
