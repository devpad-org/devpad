package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
