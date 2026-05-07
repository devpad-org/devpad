package app

import (
	"context"

	"github.com/devpad-org/devpad/internal/ai/approval"
	"github.com/devpad-org/devpad/internal/ai/domain"
	"github.com/devpad-org/devpad/internal/ai/orchestrator"
	aiprovider "github.com/devpad-org/devpad/internal/ai/provider"
)

// SimpleChatRequest contains the inputs for non-agent chat.
type SimpleChatRequest struct {
	UserID   int64
	Model    string
	Turns    []domain.Turn
	Thinking *domain.ThinkingConfig
}

// AgentChatRequest contains the inputs for tool-enabled agent chat.
type AgentChatRequest struct {
	UserID         int64
	WorkspaceID    int64
	CurrentRunID   int64
	ConversationID int64
	Model          string
	Turns          []domain.Turn
	Thinking       *domain.ThinkingConfig
	AgentPrompt    string
}

// ChatService owns the app-level chat entry points.
type ChatService interface {
	StreamSimple(ctx context.Context, req SimpleChatRequest) (<-chan domain.ClientEvent, error)
	StreamAgent(ctx context.Context, req AgentChatRequest) (<-chan domain.ClientEvent, error)
}

type chatService struct {
	simple *orchestrator.SimpleChatOrchestrator
	agent  orchestrator.AgentChatOrchestrator
}

type providerChatBackend struct {
	catalog CatalogService
}

// NewChatService creates the app-level chat service for simple and agent chat.
func NewChatService(catalog CatalogService, toolCatalog orchestrator.ToolCatalog, executor orchestrator.ToolExecutor, approvals approval.Broker) ChatService {
	backend := &providerChatBackend{catalog: catalog}

	return &chatService{
		simple: orchestrator.NewSimpleChatOrchestrator(backend),
		agent:  orchestrator.NewAgentChatOrchestrator(backend, toolCatalog, executor, approvals),
	}
}

func (s *chatService) StreamSimple(ctx context.Context, req SimpleChatRequest) (<-chan domain.ClientEvent, error) {
	return s.simple.Stream(ctx, domain.ChatRequest{
		Model:    req.Model,
		Turns:    req.Turns,
		Thinking: req.Thinking,
	})
}

func (s *chatService) StreamAgent(ctx context.Context, req AgentChatRequest) (<-chan domain.ClientEvent, error) {
	return s.agent.Stream(ctx, orchestrator.AgentChatRequest{
		UserID:         req.UserID,
		WorkspaceID:    req.WorkspaceID,
		CurrentRunID:   req.CurrentRunID,
		ConversationID: req.ConversationID,
		Model:          req.Model,
		Turns:          req.Turns,
		Thinking:       req.Thinking,
		AgentPrompt:    req.AgentPrompt,
	})
}

func (s *providerChatBackend) ChatStream(ctx context.Context, req domain.ChatRequest) (<-chan domain.ProviderEvent, error) {
	model, err := s.catalog.FindModel(req.Model)
	if err != nil {
		return nil, err
	}
	if err := domain.ValidateThinkingRequest(model, req); err != nil {
		return nil, err
	}

	target, err := s.catalog.ResolveChatTarget(ctx, req.Model)
	if err != nil {
		return nil, err
	}

	return target.Adapter.Stream(ctx, target.Creds, aiprovider.StreamRequest{
		Model:       req.Model,
		Turns:       req.Turns,
		Thinking:    req.Thinking,
		WorkspaceID: req.WorkspaceID,
		Tools:       req.Tools,
	})
}

func (s *providerChatBackend) FindModel(modelID string) (domain.Model, error) {
	return s.catalog.FindModel(modelID)
}
