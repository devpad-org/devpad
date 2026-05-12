package orchestrator

import (
	"context"

	"github.com/devpad-org/devpad/internal/ai/approval"
	"github.com/devpad-org/devpad/internal/ai/domain"
	aitools "github.com/devpad-org/devpad/internal/ai/tools"
)

// ChatService is the provider-backed streaming surface the orchestrators depend on.
type ChatService interface {
	ChatStream(ctx context.Context, req domain.ChatRequest) (<-chan domain.ProviderEvent, error)
	FindModel(modelID string) (domain.Model, error)
}

// ToolCatalog exposes the built-in agent prompt and tool definitions.
type ToolCatalog interface {
	SystemPrompt() string
	Definitions() []domain.ToolDefinition
}

// ToolExecutor executes agent tools against a workspace.
type ToolExecutor interface {
	ExecuteTool(ctx context.Context, req aitools.ExecutionRequest) domain.ToolResultPart
}

// AgentChatRequest contains the inputs needed to run the agent loop.
type AgentChatRequest struct {
	UserID                int64
	WorkspaceID           int64
	CurrentRunID          int64
	ConversationID        int64
	Model                 string
	Turns                 []domain.Turn
	Thinking              *domain.ThinkingConfig
	AgentPrompt           string
	WorkspaceInstructions string
}

// AgentChatOrchestrator owns the agent runtime state machine outside HTTP transport.
type AgentChatOrchestrator interface {
	Stream(ctx context.Context, req AgentChatRequest) (<-chan domain.ClientEvent, error)
}

var _ approval.Broker

func emitEvent(ctx context.Context, out chan<- domain.ClientEvent, event domain.ClientEvent) bool {
	select {
	case out <- event:
		return true
	case <-ctx.Done():
		return false
	}
}
