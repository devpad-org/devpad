package app

import (
	"context"
	"errors"
	"testing"

	"github.com/devpad-org/devpad/internal/ai/domain"
)

type memorySummaryModels map[int64]string

func (r memorySummaryModels) GetSummaryModel(_ context.Context, userID int64) (string, error) {
	return r[userID], nil
}

func (r memorySummaryModels) SetSummaryModel(_ context.Context, userID int64, model string) error {
	r[userID] = model
	return nil
}

func TestSummaryModelServiceFallsBackToTheCheapestConfiguredModel(t *testing.T) {
	keys := memoryProviderKeys{}
	catalog := newTwoProviderCatalog(t, keys, newTestCipher(t, "a"))
	repo := memorySummaryModels{}
	service := NewSummaryModelService(repo, catalog)
	ctx := context.Background()

	if _, err := service.ResolveSummaryModel(ctx, 1); !errors.Is(err, domain.ErrNoAPIKey) {
		t.Fatalf("expected no summary model without keys, got %v", err)
	}
	if err := catalog.SetProviderKey(ctx, 1, "anthropic", "k"); err != nil {
		t.Fatalf("set key: %v", err)
	}
	if got, err := service.ResolveSummaryModel(ctx, 1); err != nil || got != "small" {
		t.Fatalf("expected automatic to pick small, got %q %v", got, err)
	}

	if got, err := service.SetSummaryModel(ctx, 1, " big "); err != nil || got != "big" {
		t.Fatalf("set summary model: %q %v", got, err)
	}
	if got, err := service.ResolveSummaryModel(ctx, 1); err != nil || got != "big" {
		t.Fatalf("expected the chosen model, got %q %v", got, err)
	}

	// A chosen model the user lost the key for gives way to one they can use.
	if _, err := service.SetSummaryModel(ctx, 1, "tiny"); err != nil {
		t.Fatalf("set summary model: %v", err)
	}
	if got, err := service.ResolveSummaryModel(ctx, 1); err != nil || got != "small" {
		t.Fatalf("expected the cheapest usable model when the chosen one has no key, got %q %v", got, err)
	}

	if _, err := service.SetSummaryModel(ctx, 1, "nope"); !errors.Is(err, domain.ErrModelNotFound) {
		t.Fatalf("expected an unknown model refused, got %v", err)
	}
	repo[1] = "retired"
	if got, err := service.SummaryModel(ctx, 1); err != nil || got != "" {
		t.Fatalf("expected a model devpad no longer offers to read as automatic, got %q %v", got, err)
	}
}
