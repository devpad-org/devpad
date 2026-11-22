package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"

	"github.com/devpad-org/devpad/internal/gitbundle"
	"github.com/devpad-org/devpad/internal/pipelineid"
)

// ErrBundleTooLarge is returned for a bundle over gitbundle.MaxBytes.
var ErrBundleTooLarge = fmt.Errorf("the bundle is over %d MB", gitbundle.MaxBytes>>20)

// bundleLimit is gitbundle.MaxBytes, which tests lower.
var bundleLimit int64 = gitbundle.MaxBytes

var fullHashPattern = regexp.MustCompile(`^([0-9a-f]{40}|[0-9a-f]{64})$`)

// PipelinePin says what a pipeline tests: Commit as it is, or the trial
// merge of Source onto Target, the tree a merge, squash or fast-forward of
// Source into Target lands. Each is a full commit hash.
type PipelinePin struct {
	ID     string `json:"id"`
	Commit string `json:"commit,omitempty"`
	Source string `json:"source,omitempty"`
	Target string `json:"target,omitempty"`
}

// Validate refuses a pin the agent would.
func (p PipelinePin) Validate() error {
	if !pipelineid.Valid(p.ID) {
		return fmt.Errorf("pipeline id %q must be lowercase letters, digits and hyphens", p.ID)
	}
	var hashes []string
	switch {
	case p.Commit != "" && p.Source == "" && p.Target == "":
		hashes = []string{p.Commit}
	case p.Commit == "" && p.Source != "" && p.Target != "":
		hashes = []string{p.Source, p.Target}
	default:
		return errors.New("a pipeline tests a commit, or a source merged onto a target")
	}
	for _, h := range hashes {
		if !fullHashPattern.MatchString(h) {
			return fmt.Errorf("%q is not a full commit hash", h)
		}
	}
	return nil
}

// PipelinePinResult is what a pipeline tests, pinned in the canonical repo,
// or why there is nothing to test.
type PipelinePinResult struct {
	// Ref holds SHA until the pin is deleted.
	Ref string `json:"ref,omitempty"`
	// SHA is the commit tested. For a merge it is the target when it
	// already has the source, the source when the target can fast-forward
	// to it, and otherwise a merge commit of the two that no branch has.
	SHA string `json:"sha,omitempty"`
	// Conflict is set instead, with nothing pinned, when the source does
	// not merge onto the target.
	Conflict *MergeConflict `json:"conflict,omitempty"`
}

// MergeConflict is why a source does not merge onto a target.
type MergeConflict struct {
	Message string `json:"message"`
	// Paths are the files that conflict, when that is why.
	Paths []string `json:"paths,omitempty"`
}

// RepoPinPipeline pins what a pipeline tests to its ref, replacing an
// earlier pin of the same id. It never moves a branch.
func (c *Client) RepoPinPipeline(ctx context.Context, pin PipelinePin) (*PipelinePinResult, error) {
	if err := pin.Validate(); err != nil {
		return nil, err
	}
	payload, err := json.Marshal(pin)
	if err != nil {
		return nil, fmt.Errorf("marshaling request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/repo/pipeline-ref", bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	var result PipelinePinResult
	if err := c.doRepoJSON(req, "repo pin pipeline", &result); err != nil {
		return nil, err
	}
	if result.Conflict == nil && (result.Ref != pipelineid.Ref(pin.ID) || !fullHashPattern.MatchString(result.SHA)) {
		return nil, fmt.Errorf("repo pin pipeline: unexpected answer %+v", result)
	}
	return &result, nil
}

// RepoPipelineBundle writes a bundle of what a pipeline tests to w, and
// returns its size. Past gitbundle.MaxBytes it stops with ErrBundleTooLarge,
// having written part of it.
func (c *Client) RepoPipelineBundle(ctx context.Context, id string, w io.Writer) (int64, error) {
	if !pipelineid.Valid(id) {
		return 0, fmt.Errorf("pipeline id %q must be lowercase letters, digits and hyphens", id)
	}
	body, err := c.repoBundle(ctx, "/api/repo/pipeline-bundle", url.Values{"id": {id}})
	if err != nil {
		return 0, err
	}
	defer body.Close()
	n, err := io.Copy(w, io.LimitReader(body, bundleLimit+1))
	if err != nil {
		return n, fmt.Errorf("copying the pipeline's bundle: %w", err)
	}
	if n > bundleLimit {
		return n, ErrBundleTooLarge
	}
	return n, nil
}

// RepoDeletePipelineRef deletes a pipeline's ref, if it has one.
func (c *Client) RepoDeletePipelineRef(ctx context.Context, id string) error {
	if !pipelineid.Valid(id) {
		return fmt.Errorf("pipeline id %q must be lowercase letters, digits and hyphens", id)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, c.baseURL+"/api/repo/pipeline-ref?"+url.Values{"id": {id}}.Encode(), nil)
	if err != nil {
		return err
	}
	var result struct {
		Ref string `json:"ref"`
	}
	return c.doRepoJSON(req, "repo delete pipeline ref", &result)
}
