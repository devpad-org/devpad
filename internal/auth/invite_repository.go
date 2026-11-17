package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// InviteRepository stores invites.
type InviteRepository interface {
	// Create stores invite, filling in its ID and CreatedByName, but only
	// while its creator is an admin: it returns ErrNotAdmin otherwise, so an
	// invite made while its creator is being demoted can't outlive the
	// demotion.
	Create(ctx context.Context, invite *Invite, tokenHash string) error
	// GetByTokenHash returns nil when no invite has that hash.
	GetByTokenHash(ctx context.Context, tokenHash string) (*Invite, error)
	ListPending(ctx context.Context) ([]*Invite, error)
	DeletePending(ctx context.Context, id int64) (bool, error)
	// Redeem marks the unused, unexpired invite with tokenHash used and
	// creates user, both or neither. It returns ErrInviteInvalid when there
	// is no such invite (never made, revoked, expired or already used) and
	// ErrUserExists when the username or email is taken.
	Redeem(ctx context.Context, tokenHash string, user *User, now time.Time) error
}

type inviteRepository struct {
	db *sql.DB
}

// NewInviteRepository creates an InviteRepository backed by SQLite.
func NewInviteRepository(db *sql.DB) InviteRepository {
	return &inviteRepository{db: db}
}

// An invite goes when its creator does (ON DELETE CASCADE), so every invite
// has one to join.
const inviteSelect = `SELECT i.id, i.is_admin, i.created_by, u.username, i.expires_at, i.used_at, i.used_by, i.created_at
	FROM user_invites i JOIN users u ON u.id = i.created_by`

type rowScanner interface {
	Scan(dest ...any) error
}

func scanInvite(row rowScanner) (*Invite, error) {
	inv := &Invite{}
	var usedAt sql.NullTime
	var usedBy sql.NullInt64
	if err := row.Scan(&inv.ID, &inv.IsAdmin, &inv.CreatedBy, &inv.CreatedByName, &inv.ExpiresAt, &usedAt, &usedBy, &inv.CreatedAt); err != nil {
		return nil, err
	}
	if usedAt.Valid {
		inv.UsedAt = &usedAt.Time
	}
	if usedBy.Valid {
		inv.UsedBy = &usedBy.Int64
	}
	return inv, nil
}

func (r *inviteRepository) Create(ctx context.Context, invite *Invite, tokenHash string) error {
	err := r.db.QueryRowContext(ctx,
		`INSERT INTO user_invites (token_hash, is_admin, created_by, expires_at, created_at)
		 SELECT ?, ?, id, ?, ? FROM users WHERE id = ? AND is_admin
		 RETURNING id, (SELECT username FROM users WHERE users.id = created_by)`,
		tokenHash, invite.IsAdmin, invite.ExpiresAt.UTC(), invite.CreatedAt.UTC(), invite.CreatedBy,
	).Scan(&invite.ID, &invite.CreatedByName)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotAdmin
	}
	if err != nil {
		return fmt.Errorf("inserting invite: %w", err)
	}
	return nil
}

func (r *inviteRepository) GetByTokenHash(ctx context.Context, tokenHash string) (*Invite, error) {
	inv, err := scanInvite(r.db.QueryRowContext(ctx, inviteSelect+` WHERE i.token_hash = ?`, tokenHash))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("querying invite: %w", err)
	}
	return inv, nil
}

func (r *inviteRepository) ListPending(ctx context.Context) ([]*Invite, error) {
	rows, err := r.db.QueryContext(ctx, inviteSelect+` WHERE i.used_at IS NULL ORDER BY i.created_at DESC, i.id DESC`)
	if err != nil {
		return nil, fmt.Errorf("listing invites: %w", err)
	}
	defer rows.Close()

	invites := []*Invite{}
	for rows.Next() {
		inv, err := scanInvite(rows)
		if err != nil {
			return nil, fmt.Errorf("scanning invite row: %w", err)
		}
		invites = append(invites, inv)
	}
	return invites, rows.Err()
}

func (r *inviteRepository) DeletePending(ctx context.Context, id int64) (bool, error) {
	result, err := r.db.ExecContext(ctx, `DELETE FROM user_invites WHERE id = ? AND used_at IS NULL`, id)
	if err != nil {
		return false, fmt.Errorf("deleting invite: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("counting deleted invites: %w", err)
	}
	return n > 0, nil
}

func (r *inviteRepository) Redeem(ctx context.Context, tokenHash string, user *User, now time.Time) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("beginning transaction: %w", err)
	}
	defer tx.Rollback()

	// Claiming first means two redemptions of one invite can't both get
	// past here, whatever the transaction mode.
	var id int64
	err = tx.QueryRowContext(ctx,
		`UPDATE user_invites SET used_at = ? WHERE token_hash = ? AND used_at IS NULL AND expires_at > ? RETURNING id`,
		now.UTC(), tokenHash, now.UTC()).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrInviteInvalid
	}
	if err != nil {
		return fmt.Errorf("claiming invite: %w", err)
	}

	if err := insertUser(ctx, tx, user); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE user_invites SET used_by = ? WHERE id = ?`, user.ID, id); err != nil {
		return fmt.Errorf("recording invitee: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing invite: %w", err)
	}
	return nil
}
