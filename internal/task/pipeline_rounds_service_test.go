package task

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/devpad-org/devpad/internal/ai/domain"
	"github.com/devpad-org/devpad/internal/project"
)

// reviewTask is a task whose agent finished a round at head.
func (e *env) reviewTask(t *testing.T, head string) *Task {
	t.Helper()
	tk := e.createTask(t)
	tk.Status, tk.HeadSHA = StatusInReview, head
	if err := e.repo.Update(context.Background(), tk); err != nil {
		t.Fatal(err)
	}
	return tk
}

// pipelineEnded tells the task how its merge request's pipeline ended.
func (e *env) pipelineEnded(t *testing.T, id int64, res PipelineResult) *Task {
	t.Helper()
	got, err := e.svc.PipelineEnded(context.Background(), owner, 1, id, res)
	if err != nil {
		t.Fatalf("pipeline ended: %v", err)
	}
	return got
}

// roundDone waits for the agent the task was sent back to, and ends its
// round with the branch at head. It returns what the agent was told.
func (e *env) roundDone(t *testing.T, id int64, head string) string {
	t.Helper()
	running := e.waitTask(t, id, hasRun)
	reqs := e.runs.startedRequests()
	prompt := reqs[len(reqs)-1].Message.Text()
	e.runs.finish(running.RunID, domain.AgentRunCompleted, "")
	done := e.waitTask(t, id, status(StatusInReview))
	done.HeadSHA = head
	if err := e.repo.Update(context.Background(), done); err != nil {
		t.Fatal(err)
	}
	return prompt
}

// on is res as of the task's branch at head.
func on(head string, res PipelineResult) PipelineResult {
	res.HeadSHA = head
	return res
}

func (e *env) stored(t *testing.T, id int64) *Task {
	t.Helper()
	got, err := e.repo.GetByID(context.Background(), id)
	if err != nil || got == nil {
		t.Fatalf("task %d: %v", id, err)
	}
	return got
}

// A failed pipeline sends the task back to its agent with what failed,
// twice in a row; the third failure waits for the user, as feedback for
// the next run, which starting the task sends.
func TestPipelineFailuresGoBackTwiceThenWait(t *testing.T) {
	e := setup(t)
	tk := e.reviewTask(t, "h1")

	got := e.pipelineEnded(t, tk.ID, failure(5, failedJob("backend", "cart_test.go:12: got 1\n")))
	if got.Status != StatusQueued || got.PipelineRounds.Count != 1 || got.PipelineRounds.Held != HoldNone || got.PipelineRounds.DecidedID != 5 {
		t.Fatalf("after the first failure: %+v", got)
	}
	prompt := e.roundDone(t, tk.ID, "h2")
	for _, want := range []string{"pipeline #5 failed", "automatic round 1 of 2", "cart_test.go:12: got 1"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("round 1 prompt lacks %q:\n%s", want, prompt)
		}
	}

	if got = e.pipelineEnded(t, tk.ID, on("h2", failure(6, failedJob("backend", "cart_test.go:30: got 2\n")))); got.PipelineRounds.Count != 2 || got.Status != StatusQueued {
		t.Fatalf("after the second failure: %+v", got)
	}
	if prompt := e.roundDone(t, tk.ID, "h3"); !strings.Contains(prompt, "automatic round 2 of 2") {
		t.Errorf("round 2 prompt:\n%s", prompt)
	}

	runs := len(e.runs.startedRequests())
	got = e.pipelineEnded(t, tk.ID, on("h3", failure(7, failedJob("backend", "cart_test.go:44: got 3\n"))))
	if got.Status != StatusChangesRequested || got.PipelineRounds.Held != HoldLimit || got.PipelineRounds.Count != 2 || !strings.Contains(got.PendingMessage, "pipeline #7 failed") || strings.Contains(got.PendingMessage, "automatic round") {
		t.Fatalf("after the third failure: %+v", got)
	}
	time.Sleep(20 * time.Millisecond)
	if len(e.runs.startedRequests()) != runs {
		t.Fatal("a third failure started the agent")
	}

	// The user sends it: the count starts again, and the agent gets it.
	e.start(t, tk.ID, StartInput{})
	if got := e.stored(t, tk.ID); got.PipelineRounds != (PipelineRounds{DecidedID: 7}) {
		t.Fatalf("after the user started it: %+v", got.PipelineRounds)
	}
	if prompt := e.roundDone(t, tk.ID, "h4"); !strings.Contains(prompt, "pipeline #7 failed") {
		t.Errorf("the user's round lacks the failure:\n%s", prompt)
	}
	if got := e.pipelineEnded(t, tk.ID, on("h4", failure(8, failedJob("backend", "other\n")))); got.PipelineRounds.Count != 1 || got.Status != StatusQueued {
		t.Fatalf("after the user stepped in, a failure is round 1 again: %+v", got)
	}
}

