package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/devpad-org/devpad/internal/agent"
	"github.com/devpad-org/devpad/internal/ai/domain"
)

type stubWorkspaceOps struct {
	readFileFn          func(ctx context.Context, userID, workspaceID int64, path string) ([]byte, error)
	listFilesFn         func(ctx context.Context, userID, workspaceID int64, path string, opts agent.ListFilesOptions) (*agent.FileList, error)
	searchFilesFn       func(ctx context.Context, userID, workspaceID int64, pattern, pathFilter string, maxResults int) ([]agent.SearchResult, error)
	runCommandFn        func(ctx context.Context, userID, workspaceID int64, command string) (*agent.CommandResult, error)
	startCommandFn      func(ctx context.Context, userID, workspaceID int64, command, cwd string) (*agent.ManagedCommand, error)
	commandStatusFn     func(ctx context.Context, userID, workspaceID int64, commandID string) (*agent.ManagedCommand, error)
	readCommandOutputFn func(ctx context.Context, userID, workspaceID int64, commandID string, cursor int64, maxBytes, waitMS int) (*agent.CommandOutput, error)
	stopCommandFn       func(ctx context.Context, userID, workspaceID int64, commandID string) (*agent.ManagedCommand, error)
}

type stubChildAgentRunner struct {
	startFn func(ctx context.Context, req ChildAgentRunRequest) (*ChildAgentRun, error)
	waitFn  func(ctx context.Context, userID, parentRunID, runID int64) (*ChildAgentRunResult, error)
}

type stubAgentLister struct {
	listFn func(ctx context.Context, userID, workspaceID int64) ([]domain.Agent, error)
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
		AgentID:        req.AgentID,
		Model:          req.Model,
		Status:         "queued",
	}, nil
}

func (s stubChildAgentRunner) WaitChildAgentRun(ctx context.Context, userID, parentRunID, runID int64) (*ChildAgentRunResult, error) {
	if s.waitFn != nil {
		return s.waitFn(ctx, userID, parentRunID, runID)
	}
	return &ChildAgentRunResult{RunID: runID, Status: "completed", Summary: "done"}, nil
}

