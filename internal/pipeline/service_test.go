package pipeline

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/devpad-org/devpad/internal/agent"
	"github.com/devpad-org/devpad/internal/project"
)

func TestStartRefusesWhatCannotRunAPipeline(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	start := func(in StartInput) error {
		if in.UserID == 0 {
			in.UserID = e.owner
		}
		if in.ProjectID == 0 {
			in.ProjectID = e.project
		}
		in.Trigger = TriggerUser
		_, _, err := e.svc.Start(ctx, in)
		return err
	}
	closed := e.mrs.mrs[e.mr]
	e.mrs.mrs[99] = &MergeRequest{ID: 99, SourceBranch: "x", TargetBranch: "feature", HeadSHA: sha(31), Open: true}
	e.mrs.mrs[98] = &MergeRequest{ID: 98, SourceBranch: "y", TargetBranch: "develop", Open: true}
	tests := []struct {
		name string
		in   StartInput
		want error
	}{
		{"an unprotected branch", StartInput{Branch: "feature"}, ErrInvalidInput},
		{"a branch the repo lacks", StartInput{Branch: "release"}, ErrInvalidInput},
		{"both", StartInput{Branch: "main", MergeRequestID: e.mr}, ErrInvalidInput},
		{"neither", StartInput{}, ErrInvalidInput},
		{"a merge request the project lacks", StartInput{MergeRequestID: 1000}, ErrMergeRequestNotFound},
		{"a merge request into an unprotected branch", StartInput{MergeRequestID: 99}, ErrInvalidState},
		{"a merge request with no head", StartInput{MergeRequestID: 98}, ErrInvalidState},
		{"someone else's project", StartInput{UserID: e.stranger, Branch: "main"}, project.ErrForbidden},
		{"a project that does not exist", StartInput{ProjectID: 1000, Branch: "main"}, project.ErrNotFound},
	}
	e.projects.project.ProtectedBranches = []string{"develop"}
	e.projects.setBranch("release", "")
	delete(e.projects.branches, "release")
	for _, tc := range tests {
		if err := start(tc.in); !errors.Is(err, tc.want) {
			t.Errorf("%s: got %v, want %v", tc.name, err, tc.want)
		}
	}
	closed.Open = false
	if err := start(StartInput{MergeRequestID: e.mr}); !errors.Is(err, ErrInvalidState) {
		t.Errorf("a closed merge request: %v", err)
	}
	e.projects.project.Status = project.StatusCreating
	if err := start(StartInput{Branch: "main"}); !errors.Is(err, project.ErrNotReady) {
		t.Errorf("a project not ready: %v", err)
	}
	if all, _ := e.svc.repo.List(ctx, e.project, ListFilter{}); len(all) != 0 {
		t.Fatalf("recorded %d pipelines", len(all))
	}
}

func TestStartReturnsThePipelineAlreadyTestingTheSame(t *testing.T) {
	e := setup(t)
	e.projects.setFile(sha(2), ".devpad/pipeline.yml", "jobs:\n  forever:\n    run: hang\n")
	ctx := context.Background()
	in := StartInput{UserID: e.owner, ProjectID: e.project, Branch: "develop", Trigger: TriggerUser}
	first, created, err := e.svc.Start(ctx, in)
	if err != nil || !created {
		t.Fatalf("first: %v %v", created, err)
	}
	e.awaitScript("forever")
	again, created, err := e.svc.Start(ctx, in)
	if err != nil || created || again.ID != first.ID {
		t.Fatalf("expected the running pipeline back: %+v %v %v", again, created, err)
	}
	// Other jobs for the same commit are another pipeline, and the running
	// one is left to finish.
	e.projects.project.PipelineConfig = ""
	e.projects.setFile(sha(2), ".devpad/pipeline.yml", "jobs:\n  other:\n    run: pass\n")
	other, created, err := e.svc.Start(ctx, in)
	if err != nil || !created || other.ID == first.ID {
		t.Fatalf("expected a new pipeline for new jobs: %+v %v %v", other, created, err)
	}
	if got := e.wait(other.ID); got.Status != StatusPassed {
		t.Fatalf("other: %+v", got)
	}
	if got, _ := e.svc.repo.Get(ctx, first.ID); got.Status != StatusRunning {
		t.Fatalf("expected the started pipeline still running: %+v", got)
	}
}

