package project

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/devpad-org/devpad/internal/workspace"
)

func TestOpenWorkspaceCreatesOnceFromDefaultBranch(t *testing.T) {
	e := setupTest(t)
	ctx := context.Background()
	p := e.createProject(t, CreateInput{Name: "proj", SetupScript: "make deps"})

	if _, err := e.svc.Workspace(ctx, e.userID, p.ID); !errors.Is(err, ErrNoWorkspace) {
		t.Fatalf("expected ErrNoWorkspace before opening, got %v", err)
	}
	ws, created, err := e.svc.OpenWorkspace(ctx, e.userID, p.ID)
	if err != nil || !created {
		t.Fatalf("open: created=%v err=%v", created, err)
	}
	if !ws.IsProjectWorkspace() || ws.ProjectID != p.ID || ws.Name != "proj" {
		t.Fatalf("unexpected project workspace: %+v", ws)
	}
	if got := e.workspaces.seeds[ws.ID]; got != "main <- bundle-of-main" {
		t.Fatalf("expected seed from main, got %q", got)
	}
	if e.workspaces.commands[ws.ID] != "make deps" {
		t.Fatalf("expected setup script started, got %q", e.workspaces.commands[ws.ID])
	}

	again, created, err := e.svc.OpenWorkspace(ctx, e.userID, p.ID)
	if err != nil || created || again.ID != ws.ID {
		t.Fatalf("reopen: ws=%+v created=%v err=%v", again, created, err)
	}
	got, err := e.svc.Workspace(ctx, e.userID, p.ID)
	if err != nil || got.ID != ws.ID {
		t.Fatalf("workspace: %+v %v", got, err)
	}
	if _, _, err := e.svc.OpenWorkspace(ctx, e.otherID, p.ID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("expected ErrForbidden for another user, got %v", err)
	}
}

func TestOpenWorkspaceConcurrentlyCreatesOne(t *testing.T) {
	e := setupTest(t)
	p := e.createProject(t, CreateInput{Name: "proj"})

	var wg sync.WaitGroup
	ids := make([]int64, 4)
	errs := make([]error, 4)
	for i := range ids {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ws, _, err := e.svc.OpenWorkspace(context.Background(), e.userID, p.ID)
			errs[i] = err
			if ws != nil {
				ids[i] = ws.ID
			}
		}(i)
	}
	wg.Wait()
	for i := range ids {
		if errs[i] != nil || ids[i] != ids[0] {
			t.Fatalf("open %d: id=%d err=%v, want id %d", i, ids[i], errs[i], ids[0])
		}
	}
}

func TestOpenWorkspaceRemovesUnseededWorkspace(t *testing.T) {
	e := setupTest(t)
	p := e.createProject(t, CreateInput{Name: "proj"})
	e.workspaces.seedErr = errors.New("seed failed")

	if _, _, err := e.svc.OpenWorkspace(context.Background(), e.userID, p.ID); err == nil {
		t.Fatal("expected a seed failure")
	}
	if len(e.workspaces.deleted) != 1 {
		t.Fatalf("expected the unseeded workspace removed, got %v", e.workspaces.deleted)
	}
	if _, err := e.svc.Workspace(context.Background(), e.userID, p.ID); !errors.Is(err, ErrNoWorkspace) {
		t.Fatalf("expected no workspace left, got %v", err)
	}
}

func TestProjectWorkspaceIsNotASandbox(t *testing.T) {
	e := setupTest(t)
	ctx := context.Background()
	p := e.createProject(t, CreateInput{Name: "proj", MaxSandboxes: 1})
	ws, _, err := e.svc.OpenWorkspace(ctx, e.userID, p.ID)
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	// It neither uses the sandbox limit nor shows up as a sandbox.
	if _, err := e.svc.CreateSandbox(ctx, e.userID, p.ID, SandboxInput{Branch: "task/1"}); err != nil {
		t.Fatalf("create sandbox within limit: %v", err)
	}
	sandboxes, err := e.svc.ListSandboxes(ctx, e.userID, p.ID)
	if err != nil || len(sandboxes) != 1 || sandboxes[0].Branch != "task/1" {
		t.Fatalf("expected only the task sandbox, got %+v %v", sandboxes, err)
	}
	if _, err := e.svc.StartSandbox(ctx, e.userID, p.ID, ws.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected the project workspace to be no sandbox, got %v", err)
	}
	if _, err := e.svc.PublishSandbox(ctx, e.userID, p.ID, ws.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected the project workspace to be no sandbox, got %v", err)
	}
}

