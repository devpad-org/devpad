package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os/exec"
	"path"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// Merge request endpoints. Diff, file, merge and push run in a project's
// canonical bare repo; fetch runs in a sandbox to refresh its copy of the
// default branch. Merges are computed with merge-tree, so no working tree,
// hooks or repo-controlled drivers are involved.

const (
	// maxRepoFileBytes caps file contents returned for diff views.
	maxRepoFileBytes = 5 << 20

	// sandboxBaseRemote is the ref namespace a sandbox's copy of the default
	// branch lives under, as in devpad/main.
	sandboxBaseRemote = "devpad"
	// upstreamRemote is the project workspace's read-only view of the
	// project's upstream, as in origin/main.
	upstreamRemote = "origin"
)

type revError struct {
	status int
	msg    string
}

func (e *revError) Error() string { return e.msg }

// resolveRev resolves a branch name or full commit hash to a commit SHA.
func resolveRev(ctx context.Context, rev, field string) (string, error) {
	if isFullHash(rev) {
		return resolveCommit(ctx, rev, rev, field)
	}
	return resolveBranch(ctx, rev, field)
}

// resolveBranch resolves a branch name, never a hash, to a commit SHA.
func resolveBranch(ctx context.Context, branch, field string) (string, error) {
	if err := validateBranchName(ctx, branch, field); err != nil {
		return "", &revError{http.StatusBadRequest, err.Error()}
	}
	return resolveCommit(ctx, "refs/heads/"+branch, branch, field)
}

func resolveCommit(ctx context.Context, ref, name, field string) (string, error) {
	out, err := gitOutput(ctx, "rev-parse", "--verify", "--quiet", ref+"^{commit}")
	if err != nil {
		return "", &revError{http.StatusNotFound, fmt.Sprintf("%s not found: %s", field, name)}
	}
	return strings.TrimSpace(out), nil
}

func isFullHash(s string) bool {
	if len(s) != 40 && len(s) != 64 {
		return false
	}
	for _, r := range s {
		if !strings.ContainsRune("0123456789abcdef", r) {
			return false
		}
	}
	return true
}

func writeRevError(w http.ResponseWriter, err error) {
	var re *revError
	if errors.As(err, &re) {
		writeErr(w, re.status, re.msg)
		return
	}
	writeErr(w, http.StatusInternalServerError, err.Error())
}

type repoDiffFile struct {
	Path      string `json:"path"`
	OldPath   string `json:"oldPath,omitempty"`
	Status    string `json:"status"`
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
	Binary    bool   `json:"binary"`
}

type repoDiff struct {
	BaseSHA   string         `json:"baseSha"`
	HeadSHA   string         `json:"headSha"`
	MergeBase string         `json:"mergeBase"`
	Files     []repoDiffFile `json:"files"`
}

// handleRepoDiff lists the files head changed since it diverged from base,
// the way a merge request shows them.
func handleRepoDiff(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), gitTimeout)
	defer cancel()

	q := r.URL.Query()
	baseSHA, err := resolveRev(ctx, q.Get("base"), "base")
	if err != nil {
		writeRevError(w, err)
		return
	}
	headSHA, err := resolveRev(ctx, q.Get("head"), "head")
	if err != nil {
		writeRevError(w, err)
		return
	}
	mergeBase, err := gitOutput(ctx, "merge-base", baseSHA, headSHA)
	if err != nil {
		writeErr(w, http.StatusConflict, "base and head have no common history")
		return
	}
	diff := repoDiff{BaseSHA: baseSHA, HeadSHA: headSHA, MergeBase: strings.TrimSpace(mergeBase)}
	if diff.Files, err = diffFiles(ctx, diff.MergeBase, headSHA); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, diff)
}

const (
	// maxRepoLogCommits caps the commits a merge request lists.
	maxRepoLogCommits = 250
	// maxCommitSubjectBytes caps each listed commit's subject, which a
	// sandbox's agent wrote.
	maxCommitSubjectBytes = 500
)

