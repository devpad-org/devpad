package tools

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/devpad-org/devpad/internal/agent"
)

type stubWorkspaceOps struct {
	readFileFn   func(ctx context.Context, userID, workspaceID int64, path string) ([]byte, error)
	runCommandFn func(ctx context.Context, userID, workspaceID int64, command string) (*agent.CommandResult, error)
}

func (s *stubWorkspaceOps) ReadFile(ctx context.Context, userID, workspaceID int64, path string) ([]byte, error) {
	if s.readFileFn != nil {
		return s.readFileFn(ctx, userID, workspaceID, path)
	}
	return nil, nil
}

func (s *stubWorkspaceOps) WriteFile(context.Context, int64, int64, string, []byte) error {
	return nil
}

func (s *stubWorkspaceOps) ListFiles(context.Context, int64, int64, string) ([]agent.FileEntry, error) {
	return nil, nil
}

func (s *stubWorkspaceOps) DeleteFile(context.Context, int64, int64, string) error {
	return nil
}

func (s *stubWorkspaceOps) SearchFiles(context.Context, int64, int64, string, string, int) ([]agent.SearchResult, error) {
	return nil, nil
}

func (s *stubWorkspaceOps) RunCommand(ctx context.Context, userID, workspaceID int64, command string) (*agent.CommandResult, error) {
	if s.runCommandFn != nil {
		return s.runCommandFn(ctx, userID, workspaceID, command)
	}
	return nil, nil
}

func TestWorkspaceExecutor_ExecuteToolReturnsStructuredArgumentErrors(t *testing.T) {
	executor := NewWorkspaceExecutor(&stubWorkspaceOps{})

	result := executor.ExecuteTool(context.Background(), 1, 2, "read_file", []byte(`{`))
	if !result.IsError {
		t.Fatalf("expected structured error result, got %+v", result)
	}
	if result.Name != "read_file" {
		t.Fatalf("expected read_file result name, got %+v", result)
	}
	if !strings.Contains(result.Content, "invalid tool arguments") {
		t.Fatalf("expected invalid argument error, got %q", result.Content)
	}
}

func TestWorkspaceExecutor_ReadFileReturnsStructuredWorkspaceError(t *testing.T) {
	executor := NewWorkspaceExecutor(&stubWorkspaceOps{readFileFn: func(context.Context, int64, int64, string) ([]byte, error) {
		return nil, errors.New("permission denied")
	}})

	result := executor.ExecuteTool(context.Background(), 1, 2, "read_file", []byte(`{"path":"main.go"}`))
	if !result.IsError {
		t.Fatalf("expected structured error result, got %+v", result)
	}
	if !strings.Contains(result.Content, "permission denied") {
		t.Fatalf("expected workspace error in content, got %q", result.Content)
	}
}

func TestWorkspaceExecutor_RunCommandFailureSetsIsError(t *testing.T) {
	executor := NewWorkspaceExecutor(&stubWorkspaceOps{runCommandFn: func(context.Context, int64, int64, string) (*agent.CommandResult, error) {
		return &agent.CommandResult{Output: "apt output", ExitCode: 1, Error: "permission denied"}, nil
	}})

	result := executor.ExecuteTool(context.Background(), 1, 2, "run_command", []byte(`{"command":"apt update"}`))
	if !result.IsError {
		t.Fatalf("expected command failure to set IsError, got %+v", result)
	}
	if result.Name != "run_command" {
		t.Fatalf("expected run_command name, got %+v", result)
	}
	if !strings.Contains(result.Content, "exit code 1") {
		t.Fatalf("expected exit code in content, got %q", result.Content)
	}
	if !strings.Contains(result.Content, "permission denied") {
		t.Fatalf("expected stderr in content, got %q", result.Content)
	}
}

func TestWorkspaceExecutor_ReadFileSuccessReturnsStructuredResult(t *testing.T) {
	executor := NewWorkspaceExecutor(&stubWorkspaceOps{readFileFn: func(_ context.Context, userID, workspaceID int64, path string) ([]byte, error) {
		if userID != 7 || workspaceID != 9 || path != "README.md" {
			t.Fatalf("unexpected workspace call: user=%d workspace=%d path=%q", userID, workspaceID, path)
		}
		return []byte("# Devpad"), nil
	}})

	result := executor.ExecuteTool(context.Background(), 7, 9, "read_file", []byte(`{"path":"README.md"}`))
	if result.IsError {
		t.Fatalf("expected success result, got %+v", result)
	}
	if result.Name != "read_file" {
		t.Fatalf("expected read_file result name, got %+v", result)
	}
	if result.Content != "# Devpad" {
		t.Fatalf("unexpected content: %q", result.Content)
	}
}
