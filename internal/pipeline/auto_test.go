package pipeline

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/devpad-org/devpad/internal/project"
)

// The one rule for what runs by itself: ticked protected branches, and
// open merge requests into them.
func TestRunsAutomatically(t *testing.T) {
	ready := &project.Project{DefaultBranch: "main", ProtectedBranches: []string{"develop", "release"}, PipelineBranches: []string{"develop", "feature"}, Status: project.StatusReady}
	notReady := *ready
	notReady.Status = project.StatusCreating
	into := func(target string, open bool) *MergeRequest {
		return &MergeRequest{ID: 1, SourceBranch: "feature", TargetBranch: target, HeadSHA: sha(1), Open: open}
	}
	for _, tc := range []struct {
		name   string
		p      *project.Project
		branch string
		mr     *MergeRequest
		want   bool
	}{
		{"a ticked protected branch", ready, "develop", nil, true},
		{"an unticked protected branch", ready, "main", nil, false},
		{"an unticked protected branch besides the default", ready, "release", nil, false},
		{"a ticked branch no longer protected", ready, "feature", nil, false},
		{"an unknown branch", ready, "nope", nil, false},
		{"an open merge request into a ticked branch", ready, "", into("develop", true), true},
		{"a closed merge request into a ticked branch", ready, "", into("develop", false), false},
		{"an open merge request into an unticked branch", ready, "", into("main", true), false},
		{"the merge request decides, not the branch", ready, "develop", into("main", true), false},
		{"a project not ready", &notReady, "develop", nil, false},
		{"no project", nil, "develop", nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := runsAutomatically(tc.p, tc.branch, tc.mr); got != tc.want {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}

// tick has the project run pipelines on branches, as SetBranches records.
func (e *env) tick(branches ...string) {
	e.projects.setProject(func(p *project.Project) { p.PipelineBranches = branches })
}

// addMergeRequest records an open merge request, in the database and the
// fake, and returns its ID.
func (e *env) addMergeRequest(source, target, head string) int64 {
	e.t.Helper()
	res, err := e.db.Exec(`INSERT INTO merge_requests (project_id, user_id, title, source_branch, target_branch, status, head_sha) VALUES (?, ?, ?, ?, ?, 'open', ?)`,
		e.project, e.owner, source, source, target, head)
	if err != nil {
		e.t.Fatalf("insert merge request: %v", err)
	}
	id, _ := res.LastInsertId()
	e.mrs.set(MergeRequest{ID: id, SourceBranch: source, TargetBranch: target, HeadSHA: head, Open: true})
	return id
}

// moveBranch moves a branch of the fake repo to a commit with the jobs.
func (e *env) moveBranch(branch, commit string) {
	e.projects.setFile(commit, ".devpad/pipeline.yml", jobsYAML)
	e.projects.setBranch(branch, commit)
}

// holdEnvironment keeps new pipelines queued until the returned func runs.
func (e *env) holdEnvironment() (release func()) {
	gate := make(chan struct{})
	e.projects.mu.Lock()
	e.projects.envGate = gate
	e.projects.mu.Unlock()
	return func() {
		e.projects.mu.Lock()
		e.projects.envGate = nil
		e.projects.mu.Unlock()
		close(gate)
	}
}

// autoIdle waits until automatic runs have begun, after Reconcile's
// catch-up when it was called, and none waits or is being made.
func (e *env) autoIdle() {
	e.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if waiting, workers := e.svc.auto.size(); e.svc.auto.opened() && waiting == 0 && workers == 0 {
			return
		}
		if time.Now().After(deadline) {
			waiting, workers := e.svc.auto.size()
			e.t.Fatalf("automatic runs still going: %d waiting, %d workers", waiting, workers)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// pipelines lists the project's pipelines, oldest first.
func (e *env) pipelines() []*Pipeline {
	e.t.Helper()
	list, err := e.svc.repo.List(context.Background(), e.project, ListFilter{Limit: MaxListLimit})
	if err != nil {
		e.t.Fatalf("list: %v", err)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].ID < list[j].ID })
	return list
}

// started describes each pipeline as "<subject>@<head>[/<target>] <trigger>",
// in order of the descriptions, since runs made at once start in any order.
func (e *env) started() string {
	e.t.Helper()
	var out []string
	for _, p := range e.pipelines() {
		out = append(out, describe(p))
	}
	return set(out...)
}

// set is descriptions as started lists them.
func set(descriptions ...string) string {
	sort.Strings(descriptions)
	return strings.Join(descriptions, ", ")
}

// find returns the pipeline started describes so.
func (e *env) find(description string) *Pipeline {
	e.t.Helper()
	for _, p := range e.pipelines() {
		if describe(p) == description {
			return p
		}
	}
	e.t.Fatalf("no pipeline %s among %s", description, e.started())
	return nil
}

func describe(p *Pipeline) string {
	if p.Kind == KindBranch {
		return fmt.Sprintf("%s@%s %s", p.Branch, short(p.HeadSHA), p.Trigger)
	}
	return fmt.Sprintf("!%d@%s/%s %s", p.MergeRequestID, short(p.HeadSHA), short(p.TargetSHA), p.Trigger)
}

// short is a made-up commit hash by its number.
func short(sha string) string {
	return strings.TrimLeft(sha, "0")
}

func (e *env) merged(branch, commit, source string) project.BranchMove {
	e.moveBranch(branch, commit)
	return project.BranchMove{UserID: e.owner, ProjectID: e.project, Branch: branch, SHA: commit, Cause: project.MoveMerged, Source: source}
}

// A merge into a ticked branch runs its new commit and runs again the open
// merge requests into it, but the one that merged, which would only test
// its own merge; a branch not ticked runs nothing.
func TestAMergeRunsTheTickedBranchAndItsMergeRequests(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	e.tick("develop")
	hotfix := e.addMergeRequest("hotfix", "develop", sha(40))
	e.addMergeRequest("release", "main", sha(50))
	e.svc.beginAutomatic()

	// hotfix is still open as its merge is told.
	e.svc.BranchMoved(ctx, e.merged("develop", sha(3), "hotfix"))
	e.svc.BranchMoved(ctx, e.merged("main", sha(4), "other"))
	e.autoIdle()
	if got, want := e.started(), set("develop@3 merged", fmt.Sprintf("!%d@1e/3 target_moved", e.mr)); got != want {
		t.Fatalf("started %q, want %q", got, want)
	}
	if got, _ := e.svc.repo.List(ctx, e.project, ListFilter{MergeRequestID: hotfix}); len(got) != 0 {
		t.Fatalf("the merge request that merged ran: %s", describe(got[0]))
	}
}

// A fast-forward from the upstream does the same; a publish, which never
// moves a protected branch, runs nothing.
func TestAFastForwardRunsTheTickedBranchAndAPublishNothing(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	e.tick("develop")
	e.svc.beginAutomatic()

	e.moveBranch("develop", sha(3))
	e.svc.BranchMoved(ctx, project.BranchMove{UserID: e.owner, ProjectID: e.project, Branch: "develop", SHA: sha(3), Cause: project.MoveFastForwarded})
	e.moveBranch("feature", sha(31))
	e.svc.BranchMoved(ctx, project.BranchMove{UserID: e.owner, ProjectID: e.project, Branch: "feature", SHA: sha(31), Cause: project.MovePublished})
	e.autoIdle()
	if got, want := e.started(), set("develop@3 upstream", fmt.Sprintf("!%d@1e/3 target_moved", e.mr)); got != want {
		t.Fatalf("started %q, want %q", got, want)
	}
}

// A merge request into a ticked branch runs when it opens and when its head
// moves; one into an unticked branch runs only by hand.
func TestMergeRequestsIntoTickedBranchesRunWhenTheyOpenOrMove(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	e.tick("develop")
	release := e.addMergeRequest("release", "main", sha(50))
	e.svc.beginAutomatic()

	e.svc.MergeRequestMoved(ctx, MergeRequestMove{UserID: e.owner, ProjectID: e.project, MergeRequestID: e.mr, Opened: true})
	e.svc.MergeRequestMoved(ctx, MergeRequestMove{UserID: e.owner, ProjectID: e.project, MergeRequestID: release, Opened: true})
	e.autoIdle()
	e.mrs.setHead(e.mr, sha(31))
	e.svc.MergeRequestMoved(ctx, MergeRequestMove{UserID: e.owner, ProjectID: e.project, MergeRequestID: e.mr})
	e.mrs.setHead(release, sha(51))
	e.svc.MergeRequestMoved(ctx, MergeRequestMove{UserID: e.owner, ProjectID: e.project, MergeRequestID: release})
	e.autoIdle()
	want := set(fmt.Sprintf("!%d@1e/2 opened", e.mr), fmt.Sprintf("!%d@1f/2 source_moved", e.mr))
	if got := e.started(); got != want {
		t.Fatalf("started %q, want %q", got, want)
	}
	// The older one, of a head since moved, was replaced unless it had
	// passed already.
	if got := e.wait(e.find(fmt.Sprintf("!%d@1e/2 opened", e.mr)).ID); got.Status != StatusSuperseded && got.Status != StatusPassed {
		t.Fatalf("the older pipeline: %s", got.Status)
	}

	// A merge request that closed runs nothing.
	e.mrs.set(MergeRequest{ID: e.mr, SourceBranch: "feature", TargetBranch: "develop", HeadSHA: sha(32), Open: false})
	e.svc.MergeRequestMoved(ctx, MergeRequestMove{UserID: e.owner, ProjectID: e.project, MergeRequestID: e.mr})
	e.autoIdle()
	if got := e.started(); got != want {
		t.Fatalf("a closed merge request ran: %q", got)
	}
}

// Ticking a branch runs its current commit and its open merge requests;
// a branch already ticked runs nothing more.
func TestTickingABranchRunsIt(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	e.svc.beginAutomatic()
	before := e.projects.current()
	e.tick("develop")
	after := e.projects.current()
	e.svc.ProjectSettingsChanged(ctx, before, after)
	e.autoIdle()
	ticked := []string{"develop@2 ticked", fmt.Sprintf("!%d@1e/2 ticked", e.mr)}
	if got, want := e.started(), set(ticked...); got != want {
		t.Fatalf("started %q, want %q", got, want)
	}

	// Ticking main too runs main alone.
	e.tick("develop", "main")
	e.svc.ProjectSettingsChanged(ctx, after, e.projects.current())
	e.autoIdle()
	if got, want := e.started(), set(append(ticked, "main@1 ticked")...); got != want {
		t.Fatalf("started %q, want %q", got, want)
	}
}

// Changing the project's pipeline setting runs again what used it: a
// ticked branch whose commit has no pipeline file, and the merge requests
// into it. One whose jobs come from its file runs nothing more.
func TestChangingTheSettingRunsAgainWhatUsedIt(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	e.tick("develop", "main")
	// develop's commit has no file, so its pipeline is the setting.
	e.projects.mu.Lock()
	delete(e.projects.files, sha(2))
	e.projects.project.PipelineConfig = "jobs:\n  lint:\n    run: pass\n"
	e.projects.mu.Unlock()
	if err := e.svc.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	e.autoIdle()
	caughtUp := []string{"develop@2 catch_up", "main@1 catch_up", fmt.Sprintf("!%d@1e/2 catch_up", e.mr)}
	if got, want := e.started(), set(caughtUp...); got != want {
		t.Fatalf("caught up %q, want %q", got, want)
	}

	before := e.projects.current()
	e.projects.setProject(func(p *project.Project) { p.PipelineConfig = "jobs:\n  lint:\n    run: pass\n  vet:\n    run: pass\n" })
	e.svc.ProjectSettingsChanged(ctx, before, e.projects.current())
	e.autoIdle()
	want := set(append(caughtUp, "develop@2 setting", fmt.Sprintf("!%d@1e/2 setting", e.mr))...)
	if got := e.started(); got != want {
		t.Fatalf("started %q, want %q", got, want)
	}
	// Another change of something else runs nothing.
	e.svc.ProjectSettingsChanged(ctx, e.projects.current(), e.projects.current())
	e.autoIdle()
	if got := e.started(); got != want {
		t.Fatalf("an unchanged setting ran %q", got)
	}
}

// A newer commit of a ticked branch replaces its queued pipeline but lets
// a running one finish; a merge request's newer target or head stops its
// pipeline, running or not.
func TestNewerCommitsReplaceQueuedBranchPipelinesAndStopStaleMergeRequestOnes(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	e.tick("develop")
	e.svc.beginAutomatic()
	e.projects.setFile(sha(2), ".devpad/pipeline.yml", "jobs:\n  talk:\n    run: block hi\n")
	mr := func(head, target int, trigger Trigger) string {
		return fmt.Sprintf("!%d@%x/%x %s", e.mr, head, target, trigger)
	}

	// Running: develop at 2, and the merge request onto it.
	e.svc.BranchMoved(ctx, project.BranchMove{UserID: e.owner, ProjectID: e.project, Branch: "develop", SHA: sha(2), Cause: project.MoveMerged})
	e.autoIdle()
	e.awaitScript("talk")
	e.awaitScript("talk")
	runningBranch, runningMR := e.find("develop@2 merged"), e.find(mr(30, 2, TriggerTargetMoved))

	// Queued, held before they start: develop at 3, then at 4, then a new
	// head for the merge request.
	release := e.holdEnvironment()
	e.svc.BranchMoved(ctx, e.merged("develop", sha(3), ""))
	e.autoIdle()
	e.svc.BranchMoved(ctx, e.merged("develop", sha(4), ""))
	e.autoIdle()
	e.mrs.setHead(e.mr, sha(31))
	e.svc.MergeRequestMoved(ctx, MergeRequestMove{UserID: e.owner, ProjectID: e.project, MergeRequestID: e.mr})
	e.autoIdle()

	if got := e.wait(e.find("develop@3 merged").ID); got.Status != StatusSuperseded {
		t.Fatalf("the queued branch pipeline: %s", got.Status)
	}
	if got, _ := e.svc.repo.Get(ctx, runningBranch.ID); got.Status != StatusRunning {
		t.Fatalf("the running branch pipeline: %s", got.Status)
	}
	if got := e.wait(runningMR.ID); got.Status != StatusSuperseded || got.StartedAt == nil {
		t.Fatalf("the running merge request pipeline: %s", got.Status)
	}
	for _, stale := range []string{mr(30, 3, TriggerTargetMoved), mr(30, 4, TriggerTargetMoved)} {
		if got := e.wait(e.find(stale).ID); got.Status != StatusSuperseded {
			t.Fatalf("%s: %s", stale, got.Status)
		}
	}
	e.docker.openAll()
	release()
	for _, want := range []string{"develop@2 merged", "develop@4 merged", mr(31, 4, TriggerSourceMoved)} {
		if got := e.wait(e.find(want).ID); got.Status != StatusPassed {
			t.Fatalf("%s ended %s", want, got.Status)
		}
	}
	want := set("develop@2 merged", "develop@3 merged", "develop@4 merged",
		mr(30, 2, TriggerTargetMoved), mr(30, 3, TriggerTargetMoved), mr(30, 4, TriggerTargetMoved), mr(31, 4, TriggerSourceMoved))
	if got := e.started(); got != want {
		t.Fatalf("started %q\nwant    %q", got, want)
	}
}

// Devpad never runs again by itself what the user cancelled or what could
// not run: only new commits or jobs do, or Run again.
func TestAutomaticRunsNeverRepeatACancelledOrFailedToRunPipeline(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	e.tick("develop")
	e.svc.beginAutomatic()
	moved := MergeRequestMove{UserID: e.owner, ProjectID: e.project, MergeRequestID: e.mr}

	release := e.holdEnvironment()
	e.svc.MergeRequestMoved(ctx, moved)
	e.autoIdle()
	first := e.pipelines()[0]
	if _, err := e.svc.Cancel(ctx, e.owner, e.project, first.ID); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	release()
	e.svc.MergeRequestMoved(ctx, moved)
	e.autoIdle()
	if n := len(e.pipelines()); n != 1 {
		t.Fatalf("a cancelled pipeline ran again: %s", e.started())
	}

	// One that could not run is not run again either.
	e.projects.mu.Lock()
	e.projects.pinErr = fmt.Errorf("the repo container is gone")
	e.projects.mu.Unlock()
	e.mrs.setHead(e.mr, sha(31))
	e.svc.MergeRequestMoved(ctx, moved)
	e.autoIdle()
	failed := e.wait(e.pipelines()[1].ID)
	if failed.Status != StatusError {
		t.Fatalf("expected an error, got %s", failed.Status)
	}
	e.svc.MergeRequestMoved(ctx, moved)
	e.autoIdle()
	if n := len(e.pipelines()); n != 2 {
		t.Fatalf("a pipeline that could not run ran again: %s", e.started())
	}
	// Nothing reacted to it ending.
	time.Sleep(20 * time.Millisecond)
	e.autoIdle()
	if n := len(e.pipelines()); n != 2 {
		t.Fatalf("a pipeline ending started another: %s", e.started())
	}
	// A new head does run.
	e.projects.mu.Lock()
	e.projects.pinErr = nil
	e.projects.mu.Unlock()
	e.mrs.setHead(e.mr, sha(32))
	e.svc.MergeRequestMoved(ctx, moved)
	e.autoIdle()
	if got := e.wait(e.pipelines()[2].ID); got.Status != StatusPassed || got.HeadSHA != sha(32) {
		t.Fatalf("the new head's pipeline: %s %s", describe(got), got.Status)
	}
}

// However many times a merge request's head moves while devpad reads it,
// it is read once more after, and starts a pipeline of its newest head:
// what a hostile workspace pushing in a loop can make devpad do is bounded
// by that, not by how often it pushes. The listeners never wait meanwhile.
func TestABurstOfHeadMovesIsReadTwice(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	e.tick("develop")
	e.svc.beginAutomatic()
	release := e.mrs.hold()

	moved := MergeRequestMove{UserID: e.owner, ProjectID: e.project, MergeRequestID: e.mr}
	e.svc.MergeRequestMoved(ctx, moved)
	deadline := time.Now().Add(5 * time.Second)
	for e.mrs.lookupCount() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the first run never read the merge request")
		}
		time.Sleep(time.Millisecond)
	}
	start := time.Now()
	for i := 1; i <= 50; i++ {
		e.mrs.setHead(e.mr, sha(100+i))
		e.svc.MergeRequestMoved(ctx, moved)
		e.svc.BranchMoved(ctx, project.BranchMove{UserID: e.owner, ProjectID: e.project, Branch: fmt.Sprint("b", i), SHA: sha(i), Cause: project.MovePublished})
	}
	if waited := time.Since(start); waited > time.Second {
		t.Fatalf("the listeners waited %s for the held run", waited)
	}
	if waiting, workers := e.svc.auto.size(); waiting != 1 || workers != 1 {
		t.Fatalf("%d runs waiting on %d workers", waiting, workers)
	}
	release()
	e.autoIdle()
	// Two runs, each reading the merge request once and Start once more.
	if n := e.mrs.lookupCount(); n != 4 {
		t.Fatalf("%d lookups of the merge request", n)
	}
	if got, want := e.started(), fmt.Sprintf("!%d@%x/2 source_moved", e.mr, 150); got != want {
		t.Fatalf("started %q, want %q", got, want)
	}
}

// Events told before Reconcile wait for it, and starting up catches up on
// every ticked branch and merge request into one that has no pipeline of
// its commits and jobs as they are, once; a restart after makes no more.
func TestStartingUpCatchesUpOnce(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	e.tick("develop")
	e.svc.MergeRequestMoved(ctx, MergeRequestMove{UserID: e.owner, ProjectID: e.project, MergeRequestID: e.mr, Opened: true})
	time.Sleep(20 * time.Millisecond)
	if got := e.started(); got != "" {
		t.Fatalf("started before Reconcile: %q", got)
	}
	if err := e.svc.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	e.autoIdle()
	want := set(fmt.Sprintf("!%d@1e/2 opened", e.mr), "develop@2 catch_up")
	if got := e.started(); got != want {
		t.Fatalf("started %q, want %q", got, want)
	}
	for _, p := range e.pipelines() {
		e.wait(p.ID)
	}

	// devpad restarts: everything has its pipeline already.
	e.shutdown()
	e.svc = e.newService()
	if err := e.svc.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	e.autoIdle()
	if got := e.started(); got != want {
		t.Fatalf("a restart caught up again: %q", got)
	}
}

// Shutting down leaves automatic runs waiting and stops one being made.
func TestShutdownStopsAutomaticRuns(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	e.tick("develop")
	e.svc.beginAutomatic()
	release := e.mrs.hold()
	defer release()
	e.svc.MergeRequestMoved(ctx, MergeRequestMove{UserID: e.owner, ProjectID: e.project, MergeRequestID: e.mr, Opened: true})
	for e.mrs.lookupCount() == 0 {
		time.Sleep(time.Millisecond)
	}
	e.svc.MergeRequestMoved(ctx, MergeRequestMove{UserID: e.owner, ProjectID: e.project, MergeRequestID: e.mr})
	done := make(chan struct{})
	go func() {
		e.shutdown()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("shutdown waited for the held automatic run")
	}
	// Nothing starts once devpad stops.
	e.svc.MergeRequestMoved(ctx, MergeRequestMove{UserID: e.owner, ProjectID: e.project, MergeRequestID: e.mr})
	if _, workers := e.svc.auto.size(); workers != 0 {
		t.Fatalf("%d workers after shutdown", workers)
	}
	if got := e.started(); got != "" {
		t.Fatalf("started %q", got)
	}
}
