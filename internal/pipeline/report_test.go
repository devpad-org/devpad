package pipeline

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"
)

const failingJobsYAML = "jobs:\n  build:\n    run: fail 3\n  test:\n    run: pass\n"

// A report has the end of each failed job's output, read from the
// database, and devpad's notes on it; nothing of the jobs that passed.
func TestReportReadsTheEndOfEachFailedJob(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	e.projects.setFile(sha(2), ".devpad/pipeline.yml", failingJobsYAML)
	p := e.wait(e.start(StartInput{MergeRequestID: e.mr}).ID)
	if p.Status != StatusFailed {
		t.Fatalf("pipeline %s", p.Status)
	}
	r, err := e.svc.Report(ctx, e.owner, e.project, p.ID, 4)
	if err != nil {
		t.Fatalf("report: %v", err)
	}
	build, test := job(t, r.Pipeline, "build"), job(t, r.Pipeline, "test")
	if len(r.Logs) != 1 || r.Logs[test.ID] != nil {
		t.Fatalf("logs of %d jobs", len(r.Logs))
	}
	told := r.Logs[build.ID]
	if string(told.Output) != "oke\n" || told.Total != int64(len("it broke\n")) || len(told.Notes) == 0 || build.Run != "fail 3" {
		t.Fatalf("build's log: %q of %d, notes %v", told.Output, told.Total, told.Notes)
	}
	if r.Target == nil || r.Target.Pipeline != nil || r.Target.Pending || r.Target.Branch != "develop" {
		t.Fatalf("with no pipeline of develop, target %+v", r.Target)
	}
	if r, _ := e.svc.Report(ctx, e.owner, e.project, p.ID, 0); len(r.Logs) != 0 {
		t.Fatal("a report without output read output")
	}
	if _, err := e.svc.Report(ctx, e.stranger, e.project, p.ID, 4); err == nil {
		t.Fatal("a stranger read the report")
	}
}

// A merge request's failure is compared with the target's own pipeline of
// the same commit and jobs, which the merge request's status carries too.
func TestTargetResultOfTheSameCommit(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	e.projects.setFile(sha(2), ".devpad/pipeline.yml", failingJobsYAML)
	branch := e.wait(e.start(StartInput{Branch: "develop"}).ID)
	mrp := e.wait(e.start(StartInput{MergeRequestID: e.mr}).ID)
	r, err := e.svc.Report(ctx, e.owner, e.project, mrp.ID, 0)
	if err != nil {
		t.Fatalf("report: %v", err)
	}
	if r.Target.Pipeline == nil || r.Target.Pipeline.ID != branch.ID || !r.Target.Fails("build") || r.Target.Fails("test") || r.Target.Pending {
		t.Fatalf("target %+v", r.Target)
	}
	st, err := e.svc.MergeRequestStatus(ctx, e.owner, e.project, e.mr)
	if err != nil || st.OnTarget == nil || st.OnTarget.Pipeline.ID != branch.ID || len(st.OnTarget.Failing()) != 1 {
		t.Fatalf("status: %+v %v", st, err)
	}
}