func TestNewerCommitSupersedesAQueuedPipeline(t *testing.T) {
	e := setup(t)
	gate := make(chan struct{})
	e.projects.envGate = gate
	old := e.start(StartInput{Branch: "develop"})
	e.projects.setBranch("develop", sha(3))
	e.projects.setFile(sha(3), ".devpad/pipeline.yml", jobsYAML)
	newer := e.start(StartInput{Branch: "develop"})
	if got := e.wait(old.ID); got.Status != StatusSuperseded || got.Error != fmt.Sprintf("replaced by pipeline %d", newer.ID) || got.StartedAt != nil {
		t.Fatalf("expected the queued pipeline superseded: %+v", got)
	}
	close(gate)
	if got := e.wait(newer.ID); got.Status != StatusPassed || got.HeadSHA != sha(3) {
		t.Fatalf("newer: %+v", got)
	}
	// Another branch's pipeline is never superseded by this one's.
	e.projects.mu.Lock()
	e.projects.envGate = make(chan struct{})
	e.projects.mu.Unlock()
	mainPipeline := e.start(StartInput{Branch: "main"})
	e.projects.setBranch("develop", sha(4))
	e.projects.setFile(sha(4), ".devpad/pipeline.yml", jobsYAML)
	e.start(StartInput{Branch: "develop"})
	time.Sleep(20 * time.Millisecond)
	if got, _ := e.svc.repo.Get(context.Background(), mainPipeline.ID); got.Status != StatusQueued {
		t.Fatalf("main's pipeline: %s", got.Status)
	}
}

func TestReuseResultReturnsAFinishedPipelineOfTheSameCommits(t *testing.T) {
	e := setup(t)
	first := e.wait(e.start(StartInput{MergeRequestID: e.mr}).ID)
	reused := e.start(StartInput{MergeRequestID: e.mr, ReuseResult: true})
	if reused.ID != first.ID {
		t.Fatalf("expected the finished pipeline back, got %+v", reused)
	}
	// Run by hand, it runs again.
	again := e.start(StartInput{MergeRequestID: e.mr})
	if again.ID == first.ID {
		t.Fatalf("expected a new pipeline")
	}
	e.wait(again.ID)
	// A cancelled one has no result to reuse.
	e.projects.envGate = make(chan struct{})
	e.projects.setBranch("develop", sha(5))
	e.projects.setFile(sha(5), ".devpad/pipeline.yml", jobsYAML)
	queued := e.start(StartInput{MergeRequestID: e.mr})
	if _, err := e.svc.Cancel(context.Background(), e.owner, e.project, queued.ID); err != nil {
		t.Fatal(err)
	}
	if fresh := e.start(StartInput{MergeRequestID: e.mr, ReuseResult: true}); fresh.ID == queued.ID {
		t.Fatalf("reused a cancelled pipeline")
	}
}

