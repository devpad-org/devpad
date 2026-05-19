package workspace

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
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

func TestFileHandlerUploadFileProxiesMultipartToAgent(t *testing.T) {
	var wantToken string
	svc, ws := setupAgentBackedWorkspace(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/file" {
			t.Fatalf("unexpected agent path %q", r.URL.Path)
		}
		if r.Method != http.MethodPut {
			t.Fatalf("expected PUT, got %s", r.Method)
		}
		if got := r.URL.Query().Get("path"); got != "/workspace/src/app.txt" {
			t.Fatalf("expected uploaded path /workspace/src/app.txt, got %q", got)
		}
		if got := r.Header.Get("X-Devpad-Agent-Token"); got != wantToken {
			t.Fatalf("expected agent token %q, got %q", wantToken, got)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("reading agent request body: %v", err)
		}
		if string(body) != "uploaded content" {
			t.Fatalf("unexpected upload body %q", body)
		}

		w.WriteHeader(http.StatusNoContent)
	})
	wantToken = ws.AgentToken

	body, contentType := multipartUploadBody(t, "/workspace/src", "app.txt", "uploaded content")
	handler := NewHandler(svc, nil)
	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/1/file/upload", body)
	req.Header.Set("Content-Type", contentType)
	req.SetPathValue("id", strconv.FormatInt(ws.ID, 10))
	req = authedRequest(req, testUser(1))
	rec := httptest.NewRecorder()

	handler.HandleUploadFile(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected status 201, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if resp.Path != "/workspace/src/app.txt" {
		t.Fatalf("unexpected uploaded path %q", resp.Path)
	}
}

func TestFileHandlerUploadFileRejectsInvalidFilename(t *testing.T) {
	svc, ws := setupAgentBackedWorkspace(t, func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("agent should not be called for invalid filename")
	})

	body, contentType := multipartUploadBody(t, "/workspace", ".", "bad")
	handler := NewHandler(svc, nil)
	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/1/file/upload", body)
	req.Header.Set("Content-Type", contentType)
	req.SetPathValue("id", strconv.FormatInt(ws.ID, 10))
	req = authedRequest(req, testUser(1))
	rec := httptest.NewRecorder()

	handler.HandleUploadFile(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestFileHandlerUploadFileRejectsEscapingDirectory(t *testing.T) {
	svc, ws := setupAgentBackedWorkspace(t, func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("agent should not be called for escaping directory")
	})

	body, contentType := multipartUploadBody(t, "/workspace/../etc", "passwd", "bad")
	handler := NewHandler(svc, nil)
	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/1/file/upload", body)
	req.Header.Set("Content-Type", contentType)
	req.SetPathValue("id", strconv.FormatInt(ws.ID, 10))
	req = authedRequest(req, testUser(1))
	rec := httptest.NewRecorder()

	handler.HandleUploadFile(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestSanitizeUploadFilenameRejectsPathSeparators(t *testing.T) {
	tests := []string{"../evil.txt", `nested\evil.txt`, "", "."}
	for _, tt := range tests {
		t.Run(tt, func(t *testing.T) {
			if _, err := sanitizeUploadFilename(tt); err == nil {
				t.Fatalf("expected %q to be rejected", tt)
			}
		})
	}
}

func TestNormalizeUploadDirectory(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{name: "workspace root", input: "/workspace", want: "/workspace"},
		{name: "nested absolute directory", input: "/workspace/src/", want: "/workspace/src"},
		{name: "relative directory", input: "src", want: "/workspace/src"},
		{name: "reject escaping directory", input: "/workspace/../etc", wantErr: true},
		{name: "reject sibling prefix", input: "/workspace2", wantErr: true},
		{name: "reject empty directory", input: " ", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := normalizeUploadDirectory(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("normalizing directory: %v", err)
			}
			if got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func multipartUploadBody(t *testing.T, targetPath, filename, content string) (*bytes.Buffer, string) {
	t.Helper()

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	if err := writer.WriteField("path", targetPath); err != nil {
		t.Fatalf("writing path field: %v", err)
	}
	part, err := writer.CreateFormFile("file", filename)
	if err != nil {
		t.Fatalf("creating file field: %v", err)
	}
	if _, err := part.Write([]byte(content)); err != nil {
		t.Fatalf("writing file content: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("closing multipart writer: %v", err)
	}
	return body, writer.FormDataContentType()
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
