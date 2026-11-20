package orchestrator

import (
	"github.com/devpad-org/devpad/internal/ai/attachment"
	aicontext "github.com/devpad-org/devpad/internal/ai/context"
	"github.com/devpad-org/devpad/internal/ai/domain"
)

func contextSizeForRequest(model domain.Model, req domain.ChatRequest) *domain.ContextSize {
	// Count the images as the provider gets them: only those the model is
	// sent, and notes for the rest.
	req.Turns = attachment.ForModel(model, req.Turns)
	estimate := aicontext.EstimateChatRequest(model, req)
	budget := aicontext.BudgetFor(estimate.ProviderID, estimate.ModelID)
	percentage := aicontext.PercentageUsed(estimate.TotalTokens, budget.InputTokens)

	return &domain.ContextSize{
		Approximate:                true,
		ProviderID:                 estimate.ProviderID,
		Model:                      estimate.ModelID,
		NextRequestTokens:          estimate.TotalTokens,
		TotalTranscriptTokens:      estimate.TotalTokens,
		ProviderFacingTokens:       estimate.TotalTokens,
		InputBudgetTokens:          budget.InputTokens,
		PercentageUsed:             percentage,
		WarningThresholdPercentage: budget.WarningThresholdPercentage,
		Warning:                    percentage >= float64(budget.WarningThresholdPercentage),
	}
}
