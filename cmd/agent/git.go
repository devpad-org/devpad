package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os/exec"
	"strings"
	"time"
	"unicode"
)

const gitTimeout = 30 * time.Second
const gitNetworkTimeout = 5 * time.Minute
const maxGitRequestBody = 1 << 20 // 1 MB

// gitActionTimeout returns the context timeout appropriate for a given action.
// Network-heavy actions (clone, push, pull) get a longer timeout.
var longRunningActions = map[string]bool{
	"clone": true,
	"push":  true,
	"pull":  true,
	"fetch": true,
}

func gitActionTimeout(action string) time.Duration {
	if longRunningActions[action] {
		return gitNetworkTimeout
	}
	return gitTimeout
}

// validateGitRef rejects branch/remote names that could be interpreted as flags.
func validateGitRef(name, field string) error {
	if strings.HasPrefix(name, "-") {
		return fmt.Errorf("%s must not start with '-'", field)
	}
	return nil
}

func validateGitCommitHash(hash string) error {
	if hash == "" {
		return fmt.Errorf("commit is required")
	}
	if len(hash) < 7 || len(hash) > 64 {
		return fmt.Errorf("commit must be a 7-64 character hex hash")
	}
	for _, r := range hash {
		if !unicode.Is(unicode.ASCII_Hex_Digit, r) {
			return fmt.Errorf("commit must be a hex hash")
		}
	}
	return nil
}

// validateFilePaths rejects file paths that could escape the workspace or inject flags.
func validateFilePaths(files []string) error {
	for _, f := range files {
		if strings.HasPrefix(f, "-") {
			return fmt.Errorf("file path must not start with '-': %s", f)
		}
		if strings.Contains(f, "..") {
			return fmt.Errorf("file path must not contain '..': %s", f)
		}
		if strings.HasPrefix(f, "/") {
			return fmt.Errorf("file path must not be absolute: %s", f)
		}
	}
	return nil
}

// validateUserIdentity rejects user.name/user.email values that could inject flags.
func validateUserIdentity(value, field string) error {
	if strings.HasPrefix(value, "-") {
		return fmt.Errorf("%s must not start with '-'", field)
	}
	return nil
}

func parseGitRemoteNames(out string) []string {
	remotes := []string{}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line != "" {
			remotes = append(remotes, line)
		}
	}
	return remotes
}

func isRemoteBranch(name string, remotes []string) bool {
	for _, remote := range remotes {
		if strings.HasPrefix(name, remote+"/") {
			return true
		}
	}
	return false
}

// gitStatusEntry represents a single file in git status output.
type gitStatusEntry struct {
	Path       string `json:"path"`
	StatusCode string `json:"statusCode"`
	Status     string `json:"status"`
	Staged     bool   `json:"staged"`
}

func handleGitStatus(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), gitTimeout)
	defer cancel()

	// Check if we're in a git repo
	if err := gitExec(ctx, "rev-parse", "--git-dir"); err != nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"isRepo":  false,
			"branch":  "",
			"files":   []gitStatusEntry{},
			"ahead":   0,
			"behind":  0,
			"remotes": []string{},
		})
		return
	}

	// Current branch
	branch := ""
	if out, err := gitOutput(ctx, "rev-parse", "--abbrev-ref", "HEAD"); err == nil {
		branch = strings.TrimSpace(out)
	}

	// Ahead/behind
	ahead, behind := 0, 0
	if out, err := gitOutput(ctx, "rev-list", "--left-right", "--count", "HEAD...@{upstream}"); err == nil {
		fmt.Sscanf(strings.TrimSpace(out), "%d\t%d", &ahead, &behind)
	}

	// Status
	files := parseGitStatus(ctx)

	// Remotes
	remotes := []string{}
	if out, err := gitOutput(ctx, "remote"); err == nil {
		remotes = parseGitRemoteNames(out)
	}

	// User config
	userName := ""
	if out, err := gitOutput(ctx, "config", "user.name"); err == nil {
		userName = strings.TrimSpace(out)
	}
	userEmail := ""
	if out, err := gitOutput(ctx, "config", "user.email"); err == nil {
		userEmail = strings.TrimSpace(out)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"isRepo":    true,
		"branch":    branch,
		"files":     files,
		"ahead":     ahead,
		"behind":    behind,
		"remotes":   remotes,
		"userName":  userName,
		"userEmail": userEmail,
	})
}

