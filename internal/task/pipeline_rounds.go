package task

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"
)

// Automatic pipeline rounds. When the pipeline that guards a task's merge
// request fails, or finds the branch doesn't merge, devpad sends the task
// back to its agent with what failed, up to maxPipelineRounds times in a
// row; after that, or as soon as a round fails just as the one before did,
// the failure waits for the user. PipelineRounds.decide is the whole rule,
// and the task service only applies what it decides, under the task's lock.

// maxPipelineRounds is how many times in a row devpad sends a task back to
// its agent because its merge request's pipeline failed.
const maxPipelineRounds = 2

// PipelineHold says why a task's latest pipeline failure waits for the user
// rather than going back to its agent.
type PipelineHold string

const (
	HoldNone PipelineHold = ""
	// HoldLimit: the agent already had maxPipelineRounds rounds in a row.
	HoldLimit PipelineHold = "limit"
	// HoldStuck: the agent's last round failed the same jobs the same way as
	// the failure it was sent back for.
	HoldStuck PipelineHold = "stuck"
	// HoldBudget: the task has spent its budget.
	HoldBudget PipelineHold = "budget"
)

// PipelineRounds is how a task's automatic pipeline rounds stand.
type PipelineRounds struct {
	// Count is how many rounds in a row devpad sent the task back for its
	// pipeline, since the user last stepped in or the pipeline last passed.
	Count int
	// Fingerprint is roundFingerprint of the failure the last of them was
	// sent for.
	Fingerprint string
	// DecidedID is the newest pipeline devpad decided on, so that one told
	// again, or an older one told late, is never acted on twice. Nothing
	// resets it.
	DecidedID int64
	// Held says why the latest failure waits for the user, if it does: the
	// task is then changes requested, with the failure as its pending
	// message.
	Held PipelineHold
}

// Limit is how many rounds in a row the task's agent gets.
func (r PipelineRounds) Limit() int { return maxPipelineRounds }

// reset starts the count again, as when the user steps in or the pipeline
// passes, keeping which pipeline was decided on last.
func (r PipelineRounds) reset() PipelineRounds {
	return PipelineRounds{DecidedID: r.DecidedID}
}

// PipelineOutcome is how a merge request's pipeline ended, of the endings
// that say something of the task's work.
type PipelineOutcome string

const (
	PipelinePassed   PipelineOutcome = "passed"
	PipelineFailed   PipelineOutcome = "failed"
	PipelineConflict PipelineOutcome = "conflict"
)

// PipelineResult is how the pipeline that guards a task's merge request
// ended on the task's branch merged onto the target, as the merge request
// service tells it: only for the pipeline of the merge request's commits
// and jobs as they are now.
type PipelineResult struct {
	PipelineID int64
	// HeadSHA is the commit of the task's branch it tested; a result of any
	// other is stale.
	HeadSHA string
	// TargetBranch and TargetSHA are what it merged the branch onto, and
	// JobsFrom where the jobs came from, for the agent to read, such as
	// "`.devpad/pipeline.yml` on `main`".
	TargetBranch string
	TargetSHA    string
	JobsFrom     string
	Outcome      PipelineOutcome
	// Failed are the jobs whose code failed, in the file's order.
	Failed []PipelineJobResult
	// TargetPending says the target's own pipeline of TargetSHA is still to
	// finish, so whether a job fails there too is not known yet.
	TargetPending bool
	// ConflictFiles are the files that conflict, for PipelineConflict.
	ConflictFiles []string
}

// PipelineJobResult is a job of a pipeline as it ended. Everything but
// Output and Run comes from devpad; Output is what the job printed, which
// the code under test wrote.
type PipelineJobResult struct {
	Name string
	// Status is how it ended, such as failed, timed_out or out_of_memory.
	Status   string
	ExitCode *int
	Duration time.Duration
	// Run is the job's script, from the target's pipeline.
	Run string
	// Error is devpad's word on why it did not pass, and Notes devpad's
	// notes on it.
	Error string
	Notes []string
	// Output is the end of what the job printed, and OutputBytes how much it
	// printed in all.
	Output      string
	OutputBytes int64
	// AlsoFailingOnTarget says the job failed in the target branch's own
	// pipeline of the same commit, with the same jobs, so its failure is
	// not the task's.
	AlsoFailingOnTarget bool
}

// roundAction is what devpad does with a pipeline's result for a task.
type roundAction int

