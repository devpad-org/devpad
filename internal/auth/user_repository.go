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
	List(ctx context.Context) ([]*User, error)
	Update(ctx context.Context, user *User) error
	Delete(ctx context.Context, id int64) error
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
		`INSERT INTO users (username, email, password, is_admin, totp_secret, totp_enabled, ssh_public_key, ssh_private_key, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		user.Username, user.Email, user.Password, user.IsAdmin, user.TOTPSecret, user.TOTPEnabled, user.SSHPublicKey, user.SSHPrivateKey, now, now,
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
		`SELECT id, username, email, password, is_admin, totp_secret, totp_enabled, ssh_public_key, ssh_private_key, created_at, updated_at FROM users WHERE username = ?`,
		username,
	).Scan(&user.ID, &user.Username, &user.Email, &user.Password, &user.IsAdmin, &user.TOTPSecret, &user.TOTPEnabled, &user.SSHPublicKey, &user.SSHPrivateKey, &user.CreatedAt, &user.UpdatedAt)
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
		`SELECT id, username, email, password, is_admin, totp_secret, totp_enabled, ssh_public_key, ssh_private_key, created_at, updated_at FROM users WHERE id = ?`,
		id,
	).Scan(&user.ID, &user.Username, &user.Email, &user.Password, &user.IsAdmin, &user.TOTPSecret, &user.TOTPEnabled, &user.SSHPublicKey, &user.SSHPrivateKey, &user.CreatedAt, &user.UpdatedAt)
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

func (r *userRepository) List(ctx context.Context) ([]*User, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, username, email, password, is_admin, totp_secret, totp_enabled, ssh_public_key, ssh_private_key, created_at, updated_at FROM users ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("listing users: %w", err)
	}
	defer rows.Close()

	var users []*User
	for rows.Next() {
		u := &User{}
		if err := rows.Scan(&u.ID, &u.Username, &u.Email, &u.Password, &u.IsAdmin, &u.TOTPSecret, &u.TOTPEnabled, &u.SSHPublicKey, &u.SSHPrivateKey, &u.CreatedAt, &u.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scanning user row: %w", err)
		}
		users = append(users, u)
	}
	return users, rows.Err()
}

func (r *userRepository) Update(ctx context.Context, user *User) error {
	now := time.Now()
	_, err := r.db.ExecContext(ctx,
		`UPDATE users SET username = ?, email = ?, password = ?, is_admin = ?, totp_secret = ?, totp_enabled = ?, ssh_public_key = ?, ssh_private_key = ?, updated_at = ? WHERE id = ?`,
		user.Username, user.Email, user.Password, user.IsAdmin, user.TOTPSecret, user.TOTPEnabled, user.SSHPublicKey, user.SSHPrivateKey, now, user.ID,
	)
	if err != nil {
		return fmt.Errorf("updating user: %w", err)
	}
	user.UpdatedAt = now
	return nil
}

func (r *userRepository) Delete(ctx context.Context, id int64) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM users WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("deleting user: %w", err)
	}
	return nil
}
