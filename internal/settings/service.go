package settings

import (
	"context"
	"errors"
	"fmt"

	"github.com/devpad-org/devpad/internal/auth"
	"github.com/pquerna/otp/totp"
	"golang.org/x/crypto/bcrypt"
)

var (
	ErrInvalidCurrentPassword = errors.New("current password is incorrect")
	ErrTOTPAlreadyEnabled     = errors.New("TOTP is already enabled")
	ErrTOTPNotEnabled         = errors.New("TOTP is not enabled")
	ErrInvalidTOTPCode        = errors.New("invalid TOTP code")
)

// Service defines settings business logic.
type Service interface {
	ChangePassword(ctx context.Context, userID int64, currentPassword, newPassword string) error
	GenerateTOTPSetup(ctx context.Context, userID int64) (secret string, url string, err error)
	EnableTOTP(ctx context.Context, userID int64, code string) error
	DisableTOTP(ctx context.Context, userID int64, password string) error
	GetMFAStatus(ctx context.Context, userID int64) (enabled bool, err error)
}

type service struct {
	users auth.UserRepository
}

// NewService creates a new settings Service.
func NewService(users auth.UserRepository) Service {
	return &service{users: users}
}

func (s *service) ChangePassword(ctx context.Context, userID int64, currentPassword, newPassword string) error {
	user, err := s.users.GetByID(ctx, userID)
	if err != nil {
		return fmt.Errorf("looking up user: %w", err)
	}
	if user == nil {
		return errors.New("user not found")
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.Password), []byte(currentPassword)); err != nil {
		return ErrInvalidCurrentPassword
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("hashing password: %w", err)
	}

	user.Password = string(hash)
	if err := s.users.Update(ctx, user); err != nil {
		return fmt.Errorf("updating password: %w", err)
	}

	return nil
}

func (s *service) GenerateTOTPSetup(ctx context.Context, userID int64) (string, string, error) {
	user, err := s.users.GetByID(ctx, userID)
	if err != nil {
		return "", "", fmt.Errorf("looking up user: %w", err)
	}
	if user == nil {
		return "", "", errors.New("user not found")
	}

	if user.TOTPEnabled {
		return "", "", ErrTOTPAlreadyEnabled
	}

	key, err := totp.Generate(totp.GenerateOpts{
		Issuer:      "Devpad",
		AccountName: user.Username,
	})
	if err != nil {
		return "", "", fmt.Errorf("generating TOTP key: %w", err)
	}

	secret := key.Secret()

	// Store the secret (not yet enabled) so we can verify on confirmation
	user.TOTPSecret = secret
	if err := s.users.Update(ctx, user); err != nil {
		return "", "", fmt.Errorf("storing TOTP secret: %w", err)
	}

	return secret, key.URL(), nil
}

func (s *service) EnableTOTP(ctx context.Context, userID int64, code string) error {
	user, err := s.users.GetByID(ctx, userID)
	if err != nil {
		return fmt.Errorf("looking up user: %w", err)
	}
	if user == nil {
		return errors.New("user not found")
	}

	if user.TOTPEnabled {
		return ErrTOTPAlreadyEnabled
	}

	if user.TOTPSecret == "" {
		return errors.New("no TOTP secret configured, generate setup first")
	}

	if !totp.Validate(code, user.TOTPSecret) {
		return ErrInvalidTOTPCode
	}

	user.TOTPEnabled = true
	if err := s.users.Update(ctx, user); err != nil {
		return fmt.Errorf("enabling TOTP: %w", err)
	}

	return nil
}

func (s *service) DisableTOTP(ctx context.Context, userID int64, password string) error {
	user, err := s.users.GetByID(ctx, userID)
	if err != nil {
		return fmt.Errorf("looking up user: %w", err)
	}
	if user == nil {
		return errors.New("user not found")
	}

	if !user.TOTPEnabled {
		return ErrTOTPNotEnabled
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.Password), []byte(password)); err != nil {
		return ErrInvalidCurrentPassword
	}

	user.TOTPEnabled = false
	user.TOTPSecret = ""
	if err := s.users.Update(ctx, user); err != nil {
		return fmt.Errorf("disabling TOTP: %w", err)
	}

	return nil
}

func (s *service) GetMFAStatus(ctx context.Context, userID int64) (bool, error) {
	user, err := s.users.GetByID(ctx, userID)
	if err != nil {
		return false, fmt.Errorf("looking up user: %w", err)
	}
	if user == nil {
		return false, errors.New("user not found")
	}
	return user.TOTPEnabled, nil
}
