package pipeline

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/devpad-org/devpad/internal/agent"
	"github.com/devpad-org/devpad/internal/container"
	"github.com/devpad-org/devpad/internal/pipelineid"
	"github.com/devpad-org/devpad/internal/project"
)

func TestBranchPipelineRunsItsJobsAndCleansUp(t *testing.T) {
	e := setup(t)
	e.projects.env = &project.Environment{Image: "devpad-project-1:abc"}
	p := e.start(StartInput{Branch: "develop"})
	if p.Status != StatusQueued || p.Kind != KindBranch || p.HeadSHA != sha(2) || p.TargetSHA != "" || p.ConfigSource != SourceFile || p.ConfigPath != ".devpad/pipeline.yml" || len(p.Jobs) != 2 {
		t.Fatalf("unexpected pipeline: %+v", p)
	}
	if !pipelineid.Valid(p.UID) || len(p.UID) != 16 {
		t.Fatalf("UID %q is not one Docker and git can carry", p.UID)
	}

	got := e.wait(p.ID)
	if got.Status != StatusPassed || got.Error != "" || got.TestedSHA != sha(2) || got.Image != "devpad-project-1:abc" || got.StartedAt == nil || got.FinishedAt == nil {
		t.Fatalf("unexpected result: %+v", got)
	}
	for _, j := range got.Jobs {
		if j.Status != JobPassed || j.ExitCode == nil || *j.ExitCode != 0 || j.StartedAt == nil || j.FinishedAt == nil || j.OutputBytes == 0 || !j.OutputKept() {
			t.Errorf("job %s: %+v", j.Name, j)
		}
	}
	if out := e.output(got, "build"); !strings.Contains(out, "built\n") || !strings.Contains(out, "devpad: cloning the commit under test") || !strings.HasSuffix(out, "devpad: the job passed\n") {
		t.Errorf("build's output:\n%s", out)
	}

	// The commit itself was pinned, bundled once, and given to each job;
	// then the pin, the bundle and everything in Docker went.
	if len(e.projects.pins) != 1 || e.projects.pins[0] != (agent.PipelinePin{ID: p.UID, Commit: sha(2)}) {
		t.Errorf("pins: %+v", e.projects.pins)
	}
	if !slices.Equal(e.projects.deleted, []string{p.UID}) || len(e.projects.pinned()) != 0 {
		t.Errorf("expected the ref deleted, got deleted %v pinned %v", e.projects.deleted, e.projects.pinned())
	}
	if files := e.tempFiles(); len(files) != 0 {
		t.Errorf("bundle left behind: %v", files)
	}
	if left := e.docker.left(""); len(left) != 0 {
		t.Errorf("left in Docker: %+v", left)
	}
	if e.docker.maxRunning > 2 {
		t.Errorf("%d scripts ran at once", e.docker.maxRunning)
	}
	var reloaded *Pipeline
	select {
	case reloaded = <-e.listener.finished:
	case <-time.After(5 * time.Second):
		t.Fatal("the listener was never told")
	}
	if reloaded.ID != p.ID || reloaded.Status != StatusPassed {
		t.Errorf("listener told of %+v", reloaded)
	}
	if ref, _ := e.svc.repo.Get(context.Background(), p.ID); ref.RefPinned {
		t.Errorf("expected the ref recorded as deleted")
	}
}

func TestJobContainerGetsTheBundleImageAndCloneCommands(t *testing.T) {
	e := setup(t)
	p := e.wait(e.start(StartInput{Branch: "main"}).ID)
	if p.Status != StatusPassed || p.Image != container.WorkspaceImage {
		t.Fatalf("unexpected result: %+v", p)
	}
	// Every clone command is a command of its own, naming only devpad's
	// ref: nothing of the repo's reaches a shell.
	want := [][]string{
		{"git", "init", "-q", "-b", "main"},
		{"git", "fetch", "-q", "--no-tags", "/tmp/pipeline.bundle", "refs/devpad/pipelines/" + p.UID},
		{"git", "checkout", "-q", "--detach", "FETCH_HEAD"},
	}
	if len(e.docker.clones) != 2*len(want) {
		t.Fatalf("clone commands: %v", e.docker.clones)
	}
	for i, cmd := range e.docker.clones {
		if !slices.Equal(cmd, want[i%len(want)]) {
			t.Errorf("clone command %d: %v, want %v", i, cmd, want[i%len(want)])
		}
	}
}