const (
	// roundIgnore: nothing, and nothing is recorded: the result is old or
	// told before, or the task is not waiting on its pipeline.
	roundIgnore roundAction = iota
	// roundWait: nothing yet, and nothing recorded: the target's own
	// pipeline of the same commit is still to say whether it fails too.
	roundWait
	// roundReset: the pipeline passed, which ends the rounds.
	roundReset
	// roundOnTarget: every job that failed fails on the target too, so none
	// is the task's, and nothing counts.
	roundOnTarget
	// roundSend: the task goes back to its agent with the failure.
	roundSend
	// roundHold: the failure waits for the user, as the task's pending
	// message.
	roundHold
)

// roundDecision is what PipelineRounds.decide decided.
type roundDecision struct {
	action roundAction
	// rounds are the task's rounds after it.
	rounds PipelineRounds
	// counted are the failures that are the task's, which the feedback
	// lists, and onTarget the names of the jobs that fail on the target too.
	counted  []PipelineJobResult
	onTarget []string
	// release says a task whose failure waited for the user goes back to
	// review, its stale feedback dropped: the pipeline passed, or nothing
	// failing is the task's any more.
	release bool
}

// decide is what devpad does with res, a result of the pipeline guarding
// task t's merge request, given how t's rounds stand.
func (r PipelineRounds) decide(t *Task, res PipelineResult) roundDecision {
	if res.PipelineID <= r.DecidedID {
		return roundDecision{action: roundIgnore, rounds: r}
	}
	held := r.Held != HoldNone && t.Status == StatusChangesRequested
	if t.Status != StatusInReview && !held {
		// The agent is at work, or the user has the task: the next round
		// moves its head, and its own pipeline follows.
		return roundDecision{action: roundIgnore, rounds: r}
	}
	decided := r
	decided.DecidedID = res.PipelineID
	switch res.Outcome {
	case PipelinePassed:
		after := decided.reset()
		return roundDecision{action: roundReset, rounds: after, release: held}
	case PipelineFailed:
		if res.TargetPending {
			return roundDecision{action: roundWait, rounds: r}
		}
	case PipelineConflict:
	default:
		return roundDecision{action: roundIgnore, rounds: r}
	}
	counted, onTarget := splitOnTarget(res)
	if res.Outcome == PipelineFailed && len(counted) == 0 {
		decided.Held = HoldNone
		return roundDecision{action: roundOnTarget, rounds: decided, onTarget: onTarget, release: held}
	}
	d := roundDecision{rounds: decided, counted: counted, onTarget: onTarget}
	fingerprint := roundFingerprint(res, counted)
	switch {
	case held:
		// Still the user's: the newer failure replaces the one waiting.
		d.action = roundHold
	case r.Count >= maxPipelineRounds:
		d.action, d.rounds.Held = roundHold, HoldLimit
	case r.Count > 0 && fingerprint == r.Fingerprint:
		d.action, d.rounds.Held = roundHold, HoldStuck
	case t.OverBudget():
		d.action, d.rounds.Held = roundHold, HoldBudget
	default:
		d.action = roundSend
		d.rounds.Count, d.rounds.Fingerprint, d.rounds.Held = r.Count+1, fingerprint, HoldNone
	}
	return d
}

// splitOnTarget parts a failed pipeline's jobs into those whose failure is
// the task's and the names of those that fail on the target too.
func splitOnTarget(res PipelineResult) (counted []PipelineJobResult, onTarget []string) {
	for _, j := range res.Failed {
		if j.AlsoFailingOnTarget {
			onTarget = append(onTarget, j.Name)
		} else {
			counted = append(counted, j)
		}
	}
	return counted, onTarget
}

// roundFingerprint says what failed and how, so that a round failing just
// as the one before can be told apart from one that made progress: the
// conflicting files, or each counted job's name, status, exit code and the
// end of its output with what changes from run to run (times, durations,
// addresses, temporary paths, ports) taken out. Two failures get the same
// fingerprint exactly when they are the same in all of that.
func roundFingerprint(res PipelineResult, counted []PipelineJobResult) string {
	var lines []string
	if res.Outcome == PipelineConflict {
		files := slices.Clone(res.ConflictFiles)
		slices.Sort(files)
		lines = append(lines, "conflict")
		lines = append(lines, files...)
	} else {
		for _, j := range counted {
			exit := "-"
			if j.ExitCode != nil {
				exit = fmt.Sprint(*j.ExitCode)
			}
			tail := sha256.Sum256([]byte(normalizeOutput(j.Output, j.OutputBytes > int64(len(j.Output)))))
			lines = append(lines, strings.Join([]string{j.Name, j.Status, exit, hex.EncodeToString(tail[:])}, "\x00"))
		}
		slices.Sort(lines)
	}
	sum := sha256.Sum256([]byte(strings.Join(lines, "\n")))
	return hex.EncodeToString(sum[:])
}