// Every way the user steps in starts the rounds again, as does a review
// requesting changes, and releases a failure waiting for them; editing a
// task does too, unless a failure waits (TestEditingKeepsAWaitingFailure).
func TestSteppingInStartsThePipelineRoundsAgain(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name string
		step func(e *env, id int64) error
	}{
		{"starting it", func(e *env, id int64) error {
			_, err := e.svc.Start(ctx, owner, 1, id, StartInput{Message: "Try another way."})
			return err
		}},
		{"requesting changes", func(e *env, id int64) error {
			_, err := e.svc.RequestChanges(ctx, owner, 1, id, "Use decimals.", false)
			return err
		}},
		{"sending changes", func(e *env, id int64) error {
			_, err := e.svc.RequestChanges(ctx, owner, 1, id, "Use decimals.", true)
			return err
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := setup(t)
			tk := e.reviewTask(t, "h1")
			e.pipelineEnded(t, tk.ID, failure(5, failedJob("backend", "one\n")))
			e.roundDone(t, tk.ID, "h2")
			e.pipelineEnded(t, tk.ID, on("h2", failure(6, failedJob("backend", "two\n"))))
			e.roundDone(t, tk.ID, "h3")
			e.pipelineEnded(t, tk.ID, on("h3", failure(7, failedJob("backend", "three\n"))))
			if got := e.stored(t, tk.ID); got.PipelineRounds.Held != HoldLimit {
				t.Fatalf("expected the failure waiting: %+v", got)
			}
			if err := tt.step(e, tk.ID); err != nil {
				t.Fatalf("%s: %v", tt.name, err)
			}
			if got := e.stored(t, tk.ID); got.PipelineRounds != (PipelineRounds{DecidedID: 7}) {
				t.Fatalf("after %s: %+v", tt.name, got.PipelineRounds)
			}
		})
	}
	t.Run("editing it, holding nothing", func(t *testing.T) {
		e := setup(t)
		tk := e.reviewTask(t, "h1")
		e.pipelineEnded(t, tk.ID, failure(5, failedJob("backend", "one\n")))
		e.roundDone(t, tk.ID, "h2")
		if _, err := e.svc.Update(ctx, owner, 1, tk.ID, UpdateInput{Title: "Add a cart", Body: "In EUR."}); err != nil {
			t.Fatal(err)
		}
		if got := e.stored(t, tk.ID); got.PipelineRounds != (PipelineRounds{DecidedID: 5}) {
			t.Fatalf("after editing it: %+v", got.PipelineRounds)
		}
	})
}

// Editing a task whose pipeline failure waits for the user, such as to
// raise its budget, keeps the failure waiting, held, for them to send.
func TestEditingKeepsAWaitingFailure(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	tk := e.reviewTask(t, "h1")
	tk.BudgetUSD = 1
	if err := e.repo.Update(ctx, tk); err != nil {
		t.Fatal(err)
	}
	if err := e.repo.RecordCost(ctx, tk.ID, 2); err != nil {
		t.Fatal(err)
	}
	if got := e.pipelineEnded(t, tk.ID, failure(5, failedJob("backend", "x\n"))); got.PipelineRounds.Held != HoldBudget {
		t.Fatalf("not held: %+v", got.PipelineRounds)
	}
	raised := 5.0
	got, err := e.svc.Update(ctx, owner, 1, tk.ID, UpdateInput{Title: tk.Title, Body: tk.Body, BudgetUSD: &raised})
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusChangesRequested || got.PipelineRounds.Held != HoldBudget || !strings.Contains(got.PendingMessage, "pipeline #5 failed") || got.PendingPipeline == "" {
		t.Fatalf("after raising the budget by editing: %s %+v pending %q", got.Status, got.PipelineRounds, got.PendingPipeline)
	}
}

