package mergerequest

import (
	"context"
	"errors"
	"fmt"
)

// Pipelines is what the merge request service reads of its merge requests'
// pipelines. The pipeline package answers, and server.go adapts it, so this
// package never imports that one. The service asks under a merge request's
// lock, so each method must take none of this service's locks and start
// nothing: it only reads. Until it is set, nothing merges.
type Pipelines interface {
	// MergeGate is the merge gate: whether merge request mrID's pipeline
	// lets it merge now. Every merge asks it first.
	MergeGate(ctx context.Context, userID, projectID, mrID int64) (*Gate, error)
	// PipelineReport reads pipeline pipelineID of the user's project, for
	// its task's agent and its reviewer: its jobs, the last outputTail bytes
	// of each failed job's output with devpad's notes on it (none when
	// outputTail is 0), and whether each failing job fails in the target
	// branch's own pipeline of the commit it merged onto too.
	PipelineReport(ctx context.Context, userID, projectID, pipelineID int64, outputTail int) (*PipelineReport, error)
	// JobScripts reads the scripts of the jobs a merge request into branch
	// runs: those of the pipeline at branch's tip, never a change's, for
	// the look-closely mark. None when branch has no pipeline.
	JobScripts(ctx context.Context, userID, projectID int64, branch string) ([]string, error)
}

// GateState is how the merge gate stands for a merge request. The pipeline
// package decides it, with the same states under the same names, which
// merges over the gate record and the app confirms; server.go maps each of
// its states to one of these, and a state it doesn't know to a shut gate
// nothing merges over.
type GateState string

const (
	// GateUnguarded is no pipeline guarding the merge request; the gate is
	// open.
	GateUnguarded GateState = "unguarded"
	// GatePassed is its pipeline passing on its commits and jobs as they
	// are now; the gate is open.
	GatePassed GateState = "passed"
	// The rest shut the gate: its pipeline is still to finish, none has
	// run, the latest is out of date, it failed, found the branch doesn't
	// merge, doesn't parse, couldn't run (GateErrored, the pipeline
	// package's GateError) or was cancelled, or the merge request can no
	// longer merge.
	GateQueued    GateState = "queued"
	GateRunning   GateState = "running"
	GateNotRun    GateState = "not_run"
	GateOutOfDate GateState = "out_of_date"
	GateFailed    GateState = "failed"
	GateConflict  GateState = "conflict"
	GateInvalid   GateState = "invalid"
	GateErrored   GateState = "error"
	GateCancelled GateState = "cancelled"
	GateClosed    GateState = "closed"
)

// GateStates lists every gate state.
func GateStates() []GateState {
	return []GateState{
		GateUnguarded, GatePassed, GateQueued, GateRunning, GateNotRun, GateOutOfDate,
		GateFailed, GateConflict, GateInvalid, GateErrored, GateCancelled, GateClosed,
	}
}

// Gate is how the merge gate stands for a merge request now.
type Gate struct {
	// State names it.
	State GateState
	// Guarded says a pipeline guards the merge request: its target is ticked
	// to run pipelines and has one configured.
	Guarded bool
	// Clear says the gate lets the merge request merge: no pipeline guards
	// it, or its pipeline passed on its head merged onto the target's tip
	// as it is now.
	Clear bool
	// Waits says a result that could clear the gate is still to come by
	// itself, such as from a pipeline still running.
	Waits bool
	// Overridable says the user can merge anyway.
	Overridable bool
	// Reason says why the gate is shut, as a clause without a full stop.
	Reason string
	// PipelineID is the pipeline State is about, 0 when none is.
	PipelineID int64
	// HeadSHA and TargetSHA are the commits a pipeline must have tested to
	// clear the gate: the merge request's head, and its target's tip as the
	// gate read it, where a merge it clears must land.
	HeadSHA   string
	TargetSHA string
	// TestedTree is, when the gate is open because the pipeline passed, the
	// tree it tested, which the merge must land; empty otherwise.
	TestedTree string
}

// confirmation is what the user confirms merging over when the gate is g.
func (g Gate) confirmation() GateConfirmation {
	return GateConfirmation{State: g.State, PipelineID: g.PipelineID, HeadSHA: g.HeadSHA, TargetSHA: g.TargetSHA}
}

// ErrPipelineGate refuses a merge the merge request's pipeline gate doesn't
// let through.
var ErrPipelineGate = errors.New("the merge request's pipeline hasn't let it merge")

// GateError is a merge the pipeline gate refused, and how the gate stood.
type GateError struct {
	Target string
	Gate   *Gate
	// Changed says the user confirmed merging over another state than the
	// gate is in now.
	Changed bool
}

