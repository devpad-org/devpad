package auth

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/devpad-org/devpad/internal/database"
)

// UserRepository defines data access for users.
type UserRepository interface {
	Create(ctx context.Context, user *User) error
	GetByUsername(ctx context.Context, username string) (*User, error)
	GetByID(ctx context.Context, id int64) (*User, error)
	Count(ctx context.Context) (int, error)
	List(ctx context.Context) ([]*User, error)
	// Update writes the account's fields. It leaves the git identity alone,
	// which only SetGitIdentity changes, so a password change or new SSH key
	// made at the same moment cannot undo a saved identity.
	Update(ctx context.Context, user *User) error
	// SetGitIdentity records the name and email the user chose to commit
	// as, either of which may be empty.
	SetGitIdentity(ctx context.Context, id int64, name, email string) error
	Delete(ctx context.Context, id int64) error
}

const userColumns = `id, username, email, password, is_admin, totp_secret, totp_enabled, ssh_public_key, ssh_private_key, git_name, git_email, git_identity_saved, created_at, updated_at`

// scanUser reads a row of userColumns.
func scanUser(row interface{ Scan(dest ...any) error }) (*User, error) {
	u := &User{}
	err := row.Scan(&u.ID, &u.Username, &u.Email, &u.Password, &u.IsAdmin, &u.TOTPSecret, &u.TOTPEnabled, &u.SSHPublicKey, &u.SSHPrivateKey, &u.GitName, &u.GitEmail, &u.GitIdentitySaved, &u.CreatedAt, &u.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return u, nil
}

type userRepository struct {
	db *sql.DB
}

// NewUserRepository creates a new UserRepository backed by SQLite.
func NewUserRepository(db *sql.DB) UserRepository {
	return &userRepository{db: db}
}

func (r *userRepository) Create(ctx context.Context, user *User) error {
	return insertUser(ctx, r.db, user)
}

// execer is what insertUser needs from a *sql.DB or a *sql.Tx.
type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// insertUser adds user and fills in its ID and timestamps. A username or
// email that is already taken comes back as ErrUserExists.
func insertUser(ctx context.Context, db execer, user *User) error {
	now := time.Now()
	result, err := db.ExecContext(ctx,
		`INSERT INTO users (username, email, password, is_admin, totp_secret, totp_enabled, ssh_public_key, ssh_private_key, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		user.Username, user.Email, user.Password, user.IsAdmin, user.TOTPSecret, user.TOTPEnabled, user.SSHPublicKey, user.SSHPrivateKey, now, now,
	)
	if err != nil {
		if database.IsUniqueViolation(err) {
			return fmt.Errorf("inserting user: %w", ErrUserExists)
		}
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
	user, err := scanUser(r.db.QueryRowContext(ctx,
		`SELECT `+userColumns+` FROM users WHERE username = ?`,
		username,
	))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("querying user by username: %w", err)
	}
	return user, nil
}

func (r *userRepository) GetByID(ctx context.Context, id int64) (*User, error) {
	user, err := scanUser(r.db.QueryRowContext(ctx,
		`SELECT `+userColumns+` FROM users WHERE id = ?`,
		id,
	))
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
		`SELECT `+userColumns+` FROM users ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("listing users: %w", err)
	}
	defer rows.Close()

	var users []*User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
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
		if database.IsUniqueViolation(err) {
			return fmt.Errorf("updating user: %w", ErrUserExists)
		}
		return fmt.Errorf("updating user: %w", err)
	}
	user.UpdatedAt = now
	return nil
}

func (r *userRepository) SetGitIdentity(ctx context.Context, id int64, name, email string) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE users SET git_name = ?, git_email = ?, git_identity_saved = 1, updated_at = ? WHERE id = ?`,
		name, email, time.Now(), id,
	)
	if err != nil {
		return fmt.Errorf("setting git identity: %w", err)
	}
	return nil
}

func (r *userRepository) Delete(ctx context.Context, id int64) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM users WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("deleting user: %w", err)
	}
	return nil
}
