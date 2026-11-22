package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/png"
	"slices"
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
	readCommandOutputFn func(ctx context.Context, userID, workspaceID int64, commandID string, opts agent.CommandOutputOptions) (*agent.CommandOutput, error)
	stopCommandFn       func(ctx context.Context, userID, workspaceID int64, commandID string) (*agent.ManagedCommand, error)
}

type stubChildAgentRunner struct {
	startFn func(ctx context.Context, req ChildAgentRunRequest) (*ChildAgentRun, error)
	waitFn  func(ctx context.Context, userID, parentRunID, runID int64) (*ChildAgentRunResult, error)
}

type stubAgentLister struct {
	listFn func(ctx context.Context, userID, workspaceID int64) ([]domain.Agent, error)
}

type stubFileSummarizer struct {
	summarizeFn func(ctx context.Context, req FileSummaryRequest) (string, error)
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

func (s stubFileSummarizer) SummarizeFile(ctx context.Context, req FileSummaryRequest) (string, error) {
	if s.summarizeFn != nil {
		return s.summarizeFn(ctx, req)
	}
	return "summary", nil
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

func (s *stubWorkspaceOps) ReadCommandOutput(ctx context.Context, userID, workspaceID int64, commandID string, opts agent.CommandOutputOptions) (*agent.CommandOutput, error) {
	if s.readCommandOutputFn != nil {
		return s.readCommandOutputFn(ctx, userID, workspaceID, commandID, opts)
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
	executor := NewWorkspaceExecutor(&stubWorkspaceOps{readCommandOutputFn: func(_ context.Context, userID, workspaceID int64, commandID string, opts agent.CommandOutputOptions) (*agent.CommandOutput, error) {
		if userID != 7 || workspaceID != 9 || commandID != "cmd_123" || opts != (agent.CommandOutputOptions{Cursor: 42, MaxBytes: 1000, WaitMS: 250}) {
			t.Fatalf("unexpected read output call: user=%d workspace=%d id=%q opts=%+v", userID, workspaceID, commandID, opts)
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

func TestWorkspaceExecutor_SummarizeFileRequiresPath(t *testing.T) {
	executor := NewWorkspaceExecutor(&stubWorkspaceOps{})
	executor.SetFileSummarizer(stubFileSummarizer{})

	result := executor.ExecuteTool(context.Background(), toolReq(1, 2, "summarize_file", []byte(`{}`)))
	if !result.IsError {
		t.Fatalf("expected missing path error, got %+v", result)
	}
	if !strings.Contains(result.Content, "path is required") {
		t.Fatalf("expected path error, got %q", result.Content)
	}
}

func TestWorkspaceExecutor_SummarizeFileRequiresConfiguredSummarizer(t *testing.T) {
	executor := NewWorkspaceExecutor(&stubWorkspaceOps{})

	result := executor.ExecuteTool(context.Background(), toolReq(1, 2, "summarize_file", []byte(`{"path":"main.go"}`)))
	if !result.IsError {
		t.Fatalf("expected missing summarizer error, got %+v", result)
	}
	if !strings.Contains(result.Content, "not configured") {
		t.Fatalf("expected configuration error, got %q", result.Content)
	}
}

func TestWorkspaceExecutor_SummarizeFileReadsThroughWorkspaceAndReturnsSummary(t *testing.T) {
	executor := NewWorkspaceExecutor(&stubWorkspaceOps{readFileFn: func(_ context.Context, userID, workspaceID int64, path string) ([]byte, error) {
		if userID != 7 || workspaceID != 9 || path != "internal/app.go" {
			t.Fatalf("unexpected read scope: user=%d workspace=%d path=%q", userID, workspaceID, path)
		}
		return []byte("package internal\n\nfunc Start() {}"), nil
	}})
	executor.SetFileSummarizer(stubFileSummarizer{summarizeFn: func(_ context.Context, req FileSummaryRequest) (string, error) {
		if req.UserID != 7 || req.WorkspaceID != 9 || req.Path != "internal/app.go" {
			t.Fatalf("unexpected summary scope: %+v", req)
		}
		if req.Focus != "public API" {
			t.Fatalf("expected focus to pass through, got %q", req.Focus)
		}
		if req.Content != "1: package internal\n2: \n3: func Start() {}" {
			t.Fatalf("unexpected summary content: %q", req.Content)
		}
		return "Defines the app startup entrypoint.", nil
	}})

	result := executor.ExecuteTool(context.Background(), toolReq(7, 9, "summarize_file", []byte(`{"path":" internal/app.go ","focus":" public API "}`)))
	if result.IsError {
		t.Fatalf("expected summary success, got %+v", result)
	}
	if result.Name != "summarize_file" || result.Content != "Defines the app startup entrypoint." {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestWorkspaceExecutor_SummarizeFileReportsReadErrors(t *testing.T) {
	executor := NewWorkspaceExecutor(&stubWorkspaceOps{readFileFn: func(context.Context, int64, int64, string) ([]byte, error) {
		return nil, errors.New("file unavailable")
	}})
	executor.SetFileSummarizer(stubFileSummarizer{})

	result := executor.ExecuteTool(context.Background(), toolReq(1, 2, "summarize_file", []byte(`{"path":"main.go"}`)))
	if !result.IsError {
		t.Fatalf("expected read error, got %+v", result)
	}
	if !strings.Contains(result.Content, "file unavailable") {
		t.Fatalf("expected read error content, got %q", result.Content)
	}
}

func TestWorkspaceExecutor_SummarizeFileReportsSummarizerErrors(t *testing.T) {
	executor := NewWorkspaceExecutor(&stubWorkspaceOps{readFileFn: func(context.Context, int64, int64, string) ([]byte, error) {
		return []byte("package main"), nil
	}})
	executor.SetFileSummarizer(stubFileSummarizer{summarizeFn: func(context.Context, FileSummaryRequest) (string, error) {
		return "", errors.New("provider missing api key")
	}})

	result := executor.ExecuteTool(context.Background(), toolReq(1, 2, "summarize_file", []byte(`{"path":"main.go"}`)))
	if !result.IsError {
		t.Fatalf("expected summarizer error, got %+v", result)
	}
	if !strings.Contains(result.Content, "provider missing api key") {
		t.Fatalf("expected summarizer error content, got %q", result.Content)
	}
}

func TestWorkspaceExecutor_SummarizeFileCapsModelInput(t *testing.T) {
	executor := NewWorkspaceExecutor(&stubWorkspaceOps{readFileFn: func(context.Context, int64, int64, string) ([]byte, error) {
		return []byte(strings.Repeat("a", maxSummaryInputBytes+1024)), nil
	}})
	executor.SetFileSummarizer(stubFileSummarizer{summarizeFn: func(_ context.Context, req FileSummaryRequest) (string, error) {
		if len(req.Content) <= maxSummaryInputBytes {
			t.Fatalf("expected truncation notice beyond capped content, got length %d", len(req.Content))
		}
		if !strings.Contains(req.Content, "File content truncated") {
			t.Fatalf("expected truncation notice, got suffix %q", req.Content[len(req.Content)-120:])
		}
		return "Large generated file.", nil
	}})

	result := executor.ExecuteTool(context.Background(), toolReq(1, 2, "summarize_file", []byte(`{"path":"large.go"}`)))
	if result.IsError {
		t.Fatalf("expected success, got %+v", result)
	}
}

func TestWorkspaceExecutor_SummarizeFileCapsSummaryOutput(t *testing.T) {
	executor := NewWorkspaceExecutor(&stubWorkspaceOps{readFileFn: func(context.Context, int64, int64, string) ([]byte, error) {
		return []byte("package main"), nil
	}})
	executor.SetFileSummarizer(stubFileSummarizer{summarizeFn: func(context.Context, FileSummaryRequest) (string, error) {
		return strings.Repeat("s", maxSummaryOutputBytes+1024), nil
	}})

	result := executor.ExecuteTool(context.Background(), toolReq(1, 2, "summarize_file", []byte(`{"path":"main.go"}`)))
	if result.IsError {
		t.Fatalf("expected success, got %+v", result)
	}
	if !strings.Contains(result.Content, "Output truncated") {
		t.Fatalf("expected summary truncation notice, got suffix %q", result.Content[len(result.Content)-120:])
	}
	if !strings.HasPrefix(result.Content, strings.Repeat("s", maxSummaryOutputBytes)) {
		t.Fatalf("expected exactly %d summary bytes before notice", maxSummaryOutputBytes)
	}
}

func TestLineNumberedContent(t *testing.T) {
	got := lineNumberedContent("first\n\nthird")
	want := "1: first\n2: \n3: third"
	if got != want {
		t.Fatalf("lineNumberedContent() = %q, want %q", got, want)
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

func TestAgentToolsIncludesSummarizeFile(t *testing.T) {
	definitions := AgentTools()
	for _, definition := range definitions {
		if definition.Function.Name != "summarize_file" {
			continue
		}
		var schema struct {
			Required   []string       `json:"required"`
			Properties map[string]any `json:"properties"`
		}
		if err := json.Unmarshal(definition.Function.Parameters, &schema); err != nil {
			t.Fatalf("decoding summarize_file schema: %v", err)
		}
		if _, ok := schema.Properties["path"]; !ok {
			t.Fatalf("expected path property in schema: %+v", schema.Properties)
		}
		for _, required := range schema.Required {
			if required == "path" {
				return
			}
		}
		t.Fatalf("expected path to be required, got %+v", schema.Required)
	}
	t.Fatal("summarize_file tool definition not found")
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

type stubAgentDefaults map[string]domain.AgentDefaults

func (s stubAgentDefaults) DefaultsFor(_ context.Context, _ int64, agentID, _ string) (*domain.AgentDefaults, error) {
	if d, ok := s[agentID]; ok {
		return &d, nil
	}
	return nil, nil
}

// stubModels is a model catalog by model ID.
type stubModels map[string]domain.ModelInfo

func (m stubModels) FindModel(id string) (domain.Model, error) {
	info, ok := m[id]
	if !ok {
		return domain.Model{}, domain.ErrModelNotFound
	}
	return info.Model, nil
}

func (m stubModels) ListModels(context.Context, int64) ([]domain.ModelInfo, error) {
	ids := make([]string, 0, len(m))
	for id := range m {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	out := make([]domain.ModelInfo, 0, len(ids))
	for _, id := range ids {
		out = append(out, m[id])
	}
	return out, nil
}

// modelsByProvider makes a catalog of models the user has keys for, from
// model IDs to their providers.
func modelsByProvider(providers map[string]string) stubModels {
	m := stubModels{}
	for id, provider := range providers {
		m[id] = domain.ModelInfo{Model: domain.Model{ID: id, Name: id, ProviderID: provider}, Configured: true}
	}
	return m
}

// A child without a model runs the user's default for its agent with that
// effort, when it is from the parent's provider. Only a child on the parent's
// model takes the parent's thinking.
func TestWorkspaceExecutor_SpawnSubAgentUsesTheAgentsDefaults(t *testing.T) {
	var got []ChildAgentRunRequest
	executor := NewWorkspaceExecutor(&stubWorkspaceOps{})
	executor.SetAgentDefaults(stubAgentDefaults{
		domain.ReviewAgentID:  {AgentID: domain.ReviewAgentID, Model: "user-review", Effort: "low"},
		domain.DefaultAgentID: {AgentID: domain.DefaultAgentID, Model: "elsewhere", Effort: "low"},
	})
	executor.SetModels(modelsByProvider(map[string]string{"parent-model": "a", "user-review": "a", "picked": "a", "elsewhere": "b"}))
	executor.SetChildAgentRunner(stubChildAgentRunner{startFn: func(_ context.Context, req ChildAgentRunRequest) (*ChildAgentRun, error) {
		got = append(got, req)
		return &ChildAgentRun{ID: int64(len(got)), AgentID: req.AgentID, Model: req.Model, Status: "queued"}, nil
	}})

	for _, args := range []string{
		`{"prompt":"review","agent_id":"review"}`,
		`{"prompt":"review","agent_id":"review","model":"picked"}`,
		`{"prompt":"review","agent_id":"review","model":"user-review"}`,
		`{"prompt":"code"}`,
		`{"prompt":"code","model":"parent-model"}`,
	} {
		req := toolReq(7, 9, "spawn_sub_agent", []byte(args))
		req.CurrentRunID = 101
		req.Model = "parent-model"
		req.Thinking = &domain.ThinkingConfig{Effort: "max"}
		if result := executor.ExecuteTool(context.Background(), req); result.IsError {
			t.Fatalf("%s: expected spawn success, got %+v", args, result)
		}
	}

	if got[0].Model != "user-review" || got[0].Thinking == nil || got[0].Thinking.Effort != "low" {
		t.Errorf("expected the review agent's defaults, got %+v %+v", got[0], got[0].Thinking)
	}
	if got[1].Model != "picked" || got[1].Thinking != nil {
		t.Errorf("expected the picked model without the parent's thinking, got %+v %+v", got[1], got[1].Thinking)
	}
	// Naming the default's model still takes the default's effort.
	if got[2].Model != "user-review" || got[2].Thinking == nil || got[2].Thinking.Effort != "low" {
		t.Errorf("expected the review agent's default effort on its named model, got %+v %+v", got[2], got[2].Thinking)
	}
	// The coding agent's default is another provider's model, so the child
	// runs on the parent's.
	for _, child := range got[3:] {
		if child.Model != "parent-model" || child.Thinking == nil || child.Thinking.Effort != "max" {
			t.Errorf("expected the parent's model and thinking, got %+v %+v", child, child.Thinking)
		}
	}
}

// An effort the agent picks must be one its child's model supports.
func TestWorkspaceExecutor_SpawnSubAgentTakesAnEffortTheModelSupports(t *testing.T) {
	var got []ChildAgentRunRequest
	executor := NewWorkspaceExecutor(&stubWorkspaceOps{})
	models := modelsByProvider(map[string]string{"parent-model": "a", "small": "a"})
	models["small"] = domain.ModelInfo{Model: domain.Model{ID: "small", ProviderID: "a", Thinking: domain.ThinkingCapability{Supported: true, SupportedEfforts: []string{"low", "high"}}}, Configured: true}
	executor.SetModels(models)
	executor.SetChildAgentRunner(stubChildAgentRunner{startFn: func(_ context.Context, req ChildAgentRunRequest) (*ChildAgentRun, error) {
		got = append(got, req)
		return &ChildAgentRun{ID: int64(len(got)), Model: req.Model, Status: "queued"}, nil
	}})
	spawn := func(args string) domain.ToolResultPart {
		req := toolReq(7, 9, "spawn_sub_agent", []byte(args))
		req.CurrentRunID = 101
		req.Model = "parent-model"
		req.Thinking = &domain.ThinkingConfig{Effort: "max"}
		return executor.ExecuteTool(context.Background(), req)
	}

	if result := spawn(`{"prompt":"find it","model":"small","effort":"low"}`); result.IsError {
		t.Fatalf("expected a supported effort to start the child, got %+v", result)
	}
	if len(got) != 1 || got[0].Model != "small" || got[0].Thinking == nil || got[0].Thinking.Effort != "low" {
		t.Fatalf("expected the child on small at low effort, got %+v", got)
	}
	for _, args := range []string{
		`{"prompt":"find it","model":"small","effort":"max"}`,
		`{"prompt":"find it","effort":"low"}`,
	} {
		result := spawn(args)
		if !result.IsError || !strings.Contains(result.Content, "effort") {
			t.Fatalf("%s: expected the effort to be refused, got %+v", args, result)
		}
	}
	if len(got) != 1 {
		t.Fatalf("expected no child for a refused effort, got %d", len(got))
	}
}

// The listing gives each agent the model its child runs on by default, and
// the models a child can use: the current model's provider's, which the
// user has a key for.
func TestWorkspaceExecutor_ListAvailableAgentsListsTheModelsAChildCanUse(t *testing.T) {
	executor := NewWorkspaceExecutor(&stubWorkspaceOps{})
	executor.SetAgentLister(stubAgentLister{listFn: func(context.Context, int64, int64) ([]domain.Agent, error) {
		return []domain.Agent{domain.DefaultAgent(), {ID: domain.ReviewAgentID, Name: "Code Review Agent", IsBuiltIn: true}}, nil
	}})
	executor.SetAgentDefaults(stubAgentDefaults{domain.ReviewAgentID: {AgentID: domain.ReviewAgentID, Model: "small", Effort: "low"}})
	executor.SetModels(stubModels{
		"parent-model": {Model: domain.Model{ID: "parent-model", Name: "Parent", ProviderID: "a", Vision: true}, Configured: true},
		"small": {Model: domain.Model{
			ID: "small", Name: "Small", ProviderID: "a",
			Thinking: domain.ThinkingCapability{Supported: true, SupportedEfforts: []string{"low", "high"}, DefaultEffort: "high"},
			Pricing:  &domain.Pricing{TokenPrices: domain.TokenPrices{Input: 1, Output: 5}},
		}, Configured: true},
		"elsewhere": {Model: domain.Model{ID: "elsewhere", ProviderID: "b"}, Configured: true},
	})

	req := toolReq(7, 9, "list_available_agents", []byte(`{}`))
	req.Model = "parent-model"
	req.Thinking = &domain.ThinkingConfig{Effort: "max"}
	result := executor.ExecuteTool(context.Background(), req)
	if result.IsError {
		t.Fatalf("expected list success, got %+v", result)
	}

	var payload struct {
		Agents []struct {
			ID     string `json:"id"`
			Model  string `json:"model"`
			Effort string `json:"effort"`
		} `json:"agents"`
		CurrentModel string `json:"currentModel"`
		Models       []struct {
			ID            string   `json:"id"`
			Current       bool     `json:"current"`
			Vision        bool     `json:"vision"`
			Efforts       []string `json:"efforts"`
			DefaultEffort string   `json:"defaultEffort"`
			InputPrice    *float64 `json:"inputUsdPerMillionTokens"`
			OutputPrice   *float64 `json:"outputUsdPerMillionTokens"`
		} `json:"models"`
	}
	if err := json.Unmarshal([]byte(result.Content), &payload); err != nil {
		t.Fatalf("decoding list result: %v", err)
	}
	if payload.CurrentModel != "parent-model" {
		t.Errorf("expected the current model, got %q", payload.CurrentModel)
	}
	if len(payload.Agents) != 2 || payload.Agents[0].Model != "parent-model" || payload.Agents[0].Effort != "max" ||
		payload.Agents[1].Model != "small" || payload.Agents[1].Effort != "low" {
		t.Errorf("expected each agent's default model and effort, got %+v", payload.Agents)
	}
	if len(payload.Models) != 2 {
		t.Fatalf("expected only the current provider's models, got %+v", payload.Models)
	}
	parent, small := payload.Models[0], payload.Models[1]
	if parent.ID != "parent-model" || !parent.Current || !parent.Vision || parent.InputPrice != nil {
		t.Errorf("unexpected current model entry: %+v", parent)
	}
	if small.ID != "small" || small.Current || small.DefaultEffort != "high" || !slices.Equal(small.Efforts, []string{"low", "high"}) ||
		small.InputPrice == nil || *small.InputPrice != 1 || small.OutputPrice == nil || *small.OutputPrice != 5 {
		t.Errorf("unexpected small model entry: %+v", small)
	}
}

// failingAgentDefaults fails to look up the defaults of the agents it names.
type failingAgentDefaults map[string]bool

func (f failingAgentDefaults) DefaultsFor(_ context.Context, _ int64, agentID, _ string) (*domain.AgentDefaults, error) {
	if f[agentID] {
		return nil, errors.New("defaults unavailable")
	}
	return nil, nil
}

// An agent whose defaults fail to load is still listed, with why its model is
// missing, and the other agents and the models are listed as usual.
func TestWorkspaceExecutor_ListAvailableAgentsListsAgentsWhoseDefaultsFail(t *testing.T) {
	executor := NewWorkspaceExecutor(&stubWorkspaceOps{})
	executor.SetAgentLister(stubAgentLister{listFn: func(context.Context, int64, int64) ([]domain.Agent, error) {
		return []domain.Agent{domain.DefaultAgent(), {ID: domain.ReviewAgentID, Name: "Code Review Agent", IsBuiltIn: true}}, nil
	}})
	executor.SetAgentDefaults(failingAgentDefaults{domain.ReviewAgentID: true})
	executor.SetModels(modelsByProvider(map[string]string{"parent-model": "a"}))

	req := toolReq(7, 9, "list_available_agents", []byte(`{}`))
	req.Model = "parent-model"
	result := executor.ExecuteTool(context.Background(), req)
	if result.IsError {
		t.Fatalf("expected list success, got %+v", result)
	}
	var payload struct {
		Agents []struct {
			ID         string `json:"id"`
			Model      string `json:"model"`
			ModelError string `json:"modelError"`
		} `json:"agents"`
		Models []struct {
			ID string `json:"id"`
		} `json:"models"`
	}
	if err := json.Unmarshal([]byte(result.Content), &payload); err != nil {
		t.Fatalf("decoding list result: %v", err)
	}
	if len(payload.Agents) != 2 || payload.Agents[0].Model != "parent-model" || payload.Agents[0].ModelError != "" {
		t.Fatalf("expected the coding agent on the parent's model, got %+v", payload.Agents)
	}
	if review := payload.Agents[1]; review.ID != domain.ReviewAgentID || review.Model != "" || !strings.Contains(review.ModelError, "defaults unavailable") {
		t.Errorf("expected the review agent without a model and with the error, got %+v", review)
	}
	if len(payload.Models) != 1 || payload.Models[0].ID != "parent-model" {
		t.Errorf("expected the models to be listed, got %+v", payload.Models)
	}
}

// A provider without the user's key offers no models, even the current
// model's provider's other models.
func TestWorkspaceExecutor_ListAvailableAgentsLeavesOutModelsWithoutAKey(t *testing.T) {
	executor := NewWorkspaceExecutor(&stubWorkspaceOps{})
	executor.SetAgentLister(stubAgentLister{listFn: func(context.Context, int64, int64) ([]domain.Agent, error) {
		return []domain.Agent{domain.DefaultAgent()}, nil
	}})
	models := modelsByProvider(map[string]string{"parent-model": "a"})
	models["unkeyed"] = domain.ModelInfo{Model: domain.Model{ID: "unkeyed", ProviderID: "a"}}
	executor.SetModels(models)

	req := toolReq(7, 9, "list_available_agents", []byte(`{}`))
	req.Model = "parent-model"
	result := executor.ExecuteTool(context.Background(), req)
	if result.IsError || strings.Contains(result.Content, "unkeyed") {
		t.Fatalf("expected the unkeyed model to be left out, got %+v", result)
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
			RunID        int64 `json:"runId"`
			TimedOut     bool  `json:"timedOut"`
			StillRunning bool  `json:"stillRunning"`
		} `json:"results"`
		Completed int    `json:"completed"`
		TimedOut  int    `json:"timedOut"`
		Message   string `json:"message"`
	}
	if err := json.Unmarshal([]byte(result.Content), &payload); err != nil {
		t.Fatalf("decoding wait result: %v", err)
	}
	if payload.Completed != 1 || payload.TimedOut != 1 {
		t.Fatalf("unexpected timeout counts: %+v", payload)
	}
	if !payload.Results[1].TimedOut || !payload.Results[1].StillRunning {
		t.Fatalf("expected second result to be timed out and still running: %+v", payload.Results)
	}
	if payload.Results[0].StillRunning {
		t.Fatalf("expected finished child not to be still running: %+v", payload.Results[0])
	}
	for _, want := range []string{"1 still running", "#404", "wait_for_sub_agents again"} {
		if !strings.Contains(payload.Message, want) {
			t.Fatalf("expected message to contain %q, got %q", want, payload.Message)
		}
	}
	if strings.Contains(payload.Message, "#303") {
		t.Fatalf("expected message not to list the finished child, got %q", payload.Message)
	}
}

func TestWorkspaceExecutor_SpawnSubAgentReportsChildStillRunning(t *testing.T) {
	executor := NewWorkspaceExecutor(&stubWorkspaceOps{})
	executor.SetChildAgentRunner(stubChildAgentRunner{
		startFn: func(_ context.Context, req ChildAgentRunRequest) (*ChildAgentRun, error) {
			return &ChildAgentRun{ID: 303, ParentRunID: req.ParentRunID, WorkspaceID: req.WorkspaceID, Status: "queued"}, nil
		},
		waitFn: func(context.Context, int64, int64, int64) (*ChildAgentRunResult, error) {
			return nil, context.DeadlineExceeded
		},
	})

	req := toolReq(7, 9, "spawn_sub_agent", []byte(`{"prompt":"summarize auth","wait_for_result":true,"timeout_seconds":5}`))
	req.CurrentRunID = 101
	result := executor.ExecuteTool(context.Background(), req)
	if result.IsError {
		t.Fatalf("expected timeout payload, got %+v", result)
	}

	var payload struct {
		TimedOut     bool   `json:"timedOut"`
		StillRunning bool   `json:"stillRunning"`
		Message      string `json:"message"`
	}
	if err := json.Unmarshal([]byte(result.Content), &payload); err != nil {
		t.Fatalf("decoding spawn result: %v", err)
	}
	if !payload.TimedOut || !payload.StillRunning {
		t.Fatalf("expected child to be reported still running: %+v", payload)
	}
	if !strings.Contains(payload.Message, "wait_for_sub_agents") {
		t.Fatalf("expected message to say how to collect the result, got %q", payload.Message)
	}
}

func TestChildAgentWaitSeconds(t *testing.T) {
	tests := []struct {
		name   string
		params map[string]any
		want   int
	}{
		{name: "defaults to the maximum", params: map[string]any{}, want: 600},
		{name: "keeps a shorter wait", params: map[string]any{"timeout_seconds": float64(30)}, want: 30},
		{name: "caps a longer wait", params: map[string]any{"timeout_seconds": float64(3600)}, want: 600},
		{name: "raises a zero wait", params: map[string]any{"timeout_seconds": float64(0)}, want: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := childAgentWaitSeconds(tt.params); got != tt.want {
				t.Fatalf("childAgentWaitSeconds() = %d, want %d", got, tt.want)
			}
		})
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

type recordingImageStore struct {
	data                          []byte
	userID, conversationID, runID int64
}

func (s *recordingImageStore) StoreToolImage(_ context.Context, userID, conversationID, runID int64, data []byte) (domain.ImagePart, error) {
	s.data, s.userID, s.conversationID, s.runID = data, userID, conversationID, runID
	return domain.ImagePart{AttachmentID: 42, MediaType: "image/png", Width: 2, Height: 1, Data: []byte("normalized")}, nil
}

func TestWorkspaceExecutor_ReadFileReturnsImages(t *testing.T) {
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 2, 1))); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	executor := NewWorkspaceExecutor(&stubWorkspaceOps{readFileFn: func(context.Context, int64, int64, string) ([]byte, error) {
		return buf.Bytes(), nil
	}})
	store := &recordingImageStore{}
	executor.SetImageStore(store)

	req := toolReq(1, 2, "read_file", []byte(`{"path":"shot.png"}`))
	req.ConversationID = 3
	req.CurrentRunID = 4
	req.Vision = true
	result := executor.ExecuteTool(context.Background(), req)

	if result.IsError || len(result.Images) != 1 || result.Images[0].AttachmentID != 42 {
		t.Fatalf("unexpected result: %+v", result)
	}
	if !strings.Contains(result.Content, "shot.png (2×1)") {
		t.Fatalf("unexpected content: %q", result.Content)
	}
	if !bytes.Equal(store.data, buf.Bytes()) || store.userID != 1 || store.conversationID != 3 || store.runID != 4 {
		t.Fatalf("unexpected stored image: %+v", store)
	}
}

func TestWorkspaceExecutor_ReadFileKeepsNoImageForModelsWithoutVision(t *testing.T) {
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 3, 2))); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	executor := NewWorkspaceExecutor(&stubWorkspaceOps{readFileFn: func(context.Context, int64, int64, string) ([]byte, error) {
		return buf.Bytes(), nil
	}})
	store := &recordingImageStore{}
	executor.SetImageStore(store)

	result := executor.ExecuteTool(context.Background(), toolReq(1, 2, "read_file", []byte(`{"path":"shot.png"}`)))
	if result.IsError || len(result.Images) != 0 || store.data != nil {
		t.Fatalf("unexpected result: %+v (stored %d bytes)", result, len(store.data))
	}
	if !strings.Contains(result.Content, "shot.png is a 3×2 image") || !strings.Contains(result.Content, "can't view images") {
		t.Fatalf("unexpected content: %q", result.Content)
	}
}

func TestWorkspaceExecutor_ReadFileReadsTextWithAnImageStore(t *testing.T) {
	executor := NewWorkspaceExecutor(&stubWorkspaceOps{readFileFn: func(context.Context, int64, int64, string) ([]byte, error) {
		return []byte("package main\n"), nil
	}})
	store := &recordingImageStore{}
	executor.SetImageStore(store)

	result := executor.ExecuteTool(context.Background(), toolReq(1, 2, "read_file", []byte(`{"path":"main.go"}`)))
	if result.Content != "package main\n" || len(result.Images) != 0 || store.data != nil {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestTruncateToolTextCutsOnACharacter(t *testing.T) {
	content := "\xff" + strings.Repeat("x", 7) + "€"
	if got := truncateToolText(content, 10, "[cut to %d]"); got != "\xff"+strings.Repeat("x", 7)+"[cut to 10]" {
		t.Fatalf("expected the cut just before the split character, got %q", got)
	}
}