type repoCommit struct {
	SHA     string   `json:"sha"`
	Parents []string `json:"parents"`
	Author  string   `json:"author"`
	// AuthoredAt is the commit's own author date, which whoever made the
	// commit chose.
	AuthoredAt time.Time `json:"authoredAt"`
	Subject    string    `json:"subject"`
}

type repoLog struct {
	Commits []repoCommit `json:"commits"`
	// Truncated says head has more commits base lacks than were listed.
	Truncated bool `json:"truncated"`
}

// handleRepoLog lists the commits head has that base lacks, newest first, the
// way a merge request shows them.
func handleRepoLog(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), gitTimeout)
	defer cancel()

	q := r.URL.Query()
	baseSHA, err := resolveRev(ctx, q.Get("base"), "base")
	if err != nil {
		writeRevError(w, err)
		return
	}
	headSHA, err := resolveRev(ctx, q.Get("head"), "head")
	if err != nil {
		writeRevError(w, err)
		return
	}
	out, err := gitStdout(ctx, "log", "--topo-order", "--no-color", "--no-show-signature", "-z",
		"--format=%H%x1f%P%x1f%an%x1f%at%x1f%s", "-n", strconv.Itoa(maxRepoLogCommits+1), headSHA, "^"+baseSHA)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "listing commits: "+err.Error())
		return
	}
	result := parseRepoLog(out)
	if len(result.Commits) > maxRepoLogCommits {
		result.Commits, result.Truncated = result.Commits[:maxRepoLogCommits], true
	}
	writeJSON(w, http.StatusOK, result)
}

func parseRepoLog(out string) repoLog {
	result := repoLog{Commits: []repoCommit{}}
	for _, record := range strings.Split(strings.TrimSuffix(out, "\x00"), "\x00") {
		fields := strings.SplitN(strings.TrimPrefix(record, "\n"), "\x1f", 5)
		if len(fields) != 5 {
			continue
		}
		parents := strings.Fields(fields[1])
		if parents == nil {
			parents = []string{}
		}
		c := repoCommit{SHA: fields[0], Parents: parents, Author: fields[2], Subject: truncateUTF8(fields[4], maxCommitSubjectBytes)}
		if secs, err := strconv.ParseInt(fields[3], 10, 64); err == nil {
			c.AuthoredAt = time.Unix(secs, 0).UTC()
		}
		result.Commits = append(result.Commits, c)
	}
	return result
}

// truncateUTF8 cuts s to at most n bytes without splitting a character.
func truncateUTF8(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

func diffFiles(ctx context.Context, from, to string) ([]repoDiffFile, error) {
	diffArgs := []string{"diff", "--no-color", "--no-ext-diff", "--no-textconv", "--find-renames", "-z"}
	statusOut, err := gitStdout(ctx, append(diffArgs, "--name-status", from, to)...)
	if err != nil {
		return nil, fmt.Errorf("listing changed files: %w", err)
	}
	numstatOut, err := gitStdout(ctx, append(diffArgs, "--numstat", from, to)...)
	if err != nil {
		return nil, fmt.Errorf("counting changed lines: %w", err)
	}

	files := []repoDiffFile{}
	fields := strings.Split(strings.TrimSuffix(statusOut, "\x00"), "\x00")
	for i := 0; i+1 < len(fields); {
		f := repoDiffFile{Status: fields[i][:1]}
		if f.Status == "R" || f.Status == "C" {
			if i+2 >= len(fields) {
				break
			}
			f.OldPath, f.Path = fields[i+1], fields[i+2]
			i += 3
		} else {
			f.Path = fields[i+1]
			i += 2
		}
		files = append(files, f)
	}

	// numstat -z: "added\tdeleted\tpath\0", or for renames
	// "added\tdeleted\t\0old\0new\0". Binary files count as "-".
	byPath := make(map[string]*repoDiffFile, len(files))
	for i := range files {
		byPath[files[i].Path] = &files[i]
	}
	fields = strings.Split(strings.TrimSuffix(numstatOut, "\x00"), "\x00")
	for i := 0; i < len(fields); i++ {
		parts := strings.SplitN(fields[i], "\t", 3)
		if len(parts) != 3 {
			continue
		}
		p := parts[2]
		if p == "" && i+2 < len(fields) {
			p = fields[i+2]
			i += 2
		}
		f, ok := byPath[p]
		if !ok {
			continue
		}
		if parts[0] == "-" {
			f.Binary = true
			continue
		}
		f.Additions, _ = strconv.Atoi(parts[0])
		f.Deletions, _ = strconv.Atoi(parts[1])
	}
	return files, nil
}

// handleRepoFile returns a file's contents at a commit or branch.
func handleRepoFile(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), gitTimeout)
	defer cancel()

	q := r.URL.Query()
	sha, err := resolveRev(ctx, q.Get("rev"), "rev")
	if err != nil {
		writeRevError(w, err)
		return
	}
	p := q.Get("path")
	if err := validateRepoPath(p); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	object := sha + ":" + p
	size, err := gitOutput(ctx, "cat-file", "-s", object)
	if err != nil {
		writeErr(w, http.StatusNotFound, "file not found at "+sha[:12]+": "+p)
		return
	}
	if n, _ := strconv.Atoi(strings.TrimSpace(size)); n > maxRepoFileBytes {
		writeErr(w, http.StatusRequestEntityTooLarge, "file is too large to show")
		return
	}
	content, err := gitStdout(ctx, "cat-file", "blob", object)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(content))
}

