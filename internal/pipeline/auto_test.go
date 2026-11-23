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
	for _, tc := range []struct {
		name   string
		p      *project.Project
		branch string
		want   bool
	}{
		{"a ticked protected branch", ready, "develop", true},
		{"an unticked protected branch", ready, "main", false},
		{"an unticked protected branch besides the default", ready, "release", false},
		{"a ticked branch no longer protected", ready, "feature", false},
		{"an unknown branch", ready, "nope", false},
		{"a project not ready", &notReady, "develop", false},
		{"no project", nil, "develop", false},
	} {
		t.Run("branch: "+tc.name, func(t *testing.T) {
			if got := branchRunsAutomatically(tc.p, tc.branch); got != tc.want {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
	into := func(target string, open bool) *MergeRequest {
		return &MergeRequest{ID: 1, SourceBranch: "feature", TargetBranch: target, HeadSHA: sha(1), Open: open}
	}
	for _, tc := range []struct {
		name string
		p    *project.Project
		mr   *MergeRequest
		want bool
	}{
		{"open into a ticked branch", ready, into("develop", true), true},
		{"closed into a ticked branch", ready, into("develop", false), false},
		{"open into an unticked branch", ready, into("main", true), false},
		{"open into a ticked branch no longer protected", ready, into("feature", true), false},
		{"in a project not ready", &notReady, into("develop", true), false},
		{"none", ready, nil, false},
	} {
		t.Run("merge request: "+tc.name, func(t *testing.T) {
			if got := mergeRequestRunsAutomatically(tc.p, tc.mr); got != tc.want {
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
// catch-up when it was called, and none is ready or being made: any left
// wait for their interval.
func (e *env) autoIdle() {
	e.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !e.svc.auto.idle() {
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

// Devpad never runs again by itself what the user cancelled; what could not
// run it runs once more, at the next event or catch-up, and then leaves to
// the user, however often devpad restarts. Only new commits or jobs run
// again, or Run again.
func TestAutomaticRunsSettleCancelledAndRetryErrorsOnce(t *testing.T) {
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

	// One that could not run runs once more, then no more.
	e.projects.mu.Lock()
	e.projects.pinErr = fmt.Errorf("the repo container is gone")
	e.projects.mu.Unlock()
	e.mrs.setHead(e.mr, sha(31))
	for i := 0; i < 4; i++ {
		e.svc.MergeRequestMoved(ctx, moved)
		e.autoIdle()
		for _, p := range e.pipelines() {
			e.wait(p.ID)
		}
	}
	if got, want := e.started(), set(fmt.Sprintf("!%d@1e/2 source_moved", e.mr), fmt.Sprintf("!%d@1f/2 source_moved", e.mr), fmt.Sprintf("!%d@1f/2 source_moved", e.mr)); got != want {
		t.Fatalf("started %q, want %q", got, want)
	}
	// Nor after a restart and its catch-up.
	e.shutdown()
	e.svc = e.newService()
	if err := e.svc.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	e.autoIdle()
	if got, _ := e.svc.repo.List(ctx, e.project, ListFilter{MergeRequestID: e.mr}); len(got) != 3 {
		t.Fatalf("a restart ran the errors again: %s", e.started())
	}
	// A new head does run.
	e.projects.mu.Lock()
	e.projects.pinErr = nil
	e.projects.mu.Unlock()
	e.mrs.setHead(e.mr, sha(32))
	e.svc.MergeRequestMoved(ctx, moved)
	e.autoIdle()
	if got := e.wait(e.find(fmt.Sprintf("!%d@20/2 source_moved", e.mr)).ID); got.Status != StatusPassed {
		t.Fatalf("the new head's pipeline: %s", got.Status)
	}
}

// What has no result on the commits and jobs as they are shows, with why
// and whether devpad tries again: on the merge request, and on a ticked
// branch's badge.
func TestSettledPipelinesShow(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	e.tick("develop")
	e.svc.beginAutomatic()
	e.projects.mu.Lock()
	e.projects.pinErr = fmt.Errorf("docker hiccup")
	e.projects.mu.Unlock()
	// Ticking develop runs it and the merge request into it, once each.
	e.svc.ProjectSettingsChanged(ctx, &project.Project{ID: e.project, UserID: e.owner}, e.projects.current())
	e.autoIdle()
	for _, p := range e.pipelines() {
		e.wait(p.ID)
	}

	st, err := e.svc.MergeRequestStatus(ctx, e.owner, e.project, e.mr)
	if err != nil {
		t.Fatal(err)
	}
	if !st.Automatic || st.Current != nil || st.Settled == nil || st.Settled.Pipeline.Status != StatusError || !st.Settled.Retrying ||
		!strings.Contains(st.Settled.Reason, "couldn't run on these commits: pinning what the pipeline tests: docker hiccup.") || !strings.Contains(st.Settled.Reason, "once more") {
		t.Fatalf("status after one error: %+v %+v", st, st.Settled)
	}
	latest, err := e.svc.Latest(ctx, e.owner, e.project, LatestFilter{Branches: []string{"develop", "main"}, MergeRequestIDs: []int64{e.mr}})
	if err != nil {
		t.Fatal(err)
	}
	if b := latest.Branches["develop"]; b == nil || !b.Automatic || b.Settled == nil || b.Settled.Pipeline.Branch != "develop" || !b.Settled.Retrying {
		t.Fatalf("develop's standing: %+v", b)
	}
	if b := latest.Branches["main"]; b == nil || b.Automatic || b.Settled != nil {
		t.Fatalf("main's standing: %+v", b)
	}

	// The retry fails too: devpad leaves it to the user.
	e.svc.MergeRequestMoved(ctx, MergeRequestMove{UserID: e.owner, ProjectID: e.project, MergeRequestID: e.mr})
	e.autoIdle()
	for _, p := range e.pipelines() {
		e.wait(p.ID)
	}
	st, _ = e.svc.MergeRequestStatus(ctx, e.owner, e.project, e.mr)
	if st.Settled == nil || st.Settled.Retrying || !strings.Contains(st.Settled.Reason, "won't run them again by itself") {
		t.Fatalf("status after the retry: %+v", st.Settled)
	}
	// Cancelled is the user's call.
	e.projects.mu.Lock()
	e.projects.pinErr = nil
	e.projects.mu.Unlock()
	e.mrs.setHead(e.mr, sha(31))
	release := e.holdEnvironment()
	e.svc.MergeRequestMoved(ctx, MergeRequestMove{UserID: e.owner, ProjectID: e.project, MergeRequestID: e.mr})
	e.autoIdle()
	queued := e.find(fmt.Sprintf("!%d@1f/2 source_moved", e.mr))
	if _, err := e.svc.Cancel(ctx, e.owner, e.project, queued.ID); err != nil {
		t.Fatal(err)
	}
	release()
	st, _ = e.svc.MergeRequestStatus(ctx, e.owner, e.project, e.mr)
	if st.Settled == nil || st.Settled.Pipeline.ID != queued.ID || st.Settled.Retrying || !strings.Contains(st.Settled.Reason, "was cancelled on these commits") {
		t.Fatalf("status after a cancel: %+v", st.Settled)
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
	for _, p := range e.pipelines() {
		e.wait(p.ID)
	}
	// Two runs, each reading the merge request once and Start once more,
	// and the pipeline once as it starts.
	if n := e.mrs.lookupCount(); n != 5 {
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

// However often a hostile workspace pushes, devpad starts a branch or merge
// request at most twice an autoMinInterval: the first push at once, and
// the newest when the interval is up.
func TestSpacedPushesStartAtMostTwiceAnInterval(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	e.tick("develop")
	clock := newFakeClock()
	e.svc.auto.now, e.svc.auto.after, e.svc.auto.interval = clock.now, clock.after, autoMinInterval
	e.svc.beginAutomatic()
	moved := MergeRequestMove{UserID: e.owner, ProjectID: e.project, MergeRequestID: e.mr}

	for i := 1; i <= 10; i++ {
		e.mrs.setHead(e.mr, sha(100+i))
		e.svc.MergeRequestMoved(ctx, moved)
		e.autoIdle()
		clock.advance(autoMinInterval / 10)
	}
	e.autoIdle()
	got := e.pipelines()
	if len(got) != 2 || got[1].HeadSHA != sha(110) {
		t.Fatalf("10 pushes in %s started %s", autoMinInterval, e.started())
	}
	if p := e.wait(got[1].ID); p.Status != StatusPassed {
		t.Fatalf("the newest head's pipeline %s", p.Status)
	}
}

// What an automatic run could not start, because the repo could not be read
// or the start took too long, the catch-up every sweepInterval starts
// later. A slow start holds one worker: the project's other runs wait in
// the queue, not on its start lock, so their own time isn't spent waiting.
func TestTheCatchUpStartsWhatARunCouldNot(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	e.tick("develop")
	ids := []int64{e.mr}
	for i := 0; i < 4; i++ {
		ids = append(ids, e.addMergeRequest(fmt.Sprint("f", i), "develop", sha(60+i)))
	}
	e.svc.sweepInterval = time.Hour
	e.svc.autoStartTimeout = 300 * time.Millisecond
	e.projects.mu.Lock()
	e.projects.branchesErr = fmt.Errorf("repo agent: connection reset")
	e.projects.mu.Unlock()
	if err := e.svc.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	e.autoIdle()
	if got := e.started(); got != "" {
		t.Fatalf("started %q with the repo unreadable", got)
	}

	gate := make(chan struct{})
	e.projects.mu.Lock()
	e.projects.branchesErr, e.projects.branchesGate = nil, gate
	e.projects.mu.Unlock()
	for _, id := range ids {
		e.svc.MergeRequestMoved(ctx, MergeRequestMove{UserID: e.owner, ProjectID: e.project, MergeRequestID: id, Opened: true})
	}
	// The first start waits on the repo past its time; the runs behind it
	// wait in the queue, one worker on the project.
	time.Sleep(400 * time.Millisecond)
	if waiting, workers := e.svc.auto.size(); workers != 1 || waiting < len(ids)-2 {
		t.Fatalf("%d runs waiting on %d workers behind a slow start", waiting, workers)
	}
	e.projects.mu.Lock()
	e.projects.branchesGate = nil
	e.projects.mu.Unlock()
	close(gate)
	e.autoIdle()
	if n := len(e.pipelines()); n == 0 || n >= len(ids) {
		t.Fatalf("%d of %d merge requests started, expected the first cut short: %s", n, len(ids), e.started())
	}
	// The catch-up starts the one cut short, and develop.
	e.svc.catchUp(ctx)
	e.autoIdle()
	byMR := map[int64]bool{}
	for _, p := range e.pipelines() {
		byMR[p.MergeRequestID] = true
	}
	for _, id := range ids {
		if !byMR[id] {
			t.Fatalf("merge request %d never started: %s", id, e.started())
		}
	}
	if !byMR[0] {
		t.Fatalf("develop never started: %s", e.started())
	}
}

// An automatic pipeline about to start whose subject no longer runs by
// itself, or any pipeline of a merge request no longer open, ends replaced,
// saying why; one started by hand on a branch runs whatever is ticked.
func TestPipelinesStartOnlyWhileTheyStillApply(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	e.tick("develop")
	e.svc.beginAutomatic()
	release := e.holdEnvironment()
	e.svc.BranchMoved(ctx, e.merged("develop", sha(3), ""))
	e.autoIdle()
	byHand := e.start(StartInput{Branch: "main"})
	other := e.addMergeRequest("hotfix", "develop", sha(40))
	byHandMR := e.start(StartInput{MergeRequestID: other})

	// No events: what changed is read as each starts.
	e.tick()
	e.mrs.set(MergeRequest{ID: e.mr, SourceBranch: "feature", TargetBranch: "develop", HeadSHA: sha(30), Merged: true})
	e.mrs.set(MergeRequest{ID: other, SourceBranch: "hotfix", TargetBranch: "develop", HeadSHA: sha(40)})
	release()
	for desc, want := range map[string]string{
		"develop@3 merged":                         "develop no longer runs pipelines by itself",
		fmt.Sprintf("!%d@1e/3 target_moved", e.mr): "the merge request was merged",
	} {
		if got := e.wait(e.find(desc).ID); got.Status != StatusSuperseded || got.Error != want || got.StartedAt != nil {
			t.Fatalf("%s: %s %q", desc, got.Status, got.Error)
		}
	}
	if got := e.wait(byHandMR.ID); got.Status != StatusSuperseded || got.Error != "the merge request was closed" {
		t.Fatalf("by hand, of a closed merge request: %s %q", got.Status, got.Error)
	}
	if got := e.wait(byHand.ID); got.Status != StatusPassed {
		t.Fatalf("by hand on main: %s %q", got.Status, got.Error)
	}

	// A branch no longer protected has nothing to test, whoever started it.
	release = e.holdEnvironment()
	unprotected := e.start(StartInput{Branch: "develop"})
	e.projects.mu.Lock()
	e.projects.project.ProtectedBranches = nil
	e.projects.mu.Unlock()
	release()
	if got := e.wait(unprotected.ID); got.Status != StatusSuperseded || got.Error != "develop is no longer protected" {
		t.Fatalf("by hand on a branch no longer protected: %s %q", got.Status, got.Error)
	}
}

// A merge request that closes or merges stops all its pipelines, running or
// queued, by hand or not.
func TestAnEndedMergeRequestStopsItsPipelines(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	e.tick("develop")
	e.svc.beginAutomatic()
	e.projects.setFile(sha(2), ".devpad/pipeline.yml", "jobs:\n  talk:\n    run: block hi\n")
	running := e.start(StartInput{MergeRequestID: e.mr})
	e.awaitScript("talk")
	release := e.holdEnvironment()
	defer release()
	other := e.addMergeRequest("hotfix", "develop", sha(40))
	e.svc.MergeRequestMoved(ctx, MergeRequestMove{UserID: e.owner, ProjectID: e.project, MergeRequestID: other, Opened: true})
	e.autoIdle()
	queued := e.find(fmt.Sprintf("!%d@28/2 opened", other))
	third := e.addMergeRequest("docs", "develop", sha(41))
	untouched := e.start(StartInput{MergeRequestID: third})

	e.svc.MergeRequestEnded(ctx, MergeRequestEnd{UserID: e.owner, ProjectID: e.project, MergeRequestID: e.mr, Merged: true})
	e.svc.MergeRequestEnded(ctx, MergeRequestEnd{UserID: e.owner, ProjectID: e.project, MergeRequestID: other})
	for p, want := range map[*Pipeline]string{running: "the merge request was merged", queued: "the merge request was closed"} {
		if got := e.wait(p.ID); got.Status != StatusSuperseded || got.Error != want {
			t.Fatalf("pipeline %d: %s %q", p.ID, got.Status, got.Error)
		}
	}
	if got, _ := e.svc.repo.Get(ctx, untouched.ID); got.Status != StatusQueued {
		t.Fatalf("another merge request's pipeline: %s", got.Status)
	}
}

// Unticking a branch stops the automatic pipelines of it and of the merge
// requests into it that have yet to start; one started by hand stays.
func TestUntickingStopsQueuedAutomaticPipelines(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	e.tick("develop", "main")
	e.svc.beginAutomatic()
	release := e.holdEnvironment()
	defer release()
	e.svc.BranchMoved(ctx, e.merged("develop", sha(3), ""))
	e.svc.BranchMoved(ctx, e.merged("main", sha(4), ""))
	e.autoIdle()
	other := e.addMergeRequest("hotfix", "develop", sha(40))
	byHand := e.start(StartInput{MergeRequestID: other})

	before := e.projects.current()
	e.tick("main")
	e.svc.ProjectSettingsChanged(ctx, before, e.projects.current())
	for _, desc := range []string{"develop@3 merged", fmt.Sprintf("!%d@1e/3 target_moved", e.mr)} {
		if got := e.wait(e.find(desc).ID); got.Status != StatusSuperseded || got.Error != "develop no longer runs pipelines by itself" {
			t.Fatalf("%s: %s %q", desc, got.Status, got.Error)
		}
	}
	for _, p := range []*Pipeline{byHand, e.find("main@4 merged")} {
		if got, _ := e.svc.repo.Get(ctx, p.ID); got.Status != StatusQueued {
			t.Fatalf("%s: %s", describe(got), got.Status)
		}
	}
}

// After a restart, automatic runs begin before the interrupted pipelines
// run again, and a re-run and an automatic start of the same commits and
// jobs are one pipeline, whichever goes first.
func TestARestartRunsEachSubjectOnce(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	e.tick("develop")
	release := e.holdEnvironment()
	byHand := e.start(StartInput{MergeRequestID: e.mr})
	branch := e.start(StartInput{Branch: "develop"})
	e.shutdown()
	release()

	e.svc = e.newService()
	if err := e.svc.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	// The re-runs go after the sweep; wait for both to have run.
	deadline := time.Now().Add(10 * time.Second)
	for {
		mr, _ := e.svc.repo.List(ctx, e.project, ListFilter{MergeRequestID: e.mr})
		br, _ := e.svc.repo.List(ctx, e.project, ListFilter{Branch: "develop"})
		if len(mr) >= 2 && len(br) >= 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("nothing ran again: %s", e.started())
		}
		time.Sleep(5 * time.Millisecond)
	}
	e.autoIdle()
	for _, p := range e.pipelines() {
		e.wait(p.ID)
	}
	time.Sleep(20 * time.Millisecond)
	for _, old := range []*Pipeline{byHand, branch} {
		if got, _ := e.svc.repo.Get(ctx, old.ID); got.Status != StatusInterrupted {
			t.Fatalf("pipeline %d: %s", old.ID, got.Status)
		}
	}
	mr, _ := e.svc.repo.List(ctx, e.project, ListFilter{MergeRequestID: e.mr})
	br, _ := e.svc.repo.List(ctx, e.project, ListFilter{Branch: "develop"})
	if len(mr) != 2 || len(br) != 2 {
		t.Fatalf("each subject should run once more: %s", e.started())
	}
}

// The catch-up runs with every sweep, so what an automatic run could not
// start, here because the repo could not be read, starts once it can be,
// with no event to say so.
func TestTheSweepCatchesUp(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	e.tick("develop")
	e.svc.sweepInterval = 20 * time.Millisecond
	e.projects.mu.Lock()
	e.projects.branchesErr = fmt.Errorf("repo agent: connection reset")
	e.projects.mu.Unlock()
	if err := e.svc.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	e.svc.MergeRequestMoved(ctx, MergeRequestMove{UserID: e.owner, ProjectID: e.project, MergeRequestID: e.mr, Opened: true})
	e.autoIdle()
	time.Sleep(50 * time.Millisecond)
	if got := e.started(); got != "" {
		t.Fatalf("started %q with the repo unreadable", got)
	}
	e.projects.mu.Lock()
	e.projects.branchesErr = nil
	e.projects.mu.Unlock()
	deadline := time.Now().Add(10 * time.Second)
	want := set("develop@2 catch_up", fmt.Sprintf("!%d@1e/2 catch_up", e.mr))
	for e.started() != want {
		if time.Now().After(deadline) {
			t.Fatalf("started %q, want %q", e.started(), want)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// A pipeline that could not run, devpad runs once more after a restart, and
// only once.
func TestARestartRetriesWhatCouldNotRunOnce(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	e.tick("develop")
	e.svc.beginAutomatic()
	e.projects.mu.Lock()
	e.projects.pinErr = fmt.Errorf("docker hiccup")
	e.projects.mu.Unlock()
	e.svc.MergeRequestMoved(ctx, MergeRequestMove{UserID: e.owner, ProjectID: e.project, MergeRequestID: e.mr, Opened: true})
	e.autoIdle()
	if failed := e.wait(e.pipelines()[0].ID); failed.Status != StatusError {
		t.Fatalf("status %s", failed.Status)
	}
	e.projects.mu.Lock()
	e.projects.pinErr = nil
	e.projects.mu.Unlock()
	for i := 0; i < 2; i++ {
		e.shutdown()
		e.svc = e.newService()
		if err := e.svc.Reconcile(ctx); err != nil {
			t.Fatal(err)
		}
		e.autoIdle()
		for _, p := range e.pipelines() {
			e.wait(p.ID)
		}
	}
	got, _ := e.svc.repo.List(ctx, e.project, ListFilter{MergeRequestID: e.mr})
	if len(got) != 2 || got[0].Trigger != TriggerCatchUp || got[0].Status != StatusPassed {
		t.Fatalf("after two restarts: %s", e.started())
	}
}

// A branch's automatic run that starts no pipeline, such as when its
// commit's pipeline was cancelled, tells nobody, as each catch-up does
// every ten minutes; only one a merge request's result waited on tells the
// BranchRunListeners, once, so that result is decided.
func TestABranchRunThatStartsNothingTellsOnlyWhatWaitedOnIt(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	e.tick("develop")
	e.svc.beginAutomatic()
	release := e.holdEnvironment()
	e.svc.queueBranch(e.owner, e.project, "develop", TriggerTicked, nil)
	e.autoIdle()
	var branch *Pipeline
	for _, p := range e.pipelines() {
		if p.Kind == KindBranch {
			branch = p
		}
	}
	if branch == nil {
		t.Fatalf("no branch pipeline: %s", e.started())
	}
	if _, err := e.svc.Cancel(ctx, e.owner, e.project, branch.ID); err != nil {
		t.Fatal(err)
	}
	release()
	e.wait(branch.ID)
	quiet := func(what string) {
		t.Helper()
		deadline := time.After(200 * time.Millisecond)
		for {
			select {
			case p := <-e.listener.finished:
				if p.ID == branch.ID && p.AnnouncedAt != nil {
					t.Fatalf("%s announced the cancelled pipeline again", what)
				}
			case b := <-e.listener.settled:
				t.Fatalf("%s told that %s's run settled", what, b)
			case <-deadline:
				return
			}
		}
	}
	quiet("the cancel")
	before := len(e.pipelines())
	for i := 0; i < 3; i++ {
		e.svc.queueBranch(e.owner, e.project, "develop", TriggerCatchUp, nil)
		e.autoIdle()
	}
	quiet("an idle catch-up")
	if got := len(e.pipelines()); got != before {
		t.Fatalf("the catch-up started a pipeline: %s", e.started())
	}

	// A merge request's result waits on the next run of develop, which
	// finds the same cancelled pipeline: the wait is told, once.
	e.shutdown()
	e.svc = e.newService()
	e.svc.queueBranch(e.owner, e.project, "develop", TriggerCatchUp, nil)
	mrp := &Pipeline{ProjectID: e.project, Kind: KindMergeRequest, MergeRequestID: e.mr, TargetBranch: "develop", TargetSHA: branch.HeadSHA, ConfigHash: branch.ConfigHash}
	if got, err := e.svc.targetResult(ctx, mrp, true); err != nil || !got.Pending {
		t.Fatalf("with develop's run queued: %+v %v", got, err)
	}
	e.svc.beginAutomatic()
	e.autoIdle()
	select {
	case b := <-e.listener.settled:
		if b != "develop" {
			t.Fatalf("settled %q", b)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the run that was waited on told nobody")
	}
	e.svc.queueBranch(e.owner, e.project, "develop", TriggerCatchUp, nil)
	e.autoIdle()
	quiet("the next catch-up")
}
