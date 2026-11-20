package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/devpad-org/devpad/internal/ai/domain"
)

// UserAISettingsRepository stores each user's limits on what their agents
// may spend and the model summarize_file uses for them. A user who never
// saved any has the table's defaults.
type UserAISettingsRepository struct {
	db *sql.DB
}

// NewUserAISettingsRepository creates a SQLite-backed user AI settings
// repository.
func NewUserAISettingsRepository(db *sql.DB) *UserAISettingsRepository {
	return &UserAISettingsRepository{db: db}
}

// The defaults of user_ai_settings, for a user without a row.
var defaultBudgets = domain.BudgetDefaults{TaskUSD: 20, ReviewUSD: 5, AssistantUSD: 0}

func (r *UserAISettingsRepository) GetBudgetDefaults(ctx context.Context, userID int64) (domain.BudgetDefaults, error) {
	var b domain.BudgetDefaults
	err := r.db.QueryRowContext(ctx,
		`SELECT task_budget_usd, review_budget_usd, assistant_budget_usd FROM user_ai_settings WHERE user_id = ?`,
		userID,
	).Scan(&b.TaskUSD, &b.ReviewUSD, &b.AssistantUSD)
	if errors.Is(err, sql.ErrNoRows) {
		return defaultBudgets, nil
	}
	if err != nil {
		return domain.BudgetDefaults{}, fmt.Errorf("querying budget defaults: %w", err)
	}
	return b, nil
}

func (r *UserAISettingsRepository) SetBudgetDefaults(ctx context.Context, userID int64, b domain.BudgetDefaults) error {
	if _, err := r.db.ExecContext(ctx,
		`INSERT INTO user_ai_settings (user_id, task_budget_usd, review_budget_usd, assistant_budget_usd, updated_at)
		 VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(user_id) DO UPDATE SET task_budget_usd = excluded.task_budget_usd,
		     review_budget_usd = excluded.review_budget_usd,
		     assistant_budget_usd = excluded.assistant_budget_usd, updated_at = excluded.updated_at`,
		userID, b.TaskUSD, b.ReviewUSD, b.AssistantUSD, time.Now().UTC(),
	); err != nil {
		return fmt.Errorf("saving budget defaults: %w", err)
	}
	return nil
}

func (r *UserAISettingsRepository) GetSummaryModel(ctx context.Context, userID int64) (string, error) {
	var model string
	err := r.db.QueryRowContext(ctx,
		`SELECT summary_model FROM user_ai_settings WHERE user_id = ?`, userID,
	).Scan(&model)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("querying summary model: %w", err)
	}
	return model, nil
}

func (r *UserAISettingsRepository) SetSummaryModel(ctx context.Context, userID int64, model string) error {
	if _, err := r.db.ExecContext(ctx,
		`INSERT INTO user_ai_settings (user_id, summary_model, updated_at) VALUES (?, ?, ?)
		 ON CONFLICT(user_id) DO UPDATE SET summary_model = excluded.summary_model, updated_at = excluded.updated_at`,
		userID, model, time.Now().UTC(),
	); err != nil {
		return fmt.Errorf("saving summary model: %w", err)
	}
	return nil
}
