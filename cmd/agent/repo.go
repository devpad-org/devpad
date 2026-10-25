package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// Repo endpoints move commits between a project's canonical bare repo and its
// sandboxes using git bundles. Devpad drives every transfer: sandboxes never
// reach the canonical repo, and the canonical repo only accepts what devpad
// imports into a branch it names.

const (
	repoTimeout = 10 * time.Minute

	// maxBundleBytes caps bundles accepted from devpad. Bundles from sandboxes
	// are untrusted, so this bounds disk use in the receiving container.
	maxBundleBytes = 512 << 20

	quarantineRefPrefix = "refs/devpad/quarantine/"
)

// Initial commit identity for canonical repos created without an upstream.
var repoInitEnv = []string{
	"GIT_AUTHOR_NAME=devpad",
	"GIT_AUTHOR_EMAIL=devpad@localhost",
	"GIT_COMMITTER_NAME=devpad",
	"GIT_COMMITTER_EMAIL=devpad@localhost",
}

type repoBranchResult struct {
	Branch string `json:"branch"`
	SHA    string `json:"sha"`
}

type repoRef struct {
	Name string `json:"name"`
	SHA  string `json:"sha"`
}

type repoRefs struct {
	DefaultBranch string    `json:"defaultBranch"`
	Branches      []repoRef `json:"branches"`
}

// validateBranchName rejects names that git would not accept as a branch or
// that could be read as a flag.
func validateBranchName(ctx context.Context, name, field string) error {
	if name == "" {
		return fmt.Errorf("%s is required", field)
	}
	if err := validateGitRef(name, field); err != nil {
		return err
	}
	if out, err := gitOutput(ctx, "check-ref-format", "refs/heads/"+name); err != nil {
		return fmt.Errorf("%s is not a valid branch name: %s", field, strings.TrimSpace(out))
	}
	return nil
}

// handleRepoInit turns /workspace into a canonical bare repo, cloned from an
// upstream when one is given, or seeded with an empty initial commit. It is
// idempotent so devpad can retry a failed project setup.
func handleRepoInit(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxGitRequestBody)
	var req struct {
		DefaultBranch string `json:"defaultBranch"`
		UpstreamURL   string `json:"upstreamUrl"`
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
	if strings.HasPrefix(req.UpstreamURL, "-") {
		writeErr(w, http.StatusBadRequest, "upstreamUrl must not start with '-'")
		return
	}

	if err := initCanonicalRepo(ctx, req.DefaultBranch, req.UpstreamURL); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeRepoRefs(ctx, w)
}

func initCanonicalRepo(ctx context.Context, defaultBranch, upstreamURL string) error {
	if _, err := os.Stat(workspaceRoot + "/HEAD"); err != nil {
		if upstreamURL != "" {
			if out, err := gitOutput(ctx, "clone", "--bare", "--", upstreamURL, "."); err != nil {
				return fmt.Errorf("cloning upstream: %s", strings.TrimSpace(out))
			}
		} else if out, err := gitOutput(ctx, "init", "--bare", "--initial-branch="+defaultBranch, "."); err != nil {
			return fmt.Errorf("initializing repo: %s", strings.TrimSpace(out))
		}
	}

	// Hooks never run in the canonical repo, and every object it receives is
	// checked, since sandbox bundles are untrusted.
	for _, kv := range [][2]string{
		{"core.hooksPath", "/dev/null"},
		{"transfer.fsckObjects", "true"},
		{"fetch.fsckObjects", "true"},
		{"receive.fsckObjects", "true"},
	} {
		if out, err := gitOutput(ctx, "config", kv[0], kv[1]); err != nil {
			return fmt.Errorf("setting %s: %s", kv[0], strings.TrimSpace(out))
		}
	}

	if out, err := gitOutput(ctx, "symbolic-ref", "HEAD", "refs/heads/"+defaultBranch); err != nil {
		return fmt.Errorf("setting default branch: %s", strings.TrimSpace(out))
	}
	if gitExec(ctx, "rev-parse", "--verify", "--quiet", "refs/heads/"+defaultBranch) == nil {
		return nil
	}

	// An empty repo gets an empty initial commit so every sandbox can be
	// seeded from a bundle of the default branch.
	tree, err := gitOutputEnv(ctx, repoInitEnv, "hash-object", "-t", "tree", "-w", "/dev/null")
	if err != nil {
		return fmt.Errorf("creating empty tree: %s", strings.TrimSpace(tree))
	}
	commit, err := gitOutputEnv(ctx, repoInitEnv, "commit-tree", strings.TrimSpace(tree), "-m", "Initial commit")
	if err != nil {
		return fmt.Errorf("creating initial commit: %s", strings.TrimSpace(commit))
	}
	if out, err := gitOutput(ctx, "update-ref", "refs/heads/"+defaultBranch, strings.TrimSpace(commit), ""); err != nil {
		return fmt.Errorf("creating default branch: %s", strings.TrimSpace(out))
	}
	return nil
}

