package pipeline

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/devpad-org/devpad/internal/agent"
	"github.com/devpad-org/devpad/internal/project"
)

const proposedYAML = "jobs:\n  lint:\n    run: echo linted\n"

// propose makes the merge request's head change the pipeline file, and the
// trial merge of head onto develop's tip hold yaml there; empty yaml
// removes it.
func (e *env) propose(head, yaml string) {
	e.t.Helper()
	e.projects.setChange(head, agent.RepoDiffFile{Path: ".devpad/pipeline.yml", Status: "M"})
	e.projects.mu.Lock()
	target := e.projects.branches["develop"]
	e.projects.mu.Unlock()
	if yaml != "" {
		e.projects.setFile(mergeOf(head, target), ".devpad/pipeline.yml", yaml)
	}
}

func (e *env) proposedStatus() *ProposedStatus {
	e.t.Helper()
	ctx := context.Background()
	official, err := e.svc.MergeRequestStatus(ctx, e.owner, e.project, e.mr)
	if err != nil {
		e.t.Fatalf("status: %v", err)
	}
	st, err := e.svc.ProposedStatus(ctx, e.owner, e.project, official)
	if err != nil {
		e.t.Fatalf("proposed status: %v", err)
	}
	return st
}

// A merge request whose change edits the pipeline file runs the jobs it
// proposes, read at the trial merge of its head onto its target and run on
// that very commit, beside its official pipeline of the target's jobs.
func TestAProposedPipelineRunsTheChangesJobsOnTheTrialMerge(t *testing.T) {
	e := setup(t)
	if st := e.proposedStatus(); st != nil {
		t.Fatalf("a change that leaves the pipeline file alone: %+v", st)
	}
	e.mrs.setHead(e.mr, sha(31))
	e.propose(sha(31), proposedYAML)
	official := e.wait(e.start(StartInput{MergeRequestID: e.mr}).ID)
	proposed := e.wait(e.start(StartInput{MergeRequestID: e.mr, Purpose: PurposeProposed}).ID)
	if official.ID == proposed.ID || official.Purpose != PurposeOfficial || proposed.Purpose != PurposeProposed {
		t.Fatalf("expected two pipelines: %+v %+v", official, proposed)
	}
	names := func(p *Pipeline) []string {
		var out []string
		for _, j := range p.Jobs {
			out = append(out, j.Name)
		}
		return out
	}
	if got := names(official); !slices.Equal(got, []string{"build", "test"}) {
		t.Fatalf("official jobs %v, want the target's", got)
	}
	merged := mergeOf(sha(31), sha(2))
	if got := names(proposed); !slices.Equal(got, []string{"lint"}) || proposed.Status != StatusPassed || proposed.TestedSHA != merged || proposed.ConfigPath != ".devpad/pipeline.yml" || proposed.ConfigSource != SourceFile {
		t.Fatalf("proposed: %v %+v", got, proposed)
	}
	if out := e.output(proposed, "lint"); !strings.Contains(out, "linted") {
		t.Fatalf("lint printed %q", out)
	}
	st := e.proposedStatus()
	if st == nil || st.File != ".devpad/pipeline.yml" || st.Current == nil || st.Current.ID != proposed.ID || st.Latest.ID != proposed.ID || st.Key.ConfigHash != proposed.ConfigHash || st.Removed || st.Conflict || st.Invalid != "" {
		t.Fatalf("proposed status: %+v", st)
	}
	// Starting either again finds its own.
	if again := e.start(StartInput{MergeRequestID: e.mr, Purpose: PurposeProposed, Trigger: TriggerRestart}); again.ID != proposed.ID {
		t.Fatalf("reused %d, want %d", again.ID, proposed.ID)
	}
	if again := e.start(StartInput{MergeRequestID: e.mr, Trigger: TriggerRestart}); again.ID != official.ID {
		t.Fatalf("reused %d, want %d", again.ID, official.ID)
	}
	// The change and its trial merge are read once for each pair of
	// commits: the first head's change, then the second's and its merge.
	e.projects.mu.Lock()
	diffs, merges := e.projects.diffs, e.projects.trialMerges
	e.projects.mu.Unlock()
	if diffs != 2 || merges != 1 {
		t.Fatalf("read the change %d times and merged %d times", diffs, merges)
	}
}

