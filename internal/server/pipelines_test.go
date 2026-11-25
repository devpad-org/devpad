package server

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/devpad-org/devpad/internal/mergerequest"
	"github.com/devpad-org/devpad/internal/pipeline"
)

// lookupMergeRequests answers Lookup and ListOpenInto, all the pipelines
// ask of the merge request service.
type lookupMergeRequests struct {
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
	a := mergeRequestMoves{pipelines: got}
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

// readPipelines answers what the merge request service and the task
// service read of pipelines: MergeRequestStatus with st, Report with report,
// recording the output tails asked for, and BranchPipeline with resolved.
type readPipelines struct {
	st          *pipeline.MergeRequestStatus
	err         error
	report      *pipeline.Report
	resolved    *pipeline.Resolved
	resolvedErr error
	tails       []int
}

func (f *readPipelines) MergeRequestStatus(context.Context, int64, int64, int64) (*pipeline.MergeRequestStatus, error) {
	return f.st, f.err
}

func (f *readPipelines) Report(_ context.Context, _, _, _ int64, tail int) (*pipeline.Report, error) {
	f.tails = append(f.tails, tail)
	return f.report, nil
}

func (f *readPipelines) BranchPipeline(context.Context, int64, int64, string) (*pipeline.Resolved, error) {
	return f.resolved, f.resolvedErr
}

// The merge request service gets the pipeline package's gate as it is.
func TestMergeRequestGateIsThePipelinesGate(t *testing.T) {
	ctx := context.Background()
	failed := &pipeline.Pipeline{ID: 9, Kind: pipeline.KindMergeRequest, Status: pipeline.StatusFailed}
	st := &pipeline.MergeRequestStatus{
		Open: true, Target: "develop", Automatic: true, Source: pipeline.SourceFile, Standing: pipeline.Standing{Current: failed},
		Key: pipeline.MergeRequestKey{MergeRequestID: 4, HeadSHA: "head", TargetSHA: "tip", ConfigHash: "jobs"},
	}
	got, err := mergeRequestPipelines{&readPipelines{st: st}}.MergeGate(ctx, 1, 2, 4)
	want := mergerequest.Gate{State: mergerequest.GateFailed, Guarded: true, Overridable: true, Reason: "pipeline #9 failed", PipelineID: 9, HeadSHA: "head", TargetSHA: "tip"}
	if err != nil || *got != want {
		t.Fatalf("failed: %+v %v", got, err)
	}
	// A pass carries the tree it tested.
	failed.Status, failed.TestedTree = pipeline.StatusPassed, "tree"
	if got, _ := (mergeRequestPipelines{&readPipelines{st: st}}).MergeGate(ctx, 1, 2, 4); !got.Clear || got.TestedTree != "tree" {
		t.Fatalf("passed: %+v", got)
	}
	st.Automatic = false
	if got, _ := (mergeRequestPipelines{&readPipelines{st: st}}).MergeGate(ctx, 1, 2, 4); got.Guarded || !got.Clear || got.State != mergerequest.GateUnguarded || got.TestedTree != "" {
		t.Fatalf("not ticked: %+v", got)
	}
	broken := errors.New("the repo is down")
	if _, err := (mergeRequestPipelines{&readPipelines{err: broken}}).MergeGate(ctx, 1, 2, 4); !errors.Is(err, broken) {
		t.Fatalf("an error: %v", err)
	}
}

// Every pipeline gate state reaches the merge request service as its own
// state of the same name, which a merge over the gate records and the app
// confirms, with the pipeline's word on whether it guards, clears, waits
// and can be overridden. A state the mapping doesn't know shuts the gate
// for good, so a state added to one package and not the other can never
// let a merge through.
func TestEveryGateStateReachesTheMergeRequestService(t *testing.T) {
	var mapped []mergerequest.GateState
	for _, s := range pipeline.GateStates() {
		in := &pipeline.Gate{State: s, Reason: "why", HeadSHA: "head", TargetSHA: "tip", TestedTree: "tree", Waits: true, Pipeline: &pipeline.Pipeline{ID: 7}}
		got := mergeRequestGateOf(in)
		want := mergerequest.Gate{
			State: mergerequest.GateState(s), Guarded: in.Guarded(), Clear: in.Clear(), Waits: true, Overridable: in.Overridable(),
			Reason: "why", PipelineID: 7, HeadSHA: "head", TargetSHA: "tip", TestedTree: "tree",
		}
		if *got != want {
			t.Errorf("%s: %+v, want %+v", s, got, want)
		}
		mapped = append(mapped, got.State)
	}
	if !slices.Equal(mapped, mergerequest.GateStates()) {
		t.Errorf("the pipeline's gate states map to %v, the merge request service has %v", mapped, mergerequest.GateStates())
	}
	got := mergeRequestGateOf(&pipeline.Gate{State: "new_state", Waits: true, TestedTree: "tree"})
	if got.Clear || got.Overridable || !got.Guarded || got.Waits || got.TestedTree != "" || got.State != mergerequest.GateErrored {
		t.Errorf("an unknown state: %+v", got)
	}
}

// Every pipeline status reaches the merge request service's report as the
// gate state it stands for as a merge request's current pipeline, so the
// report and the gate compare directly; one replaced, interrupted or
// unknown stands for none, which nothing acts on.
func TestEveryPipelineStatusReachesTheReportAsAGateState(t *testing.T) {
	want := map[pipeline.Status]mergerequest.GateState{
		pipeline.StatusQueued: mergerequest.GateQueued, pipeline.StatusRunning: mergerequest.GateRunning,
		pipeline.StatusPassed: mergerequest.GatePassed, pipeline.StatusFailed: mergerequest.GateFailed,
		pipeline.StatusConflict: mergerequest.GateConflict, pipeline.StatusInvalid: mergerequest.GateInvalid,
		pipeline.StatusError: mergerequest.GateErrored, pipeline.StatusCancelled: mergerequest.GateCancelled,
		pipeline.StatusNotConfigured: mergerequest.GateUnguarded,
		pipeline.StatusSuperseded:    "", pipeline.StatusInterrupted: "",
	}
	for _, s := range pipeline.Statuses() {
		w, ok := want[s]
		if !ok {
			t.Errorf("status %s: say which gate state it stands for", s)
			continue
		}
		if got := reportGateState(s); got != w {
			t.Errorf("status %s reports %q, want %q", s, got, w)
		}
	}
	if len(want) != len(pipeline.Statuses()) {
		t.Errorf("want lists %d statuses, the pipeline package has %d", len(want), len(pipeline.Statuses()))
	}
	if got := reportGateState("new_status"); got != "" {
		t.Errorf("an unknown status reports %q", got)
	}
	// Each status a current pipeline can have gives the gate the very state
	// the report says.
	for _, s := range []pipeline.Status{pipeline.StatusQueued, pipeline.StatusRunning, pipeline.StatusPassed, pipeline.StatusFailed, pipeline.StatusConflict} {
		st := &pipeline.MergeRequestStatus{Open: true, Automatic: true, Source: pipeline.SourceFile, Standing: pipeline.Standing{Current: &pipeline.Pipeline{ID: 1, Status: s}}}
		if g := mergeRequestGateOf(st.Gate()); g.State != reportGateState(s) {
			t.Errorf("a current pipeline %s: the gate is %s, the report %s", s, g.State, reportGateState(s))
		}
	}
}

// endedMergeRequests records what the pipelines tell the merge requests.
type endedMergeRequests struct {
	ends     []mergerequest.PipelineEnd
	branches []mergerequest.BranchPipelineEnd
}

func (f *endedMergeRequests) PipelineFinished(_ context.Context, m mergerequest.PipelineEnd) {
	f.ends = append(f.ends, m)
}

func (f *endedMergeRequests) BranchPipelineFinished(_ context.Context, m mergerequest.BranchPipelineEnd) {
	f.branches = append(f.branches, m)
}

// Each official pipeline that finishes is told to the merge request
// service: a merge request's as its own, a branch's for the merge requests
// into it. One a change proposes never is, however it ended, so it sends
// no task back, starts no review and decides no merge.
func TestMergeRequestPipelineEndsTellsOfEveryOfficialPipeline(t *testing.T) {
	ctx := context.Background()
	got := &endedMergeRequests{}
	a := mergeRequestPipelineEnds{mrs: got}
	a.PipelineFinished(ctx, &pipeline.Pipeline{ID: 1, UserID: 1, ProjectID: 2, Kind: pipeline.KindBranch, Purpose: pipeline.PurposeOfficial, Branch: "develop"})
	a.PipelineFinished(ctx, &pipeline.Pipeline{ID: 2, UserID: 1, ProjectID: 2, Kind: pipeline.KindMergeRequest, Purpose: pipeline.PurposeOfficial, MergeRequestID: 5, Status: pipeline.StatusFailed})
	for _, status := range []pipeline.Status{pipeline.StatusFailed, pipeline.StatusConflict, pipeline.StatusPassed, pipeline.StatusError} {
		a.PipelineFinished(ctx, &pipeline.Pipeline{ID: 3, UserID: 1, ProjectID: 2, Kind: pipeline.KindMergeRequest, Purpose: pipeline.PurposeProposed, MergeRequestID: 5, Status: status})
	}
	if len(got.ends) != 1 || got.ends[0] != (mergerequest.PipelineEnd{UserID: 1, ProjectID: 2, MergeRequestID: 5, PipelineID: 2}) {
		t.Fatalf("told %+v", got.ends)
	}
	if len(got.branches) != 1 || got.branches[0] != (mergerequest.BranchPipelineEnd{UserID: 1, ProjectID: 2, Branch: "develop", PipelineID: 1}) {
		t.Fatalf("told %+v", got.branches)
	}
}

// The look-closely mark reads the scripts of the target's own jobs, and
// none of a branch that runs no pipeline.
func TestMergeRequestJobScriptsAreTheTargetsJobs(t *testing.T) {
	ctx := context.Background()
	f := &readPipelines{resolved: &pipeline.Resolved{Source: pipeline.SourceFile, Config: &pipeline.Config{Jobs: []pipeline.Job{{Name: "a", Run: "./scripts/a.sh"}, {Name: "b", Run: "make b"}}}}}
	got, err := mergeRequestPipelines{f}.JobScripts(ctx, 1, 2, "main")
	if err != nil || !slices.Equal(got, []string{"./scripts/a.sh", "make b"}) {
		t.Fatalf("scripts %v %v", got, err)
	}
	f.resolved = &pipeline.Resolved{Source: pipeline.SourceFile, Invalid: errors.New("line 2: unknown key")}
	if got, err := (mergeRequestPipelines{f}).JobScripts(ctx, 1, 2, "main"); got != nil || err != nil {
		t.Fatalf("invalid: %v %v", got, err)
	}
	f.resolved, f.resolvedErr = nil, fmt.Errorf("%w: main is not a protected branch", pipeline.ErrInvalidInput)
	if got, err := (mergeRequestPipelines{f}).JobScripts(ctx, 1, 2, "main"); got != nil || err != nil {
		t.Fatalf("unprotected: %v %v", got, err)
	}
}

// The merge request service gets every job's status and time, and of each
// that failed its script, devpad's word on it, the end of its output and
// whether it fails on the target too.
func TestMergeRequestPipelineReportsAreThePipelinesReport(t *testing.T) {
	start := time.Now()
	end := start.Add(90 * time.Second)
	code := 1
	failed := &pipeline.JobRun{ID: 1, Job: pipeline.Job{Name: "backend", Run: "go test ./..."}, Status: pipeline.JobFailed, ExitCode: &code, Error: "the job exited with 1", OutputBytes: 5000, StartedAt: &start, FinishedAt: &end}
	timedOut := &pipeline.JobRun{ID: 2, Job: pipeline.Job{Name: "lint"}, Status: pipeline.JobTimedOut}
	passed := &pipeline.JobRun{ID: 3, Job: pipeline.Job{Name: "frontend", Run: "npm test"}, Status: pipeline.JobPassed, StartedAt: &start, FinishedAt: &end}
	target := &pipeline.Pipeline{ID: 8, Jobs: []*pipeline.JobRun{{Job: pipeline.Job{Name: "lint"}, Status: pipeline.JobFailed}}}
	f := &readPipelines{report: &pipeline.Report{
		Pipeline: &pipeline.Pipeline{ID: 9, Kind: pipeline.KindMergeRequest, Status: pipeline.StatusFailed, HeadSHA: "head", TargetSHA: "tip", ConfigSource: pipeline.SourceFile, ConfigPath: ".devpad/pipeline.yml", Jobs: []*pipeline.JobRun{failed, timedOut, passed}},
		Logs:     map[int64]*pipeline.JobLog{1: {Output: []byte("FAIL\n"), Total: 5000, Notes: []pipeline.Note{{Text: "the job exited with 1"}}}},
		Target:   &pipeline.TargetResult{Branch: "develop", Pipeline: target},
	}}
	got, err := mergeRequestPipelines{f}.PipelineReport(context.Background(), 1, 2, 9, 4096)
	if err != nil || len(f.tails) != 1 || f.tails[0] != 4096 {
		t.Fatalf("report: %v, tails %v", err, f.tails)
	}
	if got.PipelineID != 9 || got.State != mergerequest.GateFailed || got.HeadSHA != "head" || got.TargetSHA != "tip" || got.ConfigPath != ".devpad/pipeline.yml" || got.TargetPending {
		t.Fatalf("report %+v", got)
	}
	if len(got.Jobs) != 3 || got.Jobs[0].Duration != 90*time.Second || got.Jobs[2].Status != "passed" || !got.Jobs[1].AlsoFailingOnTarget {
		t.Fatalf("jobs %+v", got.Jobs)
	}
	b := got.Failed[0]
	if len(got.Failed) != 2 || b.Output != "FAIL\n" || b.OutputBytes != 5000 || b.Run != "go test ./..." || b.Error != "the job exited with 1" || len(b.Notes) != 1 || b.AlsoFailingOnTarget || !got.Failed[1].AlsoFailingOnTarget {
		t.Fatalf("failed %+v", got.Failed)
	}
	f.report.Pipeline.ConfigSource, f.report.Target = pipeline.SourceProject, &pipeline.TargetResult{Pending: true}
	if got, _ := (mergeRequestPipelines{f}).PipelineReport(context.Background(), 1, 2, 9, 0); got.ConfigPath != "" || !got.TargetPending {
		t.Fatalf("from the setting, the target pending: %+v", got)
	}
	f.report.Pipeline.Status, f.report.Pipeline.ConflictPaths, f.report.Pipeline.ConflictMore = pipeline.StatusConflict, []string{"a.go"}, 4
	if got, _ := (mergeRequestPipelines{f}).PipelineReport(context.Background(), 1, 2, 9, 0); len(got.ConflictPaths) != 1 || got.ConflictMore != 4 {
		t.Fatalf("a conflict: %+v", got)
	}
}

// A task's agent gets the jobs of the pipeline at the tip of the branch its
// merge request merges into, and none when it has none or it doesn't parse.
func TestTaskPipelinesAreTheTargetsJobs(t *testing.T) {
	ctx := context.Background()
	f := &readPipelines{resolved: &pipeline.Resolved{Source: pipeline.SourceFile, Path: ".devpad/pipeline.yml", Config: &pipeline.Config{Jobs: []pipeline.Job{{Name: "backend", Run: "go test ./...", Services: []string{"postgres"}, Timeout: 15 * time.Minute}}}}}
	got, err := taskPipelines{f}.PipelineJobs(ctx, 1, 2, "main")
	if err != nil || got.Path != ".devpad/pipeline.yml" || len(got.Jobs) != 1 || got.Jobs[0].Name != "backend" || got.Jobs[0].Services[0] != "postgres" || got.Jobs[0].Timeout != 15*time.Minute {
		t.Fatalf("jobs %+v %v", got, err)
	}
	f.resolved = &pipeline.Resolved{Source: pipeline.SourceFile, Invalid: errors.New("line 2: unknown key")}
	if got, err := (taskPipelines{f}).PipelineJobs(ctx, 1, 2, "main"); got != nil || err != nil {
		t.Fatalf("invalid: %+v %v", got, err)
	}
}
