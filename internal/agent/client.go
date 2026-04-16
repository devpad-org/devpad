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
	http    *http.Client
}

// NewClient creates a new agent Client for the given host and port.
func NewClient(host string, port int) *Client {
	return &Client{
		baseURL: fmt.Sprintf("http://%s:%d", host, port),
		http:    &http.Client{},
	}
}

// Healthz checks if the agent is healthy.
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

// ListFiles lists the contents of a directory inside the workspace.
func (c *Client) ListFiles(ctx context.Context, path string) ([]FileEntry, error) {
	u := fmt.Sprintf("%s/api/files?path=%s", c.baseURL, url.QueryEscape(path))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}

	resp, err := c.http.Do(req)
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

	resp, err := c.http.Do(req)
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

	resp, err := c.http.Do(req)
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

	resp, err := c.http.Do(req)
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

	resp, err := c.http.Do(req)
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

	resp, err := c.http.Do(req)
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

func parseError(resp *http.Response) error {
	var errResp struct {
		Error string `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&errResp); err != nil {
		return fmt.Errorf("unexpected status %d", resp.StatusCode)
	}
	return fmt.Errorf("agent error (%d): %s", resp.StatusCode, errResp.Error)
}
