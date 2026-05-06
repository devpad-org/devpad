package ai

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/devpad-org/devpad/internal/ai/app"
	"github.com/devpad-org/devpad/internal/ai/approval"
	aiprovider "github.com/devpad-org/devpad/internal/ai/provider"
	"github.com/devpad-org/devpad/internal/ai/provider/anthropic"
	"github.com/devpad-org/devpad/internal/ai/provider/minimax"
	"github.com/devpad-org/devpad/internal/ai/provider/mistral"
	"github.com/devpad-org/devpad/internal/ai/provider/moonshot"
	"github.com/devpad-org/devpad/internal/ai/provider/openairesponses"
	"github.com/devpad-org/devpad/internal/ai/storage"
	aitools "github.com/devpad-org/devpad/internal/ai/tools"
	httptransport "github.com/devpad-org/devpad/internal/ai/transport/http"
)

// Module bundles the composed AI dependencies used by the server.
type Module struct {
	CatalogService      app.CatalogService
	ChatService         app.ChatService
	AgentRunService     app.AgentRunService
	ConversationService app.ConversationService
	ToolExecutor        aitools.Executor
	Handler             *httptransport.Handler
}

// NewModule assembles the AI subpackages behind the root composition facade.
func NewModule(db *sql.DB, workspaceOps aitools.WorkspaceOps) *Module {
	providerConfigs := storage.NewProviderConfigRepository(db)
	conversations := storage.NewConversationRepository(db)
	registry := aiprovider.NewRegistry(
		anthropic.NewAdapter(),
		mistral.NewAdapter(),
		minimax.NewAdapter(),
		moonshot.NewAdapter(),
		openairesponses.NewAdapter(),
	)

	catalogService := app.NewCatalogService(providerConfigs, registry)
	conversationService := app.NewConversationService(conversations)
	toolExecutor := aitools.NewWorkspaceExecutor(workspaceOps)
	approvalBroker := approval.NewMemoryBroker()
	chatService := app.NewChatService(catalogService, aitools.NewCatalog(), toolExecutor, approvalBroker)
	agentRuns := storage.NewAgentRunRepository(db)
	agentRunService := app.NewAgentRunService(context.Background(), agentRuns, chatService)

	return &Module{
		CatalogService:      catalogService,
		ChatService:         chatService,
		AgentRunService:     agentRunService,
		ConversationService: conversationService,
		ToolExecutor:        toolExecutor,
		Handler:             httptransport.NewHandler(catalogService, chatService, agentRunService, conversationService, approvalBroker),
	}
}

// Shutdown stops AI background workers owned by the module.
func (m *Module) Shutdown(ctx context.Context) error {
	if m == nil || m.AgentRunService == nil {
		return nil
	}
	if err := m.AgentRunService.Shutdown(ctx); err != nil {
		return fmt.Errorf("shutting down agent runner: %w", err)
	}
	return nil
}
