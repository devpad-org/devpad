package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/devpad-org/devpad/internal/agent"
	"github.com/devpad-org/devpad/internal/ai/domain"
	"github.com/devpad-org/devpad/internal/workspace"
)

const (
	childAgentDefaultWaitSeconds = 120
	childAgentMaxWaitSeconds     = 600
)

// WorkspaceOps is the reduced workspace surface the AI tooling needs in Phase 1.
type WorkspaceOps interface {
	workspace.FileContentService
	workspace.FileSearchService
	workspace.CommandService
}

// WorkspaceExecutor implements Executor using workspace operations.
type WorkspaceExecutor struct {
	ws          WorkspaceOps
	childRunner ChildAgentRunner
}

// NewWorkspaceExecutor creates an Executor backed by workspace operations.
func NewWorkspaceExecutor(ws WorkspaceOps) *WorkspaceExecutor {
	return &WorkspaceExecutor{ws: ws}
}

// SetChildAgentRunner wires the background runner used by spawn_sub_agent.
func (e *WorkspaceExecutor) SetChildAgentRunner(runner ChildAgentRunner) {
	e.childRunner = runner
}

func (e *WorkspaceExecutor) ExecuteTool(ctx context.Context, req ExecutionRequest) domain.ToolResultPart {
	var params map[string]any
	if err := json.Unmarshal(req.Arguments, &params); err != nil {
		return toolFailure(req.ToolName, "invalid tool arguments: %v", err)
	}

	switch req.ToolName {
	case "read_file":
		return e.readFile(ctx, req.UserID, req.WorkspaceID, params)
	case "write_file":
		return e.writeFile(ctx, req.UserID, req.WorkspaceID, params)
	case "list_files":
		return e.listFiles(ctx, req.UserID, req.WorkspaceID, params)
	case "delete_file":
		return e.deleteFile(ctx, req.UserID, req.WorkspaceID, params)
	case "search_files":
		return e.searchFiles(ctx, req.UserID, req.WorkspaceID, params)
	case "run_command":
		return e.runCommand(ctx, req.UserID, req.WorkspaceID, params)
	case "edit_file":
		return e.editFile(ctx, req.UserID, req.WorkspaceID, params)
	case "read_file_lines":
		return e.readFileLines(ctx, req.UserID, req.WorkspaceID, params)
	case "update_plan":
		return toolSuccess(req.ToolName, "Plan updated.")
	case "spawn_sub_agent":
		return e.spawnSubAgent(ctx, req, params)
	default:
		return toolFailure(req.ToolName, "unknown tool: %s", req.ToolName)
	}
}

func (e *WorkspaceExecutor) readFile(ctx context.Context, userID, workspaceID int64, params map[string]any) domain.ToolResultPart {
	path, _ := params["path"].(string)
	if path == "" {
		return toolFailure("read_file", "Error: path is required")
	}
	data, err := e.ws.ReadFile(ctx, userID, workspaceID, path)
	if err != nil {
		return toolFailure("read_file", "Error: %v", err)
	}

	return toolSuccess("read_file", string(data))
}

func (e *WorkspaceExecutor) writeFile(ctx context.Context, userID, workspaceID int64, params map[string]any) domain.ToolResultPart {
	path, _ := params["path"].(string)
	content, _ := params["content"].(string)
	if path == "" {
		return toolFailure("write_file", "Error: path is required")
	}
	if err := e.ws.WriteFile(ctx, userID, workspaceID, path, []byte(content)); err != nil {
		return toolFailure("write_file", "Error: %v", err)
	}

	return toolSuccess("write_file", "File written successfully.")
}

func (e *WorkspaceExecutor) listFiles(ctx context.Context, userID, workspaceID int64, params map[string]any) domain.ToolResultPart {
	path, _ := params["path"].(string)
	entries, err := e.ws.ListFiles(ctx, userID, workspaceID, path)
	if err != nil {
		return toolFailure("list_files", "Error: %v", err)
	}
	data, err := json.Marshal(formatFileEntries(entries))
	if err != nil {
		return toolFailure("list_files", "Error: %v", err)
	}

	return toolSuccess("list_files", string(data))
}

