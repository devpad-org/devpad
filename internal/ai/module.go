package ai

import (
	"database/sql"

	"github.com/devpad-org/devpad/internal/ai/app"
	"github.com/devpad-org/devpad/internal/ai/approval"
	aiprovider "github.com/devpad-org/devpad/internal/ai/provider"
	"github.com/devpad-org/devpad/internal/ai/provider/anthropic"
	"github.com/devpad-org/devpad/internal/ai/provider/minimax"
	"github.com/devpad-org/devpad/internal/ai/provider/mistral"
	"github.com/devpad-org/devpad/internal/ai/provider/moonshot"
	"github.com/devpad-org/devpad/internal/ai/provider/openaichat"
	"github.com/devpad-org/devpad/internal/ai/provider/openairesponses"
	"github.com/devpad-org/devpad/internal/ai/storage"
	aitools "github.com/devpad-org/devpad/internal/ai/tools"
	httptransport "github.com/devpad-org/devpad/internal/ai/transport/http"
)

// Module bundles the composed AI dependencies used by the server.
type Module struct {
	CatalogService      app.CatalogService
	ChatService         app.ChatService
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
		openaichat.NewAdapter(),
		openairesponses.NewAdapter(),
	)

	catalogService := app.NewCatalogService(providerConfigs, registry)
	conversationService := app.NewConversationService(conversations)
	toolExecutor := aitools.NewWorkspaceExecutor(workspaceOps)
	approvalBroker := approval.NewMemoryBroker()
	chatService := app.NewChatService(catalogService, aitools.NewCatalog(), toolExecutor, approvalBroker)

	return &Module{
		CatalogService:      catalogService,
		ChatService:         chatService,
		ConversationService: conversationService,
		ToolExecutor:        toolExecutor,
		Handler:             httptransport.NewHandler(catalogService, chatService, conversationService, approvalBroker),
	}
}
