package server

import (
	"context"
	"errors"
	"testing"

	"github.com/devpad-org/devpad/internal/mergerequest"
	"github.com/devpad-org/devpad/internal/pipeline"
)

// lookupMergeRequests answers Lookup; nothing else of the merge request
// service is used.
type lookupMergeRequests struct {
	mergerequest.Service
	mr  *mergerequest.MergeRequest
	err error
}

func (f lookupMergeRequests) Lookup(context.Context, int64, int64, int64) (*mergerequest.MergeRequest, error) {
	return f.mr, f.err
}

func TestPipelineMergeRequestsShowsWhatPipelinesNeed(t *testing.T) {
	ctx := context.Background()
	open := &mergerequest.MergeRequest{ID: 4, SourceBranch: "task/1", TargetBranch: "develop", HeadSHA: "abc", Status: mergerequest.StatusOpen}
	got, err := pipelineMergeRequests{lookupMergeRequests{mr: open}}.PipelineMergeRequest(ctx, 1, 2, 4)
	if err != nil || *got != (pipeline.MergeRequest{ID: 4, SourceBranch: "task/1", TargetBranch: "develop", HeadSHA: "abc", Open: true}) {
		t.Fatalf("open: %+v %v", got, err)
	}
	merged := *open
	merged.Status = mergerequest.StatusMerged
	if got, _ := (pipelineMergeRequests{lookupMergeRequests{mr: &merged}}).PipelineMergeRequest(ctx, 1, 2, 4); got.Open {
		t.Fatalf("a merged merge request is open: %+v", got)
	}
	if got, err := (pipelineMergeRequests{lookupMergeRequests{err: mergerequest.ErrNotFound}}).PipelineMergeRequest(ctx, 1, 2, 4); got != nil || err != nil {
		t.Fatalf("missing: %+v %v", got, err)
	}
	broken := errors.New("database is locked")
	if _, err := (pipelineMergeRequests{lookupMergeRequests{err: broken}}).PipelineMergeRequest(ctx, 1, 2, 4); !errors.Is(err, broken) {
		t.Fatalf("an error: %v", err)
	}
}
