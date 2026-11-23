package task

import (
	"fmt"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"
)

func exitCode(n int) *int { return &n }

// failedJob is a job whose script exited 1 having printed output.
func failedJob(name, output string) PipelineJobResult {
	return PipelineJobResult{Name: name, Status: "failed", ExitCode: exitCode(1), Run: "go test ./...", Output: output, OutputBytes: int64(len(output))}
}

// failure is pipeline id failing on head h1 with jobs.
func failure(id int64, jobs ...PipelineJobResult) PipelineResult {
	return PipelineResult{PipelineID: id, HeadSHA: "h1", TargetBranch: "main", TargetSHA: "m1", Outcome: PipelineFailed, Failed: jobs}
}

func TestPipelineRoundsDecide(t *testing.T) {
	backend := failedJob("backend", "--- FAIL: TestCart (0.01s)\n    cart_test.go:12: got 1, want 2\nFAIL\n")
	frontend := failedJob("frontend", "src/cart.ts(3,1): error TS2322\n")
	onMain := frontend
	onMain.AlsoFailingOnTarget = true
	conflict := PipelineResult{PipelineID: 9, HeadSHA: "h1", TargetBranch: "main", Outcome: PipelineConflict, ConflictFiles: []string{"cart.go"}}
	sent := func(count int, res PipelineResult) PipelineRounds {
		counted, _ := splitOnTarget(res)
		return PipelineRounds{Count: count, Fingerprint: roundFingerprint(res, counted), DecidedID: res.PipelineID - 1}
	}
	// outstanding is a round sent for res, the newest pipeline decided.
	outstanding := func(count int, res PipelineResult) PipelineRounds {
		r := sent(count, res)
		r.DecidedID, r.SentHead = res.PipelineID, res.HeadSHA
		return r
	}
	reviewing := func(res PipelineResult) PipelineResult { res.Reviewing = true; return res }
	inReview := &Task{Status: StatusInReview}
	tests := []struct {
		name   string
		rounds PipelineRounds
		task   *Task
		res    PipelineResult
		action roundAction
		// count, held, decided and sentHead are the rounds after it.
		count    int
		held     PipelineHold
		decided  int64
		sentHead string
		release  bool
		counted  []string
	}{
		{name: "a first failure goes back", task: inReview, res: failure(5, backend), action: roundSend, count: 1, decided: 5, sentHead: "h1", counted: []string{"backend"}},
		{name: "a second, different failure goes back", rounds: sent(1, failure(5, frontend)), task: inReview, res: failure(6, backend), action: roundSend, count: 2, decided: 6, sentHead: "h1", counted: []string{"backend"}},
		{name: "a third waits for the user", rounds: sent(2, failure(6, frontend)), task: inReview, res: failure(7, backend), action: roundHold, count: 2, held: HoldLimit, decided: 7, counted: []string{"backend"}},
		{name: "the same failure again is stuck", rounds: sent(1, failure(5, backend)), task: inReview, res: failure(6, backend), action: roundHold, count: 1, held: HoldStuck, decided: 6, counted: []string{"backend"}},
		{name: "a first failure is never stuck", rounds: PipelineRounds{Fingerprint: roundFingerprint(failure(4, backend), []PipelineJobResult{backend}), DecidedID: 4}, task: inReview, res: failure(5, backend), action: roundSend, count: 1, decided: 5, sentHead: "h1"},
		{name: "a conflict counts", rounds: sent(1, failure(8, backend)), task: inReview, res: conflict, action: roundSend, count: 2, decided: 9, sentHead: "h1"},
		{name: "a conflict after two rounds waits", rounds: sent(2, failure(8, backend)), task: inReview, res: conflict, action: roundHold, count: 2, held: HoldLimit, decided: 9},
		{name: "a job failing on the target too isn't sent and doesn't count", rounds: sent(1, failure(5, frontend)), task: inReview, res: failure(6, backend, onMain), action: roundSend, count: 2, decided: 6, sentHead: "h1", counted: []string{"backend"}},
		{name: "every job failing on the target sends nothing", rounds: sent(1, failure(5, backend)), task: inReview, res: failure(6, onMain), action: roundOnTarget, count: 1, decided: 6},
		{name: "every job failing on the target, after two rounds, sends nothing", rounds: sent(2, failure(5, backend)), task: inReview, res: failure(6, onMain), action: roundOnTarget, count: 2, decided: 6},
		{name: "while the target's pipeline runs, it waits", task: inReview, res: func() PipelineResult { r := failure(5, backend); r.TargetPending = true; return r }(), action: roundWait},
		{name: "a pass resets", rounds: sent(2, failure(5, backend)), task: inReview, res: PipelineResult{PipelineID: 6, HeadSHA: "h1", Outcome: PipelinePassed}, action: roundReset, decided: 6},
		{name: "over budget waits", task: &Task{Status: StatusInReview, BudgetUSD: 1, CostUSD: 1}, res: failure(5, backend), action: roundHold, held: HoldBudget, decided: 5},
		{name: "told again", rounds: PipelineRounds{Count: 1, DecidedID: 5}, task: inReview, res: failure(5, backend), action: roundIgnore, count: 1, decided: 5},
		{name: "an older pipeline told late", rounds: PipelineRounds{Count: 1, DecidedID: 7}, task: inReview, res: failure(6, backend), action: roundIgnore, count: 1, decided: 7},
		{name: "the agent already at work", task: &Task{Status: StatusInProgress}, res: failure(5, backend), action: roundIgnore},
		{name: "queued", task: &Task{Status: StatusQueued}, res: failure(5, backend), action: roundIgnore},
		{name: "feedback waiting that isn't the pipeline's", task: &Task{Status: StatusChangesRequested}, res: failure(5, backend), action: roundIgnore},
		{name: "a failed task is the user's", task: &Task{Status: StatusFailed}, res: failure(5, backend), action: roundIgnore},
		{name: "a newer failure replaces the one waiting", rounds: PipelineRounds{Count: 2, DecidedID: 5, Held: HoldLimit}, task: &Task{Status: StatusChangesRequested}, res: failure(6, frontend), action: roundHold, count: 2, held: HoldLimit, decided: 6, counted: []string{"frontend"}},
		{name: "a pass releases a waiting failure", rounds: PipelineRounds{Count: 2, DecidedID: 5, Held: HoldStuck}, task: &Task{Status: StatusChangesRequested}, res: PipelineResult{PipelineID: 6, HeadSHA: "h1", Outcome: PipelinePassed}, action: roundReset, decided: 6, release: true},
		{name: "a failure only on the target releases one waiting", rounds: PipelineRounds{Count: 2, DecidedID: 5, Held: HoldLimit}, task: &Task{Status: StatusChangesRequested}, res: failure(6, onMain), action: roundOnTarget, count: 2, decided: 6, release: true},
		{name: "a status that says nothing of the work", task: inReview, res: PipelineResult{PipelineID: 5, Outcome: "error"}, action: roundIgnore},
		{name: "while an agent reviews the commit, a failure waits", task: inReview, res: reviewing(failure(5, backend)), action: roundWait},
		{name: "while an agent reviews the commit, a conflict waits", task: inReview, res: reviewing(conflict), action: roundWait},
		{name: "while an agent reviews the commit, a pass resets", rounds: sent(1, failure(4, backend)), task: inReview, res: reviewing(PipelineResult{PipelineID: 5, HeadSHA: "h1", Outcome: PipelinePassed}), action: roundReset, decided: 5},
		{name: "a newer pipeline ends the outstanding round", rounds: outstanding(1, failure(5, backend)), task: inReview, res: failure(6, frontend), action: roundSend, count: 2, decided: 6, sentHead: "h1"},
		{name: "a pass ends the outstanding round", rounds: outstanding(1, failure(5, backend)), task: inReview, res: PipelineResult{PipelineID: 6, HeadSHA: "h1", Outcome: PipelinePassed}, action: roundReset, decided: 6},
		{name: "a failure only on the target ends the outstanding round", rounds: outstanding(1, failure(5, backend)), task: inReview, res: failure(6, onMain), action: roundOnTarget, count: 1, decided: 6},
		{name: "a round that brought nothing new is stuck", rounds: outstanding(1, failure(5, backend)), task: inReview, res: failure(5, backend), action: roundHold, count: 1, held: HoldStuck, decided: 5, counted: []string{"backend"}},
		{name: "a round back at commits already tested is stuck", rounds: outstanding(2, on("h2", failure(6, frontend))), task: &Task{Status: StatusInReview}, res: failure(4, backend), action: roundHold, count: 2, held: HoldStuck, decided: 6, counted: []string{"backend"}},
		{name: "a conflict that a round left standing is stuck", rounds: outstanding(1, conflict), task: inReview, res: conflict, action: roundHold, count: 1, held: HoldStuck, decided: 9},
		{name: "a round still at work is not yet stuck", rounds: outstanding(1, failure(5, backend)), task: &Task{Status: StatusInProgress}, res: failure(5, backend), action: roundIgnore, count: 1, decided: 5, sentHead: "h1"},
		{name: "a failure only on the target, told again, is not stuck", rounds: PipelineRounds{Count: 1, DecidedID: 6}, task: inReview, res: failure(6, onMain), action: roundIgnore, count: 1, decided: 6},
		{name: "a round's old failure only on the target is not stuck", rounds: outstanding(1, failure(6, backend)), task: inReview, res: failure(5, onMain), action: roundIgnore, count: 1, decided: 6, sentHead: "h1"},
		{name: "a round's old failure waits for an agent review", rounds: outstanding(1, failure(5, backend)), task: inReview, res: reviewing(failure(5, backend)), action: roundWait, count: 1, decided: 5, sentHead: "h1"},
		{name: "a round back at commits that passed resets", rounds: outstanding(1, failure(5, backend)), task: inReview, res: PipelineResult{PipelineID: 4, HeadSHA: "h0", Outcome: PipelinePassed}, action: roundReset, decided: 5},
		{name: "a pass told again resets what counts since", rounds: PipelineRounds{Count: 1, DecidedID: 6}, task: inReview, res: PipelineResult{PipelineID: 6, HeadSHA: "h1", Outcome: PipelinePassed}, action: roundReset, decided: 6},
		{name: "a pass told again with nothing to reset", rounds: PipelineRounds{DecidedID: 6}, task: inReview, res: PipelineResult{PipelineID: 6, HeadSHA: "h1", Outcome: PipelinePassed}, action: roundIgnore, decided: 6},
		{name: "a round's old pass while the agent works", rounds: outstanding(1, failure(5, backend)), task: &Task{Status: StatusInProgress}, res: PipelineResult{PipelineID: 4, HeadSHA: "h0", Outcome: PipelinePassed}, action: roundIgnore, count: 1, decided: 5, sentHead: "h1"},
		{name: "a held failure told again stays as it is", rounds: PipelineRounds{Count: 2, DecidedID: 5, Held: HoldLimit}, task: &Task{Status: StatusChangesRequested}, res: failure(5, backend), action: roundIgnore, count: 2, held: HoldLimit, decided: 5},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := tt.rounds.decide(tt.task, tt.res)
			if d.action != tt.action || d.rounds.Count != tt.count || d.rounds.Held != tt.held || d.rounds.DecidedID != tt.decided || d.rounds.SentHead != tt.sentHead || d.release != tt.release {
				t.Fatalf("decide = action %d, rounds %+v, release %v; want action %d, count %d, held %q, decided %d, sent head %q, release %v",
					d.action, d.rounds, d.release, tt.action, tt.count, tt.held, tt.decided, tt.sentHead, tt.release)
			}
			if tt.counted != nil {
				var names []string
				for _, j := range d.counted {
					names = append(names, j.Name)
				}
				if strings.Join(names, ",") != strings.Join(tt.counted, ",") {
					t.Fatalf("counted %v, want %v", names, tt.counted)
				}
			}
			if d.action == roundSend && d.rounds.Fingerprint != roundFingerprint(tt.res, d.counted) {
				t.Fatal("a round sent records the fingerprint of the failure it was sent for")
			}
		})
	}
}

