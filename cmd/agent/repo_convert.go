package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

// Conversion endpoints run in a standalone workspace that devpad is turning
// into a project's workspace. They find the workspace's repos, bundle every
// branch of one so devpad can import it into the new canonical repo, or make
// a repo from the workspace's files when it has none. None of them moves or
// deletes the user's files.

const (
	// discoverMaxDepth is how far below the workspace root repos are found.
	discoverMaxDepth = 3
	// discoverMaxEntries bounds the directory walk in large workspaces.
	discoverMaxEntries = 20000
	// maxSensitiveListed caps the sensitive paths a preview names.
	maxSensitiveListed = 50
	// maxRefsListed caps the branches and tags a discovered repo names.
	maxRefsListed = 200
)

// discoverSkipDirs are never searched for repos.
var discoverSkipDirs = map[string]bool{
	"node_modules": true, ".cache": true, ".npm": true, ".venv": true,
	"venv": true, "__pycache__": true, ".git": true,
}

// junkPatterns are left out of a repo made from the workspace's files.
var junkPatterns = []string{
	"node_modules/", ".venv/", "venv/", "__pycache__/", ".cache/",
	".next/", ".nuxt/", "dist/", "build/", "target/", "coverage/",
	"*.log", ".DS_Store",
}

// sensitivePatterns are left out of a repo made from the workspace's files
// unless the user opts in: they usually hold secrets.
var sensitivePatterns = []string{
	".env", ".env.*", "*.pem", "*.key", "*.p12", "*.pfx",
	"id_rsa*", "id_dsa*", "id_ecdsa*", "id_ed25519*",
	".netrc", ".git-credentials", ".npmrc", ".pypirc",
}

type discoveredRepo struct {
	// Path is relative to the workspace root; "." is the root itself.
	Path        string   `json:"path"`
	Branch      string   `json:"branch"`
	BranchCount int      `json:"branchCount"`
	Branches    []string `json:"branches"`
	TagCount    int      `json:"tagCount"`
	Tags        []string `json:"tags"`
	Dirty       bool     `json:"dirty"`
	// OriginURL has any credentials removed; OriginHadCredentials says so.
	OriginURL            string `json:"originUrl"`
	OriginHadCredentials bool   `json:"originHadCredentials"`
	OriginDefaultBranch  string `json:"originDefaultBranch"`
	// RemoteOnlyBranches exist as origin/<name> but not locally, so they are
	// not imported; BehindBranches are local branches behind origin's.
	RemoteOnlyBranches []string    `json:"remoteOnlyBranches"`
	BehindBranches     []branchLag `json:"behindBranches"`
	// HasDevpadRemote is set when a remote is named "devpad", which devpad
	// needs for its own copies of project branches.
	HasDevpadRemote bool `json:"hasDevpadRemote"`
}

type branchLag struct {
	Name   string `json:"name"`
	Behind int    `json:"behind"`
}

type contentsPreview struct {
	Files          int      `json:"files"`
	Bytes          int64    `json:"bytes"`
	SensitiveFiles []string `json:"sensitiveFiles"`
	SensitiveCount int      `json:"sensitiveCount"`
	SensitiveBytes int64    `json:"sensitiveBytes"`
	// JunkPatterns and SensitivePatterns are what gets left out.
	JunkPatterns      []string `json:"junkPatterns"`
	SensitivePatterns []string `json:"sensitivePatterns"`
	MaxBytes          int64    `json:"maxBytes"`
}

type repoDiscovery struct {
	Repos []discoveredRepo `json:"repos"`
	// Contents previews a repo made from the workspace's files; it is only
	// set when the workspace has no repo at all.
	Contents *contentsPreview `json:"contents,omitempty"`
	// Credentials lists what a converted workspace must not keep, with no
	// secret values.
	Credentials []credentialItem `json:"credentials"`
	// CredentialIncludes names the config includes searched as well, and
	// CredentialScanIncomplete says the search for repos hit its limit.
	CredentialIncludes       []string `json:"credentialIncludes"`
	CredentialScanIncomplete bool     `json:"credentialScanIncomplete"`
}

