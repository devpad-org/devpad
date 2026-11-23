package pipeline

import (
	"fmt"
	"strings"
)

// The merge gate. A merge request is guarded by a pipeline when it is open,
// its target is ticked under Run pipelines, so that devpad runs its
// pipelines by itself, and a pipeline is configured at the target's current
// commit, which one that doesn't parse is. A guarded merge request merges
// only once its pipeline passed on its head merged onto the target's
// current commit, with the jobs the target has now: by the user, who can
// also merge it anyway, and by devpad merging it automatically, which never
// overrides. Into a branch not ticked, or with no pipeline, nothing guards
// it, and devpad says so rather than that it passed.
//
// MergeRequestStatus.Gate is the one rule: the merge request service asks
// it under the merge request's lock before every merge, and the merge
// request page shows it.

// GateState is how the merge gate stands for a merge request.
type GateState string

const (
	// GateUnguarded is no pipeline guarding the merge request: its target
	// isn't ticked to run pipelines, or neither the target's commit nor the
	// project has one. The gate is open.
	GateUnguarded GateState = "unguarded"
	// GatePassed is its pipeline passing on its commits and jobs as they
	// are now. The gate is open.
	GatePassed GateState = "passed"
	// GateQueued and GateRunning are its pipeline of the commits and jobs
	// as they are now still to finish.
	GateQueued  GateState = "queued"
	GateRunning GateState = "running"
	// GateNotRun is no pipeline of the merge request having run yet.
	GateNotRun GateState = "not_run"
	// GateOutOfDate is its latest result being of other commits or jobs.
	GateOutOfDate GateState = "out_of_date"
	// GateFailed, GateConflict and GateInvalid are its pipeline's result on
	// the commits and jobs as they are now: a job failed, the change
	// doesn't merge onto the target, or the pipeline doesn't parse.
	GateFailed   GateState = "failed"
	GateConflict GateState = "conflict"
	GateInvalid  GateState = "invalid"
	// GateError and GateCancelled are its pipeline of the commits and jobs
	// as they are now that couldn't run, or was cancelled, with none that
	// counts.
	GateError     GateState = "error"
	GateCancelled GateState = "cancelled"
	// GateClosed is a merge request that can no longer merge into a
	// protected branch, which no pipeline changes.
	GateClosed GateState = "closed"
)

// Gate is how the merge gate stands for a merge request now.
type Gate struct {
	State GateState
	// Pipeline is the pipeline State is about: the one of the commits and
	// jobs as they are now, or the out of date one. Nil when none is.
	Pipeline *Pipeline
	// HeadSHA and TargetSHA are the commits the merge request's pipeline
	// must have tested to clear the gate: its head, and its target's tip
	// as it was read. A merge the gate clears is to land on that same tip,
	// so that the tree that passed is the tree that lands.
	HeadSHA   string
	TargetSHA string
	// TestedTree is, when the gate is open because the pipeline passed, the
	// tree it tested, which the merge must land; empty otherwise.
	TestedTree string
	// Reason says why the gate is shut, as a clause for the user without a
	// capital or a full stop, such as "pipeline #12 failed (backend)";
	// empty when it is open.
	Reason string
	// Waits says a result that could open the gate is still to come by
	// itself: the pipeline is queued or running, or devpad runs one, or
	// runs once more one that couldn't run.
	Waits bool
}

// Guarded reports whether a pipeline guards the merge request.
func (g *Gate) Guarded() bool { return g.State != GateUnguarded }

// Clear reports whether the gate lets the merge request merge.
func (g *Gate) Clear() bool { return g.State == GateUnguarded || g.State == GatePassed }

// Overridable reports whether the user can merge the merge request anyway:
// the gate is shut by its pipeline, not by its no longer merging at all,
// nor by its branch not merging onto the target, which the merge itself
// would refuse just as the pipeline's trial merge did.
func (g *Gate) Overridable() bool {
	return !g.Clear() && g.State != GateClosed && g.State != GateConflict
}

// Gate is the merge gate for the merge request as st found it.
func (st *MergeRequestStatus) Gate() *Gate {
	if !st.Open {
		return &Gate{State: GateClosed, Reason: "it can no longer merge into a protected branch"}
	}
	g := &Gate{HeadSHA: st.Key.HeadSHA, TargetSHA: st.Key.TargetSHA}
	switch {
	case !st.Automatic || !st.Configured():
		g.State = GateUnguarded
	case st.Invalid != "":
		// It can never pass, whatever ran before.
		g.State, g.Pipeline = GateInvalid, st.Current
		g.Reason = fmt.Sprintf("%s's pipeline doesn't parse: %s", st.Target, trimStop(st.Invalid))
	case st.Current != nil:
		g.Pipeline = st.Current
		g.State, g.Reason, g.Waits = currentGate(st.Current, st.Target)
		if g.State == GatePassed {
			g.TestedTree = st.Current.TestedTree
		}
	case st.Settled != nil:
		p := st.Settled.Pipeline
		g.Pipeline = p
		if p.Status == StatusCancelled {
			g.State, g.Reason = GateCancelled, fmt.Sprintf("pipeline #%d was cancelled", p.ID)
			break
		}
		g.State, g.Waits = GateError, st.Settled.Retrying
		g.Reason = fmt.Sprintf("pipeline #%d couldn't run: %s", p.ID, trimStop(noResultError(p)))
		if g.Waits {
			g.Reason += "; devpad runs it once more by itself"
		}
	case st.Latest != nil:
		// A merge request that runs by itself runs again on what changed.
		g.State, g.Pipeline, g.Waits = GateOutOfDate, st.Latest, true
		g.Reason = fmt.Sprintf("the latest pipeline, #%d, is out of date: the branch, %s or its jobs have changed since", st.Latest.ID, st.Target)
	default:
		g.State, g.Waits = GateNotRun, true
		g.Reason = "no pipeline has run on it yet"
	}
	return g
}

// currentGate is the gate's state when p is the pipeline of the merge
// request's commits and jobs as they are now, with why it is shut and
// whether a result is still to come.
func currentGate(p *Pipeline, target string) (GateState, string, bool) {
	switch p.Status {
	case StatusPassed:
		return GatePassed, "", false
	case StatusQueued:
		return GateQueued, fmt.Sprintf("pipeline #%d is still queued", p.ID), true
	case StatusRunning:
		return GateRunning, fmt.Sprintf("pipeline #%d is still running", p.ID), true
	case StatusConflict:
		return GateConflict, fmt.Sprintf("pipeline #%d found the branch doesn't merge onto %s", p.ID, target), false
	case StatusFailed:
		if failed := failedJobs(p); len(failed) > 0 {
			return GateFailed, fmt.Sprintf("pipeline #%d failed (%s)", p.ID, strings.Join(failed, ", ")), false
		}
		return GateFailed, fmt.Sprintf("pipeline #%d failed", p.ID), false
	}
	// No other status counts for a configured pipeline; whatever it is, it
	// is no pass.
	return GateFailed, fmt.Sprintf("pipeline #%d ended %s", p.ID, p.Status), false
}

// failedJobs names the jobs of p whose code failed.
func failedJobs(p *Pipeline) []string {
	var names []string
	for _, j := range p.Jobs {
		if j.Status.failed() {
			names = append(names, j.Name)
		}
	}
	return names
}

func trimStop(s string) string {
	return strings.TrimRight(strings.TrimSpace(s), ". ")
}