// validateRepoPath accepts a clean path relative to the repo root.
func validateRepoPath(p string) error {
	switch {
	case p == "":
		return fmt.Errorf("path is required")
	case strings.HasPrefix(p, "/") || strings.HasPrefix(p, "-"):
		return fmt.Errorf("path must be relative to the repo root")
	case path.Clean(p) != p || p == "." || strings.HasPrefix(p, "../") || p == "..":
		return fmt.Errorf("path must be clean and inside the repo")
	}
	return nil
}

type repoMergeRequest struct {
	Source            string `json:"source"`
	Target            string `json:"target"`
	Strategy          string `json:"strategy"`
	Message           string `json:"message"`
	ExpectedSourceSHA string `json:"expectedSourceSha"`
	Name              string `json:"name"`
	Email             string `json:"email"`
}

type repoMergeResult struct {
	Target    string `json:"target"`
	SHA       string `json:"sha"`
	Strategy  string `json:"strategy"`
	MergeBase string `json:"mergeBase"`
}

// handleRepoMerge merges source into target in the canonical repo. The
// merge only happens if source is still at the commit devpad expects, and
// target moves with a compare-and-swap, so neither can change underneath it.
func handleRepoMerge(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), repoTimeout)
	defer cancel()

	var req repoMergeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Strategy == "" {
		req.Strategy = "merge"
	}
	if req.Strategy != "merge" && req.Strategy != "squash" && req.Strategy != "fast-forward" {
		writeErr(w, http.StatusBadRequest, "strategy must be merge, squash or fast-forward")
		return
	}
	if err := validateCommitIdentity(req.Name, "name"); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := validateCommitIdentity(req.Email, "email"); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.Source == req.Target {
		writeErr(w, http.StatusBadRequest, "source and target must differ")
		return
	}
	sourceSHA, err := resolveBranch(ctx, req.Source, "source")
	if err != nil {
		writeRevError(w, err)
		return
	}
	targetSHA, err := resolveBranch(ctx, req.Target, "target")
	if err != nil {
		writeRevError(w, err)
		return
	}
	if req.ExpectedSourceSHA != "" && req.ExpectedSourceSHA != sourceSHA {
		writeErr(w, http.StatusConflict, fmt.Sprintf("%s moved to %s since it was reviewed", req.Source, sourceSHA[:12]))
		return
	}
	if gitExec(ctx, "merge-base", "--is-ancestor", sourceSHA, targetSHA) == nil {
		writeErr(w, http.StatusConflict, fmt.Sprintf("%s has nothing to merge into %s", req.Source, req.Target))
		return
	}
	mergeBase, _ := gitOutput(ctx, "merge-base", targetSHA, sourceSHA)
	canFastForward := gitExec(ctx, "merge-base", "--is-ancestor", targetSHA, sourceSHA) == nil

	var newSHA string
	if req.Strategy == "fast-forward" {
		if !canFastForward {
			writeErr(w, http.StatusConflict, fmt.Sprintf("%s cannot be fast-forwarded to %s", req.Target, req.Source))
			return
		}
		newSHA = sourceSHA
	} else {
		tree, conflicts, err := mergeTree(ctx, targetSHA, sourceSHA)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		if len(conflicts) > 0 {
			writeJSON(w, http.StatusConflict, map[string]any{
				"error":     fmt.Sprintf("%s conflicts with %s", req.Source, req.Target),
				"conflicts": conflicts,
			})
			return
		}
		message := strings.TrimSpace(req.Message)
		if message == "" {
			message = fmt.Sprintf("Merge branch '%s' into %s", req.Source, req.Target)
		}
		args := []string{"commit-tree", tree, "-p", targetSHA}
		if req.Strategy == "merge" {
			args = append(args, "-p", sourceSHA)
		}
		args = append(args, "-m", message)
		out, err := gitOutputEnv(ctx, commitIdentityEnv(req.Name, req.Email), args...)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "creating merge commit: "+strings.TrimSpace(out))
			return
		}
		newSHA = strings.TrimSpace(out)
	}

	if out, err := gitOutput(ctx, "update-ref", "refs/heads/"+req.Target, newSHA, targetSHA); err != nil {
		writeErr(w, http.StatusConflict, fmt.Sprintf("%s moved during the merge: %s", req.Target, strings.TrimSpace(out)))
		return
	}
	writeJSON(w, http.StatusOK, repoMergeResult{Target: req.Target, SHA: newSHA, Strategy: req.Strategy, MergeBase: strings.TrimSpace(mergeBase)})
}

