package storage

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/devpad-org/devpad/internal/ai/domain"
)

// BudgetDefaultsRepository stores the limits an admin set on what agents
// may spend.
type BudgetDefaultsRepository struct {
	db *sql.DB
}

// NewBudgetDefaultsRepository creates a SQLite-backed budget defaults
// repository.
func NewBudgetDefaultsRepository(db *sql.DB) *BudgetDefaultsRepository {
	return &BudgetDefaultsRepository{db: db}
}

func (r *BudgetDefaultsRepository) GetBudgetDefaults(ctx context.Context) (domain.BudgetDefaults, error) {
	var b domain.BudgetDefaults
	err := r.db.QueryRowContext(ctx,
		`SELECT task_usd, review_usd, assistant_usd FROM ai_budget_defaults WHERE id = 1`,
	).Scan(&b.TaskUSD, &b.ReviewUSD, &b.AssistantUSD)
	if err != nil {
		return domain.BudgetDefaults{}, fmt.Errorf("querying budget defaults: %w", err)
	}
	return b, nil
}

func (r *BudgetDefaultsRepository) SetBudgetDefaults(ctx context.Context, b domain.BudgetDefaults) error {
	if _, err := r.db.ExecContext(ctx,
		`INSERT INTO ai_budget_defaults (id, task_usd, review_usd, assistant_usd, updated_at)
		 VALUES (1, ?, ?, ?, ?)
		 ON CONFLICT(id) DO UPDATE SET task_usd = excluded.task_usd, review_usd = excluded.review_usd,
		     assistant_usd = excluded.assistant_usd, updated_at = excluded.updated_at`,
		b.TaskUSD, b.ReviewUSD, b.AssistantUSD, time.Now().UTC(),
	); err != nil {
		return fmt.Errorf("upserting budget defaults: %w", err)
	}
	return nil
}