// Nothing runs when the change proposes no jobs to run: it leaves the
// pipeline file alone, removes it, or doesn't merge; one that doesn't parse
// is recorded with its error, as an official one is. The project's setting
// never stands in for the change's file.
func TestAProposedPipelineRunsOnlyWhatTheChangeProposes(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	start := func(in StartInput) error {
		in.UserID, in.ProjectID, in.Trigger = e.owner, e.project, TriggerUser
		_, _, err := e.svc.Start(ctx, in)
		return err
	}
	if err := start(StartInput{MergeRequestID: e.mr, Purpose: PurposeProposed}); !errors.Is(err, ErrPipelineUnchanged) {
		t.Fatalf("leaving the file alone: %v", err)
	}
	if err := start(StartInput{Branch: "develop", Purpose: PurposeProposed}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("a branch's proposed pipeline: %v", err)
	}
	if err := start(StartInput{MergeRequestID: e.mr, Purpose: "secret"}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("an unknown purpose: %v", err)
	}

	e.projects.setProject(func(p *project.Project) { p.PipelineConfig = "jobs:\n  setting:\n    run: pass\n" })
	e.mrs.setHead(e.mr, sha(31))
	e.propose(sha(31), "")
	if err := start(StartInput{MergeRequestID: e.mr, Purpose: PurposeProposed}); !errors.Is(err, ErrPipelineRemoved) {
		t.Fatalf("removing the file: %v", err)
	}
	if st := e.proposedStatus(); st == nil || !st.Removed || st.Current != nil {
		t.Fatalf("removed: %+v", st)
	}

	e.mrs.setHead(e.mr, sha(32))
	e.propose(sha(32), "jobs: {}\n")
	invalid := e.start(StartInput{MergeRequestID: e.mr, Purpose: PurposeProposed})
	if invalid.Status != StatusInvalid || invalid.Error == "" || len(invalid.Jobs) != 0 {
		t.Fatalf("invalid: %+v", invalid)
	}
	if st := e.proposedStatus(); st == nil || st.Invalid == "" || st.Current == nil || st.Current.ID != invalid.ID {
		t.Fatalf("invalid status: %+v", st)
	}

	e.mrs.setHead(e.mr, sha(33))
	e.propose(sha(33), proposedYAML)
	e.projects.mu.Lock()
	e.projects.conflict = &agent.MergeConflict{Message: "the source conflicts with the target", Paths: []string{".devpad/pipeline.yml"}}
	e.projects.mu.Unlock()
	if err := start(StartInput{MergeRequestID: e.mr, Purpose: PurposeProposed}); !errors.Is(err, ErrInvalidState) || !strings.Contains(err.Error(), "doesn't merge onto develop") {
		t.Fatalf("a conflict: %v", err)
	}
	if st := e.proposedStatus(); st == nil || !st.Conflict {
		t.Fatalf("conflict status: %+v", st)
	}
	for _, p := range e.pipelines() {
		if p.Purpose == PurposeProposed && p.ID != invalid.ID {
			t.Errorf("pipeline %d made for a change that proposes nothing to run", p.ID)
		}
	}
}

// A proposed pipeline and an official one of the same merge request never
// replace each other: each is replaced only by a newer one of its own.
func TestProposedAndOfficialPipelinesReplaceOnlyTheirOwn(t *testing.T) {
	e := setup(t)
	e.tick("develop")
	release := e.holdEnvironment()
	e.propose(sha(30), proposedYAML)
	officialA := e.start(StartInput{MergeRequestID: e.mr})
	proposedA := e.start(StartInput{MergeRequestID: e.mr, Purpose: PurposeProposed})

	e.mrs.setHead(e.mr, sha(31))
	e.propose(sha(31), proposedYAML)
	officialB := e.start(StartInput{MergeRequestID: e.mr, Trigger: TriggerSourceMoved})
	if got := e.wait(officialA.ID); got.Status != StatusSuperseded {
		t.Fatalf("the older official pipeline: %s", got.Status)
	}
	if got, _ := e.svc.repo.Get(context.Background(), proposedA.ID); got.Status != StatusQueued {
		t.Fatalf("a newer official pipeline replaced the proposed one: %s", got.Status)
	}
	proposedB := e.start(StartInput{MergeRequestID: e.mr, Purpose: PurposeProposed, Trigger: TriggerSourceMoved})
	if got := e.wait(proposedA.ID); got.Status != StatusSuperseded || !strings.Contains(got.Error, fmt.Sprintf("pipeline %d", proposedB.ID)) {
		t.Fatalf("the older proposed pipeline: %s %q", got.Status, got.Error)
	}
	release()
	if got := e.wait(officialB.ID); got.Status != StatusPassed {
		t.Fatalf("official: %s", got.Status)
	}
	if got := e.wait(proposedB.ID); got.Status != StatusPassed {
		t.Fatalf("proposed: %s", got.Status)
	}
}