// handleRepoRefs lists the branches of the repo in /workspace.
func handleRepoRefs(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), gitTimeout)
	defer cancel()
	writeRepoRefs(ctx, w)
}

func writeRepoRefs(ctx context.Context, w http.ResponseWriter) {
	refs, err := listRepoRefs(ctx)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, refs)
}

func listRepoRefs(ctx context.Context) (*repoRefs, error) {
	out, err := gitOutput(ctx, "for-each-ref", "--format=%(refname:strip=2) %(objectname)", "refs/heads/")
	if err != nil {
		return nil, fmt.Errorf("listing branches: %s", strings.TrimSpace(out))
	}
	refs := &repoRefs{Branches: []repoRef{}}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		name, sha, ok := strings.Cut(line, " ")
		if ok {
			refs.Branches = append(refs.Branches, repoRef{Name: name, SHA: sha})
		}
	}
	if head, err := gitOutput(ctx, "symbolic-ref", "--short", "HEAD"); err == nil {
		refs.DefaultBranch = strings.TrimSpace(head)
	}
	return refs, nil
}

// handleRepoBundle streams a bundle containing one branch.
func handleRepoBundle(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), repoTimeout)
	defer cancel()

	branch := r.URL.Query().Get("branch")
	if err := validateBranchName(ctx, branch, "branch"); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if gitExec(ctx, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch) != nil {
		writeErr(w, http.StatusNotFound, "branch not found: "+branch)
		return
	}

	// Write to a temp file first so a git failure can still be reported as an
	// error status instead of a truncated bundle.
	path, cleanup, err := tempBundlePath()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer cleanup()
	if out, err := gitOutput(ctx, "bundle", "create", path, "refs/heads/"+branch); err != nil {
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

type repoComparison struct {
	Base      string `json:"base"`
	Head      string `json:"head"`
	BaseSHA   string `json:"baseSha"`
	HeadSHA   string `json:"headSha"`
	MergeBase string `json:"mergeBase"`
	Ahead     int    `json:"ahead"`
	Behind    int    `json:"behind"`
}

// handleRepoCompare reports how far head has diverged from base: commits on
// head that base lacks (ahead) and commits on base that head lacks (behind).
func handleRepoCompare(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), gitTimeout)
	defer cancel()

	q := r.URL.Query()
	cmp := repoComparison{Base: q.Get("base"), Head: q.Get("head")}
	for _, f := range []struct {
		name, field string
		sha         *string
	}{{cmp.Base, "base", &cmp.BaseSHA}, {cmp.Head, "head", &cmp.HeadSHA}} {
		if err := validateBranchName(ctx, f.name, f.field); err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		out, err := gitOutput(ctx, "rev-parse", "--verify", "--quiet", "refs/heads/"+f.name+"^{commit}")
		if err != nil {
			writeErr(w, http.StatusNotFound, "branch not found: "+f.name)
			return
		}
		*f.sha = strings.TrimSpace(out)
	}

	if out, err := gitOutput(ctx, "merge-base", cmp.BaseSHA, cmp.HeadSHA); err == nil {
		cmp.MergeBase = strings.TrimSpace(out)
	}
	out, err := gitOutput(ctx, "rev-list", "--left-right", "--count", cmp.BaseSHA+"..."+cmp.HeadSHA)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "comparing branches: "+strings.TrimSpace(out))
		return
	}
	if _, err := fmt.Sscanf(strings.TrimSpace(out), "%d %d", &cmp.Behind, &cmp.Ahead); err != nil {
		writeErr(w, http.StatusInternalServerError, "parsing rev-list output: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, cmp)
}

// handleRepoSeed clones a bundle of the base branch into an empty /workspace
// and checks out the working branch, creating it unless it is the base
// itself (a sandbox resuming an existing branch). The clone keeps no remote,
// so the sandbox has nothing to push to.
func handleRepoSeed(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), repoTimeout)
	defer cancel()

	q := r.URL.Query()
	base, branch := q.Get("base"), q.Get("branch")
	if err := validateBranchName(ctx, base, "base"); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := validateBranchName(ctx, branch, "branch"); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	name, email := q.Get("name"), q.Get("email")
	if err := validateUserIdentity(name, "name"); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := validateUserIdentity(email, "email"); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if _, err := os.Stat(workspaceRoot + "/.git"); err == nil {
		writeErr(w, http.StatusConflict, "workspace already contains a repository")
		return
	}

	path, cleanup, err := receiveBundle(w, r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	defer cleanup()

	const seedRemote = "devpad-seed"
	steps := [][]string{
		{"clone", "--branch", base, "--origin", seedRemote, "--", path, "."},
		{"remote", "remove", seedRemote},
	}
	if branch != base {
		steps = append(steps, []string{"checkout", "-b", branch})
	}
	if name != "" {
		steps = append(steps, []string{"config", "user.name", name})
	}
	if email != "" {
		steps = append(steps, []string{"config", "user.email", email})
	}
	for _, args := range steps {
		if out, err := gitOutput(ctx, args...); err != nil {
			writeErr(w, http.StatusInternalServerError, fmt.Sprintf("git %s: %s", args[0], strings.TrimSpace(out)))
			return
		}
	}

	sha, err := gitOutput(ctx, "rev-parse", "HEAD")
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "resolving HEAD: "+strings.TrimSpace(sha))
		return
	}
	writeJSON(w, http.StatusOK, repoBranchResult{Branch: branch, SHA: strings.TrimSpace(sha)})
}

