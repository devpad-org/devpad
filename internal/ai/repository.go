package ai

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// ProviderConfig holds the stored configuration for an AI provider.
type ProviderConfig struct {
	ID        string    `json:"id"`
	APIKey    string    `json:"apiKey,omitempty"`
	Enabled   bool      `json:"enabled"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// Repository handles persistence of AI provider configuration.
type Repository interface {
	GetConfig(ctx context.Context, providerID string) (*ProviderConfig, error)
	ListConfigs(ctx context.Context) ([]ProviderConfig, error)
	UpsertConfig(ctx context.Context, cfg *ProviderConfig) error
}

type repository struct {
	db *sql.DB
}

// NewRepository creates a new AI config repository.
func NewRepository(db *sql.DB) Repository {
	return &repository{db: db}
}

func (r *repository) GetConfig(ctx context.Context, providerID string) (*ProviderConfig, error) {
	row := r.db.QueryRowContext(ctx,
		`SELECT id, api_key, enabled, created_at, updated_at FROM ai_providers WHERE id = ?`,
		providerID,
	)

	var cfg ProviderConfig
	err := row.Scan(&cfg.ID, &cfg.APIKey, &cfg.Enabled, &cfg.CreatedAt, &cfg.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("scanning provider config: %w", err)
	}
	return &cfg, nil
}

func (r *repository) ListConfigs(ctx context.Context) ([]ProviderConfig, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, api_key, enabled, created_at, updated_at FROM ai_providers ORDER BY id`,
	)
	if err != nil {
		return nil, fmt.Errorf("querying provider configs: %w", err)
	}
	defer rows.Close()

	var configs []ProviderConfig
	for rows.Next() {
		var cfg ProviderConfig
		if err := rows.Scan(&cfg.ID, &cfg.APIKey, &cfg.Enabled, &cfg.CreatedAt, &cfg.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scanning provider config: %w", err)
		}
		configs = append(configs, cfg)
	}
	return configs, rows.Err()
}

func (r *repository) UpsertConfig(ctx context.Context, cfg *ProviderConfig) error {
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
