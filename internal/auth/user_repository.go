package auth

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// UserRepository defines data access for users.
type UserRepository interface {
	Create(ctx context.Context, user *User) error
	GetByUsername(ctx context.Context, username string) (*User, error)
	GetByID(ctx context.Context, id int64) (*User, error)
	Count(ctx context.Context) (int, error)
}

type userRepository struct {
	db *sql.DB
}

// NewUserRepository creates a new UserRepository backed by SQLite.
func NewUserRepository(db *sql.DB) UserRepository {
	return &userRepository{db: db}
}

func (r *userRepository) Create(ctx context.Context, user *User) error {
	now := time.Now()
	result, err := r.db.ExecContext(ctx,
		`INSERT INTO users (username, email, password, is_admin, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)`,
		user.Username, user.Email, user.Password, user.IsAdmin, now, now,
	)
	if err != nil {
		return fmt.Errorf("inserting user: %w", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return fmt.Errorf("getting inserted id: %w", err)
	}

	user.ID = id
	user.CreatedAt = now
	user.UpdatedAt = now
	return nil
}

func (r *userRepository) GetByUsername(ctx context.Context, username string) (*User, error) {
	user := &User{}
	err := r.db.QueryRowContext(ctx,
		`SELECT id, username, email, password, is_admin, created_at, updated_at FROM users WHERE username = ?`,
		username,
	).Scan(&user.ID, &user.Username, &user.Email, &user.Password, &user.IsAdmin, &user.CreatedAt, &user.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("querying user by username: %w", err)
	}
	return user, nil
}

func (r *userRepository) GetByID(ctx context.Context, id int64) (*User, error) {
	user := &User{}
	err := r.db.QueryRowContext(ctx,
		`SELECT id, username, email, password, is_admin, created_at, updated_at FROM users WHERE id = ?`,
		id,
	).Scan(&user.ID, &user.Username, &user.Email, &user.Password, &user.IsAdmin, &user.CreatedAt, &user.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("querying user by id: %w", err)
	}
	return user, nil
}

func (r *userRepository) Count(ctx context.Context) (int, error) {
	var count int
	err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("counting users: %w", err)
	}
	return count, nil
}