// mergeTree computes the merged tree of two commits without a working tree.
// It returns the conflicted paths instead of a tree when the merge is not clean.
func mergeTree(ctx context.Context, ours, theirs string) (string, []string, error) {
	cmd := gitCommand(ctx, "merge-tree", "--write-tree", "--name-only", "--no-messages", "-z", ours, theirs)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	fields := strings.Split(strings.TrimSuffix(stdout.String(), "\x00"), "\x00")
	var exitErr *exec.ExitError
	switch {
	case err == nil:
		return fields[0], nil, nil
	case errors.As(err, &exitErr) && exitErr.ExitCode() == 1:
		seen := map[string]bool{}
		var conflicts []string
		for _, f := range fields[1:] {
			if f != "" && !seen[f] {
				seen[f] = true
				conflicts = append(conflicts, f)
			}
		}
		return "", conflicts, nil
	default:
		return "", nil, fmt.Errorf("computing merge: %s", strings.TrimSpace(stderr.String()))
	}
}

// validateCommitIdentity rejects values that could break a commit header.
func validateCommitIdentity(value, field string) error {
	if len(value) > 256 {
		return fmt.Errorf("%s is too long", field)
	}
	if strings.ContainsAny(value, "<>") || strings.ContainsFunc(value, unicode.IsControl) {
		return fmt.Errorf("%s contains invalid characters", field)
	}
	return nil
}

func commitIdentityEnv(name, email string) []string {
	if name == "" {
		name = "devpad"
	}
	if email == "" {
		email = "devpad@localhost"
	}
	return []string{
		"GIT_AUTHOR_NAME=" + name, "GIT_AUTHOR_EMAIL=" + email,
		"GIT_COMMITTER_NAME=" + name, "GIT_COMMITTER_EMAIL=" + email,
	}
}

