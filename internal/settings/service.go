package settings

import (
	"context"
	"errors"
	"fmt"
	"log"
	"slices"

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
	ErrNoPreferenceChange     = errors.New("no preference to update")
	ErrUnknownTheme           = errors.New("unknown theme")
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
	GetGitIdentity(ctx context.Context, userID int64) (GitIdentity, error)
	// SaveGitIdentity records the name and email the user commits as, empty
	// for their account's own, and applies them to their running project
	// workspaces and sandboxes. The others get them when they next start.
	SaveGitIdentity(ctx context.Context, userID int64, name, email string) (SavedGitIdentity, error)
	// SetGitIdentitySyncer registers what applies a saved identity to
	// running workspaces.
	SetGitIdentitySyncer(syncer GitIdentitySyncer)
	GetPreferences(ctx context.Context, userID int64) (UserPreferences, error)
	// UpdatePreferences changes only the preferences the update sets and
	// returns them all.
	UpdatePreferences(ctx context.Context, userID int64, update PreferencesUpdate) (UserPreferences, error)
}

// GitIdentity is what a user commits as. Name and Email are their own choice,
// empty when they use their account's, which come with it for the page to show.
type GitIdentity struct {
	Name         string `json:"name"`
	Email        string `json:"email"`
	AccountName  string `json:"accountName"`
	AccountEmail string `json:"accountEmail"`
}

// SavedGitIdentity is a saved identity and how far it has reached.
type SavedGitIdentity struct {
	GitIdentity
	// WorkspacesUpdated counts the running workspaces that now use it.
	WorkspacesUpdated int `json:"workspacesUpdated"`
	// SyncIncomplete is set when a running workspace could not be updated.
	// It gets the identity when it next starts.
	SyncIncomplete bool `json:"syncIncomplete"`
}

// GitIdentitySyncer applies a user's git identity to their running workspaces.
type GitIdentitySyncer interface {
	SyncGitIdentity(ctx context.Context, userID int64) (updated int, complete bool)
}

type service struct {
	users       auth.UserRepository
	preferences PreferencesRepository
	cipher      *encrypt.Cipher
	gitSyncer   GitIdentitySyncer
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

func (s *service) SetGitIdentitySyncer(syncer GitIdentitySyncer) {
	s.gitSyncer = syncer
}

func (s *service) GetGitIdentity(ctx context.Context, userID int64) (GitIdentity, error) {
	user, err := s.users.GetByID(ctx, userID)
	if err != nil {
		return GitIdentity{}, fmt.Errorf("looking up user: %w", err)
	}
	if user == nil {
		return GitIdentity{}, errors.New("user not found")
	}
	return GitIdentity{Name: user.GitName, Email: user.GitEmail, AccountName: user.Username, AccountEmail: user.Email}, nil
}

func (s *service) SaveGitIdentity(ctx context.Context, userID int64, name, email string) (SavedGitIdentity, error) {
	name, email, err := auth.CleanGitIdentity(name, email)
	if err != nil {
		return SavedGitIdentity{}, err
	}
	if err := s.ensureUser(ctx, userID); err != nil {
		return SavedGitIdentity{}, err
	}
	if err := s.users.SetGitIdentity(ctx, userID, name, email); err != nil {
		return SavedGitIdentity{}, err
	}
	identity, err := s.GetGitIdentity(ctx, userID)
	if err != nil {
		return SavedGitIdentity{}, err
	}
	saved := SavedGitIdentity{GitIdentity: identity}
	if s.gitSyncer != nil {
		// The identity is saved, so finish applying it even if the
		// browser stops waiting.
		updated, complete := s.gitSyncer.SyncGitIdentity(context.WithoutCancel(ctx), userID)
		saved.WorkspacesUpdated, saved.SyncIncomplete = updated, !complete
		if !complete {
			log.Printf("user %d: git identity did not reach every running workspace", userID)
		}
	}
	return saved, nil
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

func (s *service) UpdatePreferences(ctx context.Context, userID int64, update PreferencesUpdate) (UserPreferences, error) {
	if update.DiffViewSideBySide == nil && update.Theme == nil {
		return UserPreferences{}, ErrNoPreferenceChange
	}
	if update.Theme != nil && !slices.Contains(Themes, *update.Theme) {
		return UserPreferences{}, ErrUnknownTheme
	}
	if err := s.ensureUser(ctx, userID); err != nil {
		return UserPreferences{}, err
	}
	updated, err := s.preferences.Update(ctx, userID, update)
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
