package workspace

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/devpad-org/devpad/internal/agent"
)

const gitActionPath = "/api/git/action?"

func gitActions(ra *recordingAgent) int {
	n := 0
	for _, p := range ra.paths() {
		if p == gitActionPath {
			n++
		}
	}
	return n
}

// seededProjectWorkspace returns a running project workspace with its repo
// seeded, and forgets the requests that took.
func seededProjectWorkspace(t *testing.T, svc Service, ra *recordingAgent) *Workspace {
	t.Helper()
	ctx := context.Background()
	ws, err := svc.CreateProjectWorkspace(ctx, 1, ProjectWorkspaceSpec{ProjectID: 7, Name: "proj"})
	if err != nil {
		t.Fatalf("create project workspace: %v", err)
	}
	if _, err := svc.SeedSandboxRepo(ctx, 1, ws.ID, "main", strings.NewReader("bundle")); err != nil {
		t.Fatalf("seed: %v", err)
	}
	ra.mu.Lock()
	ra.requests = nil
	ra.mu.Unlock()
	return ws
}

func saveGitIdentity(t *testing.T, svc Service, name, email string) {
	t.Helper()
	if err := svc.(*service).users.SetGitIdentity(context.Background(), 1, name, email); err != nil {
		t.Fatalf("saving git identity: %v", err)
	}
}

func sentGitIdentity(t *testing.T, ra *recordingAgent) agent.GitActionRequest {
	t.Helper()
	ra.mu.Lock()
	defer ra.mu.Unlock()
	var req agent.GitActionRequest
	if err := json.Unmarshal([]byte(ra.bodies["/api/git/action"]), &req); err != nil {
		t.Fatalf("decoding git action: %v", err)
	}
	return req
}

func TestSeedUsesChosenGitIdentity(t *testing.T) {
	svc, ra := setupSandboxService(t)
	ctx := context.Background()
	saveGitIdentity(t, svc, "Test User", "")

	ws, err := svc.CreateProjectWorkspace(ctx, 1, ProjectWorkspaceSpec{ProjectID: 7, Name: "proj"})
	if err != nil {
		t.Fatalf("create project workspace: %v", err)
	}
	if _, err := svc.SeedSandboxRepo(ctx, 1, ws.ID, "main", strings.NewReader("bundle")); err != nil {
		t.Fatalf("seed: %v", err)
	}
	// The email was left empty, so it is still the account's.
	want := "/api/repo/seed?base=main&branch=main&email=test%40test.com&name=Test+User"
	if got := ra.paths(); len(got) != 1 || got[0] != want {
		t.Fatalf("seed requests = %v, want %s", got, want)
	}
}

func TestSyncGitIdentityReachesRunningSeededWorkspaces(t *testing.T) {
	svc, ra := setupSandboxService(t)
	ctx := context.Background()
	seeded := seededProjectWorkspace(t, svc, ra)
	// Neither of these holds a repo devpad keeps the identity of.
	if _, err := svc.CreateProjectWorkspace(ctx, 1, ProjectWorkspaceSpec{ProjectID: 8, Name: "unseeded"}); err != nil {
		t.Fatalf("create unseeded workspace: %v", err)
	}
	stopped := seededStoppedSandbox(t, svc, ra)
	saveGitIdentity(t, svc, "Test User", "me@example.com")

	updated, complete := svc.SyncGitIdentity(ctx, 1)
	if updated != 1 || !complete {
		t.Fatalf("SyncGitIdentity = %d, %v; want 1, true (workspace %d, not stopped %d)", updated, complete, seeded.ID, stopped.ID)
	}
	req := sentGitIdentity(t, ra)
	if req.Action != "set-config" || req.UserName != "Test User" || req.UserEmail != "me@example.com" {
		t.Fatalf("sent %+v", req)
	}
}

func seededStoppedSandbox(t *testing.T, svc Service, ra *recordingAgent) *Workspace {
	t.Helper()
	ctx := context.Background()
	ws, err := svc.CreateSandbox(ctx, 1, sandboxSpec("feature"))
	if err != nil {
		t.Fatalf("create sandbox: %v", err)
	}
	if _, err := svc.SeedSandboxRepo(ctx, 1, ws.ID, "main", strings.NewReader("bundle")); err != nil {
		t.Fatalf("seed sandbox: %v", err)
	}
	if _, err := svc.Stop(ctx, 1, ws.ID); err != nil {
		t.Fatalf("stop sandbox: %v", err)
	}
	ra.mu.Lock()
	ra.requests = nil
	ra.mu.Unlock()
	return ws
}

func TestStartSyncsGitIdentityOnlyOnceSaved(t *testing.T) {
	svc, ra := setupSandboxService(t)
	ctx := context.Background()
	ws := seededProjectWorkspace(t, svc, ra)

	restart := func() {
		t.Helper()
		if _, err := svc.Stop(ctx, 1, ws.ID); err != nil {
			t.Fatalf("stop: %v", err)
		}
		if _, err := svc.Start(ctx, 1, ws.ID); err != nil {
			t.Fatalf("start: %v", err)
		}
	}

	// Nothing saved: whatever is set in the repo stays.
	restart()
	if n := gitActions(ra); n != 0 {
		t.Fatalf("start before any save sent %d git actions", n)
	}

	saveGitIdentity(t, svc, "Test User", "me@example.com")
	restart()
	if n := gitActions(ra); n != 1 {
		t.Fatalf("start after a save sent %d git actions, want 1", n)
	}

	// Going back to the account's values reaches the repo too.
	saveGitIdentity(t, svc, "", "")
	restart()
	if req := sentGitIdentity(t, ra); req.UserName != "testuser" || req.UserEmail != "test@test.com" {
		t.Fatalf("after clearing, sent %+v", req)
	}
}

func TestSyncGitIdentityReportsUnreachableWorkspace(t *testing.T) {
	svc, ra := setupSandboxService(t)
	ctx := context.Background()
	seededProjectWorkspace(t, svc, ra)
	saveGitIdentity(t, svc, "Test User", "")
	svc.(*service).agentPort = 1 // nothing listens here

	if updated, complete := svc.SyncGitIdentity(ctx, 1); updated != 0 || complete {
		t.Fatalf("SyncGitIdentity = %d, %v; want 0, false", updated, complete)
	}
}

func TestStandaloneWorkspaceKeepsItsGitIdentity(t *testing.T) {
	ws := &Workspace{Kind: KindInteractive, RepoSeeded: true}
	if ws.holdsDevpadRepo() {
		t.Fatal("a standalone workspace's repo is the user's own")
	}
}
