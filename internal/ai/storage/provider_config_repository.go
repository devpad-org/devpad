package storage

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/devpad-org/devpad/internal/ai/app"
)

// ProviderConfigRepository stores AI provider configuration.
type ProviderConfigRepository struct {
	db *sql.DB
}

// NewProviderConfigRepository creates a new AI config repository.
func NewProviderConfigRepository(db *sql.DB) *ProviderConfigRepository {
	return &ProviderConfigRepository{db: db}
}

func (r *ProviderConfigRepository) GetConfig(ctx context.Context, providerID string) (*app.ProviderConfig, error) {
	row := r.db.QueryRowContext(ctx,
		`SELECT id, api_key, enabled, created_at, updated_at FROM ai_providers WHERE id = ?`,
		providerID,
	)

	var cfg app.ProviderConfig
	err := row.Scan(&cfg.ID, &cfg.APIKey, &cfg.Enabled, &cfg.CreatedAt, &cfg.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("scanning provider config: %w", err)
	}

	return &cfg, nil
}

func (r *ProviderConfigRepository) ListConfigs(ctx context.Context) ([]app.ProviderConfig, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, api_key, enabled, created_at, updated_at FROM ai_providers ORDER BY id`,
	)
	if err != nil {
		return nil, fmt.Errorf("querying provider configs: %w", err)
	}
	defer rows.Close()

	var configs []app.ProviderConfig
	for rows.Next() {
		var cfg app.ProviderConfig
		if err := rows.Scan(&cfg.ID, &cfg.APIKey, &cfg.Enabled, &cfg.CreatedAt, &cfg.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scanning provider config: %w", err)
		}
		configs = append(configs, cfg)
	}

	return configs, rows.Err()
}

func (r *ProviderConfigRepository) UpsertConfig(ctx context.Context, cfg *app.ProviderConfig) error {
	now := time.Now().UTC()
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO ai_providers (id, api_key, enabled, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(id) DO UPDATE SET api_key = excluded.api_key, enabled = excluded.enabled, updated_at = excluded.updated_at`,
		cfg.ID, cfg.APIKey, cfg.Enabled, now, now,
	)
	if err != nil {
		return fmt.Errorf("upserting provider config: %w", err)
	}

	return nil
}
