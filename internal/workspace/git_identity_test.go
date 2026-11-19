package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

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
	if got := ra.paths(); len(got) == 0 || got[0] != want {
		t.Fatalf("seed requests = %v, want %s first", got, want)
	}
}

func TestSaveDuringSeedReachesTheRepo(t *testing.T) {
	svc, ra := setupSandboxService(t)
	ctx := context.Background()
	ws, err := svc.CreateProjectWorkspace(ctx, 1, ProjectWorkspaceSpec{ProjectID: 7, Name: "proj"})
	if err != nil {
		t.Fatalf("create project workspace: %v", err)
	}
	ra.mu.Lock()
	ra.onSeed = func() {
		if err := svc.(*service).users.SetGitIdentity(ctx, 1, "Test User", "me@example.com"); err != nil {
			t.Errorf("saving git identity: %v", err)
		}
		// The save's own sync finds the repo not seeded yet.
		if updated, complete := svc.SyncGitIdentity(ctx, 1); updated != 0 || !complete {
			t.Errorf("SyncGitIdentity during the seed = %d, %v; want 0, true", updated, complete)
		}
	}
	ra.mu.Unlock()

	if _, err := svc.SeedSandboxRepo(ctx, 1, ws.ID, "main", strings.NewReader("bundle")); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if n := gitActions(ra); n != 1 {
		t.Fatalf("sent %d git actions, want 1", n)
	}
	if req := sentGitIdentity(t, ra); req.UserName != "Test User" || req.UserEmail != "me@example.com" {
		t.Fatalf("sent %+v", req)
	}
}

// cancelOnAgentWait cancels a start's context once the container is up and
// devpad goes looking for its agent, as a caller leaving then would.
type cancelOnAgentWait struct {
	*mockContainerManager
	cancel  context.CancelFunc
	started bool
}

func (m *cancelOnAgentWait) Start(ctx context.Context, id string) error {
	m.started = true
	return m.mockContainerManager.Start(ctx, id)
}

func (m *cancelOnAgentWait) GetIP(ctx context.Context, id, network string) (string, error) {
	if m.started {
		m.cancel()
	}
	return m.mockContainerManager.GetIP(ctx, id, network)
}

func TestStartSyncsGitIdentityAfterCallerLeaves(t *testing.T) {
	svc, ra := setupSandboxService(t)
	background := context.Background()
	ws := seededProjectWorkspace(t, svc, ra)
	if _, err := svc.Stop(background, 1, ws.ID); err != nil {
		t.Fatalf("stop: %v", err)
	}
	saveGitIdentity(t, svc, "Test User", "me@example.com")

	ctx, cancel := context.WithCancel(background)
	defer cancel()
	s := svc.(*service)
	s.container = &cancelOnAgentWait{mockContainerManager: s.container.(*mockContainerManager), cancel: cancel}

	if _, err := svc.Start(ctx, 1, ws.ID); err != nil {
		t.Fatalf("start: %v", err)
	}
	if ctx.Err() == nil {
		t.Fatal("the caller never left")
	}
	if n := gitActions(ra); n != 1 {
		t.Fatalf("sent %d git actions, want 1", n)
	}
}

func TestRestartConvertedSyncsGitIdentity(t *testing.T) {
	svc, ca := setupConvertService(t)
	ctx := context.Background()
	ws, err := createLegacyWorkspace(ctx, svc, 1, "mine", "")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := svc.AttachToProject(ctx, 1, ws.ID, 7, nil); err != nil {
		t.Fatalf("attach: %v", err)
	}
	saveGitIdentity(t, svc, "Test User", "me@example.com")
	ca.reset()

	if err := svc.RestartConverted(ctx, 1, ws.ID); err != nil {
		t.Fatalf("restart: %v", err)
	}
	if !slices.Contains(ca.paths(), "/api/git/action") {
		t.Fatalf("requests after the restart = %v, want a git action", ca.paths())
	}
}

func TestSyncGitIdentityStopsWaitingForTheLock(t *testing.T) {
	svc, ra := setupSandboxService(t)
	ctx := context.Background()
	created := seededProjectWorkspace(t, svc, ra)
	ws, err := svc.Get(ctx, 1, created.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	saveGitIdentity(t, svc, "Test User", "")
	s := svc.(*service)

	// A sync stalled by its agent holds the lock.
	unlock, err := s.lockAgentSync(ctx, ws.ID)
	if err != nil {
		t.Fatalf("lock: %v", err)
	}
	waiting, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	if err := s.syncGitIdentity(waiting, ws); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("sync behind a held lock = %v, want the deadline", err)
	}
	if n := gitActions(ra); n != 0 {
		t.Fatalf("sent %d git actions without the lock", n)
	}

	unlock()
	if err := s.syncGitIdentity(ctx, ws); err != nil {
		t.Fatalf("sync after the lock freed: %v", err)
	}
	if n := gitActions(ra); n != 1 {
		t.Fatalf("sent %d git actions, want 1", n)
	}
}

func TestSyncGitIdentityContainsAgentErrors(t *testing.T) {
	hostile := "first line\nforged log line " + strings.Repeat("x", 10_000)
	tests := []struct {
		name   string
		answer http.HandlerFunc
	}{
		{"failed action", func(w http.ResponseWriter, _ *http.Request) {
			json.NewEncoder(w).Encode(map[string]any{"success": false, "error": hostile})
		}},
		{"error status", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]any{"error": hostile})
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, ra := setupSandboxService(t)
			ctx := context.Background()
			created := seededProjectWorkspace(t, svc, ra)
			ws, err := svc.Get(ctx, 1, created.ID)
			if err != nil {
				t.Fatalf("get: %v", err)
			}
			saveGitIdentity(t, svc, "Test User", "")
			ra.mu.Lock()
			ra.gitAction = tt.answer
			ra.mu.Unlock()

			err = svc.(*service).syncGitIdentity(ctx, ws)
			if err == nil {
				t.Fatal("expected an error")
			}
			if msg := err.Error(); strings.Contains(msg, "\n") || len(msg) > 2*maxFailureReason {
				t.Fatalf("error is %d bytes, newline %v", len(msg), strings.Contains(msg, "\n"))
			}
		})
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