// handleRepoDiscover lists the git repos in the workspace, or previews a repo
// made from its files when there are none.
func handleRepoDiscover(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), repoTimeout)
	defer cancel()

	root, err := filepath.EvalSymlinks(workspaceRoot)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "resolving workspace root: "+err.Error())
		return
	}
	dirs, err := findRepos(root)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	creds := detectCredentials(ctx, root)
	result := repoDiscovery{
		Repos:                    []discoveredRepo{},
		Credentials:              creds.Items,
		CredentialIncludes:       creds.Includes,
		CredentialScanIncomplete: creds.Incomplete,
	}
	for _, dir := range dirs {
		repo, err := describeRepo(ctx, root, dir)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		result.Repos = append(result.Repos, *repo)
	}
	if len(result.Repos) == 0 {
		preview, err := previewContents(ctx, root)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		result.Contents = preview
	}
	writeJSON(w, http.StatusOK, result)
}

// findRepos walks the workspace for repo roots, without following symlinks
// or descending into a repo once found.
func findRepos(root string) ([]string, error) {
	var repos []string
	seen := 0
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			// Unreadable directories are skipped, not fatal.
			if d != nil && d.IsDir() && path != root {
				return fs.SkipDir
			}
			return nil
		}
		if !d.IsDir() {
			return nil
		}
		if seen++; seen > discoverMaxEntries {
			return fs.SkipAll
		}
		rel, _ := filepath.Rel(root, path)
		if path != root {
			if discoverSkipDirs[d.Name()] {
				return fs.SkipDir
			}
			if strings.Count(rel, string(filepath.Separator))+1 > discoverMaxDepth {
				return fs.SkipDir
			}
		}
		// Only a .git directory marks a repo; a .git file is a submodule or
		// worktree that belongs to another repo.
		if info, err := os.Lstat(filepath.Join(path, ".git")); err == nil && info.IsDir() {
			repos = append(repos, path)
			return fs.SkipDir
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("searching for repos: %w", err)
	}
	return repos, nil
}

func describeRepo(ctx context.Context, root, dir string) (*discoveredRepo, error) {
	rel, err := filepath.Rel(root, dir)
	if err != nil {
		return nil, err
	}
	repo := &discoveredRepo{
		Path: filepath.ToSlash(rel), Branches: []string{}, Tags: []string{},
		RemoteOnlyBranches: []string{}, BehindBranches: []branchLag{},
	}
	if out, err := gitOutputIn(ctx, dir, "symbolic-ref", "--quiet", "--short", "HEAD"); err == nil {
		repo.Branch = strings.TrimSpace(out)
	}
	local := map[string]string{}
	if out, err := gitOutputIn(ctx, dir, "for-each-ref", "--format=%(refname:strip=2) %(objectname)", "refs/heads/"); err == nil {
		for _, line := range nonEmptyLines(out) {
			name, sha, _ := strings.Cut(line, " ")
			local[name] = sha
			repo.BranchCount++
			if len(repo.Branches) < maxRefsListed {
				repo.Branches = append(repo.Branches, name)
			}
		}
	}
	if out, err := gitOutputIn(ctx, dir, "for-each-ref", "--format=%(refname:strip=2)", "refs/tags/"); err == nil {
		for _, name := range nonEmptyLines(out) {
			repo.TagCount++
			if len(repo.Tags) < maxRefsListed {
				repo.Tags = append(repo.Tags, name)
			}
		}
	}
	if out, err := gitOutputIn(ctx, dir, "status", "--porcelain"); err == nil {
		repo.Dirty = strings.TrimSpace(out) != ""
	}
	if out, err := gitOutputIn(ctx, dir, "symbolic-ref", "--quiet", "--short", "refs/remotes/origin/HEAD"); err == nil {
		repo.OriginDefaultBranch = strings.TrimPrefix(strings.TrimSpace(out), "origin/")
	}
	// Only local branches are imported, so say which of origin's are missing
	// or newer than the local copy.
	if out, err := gitOutputIn(ctx, dir, "for-each-ref", "--format=%(refname:strip=3) %(objectname)", "refs/remotes/origin/"); err == nil {
		for _, line := range nonEmptyLines(out) {
			name, sha, _ := strings.Cut(line, " ")
			if name == "HEAD" {
				continue
			}
			localSHA, ok := local[name]
			switch {
			case !ok:
				if len(repo.RemoteOnlyBranches) < maxRefsListed {
					repo.RemoteOnlyBranches = append(repo.RemoteOnlyBranches, name)
				}
			case localSHA != sha && len(repo.BehindBranches) < maxRefsListed:
				count, err := gitOutputIn(ctx, dir, "rev-list", "--count", "refs/heads/"+name+"..refs/remotes/origin/"+name)
				if n, convErr := strconv.Atoi(strings.TrimSpace(count)); err == nil && convErr == nil && n > 0 {
					repo.BehindBranches = append(repo.BehindBranches, branchLag{Name: name, Behind: n})
				}
			}
		}
	}

	out, _ := gitOutputIn(ctx, dir, "config", "--local", "--get-regexp", `^remote\..*\.url$`)
	for _, line := range nonEmptyLines(out) {
		key, value, _ := strings.Cut(line, " ")
		name := strings.TrimSuffix(strings.TrimPrefix(key, "remote."), ".url")
		if name == "origin" {
			repo.OriginURL, repo.OriginHadCredentials = stripURLCredentials(value)
		}
		if name == sandboxBaseRemote {
			repo.HasDevpadRemote = true
		}
	}
	return repo, nil
}

