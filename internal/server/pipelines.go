package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"log"
	"os"
	"path/filepath"

	"github.com/devpad-org/devpad/internal/mergerequest"
	"github.com/devpad-org/devpad/internal/pipeline"
)

// pipelineMergeRequests shows pipelines the merge requests they test. The
// pipeline package never imports the merge request package, which gates
// merges on pipelines, and the merge request package never imports the
// pipeline package, so the two meet here.
type pipelineMergeRequests struct {
	mrs mergerequest.Service
}

func (a pipelineMergeRequests) PipelineMergeRequest(ctx context.Context, userID, projectID, mrID int64) (*pipeline.MergeRequest, error) {
	mr, err := a.mrs.Lookup(ctx, userID, projectID, mrID)
	if errors.Is(err, mergerequest.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return pipelineMergeRequest(mr), nil
}

func (a pipelineMergeRequests) PipelineMergeRequestsInto(ctx context.Context, userID, projectID int64, target string) ([]*pipeline.MergeRequest, error) {
	mrs, err := a.mrs.ListOpenInto(ctx, userID, projectID, target)
	if err != nil {
		return nil, err
	}
	out := make([]*pipeline.MergeRequest, 0, len(mrs))
	for _, mr := range mrs {
		out = append(out, pipelineMergeRequest(mr))
	}
	return out, nil
}

func pipelineMergeRequest(mr *mergerequest.MergeRequest) *pipeline.MergeRequest {
	return &pipeline.MergeRequest{
		ID:           mr.ID,
		SourceBranch: mr.SourceBranch,
		TargetBranch: mr.TargetBranch,
		HeadSHA:      mr.HeadSHA,
		Open:         mr.Status == mergerequest.StatusOpen,
		Merged:       mr.Status == mergerequest.StatusMerged,
	}
}

// mergeRequestPipelines tells the pipeline service each time a merge
// request opens or its head moves, so that one into a ticked branch runs
// its pipeline, and when it closes or merges, so that its pipelines stop.
// The merge request service owns the listener, and never imports the
// pipeline package, so the two meet here too.
type mergeRequestPipelines struct {
	pipelines pipeline.Service
}

func (a mergeRequestPipelines) MergeRequestHeadMoved(ctx context.Context, m mergerequest.HeadMove) {
	a.pipelines.MergeRequestMoved(ctx, pipeline.MergeRequestMove{
		UserID: m.UserID, ProjectID: m.ProjectID, MergeRequestID: m.MergeRequestID, Opened: m.Opened,
	})
}

func (a mergeRequestPipelines) MergeRequestEnded(ctx context.Context, m mergerequest.End) {
	a.pipelines.MergeRequestEnded(ctx, pipeline.MergeRequestEnd{
		UserID: m.UserID, ProjectID: m.ProjectID, MergeRequestID: m.MergeRequestID, Merged: m.Merged,
	})
}

// mergeRequestGate is the merge gate every merge asks first: the pipeline
// service's MergeRequestStatus.Gate, the one rule for whether a merge
// request's pipeline lets it merge. It only reads, so the merge request
// service asks it under the merge request's lock.
type mergeRequestGate struct {
	pipelines pipeline.Service
}

func (a mergeRequestGate) MergeGate(ctx context.Context, userID, projectID, mrID int64) (*mergerequest.Gate, error) {
	st, err := a.pipelines.MergeRequestStatus(ctx, userID, projectID, mrID)
	if err != nil {
		return nil, err
	}
	g := st.Gate()
	out := &mergerequest.Gate{
		State: string(g.State), Guarded: g.Guarded(), Clear: g.Clear(), Waits: g.Waits, Overridable: g.Overridable(),
		Reason: g.Reason, HeadSHA: g.HeadSHA, TargetSHA: g.TargetSHA, TestedTree: g.TestedTree,
	}
	if g.Pipeline != nil {
		out.PipelineID = g.Pipeline.ID
	}
	return out, nil
}

// mergeRequestPipelineEnds tells the merge request service each time one
// of a merge request's pipelines finishes, so that an automatic merge
// waiting for it decides again. The pipeline service tells it holding none
// of its locks; the merge request service takes its own.
type mergeRequestPipelineEnds struct {
	mrs mergerequest.Service
}

func (a mergeRequestPipelineEnds) PipelineFinished(ctx context.Context, p *pipeline.Pipeline) {
	if p.Kind != pipeline.KindMergeRequest {
		return
	}
	a.mrs.PipelineFinished(ctx, mergerequest.PipelineEnd{
		UserID: p.UserID, ProjectID: p.ProjectID, MergeRequestID: p.MergeRequestID, PipelineID: p.ID, Status: string(p.Status),
	})
}

// pipelineBundleDir is where this devpad keeps its pipelines' bundles: a
// directory of the user's own, named by the database, so that the same
// devpad finds it again after a restart and removes what it left there, and
// no other devpad uses it. Empty, when there is none, means the system's
// temporary directory, where nothing left is removed.
func pipelineBundleDir(dbPath string) string {
	cache, err := os.UserCacheDir()
	if err != nil {
		log.Printf("pipelines keep their bundles in the system's temporary directory: %v", err)
		return ""
	}
	db, err := filepath.Abs(dbPath)
	if err != nil {
		log.Printf("pipelines keep their bundles in the system's temporary directory: %v", err)
		return ""
	}
	sum := sha256.Sum256([]byte(db))
	dir := filepath.Join(cache, "devpad", "pipeline-bundles-"+hex.EncodeToString(sum[:8]))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		log.Printf("pipelines keep their bundles in the system's temporary directory: %v", err)
		return ""
	}
	return dir
}