// A merge request that closes stops its pipelines of both purposes.
func TestAnEndedMergeRequestStopsItsProposedPipelineToo(t *testing.T) {
	e := setup(t)
	release := e.holdEnvironment()
	defer release()
	e.propose(sha(30), proposedYAML)
	official := e.start(StartInput{MergeRequestID: e.mr})
	proposed := e.start(StartInput{MergeRequestID: e.mr, Purpose: PurposeProposed})
	e.svc.MergeRequestEnded(context.Background(), MergeRequestEnd{UserID: e.owner, ProjectID: e.project, MergeRequestID: e.mr})
	for _, p := range []*Pipeline{official, proposed} {
		if got := e.wait(p.ID); got.Status != StatusSuperseded || got.Error != "the merge request was closed" {
			t.Fatalf("pipeline %d (%s): %s %q", p.ID, p.Purpose, got.Status, got.Error)
		}
	}
}

// Into a ticked branch, the merge request's run starts the pipeline its
// change proposes after its official one, under the same run.
func TestAnAutomaticRunStartsTheProposedPipelineToo(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	e.tick("develop")
	e.svc.beginAutomatic()
	e.svc.MergeRequestMoved(ctx, MergeRequestMove{UserID: e.owner, ProjectID: e.project, MergeRequestID: e.mr, Opened: true})
	e.autoIdle()
	mr := fmt.Sprintf("!%d", e.mr)
	if got, want := e.started(), set(mr+"@1e/2 opened"); got != want {
		t.Fatalf("a change that leaves the file alone started %q, want %q", got, want)
	}

	e.mrs.setHead(e.mr, sha(31))
	e.propose(sha(31), proposedYAML)
	e.svc.MergeRequestMoved(ctx, MergeRequestMove{UserID: e.owner, ProjectID: e.project, MergeRequestID: e.mr})
	e.autoIdle()
	for _, p := range e.pipelines() {
		e.wait(p.ID)
	}
	if got, want := e.started(), set(mr+"@1e/2 opened", mr+"@1f/2 source_moved", mr+"@1f/2 source_moved proposed"); got != want {
		t.Fatalf("started %q, want %q", got, want)
	}
	// Run again by itself, it reuses both results.
	e.svc.MergeRequestMoved(ctx, MergeRequestMove{UserID: e.owner, ProjectID: e.project, MergeRequestID: e.mr})
	e.autoIdle()
	if n := len(e.pipelines()); n != 3 {
		t.Fatalf("%d pipelines after running the same commits again", n)
	}
}

// A head whose change no longer proposes a pipeline stops the proposed
// pipeline of the head before, when devpad runs the merge request by
// itself.
func TestAProposedPipelineStopsWhenTheChangeNoLongerProposesOne(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	e.tick("develop")
	e.svc.beginAutomatic()
	release := e.holdEnvironment()
	defer release()
	e.propose(sha(30), proposedYAML)
	e.svc.MergeRequestMoved(ctx, MergeRequestMove{UserID: e.owner, ProjectID: e.project, MergeRequestID: e.mr, Opened: true})
	e.autoIdle()
	mr := fmt.Sprintf("!%d", e.mr)
	old := e.find(mr + "@1e/2 opened proposed")

	// The new head leaves the file as the target has it.
	e.mrs.setHead(e.mr, sha(31))
	e.svc.MergeRequestMoved(ctx, MergeRequestMove{UserID: e.owner, ProjectID: e.project, MergeRequestID: e.mr})
	e.autoIdle()
	if got := e.wait(old.ID); got.Status != StatusSuperseded || got.Error != ErrPipelineUnchanged.Error() {
		t.Fatalf("the old proposed pipeline: %s %q", got.Status, got.Error)
	}
	// By hand, a refusal stops nothing.
	e.mrs.setHead(e.mr, sha(32))
	e.propose(sha(32), proposedYAML)
	kept := e.start(StartInput{MergeRequestID: e.mr, Purpose: PurposeProposed})
	e.mrs.setHead(e.mr, sha(33))
	if _, _, err := e.svc.Start(ctx, StartInput{UserID: e.owner, ProjectID: e.project, MergeRequestID: e.mr, Purpose: PurposeProposed, Trigger: TriggerUser}); !errors.Is(err, ErrPipelineUnchanged) {
		t.Fatalf("by hand: %v", err)
	}
	if got, _ := e.svc.repo.Get(ctx, kept.ID); got.Status != StatusQueued {
		t.Fatalf("a refused start by hand stopped %d: %s", kept.ID, got.Status)
	}
}

