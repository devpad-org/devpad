package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

const (
	// DefaultInviteTTL is how long an invite lasts when the admin doesn't say.
	DefaultInviteTTL = 7 * 24 * time.Hour
	// MaxInviteTTL is the longest an invite may last.
	MaxInviteTTL = 30 * 24 * time.Hour

	// MinPasswordLength is the shortest password devpad accepts.
	MinPasswordLength = 8
	// MaxPasswordLength is bcrypt's limit: it refuses anything longer.
	MaxPasswordLength = 72
)

var (
	ErrInviteInvalid  = errors.New("invite is invalid, expired or already used")
	ErrInviteNotFound = errors.New("invite not found")
	ErrEmailTaken     = errors.New("a user with that email already exists")
	ErrEmailRequired  = errors.New("email is required")
	ErrInviteTTL      = errors.New("an invite must last between 1 hour and 30 days")
)

// Invite is a one-time link an admin hands someone so they can create their
// own account. Only the hash of its token is stored.
type Invite struct {
	ID        int64
	Email     string // when set, the account's email; otherwise the invitee picks one
	IsAdmin   bool
	CreatedBy int64
	// CreatedByName is the inviting admin's username (listings only).
	CreatedByName string
	ExpiresAt     time.Time
	UsedAt        *time.Time
	UsedBy        *int64
	CreatedAt     time.Time
}

// Expired reports whether the invite can no longer be used because of its age.
func (i *Invite) Expired(now time.Time) bool {
	return !now.Before(i.ExpiresAt)
}

// usable reports whether the invite can still create an account.
func (i *Invite) usable(now time.Time) bool {
	return i.UsedAt == nil && !i.Expired(now)
}

// InviteRepository stores invites.
type InviteRepository interface {
	Create(ctx context.Context, invite *Invite, tokenHash string) error
	GetByTokenHash(ctx context.Context, tokenHash string) (*Invite, error)
	ListPending(ctx context.Context) ([]*Invite, error)
	DeletePending(ctx context.Context, id int64) (bool, error)
	EmailTaken(ctx context.Context, email string) (bool, error)
	// Redeem creates user and marks the invite used, both or neither. It
	// returns ErrInviteInvalid when the invite is unknown, expired or used,
	// and ErrUserExists when the username or email is taken.
	Redeem(ctx context.Context, tokenHash string, user *User, now time.Time) (*Invite, error)
}

type inviteRepository struct {
	db *sql.DB
}

// NewInviteRepository creates an InviteRepository backed by SQLite.
func NewInviteRepository(db *sql.DB) InviteRepository {
	return &inviteRepository{db: db}
}

const inviteColumns = `i.id, i.email, i.is_admin, i.created_by, COALESCE(u.username, ''), i.expires_at, i.used_at, i.used_by, i.created_at`

type rowScanner interface {
	Scan(dest ...any) error
}

