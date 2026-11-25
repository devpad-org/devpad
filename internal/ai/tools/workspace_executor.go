package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/devpad-org/devpad/internal/agent"
	"github.com/devpad-org/devpad/internal/ai/domain"
	"github.com/devpad-org/devpad/internal/workspace"
)

const (
	maxToolTextBytes      = 16 * 1024
	maxSummaryInputBytes  = 128 * 1024
	maxSummaryOutputBytes = 4 * 1024
	maxReadFileLines      = 400
	// Keep recursive listings below the raw agent API caps to protect model context.
	maxListFilesDepth      = 6
	maxListFilesEntries    = 1000
	truncationNoticeFormat = "\n\n[Output truncated to %d bytes. Use read_file with start_line and end_line, or search_files, for more targeted context.]"
	summaryInputNotice     = "\n\n[File content truncated to %d bytes before summarization.]"
)

// maxToolWait is the longest a tool call waits, for a command or for
// sub-agents, before it returns for the agent to wait again. A provider's
// prompt cache keeps a conversation for five minutes from the request that
// last read it, a time that also covers the model's answer to it, so a
// wait of four minutes leaves the next request reading the conversation
// from the cache rather than writing all of it again. Tests shorten it.
var maxToolWait = 4 * time.Minute

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
	models      ModelCatalog
	summarizer  FileSummarizer
	images      ImageStore

	providersMu sync.RWMutex
	providers   []ToolProvider
	scopes      ScopeResolver
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

// SetAgentDefaults wires users' agent defaults, which child runs use
// when spawn_sub_agent names no model.
func (e *WorkspaceExecutor) SetAgentDefaults(defaults AgentDefaults) {
	e.defaults = defaults
}

// SetModels wires the model catalog, which list_available_agents lists the
// models a child can use from, and which tells spawn_sub_agent whether an
// agent's default model is from the parent's provider and an effort suits a
// model.
func (e *WorkspaceExecutor) SetModels(models ModelCatalog) {
	e.models = models
}

// AddProvider routes calls for a provider's workspace-specific tools.
func (e *WorkspaceExecutor) AddProvider(p ToolProvider) {
	e.providersMu.Lock()
	defer e.providersMu.Unlock()
	e.providers = append(e.providers, p)
}

// SetScopeResolver sets what tells providers what a call's workspace is.
func (e *WorkspaceExecutor) SetScopeResolver(r ScopeResolver) {
	e.providersMu.Lock()
	defer e.providersMu.Unlock()
	e.scopes = r
}

// retiredTools are tools no longer offered that the executor still runs,
// for conversations whose models called them before: read_file takes a
// range of lines, and read_command_output gives a command's status.
var retiredTools = []string{"read_file_lines", "command_status"}

// builtinTools names the tools the executor runs itself, which no provider
// sees.
var builtinTools = sync.OnceValue(func() map[string]bool {
	names := map[string]bool{}
	for _, d := range AgentTools() {
		names[d.Function.Name] = true
	}
	for _, name := range retiredTools {
		names[name] = true
	}
	return names
})

// providerCall returns the providers, and the call with its scope resolved
// for them. Built-in tools reach no provider and resolve nothing.
func (e *WorkspaceExecutor) providerCall(ctx context.Context, req ExecutionRequest) ([]ToolProvider, ExecutionRequest, error) {
	e.providersMu.RLock()
	providers, scopes := e.providers, e.scopes
	e.providersMu.RUnlock()
	if len(providers) == 0 || builtinTools()[req.ToolName] {
		return nil, req, nil
	}
	scope, err := resolveScope(ctx, scopes, true, req.UserID, req.WorkspaceID)
	if err != nil {
		return nil, req, err
	}
	req.Scope = scope
	return providers, req, nil
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
	providers, req, err := e.providerCall(ctx, req)
	if err != nil {
		return Approval{}, false, fmt.Errorf("resolving the run's scope: %w", err)
	}
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
		return e.summarizeFile(ctx, req, params)
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
		return e.editFile(ctx, req)
	case "read_file_lines":
		return e.readFile(ctx, req, params)
	case "update_plan":
		return toolSuccess(req.ToolName, "Plan updated.")
	case "list_available_agents":
		return e.listAvailableAgents(ctx, req)
	case "spawn_sub_agent":
		return e.spawnSubAgent(ctx, req, params)
	case "wait_for_sub_agents":
		return e.waitForSubAgents(ctx, req, params)
	default:
		providers, req, err := e.providerCall(ctx, req)
		if err != nil {
			return toolFailure(req.ToolName, "Error: could not tell what this workspace is, so the call did not run: %v", err)
		}
		for _, p := range providers {
			if result, ok := p.ExecuteTool(ctx, req); ok {
				return result
			}
		}
		return toolFailure(req.ToolName, "unknown tool: %s", req.ToolName)
	}
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

	return domain.CutText(content, maxBytes) + fmt.Sprintf(noticeFormat, maxBytes)
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

// plural writes a count with the noun that goes with it, such as "1 match"
// or "3 matches".
func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
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
