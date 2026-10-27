package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/devpad-org/devpad/internal/agentbin"
	"github.com/devpad-org/devpad/internal/auth"
)

// Restarting a workspace whose agent is outdated installs the current agent
// before the restart, so one restart is enough to run it.
func TestRestartUpdatesAgentWithOneRestart(t *testing.T) {
	db := setupTestDB(t)

	var mu sync.Mutex
	running := "old"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/healthz":
			w.WriteHeader(http.StatusOK)
		case "/api/version":
			mu.Lock()
			defer mu.Unlock()
			json.NewEncoder(w).Encode(map[string]string{"version": running})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	host, portStr, _ := net.SplitHostPort(strings.TrimPrefix(srv.URL, "http://"))
	port, _ := strconv.Atoi(portStr)

	cm := &mockContainerManager{ipOverride: host}
	// A restart runs whichever agent was last copied in.
	cm.onRestart = func() {
		if len(cm.copied) > 0 {
			mu.Lock()
			running = agentbin.Version
			mu.Unlock()
		}
	}
	svc := NewService(NewRepository(db), cm, auth.NewUserRepository(db), nil)
	svc.(*service).agentPort = port
	ctx := context.Background()

	ws, err := svc.CreateProjectWorkspace(ctx, 1, ProjectWorkspaceSpec{ProjectID: 7, Name: "project"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	got, err := svc.Restart(ctx, 1, ws.ID)
	if err != nil {
		t.Fatalf("restart: %v", err)
	}
	if got.Status != StatusRunning {
		t.Fatalf("status after restart = %q, want running", got.Status)
	}
	if len(cm.copied) != 1 || !strings.HasSuffix(cm.copied[0], "usr/local/bin/devpad-agent") {
		t.Fatalf("copied %v, want the agent binary once", cm.copied)
	}
	if len(cm.restarted) != 1 {
		t.Fatalf("restarted %d times, want 1", len(cm.restarted))
	}

	// A current agent is left alone.
	if _, err := svc.Restart(ctx, 1, ws.ID); err != nil {
		t.Fatalf("second restart: %v", err)
	}
	if len(cm.copied) != 1 || len(cm.restarted) != 2 {
		t.Fatalf("after second restart: copied %v, restarted %d times", cm.copied, len(cm.restarted))
	}

	// A stopped workspace is left stopped, so restart can't start a sandbox
	// outside its project.
	if _, err := svc.Stop(ctx, 1, ws.ID); err != nil {
		t.Fatalf("stop: %v", err)
	}
	startedBefore := len(cm.started)
	if _, err := svc.Restart(ctx, 1, ws.ID); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("restart stopped: got %v, want ErrNotRunning", err)
	}
	if len(cm.started) != startedBefore || len(cm.restarted) != 2 {
		t.Fatalf("restarting a stopped workspace started it %d times, restarted it %d times",
			len(cm.started)-startedBefore, len(cm.restarted)-2)
	}
}

// A sandbox starts only through StartSandbox, which its project calls after
// counting it against the sandbox limit; StartSandbox starts nothing else.
func TestStartRefusesSandboxes(t *testing.T) {
	svc, _ := setupSandboxService(t)
	ctx := context.Background()

	sb, err := svc.CreateSandbox(ctx, 1, sandboxSpec("feature"))
	if err != nil {
		t.Fatalf("create sandbox: %v", err)
	}
	if _, err := svc.Stop(ctx, 1, sb.ID); err != nil {
		t.Fatalf("stop sandbox: %v", err)
	}
	if _, err := svc.Start(ctx, 1, sb.ID); !errors.Is(err, ErrSandboxStart) {
		t.Fatalf("start sandbox: got %v, want ErrSandboxStart", err)
	}
	if got, err := svc.Get(ctx, 1, sb.ID); err != nil || got.Status != StatusStopped {
		t.Fatalf("sandbox after refused start: %+v, %v", got, err)
	}
	if got, err := svc.StartSandbox(ctx, 1, sb.ID); err != nil || got.Status != StatusRunning {
		t.Fatalf("StartSandbox: %+v, %v", got, err)
	}

	pw, err := svc.CreateProjectWorkspace(ctx, 1, ProjectWorkspaceSpec{ProjectID: 7, Name: "project"})
	if err != nil {
		t.Fatalf("create project workspace: %v", err)
	}
	if _, err := svc.Stop(ctx, 1, pw.ID); err != nil {
		t.Fatalf("stop project workspace: %v", err)
	}
	if _, err := svc.StartSandbox(ctx, 1, pw.ID); err == nil {
		t.Fatal("StartSandbox started a project workspace")
	}
	if _, err := svc.Start(ctx, 1, pw.ID); err != nil {
		t.Fatalf("start project workspace: %v", err)
	}
}

// Restart is refused while the container is being replaced, like start and
// stop.
func TestRestartRefusedWhileRebuilding(t *testing.T) {
	svc, _ := setupSandboxService(t)
	ctx := context.Background()
	ws, err := svc.CreateProjectWorkspace(ctx, 1, ProjectWorkspaceSpec{ProjectID: 7, Name: "project"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	s := svc.(*service)
	s.convertMu.Lock()
	s.rebuilding = map[int64]bool{ws.ID: true}
	s.convertMu.Unlock()

	if _, err := svc.Restart(ctx, 1, ws.ID); !errors.Is(err, ErrRebuilding) {
		t.Fatalf("restart during rebuild: got %v, want ErrRebuilding", err)
	}
}