func handleGitLog(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), gitTimeout)
	defer cancel()

	count := r.URL.Query().Get("count")
	if count == "" {
		count = "50"
	}

	out, err := gitOutput(ctx, gitLogArgs(count, r.URL.Query().Get("all") == "true")...)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"commits": []any{}})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"commits": parseGitLogOutput(out)})
}

type gitCommitFileEntry struct {
	Path    string `json:"path"`
	OldPath string `json:"oldPath,omitempty"`
	Status  string `json:"status"`
}

type gitFileDiffEntry struct {
	Path        string `json:"path"`
	OldPath     string `json:"oldPath,omitempty"`
	Status      string `json:"status"`
	OldContent  string `json:"oldContent"`
	NewContent  string `json:"newContent"`
	OldFileName string `json:"oldFileName"`
	NewFileName string `json:"newFileName"`
}

func handleGitCommitFiles(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), gitTimeout)
	defer cancel()

	commit := r.URL.Query().Get("commit")
	if err := validateGitCommitHash(commit); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}

	files, err := gitCommitFiles(ctx, commit)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to load commit files")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"files": files})
}

func handleGitCommitDiff(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), gitTimeout)
	defer cancel()

	commit := r.URL.Query().Get("commit")
	path := r.URL.Query().Get("path")
	oldPath := r.URL.Query().Get("oldPath")

	if err := validateGitCommitHash(commit); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if path == "" {
		writeErr(w, http.StatusBadRequest, "path is required")
		return
	}
	paths := []string{path}
	if oldPath != "" {
		paths = append(paths, oldPath)
	}
	if err := validateFilePaths(paths); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}

	diff, err := gitCommitFileDiff(ctx, commit, path, oldPath)
	if err != nil {
		if errors.Is(err, errGitCommitFileNotFound) {
			writeErr(w, http.StatusNotFound, "file not found in commit")
			return
		}
		writeErr(w, http.StatusInternalServerError, "failed to load commit diff")
		return
	}

	writeJSON(w, http.StatusOK, diff)
}

func gitCommitFiles(ctx context.Context, commit string) ([]gitCommitFileEntry, error) {
	if err := gitExec(ctx, "rev-parse", "--verify", commit+"^{commit}"); err != nil {
		return nil, fmt.Errorf("verifying commit: %w", err)
	}

	parent, hasParent, err := firstCommitParent(ctx, commit)
	if err != nil {
		return nil, err
	}

	args := []string{"diff-tree", "--no-commit-id", "--name-status", "-r", "-M", "--root", commit}
	if hasParent {
		args = []string{"diff", "--name-status", "-M", parent, commit, "--"}
	}

	out, err := gitOutput(ctx, args...)
	if err != nil {
		return nil, fmt.Errorf("listing commit files: %w", err)
	}
	return parseGitCommitFilesOutput(out), nil
}

func parseGitCommitFilesOutput(out string) []gitCommitFileEntry {
	files := []gitCommitFileEntry{}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line == "" {
			continue
		}
		parts := strings.Split(line, "\t")
		if len(parts) < 2 {
			continue
		}

		entry := gitCommitFileEntry{Status: commitFileStatus(parts[0])}
		if strings.HasPrefix(parts[0], "R") || strings.HasPrefix(parts[0], "C") {
			if len(parts) < 3 {
				continue
			}
			entry.OldPath = parts[1]
			entry.Path = parts[2]
		} else {
			entry.Path = parts[1]
		}
		files = append(files, entry)
	}
	return files
}