// Only a pipeline of the very commit, with the same jobs, says whether a
// job fails on the target; one still to finish says it is still to know.
func TestTargetResultWaitsForThatCommitsPipeline(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	repo := e.svc.repo
	mrp := &Pipeline{ProjectID: e.project, Kind: KindMergeRequest, MergeRequestID: e.mr, TargetBranch: "develop", TargetSHA: sha(2), ConfigHash: "h1"}
	check := func(what string, pending bool, pipeline *Pipeline) {
		t.Helper()
		got, err := e.svc.targetResult(ctx, mrp)
		if err != nil || got.Pending != pending || (got.Pipeline == nil) != (pipeline == nil) || (pipeline != nil && got.Pipeline.ID != pipeline.ID) {
			t.Fatalf("%s: %+v %v", what, got, err)
		}
	}
	finish := func(p *Pipeline, failing string) {
		t.Helper()
		now := time.Now()
		for _, j := range p.Jobs {
			j.Status, j.FinishedAt = JobPassed, &now
			if j.Name == failing {
				j.Status = JobFailed
			}
			if err := repo.FinishJob(ctx, j, JobLog{}); err != nil {
				t.Fatal(err)
			}
		}
		p.Status, p.FinishedAt = StatusFailed, &now
		if ok, err := repo.Finish(ctx, p); !ok || err != nil {
			t.Fatalf("finish: %v %v", ok, err)
		}
	}
	check("none", false, nil)
	// An automatic run of the target still to start one says it may come.
	e.svc.auto.add(&autoRun{key: autoKey{projectID: e.project, kind: autoBranch, branch: "develop"}, userID: e.owner, trigger: TriggerMerged})
	check("a run of the target queued", true, nil)
	if r, ok := e.svc.auto.next(false); ok {
		check("a run of the target being read", true, nil)
		e.svc.auto.done(r.key)
	}
	check("a run of the target done", false, nil)
	other := e.branchPipeline("c0ffee0000000011", "develop", sha(9))
	if err := repo.Create(ctx, other); err != nil {
		t.Fatal(err)
	}
	finish(other, "a")
	check("another commit's", false, nil)
	otherJobs := e.branchPipeline("c0ffee0000000012", "develop", sha(2))
	otherJobs.ConfigHash = "h2"
	if err := repo.Create(ctx, otherJobs); err != nil {
		t.Fatal(err)
	}
	finish(otherJobs, "a")
	check("other jobs'", false, nil)
	running := e.branchPipeline("c0ffee0000000013", "develop", sha(2))
	if err := repo.Create(ctx, running); err != nil {
		t.Fatal(err)
	}
	check("still running", true, nil)
	finish(running, "a")
	check("finished", false, running)
	got, _ := e.svc.targetResult(ctx, mrp)
	if !got.Fails("a") || got.Fails("b") {
		t.Fatalf("failing %v", got.Failing())
	}
	if got, _ := e.svc.targetResult(ctx, other); got != nil {
		t.Fatalf("a branch's pipeline has no target: %+v", got)
	}
}

// A task's agent is told the jobs a merge request into a protected branch
// would run now.
func TestBranchPipelineResolvesAtTheTip(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	r, err := e.svc.BranchPipeline(ctx, e.owner, e.project, "develop")
	if err != nil || r.Config == nil || len(r.Config.Jobs) != 2 || r.Path != ".devpad/pipeline.yml" {
		t.Fatalf("develop: %+v %v", r, err)
	}
	if _, err := e.svc.BranchPipeline(ctx, e.owner, e.project, "feature"); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("an unprotected branch: %v", err)
	}
	if _, err := e.svc.BranchPipeline(ctx, e.stranger, e.project, "develop"); err == nil {
		t.Fatal("a stranger read the pipeline")
	}
}

// The merge request's status says which of its failing jobs fail on the
// target too, for the Pipeline card to mark.
func TestMergeRequestStatusSaysWhatFailsOnTheTarget(t *testing.T) {
	e := setup(t)
	e.projects.setFile(sha(2), ".devpad/pipeline.yml", failingJobsYAML)
	url := fmt.Sprintf("/api/projects/%d/merge-requests/%d/pipeline", e.project, e.mr)
	e.wait(e.start(StartInput{MergeRequestID: e.mr}).ID)
	_, out := e.serve(e.owner, http.MethodGet, url, "")
	onTarget, _ := out["status"].(map[string]any)["onTarget"].(map[string]any)
	if onTarget == nil || onTarget["branch"] != "develop" || onTarget["pipelineId"] != nil || onTarget["pending"] != false || len(onTarget["failing"].([]any)) != 0 {
		t.Fatalf("with no pipeline of develop: %v", out["status"])
	}
	branch := e.wait(e.start(StartInput{Branch: "develop"}).ID)
	_, out = e.serve(e.owner, http.MethodGet, url, "")
	onTarget, _ = out["status"].(map[string]any)["onTarget"].(map[string]any)
	if failing, _ := onTarget["failing"].([]any); onTarget["pipelineId"] != float64(branch.ID) || len(failing) != 1 || failing[0] != "build" {
		t.Fatalf("with develop's pipeline: %v", onTarget)
	}
}
