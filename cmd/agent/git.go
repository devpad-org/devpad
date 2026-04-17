package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os/exec"
	"strings"
	"time"
)

const gitTimeout = 30 * time.Second
const maxGitRequestBody = 1 << 20 // 1 MB

// validateGitRef rejects branch/remote names that could be interpreted as flags.
func validateGitRef(name, field string) error {
	if strings.HasPrefix(name, "-") {
		return fmt.Errorf("%s must not start with '-'", field)
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
		for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
			if line != "" {
				remotes = append(remotes, line)
			}
		}
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

	out, err := gitOutput(ctx, "log", "--oneline", "--format=%H%n%h%n%an%n%ae%n%at%n%s", "-n", count)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"commits": []any{}})
		return
	}

	lines := strings.Split(strings.TrimSpace(out), "\n")
	commits := []map[string]string{}
	for i := 0; i+5 < len(lines); i += 6 {
		commits = append(commits, map[string]string{
			"hash":      lines[i],
			"shortHash": lines[i+1],
			"author":    lines[i+2],
			"email":     lines[i+3],
			"timestamp": lines[i+4],
			"message":   lines[i+5],
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"commits": commits})
}

func handleGitBranches(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), gitTimeout)
	defer cancel()

	current := ""
	if out, err := gitOutput(ctx, "rev-parse", "--abbrev-ref", "HEAD"); err == nil {
		current = strings.TrimSpace(out)
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
				"remote":   strings.HasPrefix(name, "origin/"),
			})
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"branches": branches, "current": current})
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

func handleGitAction(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), gitTimeout)
	defer cancel()

	r.Body = http.MaxBytesReader(w, r.Body, maxGitRequestBody)

	var req struct {
		Action    string   `json:"action"`
		Files     []string `json:"files"`
		Msg       string   `json:"message"`
		Branch    string   `json:"branch"`
		Remote    string   `json:"remote"`
		UserName  string   `json:"userName"`
		UserEmail string   `json:"userEmail"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request body")
		return
	}

	// Validate inputs to prevent flag injection and path traversal.
	if req.Branch != "" {
		if err := validateGitRef(req.Branch, "branch"); err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	if req.Remote != "" {
		if err := validateGitRef(req.Remote, "remote"); err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	if len(req.Files) > 0 {
		if err := validateFilePaths(req.Files); err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	if req.UserName != "" {
		if err := validateUserIdentity(req.UserName, "userName"); err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	if req.UserEmail != "" {
		if err := validateUserIdentity(req.UserEmail, "userEmail"); err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
	}

	var out string
	var err error

	switch req.Action {
	case "stage":
		if len(req.Files) == 0 {
			out, err = gitOutput(ctx, "add", "-A")
		} else {
			args := append([]string{"add", "--"}, req.Files...)
			out, err = gitOutput(ctx, args...)
		}
	case "unstage":
		// On an empty repo with no commits, HEAD doesn't exist so "git reset HEAD" fails.
		// Use "git rm --cached" in that case.
		hasHead := gitExec(ctx, "rev-parse", "HEAD") == nil
		if hasHead {
			if len(req.Files) == 0 {
				out, err = gitOutput(ctx, "reset", "HEAD")
			} else {
				args := append([]string{"reset", "HEAD", "--"}, req.Files...)
				out, err = gitOutput(ctx, args...)
			}
		} else {
			if len(req.Files) == 0 {
				out, err = gitOutput(ctx, "rm", "--cached", "-r", ".")
			} else {
				args := append([]string{"rm", "--cached", "--"}, req.Files...)
				out, err = gitOutput(ctx, args...)
			}
		}
	case "set-config":
		if req.UserName != "" {
			_, _ = gitOutput(ctx, "config", "user.name", req.UserName)
		}
		if req.UserEmail != "" {
			_, _ = gitOutput(ctx, "config", "user.email", req.UserEmail)
		}
		out = "Git config updated"
	case "commit":
		if req.Msg == "" {
			writeErr(w, http.StatusBadRequest, "commit message is required")
			return
		}
		out, err = gitOutput(ctx, "commit", "-m", req.Msg)
	case "push":
		remote := req.Remote
		if remote == "" {
			remote = "origin"
		}
		args := []string{"push", remote}
		if req.Branch != "" {
			args = append(args, req.Branch)
		}
		out, err = gitOutput(ctx, args...)
	case "pull":
		remote := req.Remote
		if remote == "" {
			remote = "origin"
		}
		args := []string{"pull", remote}
		if req.Branch != "" {
			args = append(args, req.Branch)
		}
		out, err = gitOutput(ctx, args...)
	case "checkout":
		if req.Branch == "" {
			writeErr(w, http.StatusBadRequest, "branch is required")
			return
		}
		out, err = gitOutput(ctx, "checkout", req.Branch)
	case "checkout-new":
		if req.Branch == "" {
			writeErr(w, http.StatusBadRequest, "branch is required")
			return
		}
		out, err = gitOutput(ctx, "checkout", "-b", req.Branch)
	case "discard":
		if len(req.Files) == 0 {
			writeErr(w, http.StatusBadRequest, "files are required for discard")
			return
		}
		args := append([]string{"checkout", "--"}, req.Files...)
		out, err = gitOutput(ctx, args...)
	case "init":
		out, err = gitOutput(ctx, "init")
	default:
		writeErr(w, http.StatusBadRequest, "unknown action: "+req.Action)
		return
	}

	if err != nil {
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
