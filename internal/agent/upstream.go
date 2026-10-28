package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// UpstreamBranch compares a protected branch of the canonical repo with the
// upstream's copy as last fetched. Ahead counts the project's commits the
// upstream lacks, Behind the upstream's commits the project lacks. Either
// SHA is empty when that side has no such branch.
type UpstreamBranch struct {
	Branch        string `json:"branch"`
	SHA           string `json:"sha"`
	UpstreamSHA   string `json:"upstreamSha"`
	Ahead         int    `json:"ahead"`
	Behind        int    `json:"behind"`
	FastForwarded bool   `json:"fastForwarded,omitempty"`
}

// UpstreamStatus is the canonical repo's protected branches against the
// upstream, and when the upstream was last fetched, if ever.
type UpstreamStatus struct {
	FetchedAt *time.Time       `json:"fetchedAt,omitempty"`
	Branches  []UpstreamBranch `json:"branches"`
}

// RepoSetUpstream points the canonical repo at another upstream, or at none
// when upstreamURL is empty, forgetting the old upstream's branches as last
// fetched. The repo refuses with 409 while a fetch from the upstream runs.
func (c *Client) RepoSetUpstream(ctx context.Context, upstreamURL string) error {
	payload, err := json.Marshal(map[string]string{"url": upstreamURL})
	if err != nil {
		return fmt.Errorf("marshaling request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, c.baseURL+"/api/repo/upstream", bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	var result struct {
		URL string `json:"url"`
	}
	return c.doRepoJSON(req, "repo set upstream", &result)
}

// RepoUpstreamFetch fetches the upstream into the canonical repo's own
// namespace for it. It moves no project branch.
func (c *Client) RepoUpstreamFetch(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/repo/upstream/fetch", nil)
	if err != nil {
		return err
	}
	var status UpstreamStatus
	return c.doRepoJSON(req, "repo upstream fetch", &status)
}

// RepoUpstreamFastForward fast-forwards each protected branch that is
// strictly behind its upstream copy as last fetched, and reports how they
// all stand.
func (c *Client) RepoUpstreamFastForward(ctx context.Context, protected []string) (*UpstreamStatus, error) {
	u := c.baseURL + "/api/repo/upstream/fast-forward?" + url.Values{"protected": protected}.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, nil)
	if err != nil {
		return nil, err
	}
	var status UpstreamStatus
	if err := c.doRepoJSON(req, "repo upstream fast-forward", &status); err != nil {
		return nil, err
	}
	return &status, nil
}

// RepoUpstreamStatus compares the protected branches with the upstream as
// last fetched, without fetching.
func (c *Client) RepoUpstreamStatus(ctx context.Context, protected []string) (*UpstreamStatus, error) {
	u := c.baseURL + "/api/repo/upstream/status?" + url.Values{"protected": protected}.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	var status UpstreamStatus
	if err := c.doRepoJSON(req, "repo upstream status", &status); err != nil {
		return nil, err
	}
	return &status, nil
}

// RepoUpstreamRefs lists the upstream's branches as last fetched, the most
// recently committed to first, with the canonical repo's default branch.
func (c *Client) RepoUpstreamRefs(ctx context.Context) (*RepoRefs, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/api/repo/upstream/refs", nil)
	if err != nil {
		return nil, err
	}
	var refs RepoRefs
	if err := c.doRepoJSON(req, "repo upstream refs", &refs); err != nil {
		return nil, err
	}
	return &refs, nil
}

// RepoBundleUpstream streams one bundle of the upstream's copies of
// branches, as last fetched.
func (c *Client) RepoBundleUpstream(ctx context.Context, branches []string) (io.ReadCloser, error) {
	return c.repoBundle(ctx, url.Values{"branch": branches, "ns": {"upstream"}})
}

// RepoFetchUpstreamBases refreshes origin/<branch> in a project workspace
// for each branch, from a bundle made by RepoBundleUpstream.
func (c *Client) RepoFetchUpstreamBases(ctx context.Context, branches []string, bundle io.Reader) ([]RepoBranch, error) {
	return c.repoFetchBases(ctx, url.Values{"branch": branches, "source": {"upstream"}}, bundle)
}