// stripURLCredentials removes a password or token from a URL. A bare user
// name, as in ssh://git@host/repo, is not a credential and stays, and a
// non-HTTP URL keeps its user name when only the password goes. The URL is
// split by hand rather than parsed, so one Go cannot parse, such as a
// password with a stray % or #, is still caught.
func stripURLCredentials(raw string) (string, bool) {
	scheme, rest, ok := strings.Cut(raw, "://")
	if !ok || scheme == "" {
		return raw, false
	}
	authority, path := rest, ""
	if i := strings.IndexByte(rest, '/'); i >= 0 {
		authority, path = rest[:i], rest[i:]
	}
	at := strings.LastIndex(authority, "@")
	if at < 0 {
		return raw, false
	}
	userinfo, host := authority[:at], authority[at+1:]
	user, _, hasPassword := strings.Cut(userinfo, ":")
	isHTTP := strings.HasPrefix(strings.ToLower(scheme), "http")
	switch {
	case isHTTP:
		return scheme + "://" + host + path, true
	case hasPassword:
		return scheme + "://" + user + "@" + host + path, true
	default:
		return raw, false
	}
}

func nonEmptyLines(s string) []string {
	lines := []string{}
	for _, line := range strings.Split(s, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

// previewContents reports what a repo made from the workspace's files would
// hold. Git lists the files, with the same ignore rules the commit uses, from
// a scratch repo outside the workspace so nothing is written into it.
func previewContents(ctx context.Context, root string) (*contentsPreview, error) {
	withSensitive, err := listContents(ctx, root, false)
	if err != nil {
		return nil, err
	}
	without, err := listContents(ctx, root, true)
	if err != nil {
		return nil, err
	}
	kept := make(map[string]bool, len(without))
	preview := &contentsPreview{
		SensitiveFiles:    []string{},
		JunkPatterns:      junkPatterns,
		SensitivePatterns: sensitivePatterns,
		MaxBytes:          maxBundleBytes,
	}
	for _, path := range without {
		kept[path] = true
		preview.Files++
		preview.Bytes += fileSize(root, path)
	}
	for _, path := range withSensitive {
		if kept[path] {
			continue
		}
		preview.SensitiveCount++
		preview.SensitiveBytes += fileSize(root, path)
		if len(preview.SensitiveFiles) < maxSensitiveListed {
			preview.SensitiveFiles = append(preview.SensitiveFiles, path)
		}
	}
	return preview, nil
}

// listContents lists the files a repo made from root would commit, leaving
// out junk and, when excludeSensitive is set, likely secrets.
func listContents(ctx context.Context, root string, excludeSensitive bool) ([]string, error) {
	scratch, err := os.MkdirTemp("", "devpad-contents-")
	if err != nil {
		return nil, fmt.Errorf("creating scratch repo: %w", err)
	}
	defer os.RemoveAll(scratch)
	if out, err := gitOutputIn(ctx, scratch, "init", "--quiet"); err != nil {
		return nil, fmt.Errorf("creating scratch repo: %s", strings.TrimSpace(out))
	}
	excludes := filepath.Join(scratch, "excludes")
	if err := os.WriteFile(excludes, []byte(excludeRules(excludeSensitive)), 0o644); err != nil {
		return nil, fmt.Errorf("writing exclude rules: %w", err)
	}
	out, err := gitOutputIn(ctx, root, "--git-dir="+filepath.Join(scratch, ".git"), "--work-tree="+root,
		"ls-files", "--others", "-z", "--exclude-standard", "--exclude-from="+excludes)
	if err != nil {
		return nil, fmt.Errorf("listing workspace files: %s", strings.TrimSpace(out))
	}
	var paths []string
	for _, p := range strings.Split(out, "\x00") {
		if p != "" {
			paths = append(paths, p)
		}
	}
	return paths, nil
}

func excludeRules(excludeSensitive bool) string {
	var b strings.Builder
	b.WriteString("# Left out when devpad made this repo from the workspace's files.\n")
	for _, p := range junkPatterns {
		b.WriteString(p + "\n")
	}
	if excludeSensitive {
		for _, p := range sensitivePatterns {
			b.WriteString(p + "\n")
		}
	}
	return b.String()
}

func fileSize(root, rel string) int64 {
	info, err := os.Lstat(filepath.Join(root, rel))
	if err != nil {
		return 0
	}
	return info.Size()
}

// resolveWorkspaceDir resolves a path relative to the workspace root,
// refusing anything that leaves it, including through symlinks.
func resolveWorkspaceDir(rel string) (string, error) {
	if rel == "" {
		rel = "."
	}
	if filepath.IsAbs(rel) || strings.HasPrefix(rel, "-") {
		return "", fmt.Errorf("dir must be relative to the workspace")
	}
	clean := filepath.Clean(rel)
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("dir must stay inside the workspace")
	}
	root, err := filepath.EvalSymlinks(workspaceRoot)
	if err != nil {
		return "", fmt.Errorf("resolving workspace root: %w", err)
	}
	dir, err := filepath.EvalSymlinks(filepath.Join(root, clean))
	if err != nil {
		return "", fmt.Errorf("dir not found: %s", rel)
	}
	if dir != root && !strings.HasPrefix(dir, root+string(filepath.Separator)) {
		return "", fmt.Errorf("dir must stay inside the workspace")
	}
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return "", fmt.Errorf("dir is not a directory: %s", rel)
	}
	return dir, nil
}

