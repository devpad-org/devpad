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
}

// AgentLister exposes the agents a user can assign to child runs.
type AgentLister interface {
	ListAgents(ctx context.Context, userID, workspaceID int64) ([]domain.Agent, error)
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