// A proposed pipeline devpad stopped is run again as a proposed one.
func TestARestartRunsAProposedPipelineAgainAsProposed(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	e.propose(sha(30), proposedYAML)
	left := &Pipeline{
		ProjectID: e.project, UserID: e.owner, UID: "ffffffffffffff01", Kind: KindMergeRequest, Purpose: PurposeProposed,
		MergeRequestID: e.mr, SourceBranch: "feature", TargetBranch: "develop", HeadSHA: sha(30), TargetSHA: sha(2),
		ConfigSource: SourceFile, Status: StatusRunning, Trigger: TriggerUser, CreatedAt: e.svc.now(),
	}
	if err := e.svc.repo.Create(ctx, left); err != nil {
		t.Fatal(err)
	}
	e.svc = e.newService()
	if err := e.svc.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	got := e.wait(e.awaitRerun(left.ID).ID)
	if got.Purpose != PurposeProposed || got.Trigger != TriggerRestart || got.Status != StatusPassed || len(got.Jobs) != 1 || got.Jobs[0].Name != "lint" {
		t.Fatalf("rerun: %+v", got)
	}
	if n := len(e.pipelines()); n != 2 {
		t.Fatalf("%d pipelines, want the interrupted one and its rerun", n)
	}
}

// Every path that reads a merge request's result, or acts on one, sees
// only its official pipelines: the merge gate and the pipeline it names,
// the current and latest pipelines, the badges, one without a result and the
// errors counted towards running it again, and reusing a result. A
// passing proposed pipeline never opens the gate, and one that fails or
// can't run never shuts it or stands for the merge request's own.
func TestProposedPipelinesNeverCountForTheMergeRequest(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	e.tick("develop")
	e.propose(sha(30), proposedYAML)

	official := func() *MergeRequestStatus {
		t.Helper()
		st, err := e.svc.MergeRequestStatus(ctx, e.owner, e.project, e.mr)
		if err != nil {
			t.Fatal(err)
		}
		return st
	}
	key := official().Key
	probe := key.of(e.project, PurposeOfficial)
	countsNothing := func(when string, wantGate GateState) {
		t.Helper()
		st := official()
		g := st.Gate()
		if g.State != wantGate || (g.Pipeline != nil && g.Pipeline.Purpose != PurposeOfficial) {
			t.Fatalf("%s: gate %+v", when, g)
		}
		if st.Current != nil && st.Current.Purpose != PurposeOfficial || st.Latest != nil && st.Latest.Purpose != PurposeOfficial || st.NoResult != nil && st.NoResult.Pipeline.Purpose != PurposeOfficial {
			t.Fatalf("%s: status %+v", when, st)
		}
		if got, err := e.svc.current(ctx, probe); err != nil || (got != nil && got.Purpose != PurposeOfficial) {
			t.Fatalf("%s: latest for the merge request %+v %v", when, got, err)
		}
		if _, err := e.svc.Latest(ctx, e.owner, e.project, LatestFilter{MergeRequestIDs: []int64{e.mr}, Purpose: PurposeProposed}); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("%s: badges of proposed pipelines: %v", when, err)
		}
		latest, err := e.svc.Latest(ctx, e.owner, e.project, LatestFilter{MergeRequestIDs: []int64{e.mr}})
		if err != nil {
			t.Fatal(err)
		}
		for _, p := range latest.MergeRequests {
			if p.Purpose != PurposeOfficial {
				t.Fatalf("%s: badge of %+v", when, p)
			}
		}
		for _, statuses := range [][]Status{countingStatuses, endedStatuses, {StatusPassed}} {
			if same, err := e.svc.repo.FindSame(ctx, probe, statuses); err != nil || (same != nil && same.Purpose != PurposeOfficial) {
				t.Fatalf("%s: the same pipeline as the gate's %+v %v", when, same, err)
			}
		}
		if ended, _, err := e.svc.lastEnded(ctx, probe); err != nil || (ended != nil && ended.Purpose != PurposeOfficial) {
			t.Fatalf("%s: ended without a result %+v %v", when, ended, err)
		}
	}

	// A proposed pipeline that passes leaves the gate as if it never ran.
	passed := e.wait(e.start(StartInput{MergeRequestID: e.mr, Purpose: PurposeProposed}).ID)
	if passed.Status != StatusPassed {
		t.Fatalf("proposed: %s", passed.Status)
	}
	countsNothing("after a passing proposed pipeline", GateNotRun)
	if n, err := e.svc.repo.CountSame(ctx, probe, []Status{StatusPassed}); err != nil || n != 0 {
		t.Fatalf("counted %d official passes %v", n, err)
	}

	// Ones that couldn't run, or were cancelled, stand for nothing of it and
	// count towards no retry.
	e.projects.mu.Lock()
	e.projects.envErr = errors.New("the build broke")
	e.projects.mu.Unlock()
	for range 2 {
		erred := e.wait(e.start(StartInput{MergeRequestID: e.mr, Purpose: PurposeProposed}).ID)
		if erred.Status != StatusError {
			t.Fatalf("expected an error: %+v", erred)
		}
	}
	e.projects.mu.Lock()
	e.projects.envErr = nil
	e.projects.mu.Unlock()
	countsNothing("after proposed pipelines that couldn't run", GateNotRun)
	if n, err := e.svc.repo.CountSame(ctx, probe, []Status{StatusError}); err != nil || n != 0 {
		t.Fatalf("counted %d official errors %v", n, err)
	}

	// Devpad's own start of the official pipeline makes one of its own,
	// reusing nothing proposed.
	ran := e.wait(e.start(StartInput{MergeRequestID: e.mr, Trigger: TriggerSourceMoved}).ID)
	if ran.Purpose != PurposeOfficial || ran.ID == passed.ID {
		t.Fatalf("the official start reused %+v", ran)
	}
	countsNothing("after an official pass", GatePassed)

	// A newer failing official pipeline shuts the gate whatever passed of
	// the change's own.
	e.projects.setFile(sha(3), ".devpad/pipeline.yml", "jobs:\n  test:\n    run: fail 1\n")
	e.projects.setBranch("develop", sha(3))
	e.propose(sha(30), proposedYAML)
	key = official().Key
	probe = key.of(e.project, PurposeOfficial)
	failed := e.wait(e.start(StartInput{MergeRequestID: e.mr}).ID)
	again := e.wait(e.start(StartInput{MergeRequestID: e.mr, Purpose: PurposeProposed}).ID)
	if failed.Status != StatusFailed || again.Status != StatusPassed {
		t.Fatalf("official %s, proposed %s", failed.Status, again.Status)
	}
	countsNothing("after a failing official pipeline", GateFailed)
	if g := official().Gate(); g.Pipeline.ID != failed.ID {
		t.Fatalf("the gate names %d, want %d", g.Pipeline.ID, failed.ID)
	}
}

