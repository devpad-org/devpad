package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"golang.org/x/crypto/bcrypt"
)

const (
	// DefaultInviteDays is how long an invite lasts when the admin doesn't say.
	DefaultInviteDays = 7
	// MaxInviteDays is the longest an invite may last.
	MaxInviteDays = 30
)

var (
	ErrInviteInvalid  = errors.New("invite is invalid, expired or already used")
	ErrInviteNotFound = errors.New("invite not found")
	ErrInviteDays     = fmt.Errorf("an invite must last between 1 and %d days", MaxInviteDays)
	ErrNotAdmin       = errors.New("only an admin can invite users")
)

// Invite is a one-time link an admin hands someone so they can create their
// own account. Only the hash of its token is stored. Invites never change:
// one is only ever used, revoked, or deleted with its creator's admin status.
type Invite struct {
	ID        int64
	IsAdmin   bool
	CreatedBy int64
	// CreatedByName is the inviting admin's username.
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

// InviteService creates and redeems invites.
type InviteService interface {
	// Create makes an invite lasting validDays (zero means DefaultInviteDays)
	// and returns it with its token, which is shown only this once.
	Create(ctx context.Context, createdBy int64, isAdmin bool, validDays int) (*Invite, string, error)
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

func (s *inviteService) Create(ctx context.Context, createdBy int64, isAdmin bool, validDays int) (*Invite, string, error) {
	if validDays == 0 {
		validDays = DefaultInviteDays
	}
	if validDays < 1 || validDays > MaxInviteDays {
		return nil, "", ErrInviteDays
	}

	token, hash, err := newInviteToken()
	if err != nil {
		return nil, "", fmt.Errorf("generating invite token: %w", err)
	}
	now := s.now()
	inv := &Invite{
		IsAdmin:   isAdmin,
		CreatedBy: createdBy,
		ExpiresAt: now.AddDate(0, 0, validDays),
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
	username, email, err := CleanIdentity(username, email)
	if err != nil {
		return nil, nil, err
	}
	if err := ValidatePassword(password); err != nil {
		return nil, nil, err
	}

	// Checked before hashing so a bad token costs no bcrypt. Redeem then
	// claims the invite only if it is still unused and unexpired: an invite
	// never changes otherwise, so what Check read still holds, and one
	// revoked or deleted meanwhile is gone.
	inv, err := s.Check(ctx, token)
	if err != nil {
		return nil, nil, err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return nil, nil, fmt.Errorf("hashing password: %w", err)
	}
	user := &User{
		Username: username,
		Email:    email,
		Password: string(hash),
		IsAdmin:  inv.IsAdmin,
	}
	if err := s.invites.Redeem(ctx, hashInviteToken(token), user, s.now()); err != nil {
		return nil, nil, err
	}

	session := newSession(user.ID)
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