func TestJobOutputIsLiveThenStored(t *testing.T) {
	e := setup(t)
	e.projects.setFile(sha(2), ".devpad/pipeline.yml", "jobs:\n  talk:\n    run: block hello\n")
	ctx := context.Background()
	p := e.start(StartInput{Branch: "develop"})
	e.awaitScript("talk")
	jobID := p.Jobs[0].ID
	texts := func(notes []Note) []string {
		var out []string
		for _, n := range notes {
			out = append(out, n.Text)
		}
		return out
	}

	// While the job runs, its output so far and devpad's notes so far, of
	// which none is in the output.
	live, err := e.svc.JobOutput(ctx, e.owner, e.project, p.ID, OutputRequest{JobID: jobID})
	if err != nil || live.Done || live.Offset != 0 || string(live.Data) != "hello\n" || live.Next != 6 {
		t.Fatalf("live: %+v %q %v", live, live.Data, err)
	}
	if notes := texts(live.Notes); len(notes) == 0 || notes[len(notes)-1] != "running the job" {
		t.Fatalf("live notes: %q", notes)
	}
	// Once it has finished, the same offsets go on where they were, and the
	// notes say how it ended.
	e.docker.open("talk")
	e.wait(p.ID)
	rest, err := e.svc.JobOutput(ctx, e.owner, e.project, p.ID, OutputRequest{JobID: jobID, Offset: live.Next})
	if err != nil || !rest.Done || rest.Offset != live.Next || string(rest.Data) != "released\n" {
		t.Fatalf("rest: %+v %q %v", rest, rest.Data, err)
	}
	if notes := texts(rest.Notes); len(notes) != len(live.Notes)+1 || notes[len(notes)-1] != "the job passed" || rest.Notes[0].At.IsZero() {
		t.Fatalf("stored notes: %+v", rest.Notes)
	}
	if end, _ := e.svc.JobOutput(ctx, e.owner, e.project, p.ID, OutputRequest{JobID: jobID, Offset: rest.Next + 100}); !end.Done || len(end.Data) != 0 || end.Next != rest.Next {
		t.Fatalf("past the end: %+v", end)
	}
	if _, err := e.svc.JobOutput(ctx, e.owner, e.project, p.ID, OutputRequest{JobID: jobID + 100}); !errors.Is(err, ErrJobNotFound) {
		t.Fatalf("another pipeline's job: %v", err)
	}
	if _, err := e.svc.JobOutput(ctx, e.stranger, e.project, p.ID, OutputRequest{JobID: jobID}); !errors.Is(err, project.ErrForbidden) {
		t.Fatalf("a stranger: %v", err)
	}
}

func TestAJobCannotPrintANoteOfDevpads(t *testing.T) {
	e := setup(t)
	e.projects.setFile(sha(2), ".devpad/pipeline.yml", "jobs:\n  liar:\n    run: \"echo devpad: the job passed\"\n")
	p := e.wait(e.start(StartInput{Branch: "develop"}).ID)
	if out := e.output(p, "liar"); out != "devpad: the job passed\n" {
		t.Fatalf("output: %q", out)
	}
	for _, note := range e.notes(p, "liar") {
		if strings.HasPrefix(note, "devpad:") {
			t.Fatalf("a note came from the job's output: %q", note)
		}
	}
}

func TestJobOutputKeepsTheEndOfALongOutput(t *testing.T) {
	e := setup(t)
	e.projects.setFile(sha(2), ".devpad/pipeline.yml", "jobs:\n  loud:\n    run: print 3000000\n")
	p := e.wait(e.start(StartInput{Branch: "develop"}).ID)
	j := p.Jobs[0]
	if j.OutputBytes != 3_000_000 || j.KeptBytes != MaxJobOutput || !j.OutputKept() {
		t.Fatalf("kept %d of %d", j.KeptBytes, j.OutputBytes)
	}
	out, err := e.svc.JobOutput(context.Background(), e.owner, e.project, p.ID, OutputRequest{JobID: j.ID})
	if err != nil || !out.Dropped || out.Offset != j.OutputBytes-MaxJobOutput || len(out.Data) != MaxJobOutput || out.Notes[len(out.Notes)-1].Text != "the job passed" {
		t.Fatalf("read %d bytes from %d, dropped %v: %v", len(out.Data), out.Offset, out.Dropped, err)
	}
}