// handleRepoBundleAll streams a bundle of every branch and tag of the repo at
// dir, a path relative to the workspace root.
func handleRepoBundleAll(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), repoTimeout)
	defer cancel()

	dir, err := resolveWorkspaceDir(r.URL.Query().Get("dir"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	// The dir must be a repo's own root, not a folder inside some other repo.
	top, err := gitOutputIn(ctx, dir, "rev-parse", "--show-toplevel")
	if err != nil {
		writeErr(w, http.StatusNotFound, "not a git repository: "+r.URL.Query().Get("dir"))
		return
	}
	if resolved, err := filepath.EvalSymlinks(strings.TrimSpace(top)); err != nil || resolved != dir {
		writeErr(w, http.StatusBadRequest, "dir is not the root of a repository")
		return
	}
	if out, _ := gitOutputIn(ctx, dir, "for-each-ref", "--count=1", "refs/heads/"); strings.TrimSpace(out) == "" {
		writeErr(w, http.StatusConflict, "the repository has no commits")
		return
	}

	path, cleanup, err := tempBundlePath()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer cleanup()
	if out, err := gitOutputIn(ctx, dir, "bundle", "create", path, "--branches", "--tags"); err != nil {
		writeErr(w, http.StatusInternalServerError, "creating bundle: "+strings.TrimSpace(out))
		return
	}
	f, err := os.Open(path)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "opening bundle: "+err.Error())
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", "application/octet-stream")
	w.WriteHeader(http.StatusOK)
	io.Copy(w, f)
}