func (s stubAgentLister) ListAgents(ctx context.Context, userID, workspaceID int64) ([]domain.Agent, error) {
	if s.listFn != nil {
		return s.listFn(ctx, userID, workspaceID)
	}
	return []domain.Agent{domain.DefaultAgent()}, nil
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

func (s *stubWorkspaceOps) ListFiles(ctx context.Context, userID, workspaceID int64, path string, opts agent.ListFilesOptions) (*agent.FileList, error) {
	if s.listFilesFn != nil {
		return s.listFilesFn(ctx, userID, workspaceID, path, opts)
	}
	return &agent.FileList{}, nil
}

func (s *stubWorkspaceOps) DeleteFile(context.Context, int64, int64, string) error {
	return nil
}

func (s *stubWorkspaceOps) SearchFiles(ctx context.Context, userID, workspaceID int64, pattern, pathFilter string, maxResults int) ([]agent.SearchResult, error) {
	if s.searchFilesFn != nil {
		return s.searchFilesFn(ctx, userID, workspaceID, pattern, pathFilter, maxResults)
	}
	return nil, nil
}

func (s *stubWorkspaceOps) RunCommand(ctx context.Context, userID, workspaceID int64, command string) (*agent.CommandResult, error) {
	if s.runCommandFn != nil {
		return s.runCommandFn(ctx, userID, workspaceID, command)
	}
	return nil, nil
}

func (s *stubWorkspaceOps) StartCommand(ctx context.Context, userID, workspaceID int64, command, cwd string) (*agent.ManagedCommand, error) {
	if s.startCommandFn != nil {
		return s.startCommandFn(ctx, userID, workspaceID, command, cwd)
	}
	return nil, nil
}

func (s *stubWorkspaceOps) CommandStatus(ctx context.Context, userID, workspaceID int64, commandID string) (*agent.ManagedCommand, error) {
	if s.commandStatusFn != nil {
		return s.commandStatusFn(ctx, userID, workspaceID, commandID)
	}
	return nil, nil
}

func (s *stubWorkspaceOps) ReadCommandOutput(ctx context.Context, userID, workspaceID int64, commandID string, cursor int64, maxBytes, waitMS int) (*agent.CommandOutput, error) {
	if s.readCommandOutputFn != nil {
		return s.readCommandOutputFn(ctx, userID, workspaceID, commandID, cursor, maxBytes, waitMS)
	}
	return nil, nil
}

func (s *stubWorkspaceOps) StopCommand(ctx context.Context, userID, workspaceID int64, commandID string) (*agent.ManagedCommand, error) {
	if s.stopCommandFn != nil {
		return s.stopCommandFn(ctx, userID, workspaceID, commandID)
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

func TestWorkspaceExecutor_ListFilesPassesRecursiveOptions(t *testing.T) {
	executor := NewWorkspaceExecutor(&stubWorkspaceOps{listFilesFn: func(_ context.Context, userID, workspaceID int64, path string, opts agent.ListFilesOptions) (*agent.FileList, error) {
		if userID != 7 || workspaceID != 9 || path != "" {
			t.Fatalf("unexpected list files scope: user=%d workspace=%d path=%q", userID, workspaceID, path)
		}
		if !opts.Recursive || opts.MaxDepth != maxListFilesDepth || opts.MaxEntries != maxListFilesEntries {
			t.Fatalf("unexpected list options: %+v", opts)
		}
		return &agent.FileList{
			Entries: []agent.FileEntry{
				{Name: "src", Path: "/workspace/src", Type: "directory"},
				{Name: "main.go", Path: "/workspace/src/main.go", Type: "file", Size: 12},
			},
			Truncated: true,
		}, nil
	}})

	result := executor.ExecuteTool(context.Background(), toolReq(7, 9, "list_files", []byte(`{"path":"","recursive":true,"max_depth":99,"max_entries":5000}`)))
	if result.IsError {
		t.Fatalf("expected success result, got %+v", result)
	}

	var payload struct {
		Entries []struct {
			Name string `json:"name"`
			Path string `json:"path"`
			Type string `json:"type"`
			Size int64  `json:"size"`
		} `json:"entries"`
		Truncated bool `json:"truncated"`
	}
	if err := json.Unmarshal([]byte(result.Content), &payload); err != nil {
		t.Fatalf("decoding list result: %v", err)
	}
	if !payload.Truncated || len(payload.Entries) != 2 {
		t.Fatalf("unexpected list payload: %+v", payload)
	}
	if payload.Entries[1].Path != "src/main.go" {
		t.Fatalf("expected workspace-relative path, got %+v", payload.Entries[1])
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

func TestWorkspaceExecutor_StartCommandReturnsStructuredResult(t *testing.T) {
	executor := NewWorkspaceExecutor(&stubWorkspaceOps{startCommandFn: func(_ context.Context, userID, workspaceID int64, command, cwd string) (*agent.ManagedCommand, error) {
		if userID != 7 || workspaceID != 9 || command != "npm start" || cwd != "frontend" {
			t.Fatalf("unexpected start command call: user=%d workspace=%d command=%q cwd=%q", userID, workspaceID, command, cwd)
		}
		return &agent.ManagedCommand{CommandID: "cmd_123", Command: command, CWD: "/workspace/frontend", Status: "running", Cursor: 0}, nil
	}})

	result := executor.ExecuteTool(context.Background(), toolReq(7, 9, "start_command", []byte(`{"command":"npm start","cwd":"frontend"}`)))
	if result.IsError {
		t.Fatalf("expected success result, got %+v", result)
	}

	var payload agent.ManagedCommand
	if err := json.Unmarshal([]byte(result.Content), &payload); err != nil {
		t.Fatalf("unmarshal result content: %v", err)
	}
	if payload.CommandID != "cmd_123" || payload.Status != "running" {
		t.Fatalf("unexpected payload: %+v", payload)
	}
}

func TestWorkspaceExecutor_ReadCommandOutputUsesCursor(t *testing.T) {
	executor := NewWorkspaceExecutor(&stubWorkspaceOps{readCommandOutputFn: func(_ context.Context, userID, workspaceID int64, commandID string, cursor int64, maxBytes, waitMS int) (*agent.CommandOutput, error) {
		if userID != 7 || workspaceID != 9 || commandID != "cmd_123" || cursor != 42 || maxBytes != 1000 || waitMS != 250 {
			t.Fatalf("unexpected read output call: user=%d workspace=%d id=%q cursor=%d max=%d wait=%d", userID, workspaceID, commandID, cursor, maxBytes, waitMS)
		}
		return &agent.CommandOutput{CommandID: commandID, Status: "running", Output: "listening", Cursor: 42, NextCursor: 51}, nil
	}})

	result := executor.ExecuteTool(context.Background(), toolReq(7, 9, "read_command_output", []byte(`{"command_id":"cmd_123","cursor":42,"max_bytes":1000,"wait_ms":250}`)))
	if result.IsError {
		t.Fatalf("expected success result, got %+v", result)
	}

	var payload agent.CommandOutput
	if err := json.Unmarshal([]byte(result.Content), &payload); err != nil {
		t.Fatalf("unmarshal result content: %v", err)
	}
	if payload.Output != "listening" || payload.NextCursor != 51 {
		t.Fatalf("unexpected payload: %+v", payload)
	}
}

func TestWorkspaceExecutor_StopCommandRequiresCommandID(t *testing.T) {
	executor := NewWorkspaceExecutor(&stubWorkspaceOps{})

	result := executor.ExecuteTool(context.Background(), toolReq(1, 2, "stop_command", []byte(`{}`)))
	if !result.IsError {
		t.Fatalf("expected missing command_id error, got %+v", result)
	}
	if !strings.Contains(result.Content, "command_id is required") {
		t.Fatalf("expected command_id error, got %q", result.Content)
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

func TestWorkspaceExecutor_ReadFileTruncatesLargeResults(t *testing.T) {
	executor := NewWorkspaceExecutor(&stubWorkspaceOps{readFileFn: func(context.Context, int64, int64, string) ([]byte, error) {
		return []byte(strings.Repeat("a", maxToolTextBytes+1024)), nil
	}})

	result := executor.ExecuteTool(context.Background(), toolReq(7, 9, "read_file", []byte(`{"path":"large.txt"}`)))
	if result.IsError {
		t.Fatalf("expected success result, got %+v", result)
	}
	if len(result.Content) <= maxToolTextBytes {
		t.Fatalf("expected truncation notice beyond capped content, got length %d", len(result.Content))
	}
	if !strings.Contains(result.Content, "Output truncated") {
		t.Fatalf("expected truncation notice, got %q", result.Content[len(result.Content)-120:])
	}
	if !strings.HasPrefix(result.Content, strings.Repeat("a", maxToolTextBytes)) {
		t.Fatalf("expected exactly %d content bytes before notice", maxToolTextBytes)
	}
}

func TestWorkspaceExecutor_ReadFileLinesCapsLargeRanges(t *testing.T) {
	var builder strings.Builder
	for i := 1; i <= maxReadFileLines+25; i++ {
		fmt.Fprintf(&builder, "line %d\n", i)
	}
	executor := NewWorkspaceExecutor(&stubWorkspaceOps{readFileFn: func(context.Context, int64, int64, string) ([]byte, error) {
		return []byte(builder.String()), nil
	}})

	result := executor.ExecuteTool(context.Background(), toolReq(7, 9, "read_file_lines", []byte(`{"path":"main.go","start_line":1,"end_line":425}`)))
	if result.IsError {
		t.Fatalf("expected success result, got %+v", result)
	}
	if !strings.Contains(result.Content, "400: line 400") {
		t.Fatalf("expected capped range to include line 400, got %q", result.Content)
	}
	if strings.Contains(result.Content, "401: line 401") {
		t.Fatalf("expected capped range to exclude line 401, got %q", result.Content)
	}
}

func TestWorkspaceExecutor_SearchFilesCapsMaxResults(t *testing.T) {
	executor := NewWorkspaceExecutor(&stubWorkspaceOps{searchFilesFn: func(_ context.Context, _, _ int64, pattern, pathFilter string, maxResults int) ([]agent.SearchResult, error) {
		if maxResults != maxSearchFileResults {
			t.Fatalf("expected max_results to be capped to %d, got %d", maxSearchFileResults, maxResults)
		}
		return []agent.SearchResult{{File: "main.go", Line: 1, Content: "package main"}}, nil
	}})

	result := executor.ExecuteTool(context.Background(), toolReq(7, 9, "search_files", []byte(`{"pattern":"main","max_results":500}`)))
	if result.IsError {
		t.Fatalf("expected success result, got %+v", result)
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
		if req.AgentID != "review-agent" {
			t.Fatalf("expected selected agent, got %q", req.AgentID)
		}
		if req.Prompt != "review the auth package" {
			t.Fatalf("unexpected prompt: %q", req.Prompt)
		}
		return &ChildAgentRun{ID: 202, ParentRunID: req.ParentRunID, WorkspaceID: req.WorkspaceID, ConversationID: req.ConversationID, AgentID: req.AgentID, Model: req.Model, Status: "queued"}, nil
	}})

	req := toolReq(7, 9, "spawn_sub_agent", []byte(`{"prompt":" review the auth package ","agent_id":"review-agent"}`))
	req.CurrentRunID = 101
	req.ConversationID = 33
	req.Model = "gpt-5.4"
	result := executor.ExecuteTool(context.Background(), req)
	if result.IsError {
		t.Fatalf("expected spawn success, got %+v", result)
	}

	var payload struct {
		RunID       int64  `json:"runId"`
		ParentRunID int64  `json:"parentRunId"`
		AgentID     string `json:"agentId"`
	}
	if err := json.Unmarshal([]byte(result.Content), &payload); err != nil {
		t.Fatalf("decoding spawn result: %v", err)
	}
	if payload.RunID != 202 || payload.ParentRunID != 101 {
		t.Fatalf("unexpected spawn payload: %+v", payload)
	}
	if payload.AgentID != "review-agent" {
		t.Fatalf("expected agent ID in payload, got %+v", payload)
	}
}

func TestWorkspaceExecutor_ListAvailableAgentsReturnsScopedCatalog(t *testing.T) {
	executor := NewWorkspaceExecutor(&stubWorkspaceOps{})
	executor.SetAgentLister(stubAgentLister{listFn: func(_ context.Context, userID, workspaceID int64) ([]domain.Agent, error) {
		if userID != 7 || workspaceID != 9 {
			t.Fatalf("unexpected list scope: user=%d workspace=%d", userID, workspaceID)
		}
		defaultAgent := domain.DefaultAgent()
		return []domain.Agent{
			defaultAgent,
			{ID: "12", UserID: userID, WorkspaceID: workspaceID, Name: "Code Review", Purpose: "Review risky code paths"},
			{ID: "15", UserID: userID, Name: "UI Design", Purpose: "Improve visual polish", IsGlobal: true},
		}, nil
	}})

	result := executor.ExecuteTool(context.Background(), toolReq(7, 9, "list_available_agents", []byte(`{}`)))
	if result.IsError {
		t.Fatalf("expected list success, got %+v", result)
	}

	var payload struct {
		Agents []struct {
			ID        string `json:"id"`
			Name      string `json:"name"`
			Purpose   string `json:"purpose"`
			Scope     string `json:"scope"`
			IsDefault bool   `json:"isDefault"`
			IsGlobal  bool   `json:"isGlobal"`
		} `json:"agents"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal([]byte(result.Content), &payload); err != nil {
		t.Fatalf("decoding list result: %v", err)
	}
	if len(payload.Agents) != 3 {
		t.Fatalf("expected 3 agents, got %+v", payload)
	}
	if payload.Agents[0].ID != domain.DefaultAgentID || payload.Agents[0].Scope != "default" || !payload.Agents[0].IsDefault {
		t.Fatalf("unexpected default agent payload: %+v", payload.Agents[0])
	}
	if payload.Agents[1].ID != "12" || payload.Agents[1].Scope != "workspace" {
		t.Fatalf("unexpected workspace agent payload: %+v", payload.Agents[1])
	}
	if payload.Agents[2].ID != "15" || payload.Agents[2].Scope != "global" || !payload.Agents[2].IsGlobal {
		t.Fatalf("unexpected global agent payload: %+v", payload.Agents[2])
	}
	if !strings.Contains(payload.Message, "spawn_sub_agent.agent_id") {
		t.Fatalf("expected assignment hint in message, got %q", payload.Message)
	}
}

func TestWorkspaceExecutor_SpawnSubAgentCanWaitForSummary(t *testing.T) {
	executor := NewWorkspaceExecutor(&stubWorkspaceOps{})
	executor.SetChildAgentRunner(stubChildAgentRunner{
		startFn: func(_ context.Context, req ChildAgentRunRequest) (*ChildAgentRun, error) {
			return &ChildAgentRun{ID: 303, ParentRunID: req.ParentRunID, WorkspaceID: req.WorkspaceID, Model: req.Model, Status: "queued"}, nil
		},
		waitFn: func(_ context.Context, userID, parentRunID, runID int64) (*ChildAgentRunResult, error) {
			if userID != 7 || parentRunID != 101 || runID != 303 {
				t.Fatalf("unexpected wait request: user=%d parent=%d run=%d", userID, parentRunID, runID)
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

func TestWorkspaceExecutor_WaitForSubAgentsReturnsOrderedResults(t *testing.T) {
	executor := NewWorkspaceExecutor(&stubWorkspaceOps{})
	executor.SetChildAgentRunner(stubChildAgentRunner{
		waitFn: func(_ context.Context, userID, parentRunID, runID int64) (*ChildAgentRunResult, error) {
			if userID != 7 || parentRunID != 101 {
				t.Fatalf("unexpected wait request scope: user=%d parent=%d", userID, parentRunID)
			}
			switch runID {
			case 303:
				return &ChildAgentRunResult{RunID: runID, Status: "completed", Summary: "Backend summary"}, nil
			case 202:
				return &ChildAgentRunResult{RunID: runID, Status: "failed", Error: "Frontend failed"}, nil
			default:
				t.Fatalf("unexpected run ID: %d", runID)
				return nil, nil
			}
		},
	})

	req := toolReq(7, 9, "wait_for_sub_agents", []byte(`{"run_ids":[303,202]}`))
	req.CurrentRunID = 101
	result := executor.ExecuteTool(context.Background(), req)
	if result.IsError {
		t.Fatalf("expected wait success, got %+v", result)
	}

	var payload struct {
		Results []struct {
			RunID   int64  `json:"runId"`
			Status  string `json:"status"`
			Summary string `json:"summary"`
			Error   string `json:"error"`
		} `json:"results"`
		Completed int `json:"completed"`
		Failed    int `json:"failed"`
		TimedOut  int `json:"timedOut"`
	}
	if err := json.Unmarshal([]byte(result.Content), &payload); err != nil {
		t.Fatalf("decoding wait result: %v", err)
	}
	if len(payload.Results) != 2 {
		t.Fatalf("expected two results, got %+v", payload)
	}
	if payload.Results[0].RunID != 303 || payload.Results[0].Summary != "Backend summary" {
		t.Fatalf("unexpected first result: %+v", payload.Results[0])
	}
	if payload.Results[1].RunID != 202 || payload.Results[1].Error != "Frontend failed" {
		t.Fatalf("unexpected second result: %+v", payload.Results[1])
	}
	if payload.Completed != 1 || payload.Failed != 1 || payload.TimedOut != 0 {
		t.Fatalf("unexpected wait counts: %+v", payload)
	}
}

func TestWorkspaceExecutor_WaitForSubAgentsReportsTimeouts(t *testing.T) {
	executor := NewWorkspaceExecutor(&stubWorkspaceOps{})
	executor.SetChildAgentRunner(stubChildAgentRunner{
		waitFn: func(_ context.Context, _ int64, _ int64, runID int64) (*ChildAgentRunResult, error) {
			if runID == 404 {
				return nil, context.DeadlineExceeded
			}
			return &ChildAgentRunResult{RunID: runID, Status: "completed", Summary: "done"}, nil
		},
	})

	req := toolReq(7, 9, "wait_for_sub_agents", []byte(`{"run_ids":[303,404]}`))
	req.CurrentRunID = 101
	result := executor.ExecuteTool(context.Background(), req)
	if result.IsError {
		t.Fatalf("expected timeout payload, got %+v", result)
	}

	var payload struct {
		Results []struct {
			RunID    int64 `json:"runId"`
			TimedOut bool  `json:"timedOut"`
		} `json:"results"`
		Completed int `json:"completed"`
		TimedOut  int `json:"timedOut"`
	}
	if err := json.Unmarshal([]byte(result.Content), &payload); err != nil {
		t.Fatalf("decoding wait result: %v", err)
	}
	if payload.Completed != 1 || payload.TimedOut != 1 {
		t.Fatalf("unexpected timeout counts: %+v", payload)
	}
	if !payload.Results[1].TimedOut {
		t.Fatalf("expected second result to be timed out: %+v", payload.Results)
	}
}

func TestWorkspaceExecutor_WaitForSubAgentsRequiresCurrentRun(t *testing.T) {
	executor := NewWorkspaceExecutor(&stubWorkspaceOps{})
	executor.SetChildAgentRunner(stubChildAgentRunner{})

	result := executor.ExecuteTool(context.Background(), toolReq(7, 9, "wait_for_sub_agents", []byte(`{"run_ids":[202]}`)))
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
