package project

import (
	"context"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/devpad-org/devpad/internal/agent"
)

// recordingSettingsListener records each settings change it is told of,
// taking the project's branch lock as it is told when svc is set, which
// deadlocks if a change is told under it.
type recordingSettingsListener struct {
	mu      sync.Mutex
	changes [][2]Project
	svc     *service
}

func (l *recordingSettingsListener) ProjectSettingsChanged(_ context.Context, before, after *Project) {
	if l.svc != nil {
		l.svc.lockBranches(after.ID)()
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.changes = append(l.changes, [2]Project{*before, *after})
}

func (l *recordingSettingsListener) told() [][2]Project {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(l.changes)
}

// A merge tells the branch listeners where its target now is and what was
// merged into it; one that fails tells nobody.
func TestMergeTellsBranchListeners(t *testing.T) {
	e := setupTest(t)
	ctx := context.Background()
	p := e.createProject(t, CreateInput{Name: "proj"})
	hook := &recordingBranchListener{svc: e.svc}
	e.svc.AddBranchListener(hook)

	if _, err := e.svc.Merge(ctx, e.userID, p.ID, MergeInput{Source: "task/1"}); err != nil {
		t.Fatalf("merge: %v", err)
	}
	if got := hook.told(); len(got) != 1 || got[0] != "merged:main@merged<-task/1" {
		t.Fatalf("listener told %v", got)
	}

	e.repoAgent.mergeErr = map[string]any{"error": "task/1 conflicts with main", "conflicts": []string{"a.go"}}
	if _, err := e.svc.Merge(ctx, e.userID, p.ID, MergeInput{Source: "task/1"}); err == nil {
		t.Fatal("expected the conflicting merge to fail")
	}
	if got := hook.told(); len(got) != 1 {
		t.Fatalf("a failed merge reached the listener: %v", got)
	}
}

// Changing the branches or the pipeline setting tells the settings
// listeners the project before and after, once the branch lock is free; a
// change that is refused tells nobody.
func TestSettingsChangesTellSettingsListeners(t *testing.T) {
	e := setupTest(t)
	ctx := context.Background()
	p := e.createProject(t, CreateInput{Name: "proj"})
	e.repoAgent.branches = []agent.RepoRef{{Name: "main", SHA: "m"}, {Name: "develop", SHA: "d"}, {Name: "feature", SHA: "f"}}
	l := &recordingSettingsListener{svc: e.svc}
	e.svc.AddSettingsListener(l)
	list := func(b ...string) *[]string { return &b }

	if _, err := e.svc.SetBranches(ctx, e.userID, p.ID, BranchesInput{ProtectedBranches: []string{"develop"}, PipelineBranches: list("develop")}); err != nil {
		t.Fatalf("set branches: %v", err)
	}
	got := l.told()
	if len(got) != 1 || len(got[0][0].PipelineBranches) != 0 || strings.Join(got[0][1].PipelineBranches, ",") != "develop" || strings.Join(got[0][1].ProtectedBranches, ",") != "develop" {
		t.Fatalf("told %+v", got)
	}
	if _, err := e.svc.SetBranches(ctx, e.userID, p.ID, BranchesInput{PipelineBranches: list("feature")}); err == nil {
		t.Fatal("expected an unprotected branch refused")
	}
	if got := l.told(); len(got) != 1 {
		t.Fatalf("a refused change reached the listener: %d changes", len(got))
	}

	const config = "jobs:\n  test:\n    run: make test\n"
	if _, err := e.svc.SetPipelineConfig(ctx, e.userID, p.ID, config); err != nil {
		t.Fatalf("set pipeline config: %v", err)
	}
	got = l.told()
	if len(got) != 2 || got[1][0].PipelineConfig != "" || got[1][1].PipelineConfig != config || strings.Join(got[1][1].PipelineBranches, ",") != "develop" {
		t.Fatalf("told %+v", got[1:])
	}
	if _, err := e.svc.SetPipelineConfig(ctx, e.otherID, p.ID, ""); err == nil {
		t.Fatal("expected another user refused")
	}
	if got := l.told(); len(got) != 2 {
		t.Fatalf("a refused setting reached the listener: %d changes", len(got))
	}
}

// Devpad catches up on every user's projects that run pipelines somewhere.
func TestListPipelineProjects(t *testing.T) {
	e := setupTest(t)
	ctx := context.Background()
	ticked := e.createProject(t, CreateInput{Name: "ticked"})
	e.repoAgent.branches = []agent.RepoRef{{Name: "main", SHA: "m"}}
	list := []string{"main"}
	if _, err := e.svc.SetBranches(ctx, e.userID, ticked.ID, BranchesInput{PipelineBranches: &list}); err != nil {
		t.Fatalf("set branches: %v", err)
	}
	e.createProject(t, CreateInput{Name: "unticked"})
	got, err := e.svc.ListPipelineProjects(ctx)
	if err != nil || len(got) != 1 || got[0].ID != ticked.ID || !got[0].RunsPipelines("main") {
		t.Fatalf("listed %+v (%v)", got, err)
	}
}
