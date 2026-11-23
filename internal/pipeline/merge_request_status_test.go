package pipeline

import (
	"context"
	"errors"
	"testing"

	"github.com/devpad-org/devpad/internal/project"
)

func TestMergeRequestStatusIsOfTheCommitsAndJobsAsTheyAreNow(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	status := func() *MergeRequestStatus {
		t.Helper()
		st, err := e.svc.MergeRequestStatus(ctx, e.owner, e.project, e.mr)
		if err != nil {
			t.Fatal(err)
		}
		return st
	}

	st := status()
	if !st.Open || !st.Configured() || st.Passed() || st.Current != nil || st.Latest != nil || st.Source != SourceFile || st.Key.TargetSHA != sha(2) || st.Key.HeadSHA != sha(30) {
		t.Fatalf("before any pipeline: %+v", st)
	}

	ran := e.wait(e.start(StartInput{MergeRequestID: e.mr}).ID)
	if ran.Status != StatusPassed {
		t.Fatalf("pipeline: %s %s", ran.Status, ran.Error)
	}
	if st := status(); !st.Passed() || st.Current == nil || st.Current.ID != ran.ID || st.Latest.ID != ran.ID || st.Key.ConfigHash != ran.ConfigHash {
		t.Fatalf("after a pass: %+v", st)
	}

	// The target moving makes the pass one of older commits.
	e.projects.mu.Lock()
	e.projects.branches["develop"] = sha(3)
	e.projects.mu.Unlock()
	e.projects.setFile(sha(3), ".devpad/pipeline.yml", jobsYAML)
	if st := status(); st.Passed() || st.Current != nil || st.Latest == nil || st.Latest.ID != ran.ID || st.Key.TargetSHA != sha(3) {
		t.Fatalf("after the target moved: %+v", st)
	}

	// Other jobs at the target do too, and so does the head moving.
	e.projects.setFile(sha(4), ".devpad/pipeline.yml", "jobs:\n  lint:\n    run: pass\n")
	e.projects.mu.Lock()
	e.projects.branches["develop"] = sha(4)
	e.projects.mu.Unlock()
	if st := status(); st.Current != nil || st.Key.ConfigHash == ran.ConfigHash {
		t.Fatalf("after the jobs changed: %+v", st)
	}

	// A target with no file and a project with no setting has no pipeline.
	e.projects.mu.Lock()
	e.projects.branches["develop"] = sha(5)
	e.projects.mu.Unlock()
	if st := status(); st.Configured() || st.Passed() || st.Source != SourceNone {
		t.Fatalf("with no pipeline: %+v", st)
	}
	// One that doesn't parse says why.
	e.projects.setFile(sha(6), ".devpad/pipeline.yml", "jobs: {}\n")
	e.projects.mu.Lock()
	e.projects.branches["develop"] = sha(6)
	e.projects.mu.Unlock()
	if st := status(); st.Invalid == "" || !st.Configured() || st.Passed() {
		t.Fatalf("with an invalid pipeline: %+v", st)
	}

	// A closed merge request has no current key, only its history.
	e.mrs.mu.Lock()
	e.mrs.mrs[e.mr].Open = false
	e.mrs.mu.Unlock()
	if st := status(); st.Open || st.Passed() || st.Latest == nil || st.Latest.ID != ran.ID {
		t.Fatalf("closed: %+v", st)
	}

	if _, err := e.svc.MergeRequestStatus(ctx, e.stranger, e.project, e.mr); !errors.Is(err, project.ErrForbidden) {
		t.Fatalf("a stranger's: %v", err)
	}
	if _, err := e.svc.MergeRequestStatus(ctx, e.owner, e.project, e.mr+100); !errors.Is(err, ErrMergeRequestNotFound) {
		t.Fatalf("a missing merge request: %v", err)
	}
}
