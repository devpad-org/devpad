package pipeline

import (
	"context"
	"strings"
	"testing"
)

// The merge gate is a rule of the merge request's status alone: each way a
// merge request can stand gives one state, whether it lets the merge
// through, and whether a result is still to come by itself.
func TestGateStates(t *testing.T) {
	key := MergeRequestKey{MergeRequestID: 7, HeadSHA: sha(30), TargetSHA: sha(2), ConfigHash: "jobs"}
	pipelineOf := func(id int64, status Status, jobs ...*JobRun) *Pipeline {
		return &Pipeline{ID: id, Kind: KindMergeRequest, MergeRequestID: 7, HeadSHA: sha(30), TargetSHA: sha(2), Status: status, Jobs: jobs}
	}
	// guarded is a merge request into a ticked branch with a pipeline file.
	guarded := func(change func(st *MergeRequestStatus)) *MergeRequestStatus {
		st := &MergeRequestStatus{Open: true, Target: "develop", Key: key, Source: SourceFile, Automatic: true}
		if change != nil {
			change(st)
		}
		return st
	}
	tests := []struct {
		name  string
		st    *MergeRequestStatus
		state GateState
		clear bool
		waits bool
		// reason is in the gate's reason; empty means it has none.
		reason string
		// pipeline is the pipeline the gate names, 0 for none.
		pipeline int64
	}{
		{name: "not ticked", st: guarded(func(st *MergeRequestStatus) {
			st.Automatic = false
			st.Current = pipelineOf(3, StatusFailed)
		}), state: GateUnguarded, clear: true},
		{name: "no pipeline configured", st: guarded(func(st *MergeRequestStatus) { st.Source = SourceNone }), state: GateUnguarded, clear: true},
		{name: "passed", st: guarded(func(st *MergeRequestStatus) { st.Current = pipelineOf(3, StatusPassed) }), state: GatePassed, clear: true, pipeline: 3},
		{name: "queued", st: guarded(func(st *MergeRequestStatus) { st.Current = pipelineOf(3, StatusQueued) }), state: GateQueued, waits: true, reason: "pipeline #3 is still queued", pipeline: 3},
		{name: "running", st: guarded(func(st *MergeRequestStatus) { st.Current = pipelineOf(3, StatusRunning) }), state: GateRunning, waits: true, reason: "pipeline #3 is still running", pipeline: 3},
		{name: "failed", st: guarded(func(st *MergeRequestStatus) {
			st.Current = pipelineOf(3, StatusFailed, jobNamed("backend", JobFailed), jobNamed("lint", JobPassed), jobNamed("e2e", JobTimedOut))
		}), state: GateFailed, reason: "pipeline #3 failed (backend, e2e)", pipeline: 3},
		{name: "doesn't merge", st: guarded(func(st *MergeRequestStatus) { st.Current = pipelineOf(3, StatusConflict) }), state: GateConflict, reason: "doesn't merge onto develop", pipeline: 3},
		// A file that doesn't parse can never pass, whatever ran before on
		// the same hash.
		{name: "invalid", st: guarded(func(st *MergeRequestStatus) {
			st.Invalid = "line 2: unknown key \"script\"."
			st.Current = pipelineOf(3, StatusNotConfigured)
		}), state: GateInvalid, reason: `develop's pipeline doesn't parse: line 2: unknown key "script"`, pipeline: 3},
		{name: "invalid, not ticked", st: guarded(func(st *MergeRequestStatus) {
			st.Invalid = "line 2: unknown key"
			st.Automatic = false
		}), state: GateUnguarded, clear: true},
		{name: "not run yet", st: guarded(nil), state: GateNotRun, waits: true, reason: "no pipeline has run"},
		{name: "out of date", st: guarded(func(st *MergeRequestStatus) { st.Latest = pipelineOf(2, StatusPassed) }), state: GateOutOfDate, waits: true, reason: "#2, is out of date", pipeline: 2},
		{name: "cancelled", st: guarded(func(st *MergeRequestStatus) {
			st.Latest = pipelineOf(2, StatusPassed)
			st.Settled = &SettledPipeline{Pipeline: pipelineOf(4, StatusCancelled)}
		}), state: GateCancelled, reason: "pipeline #4 was cancelled", pipeline: 4},
		{name: "couldn't run, retrying", st: guarded(func(st *MergeRequestStatus) {
			st.Settled = &SettledPipeline{Pipeline: &Pipeline{ID: 4, Status: StatusError, Error: "the repo could not be read."}, Retrying: true}
		}), state: GateError, waits: true, reason: "pipeline #4 couldn't run: the repo could not be read; devpad runs it once more", pipeline: 4},
		{name: "couldn't run", st: guarded(func(st *MergeRequestStatus) {
			st.Settled = &SettledPipeline{Pipeline: &Pipeline{ID: 4, Status: StatusError, Jobs: []*JobRun{{Job: Job{Name: "backend"}, Status: JobError, Error: "no service redis"}}}}
		}), state: GateError, reason: "pipeline #4 couldn't run: job backend: no service redis", pipeline: 4},
		{name: "closed", st: &MergeRequestStatus{Latest: pipelineOf(2, StatusPassed)}, state: GateClosed, reason: "can no longer merge"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := tt.st.Gate()
			if g.State != tt.state || g.Clear() != tt.clear || g.Waits != tt.waits {
				t.Fatalf("gate %+v: want %s, clear %v, waits %v", g, tt.state, tt.clear, tt.waits)
			}
			if g.Guarded() != (tt.state != GateUnguarded) {
				t.Fatalf("guarded %v for %s", g.Guarded(), g.State)
			}
			if g.Overridable() != (!tt.clear && tt.state != GateClosed && tt.state != GateConflict) {
				t.Fatalf("overridable %v for %s", g.Overridable(), g.State)
			}
			if (tt.reason == "") != (g.Reason == "") || !strings.Contains(g.Reason, tt.reason) {
				t.Fatalf("reason %q, want it to say %q", g.Reason, tt.reason)
			}
			if strings.HasSuffix(g.Reason, ".") || g.Reason != strings.TrimSpace(g.Reason) {
				t.Fatalf("a reason is a clause, without a full stop: %q", g.Reason)
			}
			var named int64
			if g.Pipeline != nil {
				named = g.Pipeline.ID
			}
			if named != tt.pipeline {
				t.Fatalf("names pipeline %d, want %d", named, tt.pipeline)
			}
			if tt.state != GateClosed && (g.HeadSHA != key.HeadSHA || g.TargetSHA != key.TargetSHA) {
				t.Fatalf("gate is of %s onto %s, want the key's", g.HeadSHA, g.TargetSHA)
			}
		})
	}
}