func TestFailingJobFailsThePipelineAndTheOthersStillRun(t *testing.T) {
	e := setup(t)
	e.projects.setFile(sha(2), ".devpad/pipeline.yml", "jobs:\n  lint:\n    run: fail 3\n  test:\n    run: pass\n  oom:\n    run: oom\n")
	p := e.wait(e.start(StartInput{Branch: "develop"}).ID)
	if p.Status != StatusFailed || p.Error != "" {
		t.Fatalf("expected failed, got %+v", p)
	}
	if j := job(t, p, "lint"); j.Status != JobFailed || *j.ExitCode != 3 || j.Error != "the job exited with 3" {
		t.Errorf("lint: %+v", j)
	}
	if j := job(t, p, "test"); j.Status != JobPassed {
		t.Errorf("test: %+v", j)
	}
	if j := job(t, p, "oom"); j.Status != JobOutOfMemory || *j.ExitCode != 137 || !strings.Contains(j.Error, "ran out of memory") {
		t.Errorf("oom: %+v", j)
	}
	if out := e.output(p, "lint"); !strings.Contains(out, "it broke\n") || !strings.HasSuffix(out, "devpad: the job exited with 3\n") {
		t.Errorf("lint's output:\n%s", out)
	}
}

func TestJobRunningPastItsTimeoutTimesOut(t *testing.T) {
	e := setup(t)
	e.projects.setFile(sha(2), ".devpad/pipeline.yml", "jobs:\n  slow:\n    run: hang\n    timeout: 1s\n")
	p := e.wait(e.start(StartInput{Branch: "develop"}).ID)
	j := job(t, p, "slow")
	if p.Status != StatusFailed || j.Status != JobTimedOut || j.ExitCode != nil || !strings.Contains(j.Error, "timeout of 1s") {
		t.Fatalf("expected a timeout: %+v %+v", p, j)
	}
	if left := e.docker.left(p.UID); len(left) != 0 {
		t.Errorf("left in Docker: %+v", left)
	}
}

func TestCancelStopsARunningJob(t *testing.T) {
	e := setup(t)
	e.projects.setFile(sha(2), ".devpad/pipeline.yml", "jobs:\n  forever:\n    run: hang\n  later:\n    run: pass\n")
	e.svc.slots = newJobSlots(1)
	p := e.start(StartInput{Branch: "develop"})
	e.awaitScript("forever")

	got, err := e.svc.Cancel(context.Background(), e.owner, e.project, p.ID)
	if err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if got.Status != StatusCancelled || got.Error != "" {
		t.Fatalf("expected cancelled, got %+v", got)
	}
	if j := job(t, got, "forever"); j.Status != JobCancelled || !strings.Contains(e.output(got, "forever"), "devpad: the job was stopped") {
		t.Errorf("forever: %+v", j)
	}
	// The job waiting for the only slot never ran.
	if j := job(t, got, "later"); j.Status != JobSkipped || j.StartedAt != nil {
		t.Errorf("later: %+v", j)
	}
	e.waitIdle(p.ID)
	if left := e.docker.left(""); len(left) != 0 {
		t.Errorf("left in Docker: %+v", left)
	}
	if len(e.projects.pinned()) != 0 || len(e.tempFiles()) != 0 {
		t.Errorf("left a pin %v or a bundle %v", e.projects.pinned(), e.tempFiles())
	}
	if _, err := e.svc.Cancel(context.Background(), e.owner, e.project, p.ID); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("cancelling again: %v", err)
	}
}

