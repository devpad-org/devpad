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
	ends  []pipeline.MergeRequestEnd
}

func (f *movedPipelines) MergeRequestMoved(_ context.Context, m pipeline.MergeRequestMove) {
	f.moves = append(f.moves, m)
}

func (f *movedPipelines) MergeRequestEnded(_ context.Context, m pipeline.MergeRequestEnd) {
	f.ends = append(f.ends, m)
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
	a.MergeRequestEnded(ctx, mergerequest.End{UserID: 1, ProjectID: 2, MergeRequestID: 3, Merged: true})
	if len(got.ends) != 1 || got.ends[0] != (pipeline.MergeRequestEnd{UserID: 1, ProjectID: 2, MergeRequestID: 3, Merged: true}) {
		t.Fatalf("told %+v", got.ends)
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
	if got, _ := (pipelineMergeRequests{lookupMergeRequests{mr: &merged}}).PipelineMergeRequest(ctx, 1, 2, 4); got.Open || !got.Merged {
		t.Fatalf("a merged merge request: %+v", got)
	}
	if got, err := (pipelineMergeRequests{lookupMergeRequests{err: mergerequest.ErrNotFound}}).PipelineMergeRequest(ctx, 1, 2, 4); got != nil || err != nil {
		t.Fatalf("missing: %+v %v", got, err)
	}
	broken := errors.New("database is locked")
	if _, err := (pipelineMergeRequests{lookupMergeRequests{err: broken}}).PipelineMergeRequest(ctx, 1, 2, 4); !errors.Is(err, broken) {
		t.Fatalf("an error: %v", err)
	}
}

// statusPipelines answers MergeRequestStatus with st.
type statusPipelines struct {
	pipeline.Service
	st  *pipeline.MergeRequestStatus
	err error
}

func (f statusPipelines) MergeRequestStatus(context.Context, int64, int64, int64) (*pipeline.MergeRequestStatus, error) {
	return f.st, f.err
}

// The merge request service gets the pipeline package's gate as it is.
func TestMergeRequestGateIsThePipelinesGate(t *testing.T) {
	ctx := context.Background()
	failed := &pipeline.Pipeline{ID: 9, Kind: pipeline.KindMergeRequest, Status: pipeline.StatusFailed}
	st := &pipeline.MergeRequestStatus{
		Open: true, Target: "develop", Automatic: true, Source: pipeline.SourceFile, Current: failed,
		Key: pipeline.MergeRequestKey{MergeRequestID: 4, HeadSHA: "head", TargetSHA: "tip", ConfigHash: "jobs"},
	}
	got, err := mergeRequestGate{statusPipelines{st: st}}.MergeGate(ctx, 1, 2, 4)
	want := mergerequest.Gate{State: "failed", Guarded: true, Overridable: true, Reason: "pipeline #9 failed", PipelineID: 9, HeadSHA: "head", TargetSHA: "tip"}
	if err != nil || *got != want {
		t.Fatalf("failed: %+v %v", got, err)
	}
	// A pass carries the tree it tested.
	failed.Status, failed.TestedTree = pipeline.StatusPassed, "tree"
	if got, _ := (mergeRequestGate{statusPipelines{st: st}}).MergeGate(ctx, 1, 2, 4); !got.Clear || got.TestedTree != "tree" {
		t.Fatalf("passed: %+v", got)
	}
	st.Automatic = false
	if got, _ := (mergeRequestGate{statusPipelines{st: st}}).MergeGate(ctx, 1, 2, 4); got.Guarded || !got.Clear || got.State != "unguarded" || got.TestedTree != "" {
		t.Fatalf("not ticked: %+v", got)
	}
	broken := errors.New("the repo is down")
	if _, err := (mergeRequestGate{statusPipelines{err: broken}}).MergeGate(ctx, 1, 2, 4); !errors.Is(err, broken) {
		t.Fatalf("an error: %v", err)
	}
}

// endedMergeRequests records what the pipelines tell the merge requests.
type endedMergeRequests struct {
	mergerequest.Service
	ends []mergerequest.PipelineEnd
}

func (f *endedMergeRequests) PipelineFinished(_ context.Context, m mergerequest.PipelineEnd) {
	f.ends = append(f.ends, m)
}

// Each merge request pipeline that finishes is told to the merge request
// service, and no branch's.
func TestMergeRequestPipelineEndsTellsOfMergeRequestPipelines(t *testing.T) {
	ctx := context.Background()
	got := &endedMergeRequests{}
	a := mergeRequestPipelineEnds{mrs: got}
	a.PipelineFinished(ctx, &pipeline.Pipeline{ID: 1, UserID: 1, ProjectID: 2, Kind: pipeline.KindBranch, Branch: "develop"})
	a.PipelineFinished(ctx, &pipeline.Pipeline{ID: 2, UserID: 1, ProjectID: 2, Kind: pipeline.KindMergeRequest, MergeRequestID: 5})
	if len(got.ends) != 1 || got.ends[0] != (mergerequest.PipelineEnd{UserID: 1, ProjectID: 2, MergeRequestID: 5}) {
		t.Fatalf("told %+v", got.ends)
	}
}