// The gate the service reads follows the merge request's commits and jobs
// as they are now, into a ticked branch, and carries the tree a pass
// tested.
func TestGateFollowsTheMergeRequest(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	gate := func() *Gate {
		t.Helper()
		st, err := e.svc.MergeRequestStatus(ctx, e.owner, e.project, e.mr)
		if err != nil {
			t.Fatal(err)
		}
		return st.Gate()
	}
	if g := gate(); g.State != GateUnguarded || !g.Clear() {
		t.Fatalf("into a branch not ticked: %+v", g)
	}
	e.projects.mu.Lock()
	e.projects.project.PipelineBranches = []string{"develop"}
	e.projects.mu.Unlock()
	if g := gate(); g.State != GateNotRun || g.Clear() || !g.Waits || g.TargetSHA != sha(2) || g.HeadSHA != sha(30) {
		t.Fatalf("ticked, before any pipeline: %+v", g)
	}
	ran := e.wait(e.start(StartInput{MergeRequestID: e.mr}).ID)
	if g := gate(); g.State != GatePassed || !g.Clear() || g.Pipeline.ID != ran.ID || g.TargetSHA != sha(2) || ran.TestedTree == "" || g.TestedTree != ran.TestedTree {
		t.Fatalf("after a pass: %+v (pipeline tested tree %q)", g, ran.TestedTree)
	}
	// A pipeline setting leaves it passed while the target's file wins over
	// it, so the jobs are the same.
	e.projects.mu.Lock()
	e.projects.project.PipelineConfig = "jobs:\n  lint:\n    run: pass\n"
	e.projects.mu.Unlock()
	if g := gate(); g.State != GatePassed || g.Pipeline.ID != ran.ID {
		t.Fatalf("after a setting the file overrides: %+v", g)
	}
	e.projects.mu.Lock()
	e.projects.project.PipelineConfig = ""
	e.projects.mu.Unlock()
	// The target moving makes it out of date.
	e.projects.mu.Lock()
	e.projects.branches["develop"] = sha(3)
	e.projects.mu.Unlock()
	e.projects.setFile(sha(3), ".devpad/pipeline.yml", jobsYAML)
	if g := gate(); g.State != GateOutOfDate || g.Clear() || g.TargetSHA != sha(3) || g.Pipeline.ID != ran.ID {
		t.Fatalf("after the target moved: %+v", g)
	}
	// With no file and no setting at the new tip, nothing guards it.
	e.projects.mu.Lock()
	e.projects.branches["develop"] = sha(5)
	e.projects.mu.Unlock()
	if g := gate(); g.State != GateUnguarded || !g.Clear() {
		t.Fatalf("with no pipeline: %+v", g)
	}
}

func jobNamed(name string, status JobStatus) *JobRun {
	return &JobRun{Job: Job{Name: name}, Status: status}
}
