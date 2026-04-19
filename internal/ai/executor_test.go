package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/devpad-org/devpad/internal/agent"
	"github.com/devpad-org/devpad/internal/workspace"
)

// mockWorkspaceService implements workspace.Service for testing file operations.
type mockWorkspaceService struct {
	workspace.Service // embed to satisfy interface; only file ops are implemented
	files             map[string][]byte
}

func newMockWorkspaceService() *mockWorkspaceService {
	return &mockWorkspaceService{files: make(map[string][]byte)}
}

func (m *mockWorkspaceService) ReadFile(_ context.Context, _, _ int64, path string) ([]byte, error) {
	data, ok := m.files[path]
	if !ok {
		return nil, fmt.Errorf("file not found: %s", path)
	}
	return data, nil
}

func (m *mockWorkspaceService) WriteFile(_ context.Context, _, _ int64, path string, content []byte) error {
	m.files[path] = content
	return nil
}

func (m *mockWorkspaceService) ListFiles(_ context.Context, _, _ int64, _ string) ([]agent.FileEntry, error) {
	return nil, nil
}

func (m *mockWorkspaceService) DeleteFile(_ context.Context, _, _ int64, _ string) error {
	return nil
}

func (m *mockWorkspaceService) SearchFiles(_ context.Context, _, _ int64, _, _ string, _ int) ([]agent.SearchResult, error) {
	return nil, nil
}

func (m *mockWorkspaceService) RunCommand(_ context.Context, _, _ int64, _ string) (*agent.CommandResult, error) {
	return &agent.CommandResult{}, nil
}

func TestEditFile_Success(t *testing.T) {
	ws := newMockWorkspaceService()
	ws.files["src/main.ts"] = []byte("function hello() {\n  console.log('world')\n}\n")

	executor := NewToolExecutor(ws)
	args := mustJSON(t, map[string]any{
		"path":     "src/main.ts",
		"old_text": "console.log('world')",
		"new_text": "console.log('updated')",
	})

	result, err := executor.ExecuteTool(context.Background(), 1, 1, "edit_file", args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "File edited successfully." {
		t.Fatalf("expected success message, got %q", result)
	}

	expected := "function hello() {\n  console.log('updated')\n}\n"
	if string(ws.files["src/main.ts"]) != expected {
		t.Errorf("file content mismatch:\ngot:  %q\nwant: %q", string(ws.files["src/main.ts"]), expected)
	}
}

func TestEditFile_OldTextNotFound(t *testing.T) {
	ws := newMockWorkspaceService()
	ws.files["file.txt"] = []byte("hello world")

	executor := NewToolExecutor(ws)
	args := mustJSON(t, map[string]any{
		"path":     "file.txt",
		"old_text": "not in file",
		"new_text": "replacement",
	})

	result, err := executor.ExecuteTool(context.Background(), 1, 1, "edit_file", args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "Error: old_text not found in file" {
		t.Fatalf("expected not-found error, got %q", result)
	}
}

func TestEditFile_MultipleOccurrences(t *testing.T) {
	ws := newMockWorkspaceService()
	ws.files["file.txt"] = []byte("aaa bbb aaa")

	executor := NewToolExecutor(ws)
	args := mustJSON(t, map[string]any{
		"path":     "file.txt",
		"old_text": "aaa",
		"new_text": "ccc",
	})

	result, err := executor.ExecuteTool(context.Background(), 1, 1, "edit_file", args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "Error: old_text found 2 times, must appear exactly once" {
		t.Fatalf("expected multiple-match error, got %q", result)
	}
}

func TestEditFile_FileNotFound(t *testing.T) {
	ws := newMockWorkspaceService()
	executor := NewToolExecutor(ws)
	args := mustJSON(t, map[string]any{
		"path":     "nonexistent.txt",
		"old_text": "anything",
		"new_text": "replacement",
	})

	result, err := executor.ExecuteTool(context.Background(), 1, 1, "edit_file", args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "Error reading file: file not found: nonexistent.txt" {
		t.Fatalf("expected file-not-found error, got %q", result)
	}
}

func TestEditFile_MissingRequiredParams(t *testing.T) {
	ws := newMockWorkspaceService()
	executor := NewToolExecutor(ws)

	tests := []struct {
		name string
		args map[string]any
	}{
		{"missing path", map[string]any{"old_text": "foo", "new_text": "bar"}},
		{"missing old_text", map[string]any{"path": "file.txt", "new_text": "bar"}},
		{"both missing", map[string]any{"new_text": "bar"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			args := mustJSON(t, tt.args)
			result, err := executor.ExecuteTool(context.Background(), 1, 1, "edit_file", args)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if result != "Error: path and old_text are required" {
				t.Fatalf("expected required-params error, got %q", result)
			}
		})
	}
}

func TestEditFile_MultilineContent(t *testing.T) {
	ws := newMockWorkspaceService()
	ws.files["app.py"] = []byte("def greet():\n    print(\"hello\")\n    print(\"world\")\n\ndef main():\n    greet()\n")

	executor := NewToolExecutor(ws)
	args := mustJSON(t, map[string]any{
		"path":     "app.py",
		"old_text": "def greet():\n    print(\"hello\")\n    print(\"world\")",
		"new_text": "def greet():\n    print(\"hello, world!\")",
	})

	result, err := executor.ExecuteTool(context.Background(), 1, 1, "edit_file", args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "File edited successfully." {
		t.Fatalf("expected success, got %q", result)
	}

	expected := "def greet():\n    print(\"hello, world!\")\n\ndef main():\n    greet()\n"
	if string(ws.files["app.py"]) != expected {
		t.Errorf("file content mismatch:\ngot:  %q\nwant: %q", string(ws.files["app.py"]), expected)
	}
}

func TestEditFile_EmptyNewText(t *testing.T) {
	ws := newMockWorkspaceService()
	ws.files["file.txt"] = []byte("keep this\nremove this\nkeep this too")

	executor := NewToolExecutor(ws)
	args := mustJSON(t, map[string]any{
		"path":     "file.txt",
		"old_text": "\nremove this",
		"new_text": "",
	})

	result, err := executor.ExecuteTool(context.Background(), 1, 1, "edit_file", args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "File edited successfully." {
		t.Fatalf("expected success, got %q", result)
	}

	expected := "keep this\nkeep this too"
	if string(ws.files["file.txt"]) != expected {
		t.Errorf("got %q, want %q", string(ws.files["file.txt"]), expected)
	}
}

func mustJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("failed to marshal JSON: %v", err)
	}
	return json.RawMessage(data)
}
