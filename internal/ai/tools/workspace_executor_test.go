package tools

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/devpad-org/devpad/internal/agent"
)

type stubWorkspaceOps struct {
	readFileFn   func(ctx context.Context, userID, workspaceID int64, path string) ([]byte, error)
	runCommandFn func(ctx context.Context, userID, workspaceID int64, command string) (*agent.CommandResult, error)
}

type stubChildAgentRunner struct {
	startFn func(ctx context.Context, req ChildAgentRunRequest) (*ChildAgentRun, error)
	waitFn  func(ctx context.Context, userID, runID int64) (*ChildAgentRunResult, error)
}

func (s stubChildAgentRunner) StartChildAgentRun(ctx context.Context, req ChildAgentRunRequest) (*ChildAgentRun, error) {
	if s.startFn != nil {
		return s.startFn(ctx, req)
	}
	return &ChildAgentRun{
		ID:             10,
		ParentRunID:    req.ParentRunID,
		WorkspaceID:    req.WorkspaceID,
		ConversationID: req.ConversationID,
		Model:          req.Model,
		Status:         "queued",
	}, nil
}

func (s stubChildAgentRunner) WaitChildAgentRun(ctx context.Context, userID, runID int64) (*ChildAgentRunResult, error) {
	if s.waitFn != nil {
		return s.waitFn(ctx, userID, runID)
	}
	return &ChildAgentRunResult{RunID: runID, Status: "completed", Summary: "done"}, nil
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

	result := executor.ExecuteTool(context.Background(), toolReq(1, 2, "read_file", []byte(`{`)))
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

	result := executor.ExecuteTool(context.Background(), toolReq(1, 2, "read_file", []byte(`{"path":"main.go"}`)))
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

	result := executor.ExecuteTool(context.Background(), toolReq(1, 2, "run_command", []byte(`{"command":"apt update"}`)))
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

	result := executor.ExecuteTool(context.Background(), toolReq(7, 9, "read_file", []byte(`{"path":"README.md"}`)))
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

func TestWorkspaceExecutor_SpawnSubAgentStartsChildRun(t *testing.T) {
	executor := NewWorkspaceExecutor(&stubWorkspaceOps{})
	executor.SetChildAgentRunner(stubChildAgentRunner{startFn: func(_ context.Context, req ChildAgentRunRequest) (*ChildAgentRun, error) {
		if req.ParentRunID != 101 || req.UserID != 7 || req.WorkspaceID != 9 || req.ConversationID != 33 {
			t.Fatalf("unexpected child request IDs: %+v", req)
		}
		if req.Model != "gpt-5.4" {
			t.Fatalf("expected model fallback, got %q", req.Model)
		}
		if req.Prompt != "review the auth package" {
			t.Fatalf("unexpected prompt: %q", req.Prompt)
		}
		return &ChildAgentRun{ID: 202, ParentRunID: req.ParentRunID, WorkspaceID: req.WorkspaceID, ConversationID: req.ConversationID, Model: req.Model, Status: "queued"}, nil
	}})

	req := toolReq(7, 9, "spawn_sub_agent", []byte(`{"prompt":" review the auth package "}`))
	req.CurrentRunID = 101
	req.ConversationID = 33
	req.Model = "gpt-5.4"
	result := executor.ExecuteTool(context.Background(), req)
	if result.IsError {
		t.Fatalf("expected spawn success, got %+v", result)
	}

	var payload struct {
		RunID       int64 `json:"runId"`
		ParentRunID int64 `json:"parentRunId"`
	}
	if err := json.Unmarshal([]byte(result.Content), &payload); err != nil {
		t.Fatalf("decoding spawn result: %v", err)
	}
	if payload.RunID != 202 || payload.ParentRunID != 101 {
		t.Fatalf("unexpected spawn payload: %+v", payload)
	}
}

func TestWorkspaceExecutor_SpawnSubAgentCanWaitForSummary(t *testing.T) {
	executor := NewWorkspaceExecutor(&stubWorkspaceOps{})
	executor.SetChildAgentRunner(stubChildAgentRunner{
		startFn: func(_ context.Context, req ChildAgentRunRequest) (*ChildAgentRun, error) {
			return &ChildAgentRun{ID: 303, ParentRunID: req.ParentRunID, WorkspaceID: req.WorkspaceID, Model: req.Model, Status: "queued"}, nil
		},
		waitFn: func(_ context.Context, userID, runID int64) (*ChildAgentRunResult, error) {
			if userID != 7 || runID != 303 {
				t.Fatalf("unexpected wait request: user=%d run=%d", userID, runID)
			}
			return &ChildAgentRunResult{RunID: runID, Status: "completed", Summary: "The auth module handles sessions."}, nil
		},
	})

	req := toolReq(7, 9, "spawn_sub_agent", []byte(`{"prompt":"summarize auth","wait_for_result":true}`))
	req.CurrentRunID = 101
	req.Model = "gpt-5.4"
	result := executor.ExecuteTool(context.Background(), req)
	if result.IsError {
		t.Fatalf("expected spawn wait success, got %+v", result)
	}

	var payload struct {
		RunID   int64  `json:"runId"`
		Status  string `json:"status"`
		Summary string `json:"summary"`
	}
	if err := json.Unmarshal([]byte(result.Content), &payload); err != nil {
		t.Fatalf("decoding spawn result: %v", err)
	}
	if payload.RunID != 303 || payload.Status != "completed" || payload.Summary != "The auth module handles sessions." {
		t.Fatalf("unexpected waited payload: %+v", payload)
	}
}

func TestWorkspaceExecutor_SpawnSubAgentRequiresCurrentRun(t *testing.T) {
	executor := NewWorkspaceExecutor(&stubWorkspaceOps{})
	executor.SetChildAgentRunner(stubChildAgentRunner{})

	result := executor.ExecuteTool(context.Background(), toolReq(7, 9, "spawn_sub_agent", []byte(`{"prompt":"child work"}`)))
	if !result.IsError {
		t.Fatalf("expected missing current run to fail, got %+v", result)
	}
	if !strings.Contains(result.Content, "persisted agent run") {
		t.Fatalf("expected current run error, got %q", result.Content)
	}
}

func toolReq(userID, workspaceID int64, toolName string, args []byte) ExecutionRequest {
	return ExecutionRequest{
		UserID:      userID,
		WorkspaceID: workspaceID,
		ToolName:    toolName,
		Arguments:   args,
	}
}
