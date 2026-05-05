package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// DefaultPort is the port the agent listens on inside workspace containers.
const DefaultPort = 9100

// FileEntry represents a file or directory returned by the agent.
type FileEntry struct {
	Name    string `json:"name"`
	Path    string `json:"path"`
	Type    string `json:"type"` // "file" or "directory"
	Size    int64  `json:"size"`
	ModTime string `json:"modTime"`
}

// Client communicates with the devpad-agent running inside a workspace container.
type Client struct {
	baseURL string
	token   string
	http    *http.Client
}

// GitLogOptions controls which commits are returned from the git log.
type GitLogOptions struct {
	AllBranches bool
}

// NewClient creates a new agent Client for the given host, port, and auth token.
func NewClient(host string, port int, token string) *Client {
	return &Client{
		baseURL: fmt.Sprintf("http://%s:%d", host, port),
		token:   token,
		http:    &http.Client{Timeout: 6 * time.Minute},
	}
}

// setAuth adds the agent authentication header to a request.
func (c *Client) setAuth(req *http.Request) {
	if c.token != "" {
		req.Header.Set("X-Devpad-Agent-Token", c.token)
	}
}

// Token returns the agent auth token.
func (c *Client) Token() string {
	return c.token
}

// do adds the auth header and performs the request.
func (c *Client) do(req *http.Request) (*http.Response, error) {
	c.setAuth(req)
	return c.http.Do(req)
}

// Healthz checks if the agent is healthy.
// The health endpoint does not require authentication.
func (c *Client) Healthz(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/healthz", nil)
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("agent health check: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("agent unhealthy: status %d", resp.StatusCode)
	}
	return nil
}

// WaitReady polls the agent's health endpoint until it responds successfully
// or the context is cancelled. It uses exponential backoff starting at 250ms
// and capping at 2s, suitable for waiting on a container that was just started
// or restarted.
func (c *Client) WaitReady(ctx context.Context) error {
	backoff := 250 * time.Millisecond
	maxBackoff := 2 * time.Second

	for {
		err := c.Healthz(ctx)
		if err == nil {
			return nil
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf("agent not ready: %w", ctx.Err())
		case <-time.After(backoff):
		}

		backoff *= 2
		if backoff > maxBackoff {
			backoff = maxBackoff
		}
	}
}

// Version returns the version string of the running agent.
// If the endpoint is not available (old agent), it returns an empty string and no error.
// The version endpoint does not require authentication.
func (c *Client) Version(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/api/version", nil)
	if err != nil {
		return "", err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("agent version check: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return "", nil
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("agent version: unexpected status %d", resp.StatusCode)
	}
	var result struct {
		Version string `json:"version"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decoding version: %w", err)
	}
	return result.Version, nil
}

// ListFiles lists the contents of a directory inside the workspace.
func (c *Client) ListFiles(ctx context.Context, path string) ([]FileEntry, error) {
	u := fmt.Sprintf("%s/api/files?path=%s", c.baseURL, url.QueryEscape(path))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}

	resp, err := c.do(req)
	if err != nil {
		return nil, fmt.Errorf("listing files: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, parseError(resp)
	}

	var result struct {
		Entries []FileEntry `json:"entries"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decoding response: %w", err)
	}
	return result.Entries, nil
}

// ReadFile reads the contents of a file inside the workspace.
func (c *Client) ReadFile(ctx context.Context, path string) ([]byte, error) {
	u := fmt.Sprintf("%s/api/file?path=%s", c.baseURL, url.QueryEscape(path))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}

	resp, err := c.do(req)
	if err != nil {
		return nil, fmt.Errorf("reading file: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, parseError(resp)
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20)) // 10MB limit
	if err != nil {
		return nil, fmt.Errorf("reading response body: %w", err)
	}
	return data, nil
}