// The app's PipelinePurpose lists the purposes the server has.
func TestPurposesMatchTheFrontend(t *testing.T) {
	source, err := os.ReadFile("../../frontend/src/api/pipelines.ts")
	if err != nil {
		t.Fatalf("reading the frontend's pipelines API: %v", err)
	}
	block := regexp.MustCompile(`export type PipelinePurpose =([^\n]*)`).FindSubmatch(source)
	if block == nil {
		t.Fatal("cannot find PipelinePurpose in frontend/src/api/pipelines.ts")
	}
	var frontend, server []string
	for _, m := range regexp.MustCompile(`'([a-z_]+)'`).FindAllSubmatch(block[1], -1) {
		frontend = append(frontend, string(m[1]))
	}
	for _, p := range Purposes() {
		server = append(server, string(p))
	}
	sort.Strings(frontend)
	sort.Strings(server)
	if !slices.Equal(frontend, server) {
		t.Errorf("the server's purposes are %v, the frontend's PipelinePurpose %v", server, frontend)
	}
}

// A proposed pipeline can have the very key of the merge request's
// official one, in all but its purpose: a change that edits only a comment
// of the pipeline file, or renames .yml to .yaml, proposes the same jobs,
// so the same jobs hash. Devpad refuses to run such a pipeline (see
// TestAChangeThatProposesTheSameJobsRunsNothingTwice), so these rows are
// put in as an older devpad, or a bug, might have left them; still not one
// of them opens or shuts the gate, stands for the merge request's result,
// is reused for it, stands for it without a result, or counts towards
// running it again. Only the purpose tells them apart.
func TestAProposedPipelineOfTheOfficialKeyNeverStandsForIt(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	e.tick("develop")
	e.propose(sha(30), jobsYAML+"# only a comment\n")
	status := func() *MergeRequestStatus {
		t.Helper()
		st, err := e.svc.MergeRequestStatus(ctx, e.owner, e.project, e.mr)
		if err != nil {
			t.Fatal(err)
		}
		return st
	}
	key := status().Key
	now := e.svc.now()
	for i, s := range []Status{StatusPassed, StatusFailed, StatusError, StatusError, StatusCancelled} {
		p := &Pipeline{
			ProjectID: e.project, UserID: e.owner, UID: fmt.Sprintf("ffffffffffffff%02d", 10+i), Kind: KindMergeRequest, Purpose: PurposeProposed,
			MergeRequestID: e.mr, SourceBranch: "feature", TargetBranch: "develop", HeadSHA: key.HeadSHA, TargetSHA: key.TargetSHA,
			ConfigSource: SourceFile, ConfigPath: ".devpad/pipeline.yml", ConfigHash: key.ConfigHash, Status: s, Trigger: TriggerUser,
			CreatedAt: now, StartedAt: &now, FinishedAt: &now,
		}
		if err := e.svc.repo.Create(ctx, p); err != nil {
			t.Fatal(err)
		}
	}
	probe := key.of(e.project, PurposeOfficial)

	st := status()
	if g := st.Gate(); g.State != GateNotRun || st.Current != nil || st.Latest != nil || st.NoResult != nil {
		t.Fatalf("a proposed pipeline of the official key counted: gate %s, %+v", g.State, st)
	}
	if got, err := e.svc.current(ctx, probe); err != nil || got != nil {
		t.Fatalf("latest for the merge request: %+v %v", got, err)
	}
	if latest, err := e.svc.Latest(ctx, e.owner, e.project, LatestFilter{MergeRequestIDs: []int64{e.mr}}); err != nil || len(latest.MergeRequests) != 0 {
		t.Fatalf("badges: %+v %v", latest, err)
	}
	for _, statuses := range [][]Status{countingStatuses, endedStatuses, resultStatuses} {
		if same, err := e.svc.repo.FindSame(ctx, probe, statuses); err != nil || same != nil {
			t.Fatalf("found %+v %v", same, err)
		}
	}
	if ended, _, err := e.svc.lastEnded(ctx, probe); err != nil || ended != nil {
		t.Fatalf("ended without a result: %+v %v", ended, err)
	}
	for _, s := range []Status{StatusPassed, StatusError, StatusCancelled} {
		if n, err := e.svc.repo.CountSame(ctx, probe, []Status{s}); err != nil || n != 0 {
			t.Fatalf("counted %d %s %v", n, s, err)
		}
	}
	// A restart's start, which would take a result of its key, and
	// devpad's own, which would take a cancelled pipeline or two errors of
	// it as ending without a result, each run or reuse only an official
	// pipeline.
	for i, in := range []StartInput{
		{Trigger: TriggerRestart},
		{Trigger: TriggerSourceMoved},
	} {
		in.UserID, in.ProjectID, in.MergeRequestID = e.owner, e.project, e.mr
		ran, created, err := e.svc.Start(ctx, in)
		if err != nil || (i == 0 && !created) || ran.Purpose != PurposeOfficial {
			t.Fatalf("%s: %+v created %v %v", in.Trigger, ran, created, err)
		}
		e.wait(ran.ID)
	}
	if g := status().Gate(); g.State != GatePassed || g.Pipeline.Purpose != PurposeOfficial {
		t.Fatalf("after the official pipeline: %+v", g)
	}
}