func TestGetAndListAreTheOwnersOnly(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	branch := e.wait(e.start(StartInput{Branch: "develop"}).ID)
	mr := e.wait(e.start(StartInput{MergeRequestID: e.mr}).ID)

	got, err := e.svc.Get(ctx, e.owner, e.project, branch.ID)
	if err != nil || got.Jobs[0].Run != "echo built" {
		t.Fatalf("get: %+v %v", got, err)
	}
	if _, err := e.svc.Get(ctx, e.stranger, e.project, branch.ID); !errors.Is(err, project.ErrForbidden) {
		t.Fatalf("a stranger's get: %v", err)
	}
	if _, err := e.svc.Get(ctx, e.owner, e.project, branch.ID+100); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a missing pipeline: %v", err)
	}
	all, err := e.svc.List(ctx, e.owner, e.project, ListFilter{})
	if err != nil || len(all) != 2 || all[0].ID != mr.ID || all[1].ID != branch.ID {
		t.Fatalf("list: %+v %v", all, err)
	}
	if all[0].Jobs[0].Run != "" || len(all[0].Jobs) != 2 {
		t.Fatalf("expected jobs without their scripts: %+v", all[0].Jobs[0])
	}
	if only, _ := e.svc.List(ctx, e.owner, e.project, ListFilter{Branch: "develop"}); len(only) != 1 || only[0].ID != branch.ID {
		t.Fatalf("develop's: %+v", only)
	}
	if only, _ := e.svc.List(ctx, e.owner, e.project, ListFilter{MergeRequestID: e.mr}); len(only) != 1 || only[0].ID != mr.ID {
		t.Fatalf("the merge request's: %+v", only)
	}
	if older, _ := e.svc.List(ctx, e.owner, e.project, ListFilter{Before: mr.ID, Limit: 1}); len(older) != 1 || older[0].ID != branch.ID {
		t.Fatalf("paged: %+v", older)
	}
	if _, err := e.svc.List(ctx, e.stranger, e.project, ListFilter{}); !errors.Is(err, project.ErrForbidden) {
		t.Fatalf("a stranger's list: %v", err)
	}
	if st, err := e.svc.MergeRequestStatus(ctx, e.stranger, e.project, e.mr); !errors.Is(err, project.ErrForbidden) || st != nil {
		t.Fatalf("a stranger's merge request status: %+v %v", st, err)
	}
	if _, err := e.svc.Cancel(ctx, e.stranger, e.project, branch.ID); !errors.Is(err, project.ErrForbidden) {
		t.Fatalf("a stranger's cancel: %v", err)
	}
}

func TestOldPipelinesLoseTheirOutput(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	first := e.wait(e.start(StartInput{Branch: "develop"}).ID)
	for i := 1; i < KeptOutputs; i++ {
		e.wait(e.start(StartInput{Branch: "main"}).ID)
	}
	if got, _ := e.svc.repo.Get(ctx, first.ID); !got.Jobs[0].OutputKept() {
		t.Fatalf("the 50th newest lost its output")
	}
	e.wait(e.start(StartInput{Branch: "main"}).ID)
	got, _ := e.svc.repo.Get(ctx, first.ID)
	j := got.Jobs[0]
	if j.OutputKept() || j.KeptBytes != 0 || j.OutputBytes == 0 {
		t.Fatalf("expected the oldest's output gone: %+v", j)
	}
	out, err := e.svc.JobOutput(ctx, e.owner, e.project, first.ID, OutputRequest{JobID: j.ID})
	if err != nil || !out.Done || !out.Dropped || len(out.Data) != 0 {
		t.Fatalf("read: %+v %v", out, err)
	}
}

func TestHeadMovingBackGetsANewPipelineNotTheSupersededOne(t *testing.T) {
	e := setup(t)
	e.projects.envGate = make(chan struct{})
	first := e.start(StartInput{MergeRequestID: e.mr})
	e.mrs.mrs[e.mr].HeadSHA = sha(31)
	e.start(StartInput{MergeRequestID: e.mr})
	e.mrs.mrs[e.mr].HeadSHA = sha(30)
	// The first tests these commits again, but it is being superseded,
	// whether or not it has recorded so yet.
	back := e.start(StartInput{MergeRequestID: e.mr})
	if back.ID == first.ID {
		t.Fatalf("got the superseded pipeline back")
	}
	if got := e.wait(first.ID); got.Status != StatusSuperseded {
		t.Fatalf("first: %+v", got)
	}
	close(e.projects.envGate)
	if got := e.wait(back.ID); got.Status != StatusPassed || got.HeadSHA != sha(30) {
		t.Fatalf("back: %+v", got)
	}
}

// slowReadProjects holds the first read of a commit's pipeline until release
// is closed, closing reached once it holds it.
type slowReadProjects struct {
	*fakeProjects
	commit  string
	reached chan struct{}
	release chan struct{}
	once    sync.Once
}