func commitFileStatus(code string) string {
	switch {
	case strings.HasPrefix(code, "A"):
		return "added"
	case strings.HasPrefix(code, "D"):
		return "deleted"
	case strings.HasPrefix(code, "R"):
		return "renamed"
	case strings.HasPrefix(code, "C"):
		return "copied"
	case strings.HasPrefix(code, "M"):
		return "modified"
	default:
		return "modified"
	}
}

var errGitCommitFileNotFound = errors.New("file not found in commit")

func gitCommitFileDiff(ctx context.Context, commit, path, oldPath string) (*gitFileDiffEntry, error) {
	files, err := gitCommitFiles(ctx, commit)
	if err != nil {
		return nil, err
	}

	var selected *gitCommitFileEntry
	for i := range files {
		if files[i].Path != path {
			continue
		}
		if oldPath != "" && files[i].OldPath != oldPath {
			continue
		}
		selected = &files[i]
		break
	}
	if selected == nil {
		return nil, errGitCommitFileNotFound
	}

	parent, hasParent, err := firstCommitParent(ctx, commit)
	if err != nil {
		return nil, err
	}

	if selected.OldPath != "" {
		oldPath = selected.OldPath
	}
	if oldPath == "" {
		oldPath = path
	}

	diff := &gitFileDiffEntry{
		Path:        selected.Path,
		OldPath:     selected.OldPath,
		Status:      selected.Status,
		OldFileName: oldPath,
		NewFileName: selected.Path,
	}

	if selected.Status != "added" && hasParent {
		oldContent, err := gitShowFile(ctx, parent, oldPath)
		if err != nil {
			return nil, fmt.Errorf("reading old file content: %w", err)
		}
		diff.OldContent = oldContent
	}
	if selected.Status != "deleted" {
		newContent, err := gitShowFile(ctx, commit, selected.Path)
		if err != nil {
			return nil, fmt.Errorf("reading new file content: %w", err)
		}
		diff.NewContent = newContent
	}

	return diff, nil
}

func firstCommitParent(ctx context.Context, commit string) (string, bool, error) {
	out, err := gitOutput(ctx, "rev-list", "--parents", "-n", "1", commit)
	if err != nil {
		return "", false, fmt.Errorf("reading commit parents: %w", err)
	}
	parts := strings.Fields(out)
	if len(parts) < 2 {
		return "", false, nil
	}
	return parts[1], true, nil
}

func gitShowFile(ctx context.Context, commit, path string) (string, error) {
	out, err := gitOutput(ctx, "show", commit+":"+path)
	if err != nil {
		return "", err
	}
	return out, nil
}

func gitLogArgs(count string, allBranches bool) []string {
	args := []string{"log", "--topo-order", "--format=%H%n%h%n%P%n%an%n%ae%n%at%n%s", "-n", count}
	if allBranches {
		args = append(args, "--all")
	}
	return args
}

type gitCommitEntry struct {
	Hash      string   `json:"hash"`
	ShortHash string   `json:"shortHash"`
	Parents   []string `json:"parents"`
	Author    string   `json:"author"`
	Email     string   `json:"email"`
	Timestamp string   `json:"timestamp"`
	Message   string   `json:"message"`
}

func parseGitLogOutput(out string) []gitCommitEntry {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	commits := []gitCommitEntry{}
	for i := 0; i+6 < len(lines); i += 7 {
		parents := strings.Fields(lines[i+2])
		if parents == nil {
			parents = []string{}
		}
		commits = append(commits, gitCommitEntry{
			Hash:      lines[i],
			ShortHash: lines[i+1],
			Parents:   parents,
			Author:    lines[i+3],
			Email:     lines[i+4],
			Timestamp: lines[i+5],
			Message:   lines[i+6],
		})
	}
	return commits
}

