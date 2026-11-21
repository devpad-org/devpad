package runscope

import (
	"context"
	"errors"
	"slices"
	"testing"

	aitools "github.com/devpad-org/devpad/internal/ai/tools"
	"github.com/devpad-org/devpad/internal/workspace"
)

const owner, stranger = int64(1), int64(2)

type fakeWorkspaces map[int64]*workspace.Workspace

func (f fakeWorkspaces) Get(_ context.Context, userID, id int64) (*workspace.Workspace, error) {
	ws, ok := f[id]
	if !ok {
		return nil, workspace.ErrNotFound
	}
	if ws.UserID != userID {
		return nil, workspace.ErrForbidden
	}
	clone := *ws
	return &clone, nil
}

// fakeSandboxes answers for the workspaces it has a review, task or merge
// request for, and records which it was asked about.
type fakeSandboxes struct {
	reviews map[int64]*aitools.ReviewScope
	tasks   map[int64]*aitools.TaskScope
	mrs     map[int64]*aitools.TaskMRScope
	err     error
	asked   []string
}

func (f *fakeSandboxes) SandboxTask(_ context.Context, _ int64, ws *workspace.Workspace) (*aitools.TaskScope, error) {
	f.asked = append(f.asked, "sandbox task")
	return f.tasks[ws.ID], f.err
}

func (f *fakeSandboxes) PendingReview(_ context.Context, _ int64, ws *workspace.Workspace) (*aitools.ReviewScope, error) {
	f.asked = append(f.asked, "review")
	return f.reviews[ws.ID], f.err
}

func (f *fakeSandboxes) TaskMergeRequest(_ context.Context, _ int64, ws *workspace.Workspace) (*aitools.TaskMRScope, error) {
	f.asked = append(f.asked, "task")
	return f.mrs[ws.ID], f.err
}

func TestResolve(t *testing.T) {
	review := &aitools.ReviewScope{ReviewID: 4, MergeRequestID: 3, TargetBranch: "main"}
	taskMR := &aitools.TaskMRScope{TaskID: 5, MergeRequestID: 3}
	sandboxTask := &aitools.TaskScope{TaskID: 5}
	workspaces := fakeWorkspaces{
		1: {ID: 1, UserID: owner, ProjectID: 9, Kind: workspace.KindInteractive},
		2: {ID: 2, UserID: owner, Kind: workspace.KindInteractive},
		3: {ID: 3, UserID: owner, ProjectID: 9, Kind: workspace.KindTask, Branch: "task/5"},
		4: {ID: 4, UserID: owner, ProjectID: 9, Kind: workspace.KindTask, Branch: "task/6"},
		5: {ID: 5, UserID: owner, ProjectID: 9, Kind: workspace.KindReview, Branch: "review/3"},
		6: {ID: 6, UserID: owner, ProjectID: 9, Kind: workspace.KindReview, Branch: "review/2"},
	}
	// The fake has an answer for nearly every workspace, so asking a
	// workspace the wrong question would show up as a review or merge
	// request it should not have.
	reviews := map[int64]*aitools.ReviewScope{1: review, 2: review, 3: review, 4: review, 5: review}
	mrs := map[int64]*aitools.TaskMRScope{1: taskMR, 2: taskMR, 3: taskMR, 5: taskMR, 6: taskMR}
	tasks := map[int64]*aitools.TaskScope{1: sandboxTask, 2: sandboxTask, 3: sandboxTask, 5: sandboxTask, 6: sandboxTask}

	tests := []struct {
		name    string
		user    int64
		ws      int64
		want    aitools.Scope
		project bool
		asked   []string
	}{
		{"project workspace", owner, 1, aitools.Scope{UserID: owner, WorkspaceID: 1, Kind: workspace.KindInteractive, ProjectID: 9}, true, nil},
		{"standalone workspace", owner, 2, aitools.Scope{UserID: owner, WorkspaceID: 2, Kind: workspace.KindInteractive}, false, nil},
		{"task sandbox with an open merge request", owner, 3, aitools.Scope{UserID: owner, WorkspaceID: 3, Kind: workspace.KindTask, ProjectID: 9, Branch: "task/5", Task: sandboxTask, TaskMR: taskMR}, false, []string{"sandbox task", "task"}},
		{"task sandbox without one, or an open task", owner, 4, aitools.Scope{UserID: owner, WorkspaceID: 4, Kind: workspace.KindTask, ProjectID: 9, Branch: "task/6"}, false, []string{"sandbox task", "task"}},
		{"review sandbox with a pending review", owner, 5, aitools.Scope{UserID: owner, WorkspaceID: 5, Kind: workspace.KindReview, ProjectID: 9, Branch: "review/3", Review: review}, false, []string{"review"}},
		{"review sandbox whose review is done", owner, 6, aitools.Scope{UserID: owner, WorkspaceID: 6, Kind: workspace.KindReview, ProjectID: 9, Branch: "review/2"}, false, []string{"review"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sandboxes := &fakeSandboxes{reviews: reviews, tasks: tasks, mrs: mrs}
			got, err := New(workspaces, sandboxes, sandboxes).Resolve(context.Background(), tt.user, tt.ws)
			if err != nil {
				t.Fatal(err)
			}
			if got.UserID != tt.want.UserID || got.WorkspaceID != tt.want.WorkspaceID || got.Kind != tt.want.Kind || got.ProjectID != tt.want.ProjectID || got.Branch != tt.want.Branch || got.Review != tt.want.Review || got.Task != tt.want.Task || got.TaskMR != tt.want.TaskMR {
				t.Fatalf("got %+v, want %+v", got, tt.want)
			}
			if got.ProjectWorkspace() != tt.project {
				t.Fatalf("ProjectWorkspace() = %v", got.ProjectWorkspace())
			}
			if !slices.Equal(sandboxes.asked, tt.asked) {
				t.Fatalf("asked %v, want %v", sandboxes.asked, tt.asked)
			}
		})
	}
}

func TestResolveFailures(t *testing.T) {
	workspaces := fakeWorkspaces{
		1: {ID: 1, UserID: owner, ProjectID: 9, Kind: workspace.KindReview},
		2: {ID: 2, UserID: owner, ProjectID: 9, Kind: workspace.KindTask, Branch: "task/1"},
	}
	ctx := context.Background()
	r := New(workspaces, &fakeSandboxes{}, &fakeSandboxes{})
	if _, err := r.Resolve(ctx, stranger, 1); !errors.Is(err, workspace.ErrForbidden) {
		t.Fatalf("another user's workspace: got %v", err)
	}
	if _, err := r.Resolve(ctx, owner, 99); !errors.Is(err, workspace.ErrNotFound) {
		t.Fatalf("a workspace that is gone: got %v", err)
	}

	// Not knowing what a sandbox is there for is a failure, never a sandbox
	// with nothing to do.
	locked := errors.New("database is locked")
	failing := &fakeSandboxes{err: locked}
	r = New(workspaces, failing, failing)
	for _, id := range []int64{1, 2} {
		if got, err := r.Resolve(ctx, owner, id); !errors.Is(err, locked) || got != (aitools.Scope{}) {
			t.Errorf("workspace %d: expected the lookup's error and no scope, got %+v %v", id, got, err)
		}
	}
}
