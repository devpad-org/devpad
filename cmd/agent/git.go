package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
)

const gitTimeout = 30 * time.Second
const gitNetworkTimeout = 5 * time.Minute
const maxGitRequestBody = 1 << 20 // 1 MB
const gitSSHCommand = "ssh -o BatchMode=yes -o StrictHostKeyChecking=yes -o NumberOfPasswordPrompts=0 -o ConnectTimeout=15"

var sshHostAuthenticityRE = regexp.MustCompile(`(?is)The authenticity of host '([^']+)' can't be established\.\s*([A-Za-z0-9_-]+) key fingerprint is ([^\s.]+)`)

// gitActionTimeout returns the context timeout appropriate for a given action.
// Network-heavy actions (clone, push, pull) get a longer timeout.
var longRunningActions = map[string]bool{
	"clone":         true,
	"push":          true,
	"pull":          true,
	"fetch":         true,
	"delete-branch": true,
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
		if prompt := parseSSHHostKeyPrompt(out); prompt != nil {
			writeJSON(w, http.StatusConflict, map[string]any{
				"error":      "ssh host key is not trusted",
				"sshHostKey": prompt,
			})
			return
		}
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

func handleGitFileDiff(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), gitTimeout)
	defer cancel()

	path := r.URL.Query().Get("path")
	staged := r.URL.Query().Get("staged") == "true"
	if path == "" {
		writeErr(w, http.StatusBadRequest, "path is required")
		return
	}
	if err := validateFilePaths([]string{path}); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}

	diff, err := gitWorkingFileDiff(ctx, path, staged)
	if err != nil {
		if errors.Is(err, errGitCommitFileNotFound) {
			writeErr(w, http.StatusNotFound, "file not found in git changes")
			return
		}
		writeErr(w, http.StatusInternalServerError, "failed to load file diff")
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

func gitWorkingFileDiff(ctx context.Context, path string, staged bool) (*gitFileDiffEntry, error) {
	info, err := gitWorkingFileDiffInfo(ctx, path, staged)
	if err != nil {
		return nil, err
	}

	oldPath := info.OldPath
	if oldPath == "" {
		oldPath = path
	}

	diff := &gitFileDiffEntry{
		Path:        info.Path,
		OldPath:     info.OldPath,
		Status:      info.Status,
		OldFileName: oldPath,
		NewFileName: info.Path,
	}

	if staged {
		if info.Status != "added" && gitHasHead(ctx) {
			oldContent, err := gitShowFile(ctx, "HEAD", oldPath)
			if err != nil {
				return nil, fmt.Errorf("reading HEAD file content: %w", err)
			}
			diff.OldContent = oldContent
		}
		if info.Status != "deleted" {
			newContent, err := gitShowIndexFile(ctx, info.Path)
			if err != nil {
				return nil, fmt.Errorf("reading staged file content: %w", err)
			}
			diff.NewContent = newContent
		}
		return diff, nil
	}

	if info.Status != "untracked" {
		oldContent, err := gitShowIndexFile(ctx, oldPath)
		if err != nil {
			if !gitHasHead(ctx) {
				return nil, fmt.Errorf("reading index file content: %w", err)
			}
			oldContent, err = gitShowFile(ctx, "HEAD", oldPath)
			if err != nil {
				return nil, fmt.Errorf("reading HEAD file content: %w", err)
			}
		}
		diff.OldContent = oldContent
	}
	if info.Status != "deleted" {
		newContent, err := readWorkspaceFile(info.Path)
		if err != nil {
			return nil, fmt.Errorf("reading working file content: %w", err)
		}
		diff.NewContent = newContent
	}

	return diff, nil
}

func gitWorkingFileDiffInfo(ctx context.Context, path string, staged bool) (*gitCommitFileEntry, error) {
	args := []string{"diff", "--name-status", "-M", "--", path}
	if staged {
		args = []string{"diff", "--cached", "--name-status", "-M", "--", path}
	}
	if out, err := gitOutput(ctx, args...); err == nil {
		files := parseGitCommitFilesOutput(out)
		for i := range files {
			if files[i].Path == path {
				return &files[i], nil
			}
		}
	}

	for _, file := range parseGitStatus(ctx) {
		if file.Path == path && file.Staged == staged {
			return &gitCommitFileEntry{
				Path:   file.Path,
				Status: file.Status,
			}, nil
		}
	}
	return nil, errGitCommitFileNotFound
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

func gitShowIndexFile(ctx context.Context, path string) (string, error) {
	out, err := gitOutput(ctx, "show", ":"+path)
	if err != nil {
		return "", err
	}
	return out, nil
}

func gitHasHead(ctx context.Context) bool {
	return gitExec(ctx, "rev-parse", "--verify", "HEAD") == nil
}

func readWorkspaceFile(path string) (string, error) {
	fullPath, err := validatePath(path)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(fullPath)
	if err != nil {
		return "", err
	}
	return string(data), nil
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

func parseSSHHostKeyPrompt(out string) *sshHostKeyPrompt {
	match := sshHostAuthenticityRE.FindStringSubmatch(out)
	if len(match) != 4 {
		return nil
	}

	host := strings.TrimSpace(match[1])
	if idx := strings.Index(host, " ("); idx != -1 {
		host = host[:idx]
	}
	keyType := strings.ToUpper(strings.TrimSpace(match[2]))
	fingerprint := strings.TrimSpace(match[3])
	if host == "" || fingerprint == "" {
		return nil
	}

	return &sshHostKeyPrompt{
		Host:        host,
		KeyType:     keyType,
		Fingerprint: fingerprint,
		RawOutput:   strings.TrimSpace(out),
	}
}

func sshHostKeyPromptFromGitFailure(ctx context.Context, out string, req gitActionRequest) *sshHostKeyPrompt {
	if prompt := parseSSHHostKeyPrompt(out); prompt != nil {
		return prompt
	}
	if !isSSHHostKeyVerificationFailure(out) {
		return nil
	}

	target, ok := sshHostTargetForGitAction(ctx, req)
	if !ok {
		return nil
	}
	prompt, err := scanSSHHostKeyPrompt(ctx, target, out)
	if err != nil {
		log.Printf("git ssh host key prompt: %v", err)
		return nil
	}
	return prompt
}

func isSSHHostKeyVerificationFailure(out string) bool {
	return strings.Contains(strings.ToLower(out), "host key verification failed")
}

func sshHostTargetForGitAction(ctx context.Context, req gitActionRequest) (*sshHostTarget, bool) {
	switch req.Action {
	case "clone":
		return parseGitSSHRemoteTarget(req.URL)
	case "push", "pull", "fetch":
		remote := req.Remote
		if remote == "" {
			remote = "origin"
		}
		out, err := gitOutput(ctx, "remote", "get-url", remote)
		if err != nil {
			return nil, false
		}
		return parseGitSSHRemoteTarget(strings.TrimSpace(out))
	case "delete-branch":
		if req.Remote == "" {
			return nil, false
		}
		out, err := gitOutput(ctx, "remote", "get-url", req.Remote)
		if err != nil {
			return nil, false
		}
		return parseGitSSHRemoteTarget(strings.TrimSpace(out))
	default:
		return nil, false
	}
}

func parseGitSSHRemoteTarget(remoteURL string) (*sshHostTarget, bool) {
	remoteURL = strings.TrimSpace(remoteURL)
	if remoteURL == "" || strings.HasPrefix(remoteURL, "-") {
		return nil, false
	}

	if strings.Contains(remoteURL, "://") {
		u, err := url.Parse(remoteURL)
		if err != nil {
			return nil, false
		}
		if u.Scheme != "ssh" && u.Scheme != "git+ssh" {
			return nil, false
		}
		host := u.Hostname()
		if host == "" {
			return nil, false
		}
		if port := u.Port(); port != "" {
			host = "[" + host + "]:" + port
		}
		target, err := parseSSHHostTarget(host)
		return target, err == nil
	}

	colon := strings.Index(remoteURL, ":")
	if colon <= 0 || strings.Contains(remoteURL[:colon], "/") {
		return nil, false
	}
	hostPart := remoteURL[:colon]
	if at := strings.LastIndex(hostPart, "@"); at != -1 {
		hostPart = hostPart[at+1:]
	}
	target, err := parseSSHHostTarget(hostPart)
	return target, err == nil
}

func scanSSHHostKeyPrompt(ctx context.Context, target *sshHostTarget, rawOutput string) (*sshHostKeyPrompt, error) {
	scanOut, err := scanSSHHostKeys(ctx, target, "")
	if err != nil {
		return nil, err
	}
	line, fingerprint, keyType, err := preferredScannedHostKey(ctx, scanOut)
	if err != nil {
		return nil, err
	}
	if line == "" {
		return nil, fmt.Errorf("scanned SSH host key was empty")
	}
	return &sshHostKeyPrompt{
		Host:        target.KnownHost,
		KeyType:     strings.ToUpper(keyType),
		Fingerprint: fingerprint,
		RawOutput:   strings.TrimSpace(rawOutput),
	}, nil
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

	branches := []gitBranchEntry{}
	if out, err := gitOutput(ctx, "for-each-ref", "--format=%(refname)%09%(refname:short)%09%(objectname:short)%09%(upstream:short)", "refs/heads", "refs/remotes"); err == nil {
		branches = parseGitBranchRefs(out, current, remotes)
	}
	writeJSON(w, http.StatusOK, map[string]any{"branches": branches, "current": current})
}

type gitBranchEntry struct {
	Name         string `json:"name"`
	Hash         string `json:"hash"`
	Upstream     string `json:"upstream"`
	Current      bool   `json:"current"`
	Remote       bool   `json:"remote"`
	RemoteName   string `json:"remoteName,omitempty"`
	RemoteBranch string `json:"remoteBranch,omitempty"`
}

func parseGitBranchRefs(out, current string, remotes []string) []gitBranchEntry {
	branches := []gitBranchEntry{}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 4)
		if len(parts) < 2 {
			continue
		}

		fullRef := parts[0]
		name := parts[1]
		shortHash := ""
		upstream := ""
		if len(parts) > 2 {
			shortHash = parts[2]
		}
		if len(parts) > 3 {
			upstream = parts[3]
		}

		remoteName, remoteBranch, remote := remoteBranchFromRef(fullRef, remotes)
		if remote && (remoteBranch == "" || remoteBranch == "HEAD") {
			continue
		}

		branches = append(branches, gitBranchEntry{
			Name:         name,
			Hash:         shortHash,
			Upstream:     upstream,
			Current:      !remote && name == current,
			Remote:       remote,
			RemoteName:   remoteName,
			RemoteBranch: remoteBranch,
		})
	}
	return branches
}

func remoteBranchFromRef(fullRef string, remotes []string) (string, string, bool) {
	const prefix = "refs/remotes/"
	remoteRef, ok := strings.CutPrefix(fullRef, prefix)
	if !ok {
		return "", "", false
	}

	sortedRemotes := append([]string(nil), remotes...)
	sort.SliceStable(sortedRemotes, func(i, j int) bool {
		return len(sortedRemotes[i]) > len(sortedRemotes[j])
	})
	for _, remote := range sortedRemotes {
		if remoteRef == remote {
			return remote, "", true
		}
		if branch, ok := strings.CutPrefix(remoteRef, remote+"/"); ok {
			return remote, branch, true
		}
	}

	remoteName, branch, ok := strings.Cut(remoteRef, "/")
	if !ok {
		return remoteRef, "", true
	}
	return remoteName, branch, true
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
	Action      string   `json:"action"`
	Files       []string `json:"files"`
	Msg         string   `json:"message"`
	Branch      string   `json:"branch"`
	Remote      string   `json:"remote"`
	URL         string   `json:"url"`
	NewName     string   `json:"newName"`
	UserName    string   `json:"userName"`
	UserEmail   string   `json:"userEmail"`
	Host        string   `json:"host"`
	KeyType     string   `json:"keyType"`
	Fingerprint string   `json:"fingerprint"`
}

type sshHostKeyPrompt struct {
	Host        string `json:"host"`
	KeyType     string `json:"keyType"`
	Fingerprint string `json:"fingerprint"`
	RawOutput   string `json:"rawOutput,omitempty"`
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
	"stage":               actionStage,
	"unstage":             actionUnstage,
	"set-config":          actionSetConfig,
	"commit":              actionCommit,
	"push":                actionPush,
	"pull":                actionPull,
	"fetch":               actionFetch,
	"checkout":            actionCheckout,
	"checkout-new":        actionCheckoutNew,
	"merge":               actionMerge,
	"delete-branch":       actionDeleteBranch,
	"discard":             actionDiscard,
	"init":                actionInit,
	"clone":               actionClone,
	"accept-ssh-host-key": actionAcceptSSHHostKey,
	"remote-add":          actionRemoteAdd,
	"remote-remove":       actionRemoteRemove,
	"remote-rename":       actionRemoteRename,
	"remote-set-url":      actionRemoteSetURL,
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
		resp := map[string]any{
			"success": false,
			"output":  out,
			"error":   err.Error(),
		}
		if prompt := sshHostKeyPromptFromGitFailure(ctx, out, req); prompt != nil {
			resp["sshHostKey"] = prompt
		}
		writeJSON(w, http.StatusOK, resp)
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
	if req.Remote != "" {
		if gitExec(ctx, "show-ref", "--verify", "--quiet", "refs/heads/"+req.Branch) == nil {
			return gitOutput(ctx, "checkout", req.Branch)
		}
		return gitOutput(ctx, "checkout", "--track", req.Remote+"/"+req.Branch)
	}
	return gitOutput(ctx, "checkout", req.Branch)
}

func actionCheckoutNew(ctx context.Context, req gitActionRequest) (string, error) {
	if req.Branch == "" {
		return "", &gitActionError{"branch is required"}
	}
	return gitOutput(ctx, "checkout", "-b", req.Branch)
}

func actionMerge(ctx context.Context, req gitActionRequest) (string, error) {
	if req.Branch == "" {
		return "", &gitActionError{"branch is required"}
	}
	target := req.Branch
	if req.Remote != "" {
		target = req.Remote + "/" + req.Branch
	}
	return gitOutput(ctx, "merge", "--no-edit", target)
}

func actionDeleteBranch(ctx context.Context, req gitActionRequest) (string, error) {
	if req.Branch == "" {
		return "", &gitActionError{"branch is required"}
	}
	if req.Remote != "" {
		return gitOutput(ctx, "push", req.Remote, "--delete", req.Branch)
	}
	return gitOutput(ctx, "branch", "-d", req.Branch)
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

func actionAcceptSSHHostKey(ctx context.Context, req gitActionRequest) (string, error) {
	if req.Host == "" {
		return "", &gitActionError{"host is required"}
	}
	if req.Fingerprint == "" {
		return "", &gitActionError{"fingerprint is required"}
	}

	target, err := parseSSHHostTarget(req.Host)
	if err != nil {
		return "", &gitActionError{err.Error()}
	}
	keyType, err := normalizeSSHKeyType(req.KeyType)
	if err != nil {
		return "", &gitActionError{err.Error()}
	}

	scanOut, err := scanSSHHostKeys(ctx, target, keyType)
	if err != nil {
		return scanOut, fmt.Errorf("scanning ssh host key: %w", err)
	}

	line, err := matchingScannedHostKey(ctx, scanOut, req.Fingerprint, keyType)
	if err != nil {
		return scanOut, err
	}
	if err := appendKnownHostLine(line); err != nil {
		return scanOut, fmt.Errorf("writing known_hosts: %w", err)
	}

	displayKeyType := strings.ToUpper(keyType)
	if displayKeyType == "" {
		displayKeyType = "SSH"
	}
	return fmt.Sprintf("Accepted %s host key for %s", displayKeyType, target.KnownHost), nil
}

func scanSSHHostKeys(ctx context.Context, target *sshHostTarget, keyType string) (string, error) {
	args := []string{}
	if keyType != "" {
		args = append(args, "-t", keyType)
	}
	if target.Port != "" {
		args = append(args, "-p", target.Port)
	}
	args = append(args, target.ScanHost)

	cmd := exec.CommandContext(ctx, "ssh-keyscan", args...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

type sshHostTarget struct {
	ScanHost  string
	Port      string
	KnownHost string
}

func parseSSHHostTarget(host string) (*sshHostTarget, error) {
	host = strings.TrimSpace(host)
	if host == "" {
		return nil, fmt.Errorf("host is required")
	}
	if strings.HasPrefix(host, "-") || strings.ContainsFunc(host, unicode.IsSpace) {
		return nil, fmt.Errorf("host is invalid")
	}

	if strings.HasPrefix(host, "[") {
		end := strings.Index(host, "]")
		if end <= 1 || end == len(host)-1 || host[end+1] != ':' {
			return nil, fmt.Errorf("host is invalid")
		}
		port := host[end+2:]
		if err := validateSSHPort(port); err != nil {
			return nil, err
		}
		scanHost := host[1:end]
		if scanHost == "" || strings.HasPrefix(scanHost, "-") || strings.ContainsFunc(scanHost, unicode.IsSpace) {
			return nil, fmt.Errorf("host is invalid")
		}
		return &sshHostTarget{ScanHost: scanHost, Port: port, KnownHost: host}, nil
	}

	if strings.Contains(host, "]") {
		return nil, fmt.Errorf("host is invalid")
	}
	return &sshHostTarget{ScanHost: host, KnownHost: host}, nil
}

func validateSSHPort(port string) error {
	if port == "" {
		return fmt.Errorf("port is required")
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 {
		return fmt.Errorf("port is invalid")
	}
	return nil
}

func normalizeSSHKeyType(keyType string) (string, error) {
	keyType = strings.ToLower(strings.TrimSpace(keyType))
	if keyType == "" {
		return "", nil
	}
	switch keyType {
	case "rsa", "dsa", "ecdsa", "ed25519":
		return keyType, nil
	default:
		return "", fmt.Errorf("keyType is unsupported")
	}
}

func matchingScannedHostKey(ctx context.Context, scanOut, expectedFingerprint, expectedKeyType string) (string, error) {
	expectedFingerprint = strings.TrimSpace(expectedFingerprint)
	for _, line := range strings.Split(scanOut, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fingerprint, keyType, err := fingerprintKnownHostLine(ctx, line)
		if err != nil {
			continue
		}
		if fingerprint != expectedFingerprint {
			continue
		}
		if expectedKeyType != "" {
			normalizedExpected, err := normalizeSSHKeyType(expectedKeyType)
			if err != nil {
				return "", err
			}
			normalizedScanned, err := normalizeSSHKeyType(keyType)
			if err != nil || normalizedScanned != normalizedExpected {
				continue
			}
		}
		return line, nil
	}
	return "", fmt.Errorf("scanned SSH host key did not match expected fingerprint")
}

func preferredScannedHostKey(ctx context.Context, scanOut string) (line, fingerprint, keyType string, err error) {
	type scannedKey struct {
		line        string
		fingerprint string
		keyType     string
	}
	keys := []scannedKey{}
	for _, line := range strings.Split(scanOut, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fingerprint, keyType, err := fingerprintKnownHostLine(ctx, line)
		if err != nil {
			continue
		}
		keys = append(keys, scannedKey{line: line, fingerprint: fingerprint, keyType: keyType})
	}
	if len(keys) == 0 {
		return "", "", "", fmt.Errorf("no SSH host keys found")
	}

	for _, preferred := range []string{"ed25519", "ecdsa", "rsa", "dsa"} {
		for _, key := range keys {
			normalized, err := normalizeSSHKeyType(key.keyType)
			if err == nil && normalized == preferred {
				return key.line, key.fingerprint, normalized, nil
			}
		}
	}

	key := keys[0]
	normalized, err := normalizeSSHKeyType(key.keyType)
	if err != nil {
		normalized = strings.ToLower(key.keyType)
	}
	return key.line, key.fingerprint, normalized, nil
}

func fingerprintKnownHostLine(ctx context.Context, line string) (fingerprint, keyType string, err error) {
	cmd := exec.CommandContext(ctx, "ssh-keygen", "-lf", "-")
	cmd.Stdin = strings.NewReader(line + "\n")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", "", fmt.Errorf("fingerprinting ssh host key: %w", err)
	}
	fields := strings.Fields(string(out))
	if len(fields) < 4 {
		return "", "", fmt.Errorf("unexpected ssh-keygen output")
	}
	keyType = strings.Trim(fields[len(fields)-1], "()")
	return fields[1], keyType, nil
}

func appendKnownHostLine(line string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("resolving home directory: %w", err)
	}
	sshDir := filepath.Join(home, ".ssh")
	if err := os.MkdirAll(sshDir, 0700); err != nil {
		return fmt.Errorf("creating .ssh directory: %w", err)
	}

	knownHostsPath := filepath.Join(sshDir, "known_hosts")
	existing, err := os.ReadFile(knownHostsPath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("reading known_hosts: %w", err)
	}
	for _, existingLine := range strings.Split(string(existing), "\n") {
		if strings.TrimSpace(existingLine) == line {
			return nil
		}
	}

	f, err := os.OpenFile(knownHostsPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("opening known_hosts: %w", err)
	}
	defer f.Close()
	if len(existing) > 0 && !strings.HasSuffix(string(existing), "\n") {
		if _, err := f.WriteString("\n"); err != nil {
			return fmt.Errorf("separating known_hosts entry: %w", err)
		}
	}
	if _, err := f.WriteString(line + "\n"); err != nil {
		return fmt.Errorf("appending known_hosts entry: %w", err)
	}
	return nil
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
	cmd := gitCommand(ctx, args...)
	return cmd.Run()
}

// gitOutput runs a git command and returns its combined stdout+stderr.
func gitOutput(ctx context.Context, args ...string) (string, error) {
	cmd := gitCommand(ctx, args...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func gitCommand(ctx context.Context, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = workspaceRoot
	cmd.Env = append(os.Environ(),
		"GIT_TERMINAL_PROMPT=0",
		"GIT_SSH_COMMAND="+gitSSHCommand,
		"GIT_MERGE_AUTOEDIT=no",
		"GIT_EDITOR=true",
	)
	return cmd
}