func handleGitBranches(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), gitTimeout)
	defer cancel()

	current := ""
	if out, err := gitOutput(ctx, "rev-parse", "--abbrev-ref", "HEAD"); err == nil {
		current = strings.TrimSpace(out)
	}

	remotes := []string{}
	if out, err := gitOutput(ctx, "remote"); err == nil {
		remotes = parseGitRemoteNames(out)
	}

	branches := []map[string]any{}
	if out, err := gitOutput(ctx, "branch", "-a", "--format=%(refname:short)%09%(objectname:short)%09%(upstream:short)"); err == nil {
		for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
			if line == "" {
				continue
			}
			parts := strings.SplitN(line, "\t", 3)
			name := parts[0]
			shortHash := ""
			upstream := ""
			if len(parts) > 1 {
				shortHash = parts[1]
			}
			if len(parts) > 2 {
				upstream = parts[2]
			}
			branches = append(branches, map[string]any{
				"name":     name,
				"hash":     shortHash,
				"upstream": upstream,
				"current":  name == current,
				"remote":   isRemoteBranch(name, remotes),
			})
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"branches": branches, "current": current})
}

// gitRemoteEntry represents a single git remote with its fetch/push URLs.
type gitRemoteEntry struct {
	Name     string `json:"name"`
	FetchURL string `json:"fetchUrl"`
	PushURL  string `json:"pushUrl"`
}

func handleGitRemotes(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), gitTimeout)
	defer cancel()

	out, err := gitOutput(ctx, "remote", "-v")
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"remotes": []gitRemoteEntry{}})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"remotes": parseGitRemoteOutput(out)})
}

// parseGitRemoteOutput parses the output of `git remote -v` into structured entries.
func parseGitRemoteOutput(out string) []gitRemoteEntry {
	// Parse "origin\thttps://... (fetch)" and "origin\thttps://... (push)" lines.
	seen := map[string]*gitRemoteEntry{}
	var order []string
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line == "" {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) < 3 {
			continue
		}
		name := parts[0]
		url := parts[1]
		kind := parts[2] // "(fetch)" or "(push)"

		entry, ok := seen[name]
		if !ok {
			entry = &gitRemoteEntry{Name: name}
			seen[name] = entry
			order = append(order, name)
		}
		if kind == "(fetch)" {
			entry.FetchURL = url
		} else if kind == "(push)" {
			entry.PushURL = url
		}
	}

	remotes := make([]gitRemoteEntry, 0, len(order))
	for _, name := range order {
		remotes = append(remotes, *seen[name])
	}
	return remotes
}

func handleGitDiff(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), gitTimeout)
	defer cancel()

	path := r.URL.Query().Get("path")
	staged := r.URL.Query().Get("staged") == "true"

	if path != "" {
		if err := validateFilePaths([]string{path}); err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
	}

	args := []string{"diff", "--no-color"}
	if staged {
		args = append(args, "--cached")
	}
	if path != "" {
		args = append(args, "--", path)
	}

	out, err := gitOutput(ctx, args...)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"diff": ""})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"diff": out})
}

// gitActionRequest holds the decoded and validated request body for a git action.
type gitActionRequest struct {
	Action    string   `json:"action"`
	Files     []string `json:"files"`
	Msg       string   `json:"message"`
	Branch    string   `json:"branch"`
	Remote    string   `json:"remote"`
	URL       string   `json:"url"`
	NewName   string   `json:"newName"`
	UserName  string   `json:"userName"`
	UserEmail string   `json:"userEmail"`
}

// gitActionError is a validation error that should be returned as a 400 response.
type gitActionError struct {
	msg string
}

func (e *gitActionError) Error() string { return e.msg }

// gitActionFunc executes a single git action and returns its output.
// Returning a *gitActionError signals a 400 (bad request) to the handler.
type gitActionFunc func(ctx context.Context, req gitActionRequest) (string, error)

