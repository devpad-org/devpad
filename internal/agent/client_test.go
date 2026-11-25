package agent

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestWaitReady_ImmediateSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := &Client{
		baseURL: srv.URL,
		http:    srv.Client(),
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := c.WaitReady(ctx); err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
}

func TestUploadFileStreamsContentWithAuth(t *testing.T) {
	const token = "agent-token"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/file" {
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
		if r.Method != http.MethodPut {
			t.Fatalf("expected PUT, got %s", r.Method)
		}
		if got := r.URL.Query().Get("path"); got != "/workspace/docs/readme.md" {
			t.Fatalf("path query = %q, want /workspace/docs/readme.md", got)
		}
		if got := r.Header.Get("X-Devpad-Agent-Token"); got != token {
			t.Fatalf("auth token = %q, want %q", got, token)
		}
		if got := r.Header.Get("Content-Type"); got != "application/octet-stream" {
			t.Fatalf("content type = %q, want application/octet-stream", got)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("reading body: %v", err)
		}
		if string(body) != "hello upload" {
			t.Fatalf("body = %q", body)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	client := &Client{
		baseURL: srv.URL,
		token:   token,
		http:    srv.Client(),
	}

	if err := client.UploadFile(context.Background(), "/workspace/docs/readme.md", strings.NewReader("hello upload")); err != nil {
		t.Fatalf("uploading file: %v", err)
	}
}

func TestWaitReady_BecomesReadyAfterRetries(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		if n < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := &Client{
		baseURL: srv.URL,
		http:    srv.Client(),
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := c.WaitReady(ctx); err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	if got := calls.Load(); got < 3 {
		t.Fatalf("expected at least 3 calls, got %d", got)
	}
}

func TestWaitReady_ContextCancelled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	c := &Client{
		baseURL: srv.URL,
		http:    srv.Client(),
	}

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	err := c.WaitReady(ctx)
	if err == nil {
		t.Fatal("expected error when context expires, got nil")
	}
}

func TestListFilesPassesOptionsAndDecodesMetadata(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/files" {
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
		query := r.URL.Query()
		if got := query.Get("path"); got != "/workspace" {
			t.Fatalf("path query = %q, want /workspace", got)
		}
		if got := query.Get("recursive"); got != "true" {
			t.Fatalf("recursive query = %q, want true", got)
		}
		if got := query.Get("max_depth"); got != "4" {
			t.Fatalf("max_depth query = %q, want 4", got)
		}
		if got := query.Get("max_entries"); got != "50" {
			t.Fatalf("max_entries query = %q, want 50", got)
		}

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(FileList{
			Entries:   []FileEntry{{Name: "main.go", Path: "/workspace/main.go", Type: "file"}},
			Truncated: true,
		}); err != nil {
			t.Fatalf("encoding response: %v", err)
		}
	}))
	defer srv.Close()

	client := &Client{
		baseURL: srv.URL,
		http:    srv.Client(),
	}

	result, err := client.ListFiles(context.Background(), "/workspace", ListFilesOptions{
		Recursive:  true,
		MaxDepth:   4,
		MaxEntries: 50,
	})
	if err != nil {
		t.Fatalf("listing files: %v", err)
	}
	if !result.Truncated || len(result.Entries) != 1 || result.Entries[0].Name != "main.go" {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestReadCommandOutputRefusesOversizedResponses(t *testing.T) {
	output := "ok"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(CommandOutput{Status: "running", Output: output})
	}))
	defer srv.Close()
	c := &Client{baseURL: srv.URL, http: srv.Client()}

	got, err := c.ReadCommandOutput(context.Background(), "cmd", CommandOutputOptions{MaxBytes: 64 * 1024})
	if err != nil || got.Output != "ok" {
		t.Fatalf("read: %+v %v", got, err)
	}
	// A workspace that ignores max_bytes.
	output = strings.Repeat("x", maxCommandOutputResponse+1)
	if _, err := c.ReadCommandOutput(context.Background(), "cmd", CommandOutputOptions{MaxBytes: 64 * 1024}); err == nil {
		t.Fatal("expected an oversized response refused")
	}
}

func TestWaitCommandAsksTheAgentToWait(t *testing.T) {
	var asked []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked = append(asked, r.Method+" "+r.URL.Path+"?"+r.URL.RawQuery)
		json.NewEncoder(w).Encode(ManagedCommand{CommandID: "cmd_1", Status: "exited"})
	}))
	defer srv.Close()
	c := &Client{baseURL: srv.URL, http: srv.Client()}

	if got, err := c.WaitCommand(context.Background(), "cmd_1", 90*time.Second); err != nil || got.Status != "exited" {
		t.Fatalf("wait: %+v %v", got, err)
	}
	if _, err := c.CommandStatus(context.Background(), "cmd_1"); err != nil {
		t.Fatalf("status: %v", err)
	}
	want := []string{"GET /api/commands/cmd_1?wait_ms=90000", "GET /api/commands/cmd_1?"}
	if strings.Join(asked, "\n") != strings.Join(want, "\n") {
		t.Fatalf("asked %q, want %q", asked, want)
	}
}

func TestRepoReadRefusesAnAnswerToAnotherRequest(t *testing.T) {
	var answer string
	var asked atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked.Add(1)
		var in RepoReadRequest
		if r.Method != http.MethodPost || r.URL.Path != "/api/repo/read" || json.NewDecoder(r.Body).Decode(&in) != nil || in.Rev != "main" {
			http.Error(w, "unexpected request", http.StatusBadRequest)
			return
		}
		io.WriteString(w, answer)
	}))
	defer srv.Close()
	c := &Client{baseURL: srv.URL, http: srv.Client()}
	in := RepoReadRequest{Rev: "main", Dirs: []string{"skills"}, Files: []string{"AGENTS.md"}}

	answer = `{"commit":"abc","dirs":[{"path":"skills","found":true,"entries":[{"name":"deploy","type":"tree"}]}],"files":[{"path":"AGENTS.md","found":true,"content":"aGk="}]}`
	got, err := c.RepoRead(context.Background(), in)
	if err != nil || got.Commit != "abc" || got.Dirs[0].Entries[0].Name != "deploy" || string(got.Files[0].Content) != "hi" {
		t.Fatalf("unexpected answer %+v, %v", got, err)
	}

	// More paths than the agent reads at once are refused before asking,
	// not left to a 400 that could be read as nothing there.
	before := asked.Load()
	tooMany := RepoReadRequest{Rev: "main", Files: make([]string, MaxRepoReadFiles+1)}
	if _, err := c.RepoRead(context.Background(), tooMany); err == nil || asked.Load() != before {
		t.Fatalf("expected too many paths refused without asking, got %v", err)
	}

	// Answers are matched to what was asked by position, so one that does
	// not line up is refused.
	for _, bad := range []string{
		`{"commit":"abc","dirs":[],"files":[{"path":"AGENTS.md","found":true}]}`,
		`{"commit":"abc","dirs":[{"path":"other"}],"files":[{"path":"AGENTS.md"}]}`,
		`{"commit":"abc","dirs":[{"path":"skills"}],"files":[{"path":"secret"}]}`,
	} {
		answer = bad
		if _, err := c.RepoRead(context.Background(), in); err == nil {
			t.Errorf("expected %s refused", bad)
		}
	}
}
