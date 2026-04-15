package auth

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"time"
)

// SessionRepository defines data access for sessions.
type SessionRepository interface {
	Create(ctx context.Context, session *Session) error
	GetByToken(ctx context.Context, token string) (*Session, error)
	DeleteByToken(ctx context.Context, token string) error
	DeleteExpired(ctx context.Context) error
}

type sessionRepository struct {
	db *sql.DB
}

// NewSessionRepository creates a new SessionRepository backed by SQLite.
func NewSessionRepository(db *sql.DB) SessionRepository {
	return &sessionRepository{db: db}
}

func (r *sessionRepository) Create(ctx context.Context, session *Session) error {
	token, err := generateToken()
	if err != nil {
		return fmt.Errorf("generating session token: %w", err)
	}

	session.Token = token
	session.CreatedAt = time.Now()

	result, err := r.db.ExecContext(ctx,
		`INSERT INTO sessions (user_id, token, expires_at, created_at) VALUES (?, ?, ?, ?)`,
		session.UserID, session.Token, session.ExpiresAt, session.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("inserting session: %w", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return fmt.Errorf("getting inserted id: %w", err)
	}

	session.ID = id
	return nil
}

func (r *sessionRepository) GetByToken(ctx context.Context, token string) (*Session, error) {
	session := &Session{}
	err := r.db.QueryRowContext(ctx,
		`SELECT id, user_id, token, expires_at, created_at FROM sessions WHERE token = ? AND expires_at > ?`,
		token, time.Now(),
	).Scan(&session.ID, &session.UserID, &session.Token, &session.ExpiresAt, &session.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("querying session: %w", err)
	}
	return session, nil
}

func (r *sessionRepository) DeleteByToken(ctx context.Context, token string) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM sessions WHERE token = ?`, token)
	if err != nil {
		return fmt.Errorf("deleting session: %w", err)
	}
	return nil
}

func (r *sessionRepository) DeleteExpired(ctx context.Context) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at <= ?`, time.Now())
	if err != nil {
		return fmt.Errorf("deleting expired sessions: %w", err)
	}
	return nil
}

func generateToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
