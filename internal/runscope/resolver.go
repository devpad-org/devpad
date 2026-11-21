// Package runscope works out what an AI run's workspace is: a project's own
// workspace, a task or review sandbox, or a standalone workspace, and the
// task, review or merge request a sandbox is there for. It is the one place that
// decides; tool providers are handed the answer as an aitools.Scope.
package runscope

import (
	"context"
	"fmt"

	aitools "github.com/devpad-org/devpad/internal/ai/tools"
	"github.com/devpad-org/devpad/internal/workspace"
)

// Workspaces reads the run's workspace.
type Workspaces interface {
	Get(ctx context.Context, userID, workspaceID int64) (*workspace.Workspace, error)
}

// Sandboxes finds what a sandbox is there for. Each returns nil when there
// is none, and an error only when it could not find out.
type Sandboxes interface {
	// PendingReview is the review a review sandbox's agent is writing.
	PendingReview(ctx context.Context, userID int64, ws *workspace.Workspace) (*aitools.ReviewScope, error)
	// TaskMergeRequest is the open merge request of the task a task
	// sandbox works on.
	TaskMergeRequest(ctx context.Context, userID int64, ws *workspace.Workspace) (*aitools.TaskMRScope, error)
}

// Tasks finds the task a task sandbox works on.
type Tasks interface {
	// SandboxTask is the open task whose sandbox ws is, or nil when there
	// is none; an error only when it could not find out.
	SandboxTask(ctx context.Context, userID int64, ws *workspace.Workspace) (*aitools.TaskScope, error)
}

// Resolver resolves run scopes.
type Resolver struct {
	workspaces Workspaces
	sandboxes  Sandboxes
	tasks      Tasks
}

var _ aitools.ScopeResolver = (*Resolver)(nil)

// New creates a resolver.
func New(workspaces Workspaces, sandboxes Sandboxes, tasks Tasks) *Resolver {
	return &Resolver{workspaces: workspaces, sandboxes: sandboxes, tasks: tasks}
}

// Resolve works out the scope of a run in the workspace.
func (r *Resolver) Resolve(ctx context.Context, userID, workspaceID int64) (aitools.Scope, error) {
	ws, err := r.workspaces.Get(ctx, userID, workspaceID)
	if err != nil {
		return aitools.Scope{}, err
	}
	s := aitools.Scope{UserID: userID, WorkspaceID: ws.ID, Kind: ws.Kind, ProjectID: ws.ProjectID, Branch: ws.Branch}
	if ws.ProjectID == 0 {
		return s, nil
	}
	// The kind decides which question is asked: only a review sandbox can
	// be writing a review, and only a task sandbox answers a task's merge
	// request.
	switch ws.Kind {
	case workspace.KindReview:
		if s.Review, err = r.sandboxes.PendingReview(ctx, userID, ws); err != nil {
			return aitools.Scope{}, fmt.Errorf("finding the sandbox's review: %w", err)
		}
	case workspace.KindTask:
		if s.Task, err = r.tasks.SandboxTask(ctx, userID, ws); err != nil {
			return aitools.Scope{}, fmt.Errorf("finding the sandbox's task: %w", err)
		}
		if s.TaskMR, err = r.sandboxes.TaskMergeRequest(ctx, userID, ws); err != nil {
			return aitools.Scope{}, fmt.Errorf("finding the task's merge request: %w", err)
		}
	}
	return s, nil
}
