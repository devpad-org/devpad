package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

// GitBridgeRequest is a request git-remote-devpad made inside a project
// workspace. Everything in it comes from the workspace and is untrusted.
type GitBridgeRequest struct {
	ID       string   `json:"id"`
	Op       string   `json:"op"`
	Branches []string `json:"branches,omitempty"`
	Force    bool     `json:"force,omitempty"`
}

// GitBridgeResult is devpad's answer to a GitBridgeRequest.
type GitBridgeResult struct {
	Error         string    `json:"error,omitempty"`
	DefaultBranch string    `json:"defaultBranch,omitempty"`
	Refs          []RepoRef `json:"refs,omitempty"`
	SHA           string    `json:"sha,omitempty"`
}

// GitBridgeRequests long-polls the agent for git requests, waiting up to
// wait for one.
func (c *Client) GitBridgeRequests(ctx context.Context, wait time.Duration) ([]GitBridgeRequest, error) {
	q := url.Values{"wait": {wait.String()}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/api/git-bridge/requests?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	var out struct {
		Requests []GitBridgeRequest `json:"requests"`
	}
	if err := c.doRepoJSON(req, "git bridge poll", &out); err != nil {
		return nil, err
	}
	return out.Requests, nil
}

// GitBridgeRespond posts the result of a git request. The agent answers 410
// when the helper that asked has gone away.
func (c *Client) GitBridgeRespond(ctx context.Context, id string, result GitBridgeResult) error {
	payload, err := json.Marshal(result)
	if err != nil {
		return fmt.Errorf("marshaling result: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/git-bridge/requests/"+url.PathEscape(id)+"/result", bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	return c.doNoContent(req, "git bridge result")
}

// GitBridgeConfigureRemote installs git-remote-devpad and points the repo's
// devpad remote at it.
func (c *Client) GitBridgeConfigureRemote(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/git-bridge/remote", nil)
	if err != nil {
		return err
	}
	return c.doNoContent(req, "git bridge remote")
}

func (c *Client) doNoContent(req *http.Request, op string) error {
	resp, err := c.do(req)
	if err != nil {
		return fmt.Errorf("%s: %w", op, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
		return parseError(resp)
	}
	return nil
}
