package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
)

// DiscoveredRepo describes a git repo found in a standalone workspace.
type DiscoveredRepo struct {
	// Path is relative to /workspace; "." is /workspace itself.
	Path        string `json:"path"`
	Branch      string `json:"branch"`
	BranchCount int    `json:"branchCount"`
	Dirty       bool   `json:"dirty"`
	// OriginURL has any embedded credentials removed.
	OriginURL              string   `json:"originUrl"`
	OriginHadCredentials   bool     `json:"originHadCredentials"`
	OriginDefaultBranch    string   `json:"originDefaultBranch"`
	RemotesWithCredentials []string `json:"remotesWithCredentials"`
	CredentialHelpers      []string `json:"credentialHelpers"`
	HasDevpadRemote        bool     `json:"hasDevpadRemote"`
}

// ContentsPreview describes a repo that would be made from a workspace's
// files, leaving out junk and, unless asked, likely secrets.
type ContentsPreview struct {
	Files             int      `json:"files"`
	Bytes             int64    `json:"bytes"`
	SensitiveFiles    []string `json:"sensitiveFiles"`
	SensitiveCount    int      `json:"sensitiveCount"`
	SensitiveBytes    int64    `json:"sensitiveBytes"`
	JunkPatterns      []string `json:"junkPatterns"`
	SensitivePatterns []string `json:"sensitivePatterns"`
	MaxBytes          int64    `json:"maxBytes"`
}

// RepoDiscovery lists a workspace's repos, or previews a repo made from its
// files when it has none, along with credentials found in it.
type RepoDiscovery struct {
	Repos             []DiscoveredRepo `json:"repos"`
	Contents          *ContentsPreview `json:"contents,omitempty"`
	CredentialFiles   []string         `json:"credentialFiles"`
	CredentialHelpers []string         `json:"credentialHelpers"`
}

// RepoDiscover finds the git repos in the workspace.
func (c *Client) RepoDiscover(ctx context.Context) (*RepoDiscovery, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/api/repo/discover", nil)
	if err != nil {
		return nil, err
	}
	var result RepoDiscovery
	if err := c.doRepoJSON(req, "repo discover", &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// RepoBundleAll streams a bundle of every branch and tag of the repo at dir,
// relative to /workspace. The caller must close it.
func (c *Client) RepoBundleAll(ctx context.Context, dir string) (io.ReadCloser, error) {
	u := c.baseURL + "/api/repo/bundle-all?" + url.Values{"dir": {dir}}.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.do(req)
	if err != nil {
		return nil, fmt.Errorf("repo bundle all: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		return nil, parseError(resp)
	}
	return resp.Body, nil
}

// RepoContentsInit is the result of making a repo from a workspace's files.
type RepoContentsInit struct {
	Branch string `json:"branch"`
	SHA    string `json:"sha"`
	Files  int    `json:"files"`
}

// RepoInitContents makes a repo at /workspace from the files already there,
// committing as name and email.
func (c *Client) RepoInitContents(ctx context.Context, defaultBranch string, includeSensitive bool, name, email string) (*RepoContentsInit, error) {
	payload, err := json.Marshal(map[string]any{
		"defaultBranch": defaultBranch, "includeSensitive": includeSensitive, "name": name, "email": email,
	})
	if err != nil {
		return nil, fmt.Errorf("marshaling request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/repo/init-contents", bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	var result RepoContentsInit
	if err := c.doRepoJSON(req, "repo init contents", &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// RepoUndoInitContents removes a repo made by RepoInitContents, as long as it
// still holds only the commit sha.
func (c *Client) RepoUndoInitContents(ctx context.Context, sha string) error {
	u := c.baseURL + "/api/repo/init-contents/undo?" + url.Values{"sha": {sha}}.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, nil)
	if err != nil {
		return err
	}
	var result map[string]bool
	return c.doRepoJSON(req, "repo undo init contents", &result)
}

// RepoInitEmpty turns the container's /workspace into an empty canonical
// bare repo with no commits, recording upstreamURL for pushes when it is
// non-empty. RepoImportInitial fills it.
func (c *Client) RepoInitEmpty(ctx context.Context, defaultBranch, upstreamURL string) (*RepoRefs, error) {
	payload, err := json.Marshal(map[string]any{"defaultBranch": defaultBranch, "upstreamUrl": upstreamURL, "empty": true})
	if err != nil {
		return nil, fmt.Errorf("marshaling request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/repo/init", bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	var refs RepoRefs
	if err := c.doRepoJSON(req, "repo init", &refs); err != nil {
		return nil, err
	}
	return &refs, nil
}

// RepoImportInitial fills an empty canonical repo with every branch and tag
// of a bundle, which must include defaultBranch.
func (c *Client) RepoImportInitial(ctx context.Context, defaultBranch string, bundle io.Reader) (*RepoRefs, error) {
	u := c.baseURL + "/api/repo/import-initial?" + url.Values{"defaultBranch": {defaultBranch}}.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bundle)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	var refs RepoRefs
	if err := c.doRepoJSON(req, "repo import initial", &refs); err != nil {
		return nil, err
	}
	return &refs, nil
}

// ManagedSSHFile names a file devpad wrote into ~/.ssh and the SHA-256 of
// the contents it wrote.
type ManagedSSHFile struct {
	Name   string `json:"name"`
	SHA256 string `json:"sha256"`
}

// SSHCleanup reports which of devpad's SSH files were removed. Kept files
// exist but differ from what devpad wrote, so they were left alone.
type SSHCleanup struct {
	Removed []string `json:"removed"`
	Kept    []string `json:"kept"`
	Absent  []string `json:"absent"`
}

// SSHRemoveManaged removes the SSH files devpad wrote into the container,
// each only if it still holds what devpad wrote.
func (c *Client) SSHRemoveManaged(ctx context.Context, files []ManagedSSHFile) (*SSHCleanup, error) {
	payload, err := json.Marshal(map[string]any{"files": files})
	if err != nil {
		return nil, fmt.Errorf("marshaling request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/ssh/remove-managed", bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	var result SSHCleanup
	if err := c.doRepoJSON(req, "ssh remove managed", &result); err != nil {
		return nil, err
	}
	return &result, nil
}