// handleRepoInitContents makes a repo at the workspace root from the files
// already there, committing everything except junk and, unless the user opts
// in, likely secrets. The exclusions go in .git/info/exclude, so no file in
// the workspace is added, moved or changed.
func handleRepoInitContents(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxGitRequestBody)
	var req struct {
		DefaultBranch    string `json:"defaultBranch"`
		IncludeSensitive bool   `json:"includeSensitive"`
		Name             string `json:"name"`
		Email            string `json:"email"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request body")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), repoTimeout)
	defer cancel()

	if err := validateBranchName(ctx, req.DefaultBranch, "defaultBranch"); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	for _, f := range [][2]string{{req.Name, "name"}, {req.Email, "email"}} {
		if err := validateUserIdentity(f[0], f[1]); err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	root, err := filepath.EvalSymlinks(workspaceRoot)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "resolving workspace root: "+err.Error())
		return
	}
	if _, err := os.Lstat(filepath.Join(root, ".git")); err == nil {
		writeErr(w, http.StatusConflict, "workspace already contains a repository")
		return
	}
	if dirs, err := findRepos(root); err != nil || len(dirs) > 0 {
		writeErr(w, http.StatusConflict, "workspace already contains a repository")
		return
	}

	files, err := listContents(ctx, root, !req.IncludeSensitive)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	var size int64
	for _, f := range files {
		size += fileSize(root, f)
	}
	if size > maxBundleBytes {
		writeErr(w, http.StatusRequestEntityTooLarge, fmt.Sprintf("the workspace's files total %d MB, over the %d MB limit", size>>20, maxBundleBytes>>20))
		return
	}

	sha, err := initRepoFromContents(ctx, root, req.DefaultBranch, req.Name, req.Email, !req.IncludeSensitive)
	if err != nil {
		// The repo is new, so removing it puts the workspace back as it was.
		os.RemoveAll(filepath.Join(root, ".git"))
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"branch": req.DefaultBranch, "sha": sha, "files": len(files)})
}

func initRepoFromContents(ctx context.Context, root, branch, name, email string, excludeSensitive bool) (string, error) {
	if out, err := gitOutputIn(ctx, root, "init", "--quiet", "--initial-branch="+branch); err != nil {
		return "", fmt.Errorf("initializing repo: %s", strings.TrimSpace(out))
	}
	exclude := filepath.Join(root, ".git", "info", "exclude")
	if err := os.MkdirAll(filepath.Dir(exclude), 0o755); err != nil {
		return "", fmt.Errorf("creating .git/info: %w", err)
	}
	if err := os.WriteFile(exclude, []byte(excludeRules(excludeSensitive)), 0o644); err != nil {
		return "", fmt.Errorf("writing exclude rules: %w", err)
	}
	for _, kv := range [][2]string{{"user.name", name}, {"user.email", email}} {
		if kv[1] == "" {
			continue
		}
		if out, err := gitOutputIn(ctx, root, "config", kv[0], kv[1]); err != nil {
			return "", fmt.Errorf("setting %s: %s", kv[0], strings.TrimSpace(out))
		}
	}
	if out, err := gitOutputIn(ctx, root, "add", "--all"); err != nil {
		return "", fmt.Errorf("adding files: %s", strings.TrimSpace(out))
	}
	commit := []string{"-c", "core.hooksPath=/dev/null"}
	if name == "" {
		commit = append(commit, "-c", "user.name=devpad")
	}
	if email == "" {
		commit = append(commit, "-c", "user.email=devpad@localhost")
	}
	commit = append(commit, "commit", "--quiet", "--allow-empty", "--no-verify", "-m", "Initial commit from the workspace's files")
	if out, err := gitOutputIn(ctx, root, commit...); err != nil {
		return "", fmt.Errorf("committing files: %s", strings.TrimSpace(out))
	}
	out, err := gitOutputIn(ctx, root, "rev-parse", "HEAD")
	if err != nil {
		return "", fmt.Errorf("resolving HEAD: %s", strings.TrimSpace(out))
	}
	gitDir := filepath.Join(root, ".git")
	digest, err := repoStateDigest(gitDir)
	if err != nil {
		return "", fmt.Errorf("recording the repo's state: %w", err)
	}
	if err := os.WriteFile(filepath.Join(gitDir, initStateFile), []byte(digest+"\n"), 0o644); err != nil {
		return "", fmt.Errorf("recording the repo's state: %w", err)
	}
	return strings.TrimSpace(out), nil
}

// initStateFile holds, inside a repo made from the workspace's files, a
// digest of the repo's own state as devpad left it. The undo removes the
// repo only while that state is unchanged, so a remote, hook, branch switch
// or anything else the user did in it is never thrown away with it.
const initStateFile = "devpad-init"

// initStateVolatile are the .git entries the digest leaves out: objects
// and refs are checked by commit, the index by comparing it with HEAD, and
// git rewrites the rest by itself.
var initStateVolatile = map[string]bool{
	"objects": true, "refs": true, "logs": true, "index": true, "COMMIT_EDITMSG": true, initStateFile: true,
}

// repoStateDigest hashes the names, modes and contents of everything in a
// .git directory but its volatile entries: config, HEAD, hooks, info and
// any state a git command leaves, such as ORIG_HEAD or a worktree.
func repoStateDigest(gitDir string) (string, error) {
	h := sha256.New()
	err := filepath.WalkDir(gitDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(gitDir, p)
		if err != nil || rel == "." {
			return err
		}
		rel = filepath.ToSlash(rel)
		if top, _, _ := strings.Cut(rel, "/"); initStateVolatile[top] {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		fmt.Fprintf(h, "%s\x00%s\x00", rel, info.Mode())
		switch {
		case info.Mode().IsRegular():
			f, err := os.Open(p)
			if err != nil {
				return err
			}
			_, err = io.Copy(h, f)
			f.Close()
			if err != nil {
				return err
			}
		case info.Mode()&fs.ModeSymlink != 0:
			target, err := os.Readlink(p)
			if err != nil {
				return err
			}
			io.WriteString(h, target)
		}
		h.Write([]byte{0})
		return nil
	})
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// handleRepoUndoInitContents removes a repo made by handleRepoInitContents,
// so a conversion that fails afterwards leaves the workspace as it was. It
// only removes a repo that still holds nothing but that one commit.
func handleRepoUndoInitContents(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), gitTimeout)
	defer cancel()

	sha := r.URL.Query().Get("sha")
	if err := validateGitCommitHash(sha); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	root, err := filepath.EvalSymlinks(workspaceRoot)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "resolving workspace root: "+err.Error())
		return
	}
	gitDir := filepath.Join(root, ".git")
	if info, err := os.Lstat(gitDir); err != nil || !info.IsDir() {
		writeJSON(w, http.StatusOK, map[string]bool{"removed": false})
		return
	}
	head, _ := gitOutputIn(ctx, root, "rev-parse", "HEAD")
	refs, _ := gitOutputIn(ctx, root, "for-each-ref", "--format=%(objectname)")
	count, _ := gitOutputIn(ctx, root, "rev-list", "--all", "--count")
	if strings.TrimSpace(head) != sha || strings.TrimSpace(count) != "1" || slices.ContainsFunc(nonEmptyLines(refs), func(ref string) bool { return ref != sha }) ||
		!repoStateUnchanged(ctx, root) {
		writeErr(w, http.StatusConflict, "the repository has changed since devpad made it; it was left in place")
		return
	}
	if err := os.RemoveAll(gitDir); err != nil {
		writeErr(w, http.StatusInternalServerError, "removing repository: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"removed": true})
}

// repoStateUnchanged reports whether a repo made from the workspace's files
// is as devpad left it: the same .git state, and nothing staged. Anything it
// cannot check counts as changed.
func repoStateUnchanged(ctx context.Context, root string) bool {
	gitDir := filepath.Join(root, ".git")
	want, err := os.ReadFile(filepath.Join(gitDir, initStateFile))
	if err != nil {
		return false
	}
	got, err := repoStateDigest(gitDir)
	if err != nil || strings.TrimSpace(string(want)) != got {
		return false
	}
	_, err = gitOutputIn(ctx, root, "diff", "--cached", "--quiet", "HEAD")
	return err == nil
}

// managedSSHFileNames are the files devpad writes into ~/.ssh of a
// standalone workspace.
var managedSSHFileNames = []string{"id_ed25519", "id_ed25519.pub", "known_hosts", "config"}

type sshCleanupResult struct {
	Removed []string `json:"removed"`
	// Kept lists files that exist but differ from what devpad wrote, so they
	// may be the user's own and are left alone. Devpad's key is never kept.
	Kept   []string `json:"kept"`
	Absent []string `json:"absent"`
}

// handleSSHRemoveManaged removes the SSH files devpad put in the workspace.
// Devpad's key pair (devpadSSHFiles) is removed whatever it holds: devpad
// overwrites it on every start, so a file there is devpad's key, stale or
// tampered with. known_hosts and config are only removed if they still hash
// to what devpad wrote, so a file the user changed or made is kept.
func handleSSHRemoveManaged(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxGitRequestBody)
	var req struct {
		Files []struct {
			Name   string `json:"name"`
			SHA256 string `json:"sha256"`
		} `json:"files"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request body")
		return
	}
	home, err := os.UserHomeDir()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "finding home directory: "+err.Error())
		return
	}
	result := sshCleanupResult{Removed: []string{}, Kept: []string{}, Absent: []string{}}
	for _, f := range req.Files {
		if !slices.Contains(managedSSHFileNames, f.Name) {
			writeErr(w, http.StatusBadRequest, "not a devpad SSH file: "+f.Name)
			return
		}
		display := "~/.ssh/" + f.Name
		path := filepath.Join(home, ".ssh", f.Name)
		info, err := os.Lstat(path)
		if errors.Is(err, fs.ErrNotExist) {
			result.Absent = append(result.Absent, display)
			continue
		}
		always := slices.Contains(devpadSSHFiles, f.Name)
		switch {
		case err != nil:
			result.Kept = append(result.Kept, display)
			continue
		case always && info.IsDir():
			writeErr(w, http.StatusConflict, display+" is a directory")
			return
		case !always:
			if !info.Mode().IsRegular() {
				result.Kept = append(result.Kept, display)
				continue
			}
			content, err := os.ReadFile(path)
			if err != nil {
				writeErr(w, http.StatusInternalServerError, "reading "+display+": "+err.Error())
				return
			}
			sum := sha256.Sum256(content)
			if hex.EncodeToString(sum[:]) != strings.ToLower(f.SHA256) {
				result.Kept = append(result.Kept, display)
				continue
			}
		}
		if err := os.Remove(path); err != nil {
			writeErr(w, http.StatusInternalServerError, "removing "+display+": "+err.Error())
			return
		}
		result.Removed = append(result.Removed, display)
	}
	writeJSON(w, http.StatusOK, result)
}