func TestPublishWorkspaceBranch(t *testing.T) {
	e := setupTest(t)
	ctx := context.Background()
	p := e.createProject(t, CreateInput{Name: "proj"})
	ws, _, err := e.svc.OpenWorkspace(ctx, e.userID, p.ID)
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	got, err := e.svc.PublishWorkspaceBranch(ctx, e.userID, p.ID, "feature/login")
	if err != nil || got.Branch != "feature/login" {
		t.Fatalf("publish: %+v %v", got, err)
	}
	if string(e.repoAgent.imported["feature/login"]) != "workspace-bundle-of-feature/login" {
		t.Fatalf("expected the workspace's bundle imported, got %q", e.repoAgent.imported["feature/login"])
	}
	if len(e.repoAgent.importedFF) != 1 || e.repoAgent.importedFF[0] != "feature/login" {
		t.Fatalf("expected a fast-forward-only import, got %v", e.repoAgent.importedFF)
	}

	if _, err := e.svc.CreateSandbox(ctx, e.userID, p.ID, SandboxInput{Branch: "task/1"}); err != nil {
		t.Fatalf("create sandbox: %v", err)
	}
	tests := []struct {
		name   string
		branch string
		want   error
	}{
		{"default branch", "main", ErrPublishDefault},
		{"sandbox branch", "task/1", ErrBranchInUse},
		{"flag", "-f", ErrInvalidInput},
		{"invalid", "a..b", ErrInvalidInput},
		{"empty", "", ErrInvalidInput},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := e.svc.PublishWorkspaceBranch(ctx, e.userID, p.ID, tt.branch); !errors.Is(err, tt.want) {
				t.Fatalf("expected %v, got %v", tt.want, err)
			}
		})
	}
	if len(e.repoAgent.imported) != 1 {
		t.Fatalf("refused publishes reached the repo: %v", e.repoAgent.imported)
	}

	e.workspaces.setStatus(ws.ID, workspace.StatusStopped)
	if _, err := e.svc.PublishWorkspaceBranch(ctx, e.userID, p.ID, "feature/x"); !errors.Is(err, workspace.ErrNotRunning) {
		t.Fatalf("expected ErrNotRunning for a stopped workspace, got %v", err)
	}
}

func TestPublishWorkspaceBranchWithoutWorkspace(t *testing.T) {
	e := setupTest(t)
	p := e.createProject(t, CreateInput{Name: "proj"})
	if _, err := e.svc.PublishWorkspaceBranch(context.Background(), e.userID, p.ID, "feature"); !errors.Is(err, ErrNoWorkspace) {
		t.Fatalf("expected ErrNoWorkspace, got %v", err)
	}
	if _, err := e.svc.CheckoutWorkspaceBranch(context.Background(), e.userID, p.ID, "main"); !errors.Is(err, ErrNoWorkspace) {
		t.Fatalf("expected ErrNoWorkspace, got %v", err)
	}
	if _, err := e.svc.SyncWorkspaceBase(context.Background(), e.userID, p.ID); !errors.Is(err, ErrNoWorkspace) {
		t.Fatalf("expected ErrNoWorkspace, got %v", err)
	}
}

