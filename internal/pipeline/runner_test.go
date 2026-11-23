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
		if j.Status != JobPassed || j.ExitCode == nil || *j.ExitCode != 0 || j.StartedAt == nil || j.FinishedAt == nil || !j.OutputKept() {
			t.Errorf("job %s: %+v", j.Name, j)
		}
	}
	if b := job(t, got, "build"); b.OutputBytes != int64(len("built\n")) || b.KeptBytes != b.OutputBytes {
		t.Errorf("build kept %d of %d bytes", b.KeptBytes, b.OutputBytes)
	}
	// The output is only what the job printed; devpad's notes are apart.
	if out := e.output(got, "build"); out != "built\n" {
		t.Errorf("build's output:\n%s", out)
	}
	if notes := e.notes(got, "build"); !slices.Contains(notes, "cloning the commit under test into /workspace") || notes[len(notes)-1] != "the job passed" {
		t.Errorf("build's notes: %q", notes)
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
	if len(e.docker.clones) != len(p.Jobs) {
		t.Fatalf("clone commands: %v", e.docker.clones)
	}
	for name, cmds := range e.docker.clones {
		if !slices.EqualFunc(cmds, want, slices.Equal) {
			t.Errorf("job %s cloned with %v, want %v", name, cmds, want)
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
	if out, notes := e.output(p, "lint"), e.notes(p, "lint"); out != "it broke\n" || notes[len(notes)-1] != "the job exited with 3" {
		t.Errorf("lint's output %q, notes %q", out, notes)
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
	if j, notes := job(t, got, "forever"), e.notes(got, "forever"); j.Status != JobCancelled || !strings.HasPrefix(notes[len(notes)-1], "the job was stopped") {
		t.Errorf("forever: %+v, notes %q", j, notes)
	}
	// The job waiting for the only slot never ran.
	if j := job(t, got, "later"); j.Status != JobSkipped || j.StartedAt != nil {
		t.Errorf("later: %+v", j)
	}
	e.waitIdle(p.UID)
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
	key := MergeRequestKey{MergeRequestID: e.mr, HeadSHA: sha(30), TargetSHA: sha(2), ConfigHash: got.ConfigHash}
	latest, err := e.svc.current(context.Background(), key.of(e.project, PurposeOfficial))
	if err != nil || latest == nil || latest.ID != p.ID {
		t.Fatalf("latest for the pair: %+v %v", latest, err)
	}
	other := key
	other.TargetSHA = sha(1)
	if latest, err := e.svc.current(context.Background(), other.of(e.project, PurposeOfficial)); err != nil || latest != nil {
		t.Fatalf("latest for another target: %+v %v", latest, err)
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

	// The client keeps a bounded list of the files; the pipeline says how
	// many more conflict.
	e.projects.mu.Lock()
	e.projects.conflict = &agent.MergeConflict{Message: "the source conflicts with the target", Paths: []string{"a.go", "b.go"}, More: 3}
	e.projects.mu.Unlock()
	e.mrs.setHead(e.mr, sha(31))
	p = e.wait(e.start(StartInput{MergeRequestID: e.mr}).ID)
	if p.Status != StatusConflict || p.Error != "the source conflicts with the target (5 files conflict; the first 2 are listed)" || len(p.ConflictPaths) != 2 || p.ConflictMore != 3 {
		t.Fatalf("a long conflict: %+v", p)
	}
	if kept, err := e.svc.repo.Get(context.Background(), p.ID); err != nil || kept.ConflictMore != 3 || len(kept.ConflictPaths) != 2 {
		t.Fatalf("kept: %+v %v", kept, err)
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
	if p.Status != StatusNotConfigured || p.ConfigSource != SourceNone || !slices.Contains(resultStatuses, p.Status) || p.FinishedAt == nil {
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
	if notes := e.notes(p, "test"); strings.Count(strings.Join(notes, "\n"), "no address pool left") != 1 {
		t.Errorf("expected one note saying it waited: %q", notes)
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
	running := map[string]bool{}
	for range 3 {
		running[e.nextScript()] = true
	}
	var waiting string
	for _, name := range []string{"a", "b", "c", "d"} {
		if !running[name] {
			waiting = name
		}
	}
	// The second pipeline waits for a slot, behind the first's job that has
	// none.
	second := e.start(StartInput{Branch: "main"})
	awaitWaiters(t, e.svc.slots, e.project, 2)
	if got, _ := e.svc.repo.Get(context.Background(), second.ID); got.Status != StatusQueued {
		t.Fatalf("second pipeline %s with no slot free", got.Status)
	}
	if n := e.svc.slots.running(e.project); n != 3 {
		t.Fatalf("%d slots in use", n)
	}

	// One slot that frees goes to the first pipeline's waiting job.
	for name := range running {
		e.docker.open(name)
		break
	}
	if got := e.nextScript(); got != waiting {
		t.Fatalf("the freed slot went to %s, want the first pipeline's %s", got, waiting)
	}
	if got, _ := e.svc.repo.Get(context.Background(), second.ID); got.Status != StatusQueued {
		t.Fatalf("second pipeline %s before the first's jobs all had a slot", got.Status)
	}

	e.docker.openAll()
	if p := e.wait(first.ID); p.Status != StatusPassed {
		t.Fatalf("first: %+v", p)
	}
	if p := e.wait(second.ID); p.Status != StatusPassed {
		t.Fatalf("second: %+v", p)
	}
	if e.docker.maxRunning != 3 || len(e.docker.scripts) != 5 {
		t.Fatalf("%d of %d jobs ran at once", e.docker.maxRunning, len(e.docker.scripts))
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
	if notes := e.notes(stopped, "forever"); !slices.Contains(notes, "running the job") {
		t.Errorf("expected what it had told kept: %q", notes)
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
	// Not even one that would finish at once, which no listener would hear
	// of until a restart.
	e.projects.setBranch("main", sha(9))
	if _, _, err := e.svc.Start(context.Background(), StartInput{UserID: e.owner, ProjectID: e.project, Branch: "main", Trigger: TriggerUser}); !errors.Is(err, ErrShuttingDown) {
		t.Fatalf("starting one with no pipeline while shut down: %v", err)
	}
	// Nor is it cancelled meanwhile, which would leave its job running in a
	// finished pipeline.
	if _, err := e.svc.Cancel(context.Background(), e.owner, e.project, p.ID); !errors.Is(err, ErrShuttingDown) {
		t.Fatalf("cancelling while shut down: %v", err)
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
	running := &Pipeline{ProjectID: e.project, UserID: e.owner, UID: "aaaaaaaaaaaaaaa1", Kind: KindBranch, Purpose: PurposeOfficial, Branch: "develop", HeadSHA: sha(2), ConfigSource: SourceFile, Status: StatusRunning, Trigger: TriggerUser, RefPinned: true, CreatedAt: time.Now(),
		Jobs: []*JobRun{{Job: Job{Name: "a", Run: "pass", Timeout: time.Minute}, Position: 0, Status: JobRunning}, {Job: Job{Name: "b", Run: "pass", Timeout: time.Minute}, Position: 1, Status: JobQueued}}}
	queued := &Pipeline{ProjectID: e.project, UserID: e.owner, UID: "aaaaaaaaaaaaaaa2", Kind: KindMergeRequest, Purpose: PurposeOfficial, MergeRequestID: e.mr, SourceBranch: "feature", TargetBranch: "develop", HeadSHA: sha(30), TargetSHA: sha(2), ConfigSource: SourceFile, Status: StatusQueued, Trigger: TriggerUser, CreatedAt: time.Now(),
		Jobs: []*JobRun{{Job: Job{Name: "a", Run: "pass", Timeout: time.Minute}, Status: JobQueued}}}
	finished := &Pipeline{ProjectID: e.project, UserID: e.owner, UID: "aaaaaaaaaaaaaaa3", Kind: KindBranch, Purpose: PurposeOfficial, Branch: "main", HeadSHA: sha(1), ConfigSource: SourceFile, Status: StatusPassed, Trigger: TriggerUser, RefPinned: true, CreatedAt: time.Now()}
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

// awaitReconciled waits until Reconcile's background work has run its
// pipelines again: it sweeps once before that and again each sweepInterval,
// which the caller set short.
func (e *env) awaitReconciled() {
	e.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for e.docker.sweepsDone() < 2 {
		if time.Now().After(deadline) {
			e.t.Fatal("Reconcile never swept twice")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestReconcileDoesNotRerunWhatAlreadyHasAResult(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	// An older commit's pipeline still ran when devpad stopped, after the
	// pipeline of develop's tip, started later, had passed.
	old := &Pipeline{ProjectID: e.project, UserID: e.owner, UID: "cccccccccccccc01", Kind: KindBranch, Purpose: PurposeOfficial, Branch: "develop", HeadSHA: sha(7), ConfigSource: SourceFile, Status: StatusRunning, Trigger: TriggerUser, CreatedAt: time.Now()}
	if err := e.svc.repo.Create(ctx, old); err != nil {
		t.Fatal(err)
	}
	passed := e.wait(e.start(StartInput{Branch: "develop"}).ID)
	e.svc = e.newService()
	e.svc.sweepInterval = time.Millisecond
	if err := e.svc.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	e.awaitReconciled()
	all, _ := e.svc.repo.List(ctx, e.project, ListFilter{})
	if len(all) != 2 || all[0].ID != passed.ID || all[1].ID != old.ID || all[1].Status != StatusInterrupted {
		t.Fatalf("expected develop's tip left with its pass: %+v", all)
	}
}

func TestAnInterruptedRunAgainRunsAgainAfterARestart(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	passed := e.wait(e.start(StartInput{Branch: "develop"}).ID)
	// The user ran develop's tip again, with the same jobs, and devpad
	// stopped while it ran.
	again := &Pipeline{ProjectID: e.project, UserID: e.owner, UID: "cccccccccccccc02", Kind: KindBranch, Purpose: PurposeOfficial, Branch: "develop", HeadSHA: passed.HeadSHA, ConfigSource: passed.ConfigSource, ConfigPath: passed.ConfigPath, ConfigHash: passed.ConfigHash, Status: StatusRunning, Trigger: TriggerUser, CreatedAt: time.Now()}
	if err := e.svc.repo.Create(ctx, again); err != nil {
		t.Fatal(err)
	}
	e.svc = e.newService()
	if err := e.svc.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	// The pass it was to repeat does not stand in for it.
	rerun := e.awaitRerun(again.ID)
	if rerun.HeadSHA != passed.HeadSHA || rerun.ConfigHash != passed.ConfigHash || rerun.Trigger != TriggerRestart {
		t.Fatalf("unexpected rerun: %+v", rerun)
	}
	if got := e.wait(rerun.ID); got.Status != StatusPassed {
		t.Fatalf("rerun: %+v", got)
	}
}

func TestReconcileDoesNotRerunWhatNoLongerRunsPipelines(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	left := &Pipeline{ProjectID: e.project, UserID: e.owner, UID: "bbbbbbbbbbbbbbb1", Kind: KindMergeRequest, Purpose: PurposeOfficial, MergeRequestID: e.mr, SourceBranch: "feature", TargetBranch: "develop", HeadSHA: sha(30), TargetSHA: sha(2), ConfigSource: SourceFile, Status: StatusRunning, Trigger: TriggerUser, CreatedAt: time.Now()}
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
	e.waitIdle(p.UID)
	got, _ := e.svc.repo.Get(context.Background(), p.ID)
	if got.Status != StatusCancelled || got.Error != errProjectDeleted.Error() || got.Jobs[0].Status != JobCancelled {
		t.Fatalf("expected it cancelled: %+v %+v", got, got.Jobs[0])
	}
	if left := e.docker.left(""); len(left) != 0 {
		t.Errorf("left in Docker: %+v", left)
	}
}

// failingFinishJob is the repository with FinishJob failing, as a busy or
// broken database would.
type failingFinishJob struct{ Repository }

func (failingFinishJob) FinishJob(context.Context, *JobRun, JobLog) error {
	return errors.New("database is locked")
}

func TestAFinishedPipelineKeepsNoUnfinishedJob(t *testing.T) {
	e := setup(t)
	e.svc.repo = failingFinishJob{e.svc.repo}
	p := e.wait(e.start(StartInput{Branch: "develop"}).ID)
	if p.Status != StatusError {
		t.Fatalf("expected an error, as its jobs' ends went unrecorded: %+v", p)
	}
	for _, j := range p.Jobs {
		if j.Status != JobError || j.Error != lostJob || j.FinishedAt == nil {
			t.Errorf("job %s: %+v", j.Name, j)
		}
		if out, err := e.svc.JobOutput(context.Background(), e.owner, e.project, p.ID, OutputRequest{JobID: j.ID}); err != nil || !out.Done {
			t.Errorf("job %s's output: %+v %v", j.Name, out, err)
		}
	}
}

func TestReconcileSparesThePipelinesThisProcessRuns(t *testing.T) {
	e := setup(t)
	e.projects.setFile(sha(2), ".devpad/pipeline.yml", "jobs:\n  forever:\n    run: block\n")
	p := e.start(StartInput{Branch: "develop"})
	e.awaitScript("forever")
	e.svc.sweepInterval = time.Millisecond
	if err := e.svc.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	e.awaitReconciled()
	if got, _ := e.svc.repo.Get(context.Background(), p.ID); got.Status != StatusRunning {
		t.Fatalf("Reconcile took a running pipeline for one left behind: %+v", got)
	}
	if files := e.tempFiles(); !slices.Equal(files, []string{bundleName(p.UID)}) {
		t.Fatalf("expected the running pipeline's bundle kept, found %v", files)
	}
	if all, _ := e.svc.repo.List(context.Background(), e.project, ListFilter{}); len(all) != 1 {
		t.Fatalf("Reconcile ran its branch again: %+v", all)
	}
	e.docker.mu.Lock()
	removed := slices.Clone(e.docker.removed)
	e.docker.mu.Unlock()
	if len(removed) != 0 {
		t.Fatalf("the sweep removed what a running job has: %+v", removed)
	}
	e.docker.open("forever")
	if got := e.wait(p.ID); got.Status != StatusPassed {
		t.Fatalf("expected it to run to its end: %+v", got)
	}
}

// blockingListener holds each pipeline it is told of until its context
// ends, as a listener told while devpad stops does.
type blockingListener struct {
	told chan *Pipeline
}

func (l *blockingListener) PipelineFinished(ctx context.Context, p *Pipeline) {
	l.told <- p
	<-ctx.Done()
}

func TestListenersAreToldAgainOfWhatTheyWereNotAllTold(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	slow := &blockingListener{told: make(chan *Pipeline, 10)}
	e.svc.AddListener(slow)
	p := e.wait(e.start(StartInput{Branch: "develop"}).ID)
	if told := <-slow.told; told.ID != p.ID {
		t.Fatalf("told of %d", told.ID)
	}
	// devpad stops before the slow listener returns.
	e.shutdown()
	if got, _ := e.svc.repo.Get(ctx, p.ID); got.AnnouncedAt != nil {
		t.Fatalf("recorded announced before every listener was told: %+v", got)
	}
	// And a pipeline that finished without being announced at all, as when
	// devpad crashed right after recording it.
	crashed := &Pipeline{ProjectID: e.project, UserID: e.owner, UID: "dddddddddddddd01", Kind: KindBranch, Purpose: PurposeOfficial, Branch: "main", HeadSHA: sha(1), ConfigSource: SourceFile, Status: StatusPassed, Trigger: TriggerUser, CreatedAt: time.Now()}
	if err := e.svc.repo.Create(ctx, crashed); err != nil {
		t.Fatal(err)
	}

	e.svc = e.newService()
	listener := e.listener
	for len(listener.finished) > 0 {
		<-listener.finished
	}
	if err := e.svc.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	told := map[int64]bool{}
	for range 2 {
		select {
		case got := <-listener.finished:
			told[got.ID] = true
		case <-time.After(5 * time.Second):
			t.Fatalf("listeners told only of %v", told)
		}
	}
	if !told[p.ID] || !told[crashed.ID] {
		t.Fatalf("listeners told of %v", told)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		a, _ := e.svc.repo.Get(ctx, p.ID)
		b, _ := e.svc.repo.Get(ctx, crashed.ID)
		if a.AnnouncedAt != nil && b.AnnouncedAt != nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("never recorded announced: %v %v", a.AnnouncedAt, b.AnnouncedAt)
		}
		time.Sleep(time.Millisecond)
	}
	// Once announced, a restart tells no one again.
	e.svc = e.newService()
	if err := e.svc.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-listener.finished:
		t.Fatalf("told again of %d", got.ID)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestADeletedProjectStartsNoPipelines(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	// A pipeline that finished, and what a create cut short made for it
	// late, which the sweep could not find once the project's records go.
	done := e.wait(e.start(StartInput{Branch: "develop"}).ID)
	late := container.Job{Pipeline: done.UID, Name: "build"}
	another := container.Job{Pipeline: "0ther0devpad0002", Name: "a"}
	e.docker.mu.Lock()
	e.docker.leftovers = []container.JobResource{
		{Kind: container.JobResourceContainer, ID: "x1", Name: late.ContainerName(), Job: late},
		{Kind: container.JobResourceContainer, ID: "x2", Name: another.ContainerName(), Job: another},
	}
	e.docker.mu.Unlock()

	e.svc.ProjectDeleting(ctx, e.project)
	if left := e.docker.left(done.UID); len(left) != 0 {
		t.Errorf("the project's pipeline still has %+v", left)
	}
	if left := e.docker.left(another.Pipeline); len(left) != 1 {
		t.Errorf("another devpad's container was touched: %+v", left)
	}
	if _, _, err := e.svc.Start(ctx, StartInput{UserID: e.owner, ProjectID: e.project, Branch: "main", Trigger: TriggerUser}); !errors.Is(err, project.ErrDeleting) {
		t.Fatalf("started a pipeline of a project being deleted: %v", err)
	}
	// A deletion that fails leaves the project to run pipelines again.
	e.svc.ProjectDeleteEnded(e.project)
	if got := e.wait(e.start(StartInput{Branch: "main"}).ID); got.Status != StatusPassed {
		t.Fatalf("after the deletion ended: %+v", got)
	}
}

func TestAPipelineRunsOnTheImageOfWhenItGetsASlot(t *testing.T) {
	e := setup(t)
	e.projects.env = &project.Environment{Image: "devpad-project-1:old"}
	e.svc.slots = newJobSlots(1)
	e.projects.setFile(sha(1), ".devpad/pipeline.yml", "jobs:\n  hold:\n    run: block\n")
	holding := e.start(StartInput{Branch: "main"})
	e.awaitScript("hold")
	waiting := e.start(StartInput{Branch: "develop"})
	awaitWaiters(t, e.svc.slots, e.project, 1)
	// A build replaces the image while the pipeline waits for its slot.
	e.projects.mu.Lock()
	e.projects.env = &project.Environment{Image: "devpad-project-1:new"}
	e.projects.mu.Unlock()
	e.docker.open("hold")
	if got := e.wait(holding.ID); got.Image != "devpad-project-1:old" {
		t.Fatalf("the first pipeline ran on %s", got.Image)
	}
	if got := e.wait(waiting.ID); got.Status != StatusPassed || got.Image != "devpad-project-1:new" {
		t.Fatalf("expected the waiting pipeline on the new image: %+v", got)
	}
}

func TestTheBundlesDirectoryIsMadeAgainWhenItIsGone(t *testing.T) {
	e := setup(t)
	e.tempDir = filepath.Join(e.tempDir, "bundles")
	e.svc = e.newService()
	if got := e.wait(e.start(StartInput{Branch: "develop"}).ID); got.Status != StatusPassed {
		t.Fatalf("expected a pass: %+v", got)
	}
	if info, err := os.Stat(e.tempDir); err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("bundles' directory: %v %v", info, err)
	}
}

func TestCancelSettlesAPipelineNoRunHolds(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	// A run that ended without recording how, as when the database failed.
	stuck := &Pipeline{ProjectID: e.project, UserID: e.owner, UID: "eeeeeeeeeeeeee01", Kind: KindBranch, Purpose: PurposeOfficial, Branch: "develop", HeadSHA: sha(2), ConfigSource: SourceFile, Status: StatusRunning, Trigger: TriggerUser, CreatedAt: time.Now(),
		Jobs: []*JobRun{{Job: Job{Name: "a", Run: "pass", Timeout: time.Minute}, Position: 0, Status: JobRunning}, {Job: Job{Name: "b", Run: "pass", Timeout: time.Minute}, Position: 1, Status: JobQueued}}}
	if err := e.svc.repo.Create(ctx, stuck); err != nil {
		t.Fatal(err)
	}
	got, err := e.svc.Cancel(ctx, e.owner, e.project, stuck.ID)
	if err != nil || got.Status != StatusCancelled {
		t.Fatalf("cancel: %+v %v", got, err)
	}
	if a, b := got.Jobs[0], got.Jobs[1]; a.Status != JobCancelled || a.FinishedAt == nil || b.Status != JobSkipped {
		t.Fatalf("jobs: %+v %+v", a, b)
	}
	if out, err := e.svc.JobOutput(ctx, e.owner, e.project, stuck.ID, OutputRequest{JobID: got.Jobs[0].ID}); err != nil || !out.Done {
		t.Fatalf("output: %+v %v", out, err)
	}
}