// A round that fails the same jobs the same way as the failure it was sent
// for ends the rounds early: the agent is stuck.
func TestAStuckAgentWaitsAfterOneRound(t *testing.T) {
	e := setup(t)
	tk := e.reviewTask(t, "h1")
	e.pipelineEnded(t, tk.ID, failure(5, failedJob("backend", "2026-11-22T10:00:00Z --- FAIL: TestCart (0.02s)\n")))
	e.roundDone(t, tk.ID, "h2")
	got := e.pipelineEnded(t, tk.ID, on("h2", failure(6, failedJob("backend", "2026-11-22T10:09:30Z --- FAIL: TestCart (0.04s)\n"))))
	if got.Status != StatusChangesRequested || got.PipelineRounds.Held != HoldStuck || got.PipelineRounds.Count != 1 {
		t.Fatalf("a stuck round: %+v", got)
	}
}

// A conflict the pipeline found goes back as the user's request to resolve
// conflicts does, and counts toward the same rounds.
func TestAConflictCountsTowardThePipelineRounds(t *testing.T) {
	e := setup(t)
	tk := e.reviewTask(t, "h1")
	conflict := PipelineResult{PipelineID: 5, HeadSHA: "h1", TargetBranch: "main", TargetSHA: "m1", Outcome: PipelineConflict, ConflictFiles: []string{"cart.go"}}
	if got := e.pipelineEnded(t, tk.ID, conflict); got.PipelineRounds.Count != 1 || got.Status != StatusQueued {
		t.Fatalf("after a conflict: %+v", got)
	}
	if prompt := e.roundDone(t, tk.ID, "h2"); !strings.Contains(prompt, ConflictFeedback("task/1", "main", []string{"cart.go"})) {
		t.Errorf("the conflict round:\n%s", prompt)
	}
	e.pipelineEnded(t, tk.ID, on("h2", failure(6, failedJob("backend", "two\n"))))
	e.roundDone(t, tk.ID, "h3")
	conflict.PipelineID, conflict.HeadSHA = 7, "h3"
	if got := e.pipelineEnded(t, tk.ID, conflict); got.PipelineRounds.Held != HoldLimit {
		t.Fatalf("a conflict after two rounds: %+v", got)
	}
}

// A job failing on the target too isn't the task's: it isn't sent and
// doesn't count, and when every failing job is so, nothing goes back.
func TestJobsFailingOnTheTargetAreNotSentBack(t *testing.T) {
	e := setup(t)
	tk := e.reviewTask(t, "h1")
	lint := failedJob("lint", "MAIN IS BROKEN\n")
	lint.AlsoFailingOnTarget = true
	if got := e.pipelineEnded(t, tk.ID, failure(5, lint)); got.Status != StatusInReview || got.PipelineRounds != (PipelineRounds{DecidedID: 5}) {
		t.Fatalf("only the target's failure: %+v", got)
	}
	time.Sleep(20 * time.Millisecond)
	if len(e.runs.startedRequests()) != 0 {
		t.Fatal("a failure only on the target started the agent")
	}
	got := e.pipelineEnded(t, tk.ID, failure(6, failedJob("backend", "cart broke\n"), lint))
	if got.PipelineRounds.Count != 1 || got.Status != StatusQueued {
		t.Fatalf("a failure of the task's: %+v", got)
	}
	prompt := e.roundDone(t, tk.ID, "h2")
	if !strings.Contains(prompt, "cart broke") || strings.Contains(prompt, "MAIN IS BROKEN") || !strings.Contains(prompt, "`lint` fails on `main` too") {
		t.Errorf("the round:\n%s", prompt)
	}
}

// While the target's own pipeline of the same commit is still to say
// whether a job fails there, nothing is decided, so the same pipeline is
// decided once it has.
func TestAFailureWaitsForTheTargetsPipeline(t *testing.T) {
	e := setup(t)
	tk := e.reviewTask(t, "h1")
	res := failure(5, failedJob("backend", "x\n"))
	res.TargetPending = true
	if got := e.pipelineEnded(t, tk.ID, res); got.Status != StatusInReview || got.PipelineRounds != (PipelineRounds{}) {
		t.Fatalf("while the target's pipeline runs: %+v", got)
	}
	res.TargetPending = false
	if got := e.pipelineEnded(t, tk.ID, res); got.PipelineRounds.Count != 1 {
		t.Fatalf("once it ended: %+v", got)
	}
}