// handleRepoImport fetches one branch from an untrusted bundle into the
// canonical repo. Devpad chooses the branch; the bundle's other refs are
// ignored, the default branch is refused, and objects are fsck-checked before
// the branch moves. With ffOnly=true an existing branch only fast-forwards,
// so the import can add commits but never rewrite them.
func handleRepoImport(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), repoTimeout)
	defer cancel()

	branch := r.URL.Query().Get("branch")
	ffOnly := r.URL.Query().Get("ffOnly") == "true"
	if err := validateBranchName(ctx, branch, "branch"); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if head, err := gitOutput(ctx, "symbolic-ref", "--short", "HEAD"); err == nil && strings.TrimSpace(head) == branch {
		writeErr(w, http.StatusForbidden, "cannot import into the default branch")
		return
	}

	path, cleanup, err := receiveBundle(w, r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	defer cleanup()

	sha, err := importBundleBranch(ctx, path, branch, ffOnly)
	if err != nil {
		var bad *badBundleError
		var nonFF *nonFastForwardError
		switch {
		case errors.As(err, &bad):
			writeErr(w, http.StatusBadRequest, err.Error())
		case errors.As(err, &nonFF):
			writeErr(w, http.StatusConflict, err.Error())
		default:
			writeErr(w, http.StatusInternalServerError, err.Error())
		}
		return
	}
	writeJSON(w, http.StatusOK, repoBranchResult{Branch: branch, SHA: sha})
}