func (e *GateError) Error() string {
	switch {
	case e.Gate.State == GateConflict:
		return fmt.Sprintf("%s: resolve the conflicts, and its pipeline runs again", e.Gate.Reason)
	case !e.Gate.Overridable:
		return fmt.Sprintf("this merge request can't merge: %s", e.Gate.Reason)
	case e.Changed:
		return fmt.Sprintf("the pipeline or the commits changed since you chose to merge anyway: %s", e.Gate.Reason)
	}
	return fmt.Sprintf("%s's pipeline must pass before this merges, and %s", e.Target, e.Gate.Reason)
}

func (e *GateError) Unwrap() error { return ErrPipelineGate }

// targetMovedError is a merge the repo refused because the target moved
// past the commit the gate read. When a pipeline guards it, the pipeline
// has the target's new commit to test first: a move into a ticked branch
// runs the pipelines of the merge requests into it again. Unguarded, the
// target may have gained a pipeline meanwhile, so the merge is asked for
// again rather than landing on a commit the gate never read.
type targetMovedError struct {
	target  string
	guarded bool
	cause   error
}

func (e *targetMovedError) Error() string {
	if !e.guarded {
		return fmt.Sprintf("%s moved as this was merging: merge again onto %s as it is now", e.target, e.target)
	}
	return fmt.Sprintf("%s moved as this was merging, so its pipeline must pass on %s as it is now first", e.target, e.target)
}

func (e *targetMovedError) Unwrap() []error { return []error{ErrInvalidState, e.cause} }

// admission is what a merge request merges on, once let through.
type admission struct {
	// targetSHA is the commit the target must still be at when the repo
	// merges, or "" for wherever it is.
	targetSHA string
	// guarded says a pipeline guards the merge, tested on targetSHA.
	guarded bool
	// expectedTree is the tree the merge must land, the one its passing
	// pipeline tested, or "" when no pipeline's pass let it through, as on
	// an override, which has no tested tree to land.
	expectedTree string
	// override is the user merging over the pipeline gate, nil otherwise.
	override *PipelineOverride
}

func (s *service) SetPipelines(p Pipelines) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pipelines = p
}

// errNoPipelines is reading a merge request's pipelines before devpad can.
var errNoPipelines = errors.New("devpad can't read merge requests' pipelines yet")

// pipelineReader is what reads the merge requests' pipelines, or nil
// before it is set.
func (s *service) pipelineReader() Pipelines {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.pipelines
}

// readGate reads the pipeline gate of mr, whose lock the caller holds, so
// its head cannot move meanwhile. Without Pipelines set nothing merges.
func (s *service) readGate(ctx context.Context, mr *MergeRequest) (*Gate, error) {
	pipelines := s.pipelineReader()
	if pipelines == nil {
		return nil, errNoPipelines
	}
	g, err := pipelines.MergeGate(ctx, mr.UserID, mr.ProjectID, mr.ID)
	if err != nil {
		return nil, fmt.Errorf("reading the merge request's pipeline: %w", err)
	}
	if g.HeadSHA != "" && g.HeadSHA != mr.HeadSHA {
		return nil, fmt.Errorf("%w: the merge request moved to %s while its pipeline was read", ErrInvalidState, short(mr.HeadSHA))
	}
	return g, nil
}

// admitUser lets the user merge mr once its pipeline gate is clear, or over
// the gate when the user confirmed merging anyway with it standing exactly
// as it does now (override: the same state, pipeline, head and target),
// which the merge records. The merge lands on the target's commit the gate
// read, so that for a merge a pipeline guards, the tree that passed is the
// tree that lands, whichever strategy merges it.
func (s *service) admitUser(ctx context.Context, mr *MergeRequest, override *GateConfirmation) (*admission, error) {
	g, err := s.readGate(ctx, mr)
	if err != nil {
		return nil, err
	}
	// Whether or not a pipeline guards it, it lands on the target's commit
	// the gate read, so a target that moved meanwhile, perhaps to one that
	// a pipeline now guards, is never merged onto unread.
	adm := &admission{guarded: g.Guarded, targetSHA: g.TargetSHA}
	switch {
	case g.Clear:
		if g.Guarded {
			adm.expectedTree = g.TestedTree
		}
		return adm, nil
	case !g.Overridable || override == nil:
		return nil, &GateError{Target: mr.TargetBranch, Gate: g}
	case *override != g.confirmation():
		return nil, &GateError{Target: mr.TargetBranch, Gate: g, Changed: true}
	}
	adm.override = &PipelineOverride{State: g.State, PipelineID: g.PipelineID}
	return adm, nil
}

// mergeRefused is why the repo refused mr's merge: err, or, when the
// target has moved past the commit the merge was to land on, a
// targetMovedError.
func (s *service) mergeRefused(ctx context.Context, mr *MergeRequest, adm *admission, err error) error {
	if adm.targetSHA == "" {
		return err
	}
	now, readErr := s.canonicalSHA(ctx, mr.UserID, mr.ProjectID, mr.TargetBranch)
	if readErr != nil || now == adm.targetSHA {
		return err
	}
	return &targetMovedError{target: mr.TargetBranch, guarded: adm.guarded, cause: err}
}
