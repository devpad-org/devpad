package storage

import (
	"context"
	"testing"

	"github.com/devpad-org/devpad/internal/ai/domain"
)

func TestUserAISettingsRepositoryStartsWithDefaultsAndSavesPerUser(t *testing.T) {
	db := openMigratedTestDB(t)
	alice := insertAgentTestUser(t, db, "alice", "alice@example.com")
	bob := insertAgentTestUser(t, db, "bob", "bob@example.com")
	repo := NewUserAISettingsRepository(db.Conn())
	ctx := context.Background()

	defaults := domain.BudgetDefaults{TaskUSD: 20, ReviewUSD: 5}
	if got, err := repo.GetBudgetDefaults(ctx, alice); err != nil || got != defaults {
		t.Fatalf("expected the table's defaults %+v, got %+v %v", defaults, got, err)
	}

	want := domain.BudgetDefaults{TaskUSD: 12.5, ReviewUSD: 0, AssistantUSD: 1}
	if err := repo.SetBudgetDefaults(ctx, alice, want); err != nil {
		t.Fatalf("set: %v", err)
	}
	if err := repo.SetSummaryModel(ctx, alice, "cheap"); err != nil {
		t.Fatalf("set summary model: %v", err)
	}
	if got, err := repo.GetBudgetDefaults(ctx, alice); err != nil || got != want {
		t.Fatalf("expected %+v kept beside the summary model, got %+v %v", want, got, err)
	}
	if got, err := repo.GetSummaryModel(ctx, alice); err != nil || got != "cheap" {
		t.Fatalf("expected summary model cheap, got %q %v", got, err)
	}

	if got, err := repo.GetBudgetDefaults(ctx, bob); err != nil || got != defaults {
		t.Fatalf("expected bob to keep the defaults, got %+v %v", got, err)
	}
	if err := repo.SetSummaryModel(ctx, bob, "other"); err != nil {
		t.Fatalf("set bob's summary model: %v", err)
	}
	if got, err := repo.GetBudgetDefaults(ctx, bob); err != nil || got != defaults {
		t.Fatalf("expected saving a summary model to keep bob's default budgets, got %+v %v", got, err)
	}
}

func TestProviderKeyRepositoryScopesKeysToTheirUser(t *testing.T) {
	db := openMigratedTestDB(t)
	alice := insertAgentTestUser(t, db, "alice", "alice@example.com")
	bob := insertAgentTestUser(t, db, "bob", "bob@example.com")
	repo := NewProviderKeyRepository(db.Conn())
	ctx := context.Background()

	if err := repo.SetKey(ctx, alice, "anthropic", "sealed-a"); err != nil {
		t.Fatalf("set key: %v", err)
	}
	if err := repo.SetKey(ctx, alice, "anthropic", "sealed-b"); err != nil {
		t.Fatalf("replace key: %v", err)
	}
	if got, err := repo.GetKey(ctx, alice, "anthropic"); err != nil || got != "sealed-b" {
		t.Fatalf("expected alice's replaced key, got %q %v", got, err)
	}
	if got, err := repo.GetKey(ctx, bob, "anthropic"); err != nil || got != "" {
		t.Fatalf("expected bob to have no key, got %q %v", got, err)
	}
	keys, err := repo.ListKeys(ctx, alice)
	if err != nil || len(keys) != 1 || keys["anthropic"] != "sealed-b" {
		t.Fatalf("expected alice's one key, got %v %v", keys, err)
	}

	if _, err := db.Conn().Exec(`DELETE FROM users WHERE id = ?`, alice); err != nil {
		t.Fatalf("delete user: %v", err)
	}
	if keys, err := repo.ListKeys(ctx, alice); err != nil || len(keys) != 0 {
		t.Fatalf("expected a deleted user's keys to go with them, got %v %v", keys, err)
	}
}