// handleRepoPush pushes one branch of the canonical repo to its upstream.
// It never forces, so a branch that diverged upstream is reported instead,
// with 409 when the upstream has commits the branch lacks.
func handleRepoPush(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), repoTimeout)
	defer cancel()

	branch := r.URL.Query().Get("branch")
	sha, err := resolveBranch(ctx, branch, "branch")
	if err != nil {
		writeRevError(w, err)
		return
	}
	// Waiting for a fetch keeps origin, and the record of the upstream's
	// branches this push updates, from changing under it.
	select {
	case upstreamBusy <- struct{}{}:
		defer func() { <-upstreamBusy }()
	case <-ctx.Done():
		writeErr(w, http.StatusServiceUnavailable, "a fetch from the upstream is still running")
		return
	}
	if !checkUpstream(ctx, w, r) {
		return
	}
	ref := "refs/heads/" + branch
	if out, err := gitOutput(ctx, "push", "--porcelain", "origin", ref+":"+ref); err != nil {
		status := http.StatusBadGateway
		if pushRejected(out, ref) {
			status = http.StatusConflict
		}
		writeErr(w, status, "pushing to upstream: "+strings.TrimSpace(out))
		return
	}
	// The upstream now has the branch at sha; record it so the branch shows
	// as in step without another fetch. The push stands either way.
	if out, err := gitOutput(ctx, "update-ref", upstreamRefPrefix+branch, sha); err != nil {
		log.Printf("recording the upstream's %s after pushing: %s", branch, strings.TrimSpace(out))
	}
	writeJSON(w, http.StatusOK, repoBranchResult{Branch: branch, SHA: sha})
}

// pushRejected reports whether git push --porcelain output says git refused
// to update ref because the upstream has commits it lacks, as opposed to the
// upstream refusing it or the push failing. Porcelain status lines are
// "<flag>\t<from>:<to>\t<summary>", and git's own refusals are flagged "!"
// with a "[rejected]" summary, in every locale.
func pushRejected(out, ref string) bool {
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Split(line, "\t")
		if len(fields) >= 3 && fields[0] == "!" && strings.HasSuffix(fields[1], ":"+ref) && strings.HasPrefix(fields[2], "[rejected]") {
			return true
		}
	}
	return false
}

// handleRepoSetHead makes an existing branch the canonical repo's default
// branch. Devpad moves it when the user picks a new default branch; imports
// into the default branch are refused from then on.
func handleRepoSetHead(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), gitTimeout)
	defer cancel()

	branch := r.URL.Query().Get("branch")
	sha, err := resolveBranch(ctx, branch, "branch")
	if err != nil {
		writeRevError(w, err)
		return
	}
	if out, err := gitOutput(ctx, "symbolic-ref", "HEAD", "refs/heads/"+branch); err != nil {
		writeErr(w, http.StatusInternalServerError, "setting HEAD: "+strings.TrimSpace(out))
		return
	}
	writeJSON(w, http.StatusOK, repoBranchResult{Branch: branch, SHA: sha})
}

// handleRepoDeleteBranch deletes a branch other than the default one, and
// returns the commit it pointed at. With sha given, the branch is deleted
// only while it still points there, so commits published to it after the
// user chose to delete it are kept.
func handleRepoDeleteBranch(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), gitTimeout)
	defer cancel()

	branch := r.URL.Query().Get("branch")
	expected := r.URL.Query().Get("sha")
	if expected != "" && !isFullHash(expected) {
		writeErr(w, http.StatusBadRequest, "sha must be a full commit hash")
		return
	}
	sha, err := resolveBranch(ctx, branch, "branch")
	if err != nil {
		writeRevError(w, err)
		return
	}
	if head, err := gitOutput(ctx, "symbolic-ref", "--short", "HEAD"); err == nil && strings.TrimSpace(head) == branch {
		writeErr(w, http.StatusForbidden, "cannot delete the default branch")
		return
	}
	if expected != "" && expected != sha {
		writeErr(w, http.StatusConflict, fmt.Sprintf("%s has moved since you looked at it; check its new commits before deleting it", branch))
		return
	}
	// The old value makes the deletion fail if the branch moves meanwhile.
	if out, err := gitOutput(ctx, "update-ref", "-d", "refs/heads/"+branch, sha); err != nil {
		writeErr(w, http.StatusConflict, "deleting "+branch+": "+strings.TrimSpace(out))
		return
	}
	writeJSON(w, http.StatusOK, repoBranchResult{Branch: branch, SHA: sha})
}