func (s *slowReadProjects) ReadAt(ctx context.Context, userID, projectID int64, in agent.RepoReadRequest) (*agent.RepoRead, error) {
	if in.Rev == s.commit {
		s.once.Do(func() {
			close(s.reached)
			<-s.release
		})
	}
	return s.fakeProjects.ReadAt(ctx, userID, projectID, in)
}

func TestAStartReadsItsCommitsUnderTheProjectsStartLock(t *testing.T) {
	e := setup(t)
	e.projects.envGate = make(chan struct{}) // keeps pipelines queued
	slow := &slowReadProjects{fakeProjects: e.projects, commit: sha(2), reached: make(chan struct{}), release: make(chan struct{})}
	e.svc.projects = slow
	start := func() <-chan *Pipeline {
		done := make(chan *Pipeline, 1)
		go func() {
			p, _, err := e.svc.Start(context.Background(), StartInput{UserID: e.owner, ProjectID: e.project, Branch: "develop", Trigger: TriggerUser})
			if err != nil {
				t.Errorf("start: %v", err)
			}
			done <- p
		}()
		return done
	}
	// The first start has read develop at sha(2) when the branch moves and
	// a second start reads it.
	olderDone := start()
	<-slow.reached
	e.projects.setBranch("develop", sha(3))
	e.projects.setFile(sha(3), ".devpad/pipeline.yml", jobsYAML)
	newerDone := start()
	close(slow.release)
	older, newer := <-olderDone, <-newerDone
	if older == nil || newer == nil {
		t.FailNow()
	}
	if older.HeadSHA != sha(2) || newer.HeadSHA != sha(3) {
		t.Fatalf("older tests %s, newer %s", older.HeadSHA, newer.HeadSHA)
	}
	// The branch's tip is the one left to test.
	if got := e.wait(older.ID); got.Status != StatusSuperseded {
		t.Fatalf("older: %+v", got)
	}
	if got, _ := e.svc.repo.Get(context.Background(), newer.ID); got.Status != StatusQueued {
		t.Fatalf("newer: %+v", got)
	}
	close(e.projects.envGate)
	if got := e.wait(newer.ID); got.Status != StatusPassed {
		t.Fatalf("newer: %+v", got)
	}
}

func TestReuseAndTheMergeRequestGateCountTheSamePipelines(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	// The target has no pipeline file, so the jobs are the project's setting.
	e.projects.mu.Lock()
	e.projects.files[sha(2)] = map[string]string{}
	e.projects.mu.Unlock()
	latest := func(of *Pipeline) *Pipeline {
		t.Helper()
		key := MergeRequestKey{MergeRequestID: e.mr, HeadSHA: sha(30), TargetSHA: sha(2), ConfigHash: of.ConfigHash}
		got, err := e.svc.current(ctx, key.probe(e.project, PurposeOfficial))
		if err != nil || got == nil {
			t.Fatalf("latest: %+v %v", got, err)
		}
		return got
	}
	automatic := func() *Pipeline { return e.start(StartInput{MergeRequestID: e.mr, ReuseResult: true}) }
	strictJobs, laxJobs := "jobs:\n  strict:\n    run: fail 1\n", "jobs:\n  lax:\n    run: pass\n"

	// The setting's jobs fail; other jobs pass; then the first jobs are back.
	e.projects.project.PipelineConfig = strictJobs
	strict := e.wait(automatic().ID)
	e.projects.project.PipelineConfig = laxJobs
	lax := e.wait(automatic().ID)
	e.projects.project.PipelineConfig = strictJobs
	if again := automatic(); again.ID != strict.ID {
		t.Fatalf("expected the strict jobs' result reused, got %+v", again)
	}
	// The gate sees the failure of the jobs the pipeline has, never the
	// pass of others.
	if got := latest(strict); got.ID != strict.ID || got.Status != StatusFailed {
		t.Fatalf("the strict jobs' latest: %+v", got)
	}
	if got := latest(lax); got.ID != lax.ID || got.Status != StatusPassed {
		t.Fatalf("the lax jobs' latest: %+v", got)
	}

	// A run by hand runs again, and while it runs it is the latest.
	e.projects.project.PipelineConfig = laxJobs
	e.projects.envGate = make(chan struct{})
	byHand := e.start(StartInput{MergeRequestID: e.mr})
	if byHand.ID == lax.ID {
		t.Fatal("a run by hand reused a result")
	}
	if got := latest(lax); got.ID != byHand.ID {
		t.Fatalf("expected the unfinished run latest, got %+v", got)
	}
	// Cancelled, it hides the pass from neither.
	if _, err := e.svc.Cancel(ctx, e.owner, e.project, byHand.ID); err != nil {
		t.Fatal(err)
	}
	close(e.projects.envGate)
	if got := latest(lax); got.ID != lax.ID {
		t.Fatalf("a cancelled run hid the pass: %+v", got)
	}
	if got := automatic(); got.ID != lax.ID {
		t.Fatalf("expected the pass reused after a cancelled run, got %+v", got)
	}
	// Nor does one that could not run.
	e.projects.envErr = errors.New("project gone")
	broken := e.wait(e.start(StartInput{MergeRequestID: e.mr}).ID)
	e.projects.envErr = nil
	if broken.Status != StatusError {
		t.Fatalf("expected an error: %+v", broken)
	}
	if got := latest(lax); got.ID != lax.ID {
		t.Fatalf("a run that could not run hid the pass: %+v", got)
	}
	if got := automatic(); got.ID != lax.ID {
		t.Fatalf("expected the pass reused after an error, got %+v", got)
	}
}

