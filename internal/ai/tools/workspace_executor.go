package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/devpad-org/devpad/internal/agent"
	"github.com/devpad-org/devpad/internal/ai/attachment"
	"github.com/devpad-org/devpad/internal/ai/domain"
	"github.com/devpad-org/devpad/internal/workspace"
)

const (
	maxToolTextBytes      = 16 * 1024
	maxSummaryInputBytes  = 128 * 1024
	maxSummaryOutputBytes = 4 * 1024
	maxReadFileLines      = 400
	maxSearchFileResults  = 100
	// Keep recursive listings below the raw agent API caps to protect model context.
	maxListFilesDepth      = 6
	maxListFilesEntries    = 1000
	truncationNoticeFormat = "\n\n[Output truncated to %d bytes. Use read_file_lines with a narrower range or search_files for more targeted context.]"
	summaryInputNotice     = "\n\n[File content truncated to %d bytes before summarization.]"
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
	agentLister AgentLister
	defaults    AgentDefaults
	summarizer  FileSummarizer
	images      ImageStore

	providersMu sync.RWMutex
	providers   []ToolProvider
}

// NewWorkspaceExecutor creates an Executor backed by workspace operations.
func NewWorkspaceExecutor(ws WorkspaceOps) *WorkspaceExecutor {
	return &WorkspaceExecutor{ws: ws}
}

// SetChildAgentRunner wires the background runner used by spawn_sub_agent.
func (e *WorkspaceExecutor) SetChildAgentRunner(runner ChildAgentRunner) {
	e.childRunner = runner
}

// SetAgentLister wires the agent catalog exposed to coordination tools.
func (e *WorkspaceExecutor) SetAgentLister(lister AgentLister) {
	e.agentLister = lister
}

// SetAgentDefaults wires the admin's agent defaults, which child runs use
// when spawn_sub_agent names no model.
func (e *WorkspaceExecutor) SetAgentDefaults(defaults AgentDefaults) {
	e.defaults = defaults
}

// AddProvider routes calls for a provider's workspace-specific tools.
func (e *WorkspaceExecutor) AddProvider(p ToolProvider) {
	e.providersMu.Lock()
	defer e.providersMu.Unlock()
	e.providers = append(e.providers, p)
}

// SetImageStore wires the store read_file keeps the images it opens in.
// Without one, image files are read as text.
func (e *WorkspaceExecutor) SetImageStore(images ImageStore) {
	e.images = images
}

// SetFileSummarizer wires the AI summarizer used by summarize_file.
func (e *WorkspaceExecutor) SetFileSummarizer(summarizer FileSummarizer) {
	e.summarizer = summarizer
}

// ApprovalFor reports whether a provider requires the user's approval
// before the call runs.
func (e *WorkspaceExecutor) ApprovalFor(ctx context.Context, req ExecutionRequest) (Approval, bool, error) {
	e.providersMu.RLock()
	providers := e.providers
	e.providersMu.RUnlock()
	for _, p := range providers {
		if gate, ok := p.(ApprovalGate); ok {
			approval, needed, err := gate.ApprovalFor(ctx, req)
			if err != nil {
				return Approval{}, false, err
			}
			if needed {
				return approval, true, nil
			}
		}
	}
	return Approval{}, false, nil
}

func (e *WorkspaceExecutor) ExecuteTool(ctx context.Context, req ExecutionRequest) domain.ToolResultPart {
	var params map[string]any
	if err := json.Unmarshal(req.Arguments, &params); err != nil {
		return toolFailure(req.ToolName, "invalid tool arguments: %v", err)
	}

	switch req.ToolName {
	case "read_file":
		return e.readFile(ctx, req, params)
	case "summarize_file":
		return e.summarizeFile(ctx, req.UserID, req.WorkspaceID, params)
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
	case "start_command":
		return e.startCommand(ctx, req.UserID, req.WorkspaceID, params)
	case "read_command_output":
		return e.readCommandOutput(ctx, req.UserID, req.WorkspaceID, params)
	case "command_status":
		return e.commandStatus(ctx, req.UserID, req.WorkspaceID, params)
	case "stop_command":
		return e.stopCommand(ctx, req.UserID, req.WorkspaceID, params)
	case "edit_file":
		return e.editFile(ctx, req.UserID, req.WorkspaceID, params)
	case "read_file_lines":
		return e.readFileLines(ctx, req.UserID, req.WorkspaceID, params)
	case "update_plan":
		return toolSuccess(req.ToolName, "Plan updated.")
	case "list_available_agents":
		return e.listAvailableAgents(ctx, req)
	case "spawn_sub_agent":
		return e.spawnSubAgent(ctx, req, params)
	case "wait_for_sub_agents":
		return e.waitForSubAgents(ctx, req, params)
	default:
		e.providersMu.RLock()
		providers := e.providers
		e.providersMu.RUnlock()
		for _, p := range providers {
			if result, ok := p.ExecuteTool(ctx, req); ok {
				return result
			}
		}
		return toolFailure(req.ToolName, "unknown tool: %s", req.ToolName)
	}
}