// A pipeline is told at least once, in any order: told again, told after a
// newer one, or of a commit the branch has moved on from, it changes
// nothing.
func TestAPipelineToldTwiceIsDecidedOnce(t *testing.T) {
	e := setup(t)
	tk := e.reviewTask(t, "h1")
	first := failure(5, failedJob("backend", "one\n"))
	e.pipelineEnded(t, tk.ID, first)
	if got := e.pipelineEnded(t, tk.ID, first); got.PipelineRounds.Count != 1 {
		t.Fatalf("told again while the agent works: %+v", got)
	}
	e.roundDone(t, tk.ID, "h2")
	runs := len(e.runs.startedRequests())
	for name, res := range map[string]PipelineResult{
		"told again":           first,
		"of an earlier commit": failure(9, failedJob("backend", "stale\n")),
		"with no commit":       on("", failure(10, failedJob("backend", "?\n"))),
	} {
		if got := e.pipelineEnded(t, tk.ID, res); got.Status != StatusInReview || got.PipelineRounds.Count != 1 || got.PipelineRounds.DecidedID != 5 {
			t.Fatalf("%s: %+v", name, got)
		}
	}
	time.Sleep(20 * time.Millisecond)
	if len(e.runs.startedRequests()) != runs {
		t.Fatal("a pipeline told again started the agent")
	}
	if got := e.pipelineEnded(t, tk.ID, on("h2", failure(6, failedJob("backend", "two\n")))); got.PipelineRounds.Count != 2 {
		t.Fatalf("the next pipeline: %+v", got)
	}
}

// What was decided is kept with the task, so a pipeline told again after
// devpad restarts is not sent back again.
func TestARestartNeverSendsAPipelineRoundTwice(t *testing.T) {
	e := setup(t)
	tk := e.reviewTask(t, "h1")
	res := failure(5, failedJob("backend", "one\n"))
	e.pipelineEnded(t, tk.ID, res)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := e.svc.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	// Devpad stopped before the round ran, and the task is in review again
	// on the same commit, as a cut-short round leaves nothing to collect.
	stored := e.stored(t, tk.ID)
	stored.Status = StatusInReview
	if err := e.repo.Update(ctx, stored); err != nil {
		t.Fatal(err)
	}
	again := NewService(Deps{Repo: e.repo, Projects: e.sandboxes, Workspaces: workspaceLifecycle{e.sandboxes}, Runs: e.runs, Conversations: e.convs, Defaults: e.defaults, Budgets: e.budgets})
	t.Cleanup(func() { again.Shutdown(context.Background()) })
	// The round brought nothing new, so the failure waits for the user;
	// it never goes to the agent a second time.
	runs := len(e.runs.startedRequests())
	got, err := again.PipelineEnded(ctx, owner, 1, tk.ID, res)
	if err != nil || got.Status != StatusChangesRequested || got.PipelineRounds.Count != 1 || got.PipelineRounds.Held != HoldStuck {
		t.Fatalf("told again after a restart: %+v %v", got, err)
	}
	if got, err = again.PipelineEnded(ctx, owner, 1, tk.ID, res); err != nil || got.Status != StatusChangesRequested || got.PipelineRounds.Held != HoldStuck {
		t.Fatalf("told a third time: %+v %v", got, err)
	}
	time.Sleep(20 * time.Millisecond)
	if len(e.runs.startedRequests()) != runs {
		t.Fatal("a pipeline told again started the agent")
	}
}

// A round that ends with no new commit makes no pipeline of its own: the
// merge request tells the task its result again, and the failure waits
// for the user, as stuck, saying the round changed nothing.
func TestARoundWithNoNewCommitWaitsForTheUser(t *testing.T) {
	e := setup(t)
	tk := e.reviewTask(t, "h1")
	res := failure(5, failedJob("backend", "cart_test.go:12: got 1\n"))
	if got := e.pipelineEnded(t, tk.ID, res); got.PipelineRounds.Count != 1 || got.PipelineRounds.SentHead != "h1" {
		t.Fatalf("not sent: %+v", got.PipelineRounds)
	}
	e.roundDone(t, tk.ID, "h1")
	got := e.pipelineEnded(t, tk.ID, res)
	if got.Status != StatusChangesRequested || got.PipelineRounds.Held != HoldStuck || got.PipelineRounds.SentHead != "" ||
		!strings.Contains(got.PendingMessage, "already tested") || !strings.Contains(got.PendingPipeline, "`backend` failed (exit code 1)") {
		t.Fatalf("after a round with no new commit: %s %+v\n%s", got.Status, got.PipelineRounds, got.PendingMessage)
	}
}

