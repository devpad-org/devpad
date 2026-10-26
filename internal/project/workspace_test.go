package project

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/devpad-org/devpad/internal/agent"
	"github.com/devpad-org/devpad/internal/auth"
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
	e.repoAgent.branches = []agent.RepoRef{{Name: "main", SHA: "m"}, {Name: "release", SHA: "r"}}
	if _, err := e.svc.SetBranches(ctx, e.userID, p.ID, BranchesInput{ProtectedBranches: []string{"release"}}); err != nil {
		t.Fatalf("protect release: %v", err)
	}
	tests := []struct {
		name   string
		branch string
		want   error
	}{
		{"default branch", "main", ErrPublishProtected},
		{"protected branch", "release", ErrPublishProtected},
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
	if _, err := e.svc.SyncWorkspaceBase(context.Background(), e.userID, p.ID, ""); !errors.Is(err, ErrNoWorkspace) {
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
	if _, err := e.svc.CreateSandbox(ctx, e.userID, p.ID, SandboxInput{Branch: "feature"}); !errors.Is(err, ErrBranchPublishing) {
		t.Fatalf("expected ErrBranchPublishing while publishing, got %v", err)
	}
	if err := e.svc.reservePublish(ctx, p, "feature"); !errors.Is(err, ErrBranchPublishing) {
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

	synced, err := e.svc.SyncWorkspaceBase(ctx, e.userID, p.ID, "")
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

func TestOpenWorkspaceOutlivesTheRequest(t *testing.T) {
	e := setupTest(t)
	p := e.createProject(t, CreateInput{Name: "proj"})

	// The client went away before the workspace was made.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	ws, created, err := e.svc.OpenWorkspace(ctx, e.userID, p.ID)
	if err != nil || !created || !ws.RepoSeeded {
		t.Fatalf("open with a cancelled request: ws=%+v created=%v err=%v", ws, created, err)
	}
	if e.workspaces.seeds[ws.ID] != "main <- bundle-of-main" {
		t.Fatalf("expected the workspace seeded, got %q", e.workspaces.seeds[ws.ID])
	}
}

func TestOpenWorkspaceReplacesUnseededWorkspace(t *testing.T) {
	e := setupTest(t)
	ctx := context.Background()
	p := e.createProject(t, CreateInput{Name: "proj"})

	// Seeding and the cleanup after it both fail, leaving an unseeded
	// workspace behind.
	e.workspaces.seedErr = errors.New("seed failed")
	e.workspaces.deleteErr = errors.New("docker unavailable")
	if _, _, err := e.svc.OpenWorkspace(ctx, e.userID, p.ID); err == nil {
		t.Fatal("expected a seed failure")
	}
	stale, err := e.svc.Workspace(ctx, e.userID, p.ID)
	if err != nil || stale.RepoSeeded {
		t.Fatalf("expected an unseeded workspace left behind, got %+v %v", stale, err)
	}

	// While it cannot be removed, opening fails rather than handing it out.
	e.workspaces.seedErr = nil
	if _, _, err := e.svc.OpenWorkspace(ctx, e.userID, p.ID); err == nil {
		t.Fatal("expected an unremovable unseeded workspace to fail the open")
	}

	e.workspaces.deleteErr = nil
	ws, created, err := e.svc.OpenWorkspace(ctx, e.userID, p.ID)
	if err != nil || !created || ws.ID == stale.ID || !ws.RepoSeeded {
		t.Fatalf("expected a fresh workspace replacing %d: ws=%+v created=%v err=%v", stale.ID, ws, created, err)
	}
	if len(e.workspaces.deleted) != 1 || e.workspaces.deleted[0] != stale.ID {
		t.Fatalf("expected the unseeded workspace removed, got %v", e.workspaces.deleted)
	}
	again, created, err := e.svc.OpenWorkspace(ctx, e.userID, p.ID)
	if err != nil || created || again.ID != ws.ID {
		t.Fatalf("reopen: ws=%+v created=%v err=%v", again, created, err)
	}
}

type recordingPublishHook struct {
	mu    sync.Mutex
	calls []string
}

func (h *recordingPublishHook) BranchPublished(_ context.Context, userID, projectID int64, branch, sha string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.calls = append(h.calls, strings.Join([]string{branch, sha}, "@"))
}

func TestPublishWorkspaceBranchNotifiesHook(t *testing.T) {
	e := setupTest(t)
	ctx := context.Background()
	p := e.createProject(t, CreateInput{Name: "proj"})
	hook := &recordingPublishHook{}
	e.svc.SetPublishHook(hook)
	if _, _, err := e.svc.OpenWorkspace(ctx, e.userID, p.ID); err != nil {
		t.Fatalf("open: %v", err)
	}

	if _, err := e.svc.PublishWorkspaceBranch(ctx, e.userID, p.ID, "feature"); err != nil {
		t.Fatalf("publish: %v", err)
	}
	if len(hook.calls) != 1 || hook.calls[0] != "feature@abc123" {
		t.Fatalf("expected the hook told of the new head, got %v", hook.calls)
	}

	// A refused publish moves nothing, so nobody is told.
	e.repoAgent.importErr, e.repoAgent.importStatus = "feature has commits the published branch lacks", http.StatusConflict
	if _, err := e.svc.PublishWorkspaceBranch(ctx, e.userID, p.ID, "feature"); err == nil {
		t.Fatal("expected the non-fast-forward publish refused")
	}
	if len(hook.calls) != 1 {
		t.Fatalf("a refused publish reached the hook: %v", hook.calls)
	}
}

func TestNonFastForwardPublishIsAConflict(t *testing.T) {
	e := setupTest(t)
	ctx := context.Background()
	p := e.createProject(t, CreateInput{Name: "proj"})
	if _, _, err := e.svc.OpenWorkspace(ctx, e.userID, p.ID); err != nil {
		t.Fatalf("open: %v", err)
	}
	e.repoAgent.importErr, e.repoAgent.importStatus = "feature has commits the published branch lacks; merge devpad/feature first", http.StatusConflict

	_, err := e.svc.PublishWorkspaceBranch(ctx, e.userID, p.ID, "feature")
	var agentErr *agent.AgentError
	if !errors.As(err, &agentErr) || agentErr.StatusCode != http.StatusConflict {
		t.Fatalf("expected the repo agent's 409, got %v", err)
	}

	id := strconv.FormatInt(p.ID, 10)
	req := httptest.NewRequest(http.MethodPost, "/api/projects/"+id+"/workspace/publish", strings.NewReader(`{"branch":"feature"}`))
	req.SetPathValue("id", id)
	req = req.WithContext(context.WithValue(req.Context(), auth.UserContextKey, &auth.User{ID: e.userID}))
	rec := httptest.NewRecorder()
	NewHandler(e.svc).HandlePublishWorkspaceBranch(rec, req)
	var body map[string]string
	json.NewDecoder(rec.Body).Decode(&body)
	if rec.Code != http.StatusConflict || !strings.Contains(body["error"], "merge devpad/feature first") {
		t.Fatalf("expected 409 with the agent's message, got %d %v", rec.Code, body)
	}
}

func TestPendingSandboxBlocksPublish(t *testing.T) {
	e := setupTest(t)
	ctx := context.Background()
	p := e.createProject(t, CreateInput{Name: "proj"})
	if _, _, err := e.svc.OpenWorkspace(ctx, e.userID, p.ID); err != nil {
		t.Fatalf("open: %v", err)
	}

	if err := e.svc.reserveSandbox(ctx, p, "feature", false); err != nil {
		t.Fatalf("reserve sandbox: %v", err)
	}
	if _, err := e.svc.PublishWorkspaceBranch(ctx, e.userID, p.ID, "feature"); !errors.Is(err, ErrBranchInUse) {
		t.Fatalf("expected ErrBranchInUse while a sandbox is being created, got %v", err)
	}
	e.svc.releaseSandbox(p.ID, "feature")
	if _, err := e.svc.PublishWorkspaceBranch(ctx, e.userID, p.ID, "feature"); err != nil {
		t.Fatalf("publish once the claim is released: %v", err)
	}
}

func TestDeleteRefusesWhileBusyAndBlocksNewWork(t *testing.T) {
	e := setupTest(t)
	ctx := context.Background()
	p := e.createProject(t, CreateInput{Name: "proj"})

	if err := e.svc.reserveSandbox(ctx, p, "task/1", false); err != nil {
		t.Fatalf("reserve sandbox: %v", err)
	}
	if err := e.svc.Delete(ctx, e.userID, p.ID); !errors.Is(err, ErrBusy) {
		t.Fatalf("expected ErrBusy with a sandbox being created, got %v", err)
	}
	e.svc.releaseSandbox(p.ID, "task/1")
	if err := e.svc.reservePublish(ctx, p, "feature"); err != nil {
		t.Fatalf("reserve publish: %v", err)
	}
	if err := e.svc.Delete(ctx, e.userID, p.ID); !errors.Is(err, ErrBusy) {
		t.Fatalf("expected ErrBusy with a branch being published, got %v", err)
	}
	e.svc.releasePublish(p.ID, "feature")

	if err := e.svc.beginDelete(p.ID); err != nil {
		t.Fatalf("begin delete: %v", err)
	}
	if _, err := e.svc.CreateSandbox(ctx, e.userID, p.ID, SandboxInput{Branch: "task/2"}); !errors.Is(err, ErrDeleting) {
		t.Fatalf("expected ErrDeleting for a new sandbox, got %v", err)
	}
	if _, _, err := e.svc.OpenWorkspace(ctx, e.userID, p.ID); !errors.Is(err, ErrDeleting) {
		t.Fatalf("expected ErrDeleting for opening the workspace, got %v", err)
	}
	if err := e.svc.reservePublish(ctx, p, "feature"); !errors.Is(err, ErrDeleting) {
		t.Fatalf("expected ErrDeleting for a publish, got %v", err)
	}
	if err := e.svc.Delete(ctx, e.userID, p.ID); !errors.Is(err, ErrDeleting) {
		t.Fatalf("expected a second delete refused, got %v", err)
	}
	e.svc.endDelete(p.ID)
	if _, err := e.svc.CreateSandbox(ctx, e.userID, p.ID, SandboxInput{Branch: "task/2"}); err != nil {
		t.Fatalf("create sandbox after an abandoned delete: %v", err)
	}
}

func TestDeleteWaitsForWorkspaceOpenInFlight(t *testing.T) {
	e := setupTest(t)
	ctx := context.Background()
	p := e.createProject(t, CreateInput{Name: "proj"})

	// Stand in for an open that has taken the lock and is about to insert
	// the project workspace.
	unlock := e.svc.lockWorkspace(p.ID)
	done := make(chan error, 1)
	go func() { done <- e.svc.Delete(ctx, e.userID, p.ID) }()

	time.Sleep(50 * time.Millisecond)
	e.containers.mu.Lock()
	removed := len(e.containers.removed)
	e.containers.mu.Unlock()
	if removed != 0 {
		t.Fatal("delete removed the repo container while an open was in flight")
	}
	ws := e.workspaces.add(&workspace.Workspace{UserID: e.userID, ProjectID: p.ID, Kind: workspace.KindInteractive, Status: workspace.StatusRunning, RepoSeeded: true})
	unlock()

	if err := <-done; err != nil {
		t.Fatalf("delete: %v", err)
	}
	if len(e.workspaces.deleted) != 1 || e.workspaces.deleted[0] != ws.ID {
		t.Fatalf("expected the workspace the open made removed, got %v", e.workspaces.deleted)
	}
	if _, err := e.svc.Get(ctx, e.userID, p.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected the project gone, got %v", err)
	}
}

func TestWorkspaceBranchStatus(t *testing.T) {
	e := setupTest(t)
	ctx := context.Background()
	p := e.createProject(t, CreateInput{Name: "proj"})
	ws, _, err := e.svc.OpenWorkspace(ctx, e.userID, p.ID)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := e.svc.CreateSandbox(ctx, e.userID, p.ID, SandboxInput{Branch: "task/1"}); err != nil {
		t.Fatalf("create sandbox: %v", err)
	}
	e.repoAgent.mu.Lock()
	e.repoAgent.branches = []agent.RepoRef{{Name: "main", SHA: "new-main"}, {Name: "task/1", SHA: "t1"}}
	e.repoAgent.bundled = nil
	e.repoAgent.mu.Unlock()
	e.workspaces.mu.Lock()
	e.workspaces.branchStatus = map[string]agent.RepoBranchStatus{
		// The workspace last saw an older main and has a commit of its own.
		"main": {Branch: "main", SHA: "local-main", UpstreamSHA: "old-main", Ahead: 1, Behind: 0},
		// Up to date with the project.
		"task/1": {Branch: "task/1", SHA: "t1", UpstreamSHA: "t1"},
		// Never published.
		"mine": {Branch: "mine", SHA: "m1", Ahead: 2},
	}
	e.workspaces.fetchedSHA = map[string]string{"main": "new-main"}
	e.workspaces.mu.Unlock()

	got, err := e.svc.WorkspaceBranchStatus(ctx, e.userID, p.ID, "main")
	if err != nil {
		t.Fatalf("main: %v", err)
	}
	if !got.Published || got.CanonicalSHA != "new-main" || got.Ahead != 1 || got.SandboxOwned {
		t.Fatalf("unexpected status for main: %+v", got)
	}
	if e.workspaces.seeds[ws.ID] != "devpad/main <- bundle-of-main" {
		t.Fatalf("expected a stale copy of main refreshed first, got %q", e.workspaces.seeds[ws.ID])
	}

	e.repoAgent.mu.Lock()
	e.repoAgent.bundled = nil
	e.repoAgent.mu.Unlock()
	got, err = e.svc.WorkspaceBranchStatus(ctx, e.userID, p.ID, "task/1")
	if err != nil || !got.Published || !got.SandboxOwned || got.Ahead != 0 {
		t.Fatalf("task/1: %+v %v", got, err)
	}
	if len(e.repoAgent.bundled) != 0 {
		t.Fatalf("an up-to-date copy should not be fetched again, bundled %v", e.repoAgent.bundled)
	}

	got, err = e.svc.WorkspaceBranchStatus(ctx, e.userID, p.ID, "mine")
	if err != nil || got.Published || got.Ahead != 2 || got.CanonicalSHA != "" {
		t.Fatalf("mine: %+v %v", got, err)
	}

	if _, err := e.svc.WorkspaceBranchStatus(ctx, e.userID, p.ID, "--all"); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("expected ErrInvalidInput, got %v", err)
	}
	if _, err := e.svc.WorkspaceBranchStatus(ctx, e.otherID, p.ID, "main"); err == nil {
		t.Fatal("expected another user refused")
	}
}

func TestFetchWorkspaceBranches(t *testing.T) {
	e := setupTest(t)
	ctx := context.Background()
	p := e.createProject(t, CreateInput{Name: "proj"})
	ws, _, err := e.svc.OpenWorkspace(ctx, e.userID, p.ID)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	e.repoAgent.mu.Lock()
	e.repoAgent.branches = []agent.RepoRef{{Name: "main", SHA: "m"}, {Name: "task/1", SHA: "t1"}}
	e.repoAgent.bundled = nil
	e.repoAgent.mu.Unlock()

	if err := e.svc.FetchWorkspaceBranches(ctx, e.userID, p.ID, []string{"main", "task/1", "main"}); err != nil {
		t.Fatalf("fetch: %v", err)
	}
	// One bundle carries every branch, each named once.
	if got := e.workspaces.seeds[ws.ID]; got != "devpad/{main,task/1} <- bundle-of-main,task/1" {
		t.Fatalf("expected the branches fetched as devpad copies from one bundle, got %q", got)
	}
	if len(e.repoAgent.bundled) != 1 {
		t.Fatalf("expected one bundle, got %v", e.repoAgent.bundled)
	}

	// Names come from the workspace: each must be valid and exist, and
	// nothing travels unless all do.
	e.repoAgent.mu.Lock()
	e.repoAgent.bundled = nil
	e.repoAgent.mu.Unlock()
	for _, branches := range [][]string{nil, {"--all"}, {"main", "missing"}} {
		if err := e.svc.FetchWorkspaceBranches(ctx, e.userID, p.ID, branches); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("fetch %v: expected ErrInvalidInput, got %v", branches, err)
		}
	}
	if len(e.repoAgent.bundled) != 0 {
		t.Fatalf("a refused fetch bundled %v", e.repoAgent.bundled)
	}
}