func (e *WorkspaceExecutor) readFile(ctx context.Context, req ExecutionRequest, params map[string]any) domain.ToolResultPart {
	path, _ := params["path"].(string)
	if path == "" {
		return toolFailure("read_file", "Error: path is required")
	}
	data, err := e.ws.ReadFile(ctx, req.UserID, req.WorkspaceID, path)
	if err != nil {
		return toolFailure("read_file", "Error: %v", err)
	}
	if e.images != nil && attachment.IsImage(data) {
		return e.readImage(ctx, req, path, data)
	}

	return toolSuccess("read_file", truncateToolText(string(data), maxToolTextBytes, truncationNoticeFormat))
}

// readImage returns an image file as an image the model can look at.
func (e *WorkspaceExecutor) readImage(ctx context.Context, req ExecutionRequest, path string, data []byte) domain.ToolResultPart {
	image, err := e.images.StoreToolImage(ctx, req.UserID, req.ConversationID, req.CurrentRunID, data)
	if err != nil {
		return toolFailure("read_file", "Error: %s is an image that can't be shown: %v", path, err)
	}
	result := toolSuccess("read_file", fmt.Sprintf("Image %s (%d×%d) is attached.", path, image.Width, image.Height))
	result.Images = []domain.ImagePart{image}
	return result
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
	opts := agent.ListFilesOptions{}
	if value, ok := params["recursive"].(bool); ok {
		opts.Recursive = value
	}
	if value, ok := params["max_depth"].(float64); ok {
		opts.MaxDepth = clamp(int(value), 1, maxListFilesDepth)
	}
	if value, ok := params["max_entries"].(float64); ok {
		opts.MaxEntries = clamp(int(value), 1, maxListFilesEntries)
	}

	list, err := e.ws.ListFiles(ctx, userID, workspaceID, path, opts)
	if err != nil {
		return toolFailure("list_files", "Error: %v", err)
	}
	if list == nil {
		return toolFailure("list_files", "Error: empty file list response")
	}
	data, err := json.Marshal(map[string]any{
		"entries":   formatFileEntries(list.Entries),
		"truncated": list.Truncated,
	})
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
	maxResults = clamp(maxResults, 1, maxSearchFileResults)
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
	if endLine < startLine {
		return toolFailure("read_file_lines", "Error: end_line must be greater than or equal to start_line")
	}
	maxEndLine := startLine + maxReadFileLines - 1
	if endLine > maxEndLine {
		endLine = maxEndLine
	}

	selected := lines[startLine-1 : endLine]
	var result strings.Builder
	for i, line := range selected {
		fmt.Fprintf(&result, "%d: %s", startLine+i, line)
		if i < len(selected)-1 {
			result.WriteString("\n")
		}
	}

	return toolSuccess("read_file_lines", truncateToolText(result.String(), maxToolTextBytes, truncationNoticeFormat))
}

func marshalToolResponse(toolName string, response any) domain.ToolResultPart {
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

func truncateToolText(content string, maxBytes int, noticeFormat string) string {
	if maxBytes <= 0 || len(content) <= maxBytes {
		return content
	}

	truncated := content[:maxBytes]
	for !utf8.ValidString(truncated) && len(truncated) > 0 {
		truncated = truncated[:len(truncated)-1]
	}
	return truncated + fmt.Sprintf(noticeFormat, maxBytes)
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
	clone.Effort = thinking.Effort
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
			"path": trimWorkspacePath(entry.Path),
			"type": entry.Type,
			"size": entry.Size,
		})
	}

	return result
}

func trimWorkspacePath(path string) string {
	if path == "/workspace" {
		return ""
	}
	return strings.TrimPrefix(path, "/workspace/")
}
