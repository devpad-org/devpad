package app

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/devpad-org/devpad/internal/ai/domain"
)

// SummaryModelRepository persists the model each user chose for
// summarize_file. An empty model means automatic.
type SummaryModelRepository interface {
	GetSummaryModel(ctx context.Context, userID int64) (string, error)
	SetSummaryModel(ctx context.Context, userID int64, model string) error
}

// SummaryCatalog is what choosing a summary model needs of the catalog.
type SummaryCatalog interface {
	FindModel(modelID string) (domain.Model, error)
	ResolveChatTarget(ctx context.Context, userID int64, modelID string) (ResolvedTarget, error)
	CheapestConfiguredModel(ctx context.Context, userID int64) (domain.Model, error)
}

// SummaryModelService manages the model summarize_file uses for each user.
type SummaryModelService interface {
	// SummaryModel returns the model the user chose, or "" for automatic,
	// which is also what a choice devpad no longer offers reads as.
	SummaryModel(ctx context.Context, userID int64) (string, error)
	// SetSummaryModel saves the user's choice; "" means automatic. The
	// user need not have a key for the model's provider yet.
	SetSummaryModel(ctx context.Context, userID int64, model string) (string, error)
	// ResolveSummaryModel returns the model to summarize with: the user's
	// choice while they have a key for its provider, otherwise the cheapest
	// priced model they have a key for. It fails with domain.ErrNoAPIKey
	// when there is none.
	ResolveSummaryModel(ctx context.Context, userID int64) (string, error)
}

type summaryModelService struct {
	repo    SummaryModelRepository
	catalog SummaryCatalog
}

// NewSummaryModelService creates the app service for users' summary models.
func NewSummaryModelService(repo SummaryModelRepository, catalog SummaryCatalog) SummaryModelService {
	return &summaryModelService{repo: repo, catalog: catalog}
}

func (s *summaryModelService) SummaryModel(ctx context.Context, userID int64) (string, error) {
	model, err := s.repo.GetSummaryModel(ctx, userID)
	if err != nil {
		return "", fmt.Errorf("loading summary model: %w", err)
	}
	if model == "" {
		return "", nil
	}
	if _, err := s.catalog.FindModel(model); err != nil {
		return "", nil
	}
	return model, nil
}

func (s *summaryModelService) SetSummaryModel(ctx context.Context, userID int64, model string) (string, error) {
	model = strings.TrimSpace(model)
	if model != "" {
		if _, err := s.catalog.FindModel(model); err != nil {
			return "", fmt.Errorf("%w: %s", domain.ErrModelNotFound, model)
		}
	}
	if err := s.repo.SetSummaryModel(ctx, userID, model); err != nil {
		return "", fmt.Errorf("saving summary model: %w", err)
	}
	return model, nil
}

func (s *summaryModelService) ResolveSummaryModel(ctx context.Context, userID int64) (string, error) {
	chosen, err := s.SummaryModel(ctx, userID)
	if err != nil {
		return "", err
	}
	if chosen != "" {
		_, err := s.catalog.ResolveChatTarget(ctx, userID, chosen)
		if err == nil {
			return chosen, nil
		}
		if !errors.Is(err, domain.ErrNoAPIKey) && !errors.Is(err, domain.ErrAPIKeyUnreadable) {
			return "", fmt.Errorf("checking the summary model's provider: %w", err)
		}
	}
	cheapest, err := s.catalog.CheapestConfiguredModel(ctx, userID)
	if err != nil {
		return "", err
	}
	return cheapest.ID, nil
}
