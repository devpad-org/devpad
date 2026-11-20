package app

import (
	"context"
	"fmt"

	"github.com/devpad-org/devpad/internal/ai/approval"
	"github.com/devpad-org/devpad/internal/ai/attachment"
	"github.com/devpad-org/devpad/internal/ai/domain"
	"github.com/devpad-org/devpad/internal/ai/orchestrator"
	aiprovider "github.com/devpad-org/devpad/internal/ai/provider"
	"github.com/devpad-org/devpad/internal/ai/question"
)

// SimpleChatRequest contains the inputs for non-agent chat.
type SimpleChatRequest struct {
	UserID int64
	// WorkspaceID is the workspace the chat is for, if any, so its usage is
	// counted under the workspace's project.
	WorkspaceID int64
	// RunID is the agent run the chat is part of, if any, such as one a
	// tool call of the run makes, so its usage is counted under the run.
	RunID    int64
	Model    string
	Turns    []domain.Turn
	Thinking *domain.ThinkingConfig
	// Budget, which may be nil, is spent by the chat and refuses it when it
	// is already spent.
	Budget *domain.Budget
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
	// Budget stops the run once spent. Nil means no limit.
	Budget *domain.Budget
}

// WorkspaceChecker refuses a run in a workspace the user does not own or
// that is not running. Tools check again call by call, but only after the
// run has started paying for model requests.
type WorkspaceChecker interface {
	CheckRunning(ctx context.Context, userID, workspaceID int64) error
}

// ChatService owns the app-level chat entry points.
type ChatService interface {
	StreamSimple(ctx context.Context, req SimpleChatRequest) (<-chan domain.ClientEvent, error)
	StreamAgent(ctx context.Context, req AgentChatRequest) (<-chan domain.ClientEvent, error)
}

type chatService struct {
	catalog    CatalogService
	simple     *orchestrator.SimpleChatOrchestrator
	agent      orchestrator.AgentChatOrchestrator
	workspaces WorkspaceChecker
	images     ImageLoader
	usage      UsageRecorder
}

type providerChatBackend struct {
	catalog CatalogService
}

// ChatServiceDeps are what a ChatService is made from.
type ChatServiceDeps struct {
	Catalog     CatalogService
	ToolCatalog orchestrator.ToolCatalog
	Executor    orchestrator.ToolExecutor
	Approvals   approval.Broker
	Questions   question.Broker
	Workspaces  WorkspaceChecker
	// Images loads the images turns refer to; nil sends turns as they are.
	Images ImageLoader
	// Usage records every provider request the chats make; nil records none.
	Usage UsageRecorder
}

// NewChatService creates the app-level chat service for simple and agent
// chat.
func NewChatService(d ChatServiceDeps) ChatService {
	backend := &providerChatBackend{catalog: d.Catalog}

	return &chatService{
		catalog:    d.Catalog,
		simple:     orchestrator.NewSimpleChatOrchestrator(backend),
		agent:      orchestrator.NewAgentChatOrchestrator(backend, d.ToolCatalog, d.Executor, d.Approvals, d.Questions),
		workspaces: d.Workspaces,
		images:     d.Images,
		usage:      d.Usage,
	}
}

func (s *chatService) StreamSimple(ctx context.Context, req SimpleChatRequest) (<-chan domain.ClientEvent, error) {
	turns, err := s.hydrate(ctx, req.UserID, req.Turns)
	if err != nil {
		return nil, err
	}
	return s.simple.Stream(ctx, domain.ChatRequest{
		UserID:   req.UserID,
		Model:    req.Model,
		Turns:    turns,
		Thinking: req.Thinking,
	}, req.Budget, usageRecorderFor(s.usage, domain.UsageRecord{
		UserID:      req.UserID,
		WorkspaceID: req.WorkspaceID,
		RunID:       req.RunID,
		Model:       req.Model,
	}))
}

func (s *chatService) StreamAgent(ctx context.Context, req AgentChatRequest) (<-chan domain.ClientEvent, error) {
	if err := s.workspaces.CheckRunning(ctx, req.UserID, req.WorkspaceID); err != nil {
		return nil, fmt.Errorf("checking the run's workspace: %w", err)
	}
	// A user without a key for the model's provider is refused before the
	// run starts, not by its first request. Each request checks again, so
	// removing the key stops the run.
	if _, err := s.catalog.ResolveChatTarget(ctx, req.UserID, req.Model); err != nil {
		return nil, err
	}
	turns, err := s.hydrate(ctx, req.UserID, req.Turns)
	if err != nil {
		return nil, err
	}
	return s.agent.Stream(ctx, orchestrator.AgentChatRequest{
		UserID:         req.UserID,
		WorkspaceID:    req.WorkspaceID,
		CurrentRunID:   req.CurrentRunID,
		ConversationID: req.ConversationID,
		Model:          req.Model,
		Turns:          turns,
		Thinking:       req.Thinking,
		AgentPrompt:    req.AgentPrompt,
		Budget:         req.Budget,
		RecordUsage: usageRecorderFor(s.usage, domain.UsageRecord{
			UserID:      req.UserID,
			WorkspaceID: req.WorkspaceID,
			RunID:       req.CurrentRunID,
			Model:       req.Model,
		}),
	})
}

// hydrate loads the images the turns refer to, once per run: the turns are
// resent on every model request the run makes.
func (s *chatService) hydrate(ctx context.Context, userID int64, turns []domain.Turn) ([]domain.Turn, error) {
	if s.images == nil {
		return turns, nil
	}
	return s.images.Hydrate(ctx, userID, turns)
}

func (s *providerChatBackend) ChatStream(ctx context.Context, req domain.ChatRequest) (<-chan domain.ProviderEvent, error) {
	model, err := s.catalog.FindModel(req.Model)
	if err != nil {
		return nil, err
	}
	if err := domain.ValidateThinkingRequest(model, req); err != nil {
		return nil, err
	}

	target, err := s.catalog.ResolveChatTarget(ctx, req.UserID, req.Model)
	if err != nil {
		return nil, err
	}

	return target.Adapter.Stream(ctx, target.Creds, aiprovider.StreamRequest{
		Model:       req.Model,
		Turns:       attachment.ForModel(model, req.Turns),
		Thinking:    req.Thinking,
		WorkspaceID: req.WorkspaceID,
		Tools:       req.Tools,
	})
}

func (s *providerChatBackend) FindModel(modelID string) (domain.Model, error) {
	return s.catalog.FindModel(modelID)
}
