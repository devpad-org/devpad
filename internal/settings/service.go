package settings

import (
	"context"
	"errors"
	"fmt"

	"github.com/devpad-org/devpad/internal/auth"
	"github.com/devpad-org/devpad/internal/encrypt"
	"github.com/devpad-org/devpad/internal/sshkey"
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
	GetSSHPublicKey(ctx context.Context, userID int64) (publicKey string, err error)
	GenerateSSHKey(ctx context.Context, userID int64) (publicKey string, err error)
	GetPreferences(ctx context.Context, userID int64) (UserPreferences, error)
	UpdatePreferences(ctx context.Context, userID int64, preferences UserPreferences) (UserPreferences, error)
}

type service struct {
	users       auth.UserRepository
	preferences PreferencesRepository
	cipher      *encrypt.Cipher
}

// NewService creates a new settings Service.
func NewService(users auth.UserRepository, preferences PreferencesRepository, cipher *encrypt.Cipher) Service {
	return &service{users: users, preferences: preferences, cipher: cipher}
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

func (s *service) GetSSHPublicKey(ctx context.Context, userID int64) (string, error) {
	user, err := s.users.GetByID(ctx, userID)
	if err != nil {
		return "", fmt.Errorf("looking up user: %w", err)
	}
	if user == nil {
		return "", errors.New("user not found")
	}
	return user.SSHPublicKey, nil
}

func (s *service) GenerateSSHKey(ctx context.Context, userID int64) (string, error) {
	user, err := s.users.GetByID(ctx, userID)
	if err != nil {
		return "", fmt.Errorf("looking up user: %w", err)
	}
	if user == nil {
		return "", errors.New("user not found")
	}

	comment := fmt.Sprintf("devpad-%s", user.Username)
	privateKey, publicKey, err := sshkey.Generate(comment)
	if err != nil {
		return "", fmt.Errorf("generating SSH key: %w", err)
	}

	encryptedPrivateKey, err := s.cipher.Encrypt(privateKey)
	if err != nil {
		return "", fmt.Errorf("encrypting SSH private key: %w", err)
	}

	user.SSHPublicKey = publicKey
	user.SSHPrivateKey = encryptedPrivateKey
	if err := s.users.Update(ctx, user); err != nil {
		return "", fmt.Errorf("storing SSH key: %w", err)
	}

	return publicKey, nil
}

func (s *service) GetPreferences(ctx context.Context, userID int64) (UserPreferences, error) {
	if err := s.ensureUser(ctx, userID); err != nil {
		return UserPreferences{}, err
	}
	preferences, err := s.preferences.Get(ctx, userID)
	if err != nil {
		return UserPreferences{}, fmt.Errorf("getting user preferences: %w", err)
	}
	return preferences, nil
}

func (s *service) UpdatePreferences(ctx context.Context, userID int64, preferences UserPreferences) (UserPreferences, error) {
	if err := s.ensureUser(ctx, userID); err != nil {
		return UserPreferences{}, err
	}
	updated, err := s.preferences.Update(ctx, userID, preferences)
	if err != nil {
		return UserPreferences{}, fmt.Errorf("updating user preferences: %w", err)
	}
	return updated, nil
}

func (s *service) ensureUser(ctx context.Context, userID int64) error {
	user, err := s.users.GetByID(ctx, userID)
	if err != nil {
		return fmt.Errorf("looking up user: %w", err)
	}
	if user == nil {
		return errors.New("user not found")
	}
	return nil
}
