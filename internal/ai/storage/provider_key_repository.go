package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// ProviderKeyRepository stores each user's AI provider keys. It only ever
// sees them encrypted.
type ProviderKeyRepository struct {
	db *sql.DB
}

// NewProviderKeyRepository creates a SQLite-backed provider key repository.
func NewProviderKeyRepository(db *sql.DB) *ProviderKeyRepository {
	return &ProviderKeyRepository{db: db}
}

func (r *ProviderKeyRepository) GetKey(ctx context.Context, userID int64, providerID string) (string, error) {
	var key string
	err := r.db.QueryRowContext(ctx,
		`SELECT api_key FROM user_ai_provider_keys WHERE user_id = ? AND provider_id = ?`,
		userID, providerID,
	).Scan(&key)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("querying provider key: %w", err)
	}
	return key, nil
}

func (r *ProviderKeyRepository) ListKeys(ctx context.Context, userID int64) (map[string]string, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT provider_id, api_key FROM user_ai_provider_keys WHERE user_id = ?`, userID,
	)
	if err != nil {
		return nil, fmt.Errorf("querying provider keys: %w", err)
	}
	defer rows.Close()

	keys := make(map[string]string)
	for rows.Next() {
		var providerID, key string
		if err := rows.Scan(&providerID, &key); err != nil {
			return nil, fmt.Errorf("scanning provider key: %w", err)
		}
		keys[providerID] = key
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating provider keys: %w", err)
	}
	return keys, nil
}

func (r *ProviderKeyRepository) SetKey(ctx context.Context, userID int64, providerID, key string) error {
	if _, err := r.db.ExecContext(ctx,
		`INSERT INTO user_ai_provider_keys (user_id, provider_id, api_key, updated_at)
		 VALUES (?, ?, ?, ?)
		 ON CONFLICT(user_id, provider_id) DO UPDATE SET api_key = excluded.api_key, updated_at = excluded.updated_at`,
		userID, providerID, key, time.Now().UTC(),
	); err != nil {
		return fmt.Errorf("saving provider key: %w", err)
	}
	return nil
}

func (r *ProviderKeyRepository) DeleteKey(ctx context.Context, userID int64, providerID string) error {
	if _, err := r.db.ExecContext(ctx,
		`DELETE FROM user_ai_provider_keys WHERE user_id = ? AND provider_id = ?`, userID, providerID,
	); err != nil {
		return fmt.Errorf("deleting provider key: %w", err)
	}
	return nil
}
