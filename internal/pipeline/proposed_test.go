package pipeline

import (
	"context"
	"errors"
	"fmt"
	"os"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"

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
	if again := e.start(StartInput{MergeRequestID: e.mr, Purpose: PurposeProposed, ReuseResult: true}); again.ID != proposed.ID {
		t.Fatalf("reused %d, want %d", again.ID, proposed.ID)
	}
	if again := e.start(StartInput{MergeRequestID: e.mr, ReuseResult: true}); again.ID != official.ID {
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
	release := e.holdEnvironment()
	e.propose(sha(30), proposedYAML)
	officialA := e.start(StartInput{MergeRequestID: e.mr})
	proposedA := e.start(StartInput{MergeRequestID: e.mr, Purpose: PurposeProposed})

	e.mrs.setHead(e.mr, sha(31))
	e.propose(sha(31), proposedYAML)
	officialB := e.start(StartInput{MergeRequestID: e.mr, StopStale: true})
	if got := e.wait(officialA.ID); got.Status != StatusSuperseded {
		t.Fatalf("the older official pipeline: %s", got.Status)
	}
	if got, _ := e.svc.repo.Get(context.Background(), proposedA.ID); got.Status != StatusQueued {
		t.Fatalf("a newer official pipeline replaced the proposed one: %s", got.Status)
	}
	proposedB := e.start(StartInput{MergeRequestID: e.mr, Purpose: PurposeProposed, StopStale: true})
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
// the current and latest pipelines, the badges, a settled result and the
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
	probe := key.probe(e.project, PurposeOfficial)
	countsNothing := func(when string, wantGate GateState) {
		t.Helper()
		st := official()
		g := st.Gate()
		if g.State != wantGate || (g.Pipeline != nil && g.Pipeline.Purpose != PurposeOfficial) {
			t.Fatalf("%s: gate %+v", when, g)
		}
		if st.Current != nil && st.Current.Purpose != PurposeOfficial || st.Latest != nil && st.Latest.Purpose != PurposeOfficial || st.Settled != nil && st.Settled.Pipeline.Purpose != PurposeOfficial {
			t.Fatalf("%s: status %+v", when, st)
		}
		if got, err := e.svc.LatestForMergeRequest(ctx, e.owner, e.project, key); err != nil || (got != nil && got.Purpose != PurposeOfficial) {
			t.Fatalf("%s: latest for the merge request %+v %v", when, got, err)
		}
		latest, err := e.svc.Latest(ctx, e.owner, e.project, LatestFilter{MergeRequestIDs: []int64{e.mr}, Purpose: PurposeProposed})
		if err != nil {
			t.Fatal(err)
		}
		for _, p := range latest.Pipelines {
			if p.Purpose != PurposeOfficial {
				t.Fatalf("%s: badge of %+v", when, p)
			}
		}
		for _, statuses := range [][]Status{countingStatuses, endedStatuses, {StatusPassed}} {
			if same, err := e.svc.repo.FindSame(ctx, probe, statuses); err != nil || (same != nil && same.Purpose != PurposeOfficial) {
				t.Fatalf("%s: the same pipeline as the gate's %+v %v", when, same, err)
			}
		}
		if ended, _, err := e.svc.settled(ctx, probe); err != nil || (ended != nil && ended.Purpose != PurposeOfficial) {
			t.Fatalf("%s: settled %+v %v", when, ended, err)
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

	// Ones that couldn't run, or were cancelled, settle nothing of it and
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
	ran := e.wait(e.start(StartInput{MergeRequestID: e.mr, Trigger: TriggerSourceMoved, Automatic: true, StopStale: true}).ID)
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
	probe = key.probe(e.project, PurposeOfficial)
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