// gitActions maps action names to their handler functions.
var gitActions = map[string]gitActionFunc{
	"stage":          actionStage,
	"unstage":        actionUnstage,
	"set-config":     actionSetConfig,
	"commit":         actionCommit,
	"push":           actionPush,
	"pull":           actionPull,
	"fetch":          actionFetch,
	"checkout":       actionCheckout,
	"checkout-new":   actionCheckoutNew,
	"discard":        actionDiscard,
	"init":           actionInit,
	"clone":          actionClone,
	"remote-add":     actionRemoteAdd,
	"remote-remove":  actionRemoteRemove,
	"remote-rename":  actionRemoteRename,
	"remote-set-url": actionRemoteSetURL,
}

func handleGitAction(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxGitRequestBody)

	var req gitActionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if err := validateGitActionInputs(req); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}

	fn, ok := gitActions[req.Action]
	if !ok {
		writeErr(w, http.StatusBadRequest, "unknown action: "+req.Action)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), gitActionTimeout(req.Action))
	defer cancel()

	out, err := fn(ctx, req)
	if err != nil {
		var actionErr *gitActionError
		if errors.As(err, &actionErr) {
			writeErr(w, http.StatusBadRequest, actionErr.msg)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"output":  out,
			"error":   err.Error(),
		})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"output":  strings.TrimSpace(out),
	})
}

// validateGitActionInputs performs shared input sanitization to prevent flag injection
// and path traversal. Field-specific validation lives in each action function.
func validateGitActionInputs(req gitActionRequest) error {
	if req.Branch != "" {
		if err := validateGitRef(req.Branch, "branch"); err != nil {
			return err
		}
	}
	if req.Remote != "" {
		if err := validateGitRef(req.Remote, "remote"); err != nil {
			return err
		}
	}
	if len(req.Files) > 0 {
		if err := validateFilePaths(req.Files); err != nil {
			return err
		}
	}
	if req.URL != "" {
		if err := validateGitRef(req.URL, "url"); err != nil {
			return err
		}
	}
	if req.NewName != "" {
		if err := validateGitRef(req.NewName, "newName"); err != nil {
			return err
		}
	}
	if req.UserName != "" {
		if err := validateUserIdentity(req.UserName, "userName"); err != nil {
			return err
		}
	}
	if req.UserEmail != "" {
		if err := validateUserIdentity(req.UserEmail, "userEmail"); err != nil {
			return err
		}
	}
	return nil
}

func actionStage(ctx context.Context, req gitActionRequest) (string, error) {
	if len(req.Files) == 0 {
		return gitOutput(ctx, "add", "-A")
	}
	args := append([]string{"add", "--"}, req.Files...)
	return gitOutput(ctx, args...)
}

func actionUnstage(ctx context.Context, req gitActionRequest) (string, error) {
	// On an empty repo with no commits, HEAD doesn't exist so "git reset HEAD" fails.
	// Use "git rm --cached" in that case.
	hasHead := gitExec(ctx, "rev-parse", "HEAD") == nil
	if hasHead {
		if len(req.Files) == 0 {
			return gitOutput(ctx, "reset", "HEAD")
		}
		args := append([]string{"reset", "HEAD", "--"}, req.Files...)
		return gitOutput(ctx, args...)
	}
	if len(req.Files) == 0 {
		return gitOutput(ctx, "rm", "--cached", "-r", ".")
	}
	args := append([]string{"rm", "--cached", "--"}, req.Files...)
	return gitOutput(ctx, args...)
}

func actionSetConfig(ctx context.Context, req gitActionRequest) (string, error) {
	if req.UserName != "" {
		if _, err := gitOutput(ctx, "config", "user.name", req.UserName); err != nil {
			return "", fmt.Errorf("setting user.name: %w", err)
		}
	}
	if req.UserEmail != "" {
		if _, err := gitOutput(ctx, "config", "user.email", req.UserEmail); err != nil {
			return "", fmt.Errorf("setting user.email: %w", err)
		}
	}
	return "Git config updated", nil
}

