package workspace

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/devpad-org/devpad/internal/agent"
	"github.com/devpad-org/devpad/internal/auth"
	"github.com/devpad-org/devpad/internal/encrypt"
)

func setupAgentBackedWorkspace(t *testing.T, agentHandler http.HandlerFunc) (Service, *Workspace) {
	t.Helper()

	db := setupTestDB(t)
	repo := NewRepository(db)

	agentSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/healthz":
			w.WriteHeader(http.StatusOK)
		case "/api/version":
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]string{"version": "test"})
		default:
			agentHandler(w, r)
		}
	}))
	t.Cleanup(agentSrv.Close)

	host, portStr, err := net.SplitHostPort(strings.TrimPrefix(agentSrv.URL, "http://"))
	if err != nil {
		t.Fatalf("splitting test server host: %v", err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatalf("parsing test server port: %v", err)
	}

	userRepo := auth.NewUserRepository(db)
	key, err := encrypt.GenerateKey()
	if err != nil {
		t.Fatalf("generating encryption key: %v", err)
	}
	cipher, err := encrypt.NewCipher(key)
	if err != nil {
		t.Fatalf("creating cipher: %v", err)
	}

	svc := NewService(repo, &mockContainerManager{ipOverride: host}, userRepo, cipher)
	svc.(*service).agentPort = port

	ws, err := svc.Create(context.Background(), 1, "Agent Backed", "")
	if err != nil {
		t.Fatalf("creating workspace: %v", err)
	}
	return svc, ws
}

func TestFileHandlerListFilesDefaultsToWorkspacePath(t *testing.T) {
	var wantToken string
	svc, ws := setupAgentBackedWorkspace(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/files" {
			t.Fatalf("unexpected agent path %q", r.URL.Path)
		}
		if r.Method != http.MethodGet {
			t.Fatalf("expected GET, got %s", r.Method)
		}
		if got := r.URL.Query().Get("path"); got != "/workspace" {
			t.Fatalf("expected default path /workspace, got %q", got)
		}
		if got := r.Header.Get("X-Devpad-Agent-Token"); got != wantToken {
			t.Fatalf("expected agent token %q, got %q", wantToken, got)
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"entries": []agent.FileEntry{
				{Name: "main.go", Path: "/workspace/main.go", Type: "file", Size: 12},
			},
		})
	})
	wantToken = ws.AgentToken

	handler := NewHandler(svc, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/1/files", nil)
	req.SetPathValue("id", strconv.FormatInt(ws.ID, 10))
	req = authedRequest(req, testUser(1))
	rec := httptest.NewRecorder()

	handler.HandleListFiles(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Entries []agent.FileEntry `json:"entries"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if len(resp.Entries) != 1 || resp.Entries[0].Path != "/workspace/main.go" {
		t.Fatalf("unexpected entries: %+v", resp.Entries)
	}
}

func TestFileHandlerListFilesForwardsRecursiveOptions(t *testing.T) {
	svc, ws := setupAgentBackedWorkspace(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/files" {
			t.Fatalf("unexpected agent path %q", r.URL.Path)
		}
		query := r.URL.Query()
		if got := query.Get("path"); got != "src" {
			t.Fatalf("expected path src, got %q", got)
		}
		if got := query.Get("recursive"); got != "true" {
			t.Fatalf("expected recursive=true, got %q", got)
		}
		if got := query.Get("max_depth"); got != "4" {
			t.Fatalf("expected max_depth=4, got %q", got)
		}
		if got := query.Get("max_entries"); got != "50" {
			t.Fatalf("expected max_entries=50, got %q", got)
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(agent.FileList{
			Entries:   []agent.FileEntry{{Name: "main.go", Path: "/workspace/src/main.go", Type: "file"}},
			Truncated: true,
		})
	})

	handler := NewHandler(svc, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/1/files?path=src&recursive=true&max_depth=4&max_entries=50", nil)
	req.SetPathValue("id", strconv.FormatInt(ws.ID, 10))
	req = authedRequest(req, testUser(1))
	rec := httptest.NewRecorder()

	handler.HandleListFiles(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp agent.FileList
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if !resp.Truncated || len(resp.Entries) != 1 {
		t.Fatalf("unexpected response: %+v", resp)
	}
}

func TestGitActionDelegatesAgentRequest(t *testing.T) {
	var wantToken string
	svc, ws := setupAgentBackedWorkspace(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/git/action" {
			t.Fatalf("unexpected agent path %q", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Fatalf("expected POST, got %s", r.Method)
		}
		if got := r.Header.Get("X-Devpad-Agent-Token"); got != wantToken {
			t.Fatalf("expected agent token %q, got %q", wantToken, got)
		}

		var req agent.GitActionRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decoding git action request: %v", err)
		}
		if req.Action != "commit" || req.Message != "ship it" || len(req.Files) != 1 || req.Files[0] != "main.go" {
			t.Fatalf("unexpected git action request: %+v", req)
		}
		if req.UserName != "Dev Pad" || req.UserEmail != "devpad@example.com" {
			t.Fatalf("unexpected git user fields: %+v", req)
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(agent.GitActionResult{Success: true, Output: "committed"})
	})
	wantToken = ws.AgentToken

	result, err := svc.GitAction(context.Background(), 1, ws.ID, GitActionRequest{
		Action:    "commit",
		Files:     []string{"main.go"},
		Message:   "ship it",
		UserName:  "Dev Pad",
		UserEmail: "devpad@example.com",
	})
	if err != nil {
		t.Fatalf("git action failed: %v", err)
	}
	if !result.Success || result.Output != "committed" {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestListProcessesDelegatesAgentRequest(t *testing.T) {
	var wantToken string
	svc, ws := setupAgentBackedWorkspace(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/processes" {
			t.Fatalf("unexpected agent path %q", r.URL.Path)
		}
		if r.Method != http.MethodGet {
			t.Fatalf("expected GET, got %s", r.Method)
		}
		if got := r.Header.Get("X-Devpad-Agent-Token"); got != wantToken {
			t.Fatalf("expected agent token %q, got %q", wantToken, got)
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(agent.ProcessList{
			Processes: []agent.ProcessInfo{
				{PID: 42, PPID: 1, User: "dev", State: "S", Command: "node server.js", MemoryBytes: 1024, Killable: true},
			},
		})
	})
	wantToken = ws.AgentToken

	handler := NewHandler(svc, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/1/processes", nil)
	req.SetPathValue("id", strconv.FormatInt(ws.ID, 10))
	req = authedRequest(req, testUser(1))
	rec := httptest.NewRecorder()

	handler.HandleListProcesses(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp agent.ProcessList
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if len(resp.Processes) != 1 || resp.Processes[0].PID != 42 {
		t.Fatalf("unexpected process list: %+v", resp.Processes)
	}
}

func TestKillProcessDelegatesAgentRequest(t *testing.T) {
	var wantToken string
	svc, ws := setupAgentBackedWorkspace(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/processes/42/kill" {
			t.Fatalf("unexpected agent path %q", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Fatalf("expected POST, got %s", r.Method)
		}
		if got := r.Header.Get("X-Devpad-Agent-Token"); got != wantToken {
			t.Fatalf("expected agent token %q, got %q", wantToken, got)
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "signaled"})
	})
	wantToken = ws.AgentToken

	handler := NewHandler(svc, nil)
	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/1/processes/42/kill", nil)
	req.SetPathValue("id", strconv.FormatInt(ws.ID, 10))
	req.SetPathValue("pid", "42")
	req = authedRequest(req, testUser(1))
	rec := httptest.NewRecorder()

	handler.HandleKillProcess(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}
}