// fingerprintLines is how many of the last lines of a job's output its
// fingerprint covers.
const fingerprintLines = 200

// What changes from one run of the same failure to the next, replaced by
// placeholders before two outputs are compared. Paths go first, since they
// hold digits and hex, then times before the dates and numbers inside them.
var outputNoise = []struct {
	pattern *regexp.Regexp
	with    string
}{
	{regexp.MustCompile(`(/private/var/folders|/var/folders|/var/tmp|/tmp)/[^\s:'"(),]*`), "<tmp>"},
	{regexp.MustCompile(`\d{4}-\d{2}-\d{2}[T ]\d{2}:\d{2}(:\d{2}(\.\d+)?)?(Z|[+-]\d{2}:?\d{2})?`), "<time>"},
	{regexp.MustCompile(`\b\d{4}[-/]\d{2}[-/]\d{2}\b`), "<date>"},
	{regexp.MustCompile(`\b\d{1,2}:\d{2}:\d{2}(\.\d+)?\b`), "<time>"},
	{regexp.MustCompile(`0x[0-9a-fA-F]+`), "<addr>"},
	{regexp.MustCompile(`\b[0-9a-fA-F]{8,}\b`), "<hex>"},
	{regexp.MustCompile(`(localhost|127\.0\.0\.1|\[::1\]|0\.0\.0\.0):\d+`), "$1:<port>"},
	{regexp.MustCompile(`goroutine \d+`), "goroutine <n>"},
	{regexp.MustCompile(`\b\d+(\.\d+)? ?(milliseconds?|seconds?|secs?|minutes?|mins?)\b`), "<duration>"},
	{regexp.MustCompile(`\b(\d+(\.\d+)?(ns|us|µs|ms|s|m|h))+\b`), "<duration>"},
}

// normalizeOutput is the end of a job's output as fingerprints compare it:
// cleaned as the agent reads it (cleanOutput), without a first line cut
// off when the output is only its end (cut), with each line's run-to-run
// noise replaced and its trailing space trimmed, keeping the last
// fingerprintLines lines.
func normalizeOutput(output string, cut bool) string {
	text := cleanOutput(output)
	if cut {
		if i := strings.IndexByte(text, '\n'); i >= 0 {
			text = text[i+1:]
		} else {
			text = ""
		}
	}
	for _, n := range outputNoise {
		text = n.pattern.ReplaceAllString(text, n.with)
	}
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	for i, line := range lines {
		lines[i] = strings.TrimRight(line, " \t")
	}
	if len(lines) > fingerprintLines {
		lines = lines[len(lines)-fingerprintLines:]
	}
	return strings.Join(lines, "\n")
}

// ansiEscape matches terminal escape sequences: CSI (colours, cursor
// moves), OSC (titles, links) and the two-character ones.
var ansiEscape = regexp.MustCompile(`\x1b(\[[0-?]*[ -/]*[@-~]|\][^\x07\x1b]*(\x07|\x1b\\)?|[@-Z\\-_])`)

// cleanOutput is a job's output as text to read: valid UTF-8, without
// terminal escapes, nor control or format characters (such as bidi
// controls) other than newlines and tabs, and
// with each line as a terminal leaves it after a carriage return redraws it,
// as progress bars do.
func cleanOutput(output string) string {
	text := strings.ToValidUTF8(output, "�")
	text = ansiEscape.ReplaceAllString(text, "")
	text = strings.ReplaceAll(text, "\r\n", "\n")
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		if j := strings.LastIndexByte(strings.TrimRight(line, "\r"), '\r'); j >= 0 {
			line = line[j+1:]
		}
		lines[i] = strings.Map(func(r rune) rune {
			if r != '\t' && unicode.In(r, unicode.Cc, unicode.Cf) {
				return -1
			}
			return r
		}, line)
	}
	return strings.Join(lines, "\n")
}
