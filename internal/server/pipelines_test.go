package server

import (
	"context"
	"errors"
	"testing"

	"github.com/devpad-org/devpad/internal/mergerequest"
	"github.com/devpad-org/devpad/internal/pipeline"
)

// lookupMergeRequests answers Lookup and ListOpenInto; nothing else of the
// merge request service is used.
type lookupMergeRequests struct {
	mergerequest.Service
	mr   *mergerequest.MergeRequest
	into []*mergerequest.MergeRequest
	err  error
}

func (f lookupMergeRequests) Lookup(context.Context, int64, int64, int64) (*mergerequest.MergeRequest, error) {
	return f.mr, f.err
}

func (f lookupMergeRequests) ListOpenInto(context.Context, int64, int64, string) ([]*mergerequest.MergeRequest, error) {
	return f.into, f.err
}

// movedPipelines records what the merge requests tell the pipelines.
type movedPipelines struct {
	pipeline.Service
	moves []pipeline.MergeRequestMove
}

func (f *movedPipelines) MergeRequestMoved(_ context.Context, m pipeline.MergeRequestMove) {
	f.moves = append(f.moves, m)
}

func TestPipelineMergeRequestsListsThoseIntoABranch(t *testing.T) {
	ctx := context.Background()
	into := []*mergerequest.MergeRequest{
		{ID: 4, SourceBranch: "task/1", TargetBranch: "develop", HeadSHA: "abc", Status: mergerequest.StatusOpen},
		{ID: 6, SourceBranch: "feature", TargetBranch: "develop", HeadSHA: "def", Status: mergerequest.StatusOpen},
	}
	got, err := pipelineMergeRequests{lookupMergeRequests{into: into}}.PipelineMergeRequestsInto(ctx, 1, 2, "develop")
	if err != nil || len(got) != 2 || *got[1] != (pipeline.MergeRequest{ID: 6, SourceBranch: "feature", TargetBranch: "develop", HeadSHA: "def", Open: true}) {
		t.Fatalf("into develop: %+v %v", got, err)
	}
	broken := errors.New("database is locked")
	if _, err := (pipelineMergeRequests{lookupMergeRequests{err: broken}}).PipelineMergeRequestsInto(ctx, 1, 2, "develop"); !errors.Is(err, broken) {
		t.Fatalf("an error: %v", err)
	}
}

func TestMergeRequestPipelinesTellsOfEachHeadMove(t *testing.T) {
	ctx := context.Background()
	got := &movedPipelines{}
	a := mergeRequestPipelines{pipelines: got}
	a.MergeRequestHeadMoved(ctx, mergerequest.HeadMove{UserID: 1, ProjectID: 2, MergeRequestID: 3, Opened: true})
	a.MergeRequestHeadMoved(ctx, mergerequest.HeadMove{UserID: 1, ProjectID: 2, MergeRequestID: 3})
	want := []pipeline.MergeRequestMove{{UserID: 1, ProjectID: 2, MergeRequestID: 3, Opened: true}, {UserID: 1, ProjectID: 2, MergeRequestID: 3}}
	if len(got.moves) != 2 || got.moves[0] != want[0] || got.moves[1] != want[1] {
		t.Fatalf("told %+v", got.moves)
	}
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
