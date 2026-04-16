package preview

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"time"
)

// Repository defines data access for preview tokens.
type Repository interface {
	Create(ctx context.Context, token *Token) error
	GetByToken(ctx context.Context, token string) (*Token, error)
	MarkUsed(ctx context.Context, id int64) error
	DeleteExpired(ctx context.Context) error
}

type repository struct {
	db *sql.DB
}

// NewRepository creates a new preview Repository backed by SQLite.
func NewRepository(db *sql.DB) Repository {
	return &repository{db: db}
}

func (r *repository) Create(ctx context.Context, t *Token) error {
	raw, err := generatePreviewToken()
	if err != nil {
		return fmt.Errorf("generating preview token: %w", err)
	}

	t.Token = raw
	t.CreatedAt = time.Now()

	result, err := r.db.ExecContext(ctx,
		`INSERT INTO preview_tokens (token, user_id, workspace_id, port, used, expires_at, created_at)
		 VALUES (?, ?, ?, ?, 0, ?, ?)`,
		t.Token, t.UserID, t.WorkspaceID, t.Port, t.ExpiresAt, t.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("inserting preview token: %w", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return fmt.Errorf("getting inserted id: %w", err)
	}

	t.ID = id
	return nil
}

func (r *repository) GetByToken(ctx context.Context, token string) (*Token, error) {
	t := &Token{}
	var used int
	err := r.db.QueryRowContext(ctx,
		`SELECT id, token, user_id, workspace_id, port, used, expires_at, created_at
		 FROM preview_tokens
		 WHERE token = ? AND expires_at > ?`,
		token, time.Now(),
	).Scan(&t.ID, &t.Token, &t.UserID, &t.WorkspaceID, &t.Port, &used, &t.ExpiresAt, &t.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("querying preview token: %w", err)
	}
	t.Used = used != 0
	return t, nil
}

func (r *repository) MarkUsed(ctx context.Context, id int64) error {
	_, err := r.db.ExecContext(ctx, `UPDATE preview_tokens SET used = 1 WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("marking preview token used: %w", err)
	}
	return nil
}

func (r *repository) DeleteExpired(ctx context.Context) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM preview_tokens WHERE expires_at <= ?`, time.Now())
	if err != nil {
		return fmt.Errorf("deleting expired preview tokens: %w", err)
	}
	return nil
}

func generatePreviewToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