// WriteFile writes content to a file inside the workspace.
func (c *Client) WriteFile(ctx context.Context, path string, content []byte) error {
	u := fmt.Sprintf("%s/api/file?path=%s", c.baseURL, url.QueryEscape(path))
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, u, bytes.NewReader(content))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/octet-stream")

	resp, err := c.do(req)
	if err != nil {
		return fmt.Errorf("writing file: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		return parseError(resp)
	}
	return nil
}

// DeleteFile deletes a file or directory inside the workspace.
func (c *Client) DeleteFile(ctx context.Context, path string) error {
	u := fmt.Sprintf("%s/api/file?path=%s", c.baseURL, url.QueryEscape(path))
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, u, nil)
	if err != nil {
		return err
	}

	resp, err := c.do(req)
	if err != nil {
		return fmt.Errorf("deleting file: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		return parseError(resp)
	}
	return nil
}

// CreateDirectory creates a directory inside the workspace.
func (c *Client) CreateDirectory(ctx context.Context, path string) error {
	u := fmt.Sprintf("%s/api/file/mkdir?path=%s", c.baseURL, url.QueryEscape(path))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, nil)
	if err != nil {
		return err
	}

	resp, err := c.do(req)
	if err != nil {
		return fmt.Errorf("creating directory: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		return parseError(resp)
	}
	return nil
}