// Every way of stepping in starts the count again but keeps the pipeline
// decided on, so a pipeline told again after it is still never acted on.
func TestPipelineRoundsResetKeepsTheDecidedPipeline(t *testing.T) {
	r := PipelineRounds{Count: 2, Fingerprint: "f", SentHead: "h1", DecidedID: 9, Held: HoldStuck}
	if got := r.reset(); got != (PipelineRounds{DecidedID: 9}) {
		t.Fatalf("reset = %+v", got)
	}
}

// Two runs of the same failure differ in what changes from run to run, and
// their fingerprints don't; anything that says the failure moved on does.
func TestRoundFingerprint(t *testing.T) {
	base := "2026-11-22T21:04:05.123Z starting\n" +
		"\x1b[31m--- FAIL: TestCart (0.02s)\x1b[0m\n" +
		"panic at 0xc000123abc in goroutine 17\n" +
		"open /tmp/TestCart123456/cart.db: no such file\n" +
		"dial tcp 127.0.0.1:43521: connection refused\n" +
		"build 3f9a2c1d8e7b ok in 1m2.5s, 4 seconds total\n" +
		"at 21:04:05 on 2026/11/22\n" +
		"FAIL\n"
	again := "2026-11-23T08:00:00Z starting\n" +
		"\x1b[31m--- FAIL: TestCart (0.31s)\x1b[0m\n" +
		"panic at 0xc000999def in goroutine 4\n" +
		"open /tmp/TestCart998877/cart.db: no such file\n" +
		"dial tcp 127.0.0.1:39001: connection refused\n" +
		"build 77aa00bb11cc ok in 58ms, 12 seconds total   \n" +
		"at 08:00:00 on 2026/11/23\n" +
		"FAIL\n"
	fp := func(jobs ...PipelineJobResult) string { return roundFingerprint(failure(1, jobs...), jobs) }
	same := fp(failedJob("backend", base))
	if got := fp(failedJob("backend", again)); got != same {
		t.Fatalf("the same failure, run again, has another fingerprint:\n%s\n---\n%s", normalizeOutput(base, false), normalizeOutput(again, false))
	}
	// The end of a long output starts partway through a line, which says
	// nothing.
	cut := failedJob("backend", "ial line cut off\n"+base)
	cut.OutputBytes = 1 << 20
	if fp(cut) != same {
		t.Fatal("a cut tail's partial first line changed the fingerprint")
	}

	exit2 := failedJob("backend", base)
	exit2.ExitCode = exitCode(2)
	timedOut := failedJob("backend", base)
	timedOut.Status = "timed_out"
	differs := map[string]string{
		"another message":   fp(failedJob("backend", strings.Replace(base, "no such file", "permission denied", 1))),
		"another exit code": fp(exit2),
		"another status":    fp(timedOut),
		"another job":       fp(failedJob("frontend", base)),
		"one more job":      fp(failedJob("backend", base), failedJob("frontend", "x\n")),
		"a conflict":        roundFingerprint(PipelineResult{Outcome: PipelineConflict, ConflictFiles: []string{"cart.go"}}, nil),
	}
	for name, got := range differs {
		if got == same {
			t.Errorf("%s has the same fingerprint", name)
		}
	}
	// Order says nothing: jobs and conflicting files are compared as sets.
	a, b := failedJob("backend", "a\n"), failedJob("frontend", "b\n")
	if fp(a, b) != fp(b, a) {
		t.Error("the order of the failed jobs changed the fingerprint")
	}
	c1 := PipelineResult{Outcome: PipelineConflict, ConflictFiles: []string{"a.go", "b.go"}}
	c2 := PipelineResult{Outcome: PipelineConflict, ConflictFiles: []string{"b.go", "a.go"}}
	if roundFingerprint(c1, nil) != roundFingerprint(c2, nil) {
		t.Error("the order of the conflicting files changed the fingerprint")
	}
}

