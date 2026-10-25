package tools

import (
	"context"
	"encoding/json"

	"github.com/devpad-org/devpad/internal/ai/domain"
)

// ExecutionRequest carries the tool call plus the current agent run context.
type ExecutionRequest struct {
	UserID         int64
	WorkspaceID    int64
	CurrentRunID   int64
	ConversationID int64
	Model          string
	Thinking       *domain.ThinkingConfig
	ToolName       string
	Arguments      json.RawMessage
	// Approved is set only after the user approved this exact call. Tools
	// that need approval refuse to run without it, so a caller that skips the
	// approval step cannot start them.
	Approved bool
}

// FileSummaryRequest contains the workspace file content prepared for summarization.
type FileSummaryRequest struct {
	UserID      int64
	WorkspaceID int64
	Path        string
	Focus       string
	Content     string
}

// FileSummarizer produces concise AI-oriented repository file summaries.
type FileSummarizer interface {
	SummarizeFile(ctx context.Context, req FileSummaryRequest) (string, error)
}

// AgentLister exposes the agents a user can assign to child runs.
type AgentLister interface {
	ListAgents(ctx context.Context, userID, workspaceID int64) ([]domain.Agent, error)
}

// AgentDefaults looks up the model and effort an admin set for a built-in
// agent, or nil when it has none a run could use. An effort picked without
// a model is kept when the default model supports it.
type AgentDefaults interface {
	DefaultsFor(ctx context.Context, agentID, effort string) (*domain.AgentDefaults, error)
}

// ChildAgentRunRequest is the minimal request a tool executor needs to spawn a child run.
type ChildAgentRunRequest struct {
	ParentRunID    int64
	UserID         int64
	WorkspaceID    int64
	ConversationID int64
	AgentID        string
	Model          string
	Prompt         string
	Thinking       *domain.ThinkingConfig
}

// ChildAgentRun is the lightweight result returned after a child run is queued.
type ChildAgentRun struct {
	ID             int64  `json:"id"`
	ParentRunID    int64  `json:"parentRunId"`
	WorkspaceID    int64  `json:"workspaceId"`
	ConversationID int64  `json:"conversationId,omitempty"`
	AgentID        string `json:"agentId"`
	Model          string `json:"model"`
	Status         string `json:"status"`
}

// ChildAgentRunResult is the final text produced by a child run.
type ChildAgentRunResult struct {
	RunID   int64
	Status  string
	Summary string
	Error   string
}

// ChildAgentRunner starts child agent runs and can wait for their result.
type ChildAgentRunner interface {
	StartChildAgentRun(ctx context.Context, req ChildAgentRunRequest) (*ChildAgentRun, error)
	WaitChildAgentRun(ctx context.Context, userID, parentRunID, runID int64) (*ChildAgentRunResult, error)
}

// Executor executes AI agent tools against a workspace.
type Executor interface {
	ExecuteTool(ctx context.Context, req ExecutionRequest) domain.ToolResultPart
}

// ToolProvider contributes tools that only some workspaces offer, such as the
// review tools in a merge request's review sandbox.
type ToolProvider interface {
	// Tools returns the extra tools offered to runs in the workspace, if any.
	Tools(ctx context.Context, userID, workspaceID int64) []domain.ToolDefinition
	// ExecuteTool runs a tool call, reporting false if the tool is not its own.
	ExecuteTool(ctx context.Context, req ExecutionRequest) (domain.ToolResultPart, bool)
}

// Approval describes a tool call the user must approve before it runs.
type Approval struct {
	// Title names what is being approved, such as "Start an agent".
	Title string
	// Summary says what the call will do. It is written by the server, never
	// copied from the model's arguments.
	Summary string
	// Details lists everything else the call carries, in full.
	Details []domain.ApprovalDetail
}

// ApprovalGate is a ToolProvider some of whose calls need the user's
// approval before they run.
type ApprovalGate interface {
	// ApprovalFor reports whether a call needs approval, and describes it.
	// An error means it could not tell; the call must then not run.
	ApprovalFor(ctx context.Context, req ExecutionRequest) (Approval, bool, error)
}

// PromptProvider is a ToolProvider that adds context to the system prompt
// of runs in the workspaces it serves.
type PromptProvider interface {
	// Prompt returns the context for runs in the workspace, if any.
	Prompt(ctx context.Context, userID, workspaceID int64) string
}