// RenameFile renames or moves a file or directory inside the workspace.
func (c *Client) RenameFile(ctx context.Context, oldPath, newPath string) error {
	payload, err := json.Marshal(map[string]string{
		"oldPath": oldPath,
		"newPath": newPath,
	})
	if err != nil {
		return fmt.Errorf("marshaling request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/file/rename", bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.do(req)
	if err != nil {
		return fmt.Errorf("renaming file: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		return parseError(resp)
	}
	return nil
}

// TerminalURL returns the WebSocket URL for a terminal session on this agent.
func (c *Client) TerminalURL() string {
	return strings.Replace(c.baseURL, "http://", "ws://", 1) + "/ws/terminal"
}

// SearchResult represents a single matching line from a search.
type SearchResult struct {
	File    string `json:"file"`
	Line    int    `json:"line"`
	Content string `json:"content"`
}

// SearchFiles searches for a regex pattern across files in the workspace.
func (c *Client) SearchFiles(ctx context.Context, pattern, pathFilter string, maxResults int) ([]SearchResult, error) {
	payload, err := json.Marshal(map[string]any{
		"pattern":     pattern,
		"path_filter": pathFilter,
		"max_results": maxResults,
	})
	if err != nil {
		return nil, fmt.Errorf("marshaling request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/search", bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.do(req)
	if err != nil {
		return nil, fmt.Errorf("searching files: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, parseError(resp)
	}

	var result struct {
		Results []SearchResult `json:"results"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decoding response: %w", err)
	}
	return result.Results, nil
}

// CommandResult holds the output of a command execution.
type CommandResult struct {
	Output   string `json:"output"`
	ExitCode int    `json:"exit_code"`
	Error    string `json:"error,omitempty"`
}

// RunCommand executes a shell command in the workspace container.
func (c *Client) RunCommand(ctx context.Context, command string) (*CommandResult, error) {
	payload, err := json.Marshal(map[string]string{
		"command": command,
	})
	if err != nil {
		return nil, fmt.Errorf("marshaling request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/command", bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.do(req)
	if err != nil {
		return nil, fmt.Errorf("running command: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, parseError(resp)
	}

	var result CommandResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decoding response: %w", err)
	}
	return &result, nil
}

// GitStatus represents the status of a git repository.
type GitStatus struct {
	IsRepo    bool            `json:"isRepo"`
	Branch    string          `json:"branch"`
	Files     []GitFileStatus `json:"files"`
	Ahead     int             `json:"ahead"`
	Behind    int             `json:"behind"`
	Remotes   []string        `json:"remotes"`
	UserName  string          `json:"userName"`
	UserEmail string          `json:"userEmail"`
}

// GitFileStatus represents a single file in git status.
type GitFileStatus struct {
	Path       string `json:"path"`
	StatusCode string `json:"statusCode"`
	Status     string `json:"status"`
	Staged     bool   `json:"staged"`
}

// GitCommit represents a single commit from git log.
type GitCommit struct {
	Hash      string   `json:"hash"`
	ShortHash string   `json:"shortHash"`
	Parents   []string `json:"parents"`
	Author    string   `json:"author"`
	Email     string   `json:"email"`
	Timestamp string   `json:"timestamp"`
	Message   string   `json:"message"`
}

// GitCommitFile represents a file changed by a commit.
type GitCommitFile struct {
	Path    string `json:"path"`
	OldPath string `json:"oldPath,omitempty"`
	Status  string `json:"status"`
}

// GitFileDiff represents the before/after contents for a changed file.
type GitFileDiff struct {
	Path        string `json:"path"`
	OldPath     string `json:"oldPath,omitempty"`
	Status      string `json:"status"`
	OldContent  string `json:"oldContent"`
	NewContent  string `json:"newContent"`
	OldFileName string `json:"oldFileName"`
	NewFileName string `json:"newFileName"`
}

// GitBranches represents the branch listing.
type GitBranches struct {
	Branches []GitBranch `json:"branches"`
	Current  string      `json:"current"`
}

// GitBranch represents a single git branch.
type GitBranch struct {
	Name     string `json:"name"`
	Hash     string `json:"hash"`
	Upstream string `json:"upstream"`
	Current  bool   `json:"current"`
	Remote   bool   `json:"remote"`
}

// GitRemote represents a single git remote with fetch and push URLs.
type GitRemote struct {
	Name     string `json:"name"`
	FetchURL string `json:"fetchUrl"`
	PushURL  string `json:"pushUrl"`
}

// GitActionRequest holds the parameters for a git action sent to the agent.
type GitActionRequest struct {
	Action    string   `json:"action"`
	Files     []string `json:"files,omitempty"`
	Message   string   `json:"message,omitempty"`
	Branch    string   `json:"branch,omitempty"`
	Remote    string   `json:"remote,omitempty"`
	URL       string   `json:"url,omitempty"`
	NewName   string   `json:"newName,omitempty"`
	UserName  string   `json:"userName,omitempty"`
	UserEmail string   `json:"userEmail,omitempty"`
}

// GitActionResult represents the result of a git action.
type GitActionResult struct {
	Success bool   `json:"success"`
	Output  string `json:"output"`
	Error   string `json:"error,omitempty"`
}

// GitStatus returns the current git status of the workspace.
func (c *Client) GitStatus(ctx context.Context) (*GitStatus, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/api/git/status", nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.do(req)
	if err != nil {
		return nil, fmt.Errorf("git status: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, parseError(resp)
	}
	var result GitStatus
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decoding git status: %w", err)
	}
	return &result, nil
}

// GitLog returns the commit log.
func (c *Client) GitLog(ctx context.Context, count int, opts GitLogOptions) ([]GitCommit, error) {
	u, err := url.Parse(fmt.Sprintf("%s/api/git/commits", c.baseURL))
	if err != nil {
		return nil, err
	}
	q := u.Query()
	q.Set("count", fmt.Sprintf("%d", count))
	if opts.AllBranches {
		q.Set("all", "true")
	}
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.do(req)
	if err != nil {
		return nil, fmt.Errorf("git log: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, parseError(resp)
	}
	var result struct {
		Commits []GitCommit `json:"commits"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decoding git log: %w", err)
	}
	return result.Commits, nil
}

// GitCommitFiles returns the files changed by a commit.
func (c *Client) GitCommitFiles(ctx context.Context, commit string) ([]GitCommitFile, error) {
	u, err := url.Parse(fmt.Sprintf("%s/api/git/commit-files", c.baseURL))
	if err != nil {
		return nil, err
	}
	q := u.Query()
	q.Set("commit", commit)
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.do(req)
	if err != nil {
		return nil, fmt.Errorf("git commit files: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, parseError(resp)
	}
	var result struct {
		Files []GitCommitFile `json:"files"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decoding git commit files: %w", err)
	}
	return result.Files, nil
}

// GitCommitFileDiff returns the before/after contents for one file in a commit.
func (c *Client) GitCommitFileDiff(ctx context.Context, commit, path, oldPath string) (*GitFileDiff, error) {
	u, err := url.Parse(fmt.Sprintf("%s/api/git/commit-diff", c.baseURL))
	if err != nil {
		return nil, err
	}
	q := u.Query()
	q.Set("commit", commit)
	q.Set("path", path)
	if oldPath != "" {
		q.Set("oldPath", oldPath)
	}
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.do(req)
	if err != nil {
		return nil, fmt.Errorf("git commit diff: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, parseError(resp)
	}
	var result GitFileDiff
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decoding git commit diff: %w", err)
	}
	return &result, nil
}

// GitFileDiff returns before/after contents for one staged or unstaged file.
func (c *Client) GitFileDiff(ctx context.Context, path string, staged bool) (*GitFileDiff, error) {
	u, err := url.Parse(fmt.Sprintf("%s/api/git/file-diff", c.baseURL))
	if err != nil {
		return nil, err
	}
	q := u.Query()
	q.Set("path", path)
	if staged {
		q.Set("staged", "true")
	}
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.do(req)
	if err != nil {
		return nil, fmt.Errorf("git file diff: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, parseError(resp)
	}
	var result GitFileDiff
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decoding git file diff: %w", err)
	}
	return &result, nil
}

// GitBranches returns the list of branches.
func (c *Client) GitBranches(ctx context.Context) (*GitBranches, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/api/git/branches", nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.do(req)
	if err != nil {
		return nil, fmt.Errorf("git branches: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, parseError(resp)
	}
	var result GitBranches
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decoding git branches: %w", err)
	}
	return &result, nil
}

// GitDiff returns the diff for the workspace or a specific file.
func (c *Client) GitDiff(ctx context.Context, path string, staged bool) (string, error) {
	u := fmt.Sprintf("%s/api/git/diff?path=%s&staged=%t", c.baseURL, url.QueryEscape(path), staged)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return "", err
	}
	resp, err := c.do(req)
	if err != nil {
		return "", fmt.Errorf("git diff: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", parseError(resp)
	}
	var result struct {
		Diff string `json:"diff"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decoding git diff: %w", err)
	}
	return result.Diff, nil
}

// GitRemotes returns the list of remotes with their URLs.
func (c *Client) GitRemotes(ctx context.Context) ([]GitRemote, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/api/git/remotes", nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.do(req)
	if err != nil {
		return nil, fmt.Errorf("git remotes: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, parseError(resp)
	}
	var result struct {
		Remotes []GitRemote `json:"remotes"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decoding git remotes: %w", err)
	}
	return result.Remotes, nil
}

// GitAction performs a git action (stage, unstage, commit, push, pull, etc).
func (c *Client) GitAction(ctx context.Context, req GitActionRequest) (*GitActionResult, error) {
	payload, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshaling request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/git/action", bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("git action: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, parseError(resp)
	}
	var result GitActionResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decoding git action result: %w", err)
	}
	return &result, nil
}

// AgentError represents an error response from the workspace agent,
// preserving the HTTP status code so callers can forward it appropriately.
type AgentError struct {
	StatusCode int
	Message    string
}

func (e *AgentError) Error() string {
	return fmt.Sprintf("agent error (%d): %s", e.StatusCode, e.Message)
}

func parseError(resp *http.Response) error {
	var errResp struct {
		Error string `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&errResp); err != nil {
		return &AgentError{StatusCode: resp.StatusCode, Message: fmt.Sprintf("unexpected status %d", resp.StatusCode)}
	}
	return &AgentError{StatusCode: resp.StatusCode, Message: errResp.Error}
}