func TestCancelAQueuedPipeline(t *testing.T) {
	e := setup(t)
	e.projects.envGate = make(chan struct{})
	p := e.start(StartInput{Branch: "develop"})
	got, err := e.svc.Cancel(context.Background(), e.owner, e.project, p.ID)
	if err != nil || got.Status != StatusCancelled || got.StartedAt != nil {
		t.Fatalf("cancel: %+v %v", got, err)
	}
	for _, j := range got.Jobs {
		if j.Status != JobSkipped {
			t.Errorf("job %s: %s", j.Name, j.Status)
		}
	}
	if len(e.projects.pins) != 0 {
		t.Errorf("a queued pipeline pinned %v", e.projects.pins)
	}
}

func TestMergeRequestPipelineTestsTheMergeWithTheTargetsJobs(t *testing.T) {
	e := setup(t)
	// The source carries jobs of its own; only the target's count.
	e.projects.setFile(sha(30), ".devpad/pipeline.yml", "jobs:\n  sneaky:\n    run: pass\n")
	p := e.start(StartInput{MergeRequestID: e.mr})
	if p.Kind != KindMergeRequest || p.HeadSHA != sha(30) || p.TargetSHA != sha(2) || p.SourceBranch != "feature" || p.TargetBranch != "develop" || p.Branch != "" {
		t.Fatalf("unexpected pipeline: %+v", p)
	}
	got := e.wait(p.ID)
	if got.Status != StatusPassed || len(got.Jobs) != 2 || got.Jobs[0].Name != "build" {
		t.Fatalf("expected the target's jobs to pass: %+v", got)
	}
	if pin := e.projects.pins[0]; pin != (agent.PipelinePin{ID: p.UID, Source: sha(30), Target: sha(2)}) {
		t.Errorf("pinned %+v", pin)
	}
	if got.TestedSHA != mergeOf(sha(30), sha(2)) {
		t.Errorf("tested %s", got.TestedSHA)
	}
	for _, c := range e.docker.containers {
		t.Errorf("container left: %+v", c)
	}
	latest, err := e.svc.LatestForMergeRequest(context.Background(), e.owner, e.project, e.mr, sha(30), sha(2))
	if err != nil || latest == nil || latest.ID != p.ID {
		t.Fatalf("latest for the pair: %+v %v", latest, err)
	}
	if other, err := e.svc.LatestForMergeRequest(context.Background(), e.owner, e.project, e.mr, sha(30), sha(1)); err != nil || other != nil {
		t.Fatalf("latest for another target: %+v %v", other, err)
	}
}

func TestSourceThatDoesNotMergeIsAConflict(t *testing.T) {
	e := setup(t)
	e.projects.conflict = &agent.MergeConflict{Message: "the source conflicts with the target", Paths: []string{"cart.go"}}
	p := e.wait(e.start(StartInput{MergeRequestID: e.mr}).ID)
	if p.Status != StatusConflict || p.Error != "the source conflicts with the target" || !slices.Equal(p.ConflictPaths, []string{"cart.go"}) || p.TestedSHA != "" {
		t.Fatalf("expected a conflict: %+v", p)
	}
	for _, j := range p.Jobs {
		if j.Status != JobSkipped {
			t.Errorf("job %s: %s", j.Name, j.Status)
		}
	}
	if len(e.docker.scripts) != 0 || len(e.docker.removed) != 1 {
		t.Errorf("ran %v, removed %v", e.docker.scripts, e.docker.removed)
	}
}

