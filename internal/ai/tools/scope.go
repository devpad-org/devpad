package tools

import (
	"context"

	"github.com/devpad-org/devpad/internal/workspace"
)

// Scope is what a run's workspace is: whose it is, what kind, and the
// review or merge request it is there for. One resolver answers it for
// every provider, so they all agree on what a run is.
//
// A Scope holds IDs, not records: providers load what they show, so a scope
// resolved at the start of a long run never stands in for current state.
// The zero Scope is no workspace at all, and gets no provider's tools.
type Scope struct {
	UserID      int64
	WorkspaceID int64
	Kind        workspace.Kind
	// ProjectID is 0 for a standalone workspace.
	ProjectID int64
	// Branch is the branch a sandbox owns, empty otherwise.
	Branch string
	// Review is set in a review sandbox whose review is pending.
	Review *ReviewScope
	// TaskMR is set in a task sandbox whose branch has an open merge
	// request for the task the sandbox is working on.
	TaskMR *TaskMRScope
	// Task is set in the sandbox of a task that is not merged or
	// cancelled.
	Task *TaskScope
}

// ReviewScope is the pending review a review sandbox's agent is writing.
type ReviewScope struct {
	ReviewID       int64
	MergeRequestID int64
	// TargetBranch is the branch the change would merge into.
	TargetBranch string
	// TargetSHA is the commit of TargetBranch the review compares the
	// change against, which its instructions come from. It is empty for a
	// review from before devpad recorded it, whose instructions come from
	// the branch as it is.
	TargetSHA string
}

// TaskMRScope is the open merge request of the task a sandbox works on.
type TaskMRScope struct {
	TaskID         int64
	MergeRequestID int64
}

// TaskScope is the task a sandbox works on.
type TaskScope struct {
	TaskID int64
}

// ProjectWorkspace reports whether this is the user's own workspace for a
// project, as opposed to a sandbox or a standalone workspace.
func (s Scope) ProjectWorkspace() bool {
	return s.Kind == workspace.KindInteractive && s.ProjectID != 0
}

// ScopeResolver works out a run's scope from its workspace.
type ScopeResolver interface {
	// Resolve fails when the workspace is not the user's or cannot be read,
	// and when what it is there for cannot be found out. A workspace with
	// no review or merge request is not a failure.
	Resolve(ctx context.Context, userID, workspaceID int64) (Scope, error)
}