// A change that edits only a comment of the pipeline file, or renames it
// to .yaml, proposes the very jobs the merge request's own pipeline runs:
// nothing runs twice, and the status says why.
func TestAChangeThatProposesTheSameJobsRunsNothingTwice(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	for i, change := range []struct {
		file agent.RepoDiffFile
		yaml string
	}{
		{agent.RepoDiffFile{Path: ".devpad/pipeline.yml", Status: "M"}, jobsYAML + "# only a comment\n"},
		{agent.RepoDiffFile{Path: ".devpad/pipeline.yaml", OldPath: ".devpad/pipeline.yml", Status: "R"}, jobsYAML},
	} {
		head, target := sha(40+i), sha(80+i)
		e.projects.setFile(target, ".devpad/pipeline.yml", jobsYAML)
		e.projects.setBranch("develop", target)
		e.mrs.setHead(e.mr, head)
		e.projects.setChange(head, change.file)
		e.projects.setFile(mergeOf(head, target), change.file.Path, change.yaml)
		if _, _, err := e.svc.Start(ctx, StartInput{UserID: e.owner, ProjectID: e.project, MergeRequestID: e.mr, Purpose: PurposeProposed, Trigger: TriggerUser}); !errors.Is(err, ErrSameJobs) {
			t.Fatalf("%s: started %v", change.file.Path, err)
		}
		if st := e.proposedStatus(); st == nil || !st.SameJobs || st.Current != nil || st.Key.ConfigHash != "" {
			t.Fatalf("%s: status %+v", change.file.Path, st)
		}
	}
	if n := len(e.pipelines()); n != 0 {
		t.Fatalf("%d pipelines ran", n)
	}
}