// A round that goes back to commits already tested is told that commit's
// result, and waits for the user too.
func TestARoundBackAtTestedCommitsWaitsForTheUser(t *testing.T) {
	e := setup(t)
	tk := e.reviewTask(t, "h1")
	e.pipelineEnded(t, tk.ID, failure(5, failedJob("backend", "one\n")))
	e.roundDone(t, tk.ID, "h2")
	e.pipelineEnded(t, tk.ID, on("h2", failure(6, failedJob("backend", "two\n"))))
	e.roundDone(t, tk.ID, "h1")
	if got := e.pipelineEnded(t, tk.ID, failure(5, failedJob("backend", "one\n"))); got.Status != StatusChangesRequested || got.PipelineRounds.Held != HoldStuck {
		t.Fatalf("back at h1: %s %+v", got.Status, got.PipelineRounds)
	}
}

// A round that goes back to commits whose pipeline passed ends the rounds,
// as any pass does: the next failure is round 1 again.
func TestARoundBackAtCommitsThatPassedEndsTheRounds(t *testing.T) {
	e := setup(t)
	tk := e.reviewTask(t, "h1")
	e.pipelineEnded(t, tk.ID, PipelineResult{PipelineID: 4, HeadSHA: "h1", Outcome: PipelinePassed})
	tk = e.stored(t, tk.ID)
	tk.HeadSHA = "h2"
	if err := e.repo.Update(context.Background(), tk); err != nil {
		t.Fatal(err)
	}
	e.pipelineEnded(t, tk.ID, on("h2", failure(5, failedJob("backend", "one\n"))))
	e.roundDone(t, tk.ID, "h1")
	if got := e.pipelineEnded(t, tk.ID, PipelineResult{PipelineID: 4, HeadSHA: "h1", Outcome: PipelinePassed}); got.Status != StatusInReview || got.PipelineRounds != (PipelineRounds{DecidedID: 5}) {
		t.Fatalf("back at h1: %s %+v", got.Status, got.PipelineRounds)
	}
}

// A failure that can't be sent, because the agent can't start, waits for
// the user rather than be lost, since the pipeline counts as decided.
func TestAFailureTheAgentCantBeStartedForWaits(t *testing.T) {
	e := setup(t)
	tk := e.reviewTask(t, "h1")
	e.sandboxes.set(func(f *fakeSandboxes) { f.project.Status = project.StatusError })
	got, err := e.svc.PipelineEnded(context.Background(), owner, 1, tk.ID, failure(5, failedJob("backend", "x\n")))
	if err != nil {
		t.Fatal(err)
	}
	stored := e.stored(t, tk.ID)
	if got.Status != StatusChangesRequested || stored.Status != StatusChangesRequested || stored.PipelineRounds.Held != HoldNotStarted || stored.PipelineRounds.DecidedID != 5 ||
		stored.PipelineRounds.Count != 0 || !strings.Contains(stored.PendingMessage, "pipeline #5 failed") || strings.Contains(stored.PendingMessage, "automatic round") {
		t.Fatalf("after the agent couldn't start: %s %+v", stored.Status, stored.PipelineRounds)
	}
}

// The pending message's summary is devpad's: set with pipeline feedback,
// cleared by other feedback and once the agent has it.
func TestThePendingPipelineSummary(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	tk := e.reviewTask(t, "h1")
	got := e.pipelineEnded(t, tk.ID, failure(5, failedJob("backend", "SECRET OUTPUT\n")))
	if !strings.Contains(got.PendingMessage, "SECRET OUTPUT") || got.PendingPipeline != "Pipeline #5 failed on `task/1` merged onto `main`: `backend` failed (exit code 1)." {
		t.Fatalf("summary %q", got.PendingPipeline)
	}
	e.roundDone(t, tk.ID, "h2")
	if got := e.stored(t, tk.ID); got.PendingMessage != "" || got.PendingPipeline != "" {
		t.Fatalf("after the agent had it: %q %q", got.PendingMessage, got.PendingPipeline)
	}
	if got, err := e.svc.RequestChanges(ctx, owner, 1, tk.ID, "Use decimals.", false); err != nil || got.PendingPipeline != "" {
		t.Fatalf("review feedback: %+v %v", got, err)
	}
	if got, err := e.svc.Start(ctx, owner, 1, tk.ID, StartInput{Message: "quoted", PipelineSummary: "Pipeline #6 failed."}); err != nil || got.PendingPipeline != "Pipeline #6 failed." {
		t.Fatalf("a send back quoting a pipeline: %+v %v", got, err)
	}
}

