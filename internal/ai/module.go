package ai

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/devpad-org/devpad/internal/ai/app"
	"github.com/devpad-org/devpad/internal/ai/approval"
	"github.com/devpad-org/devpad/internal/ai/domain"
	"github.com/devpad-org/devpad/internal/ai/filesummary"
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
	AgentService        app.AgentService
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
	agents := storage.NewAgentRepository(db)
	registry := aiprovider.NewRegistry(
		anthropic.NewAdapter(),
		mistral.NewAdapter(),
		minimax.NewAdapter(),
		moonshot.NewAdapter(),
		openairesponses.NewAdapter(),
	)

	catalogService := app.NewCatalogService(providerConfigs, registry)
	agentService := app.NewAgentService(agents)
	conversationService := app.NewConversationService(conversations, catalogService)
	toolExecutor := aitools.NewWorkspaceExecutor(workspaceOps)
	approvalBroker := approval.NewMemoryBroker()
	chatService := app.NewChatService(catalogService, aitools.NewCatalog(), toolExecutor, approvalBroker, app.NewAGENTSInstructionSource(workspaceOps))
	toolExecutor.SetFileSummarizer(filesummary.NewChatSummarizer(chatService))
	agentRuns := storage.NewAgentRunRepository(db)
	agentRunService := app.NewAgentRunService(context.Background(), agentRuns, chatService, conversationService).WithAgentService(agentService)
	toolExecutor.SetChildAgentRunner(childAgentRunStarter{runs: agentRunService})
	toolExecutor.SetAgentLister(agentService)

	return &Module{
		CatalogService:      catalogService,
		AgentService:        agentService,
		ChatService:         chatService,
		AgentRunService:     agentRunService,
		ConversationService: conversationService,
		ToolExecutor:        toolExecutor,
		Handler:             httptransport.NewHandler(catalogService, agentService, chatService, agentRunService, conversationService, approvalBroker),
	}
}

type childAgentRunStarter struct {
	runs app.AgentRunService
}

func (s childAgentRunStarter) StartChildAgentRun(ctx context.Context, req aitools.ChildAgentRunRequest) (*aitools.ChildAgentRun, error) {
	run, err := s.runs.StartChildRun(ctx, req.ParentRunID, app.StartAgentRunRequest{
		UserID:         req.UserID,
		WorkspaceID:    req.WorkspaceID,
		ConversationID: req.ConversationID,
		Model:          req.Model,
		AgentID:        req.AgentID,
		Turns:          []domain.Turn{domain.NewTextTurn(domain.RoleUser, req.Prompt)},
		Thinking:       req.Thinking,
	})
	if err != nil {
		return nil, fmt.Errorf("starting child agent run: %w", err)
	}
	return &aitools.ChildAgentRun{
		ID:             run.ID,
		ParentRunID:    run.ParentRunID,
		WorkspaceID:    run.WorkspaceID,
		ConversationID: run.ConversationID,
		AgentID:        run.AgentID,
		Model:          run.Model,
		Status:         string(run.Status),
	}, nil
}

func (s childAgentRunStarter) WaitChildAgentRun(ctx context.Context, userID, parentRunID, runID int64) (*aitools.ChildAgentRunResult, error) {
	run, err := s.runs.GetRun(ctx, userID, runID)
	if err != nil {
		return nil, fmt.Errorf("getting child agent run: %w", err)
	}
	if run.ParentRunID != parentRunID {
		return nil, domain.ErrAgentRunNotFound
	}
	if domain.AgentRunStatusTerminal(run.Status) {
		events, err := s.runs.ListEvents(ctx, userID, runID, 0)
		if err != nil {
			return nil, fmt.Errorf("listing child agent run events: %w", err)
		}
		return childAgentResultFromEvents(runID, string(run.Status), run.Error, events), nil
	}

	stream, err := s.runs.SubscribeEvents(ctx, userID, runID, 0)
	if err != nil {
		return nil, fmt.Errorf("subscribing to child agent run: %w", err)
	}

	var summary strings.Builder
	errorMessage := ""
	for event := range stream {
		if event.Event.TextDelta != "" {
			summary.WriteString(event.Event.TextDelta)
		}
		if event.Event.ErrorMessage != "" {
			errorMessage = event.Event.ErrorMessage
		}
		if event.Event.Done {
			status := string(domain.AgentRunCompleted)
			if errorMessage != "" {
				status = string(domain.AgentRunFailed)
			}
			if run, err := s.runs.GetRun(ctx, userID, runID); err == nil && run != nil && domain.AgentRunStatusTerminal(run.Status) {
				status = string(run.Status)
				if errorMessage == "" {
					errorMessage = run.Error
				}
			}
			return &aitools.ChildAgentRunResult{
				RunID:   runID,
				Status:  status,
				Summary: strings.TrimSpace(summary.String()),
				Error:   errorMessage,
			}, nil
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	run, err = s.runs.GetRun(ctx, userID, runID)
	if err == nil && run != nil && domain.AgentRunStatusTerminal(run.Status) {
		events, listErr := s.runs.ListEvents(ctx, userID, runID, 0)
		if listErr != nil {
			return nil, fmt.Errorf("listing child agent run events: %w", listErr)
		}
		return childAgentResultFromEvents(runID, string(run.Status), run.Error, events), nil
	}
	if err != nil {
		return nil, fmt.Errorf("getting child agent run after event stream closed: %w", err)
	}
	return nil, fmt.Errorf("child agent run event stream closed before completion")
}

func childAgentResultFromEvents(runID int64, status, runError string, events []domain.AgentRunEvent) *aitools.ChildAgentRunResult {
	var summary strings.Builder
	errorMessage := runError
	for _, event := range events {
		if event.Event.TextDelta != "" {
			summary.WriteString(event.Event.TextDelta)
		}
		if event.Event.ErrorMessage != "" {
			errorMessage = event.Event.ErrorMessage
		}
	}

	return &aitools.ChildAgentRunResult{
		RunID:   runID,
		Status:  status,
		Summary: strings.TrimSpace(summary.String()),
		Error:   errorMessage,
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