type badBundleError struct{ msg string }

func (e *badBundleError) Error() string { return e.msg }

type nonFastForwardError struct{ branch string }

func (e *nonFastForwardError) Error() string {
	return fmt.Sprintf("%s has commits the published branch lacks; merge %s/%s first", e.branch, sandboxBaseRemote, e.branch)
}

func importBundleBranch(ctx context.Context, bundlePath, branch string, ffOnly bool) (string, error) {
	if out, err := gitOutput(ctx, "bundle", "verify", "--quiet", bundlePath); err != nil {
		return "", &badBundleError{"invalid bundle: " + strings.TrimSpace(out)}
	}

	suffix, err := randomHex(8)
	if err != nil {
		return "", err
	}
	quarantine := quarantineRefPrefix + suffix
	defer gitExec(context.WithoutCancel(ctx), "update-ref", "-d", quarantine)

	refspec := "refs/heads/" + branch + ":" + quarantine
	if out, err := gitOutput(ctx, "-c", "transfer.fsckObjects=true", "-c", "fetch.fsckObjects=true",
		"fetch", "--no-tags", "--no-write-fetch-head", "--", bundlePath, refspec); err != nil {
		return "", &badBundleError{"fetching bundle: " + strings.TrimSpace(out)}
	}

	sha, err := gitOutput(ctx, "rev-parse", "--verify", quarantine+"^{commit}")
	if err != nil {
		return "", &badBundleError{"bundle branch is not a commit"}
	}
	sha = strings.TrimSpace(sha)
	args := []string{"update-ref", "refs/heads/" + branch, sha}
	if ffOnly {
		// The old value makes update-ref fail if the branch moves between
		// this check and the update; an empty one means it must not exist.
		current := ""
		if out, err := gitOutput(ctx, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch); err == nil {
			current = strings.TrimSpace(out)
			if current != sha && gitExec(ctx, "merge-base", "--is-ancestor", current, sha) != nil {
				return "", &nonFastForwardError{branch}
			}
		}
		args = append(args, current)
	}
	if out, err := gitOutput(ctx, args...); err != nil {
		return "", fmt.Errorf("updating branch: %s", strings.TrimSpace(out))
	}
	return sha, nil
}

// receiveBundle saves a size-capped request body to a temp file.
func receiveBundle(w http.ResponseWriter, r *http.Request) (string, func(), error) {
	path, cleanup, err := tempBundlePath()
	if err != nil {
		return "", nil, err
	}
	f, err := os.Create(path)
	if err != nil {
		cleanup()
		return "", nil, fmt.Errorf("creating bundle file: %w", err)
	}
	_, copyErr := io.Copy(f, http.MaxBytesReader(w, r.Body, maxBundleBytes))
	closeErr := f.Close()
	if copyErr != nil {
		cleanup()
		return "", nil, fmt.Errorf("receiving bundle: %w", copyErr)
	}
	if closeErr != nil {
		cleanup()
		return "", nil, fmt.Errorf("saving bundle: %w", closeErr)
	}
	return path, cleanup, nil
}

// tempBundlePath returns a path for a bundle outside /workspace, plus a
// cleanup func that removes its directory.
func tempBundlePath() (string, func(), error) {
	dir, err := os.MkdirTemp("", "devpad-bundle-")
	if err != nil {
		return "", nil, fmt.Errorf("creating bundle dir: %w", err)
	}
	return dir + "/repo.bundle", func() { os.RemoveAll(dir) }, nil
}

func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generating random suffix: %w", err)
	}
	return hex.EncodeToString(b), nil
}

func gitOutputEnv(ctx context.Context, env []string, args ...string) (string, error) {
	cmd := gitCommand(ctx, args...)
	cmd.Env = append(cmd.Env, env...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}