// gitOutputIn is gitOutput for a repo in dir.
func gitOutputIn(ctx context.Context, dir string, args ...string) (string, error) {
	out, err := gitCommandIn(ctx, dir, args...).CombinedOutput()
	return string(out), err
}

// handleRepoImportInitial fills a new, empty canonical repo with every branch
// and tag of an untrusted bundle from a converted workspace, including the
// default branch. It refuses a repo that already has a branch or tag, so it
// can only seed a new project; after that, branches only move through the
// usual imports and merges. Objects are fsck-checked in a quarantine first,
// and the refs are created in one transaction.
func handleRepoImportInitial(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), repoTimeout)
	defer cancel()

	defaultBranch := r.URL.Query().Get("defaultBranch")
	if err := validateBranchName(ctx, defaultBranch, "defaultBranch"); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if out, err := gitOutput(ctx, "for-each-ref", "--count=1", "refs/heads/", "refs/tags/"); err != nil || strings.TrimSpace(out) != "" {
		writeErr(w, http.StatusConflict, "the project repo already has branches")
		return
	}
	bundle, cleanup, err := receiveBundle(w, r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	defer cleanup()

	if err := importInitialBundle(ctx, bundle, defaultBranch); err != nil {
		var bad *badBundleError
		if errors.As(err, &bad) {
			writeErr(w, http.StatusBadRequest, err.Error())
		} else {
			writeErr(w, http.StatusInternalServerError, err.Error())
		}
		return
	}
	writeRepoRefs(ctx, w)
}