// A proposed job runs at most as long as the target's longest job, or the
// default timeout when the target has no pipeline: a change can't make its
// own jobs hold a job slot longer than the jobs that count. Limited so, a
// job the target already runs is the target's own.
func TestProposedJobsRunNoLongerThanTheTargetsLongest(t *testing.T) {
	e := setup(t)
	timeouts := func(p *Pipeline) map[string]time.Duration {
		out := map[string]time.Duration{}
		for _, j := range p.Jobs {
			out[j.Name] = j.Timeout
		}
		return out
	}
	long := "jobs:\n  lint:\n    run: echo linted\n    timeout: 1h\n  quick:\n    run: echo quick\n    timeout: 2m\n"
	steps := []struct {
		target string // develop's pipeline file, "" for none
		want   map[string]time.Duration
	}{
		{jobsYAML, map[string]time.Duration{"lint": DefaultTimeout, "quick": 2 * time.Minute}},
		{"jobs:\n  build:\n    run: echo built\n    timeout: 20m\n", map[string]time.Duration{"lint": 20 * time.Minute, "quick": 2 * time.Minute}},
		{"", map[string]time.Duration{"lint": DefaultTimeout, "quick": 2 * time.Minute}},
	}
	for i, step := range steps {
		target, head := sha(50+i), sha(60+i)
		if step.target != "" {
			e.projects.setFile(target, ".devpad/pipeline.yml", step.target)
		}
		e.projects.setBranch("develop", target)
		e.mrs.setHead(e.mr, head)
		e.propose(head, long)
		p := e.wait(e.start(StartInput{MergeRequestID: e.mr, Purpose: PurposeProposed}).ID)
		if got := timeouts(p); !maps.Equal(got, step.want) || p.Status != StatusPassed {
			t.Fatalf("step %d: %v %s", i, got, p.Status)
		}
		if st := e.proposedStatus(); st.Current == nil || st.Current.ID != p.ID || st.Key.ConfigHash != p.ConfigHash {
			t.Fatalf("step %d: status %+v, pipeline hash %s", i, st, p.ConfigHash)
		}
	}
	// Asking an hour of the target's own job asks for nothing new.
	e.projects.setBranch("develop", sha(2))
	e.mrs.setHead(e.mr, sha(70))
	e.propose(sha(70), "jobs:\n  build:\n    run: echo built\n    timeout: 1h\n  test:\n    run: pass\n")
	if _, _, err := e.svc.Start(context.Background(), StartInput{UserID: e.owner, ProjectID: e.project, MergeRequestID: e.mr, Purpose: PurposeProposed, Trigger: TriggerUser}); !errors.Is(err, ErrSameJobs) {
		t.Fatalf("the target's own jobs, longer: %v", err)
	}
}