func actionCommit(ctx context.Context, req gitActionRequest) (string, error) {
	if req.Msg == "" {
		return "", &gitActionError{"commit message is required"}
	}
	return gitOutput(ctx, "commit", "-m", req.Msg)
}

func actionPush(ctx context.Context, req gitActionRequest) (string, error) {
	remote := req.Remote
	if remote == "" {
		remote = "origin"
	}
	args := []string{"push", remote}
	if req.Branch != "" {
		args = append(args, req.Branch)
	}
	return gitOutput(ctx, args...)
}

func actionPull(ctx context.Context, req gitActionRequest) (string, error) {
	remote := req.Remote
	if remote == "" {
		remote = "origin"
	}
	args := []string{"pull", remote}
	if req.Branch != "" {
		args = append(args, req.Branch)
	}
	return gitOutput(ctx, args...)
}

func actionFetch(ctx context.Context, req gitActionRequest) (string, error) {
	remote := req.Remote
	if remote == "" {
		remote = "origin"
	}
	args := []string{"fetch", remote}
	if req.Branch != "" {
		args = append(args, req.Branch)
	}
	return gitOutput(ctx, args...)
}

func actionCheckout(ctx context.Context, req gitActionRequest) (string, error) {
	if req.Branch == "" {
		return "", &gitActionError{"branch is required"}
	}
	return gitOutput(ctx, "checkout", req.Branch)
}

func actionCheckoutNew(ctx context.Context, req gitActionRequest) (string, error) {
	if req.Branch == "" {
		return "", &gitActionError{"branch is required"}
	}
	return gitOutput(ctx, "checkout", "-b", req.Branch)
}

func actionDiscard(ctx context.Context, req gitActionRequest) (string, error) {
	if len(req.Files) == 0 {
		return "", &gitActionError{"files are required for discard"}
	}
	args := append([]string{"checkout", "--"}, req.Files...)
	return gitOutput(ctx, args...)
}

func actionInit(ctx context.Context, _ gitActionRequest) (string, error) {
	return gitOutput(ctx, "init")
}

func actionClone(ctx context.Context, req gitActionRequest) (string, error) {
	if req.URL == "" {
		return "", &gitActionError{"url is required for clone"}
	}
	// Try a direct clone first. This works when /workspace is empty.
	out, err := gitOutput(ctx, "clone", req.URL, ".")
	if err == nil {
		return out, nil
	}
	// If the directory is not empty, fall back to init + fetch + checkout.
	if !strings.Contains(out, "not an empty directory") &&
		!strings.Contains(out, "not empty") {
		return out, err
	}
	if initOut, initErr := gitOutput(ctx, "init"); initErr != nil {
		return initOut, initErr
	}
	if addOut, addErr := gitOutput(ctx, "remote", "add", "origin", req.URL); addErr != nil {
		return addOut, addErr
	}
	if fetchOut, fetchErr := gitOutput(ctx, "fetch", "origin"); fetchErr != nil {
		return fetchOut, fetchErr
	}
	// Determine the default remote branch.
	refOut, refErr := gitOutput(ctx, "symbolic-ref", "refs/remotes/origin/HEAD", "--short")
	defaultBranch := strings.TrimSpace(refOut)
	if refErr != nil || defaultBranch == "" {
		// Fallback: try common default branch names.
		for _, candidate := range []string{"origin/main", "origin/master"} {
			if gitExec(ctx, "rev-parse", "--verify", candidate) == nil {
				defaultBranch = candidate
				break
			}
		}
	}
	if defaultBranch == "" {
		return "Cloned but could not determine default branch. Use checkout to select one.", nil
	}
	// Strip "origin/" prefix for the local branch name.
	localBranch := strings.TrimPrefix(defaultBranch, "origin/")
	return gitOutput(ctx, "checkout", "-B", localBranch, defaultBranch)
}