func importInitialBundle(ctx context.Context, bundle, defaultBranch string) error {
	if out, err := gitOutput(ctx, "bundle", "verify", "--quiet", bundle); err != nil {
		return &badBundleError{"invalid bundle: " + strings.TrimSpace(out)}
	}
	suffix, err := randomHex(8)
	if err != nil {
		return err
	}
	quarantine := quarantineRefPrefix + suffix + "/"
	defer deleteRefsUnder(context.WithoutCancel(ctx), quarantine)

	if out, err := gitOutput(ctx, "-c", "transfer.fsckObjects=true", "-c", "fetch.fsckObjects=true",
		"fetch", "--no-tags", "--no-write-fetch-head", "--", bundle,
		"refs/heads/*:"+quarantine+"heads/*", "refs/tags/*:"+quarantine+"tags/*"); err != nil {
		return &badBundleError{"fetching bundle: " + strings.TrimSpace(out)}
	}
	out, err := gitOutput(ctx, "for-each-ref", "--format=%(refname) %(objectname) %(objecttype)", quarantine)
	if err != nil {
		return fmt.Errorf("listing imported refs: %s", strings.TrimSpace(out))
	}

	var tx strings.Builder
	tx.WriteString("start\n")
	hasDefault := false
	for _, line := range nonEmptyLines(out) {
		fields := strings.Fields(line)
		if len(fields) != 3 {
			continue
		}
		rel := strings.TrimPrefix(fields[0], quarantine)
		var target string
		switch {
		case strings.HasPrefix(rel, "heads/"):
			name := strings.TrimPrefix(rel, "heads/")
			if err := validateBranchName(ctx, name, "branch"); err != nil {
				return &badBundleError{err.Error()}
			}
			if fields[2] != "commit" {
				return &badBundleError{"branch " + name + " is not a commit"}
			}
			hasDefault = hasDefault || name == defaultBranch
			target = "refs/heads/" + name
		case strings.HasPrefix(rel, "tags/"):
			name := strings.TrimPrefix(rel, "tags/")
			if err := validateGitRef(name, "tag"); err != nil {
				return &badBundleError{err.Error()}
			}
			if out, err := gitOutput(ctx, "check-ref-format", "refs/tags/"+name); err != nil {
				return &badBundleError{"tag is not a valid name: " + strings.TrimSpace(out)}
			}
			target = "refs/tags/" + name
		default:
			continue
		}
		// create fails if the ref exists, so nothing already there is moved.
		fmt.Fprintf(&tx, "create %s %s\n", target, fields[1])
	}
	if !hasDefault {
		return &badBundleError{"the repository has no branch named " + defaultBranch}
	}
	tx.WriteString("commit\n")

	cmd := gitCommand(ctx, "update-ref", "--stdin")
	cmd.Stdin = strings.NewReader(tx.String())
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("creating branches: %s", strings.TrimSpace(string(out)))
	}
	if out, err := gitOutput(ctx, "symbolic-ref", "HEAD", "refs/heads/"+defaultBranch); err != nil {
		return fmt.Errorf("setting default branch: %s", strings.TrimSpace(out))
	}
	return nil
}

// deleteRefsUnder deletes every ref under prefix.
func deleteRefsUnder(ctx context.Context, prefix string) error {
	out, err := gitStdout(ctx, "for-each-ref", "--format=delete %(refname)", prefix)
	if err != nil {
		return fmt.Errorf("listing refs under %s: %w", prefix, err)
	}
	if strings.TrimSpace(out) == "" {
		return nil
	}
	cmd := gitCommand(ctx, "update-ref", "--stdin")
	cmd.Stdin = strings.NewReader(out)
	if combined, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("deleting refs under %s: %s", prefix, strings.TrimSpace(string(combined)))
	}
	return nil
}
