package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/devpad-org/devpad/internal/agent"
	"github.com/devpad-org/devpad/internal/workspace"
)

// WorkspaceToolExecutor implements ToolExecutor using workspace.Service operations.
type WorkspaceToolExecutor struct {
	ws workspace.Service
}

// NewToolExecutor creates a ToolExecutor backed by a workspace service.
func NewToolExecutor(ws workspace.Service) *WorkspaceToolExecutor {
	return &WorkspaceToolExecutor{ws: ws}
}

func (e *WorkspaceToolExecutor) ExecuteTool(ctx context.Context, userID, workspaceID int64, toolName string, args json.RawMessage) (string, error) {
	var params map[string]any
	if err := json.Unmarshal(args, &params); err != nil {
		return "", fmt.Errorf("invalid tool arguments: %w", err)
	}

	switch toolName {
	case "read_file":
		return e.readFile(ctx, userID, workspaceID, params)
	case "write_file":
		return e.writeFile(ctx, userID, workspaceID, params)
	case "list_files":
		return e.listFiles(ctx, userID, workspaceID, params)
	case "delete_file":
		return e.deleteFile(ctx, userID, workspaceID, params)
	case "search_files":
		return e.searchFiles(ctx, userID, workspaceID, params)
	case "run_command":
		return e.runCommand(ctx, userID, workspaceID, params)
	case "edit_file":
		return e.editFile(ctx, userID, workspaceID, params)
	case "read_file_lines":
		return e.readFileLines(ctx, userID, workspaceID, params)
	case "update_plan":
		return "Plan updated.", nil
	default:
		return "", fmt.Errorf("unknown tool: %s", toolName)
	}
}

func (e *WorkspaceToolExecutor) readFile(ctx context.Context, userID, workspaceID int64, params map[string]any) (string, error) {
	path, _ := params["path"].(string)
	if path == "" {
		return "Error: path is required", nil
	}
	data, err := e.ws.ReadFile(ctx, userID, workspaceID, path)
	if err != nil {
		return fmt.Sprintf("Error: %v", err), nil
	}
	return string(data), nil
}

func (e *WorkspaceToolExecutor) writeFile(ctx context.Context, userID, workspaceID int64, params map[string]any) (string, error) {
	path, _ := params["path"].(string)
	content, _ := params["content"].(string)
	if path == "" {
		return "Error: path is required", nil
	}
	if err := e.ws.WriteFile(ctx, userID, workspaceID, path, []byte(content)); err != nil {
		return fmt.Sprintf("Error: %v", err), nil
	}
	return "File written successfully.", nil
}

func (e *WorkspaceToolExecutor) listFiles(ctx context.Context, userID, workspaceID int64, params map[string]any) (string, error) {
	path, _ := params["path"].(string)
	entries, err := e.ws.ListFiles(ctx, userID, workspaceID, path)
	if err != nil {
		return fmt.Sprintf("Error: %v", err), nil
	}
	data, _ := json.Marshal(formatFileEntries(entries))
	return string(data), nil
}

func (e *WorkspaceToolExecutor) deleteFile(ctx context.Context, userID, workspaceID int64, params map[string]any) (string, error) {
	path, _ := params["path"].(string)
	if path == "" {
		return "Error: path is required", nil
	}
	if err := e.ws.DeleteFile(ctx, userID, workspaceID, path); err != nil {
		return fmt.Sprintf("Error: %v", err), nil
	}
	return "Deleted successfully.", nil
}

func (e *WorkspaceToolExecutor) searchFiles(ctx context.Context, userID, workspaceID int64, params map[string]any) (string, error) {
	pattern, _ := params["pattern"].(string)
	if pattern == "" {
		return "Error: pattern is required", nil
	}
	pathFilter, _ := params["path_filter"].(string)
	maxResults := 100
	if v, ok := params["max_results"].(float64); ok {
		maxResults = int(v)
	}
	results, err := e.ws.SearchFiles(ctx, userID, workspaceID, pattern, pathFilter, maxResults)
	if err != nil {
		return fmt.Sprintf("Error: %v", err), nil
	}
	data, _ := json.Marshal(results)
	return string(data), nil
}

func (e *WorkspaceToolExecutor) runCommand(ctx context.Context, userID, workspaceID int64, params map[string]any) (string, error) {
	command, _ := params["command"].(string)
	if command == "" {
		return "Error: command is required", nil
	}
	result, err := e.ws.RunCommand(ctx, userID, workspaceID, command)
	if err != nil {
		return fmt.Sprintf("Error: %v", err), nil
	}
	if result.ExitCode != 0 {
		msg := fmt.Sprintf("Command failed (exit code %d):\n%s", result.ExitCode, result.Output)
		if result.Error != "" {
			msg += "\n" + result.Error
		}
		return msg, nil
	}
	if result.Error != "" {
		return fmt.Sprintf("Command timed out:\n%s\n%s", result.Output, result.Error), nil
	}
	return result.Output, nil
}

func (e *WorkspaceToolExecutor) editFile(ctx context.Context, userID, workspaceID int64, params map[string]any) (string, error) {
	path, _ := params["path"].(string)
	oldText, _ := params["old_text"].(string)
	newText, _ := params["new_text"].(string)
	if path == "" || oldText == "" {
		return "Error: path and old_text are required", nil
	}

	data, err := e.ws.ReadFile(ctx, userID, workspaceID, path)
	if err != nil {
		return fmt.Sprintf("Error reading file: %v", err), nil
	}

	content := string(data)
	count := strings.Count(content, oldText)
	if count == 0 {
		return "Error: old_text not found in file", nil
	}
	if count > 1 {
		return fmt.Sprintf("Error: old_text found %d times, must appear exactly once", count), nil
	}

	newContent := strings.Replace(content, oldText, newText, 1)
	if err := e.ws.WriteFile(ctx, userID, workspaceID, path, []byte(newContent)); err != nil {
		return fmt.Sprintf("Error writing file: %v", err), nil
	}
	return "File edited successfully.", nil
}

func (e *WorkspaceToolExecutor) readFileLines(ctx context.Context, userID, workspaceID int64, params map[string]any) (string, error) {
	path, _ := params["path"].(string)
	if path == "" {
		return "Error: path is required", nil
	}
	startLine := 1
	endLine := -1
	if v, ok := params["start_line"].(float64); ok {
		startLine = int(v)
	}
	if v, ok := params["end_line"].(float64); ok {
		endLine = int(v)
	}

	data, err := e.ws.ReadFile(ctx, userID, workspaceID, path)
	if err != nil {
		return fmt.Sprintf("Error: %v", err), nil
	}

	lines := strings.Split(string(data), "\n")
	if startLine < 1 {
		startLine = 1
	}
	if endLine < 0 || endLine > len(lines) {
		endLine = len(lines)
	}
	if startLine > len(lines) {
		return "Error: start_line is beyond end of file", nil
	}

	selected := lines[startLine-1 : endLine]
	var result strings.Builder
	for i, line := range selected {
		fmt.Fprintf(&result, "%d: %s", startLine+i, line)
		if i < len(selected)-1 {
			result.WriteString("\n")
		}
	}
	return result.String(), nil
}

// formatFileEntries converts agent.FileEntry slice to a simpler format for the AI.
func formatFileEntries(entries []agent.FileEntry) []map[string]any {
	result := make([]map[string]any, 0, len(entries))
	for _, e := range entries {
		result = append(result, map[string]any{
			"name": e.Name,
			"type": e.Type,
			"size": e.Size,
		})
	}
	return result
}