// handleRepoFetchBase updates a sandbox's copy of a canonical branch, as
// devpad/<branch>, from a bundle devpad streams in. The sandbox's own
// branches are untouched.
func handleRepoFetchBase(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), repoTimeout)
	defer cancel()

	branch := r.URL.Query().Get("branch")
	if err := validateBranchName(ctx, branch, "branch"); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if gitExec(ctx, "rev-parse", "--git-dir") != nil {
		writeErr(w, http.StatusConflict, "workspace has no repository")
		return
	}
	bundle, cleanup, err := receiveBundle(w, r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	defer cleanup()

	ref := "refs/remotes/" + sandboxBaseRemote + "/" + branch
	if out, err := gitOutput(ctx, "fetch", "--no-tags", "--no-write-fetch-head", "--force", "--", bundle, "refs/heads/"+branch+":"+ref); err != nil {
		writeErr(w, http.StatusBadRequest, "fetching bundle: "+strings.TrimSpace(out))
		return
	}
	sha, err := gitOutput(ctx, "rev-parse", ref)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "resolving "+ref)
		return
	}
	writeJSON(w, http.StatusOK, repoBranchResult{Branch: sandboxBaseRemote + "/" + branch, SHA: strings.TrimSpace(sha)})
}

// handleRepoFetchBases refreshes devpad/<branch> for each named branch from
// one bundle of them, for git fetch through the devpad remote. With
// source=upstream the bundle holds the upstream's branches and they land as
// origin/<branch>, for git fetch through the origin remote.
func handleRepoFetchBases(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), repoTimeout)
	defer cancel()

	srcPrefix, remote := "refs/heads/", sandboxBaseRemote
	switch source := r.URL.Query().Get("source"); source {
	case "":
	case "upstream":
		srcPrefix, remote = upstreamRefPrefix, upstreamRemote
	default:
		writeErr(w, http.StatusBadRequest, "unknown source "+source)
		return
	}
	branches := r.URL.Query()["branch"]
	if len(branches) == 0 || len(branches) > maxBundleBranches {
		writeErr(w, http.StatusBadRequest, fmt.Sprintf("fetch between 1 and %d branches", maxBundleBranches))
		return
	}
	var unique, refspecs []string
	seen := make(map[string]bool, len(branches))
	for _, branch := range branches {
		if err := validateBranchName(ctx, branch, "branch"); err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		if !seen[branch] {
			seen[branch] = true
			unique = append(unique, branch)
			refspecs = append(refspecs, srcPrefix+branch+":refs/remotes/"+remote+"/"+branch)
		}
	}
	if gitExec(ctx, "rev-parse", "--git-dir") != nil {
		writeErr(w, http.StatusConflict, "workspace has no repository")
		return
	}
	bundle, cleanup, err := receiveBundle(w, r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	defer cleanup()

	args := append([]string{"fetch", "--no-tags", "--no-write-fetch-head", "--force", "--", bundle}, refspecs...)
	if out, err := gitOutput(ctx, args...); err != nil {
		writeErr(w, http.StatusBadRequest, "fetching bundle: "+strings.TrimSpace(out))
		return
	}
	results := make([]repoBranchResult, 0, len(unique))
	for _, branch := range unique {
		ref := "refs/remotes/" + remote + "/" + branch
		sha, err := gitOutput(ctx, "rev-parse", ref)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "resolving "+ref)
			return
		}
		results = append(results, repoBranchResult{Branch: remote + "/" + branch, SHA: strings.TrimSpace(sha)})
	}
	writeJSON(w, http.StatusOK, map[string]any{"branches": results})
}

// gitStdout runs git and returns stdout only, so stderr warnings cannot
// corrupt parsed or binary output.
func gitStdout(ctx context.Context, args ...string) (string, error) {
	cmd := gitCommand(ctx, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %s", args[0], strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}