func TestPublishingBranchBlocksSandboxClaim(t *testing.T) {
	e := setupTest(t)
	ctx := context.Background()
	p := e.createProject(t, CreateInput{Name: "proj"})

	if err := e.svc.reservePublish(ctx, p, "feature"); err != nil {
		t.Fatalf("reserve publish: %v", err)
	}
	if _, err := e.svc.CreateSandbox(ctx, e.userID, p.ID, SandboxInput{Branch: "feature"}); !errors.Is(err, ErrBranchInUse) {
		t.Fatalf("expected ErrBranchInUse while publishing, got %v", err)
	}
	if err := e.svc.reservePublish(ctx, p, "feature"); !errors.Is(err, ErrBranchInUse) {
		t.Fatalf("expected concurrent publishes of one branch refused, got %v", err)
	}
	e.svc.releasePublish(p.ID, "feature")
	if _, err := e.svc.CreateSandbox(ctx, e.userID, p.ID, SandboxInput{Branch: "feature"}); err != nil {
		t.Fatalf("create sandbox after publish: %v", err)
	}
}

func TestCheckoutAndSyncWorkspace(t *testing.T) {
	e := setupTest(t)
	ctx := context.Background()
	p := e.createProject(t, CreateInput{Name: "proj"})
	ws, _, err := e.svc.OpenWorkspace(ctx, e.userID, p.ID)
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	// Any canonical branch can be checked out, including a sandbox's.
	if _, err := e.svc.CreateSandbox(ctx, e.userID, p.ID, SandboxInput{Branch: "task/1"}); err != nil {
		t.Fatalf("create sandbox: %v", err)
	}
	got, err := e.svc.CheckoutWorkspaceBranch(ctx, e.userID, p.ID, "task/1")
	if err != nil || got.Upstream != "devpad/task/1" {
		t.Fatalf("checkout: %+v %v", got, err)
	}
	if e.workspaces.seeds[ws.ID] != "checkout task/1 <- bundle-of-task/1" {
		t.Fatalf("expected the canonical branch's bundle, got %q", e.workspaces.seeds[ws.ID])
	}
	if _, err := e.svc.CheckoutWorkspaceBranch(ctx, e.userID, p.ID, "--all"); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("expected ErrInvalidInput, got %v", err)
	}

	synced, err := e.svc.SyncWorkspaceBase(ctx, e.userID, p.ID)
	if err != nil || synced.Branch != "devpad/main" || e.workspaces.seeds[ws.ID] != "devpad/main <- bundle-of-main" {
		t.Fatalf("sync: %+v %v seed=%q", synced, err, e.workspaces.seeds[ws.ID])
	}
}

func TestDeleteRemovesProjectWorkspace(t *testing.T) {
	e := setupTest(t)
	ctx := context.Background()
	p := e.createProject(t, CreateInput{Name: "proj"})
	ws, _, err := e.svc.OpenWorkspace(ctx, e.userID, p.ID)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := e.svc.Delete(ctx, e.userID, p.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if len(e.workspaces.deleted) != 1 || e.workspaces.deleted[0] != ws.ID {
		t.Fatalf("expected the project workspace deleted, got %v", e.workspaces.deleted)
	}
}

func TestMigrationAllowsOneProjectWorkspace(t *testing.T) {
	e := setupTest(t)
	ctx := context.Background()
	p := e.createProject(t, CreateInput{Name: "proj"})
	repo := workspace.NewRepository(e.db.Conn())

	insert := func(kind workspace.Kind, branch string) error {
		return repo.Create(ctx, &workspace.Workspace{UserID: e.userID, Name: "ws", ProjectID: p.ID, Kind: kind, Branch: branch, Status: workspace.StatusRunning})
	}
	if err := insert(workspace.KindInteractive, ""); err != nil {
		t.Fatalf("first project workspace: %v", err)
	}
	if err := insert(workspace.KindInteractive, ""); err == nil {
		t.Fatal("expected a second project workspace to fail")
	}
	if err := insert(workspace.KindTask, "feature"); err != nil {
		t.Fatalf("sandbox next to the project workspace: %v", err)
	}
	if err := insert(workspace.KindReview, "feature"); err == nil {
		t.Fatal("expected sandboxes to still own their branch")
	}
}
