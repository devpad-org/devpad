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

	// A stopped workspace is started rather than restarted.
	if _, err := svc.Stop(ctx, 1, ws.ID); err != nil {
		t.Fatalf("stop: %v", err)
	}
	startedBefore := len(cm.started)
	got, err = svc.Restart(ctx, 1, ws.ID)
	if err != nil {
		t.Fatalf("restart stopped: %v", err)
	}
	if got.Status != StatusRunning || len(cm.started) != startedBefore+1 || len(cm.restarted) != 2 {
		t.Fatalf("restarting a stopped workspace: status %q, started %d more, restarted %d times",
			got.Status, len(cm.started)-startedBefore, len(cm.restarted))
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
