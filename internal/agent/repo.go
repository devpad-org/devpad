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

// RepoRef is a branch and the commit it points at.
type RepoRef struct {
	Name string `json:"name"`
	SHA  string `json:"sha"`
}

// RepoRefs lists a repo's branches and its default branch.
type RepoRefs struct {
	DefaultBranch string    `json:"defaultBranch"`
	Branches      []RepoRef `json:"branches"`
}

// RepoBranch is the result of seeding or importing a branch.
type RepoBranch struct {
	Branch string `json:"branch"`
	SHA    string `json:"sha"`
}

// RepoInit turns the container's /workspace into a canonical bare repo,
// cloned from upstreamURL when it is non-empty.
func (c *Client) RepoInit(ctx context.Context, defaultBranch, upstreamURL string) (*RepoRefs, error) {
	payload, err := json.Marshal(map[string]string{"defaultBranch": defaultBranch, "upstreamUrl": upstreamURL})
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

// RepoRefs lists the branches of the repo in /workspace.
func (c *Client) RepoRefs(ctx context.Context) (*RepoRefs, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/api/repo/refs", nil)
	if err != nil {
		return nil, err
	}
	var refs RepoRefs
	if err := c.doRepoJSON(req, "repo refs", &refs); err != nil {
		return nil, err
	}
	return &refs, nil
}

// RepoComparison describes how head has diverged from base.
type RepoComparison struct {
	Base      string `json:"base"`
	Head      string `json:"head"`
	BaseSHA   string `json:"baseSha"`
	HeadSHA   string `json:"headSha"`
	MergeBase string `json:"mergeBase"`
	Ahead     int    `json:"ahead"`
	Behind    int    `json:"behind"`
}

// RepoCompare counts the commits head has that base lacks (Ahead) and the
// reverse (Behind).
func (c *Client) RepoCompare(ctx context.Context, base, head string) (*RepoComparison, error) {
	q := url.Values{"base": {base}, "head": {head}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/api/repo/compare?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	var cmp RepoComparison
	if err := c.doRepoJSON(req, "repo compare", &cmp); err != nil {
		return nil, err
	}
	return &cmp, nil
}

// RepoBundle streams a git bundle of one branch. The caller must close it.
func (c *Client) RepoBundle(ctx context.Context, branch string) (io.ReadCloser, error) {
	u := fmt.Sprintf("%s/api/repo/bundle?branch=%s", c.baseURL, url.QueryEscape(branch))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.do(req)
	if err != nil {
		return nil, fmt.Errorf("repo bundle: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		return nil, parseError(resp)
	}
	return resp.Body, nil
}

// RepoSeed clones a bundle of base into an empty /workspace and checks out a
// new branch, setting the commit identity when name or email is non-empty.
func (c *Client) RepoSeed(ctx context.Context, base, branch, name, email string, bundle io.Reader) (*RepoBranch, error) {
	q := url.Values{"base": {base}, "branch": {branch}, "name": {name}, "email": {email}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/repo/seed?"+q.Encode(), bundle)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	var result RepoBranch
	if err := c.doRepoJSON(req, "repo seed", &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// RepoImport fetches branch from a bundle into the repo in /workspace,
// ignoring every other ref in the bundle.
func (c *Client) RepoImport(ctx context.Context, branch string, bundle io.Reader) (*RepoBranch, error) {
	u := fmt.Sprintf("%s/api/repo/import?branch=%s", c.baseURL, url.QueryEscape(branch))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bundle)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	var result RepoBranch
	if err := c.doRepoJSON(req, "repo import", &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (c *Client) doRepoJSON(req *http.Request, op string, out any) error {
	resp, err := c.do(req)
	if err != nil {
		return fmt.Errorf("%s: %w", op, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return parseError(resp)
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decoding %s: %w", op, err)
	}
	return nil
}
