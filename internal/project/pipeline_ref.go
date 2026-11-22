package project

import (
	"context"
	"io"

	"github.com/devpad-org/devpad/internal/agent"
	"github.com/devpad-org/devpad/internal/pipelineid"
)

// A pipeline tests a commit as its canonical repo holds it: a commit of a
// branch, or the trial merge of a merge request's source onto its target.
// The repo container pins it to a ref of the pipeline's own, which moves no
// branch, and bundles it for the pipeline's jobs, which get no other way to
// the repo.

// PinPipeline pins what a pipeline tests. A source that does not merge onto
// its target comes back as the answer's Conflict, not an error.
func (s *service) PinPipeline(ctx context.Context, userID, projectID int64, pin agent.PipelinePin) (*agent.PipelinePinResult, error) {
	if err := pin.Validate(); err != nil {
		return nil, invalid("%s", err)
	}
	c, err := s.readyRepoAgent(ctx, userID, projectID)
	if err != nil {
		return nil, err
	}
	return c.RepoPinPipeline(ctx, pin)
}

// PipelineBundle writes a bundle of what a pipeline tests to w, and returns
// its size, refusing one over agent.MaxBundleBytes.
func (s *service) PipelineBundle(ctx context.Context, userID, projectID int64, pipelineID string, w io.Writer) (int64, error) {
	if !pipelineid.Valid(pipelineID) {
		return 0, invalid("pipeline id %q is not one devpad gives", pipelineID)
	}
	c, err := s.readyRepoAgent(ctx, userID, projectID)
	if err != nil {
		return 0, err
	}
	return c.RepoPipelineBundle(ctx, pipelineID, w)
}

// DeletePipelineRef deletes what a pipeline pinned, if anything.
func (s *service) DeletePipelineRef(ctx context.Context, userID, projectID int64, pipelineID string) error {
	if !pipelineid.Valid(pipelineID) {
		return invalid("pipeline id %q is not one devpad gives", pipelineID)
	}
	c, err := s.readyRepoAgent(ctx, userID, projectID)
	if err != nil {
		return err
	}
	return c.RepoDeletePipelineRef(ctx, pipelineID)
}

// readyRepoAgent is the repo agent of a ready project of the user's.
func (s *service) readyRepoAgent(ctx context.Context, userID, projectID int64) (*agent.Client, error) {
	p, err := s.getReady(ctx, userID, projectID)
	if err != nil {
		return nil, err
	}
	return s.repoAgent(ctx, p)
}
