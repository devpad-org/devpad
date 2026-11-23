package mergerequest

import (
	"context"
	"errors"
	"fmt"
)

// PipelineGate is the merge gate: whether a merge request's pipeline lets
// it merge. The pipeline package decides it, and server.go adapts it, so
// this package never imports that one. Every merge asks it under the merge
// request's lock, so it must take none of this service's locks and start
// nothing: it only reads.
type PipelineGate interface {
	MergeGate(ctx context.Context, userID, projectID, mrID int64) (*Gate, error)
}

// Gate is how the merge gate stands for a merge request now.
type Gate struct {
	// State names it, such as "passed", "running" or "failed", or
	// "unguarded" when no pipeline guards the merge request.
	State string
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
	case !e.Gate.Overridable:
		return fmt.Sprintf("this merge request can't merge: %s", e.Gate.Reason)
	case e.Changed:
		return fmt.Sprintf("the pipeline changed since you chose to merge anyway: %s", e.Gate.Reason)
	}
	return fmt.Sprintf("%s's pipeline must pass before this merges, and %s", e.Target, e.Gate.Reason)
}

func (e *GateError) Unwrap() error { return ErrPipelineGate }

// targetMovedError is a merge guarded by a pipeline that the repo refused
// because the target moved past the commit the pipeline tested, so that
// the pipeline has the target's new commit to test first. A move into a
// ticked branch runs the pipelines of the merge requests into it again.
type targetMovedError struct {
	target string
	cause  error
}

func (e *targetMovedError) Error() string {
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
	// override is the user merging over the pipeline gate, nil otherwise.
	override *PipelineOverride
}

func (s *service) SetPipelineGate(g PipelineGate) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.gate = g
}

// readGate reads the pipeline gate of mr, whose lock the caller holds, so
// its head cannot move meanwhile. Without a gate set nothing merges.
func (s *service) readGate(ctx context.Context, mr *MergeRequest) (*Gate, error) {
	s.mu.Lock()
	gate := s.gate
	s.mu.Unlock()
	if gate == nil {
		return nil, errors.New("devpad can't read merge requests' pipelines yet")
	}
	g, err := gate.MergeGate(ctx, mr.UserID, mr.ProjectID, mr.ID)
	if err != nil {
		return nil, fmt.Errorf("reading the merge request's pipeline: %w", err)
	}
	if g.HeadSHA != "" && g.HeadSHA != mr.HeadSHA {
		return nil, fmt.Errorf("%w: the merge request moved to %s while its pipeline was read", ErrInvalidState, short(mr.HeadSHA))
	}
	return g, nil
}

// admitUser lets the user merge mr once its pipeline gate is clear, or over
// the gate when the user confirmed merging anyway in the state it is in
// now (override), which the merge records. A merge a pipeline guards lands
// on the target's commit the gate read, so that the tree that passed is the
// tree that lands, whichever strategy merges it.
func (s *service) admitUser(ctx context.Context, mr *MergeRequest, override string) (*admission, error) {
	g, err := s.readGate(ctx, mr)
	if err != nil {
		return nil, err
	}
	adm := &admission{guarded: g.Guarded}
	if g.Guarded {
		adm.targetSHA = g.TargetSHA
	}
	switch {
	case g.Clear:
		return adm, nil
	case !g.Overridable || override == "":
		return nil, &GateError{Target: mr.TargetBranch, Gate: g}
	case override != g.State:
		return nil, &GateError{Target: mr.TargetBranch, Gate: g, Changed: true}
	}
	adm.override = &PipelineOverride{State: g.State, PipelineID: g.PipelineID}
	return adm, nil
}

// mergeRefused is why the repo refused mr's merge: err, or, when a pipeline
// guards it and the target has moved past the commit it tested, a
// targetMovedError.
func (s *service) mergeRefused(ctx context.Context, mr *MergeRequest, adm *admission, err error) error {
	if !adm.guarded || adm.targetSHA == "" {
		return err
	}
	now, readErr := s.canonicalSHA(ctx, mr.UserID, mr.ProjectID, mr.TargetBranch)
	if readErr != nil || now == adm.targetSHA {
		return err
	}
	return &targetMovedError{target: mr.TargetBranch, cause: err}
}