// Only the last whole lines of a long output that fit in fingerprintBytes
// are compared.
func TestNormalizeOutputKeepsTheLastLines(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 2*fingerprintBytes/len("line 000\n"); i++ {
		fmt.Fprintf(&b, "line %03d\n", i%1000)
	}
	b.WriteString("the end\n")
	got := normalizeOutput(b.String(), false)
	if len(got) > fingerprintBytes || !strings.HasSuffix(got, "\nthe end") || !strings.HasPrefix(got, "line ") || len(got) < fingerprintBytes-len("line 000\n") {
		t.Fatalf("kept %d bytes: %q…", len(got), got[:20])
	}
	long := strings.Repeat("x", 2*fingerprintBytes)
	if got := normalizeOutput(long, false); len(got) != fingerprintBytes {
		t.Fatalf("a single long line kept %d bytes", len(got))
	}
}

// A job's output is read as its last PipelineOutputTail bytes, which start
// wherever the length of what came after puts them. The same failure, with
// a temporary path of another length further up, keeps its fingerprint.
func TestFingerprintDoesNotMoveWithTheWindow(t *testing.T) {
	output := func(tmp string) string {
		var b strings.Builder
		for i := 0; i < 400; i++ {
			fmt.Fprintf(&b, "=== RUN   TestSomethingLongNamed_%03d/subtest_with_a_long_descriptive_name_here\n", i)
			if i == 300 {
				fmt.Fprintf(&b, "    store_test.go:42: opened %s/db.sqlite on 127.0.0.1:8080\n", tmp)
			}
		}
		b.WriteString("--- FAIL: TestSomethingLongNamed_399 (0.01s)\n    x_test.go:9: got 1, want 2\nFAIL\nexit status 1\n")
		return b.String()
	}
	read := func(out string) PipelineJobResult {
		j := failedJob("backend", out[max(0, len(out)-PipelineOutputTail):])
		j.OutputBytes = int64(len(out))
		return j
	}
	fp := func(j PipelineJobResult) string { return roundFingerprint(failure(1, j), []PipelineJobResult{j}) }
	base := fp(read(output("/tmp/T/001")))
	for n := 1; n <= 80; n++ {
		if got := fp(read(output("/tmp/T" + strings.Repeat("7", n) + "/001"))); got != base {
			t.Fatalf("with a temporary path %d bytes longer, the same failure has another fingerprint", n)
		}
	}
}