func (e *WorkspaceExecutor) deleteFile(ctx context.Context, userID, workspaceID int64, params map[string]any) domain.ToolResultPart {
	path, _ := params["path"].(string)
	if path == "" {
		return toolFailure("delete_file", "Error: path is required")
	}
	if err := e.ws.DeleteFile(ctx, userID, workspaceID, path); err != nil {
		return toolFailure("delete_file", "Error: %v", err)
	}

	return toolSuccess("delete_file", "Deleted successfully.")
}

func (e *WorkspaceExecutor) searchFiles(ctx context.Context, userID, workspaceID int64, params map[string]any) domain.ToolResultPart {
	pattern, _ := params["pattern"].(string)
	if pattern == "" {
		return toolFailure("search_files", "Error: pattern is required")
	}
	pathFilter, _ := params["path_filter"].(string)
	maxResults := 100
	if value, ok := params["max_results"].(float64); ok {
		maxResults = int(value)
	}
	results, err := e.ws.SearchFiles(ctx, userID, workspaceID, pattern, pathFilter, maxResults)
	if err != nil {
		return toolFailure("search_files", "Error: %v", err)
	}
	data, err := json.Marshal(results)
	if err != nil {
		return toolFailure("search_files", "Error: %v", err)
	}

	return toolSuccess("search_files", string(data))
}

func (e *WorkspaceExecutor) runCommand(ctx context.Context, userID, workspaceID int64, params map[string]any) domain.ToolResultPart {
	command, _ := params["command"].(string)
	if command == "" {
		return toolFailure("run_command", "Error: command is required")
	}
	result, err := e.ws.RunCommand(ctx, userID, workspaceID, command)
	if err != nil {
		return toolFailure("run_command", "Error: %v", err)
	}
	if result.ExitCode != 0 {
		message := fmt.Sprintf("Command failed (exit code %d):\n%s", result.ExitCode, result.Output)
		if result.Error != "" {
			message += "\n" + result.Error
		}
		return toolFailure("run_command", "%s", message)
	}
	if result.Error != "" {
		return toolFailure("run_command", "Command timed out:\n%s\n%s", result.Output, result.Error)
	}

	return toolSuccess("run_command", result.Output)
}

func (e *WorkspaceExecutor) editFile(ctx context.Context, userID, workspaceID int64, params map[string]any) domain.ToolResultPart {
	path, _ := params["path"].(string)
	oldText, _ := params["old_text"].(string)
	newText, _ := params["new_text"].(string)
	if path == "" || oldText == "" {
		return toolFailure("edit_file", "Error: path and old_text are required")
	}

	data, err := e.ws.ReadFile(ctx, userID, workspaceID, path)
	if err != nil {
		return toolFailure("edit_file", "Error reading file: %v", err)
	}

	content := string(data)
	count := strings.Count(content, oldText)
	if count == 0 {
		return toolFailure("edit_file", "Error: old_text not found in file")
	}
	if count > 1 {
		return toolFailure("edit_file", "Error: old_text found %d times, must appear exactly once", count)
	}

	newContent := strings.Replace(content, oldText, newText, 1)
	if err := e.ws.WriteFile(ctx, userID, workspaceID, path, []byte(newContent)); err != nil {
		return toolFailure("edit_file", "Error writing file: %v", err)
	}

	return toolSuccess("edit_file", "File edited successfully.")
}

func (e *WorkspaceExecutor) readFileLines(ctx context.Context, userID, workspaceID int64, params map[string]any) domain.ToolResultPart {
	path, _ := params["path"].(string)
	if path == "" {
		return toolFailure("read_file_lines", "Error: path is required")
	}
	startLine := 1
	endLine := -1
	if value, ok := params["start_line"].(float64); ok {
		startLine = int(value)
	}
	if value, ok := params["end_line"].(float64); ok {
		endLine = int(value)
	}

	data, err := e.ws.ReadFile(ctx, userID, workspaceID, path)
	if err != nil {
		return toolFailure("read_file_lines", "Error: %v", err)
	}

	lines := strings.Split(string(data), "\n")
	if startLine < 1 {
		startLine = 1
	}
	if endLine < 0 || endLine > len(lines) {
		endLine = len(lines)
	}
	if startLine > len(lines) {
		return toolFailure("read_file_lines", "Error: start_line is beyond end of file")
	}

	selected := lines[startLine-1 : endLine]
	var result strings.Builder
	for i, line := range selected {
		fmt.Fprintf(&result, "%d: %s", startLine+i, line)
		if i < len(selected)-1 {
			result.WriteString("\n")
		}
	}

	return toolSuccess("read_file_lines", result.String())
}