func actionRemoteAdd(ctx context.Context, req gitActionRequest) (string, error) {
	if req.Remote == "" || req.URL == "" {
		return "", &gitActionError{"remote name and url are required"}
	}
	return gitOutput(ctx, "remote", "add", req.Remote, req.URL)
}

func actionRemoteRemove(ctx context.Context, req gitActionRequest) (string, error) {
	if req.Remote == "" {
		return "", &gitActionError{"remote name is required"}
	}
	return gitOutput(ctx, "remote", "remove", req.Remote)
}

func actionRemoteRename(ctx context.Context, req gitActionRequest) (string, error) {
	if req.Remote == "" || req.NewName == "" {
		return "", &gitActionError{"remote name and new name are required"}
	}
	return gitOutput(ctx, "remote", "rename", req.Remote, req.NewName)
}

func actionRemoteSetURL(ctx context.Context, req gitActionRequest) (string, error) {
	if req.Remote == "" || req.URL == "" {
		return "", &gitActionError{"remote name and url are required"}
	}
	return gitOutput(ctx, "remote", "set-url", req.Remote, req.URL)
}

// parseGitStatus runs git status --porcelain=v1 and returns structured entries.
func parseGitStatus(ctx context.Context) []gitStatusEntry {
	out, err := gitOutput(ctx, "status", "--porcelain=v1")
	if err != nil {
		return []gitStatusEntry{}
	}
	return parseGitStatusOutput(out)
}

// statusLabel maps a single porcelain status byte to a human-readable string.
func statusLabel(b byte) string {
	switch b {
	case 'A':
		return "added"
	case 'D':
		return "deleted"
	case 'M':
		return "modified"
	case 'R':
		return "renamed"
	case 'C':
		return "copied"
	default:
		return "modified"
	}
}

// parseGitStatusOutput parses the output of `git status --porcelain=v1` into
// structured entries. When a file has changes in both the index and the
// work-tree (e.g. "MM"), two entries are emitted: one staged and one unstaged.
func parseGitStatusOutput(out string) []gitStatusEntry {
	entries := []gitStatusEntry{}
	for _, line := range strings.Split(out, "\n") {
		if len(line) < 4 {
			continue
		}
		xy := line[:2]
		path := line[3:]

		// Handle renames: "R  old -> new"
		if idx := strings.Index(path, " -> "); idx != -1 {
			path = path[idx+4:]
		}

		indexStatus := xy[0]
		workTreeStatus := xy[1]

		// Special cases: untracked and ignored
		if xy == "??" {
			entries = append(entries, gitStatusEntry{
				Path:       path,
				StatusCode: xy,
				Status:     "untracked",
				Staged:     false,
			})
			continue
		}
		if xy == "!!" {
			entries = append(entries, gitStatusEntry{
				Path:       path,
				StatusCode: xy,
				Status:     "ignored",
				Staged:     false,
			})
			continue
		}

		hasIndexChange := indexStatus != ' ' && indexStatus != '?'
		hasWorkTreeChange := workTreeStatus != ' ' && workTreeStatus != '?'

		// Emit a staged entry for the index change
		if hasIndexChange {
			entries = append(entries, gitStatusEntry{
				Path:       path,
				StatusCode: xy,
				Status:     statusLabel(indexStatus),
				Staged:     true,
			})
		}

		// Emit an unstaged entry for the work-tree change
		if hasWorkTreeChange {
			entries = append(entries, gitStatusEntry{
				Path:       path,
				StatusCode: xy,
				Status:     statusLabel(workTreeStatus),
				Staged:     false,
			})
		}
	}
	return entries
}

// gitExec runs a git command and returns an error if it fails.
func gitExec(ctx context.Context, args ...string) error {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = workspaceRoot
	return cmd.Run()
}

// gitOutput runs a git command and returns its combined stdout+stderr.
func gitOutput(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = workspaceRoot
	out, err := cmd.CombinedOutput()
	return string(out), err
}
