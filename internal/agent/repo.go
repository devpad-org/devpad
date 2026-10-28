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
	return c.RepoBundleBranches(ctx, []string{branch})
}

// RepoBundleBranches streams one bundle of several branches. The caller must
// close it.
func (c *Client) RepoBundleBranches(ctx context.Context, branches []string) (io.ReadCloser, error) {
	return c.repoBundle(ctx, url.Values{"branch": branches})
}

func (c *Client) repoBundle(ctx context.Context, q url.Values) (io.ReadCloser, error) {
	u := c.baseURL + "/api/repo/bundle?" + q.Encode()
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
	return c.repoImport(ctx, url.Values{"branch": {branch}}, bundle)
}

// RepoImportFastForward is RepoImport, except that an existing branch only
// moves forward: the import fails with a 409 *AgentError unless the bundle's
// commit descends from the branch's current one.
func (c *Client) RepoImportFastForward(ctx context.Context, branch string, bundle io.Reader) (*RepoBranch, error) {
	return c.repoImport(ctx, url.Values{"branch": {branch}, "ffOnly": {"true"}}, bundle)
}

func (c *Client) repoImport(ctx context.Context, q url.Values, bundle io.Reader) (*RepoBranch, error) {
	u := c.baseURL + "/api/repo/import?" + q.Encode()
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

// RepoDiffFile is one file changed by a diff.
type RepoDiffFile struct {
	Path      string `json:"path"`
	OldPath   string `json:"oldPath,omitempty"`
	Status    string `json:"status"`
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
	Binary    bool   `json:"binary"`
}

// RepoDiff lists the files head changed since its merge base with base.
type RepoDiff struct {
	BaseSHA   string         `json:"baseSha"`
	HeadSHA   string         `json:"headSha"`
	MergeBase string         `json:"mergeBase"`
	Files     []RepoDiffFile `json:"files"`
}

// RepoDiff lists the files head changed since it diverged from base. Both
// are branch names or full commit hashes.
func (c *Client) RepoDiff(ctx context.Context, base, head string) (*RepoDiff, error) {
	q := url.Values{"base": {base}, "head": {head}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/api/repo/diff?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	var diff RepoDiff
	if err := c.doRepoJSON(req, "repo diff", &diff); err != nil {
		return nil, err
	}
	return &diff, nil
}

// RepoCommit is a commit a merge request proposes.
type RepoCommit struct {
	SHA     string   `json:"sha"`
	Parents []string `json:"parents"`
	Author  string   `json:"author"`
	// AuthoredAt is the commit's own date, which whoever made it chose.
	AuthoredAt time.Time `json:"authoredAt"`
	Subject    string    `json:"subject"`
}

// RepoLog lists the commits head has that base lacks, newest first.
type RepoLog struct {
	Commits []RepoCommit `json:"commits"`
	// Truncated says there were more commits than the agent lists.
	Truncated bool `json:"truncated"`
}

// RepoLog lists the commits head has that base lacks. Both are branch names
// or full commit hashes.
func (c *Client) RepoLog(ctx context.Context, base, head string) (*RepoLog, error) {
	q := url.Values{"base": {base}, "head": {head}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/api/repo/log?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	var log RepoLog
	if err := c.doRepoJSON(req, "repo log", &log); err != nil {
		return nil, err
	}
	return &log, nil
}

// RepoFile returns a file's contents at a branch or full commit hash.
func (c *Client) RepoFile(ctx context.Context, rev, path string) ([]byte, error) {
	q := url.Values{"rev": {rev}, "path": {path}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/api/repo/file?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.do(req)
	if err != nil {
		return nil, fmt.Errorf("repo file: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, parseError(resp)
	}
	return io.ReadAll(resp.Body)
}

// RepoMergeRequest merges Source into Target. ExpectedSourceSHA, when set,
// refuses the merge if Source has moved.
type RepoMergeRequest struct {
	Source            string `json:"source"`
	Target            string `json:"target"`
	Strategy          string `json:"strategy"`
	Message           string `json:"message"`
	ExpectedSourceSHA string `json:"expectedSourceSha"`
	Name              string `json:"name"`
	Email             string `json:"email"`
}

// RepoMergeResult is where the target branch moved to.
type RepoMergeResult struct {
	Target    string `json:"target"`
	SHA       string `json:"sha"`
	Strategy  string `json:"strategy"`
	MergeBase string `json:"mergeBase"`
}

// RepoMerge merges one branch into another in the canonical repo. A merge
// with conflicts fails with an *AgentError listing them.
func (c *Client) RepoMerge(ctx context.Context, in RepoMergeRequest) (*RepoMergeResult, error) {
	payload, err := json.Marshal(in)
	if err != nil {
		return nil, fmt.Errorf("marshaling request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/repo/merge", bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	var result RepoMergeResult
	if err := c.doRepoJSON(req, "repo merge", &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// RepoPush pushes a branch of the canonical repo to its upstream.
func (c *Client) RepoPush(ctx context.Context, branch string) (*RepoBranch, error) {
	u := fmt.Sprintf("%s/api/repo/push?branch=%s", c.baseURL, url.QueryEscape(branch))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, nil)
	if err != nil {
		return nil, err
	}
	var result RepoBranch
	if err := c.doRepoJSON(req, "repo push", &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// RepoSetHead makes an existing branch the canonical repo's default branch.
func (c *Client) RepoSetHead(ctx context.Context, branch string) error {
	u := fmt.Sprintf("%s/api/repo/head?branch=%s", c.baseURL, url.QueryEscape(branch))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, nil)
	if err != nil {
		return err
	}
	var result RepoBranch
	return c.doRepoJSON(req, "repo set head", &result)
}

// RepoFetchBase updates a sandbox's devpad/<branch> from a bundle of the
// canonical branch.
func (c *Client) RepoFetchBase(ctx context.Context, branch string, bundle io.Reader) (*RepoBranch, error) {
	u := fmt.Sprintf("%s/api/repo/fetch-base?branch=%s", c.baseURL, url.QueryEscape(branch))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bundle)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	var result RepoBranch
	if err := c.doRepoJSON(req, "repo fetch base", &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// RepoFetchBases refreshes devpad/<branch> for each branch from one bundle of
// them.
func (c *Client) RepoFetchBases(ctx context.Context, branches []string, bundle io.Reader) ([]RepoBranch, error) {
	return c.repoFetchBases(ctx, url.Values{"branch": branches}, bundle)
}

func (c *Client) repoFetchBases(ctx context.Context, q url.Values, bundle io.Reader) ([]RepoBranch, error) {
	u := c.baseURL + "/api/repo/fetch-bases?" + q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bundle)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	var result struct {
		Branches []RepoBranch `json:"branches"`
	}
	if err := c.doRepoJSON(req, "repo fetch bases", &result); err != nil {
		return nil, err
	}
	return result.Branches, nil
}

// RepoCheckout is the result of checking out a canonical branch in a
// project workspace.
type RepoCheckout struct {
	Branch string `json:"branch"`
	// SHA is the local branch's commit, which differs from UpstreamSHA when
	// the local branch already existed and has diverged.
	SHA string `json:"sha"`
	// Upstream is the local ref holding devpad's copy, devpad/<branch>.
	Upstream    string `json:"upstream"`
	UpstreamSHA string `json:"upstreamSha"`
	Created     bool   `json:"created"`
}

// RepoCheckout updates devpad/<branch> in a working repo from a bundle of the
// canonical branch, creates the local branch from it if it does not exist,
// and switches to it. Switching never discards local changes.
func (c *Client) RepoCheckout(ctx context.Context, branch string, bundle io.Reader) (*RepoCheckout, error) {
	u := fmt.Sprintf("%s/api/repo/checkout?branch=%s", c.baseURL, url.QueryEscape(branch))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bundle)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	var result RepoCheckout
	if err := c.doRepoJSON(req, "repo checkout", &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// RepoBranchStatus compares a project workspace's local branch with its
// devpad/<branch> copy of the project's branch.
type RepoBranchStatus struct {
	Branch      string `json:"branch"`
	SHA         string `json:"sha"`
	UpstreamSHA string `json:"upstreamSha,omitempty"`
	Ahead       int    `json:"ahead"`
	Behind      int    `json:"behind"`
}

// RepoBranchStatus reads how a local branch of a project workspace compares
// with the project's copy as of the workspace's last fetch.
func (c *Client) RepoBranchStatus(ctx context.Context, branch string) (*RepoBranchStatus, error) {
	u := fmt.Sprintf("%s/api/repo/branch-status?branch=%s", c.baseURL, url.QueryEscape(branch))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	var result RepoBranchStatus
	if err := c.doRepoJSON(req, "repo branch status", &result); err != nil {
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