// A proposed pipeline runs only on the trial merge its jobs were read
// from: a pin that tests another commit ends it in error.
func TestAProposedPipelineRunsOnlyOnTheCommitItsJobsCameFrom(t *testing.T) {
	e := setup(t)
	e.mrs.setHead(e.mr, sha(31))
	e.propose(sha(31), proposedYAML)
	e.projects.mu.Lock()
	e.projects.pinShift = sha(99)
	e.projects.mu.Unlock()
	p := e.wait(e.start(StartInput{MergeRequestID: e.mr, Purpose: PurposeProposed}).ID)
	if p.ConfigSHA != mergeOf(sha(31), sha(2)) || p.Status != StatusError || !strings.Contains(p.Error, "not "+p.ConfigSHA) {
		t.Fatalf("proposed: %s %q, read from %s", p.Status, p.Error, p.ConfigSHA)
	}
	if len(p.Jobs) == 0 || p.Jobs[0].Status == JobPassed {
		t.Fatalf("its jobs ran: %+v", p.Jobs)
	}
	// The official pipeline reads its jobs from the target, and tests the
	// trial merge.
	official := e.wait(e.start(StartInput{MergeRequestID: e.mr}).ID)
	if official.ConfigSHA != sha(2) || official.Status != StatusPassed || official.TestedSHA != sha(99) {
		t.Fatalf("official: %+v", official)
	}
}

// A change devpad can't diff, such as one that shares no history with its
// target, is read as the trial merge says: one that conflicts proposes
// nothing, for good. Any other failure stands for proposalRetry, so a
// merge request polled for its status asks the repo again only after it.
func TestAChangeThatCantBeReadIsAskedAgainOnlyLater(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	e.mrs.setHead(e.mr, sha(31))
	e.propose(sha(31), proposedYAML)
	e.projects.mu.Lock()
	e.projects.diffErr = errors.New("the repo is busy")
	e.projects.mu.Unlock()
	official, err := e.svc.MergeRequestStatus(ctx, e.owner, e.project, e.mr)
	if err != nil {
		t.Fatal(err)
	}
	reads := func() (int, int) {
		e.projects.mu.Lock()
		defer e.projects.mu.Unlock()
		return e.projects.diffs, e.projects.trialMerges
	}
	if _, err := e.svc.ProposedStatus(ctx, e.owner, e.project, official); err == nil || isRecentFailure(err) {
		t.Fatalf("first: %v", err)
	}
	diffs, merges := reads()
	for range 3 {
		if _, err := e.svc.ProposedStatus(ctx, e.owner, e.project, official); !isRecentFailure(err) || !strings.Contains(err.Error(), "the repo is busy") {
			t.Fatalf("again: %v", err)
		}
	}
	if d, m := reads(); d != diffs || m != merges {
		t.Fatalf("asked the repo again: %d diffs, %d trial merges", d-diffs, m-merges)
	}
	// Once it has stood long enough, and the repo answers, it is read.
	key := proposalKey{projectID: e.project, head: sha(31), target: sha(2)}
	e.svc.proposals.mu.Lock()
	f := e.svc.proposals.failed[key]
	f.until = e.svc.now().Add(-time.Second)
	e.svc.proposals.failed[key] = f
	e.svc.proposals.mu.Unlock()
	e.projects.mu.Lock()
	e.projects.diffErr = nil
	e.projects.mu.Unlock()
	if st, err := e.svc.ProposedStatus(ctx, e.owner, e.project, official); err != nil || st == nil || st.Key.ConfigHash == "" {
		t.Fatalf("read again: %+v %v", st, err)
	}

	// No history in common: the diff fails, and the trial merge conflicts.
	e.mrs.setHead(e.mr, sha(32))
	e.projects.mu.Lock()
	e.projects.diffErr = errors.New("no merge base")
	e.projects.conflict = &agent.MergeConflict{Message: "refusing to merge unrelated histories"}
	e.projects.mu.Unlock()
	if _, _, err := e.svc.Start(ctx, StartInput{UserID: e.owner, ProjectID: e.project, MergeRequestID: e.mr, Purpose: PurposeProposed, Trigger: TriggerUser}); err == nil || !strings.Contains(err.Error(), "doesn't merge onto develop") {
		t.Fatalf("unrelated histories: %v", err)
	}
	diffs, merges = reads()
	if _, _, err := e.svc.Start(ctx, StartInput{UserID: e.owner, ProjectID: e.project, MergeRequestID: e.mr, Purpose: PurposeProposed, Trigger: TriggerUser}); err == nil || isRecentFailure(err) {
		t.Fatalf("again: %v", err)
	}
	if d, m := reads(); d != diffs || m != merges {
		t.Fatalf("read the unrelated change again: %d diffs, %d trial merges", d-diffs, m-merges)
	}
}