func scanInvite(row rowScanner) (*Invite, error) {
	inv := &Invite{}
	var usedAt sql.NullTime
	var usedBy sql.NullInt64
	if err := row.Scan(&inv.ID, &inv.Email, &inv.IsAdmin, &inv.CreatedBy, &inv.CreatedByName, &inv.ExpiresAt, &usedAt, &usedBy, &inv.CreatedAt); err != nil {
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
	result, err := r.db.ExecContext(ctx,
		`INSERT INTO user_invites (token_hash, email, is_admin, created_by, expires_at, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		tokenHash, invite.Email, invite.IsAdmin, invite.CreatedBy, invite.ExpiresAt, invite.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("inserting invite: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return fmt.Errorf("getting inserted id: %w", err)
	}
	invite.ID = id
	return nil
}

func (r *inviteRepository) GetByTokenHash(ctx context.Context, tokenHash string) (*Invite, error) {
	inv, err := scanInvite(r.db.QueryRowContext(ctx,
		`SELECT `+inviteColumns+` FROM user_invites i LEFT JOIN users u ON u.id = i.created_by WHERE i.token_hash = ?`, tokenHash))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("querying invite: %w", err)
	}
	return inv, nil
}

func (r *inviteRepository) ListPending(ctx context.Context) ([]*Invite, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT `+inviteColumns+` FROM user_invites i LEFT JOIN users u ON u.id = i.created_by WHERE i.used_at IS NULL ORDER BY i.created_at DESC, i.id DESC`)
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

func (r *inviteRepository) EmailTaken(ctx context.Context, email string) (bool, error) {
	var n int
	if err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE email = ?`, email).Scan(&n); err != nil {
		return false, fmt.Errorf("checking email: %w", err)
	}
	return n > 0, nil
}

func (r *inviteRepository) Redeem(ctx context.Context, tokenHash string, user *User, now time.Time) (*Invite, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("beginning transaction: %w", err)
	}
	defer tx.Rollback()

	inv, err := scanInvite(tx.QueryRowContext(ctx,
		`SELECT `+inviteColumns+` FROM user_invites i LEFT JOIN users u ON u.id = i.created_by WHERE i.token_hash = ?`, tokenHash))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrInviteInvalid
	}
	if err != nil {
		return nil, fmt.Errorf("querying invite: %w", err)
	}
	if !inv.usable(now) {
		return nil, ErrInviteInvalid
	}

	// What the invite says wins over what the invitee asked for.
	user.IsAdmin = inv.IsAdmin
	if inv.Email != "" {
		user.Email = inv.Email
	}
	if err := insertUser(ctx, tx, user); err != nil {
		return nil, err
	}

	result, err := tx.ExecContext(ctx,
		`UPDATE user_invites SET used_at = ?, used_by = ? WHERE id = ? AND used_at IS NULL`, now, user.ID, inv.ID)
	if err != nil {
		return nil, fmt.Errorf("marking invite used: %w", err)
	}
	if n, err := result.RowsAffected(); err != nil {
		return nil, fmt.Errorf("marking invite used: %w", err)
	} else if n != 1 {
		return nil, ErrInviteInvalid
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("committing invite: %w", err)
	}
	inv.UsedAt = &now
	inv.UsedBy = &user.ID
	return inv, nil
}

// InviteService creates and redeems invites.
type InviteService interface {
	// Create makes an invite and returns it with its token, which is shown
	// only this once. A ttl of zero means DefaultInviteTTL.
	Create(ctx context.Context, createdBy int64, email string, isAdmin bool, ttl time.Duration) (*Invite, string, error)
	ListPending(ctx context.Context) ([]*Invite, error)
	Revoke(ctx context.Context, id int64) error
	// Check returns the invite for token if it can still be used.
	Check(ctx context.Context, token string) (*Invite, error)
	// Accept creates the invitee's account and signs them in.
	Accept(ctx context.Context, token, username, email, password string) (*User, *Session, error)
}

type inviteService struct {
	invites  InviteRepository
	sessions SessionRepository
	now      func() time.Time
}

// NewInviteService creates an InviteService.
func NewInviteService(invites InviteRepository, sessions SessionRepository) InviteService {
	return &inviteService{invites: invites, sessions: sessions, now: func() time.Time { return time.Now().UTC() }}
}

func (s *inviteService) Create(ctx context.Context, createdBy int64, email string, isAdmin bool, ttl time.Duration) (*Invite, string, error) {
	if ttl == 0 {
		ttl = DefaultInviteTTL
	}
	if ttl < time.Hour || ttl > MaxInviteTTL {
		return nil, "", ErrInviteTTL
	}
	email = strings.TrimSpace(email)
	if email != "" {
		taken, err := s.invites.EmailTaken(ctx, email)
		if err != nil {
			return nil, "", err
		}
		if taken {
			return nil, "", ErrEmailTaken
		}
	}

	token, hash, err := newInviteToken()
	if err != nil {
		return nil, "", fmt.Errorf("generating invite token: %w", err)
	}
	now := s.now()
	inv := &Invite{
		Email:     email,
		IsAdmin:   isAdmin,
		CreatedBy: createdBy,
		ExpiresAt: now.Add(ttl),
		CreatedAt: now,
	}
	if err := s.invites.Create(ctx, inv, hash); err != nil {
		return nil, "", err
	}
	return inv, token, nil
}

func (s *inviteService) ListPending(ctx context.Context) ([]*Invite, error) {
	return s.invites.ListPending(ctx)
}

func (s *inviteService) Revoke(ctx context.Context, id int64) error {
	deleted, err := s.invites.DeletePending(ctx, id)
	if err != nil {
		return err
	}
	if !deleted {
		return ErrInviteNotFound
	}
	return nil
}

func (s *inviteService) Check(ctx context.Context, token string) (*Invite, error) {
	if token == "" {
		return nil, ErrInviteInvalid
	}
	inv, err := s.invites.GetByTokenHash(ctx, hashInviteToken(token))
	if err != nil {
		return nil, err
	}
	if inv == nil || !inv.usable(s.now()) {
		return nil, ErrInviteInvalid
	}
	return inv, nil
}

func (s *inviteService) Accept(ctx context.Context, token, username, email, password string) (*User, *Session, error) {
	// Checked before hashing so a bad token costs no bcrypt; Redeem checks
	// again, inside its transaction.
	inv, err := s.Check(ctx, token)
	if err != nil {
		return nil, nil, err
	}
	email = strings.TrimSpace(email)
	if inv.Email == "" && email == "" {
		return nil, nil, ErrEmailRequired
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return nil, nil, fmt.Errorf("hashing password: %w", err)
	}
	user := &User{
		Username: strings.TrimSpace(username),
		Email:    email,
		Password: string(hash),
	}
	if _, err := s.invites.Redeem(ctx, hashInviteToken(token), user, s.now()); err != nil {
		return nil, nil, err
	}

	session := &Session{UserID: user.ID, ExpiresAt: time.Now().Add(sessionDuration)}
	if err := s.sessions.Create(ctx, session); err != nil {
		return user, nil, fmt.Errorf("creating session: %w", err)
	}
	return user, session, nil
}

// newInviteToken returns a 256-bit random token, URL-safe, and its hash.
func newInviteToken() (token, hash string, err error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", "", err
	}
	token = base64.RawURLEncoding.EncodeToString(b)
	return token, hashInviteToken(token), nil
}

func hashInviteToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
