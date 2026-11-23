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
// pipeline package never imports the merge request package, which will gate
// merges on pipelines, so the two meet here.
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
	}
}

// mergeRequestPipelines tells the pipeline service each time a merge
// request opens or its head moves, so that one into a ticked branch runs
// its pipeline. The merge request service owns the listener, and never
// imports the pipeline package, so the two meet here too.
type mergeRequestPipelines struct {
	pipelines pipeline.Service
}

func (a mergeRequestPipelines) MergeRequestHeadMoved(ctx context.Context, m mergerequest.HeadMove) {
	a.pipelines.MergeRequestMoved(ctx, pipeline.MergeRequestMove{
		UserID: m.UserID, ProjectID: m.ProjectID, MergeRequestID: m.MergeRequestID, Opened: m.Opened,
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