func (e *WorkspaceExecutor) spawnSubAgent(ctx context.Context, req ExecutionRequest, params map[string]any) domain.ToolResultPart {
	if req.CurrentRunID <= 0 {
		return toolFailure("spawn_sub_agent", "Error: spawn_sub_agent is only available from a persisted agent run")
	}
	if e.childRunner == nil {
		return toolFailure("spawn_sub_agent", "Error: sub-agent runner is not available")
	}

	prompt, _ := params["prompt"].(string)
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return toolFailure("spawn_sub_agent", "Error: prompt is required")
	}

	model, _ := params["model"].(string)
	model = strings.TrimSpace(model)
	if model == "" {
		model = req.Model
	}

	child, err := e.childRunner.StartChildAgentRun(ctx, ChildAgentRunRequest{
		ParentRunID:    req.CurrentRunID,
		UserID:         req.UserID,
		WorkspaceID:    req.WorkspaceID,
		ConversationID: req.ConversationID,
		Model:          model,
		Prompt:         prompt,
		Thinking:       cloneThinking(req.Thinking),
	})
	if err != nil {
		return toolFailure("spawn_sub_agent", "Error: %v", err)
	}

	response := map[string]any{
		"runId":          child.ID,
		"parentRunId":    child.ParentRunID,
		"workspaceId":    child.WorkspaceID,
		"conversationId": child.ConversationID,
		"model":          child.Model,
		"status":         child.Status,
		"message":        fmt.Sprintf("Sub-agent run #%d started.", child.ID),
	}

	waitForResult, _ := params["wait_for_result"].(bool)
	if waitForResult {
		timeoutSeconds := childAgentDefaultWaitSeconds
		if value, ok := params["timeout_seconds"].(float64); ok {
			timeoutSeconds = int(value)
		}
		timeoutSeconds = clamp(timeoutSeconds, 1, childAgentMaxWaitSeconds)

		waitCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSeconds)*time.Second)
		result, waitErr := e.childRunner.WaitChildAgentRun(waitCtx, req.UserID, child.ID)
		cancel()
		if waitErr != nil {
			if errors.Is(waitErr, context.DeadlineExceeded) {
				response["timedOut"] = true
				response["message"] = fmt.Sprintf("Sub-agent run #%d is still running after %d seconds.", child.ID, timeoutSeconds)
				return marshalToolResponse("spawn_sub_agent", response)
			}
			return toolFailure("spawn_sub_agent", "Error waiting for sub-agent result: %v", waitErr)
		}
		response["status"] = result.Status
		response["summary"] = result.Summary
		if result.Error != "" {
			response["error"] = result.Error
			response["message"] = fmt.Sprintf("Sub-agent run #%d finished with an error.", child.ID)
		} else {
			response["message"] = fmt.Sprintf("Sub-agent run #%d completed.", child.ID)
		}
	}

	return marshalToolResponse("spawn_sub_agent", response)
}

func marshalToolResponse(toolName string, response map[string]any) domain.ToolResultPart {
	data, err := json.Marshal(response)
	if err != nil {
		return toolFailure(toolName, "Error: %v", err)
	}

	return toolSuccess(toolName, string(data))
}

func toolSuccess(toolName, content string) domain.ToolResultPart {
	return domain.ToolResultPart{
		Name:    toolName,
		Content: content,
	}
}

func toolFailure(toolName, format string, args ...any) domain.ToolResultPart {
	return domain.ToolResultPart{
		Name:    toolName,
		Content: fmt.Sprintf(format, args...),
		IsError: true,
	}
}

func cloneThinking(thinking *domain.ThinkingConfig) *domain.ThinkingConfig {
	if thinking == nil {
		return nil
	}
	clone := &domain.ThinkingConfig{}
	if thinking.Enabled != nil {
		enabled := *thinking.Enabled
		clone.Enabled = &enabled
	}
	return clone
}

func clamp(value, minValue, maxValue int) int {
	if value < minValue {
		return minValue
	}
	if value > maxValue {
		return maxValue
	}
	return value
}

func formatFileEntries(entries []agent.FileEntry) []map[string]any {
	result := make([]map[string]any, 0, len(entries))
	for _, entry := range entries {
		result = append(result, map[string]any{
			"name": entry.Name,
			"type": entry.Type,
			"size": entry.Size,
		})
	}

	return result
}