// A pass ends the rounds, so the next failure is round 1 again, and a
// failure waiting for the user that a pass made stale is dropped.
func TestAPassEndsThePipelineRounds(t *testing.T) {
	e := setup(t)
	tk := e.reviewTask(t, "h1")
	e.pipelineEnded(t, tk.ID, failure(5, failedJob("backend", "one\n")))
	e.roundDone(t, tk.ID, "h2")
	if got := e.pipelineEnded(t, tk.ID, PipelineResult{PipelineID: 6, HeadSHA: "h2", Outcome: PipelinePassed}); got.Status != StatusInReview || got.PipelineRounds != (PipelineRounds{DecidedID: 6}) {
		t.Fatalf("after a pass: %+v", got)
	}
	// The target moved and the change no longer passes on it.
	e.pipelineEnded(t, tk.ID, on("h2", failure(7, failedJob("backend", "two\n"))))
	e.roundDone(t, tk.ID, "h3")
	e.pipelineEnded(t, tk.ID, on("h3", failure(8, failedJob("backend", "three\n"))))
	e.roundDone(t, tk.ID, "h4")
	if got := e.pipelineEnded(t, tk.ID, on("h4", failure(9, failedJob("backend", "four\n")))); got.PipelineRounds.Held != HoldLimit {
		t.Fatalf("expected the failure waiting: %+v", got)
	}
	// The target was fixed, and the change passes on it.
	got := e.pipelineEnded(t, tk.ID, PipelineResult{PipelineID: 10, HeadSHA: "h4", Outcome: PipelinePassed})
	if got.Status != StatusInReview || got.PendingMessage != "" || got.PipelineRounds != (PipelineRounds{DecidedID: 10}) {
		t.Fatalf("a pass of a failure waiting for the user: %+v", got)
	}
}

// A failure of a task over its budget waits for the user to raise it.
func TestAPipelineFailureOverBudgetWaits(t *testing.T) {
	e := setup(t)
	tk := e.reviewTask(t, "h1")
	tk.BudgetUSD, tk.CostUSD = 1, 0
	e.repo.Update(context.Background(), tk)
	e.repo.RecordCost(context.Background(), tk.ID, 2)
	got := e.pipelineEnded(t, tk.ID, failure(5, failedJob("backend", "one\n")))
	if got.Status != StatusChangesRequested || got.PipelineRounds.Held != HoldBudget || got.PipelineRounds.Count != 0 || !strings.Contains(got.PendingMessage, "pipeline #5 failed") {
		t.Fatalf("over budget: %+v", got)
	}
}

// fakePipelines answers PipelineJobs with jobs, and records the branches
// asked for.
type fakePipelines struct {
	mu    sync.Mutex
	jobs  *PipelineJobs
	asked []string
}

func (f *fakePipelines) PipelineJobs(_ context.Context, _, _ int64, branch string) (*PipelineJobs, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.asked = append(f.asked, branch)
	return f.jobs, nil
}

// The agent is told the jobs its merge request's pipeline runs, so it runs
// them before it finishes.
func TestImplementPromptListsThePipelineJobs(t *testing.T) {
	e := setup(t)
	e.sandboxes.set(func(f *fakeSandboxes) {
		f.project.ProtectedBranches = nil
		f.project.PipelineBranches = []string{"main"}
	})
	jobs := &fakePipelines{jobs: &PipelineJobs{Path: ".devpad/pipeline.yml", Jobs: []PipelineJob{{Name: "backend", Run: "go test ./...", Services: []string{"postgres"}}}}}
	e.svc.SetPipelines(jobs)
	tk := e.createTask(t)
	e.start(t, tk.ID, StartInput{})
	e.waitTask(t, tk.ID, hasRun)
	prompt := e.runs.startedRequests()[0].Message.Text()
	for _, want := range []string{"## The project's pipeline", "devpad runs these jobs itself", "### `backend`", "Services: postgres", "go test ./..."} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt lacks %q:\n%s", want, prompt)
		}
	}
	jobs.mu.Lock()
	defer jobs.mu.Unlock()
	if len(jobs.asked) != 1 || jobs.asked[0] != "main" {
		t.Fatalf("asked for %v", jobs.asked)
	}
}
