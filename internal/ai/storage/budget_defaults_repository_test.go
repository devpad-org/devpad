package storage

import (
	"context"
	"testing"

	"github.com/devpad-org/devpad/internal/ai/domain"
	appdb "github.com/devpad-org/devpad/internal/database"
)

func TestBudgetDefaultsRepositoryStartsWithDefaultsAndSaves(t *testing.T) {
	db, err := appdb.Open(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.Migrate(); err != nil {
		t.Fatalf("migrate db: %v", err)
	}
	repo := NewBudgetDefaultsRepository(db.Conn())
	ctx := context.Background()

	got, err := repo.GetBudgetDefaults(ctx)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if want := (domain.BudgetDefaults{TaskUSD: 20, ReviewUSD: 5}); got != want {
		t.Fatalf("expected the migration's defaults %+v, got %+v", want, got)
	}

	want := domain.BudgetDefaults{TaskUSD: 12.5, ReviewUSD: 0, AssistantUSD: 1}
	if err := repo.SetBudgetDefaults(ctx, want); err != nil {
		t.Fatalf("set: %v", err)
	}
	if got, err := repo.GetBudgetDefaults(ctx); err != nil || got != want {
		t.Fatalf("expected %+v, got %+v %v", want, got, err)
	}
}