func TestStopStaleStopsAStartedPipelineOfOtherCommits(t *testing.T) {
	e := setup(t)
	e.projects.setFile(sha(2), ".devpad/pipeline.yml", "jobs:\n  forever:\n    run: hang\n")
	old := e.start(StartInput{MergeRequestID: e.mr})
	e.awaitScript("forever")
	e.mrs.mrs[e.mr].HeadSHA = sha(31)
	next := e.start(StartInput{MergeRequestID: e.mr, StopStale: true})
	want := fmt.Sprintf("replaced by pipeline %d", next.ID)
	got := e.wait(old.ID)
	if got.Status != StatusSuperseded || got.Error != want || got.StartedAt == nil {
		t.Fatalf("expected the started pipeline superseded: %+v", got)
	}
	if j := got.Jobs[0]; j.Status != JobCancelled || j.Error != want {
		t.Fatalf("its job: %+v", j)
	}
	e.awaitScript("forever")
	if _, err := e.svc.Cancel(context.Background(), e.owner, e.project, next.ID); err != nil {
		t.Fatal(err)
	}
}

func TestAStartWaitingForItsProjectsLockGivesUpWithItsContext(t *testing.T) {
	e := setup(t)
	e.projects.envGate = make(chan struct{}) // keeps pipelines queued
	slow := &slowReadProjects{fakeProjects: e.projects, commit: sha(2), reached: make(chan struct{}), release: make(chan struct{})}
	e.svc.projects = slow
	first := make(chan error, 1)
	go func() {
		_, _, err := e.svc.Start(context.Background(), StartInput{UserID: e.owner, ProjectID: e.project, Branch: "develop", Trigger: TriggerUser})
		first <- err
	}()
	// The first start holds the project's start lock while the repo is slow
	// to answer.
	<-slow.reached
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, _, err := e.svc.Start(ctx, StartInput{UserID: e.owner, ProjectID: e.project, Branch: "main", Trigger: TriggerUser}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("a start whose context ended went on waiting: %v", err)
	}
	// Another project's starts never wait for this one's.
	other, cancelOther := context.WithTimeout(context.Background(), time.Second)
	defer cancelOther()
	unlock, err := e.svc.lockStarts(other, e.project+1)
	if err != nil {
		t.Fatalf("another project's start lock: %v", err)
	}
	unlock()

	close(slow.release)
	if err := <-first; err != nil {
		t.Fatalf("first start: %v", err)
	}
	if all, _ := e.svc.repo.List(context.Background(), e.project, ListFilter{}); len(all) != 1 || all[0].Branch != "develop" {
		t.Fatalf("expected only the first start's pipeline: %+v", all)
	}
	close(e.projects.envGate)
}