func TestPipelineThatDoesNotParseFailsWithoutRunning(t *testing.T) {
	e := setup(t)
	e.projects.setFile(sha(2), ".devpad/pipeline.yml", "jobs:\n  test:\n    scrpit: make\n")
	e.projects.project.PipelineConfig = jobsYAML
	p := e.start(StartInput{Branch: "develop"})
	if p.Status != StatusInvalid || !strings.Contains(p.Error, `line 3: job test: unknown key "scrpit"`) || p.FinishedAt == nil || len(p.Jobs) != 0 {
		t.Fatalf("expected an invalid pipeline that never falls back: %+v", p)
	}
	select {
	case told := <-e.listener.finished:
		if told.ID != p.ID || told.Status != StatusInvalid {
			t.Fatalf("told of %+v", told)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the listener was never told")
	}
	if len(e.projects.pins) != 0 {
		t.Fatalf("pinned %v", e.projects.pins)
	}
}

func TestNoPipelineIsNeverAPass(t *testing.T) {
	e := setup(t)
	e.projects.setBranch("main", sha(9))
	p := e.start(StartInput{Branch: "main"})
	if p.Status != StatusNotConfigured || p.ConfigSource != SourceNone || p.Status.HasResult() != true || p.FinishedAt == nil {
		t.Fatalf("expected no pipeline configured: %+v", p)
	}

	// The project's setting runs where the commit has no file.
	e.projects.project.PipelineConfig = "jobs:\n  fallback:\n    run: pass\n"
	got := e.wait(e.start(StartInput{Branch: "main"}).ID)
	if got.Status != StatusPassed || got.ConfigSource != SourceProject || got.Jobs[0].Name != "fallback" {
		t.Fatalf("expected the setting's jobs: %+v", got)
	}
}

func TestJobNamingAServiceTheProjectLacksIsAnError(t *testing.T) {
	e := setup(t)
	e.projects.setFile(sha(2), ".devpad/pipeline.yml", "jobs:\n  db:\n    run: pass\n    services: [postgres]\n  cache:\n    run: pass\n    services: [redis]\n")
	p := e.wait(e.start(StartInput{Branch: "develop"}).ID)
	if p.Status != StatusError {
		t.Fatalf("expected an error: %+v", p)
	}
	if j := job(t, p, "db"); j.Status != JobPassed {
		t.Errorf("db: %+v", j)
	}
	j := job(t, p, "cache")
	if j.Status != JobError || j.ExitCode != nil || !strings.Contains(j.Error, "redis: the project has no such service: add it to the project's services") {
		t.Errorf("cache: %+v", j)
	}
	// The job that had its service got its connection variables.
	for _, c := range e.docker.containers {
		t.Errorf("container left: %+v", c)
	}
	if len(e.services.started) != 1 || e.services.started[0].Name != "db" || e.services.removed != 1 {
		t.Errorf("services started %v removed %d", e.services.started, e.services.removed)
	}
}

func TestJobWaitsForANetworkWhileDockerHasNoPool(t *testing.T) {
	e := setup(t)
	e.projects.setFile(sha(2), ".devpad/pipeline.yml", "jobs:\n  test:\n    run: pass\n")
	e.docker.noPool = 3
	p := e.wait(e.start(StartInput{Branch: "develop"}).ID)
	if p.Status != StatusPassed {
		t.Fatalf("expected a pass once a pool freed: %+v", p)
	}
	if out := e.output(p, "test"); strings.Count(out, "no address pool left") != 1 {
		t.Errorf("expected one line saying it waited:\n%s", out)
	}
}

func TestCloneThatFailsIsAnError(t *testing.T) {
	e := setup(t)
	e.docker.cloneFails = true
	p := e.wait(e.start(StartInput{Branch: "develop"}).ID)
	if p.Status != StatusError {
		t.Fatalf("expected an error: %+v", p)
	}
	for _, j := range p.Jobs {
		if j.Status != JobError || !strings.Contains(j.Error, "cloning the commit under test: git init exited with 128") {
			t.Errorf("job %s: %+v", j.Name, j)
		}
	}
	if len(e.docker.scripts) != 0 {
		t.Errorf("ran scripts after a failed clone: %v", e.docker.scripts)
	}
}

func TestPinThatFailsIsAnError(t *testing.T) {
	e := setup(t)
	e.projects.pinErr = errors.New("repo agent unreachable")
	p := e.wait(e.start(StartInput{Branch: "develop"}).ID)
	if p.Status != StatusError || !strings.Contains(p.Error, "pinning what the pipeline tests: repo agent unreachable") {
		t.Fatalf("expected an error: %+v", p)
	}
	// It may have pinned before failing, so the ref is deleted anyway.
	if !slices.Equal(e.projects.deleted, []string{p.UID}) || p.RefPinned {
		t.Fatalf("expected the ref deleted: %v %+v", e.projects.deleted, p)
	}
}

func TestEnvironmentThatCannotBeReadIsAnError(t *testing.T) {
	e := setup(t)
	e.projects.envErr = errors.New("project gone")
	p := e.wait(e.start(StartInput{Branch: "develop"}).ID)
	if p.Status != StatusError || !strings.Contains(p.Error, "waiting for the project's environment: project gone") || p.StartedAt != nil {
		t.Fatalf("expected an error: %+v", p)
	}
}

func TestAtMostThreeJobsOfAProjectRunAtOnceOldestPipelineFirst(t *testing.T) {
	e := setup(t)
	four := "jobs:\n  a:\n    run: block\n  b:\n    run: block\n  c:\n    run: block\n  d:\n    run: block\n"
	e.projects.setFile(sha(2), ".devpad/pipeline.yml", four)
	e.projects.setFile(sha(1), ".devpad/pipeline.yml", "jobs:\n  other:\n    run: block\n")
	first := e.start(StartInput{Branch: "develop"})
	for range 3 {
		<-e.docker.started
	}
	second := e.start(StartInput{Branch: "main"})
	// The second waits for a slot, behind the first's fourth job.
	time.Sleep(50 * time.Millisecond)
	if got, _ := e.svc.repo.Get(context.Background(), second.ID); got.Status != StatusQueued {
		t.Fatalf("second pipeline %s with no slot free", got.Status)
	}
	if n := e.svc.slots.running(e.project); n != 3 {
		t.Fatalf("%d slots in use", n)
	}
	close(e.docker.gate)
	if p := e.wait(first.ID); p.Status != StatusPassed {
		t.Fatalf("first: %+v", p)
	}
	if p := e.wait(second.ID); p.Status != StatusPassed {
		t.Fatalf("second: %+v", p)
	}
	if e.docker.maxRunning != 3 || len(e.docker.scripts) != 5 {
		t.Fatalf("%d of %d jobs ran at once", e.docker.maxRunning, len(e.docker.scripts))
	}
	// A freed slot went to the first pipeline's last job before the second
	// pipeline.
	firstDone, _ := e.svc.repo.Get(context.Background(), first.ID)
	secondDone, _ := e.svc.repo.Get(context.Background(), second.ID)
	if d := job(t, firstDone, "d").StartedAt; secondDone.StartedAt.Before(*d) {
		t.Errorf("the second pipeline started at %v, before the first's job d at %v", secondDone.StartedAt, d)
	}
}

func TestShutdownLeavesRunningPipelinesForReconcile(t *testing.T) {
	e := setup(t)
	e.projects.setFile(sha(2), ".devpad/pipeline.yml", "jobs:\n  forever:\n    run: hang\n")
	p := e.start(StartInput{Branch: "develop"})
	e.awaitScript("forever")
	e.shutdown()

	stopped, _ := e.svc.repo.Get(context.Background(), p.ID)
	if stopped.Status != StatusRunning || stopped.Jobs[0].Status != JobRunning {
		t.Fatalf("expected it left running for Reconcile: %+v %+v", stopped, stopped.Jobs[0])
	}
	if stopped.Jobs[0].OutputBytes == 0 {
		t.Errorf("expected the output it had kept")
	}
	if left := e.docker.left(""); len(left) != 0 {
		t.Errorf("left in Docker: %+v", left)
	}
	if len(e.projects.pinned()) != 0 {
		t.Errorf("left a pin: %v", e.projects.pinned())
	}
	if _, _, err := e.svc.Start(context.Background(), StartInput{UserID: e.owner, ProjectID: e.project, Branch: "main", Trigger: TriggerUser}); !errors.Is(err, ErrShuttingDown) {
		t.Fatalf("starting while shut down: %v", err)
	}

	// The next devpad interrupts it and runs the branch again.
	e.projects.setFile(sha(2), ".devpad/pipeline.yml", "jobs:\n  forever:\n    run: pass\n")
	e.svc = e.newService()
	if err := e.svc.Reconcile(context.Background()); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	interrupted, _ := e.svc.repo.Get(context.Background(), p.ID)
	if interrupted.Status != StatusInterrupted || interrupted.Error != interruptedReason || interrupted.Jobs[0].Status != JobInterrupted {
		t.Fatalf("expected interrupted: %+v %+v", interrupted, interrupted.Jobs[0])
	}
	rerun := e.awaitRerun(p.ID)
	if rerun.Trigger != TriggerRestart || rerun.Branch != "develop" || rerun.HeadSHA != sha(2) {
		t.Fatalf("unexpected rerun: %+v", rerun)
	}
	if got := e.wait(rerun.ID); got.Status != StatusPassed {
		t.Fatalf("rerun: %+v", got)
	}
}

// awaitRerun waits for the pipeline that runs interrupted again.
func (e *env) awaitRerun(interrupted int64) *Pipeline {
	e.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		all, err := e.svc.repo.List(context.Background(), e.project, ListFilter{})
		if err != nil {
			e.t.Fatal(err)
		}
		for _, p := range all {
			if p.RerunOf == interrupted {
				return p
			}
		}
		if time.Now().After(deadline) {
			e.t.Fatalf("pipeline %d was never run again", interrupted)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestReconcileAfterACrash(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	repo := e.svc.repo
	// What a devpad that crashed left: a running pipeline of develop with a
	// job running and one queued, a queued one of the merge request, and a
	// finished one whose ref and a container a late create made remain.
	running := &Pipeline{ProjectID: e.project, UserID: e.owner, UID: "aaaaaaaaaaaaaaa1", Kind: KindBranch, Branch: "develop", HeadSHA: sha(2), ConfigSource: SourceFile, Status: StatusRunning, Trigger: TriggerUser, RefPinned: true, CreatedAt: time.Now(),
		Jobs: []*JobRun{{Job: Job{Name: "a", Run: "pass", Timeout: time.Minute}, Position: 0, Status: JobRunning}, {Job: Job{Name: "b", Run: "pass", Timeout: time.Minute}, Position: 1, Status: JobQueued}}}
	queued := &Pipeline{ProjectID: e.project, UserID: e.owner, UID: "aaaaaaaaaaaaaaa2", Kind: KindMergeRequest, MergeRequestID: e.mr, SourceBranch: "feature", TargetBranch: "develop", HeadSHA: sha(30), TargetSHA: sha(2), ConfigSource: SourceFile, Status: StatusQueued, Trigger: TriggerUser, CreatedAt: time.Now(),
		Jobs: []*JobRun{{Job: Job{Name: "a", Run: "pass", Timeout: time.Minute}, Status: JobQueued}}}
	finished := &Pipeline{ProjectID: e.project, UserID: e.owner, UID: "aaaaaaaaaaaaaaa3", Kind: KindBranch, Branch: "main", HeadSHA: sha(1), ConfigSource: SourceFile, Status: StatusPassed, Trigger: TriggerUser, RefPinned: true, CreatedAt: time.Now()}
	for _, p := range []*Pipeline{running, queued, finished} {
		if err := repo.Create(ctx, p); err != nil {
			t.Fatal(err)
		}
	}
	e.projects.refs[running.UID] = sha(2)
	e.projects.refs[finished.UID] = sha(1)
	mine := container.Job{Pipeline: running.UID, Name: "a"}
	late := container.Job{Pipeline: finished.UID, Name: "a"}
	another := container.Job{Pipeline: "0ther0devpad0001", Name: "a"}
	e.docker.leftovers = []container.JobResource{
		{Kind: container.JobResourceContainer, ID: "x1", Name: mine.ContainerName(), Job: mine},
		{Kind: container.JobResourceNetwork, Name: mine.NetworkName(), Job: mine},
		{Kind: container.JobResourceContainer, ID: "x2", Name: late.ContainerName(), Job: late},
		{Kind: container.JobResourceContainer, ID: "x3", Name: another.ContainerName(), Job: another},
	}

	// And a bundle the crash left in the devpad's own directory, beside a
	// file of someone else's.
	left := filepath.Join(e.tempDir, "devpad-pipeline-123.bundle")
	other := filepath.Join(e.tempDir, "notes.txt")
	for _, path := range []string{left, other} {
		if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	e.svc = e.newService()
	if err := e.svc.Reconcile(ctx); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if _, err := os.Stat(left); !os.IsNotExist(err) {
		t.Errorf("the bundle left behind is still there: %v", err)
	}
	if _, err := os.Stat(other); err != nil {
		t.Errorf("removed a file that is no bundle: %v", err)
	}
	got, _ := repo.Get(ctx, running.ID)
	if got.Status != StatusInterrupted || got.Jobs[0].Status != JobInterrupted || got.Jobs[1].Status != JobSkipped || got.FinishedAt == nil {
		t.Fatalf("running pipeline: %+v %+v %+v", got, got.Jobs[0], got.Jobs[1])
	}
	if got, _ := repo.Get(ctx, queued.ID); got.Status != StatusInterrupted || got.Jobs[0].Status != JobSkipped {
		t.Fatalf("queued pipeline: %+v", got)
	}
	branchRerun, mrRerun := e.awaitRerun(running.ID), e.awaitRerun(queued.ID)
	if branchRerun.Branch != "develop" || mrRerun.MergeRequestID != e.mr || mrRerun.TargetSHA != sha(2) {
		t.Fatalf("reruns: %+v %+v", branchRerun, mrRerun)
	}
	for _, p := range []*Pipeline{branchRerun, mrRerun} {
		if got := e.wait(p.ID); got.Status != StatusPassed {
			t.Fatalf("rerun %d: %+v", p.ID, got)
		}
	}

	// The interrupted and the finished pipeline's leftovers went; the other
	// devpad's container stayed.
	deadline := time.Now().Add(5 * time.Second)
	for len(e.docker.left(running.UID)) > 0 || len(e.docker.left(finished.UID)) > 0 || len(e.projects.pinned()) > 0 {
		if time.Now().After(deadline) {
			t.Fatalf("leftovers remain: docker %+v pins %v", e.docker.left(""), e.projects.pinned())
		}
		time.Sleep(5 * time.Millisecond)
	}
	if left := e.docker.left(another.Pipeline); len(left) != 1 {
		t.Fatalf("another devpad's container was touched: %+v", left)
	}
	for _, p := range []*Pipeline{running, finished} {
		if got, _ := repo.Get(ctx, p.ID); got.RefPinned {
			t.Errorf("pipeline %d still recorded as pinned", p.ID)
		}
	}
}

func TestReconcileDoesNotRerunWhatNoLongerRunsPipelines(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	left := &Pipeline{ProjectID: e.project, UserID: e.owner, UID: "bbbbbbbbbbbbbbb1", Kind: KindMergeRequest, MergeRequestID: e.mr, SourceBranch: "feature", TargetBranch: "develop", HeadSHA: sha(30), TargetSHA: sha(2), ConfigSource: SourceFile, Status: StatusRunning, Trigger: TriggerUser, CreatedAt: time.Now()}
	if err := e.svc.repo.Create(ctx, left); err != nil {
		t.Fatal(err)
	}
	e.mrs.mrs[e.mr].Open = false
	e.svc = e.newService()
	if err := e.svc.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	all, _ := e.svc.repo.List(ctx, e.project, ListFilter{})
	if len(all) != 1 || all[0].Status != StatusInterrupted {
		t.Fatalf("expected only the interrupted pipeline: %+v", all)
	}
}

func TestDeletingTheProjectStopsItsPipelines(t *testing.T) {
	e := setup(t)
	e.projects.setFile(sha(2), ".devpad/pipeline.yml", "jobs:\n  forever:\n    run: hang\n")
	p := e.start(StartInput{Branch: "develop"})
	e.awaitScript("forever")
	e.svc.ProjectDeleting(context.Background(), e.project)
	e.waitIdle(p.ID)
	got, _ := e.svc.repo.Get(context.Background(), p.ID)
	if got.Status != StatusCancelled || got.Error != errProjectDeleted.Error() || got.Jobs[0].Status != JobCancelled {
		t.Fatalf("expected it cancelled: %+v %+v", got, got.Jobs[0])
	}
	if left := e.docker.left(""); len(left) != 0 {
		t.Errorf("left in Docker: %+v", left)
	}
}