func TestCleanOutput(t *testing.T) {
	tests := []struct{ name, in, want string }{
		{"colours", "\x1b[1;31mFAIL\x1b[0m ok", "FAIL ok"},
		{"a link", "\x1b]8;;https://example.com\x07here\x1b]8;;\x07", "here"},
		{"an unterminated title ends at its line", "building\n\x1b]0;title\nFAIL: TestCart\nexit status 1\n", "building\n\nFAIL: TestCart\nexit status 1\n"},
		{"a progress bar", "50%\r75%\r100%\ndone", "100%\ndone"},
		{"windows line ends", "a\r\nb\r\n", "a\nb\n"},
		{"control characters", "a\x00b\x07c\td", "abc\td"},
		{"bidi controls", "safe‮evil", "safeevil"},
		{"invalid UTF-8", "a\xffb", "a�b"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := cleanOutput(tt.in); got != tt.want {
				t.Fatalf("cleanOutput(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// The app's PipelineHold lists exactly the holds the server has.
func TestPipelineHoldsMatchTheFrontend(t *testing.T) {
	source, err := os.ReadFile("../../frontend/src/api/tasks.ts")
	if err != nil {
		t.Fatalf("reading the frontend's tasks API: %v", err)
	}
	block := regexp.MustCompile(`export type PipelineHold =([^\n]*)`).FindSubmatch(source)
	if block == nil {
		t.Fatal("cannot find PipelineHold in frontend/src/api/tasks.ts")
	}
	var frontend, server []string
	for _, m := range regexp.MustCompile(`'([a-z_]*)'`).FindAllSubmatch(block[1], -1) {
		frontend = append(frontend, string(m[1]))
	}
	for _, h := range PipelineHolds() {
		server = append(server, string(h))
	}
	slices.Sort(frontend)
	slices.Sort(server)
	if !slices.Equal(frontend, server) {
		t.Errorf("the server's holds are %v, the frontend's PipelineHold %v", server, frontend)
	}
}
