package auth

import (
	"context"
	"errors"
	"fmt"
	"time"

	"golang.org/x/crypto/bcrypt"
)

const sessionDuration = 7 * 24 * time.Hour

var (
	ErrInvalidCredentials = errors.New("invalid username or password")
	ErrUserExists         = errors.New("username or email already exists")
	ErrSetupCompleted     = errors.New("setup already completed")
)

// Service defines authentication business logic.
type Service interface {
	Setup(ctx context.Context, username, email, password string) (*User, error)
	NeedsSetup(ctx context.Context) (bool, error)
	Login(ctx context.Context, username, password string) (*Session, error)
	Logout(ctx context.Context, token string) error
	ValidateSession(ctx context.Context, token string) (*User, error)
}

type service struct {
	users    UserRepository
	sessions SessionRepository
}

// NewService creates a new auth Service.
func NewService(users UserRepository, sessions SessionRepository) Service {
	return &service{users: users, sessions: sessions}
}

func (s *service) NeedsSetup(ctx context.Context) (bool, error) {
	count, err := s.users.Count(ctx)
	if err != nil {
		return false, fmt.Errorf("checking user count: %w", err)
	}
	return count == 0, nil
}

func (s *service) Setup(ctx context.Context, username, email, password string) (*User, error) {
	needs, err := s.NeedsSetup(ctx)
	if err != nil {
		return nil, err
	}
	if !needs {
		return nil, ErrSetupCompleted
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return nil, fmt.Errorf("hashing password: %w", err)
	}

	user := &User{
		Username: username,
		Email:    email,
		Password: string(hash),
		IsAdmin:  true,
	}

	if err := s.users.Create(ctx, user); err != nil {
		return nil, fmt.Errorf("creating admin user: %w", err)
	}

	return user, nil
}

func (s *service) Login(ctx context.Context, username, password string) (*Session, error) {
	user, err := s.users.GetByUsername(ctx, username)
	if err != nil {
		return nil, fmt.Errorf("looking up user: %w", err)
	}
	if user == nil {
		return nil, ErrInvalidCredentials
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.Password), []byte(password)); err != nil {
		return nil, ErrInvalidCredentials
	}

	session := &Session{
		UserID:    user.ID,
		ExpiresAt: time.Now().Add(sessionDuration),
	}

	if err := s.sessions.Create(ctx, session); err != nil {
		return nil, fmt.Errorf("creating session: %w", err)
	}

	return session, nil
}

func (s *service) Logout(ctx context.Context, token string) error {
	if err := s.sessions.DeleteByToken(ctx, token); err != nil {
		return fmt.Errorf("deleting session: %w", err)
	}
	return nil
}

func (s *service) ValidateSession(ctx context.Context, token string) (*User, error) {
	session, err := s.sessions.GetByToken(ctx, token)
	if err != nil {
		return nil, fmt.Errorf("looking up session: %w", err)
	}
	if session == nil {
		return nil, nil
	}

	user, err := s.users.GetByID(ctx, session.UserID)
	if err != nil {
		return nil, fmt.Errorf("looking up user for session: %w", err)
	}

	return user, nil
}
