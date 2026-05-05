package settings

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/devpad-org/devpad/internal/auth"
	"github.com/devpad-org/devpad/internal/encrypt"
	_ "github.com/mattn/go-sqlite3"
	"github.com/pquerna/otp/totp"
)

func setupTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:?_foreign_keys=on")
	if err != nil {
		t.Fatalf("opening test db: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	schema := `
	CREATE TABLE users (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		username TEXT NOT NULL UNIQUE,
		email TEXT NOT NULL UNIQUE,
		password TEXT NOT NULL,
		is_admin BOOLEAN NOT NULL DEFAULT 0,
		totp_secret TEXT NOT NULL DEFAULT '',
		totp_enabled BOOLEAN NOT NULL DEFAULT 0,
		ssh_public_key TEXT NOT NULL DEFAULT '',
		ssh_private_key TEXT NOT NULL DEFAULT '',
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);
	CREATE TABLE sessions (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		token TEXT NOT NULL UNIQUE,
		expires_at DATETIME NOT NULL,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);
	CREATE TABLE user_preferences (
		user_id INTEGER PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
		diff_view_side_by_side BOOLEAN NOT NULL DEFAULT 1,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);`
	if _, err := db.Exec(schema); err != nil {
		t.Fatalf("creating schema: %v", err)
	}
	return db
}

func setupTestServices(t *testing.T) (Service, auth.Service, *sql.DB) {
	t.Helper()
	db := setupTestDB(t)
	userRepo := auth.NewUserRepository(db)
	preferencesRepo := NewPreferencesRepository(db)
	sessionRepo := auth.NewSessionRepository(db)
	authSvc := auth.NewService(userRepo, sessionRepo)
	key, err := encrypt.GenerateKey()
	if err != nil {
		t.Fatalf("generating encryption key: %v", err)
	}
	cipher, err := encrypt.NewCipher(key)
	if err != nil {
		t.Fatalf("creating cipher: %v", err)
	}
	settingsSvc := NewService(userRepo, preferencesRepo, cipher)
	return settingsSvc, authSvc, db
}

func createTestUser(t *testing.T, authSvc auth.Service) *auth.User {
	t.Helper()
	ctx := context.Background()
	user, err := authSvc.Setup(ctx, "testuser", "test@example.com", "password123")
	if err != nil {
		t.Fatalf("creating test user: %v", err)
	}
	return user
}

func TestService_ChangePassword(t *testing.T) {
	settingsSvc, authSvc, _ := setupTestServices(t)
	user := createTestUser(t, authSvc)
	ctx := context.Background()

	tests := []struct {
		name        string
		currentPW   string
		newPW       string
		expectErr   error
		expectLogin bool
	}{
		{
			name:      "wrong current password",
			currentPW: "wrongpassword",
			newPW:     "newpassword123",
			expectErr: ErrInvalidCurrentPassword,
		},
		{
			name:        "successful change",
			currentPW:   "password123",
			newPW:       "newpassword123",
			expectLogin: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := settingsSvc.ChangePassword(ctx, user.ID, tt.currentPW, tt.newPW)
			if tt.expectErr != nil {
				if err != tt.expectErr {
					t.Errorf("expected error %v, got %v", tt.expectErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if tt.expectLogin {
				_, err := authSvc.Login(ctx, "testuser", tt.newPW, "")
				if err != nil {
					t.Errorf("login with new password failed: %v", err)
				}
			}
		})
	}
}

func TestService_TOTPSetupAndEnable(t *testing.T) {
	settingsSvc, authSvc, _ := setupTestServices(t)
	user := createTestUser(t, authSvc)
	ctx := context.Background()

	// Generate TOTP setup
	secret, url, err := settingsSvc.GenerateTOTPSetup(ctx, user.ID)
	if err != nil {
		t.Fatalf("generating TOTP setup: %v", err)
	}
	if secret == "" {
		t.Fatal("expected non-empty secret")
	}
	if url == "" {
		t.Fatal("expected non-empty url")
	}

	// Enable with wrong code
	err = settingsSvc.EnableTOTP(ctx, user.ID, "000000")
	if err != ErrInvalidTOTPCode {
		t.Fatalf("expected ErrInvalidTOTPCode, got: %v", err)
	}

	// Enable with correct code
	code, err := totp.GenerateCode(secret, time.Now())
	if err != nil {
		t.Fatalf("generating TOTP code: %v", err)
	}
	if err := settingsSvc.EnableTOTP(ctx, user.ID, code); err != nil {
		t.Fatalf("enabling TOTP: %v", err)
	}

	// Verify status
	enabled, err := settingsSvc.GetMFAStatus(ctx, user.ID)
	if err != nil {
		t.Fatalf("getting MFA status: %v", err)
	}
	if !enabled {
		t.Fatal("expected TOTP to be enabled")
	}

	// Setup again should fail
	_, _, err = settingsSvc.GenerateTOTPSetup(ctx, user.ID)
	if err != ErrTOTPAlreadyEnabled {
		t.Fatalf("expected ErrTOTPAlreadyEnabled, got: %v", err)
	}
}

func TestService_TOTPDisable(t *testing.T) {
	settingsSvc, authSvc, _ := setupTestServices(t)
	user := createTestUser(t, authSvc)
	ctx := context.Background()

	// Disable without enabling should fail
	err := settingsSvc.DisableTOTP(ctx, user.ID, "password123")
	if err != ErrTOTPNotEnabled {
		t.Fatalf("expected ErrTOTPNotEnabled, got: %v", err)
	}

	// Setup and enable TOTP
	secret, _, err := settingsSvc.GenerateTOTPSetup(ctx, user.ID)
	if err != nil {
		t.Fatalf("generating TOTP setup: %v", err)
	}
	code, _ := totp.GenerateCode(secret, time.Now())
	if err := settingsSvc.EnableTOTP(ctx, user.ID, code); err != nil {
		t.Fatalf("enabling TOTP: %v", err)
	}

	// Disable with wrong password
	err = settingsSvc.DisableTOTP(ctx, user.ID, "wrongpassword")
	if err != ErrInvalidCurrentPassword {
		t.Fatalf("expected ErrInvalidCurrentPassword, got: %v", err)
	}

	// Disable with correct password
	if err := settingsSvc.DisableTOTP(ctx, user.ID, "password123"); err != nil {
		t.Fatalf("disabling TOTP: %v", err)
	}

	enabled, err := settingsSvc.GetMFAStatus(ctx, user.ID)
	if err != nil {
		t.Fatalf("getting MFA status: %v", err)
	}
	if enabled {
		t.Fatal("expected TOTP to be disabled")
	}
}

func TestService_LoginWithTOTP(t *testing.T) {
	settingsSvc, authSvc, _ := setupTestServices(t)
	_ = createTestUser(t, authSvc)
	ctx := context.Background()

	// Login without TOTP should work
	session, err := authSvc.Login(ctx, "testuser", "password123", "")
	if err != nil {
		t.Fatalf("login without TOTP failed: %v", err)
	}
	_ = authSvc.Logout(ctx, session.Token)

	// Enable TOTP
	secret, _, err := settingsSvc.GenerateTOTPSetup(ctx, 1)
	if err != nil {
		t.Fatalf("generating TOTP setup: %v", err)
	}
	code, _ := totp.GenerateCode(secret, time.Now())
	if err := settingsSvc.EnableTOTP(ctx, 1, code); err != nil {
		t.Fatalf("enabling TOTP: %v", err)
	}

	// Login without TOTP code should require it
	_, err = authSvc.Login(ctx, "testuser", "password123", "")
	if err != auth.ErrTOTPRequired {
		t.Fatalf("expected ErrTOTPRequired, got: %v", err)
	}

	// Login with wrong TOTP
	_, err = authSvc.Login(ctx, "testuser", "password123", "000000")
	if err != auth.ErrInvalidTOTP {
		t.Fatalf("expected ErrInvalidTOTP, got: %v", err)
	}

	// Login with correct TOTP
	code, _ = totp.GenerateCode(secret, time.Now())
	session, err = authSvc.Login(ctx, "testuser", "password123", code)
	if err != nil {
		t.Fatalf("login with TOTP failed: %v", err)
	}
	if session.Token == "" {
		t.Fatal("expected non-empty session token")
	}
}

func TestHandler_ChangePassword(t *testing.T) {
	settingsSvc, authSvc, _ := setupTestServices(t)
	user := createTestUser(t, authSvc)
	handler := NewHandler(settingsSvc)

	tests := []struct {
		name   string
		body   string
		status int
	}{
		{"empty body", `{}`, http.StatusBadRequest},
		{"short password", `{"currentPassword":"password123","newPassword":"short"}`, http.StatusBadRequest},
		{"wrong current", `{"currentPassword":"wrong","newPassword":"newpassword123"}`, http.StatusUnauthorized},
		{"success", `{"currentPassword":"password123","newPassword":"newpassword123"}`, http.StatusOK},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/settings/password", strings.NewReader(tt.body))
			rec := httptest.NewRecorder()

			ctx := context.WithValue(req.Context(), auth.UserContextKey, user)
			req = req.WithContext(ctx)

			handler.HandleChangePassword(rec, req)

			if rec.Code != tt.status {
				t.Errorf("expected status %d, got %d: %s", tt.status, rec.Code, rec.Body.String())
			}
		})
	}
}

func TestHandler_MFAStatus(t *testing.T) {
	settingsSvc, authSvc, _ := setupTestServices(t)
	user := createTestUser(t, authSvc)
	handler := NewHandler(settingsSvc)

	req := httptest.NewRequest(http.MethodGet, "/api/settings/mfa", nil)
	ctx := context.WithValue(req.Context(), auth.UserContextKey, user)
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()

	handler.HandleGetMFAStatus(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"totpEnabled":false`) {
		t.Errorf("expected totpEnabled=false, got: %s", rec.Body.String())
	}
}

func TestHandler_TOTPSetup(t *testing.T) {
	settingsSvc, authSvc, _ := setupTestServices(t)
	user := createTestUser(t, authSvc)
	handler := NewHandler(settingsSvc)

	req := httptest.NewRequest(http.MethodPost, "/api/settings/mfa/setup", nil)
	ctx := context.WithValue(req.Context(), auth.UserContextKey, user)
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()

	handler.HandleTOTPSetup(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"secret"`) {
		t.Errorf("expected secret in response, got: %s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"url"`) {
		t.Errorf("expected url in response, got: %s", rec.Body.String())
	}
}

func TestService_UserPreferences_DefaultAndUpdate(t *testing.T) {
	settingsSvc, authSvc, _ := setupTestServices(t)
	user := createTestUser(t, authSvc)
	ctx := context.Background()

	preferences, err := settingsSvc.GetPreferences(ctx, user.ID)
	if err != nil {
		t.Fatalf("getting default preferences: %v", err)
	}
	if !preferences.DiffViewSideBySide {
		t.Fatal("expected side-by-side diff view to be enabled by default")
	}

	updated, err := settingsSvc.UpdatePreferences(ctx, user.ID, UserPreferences{DiffViewSideBySide: false})
	if err != nil {
		t.Fatalf("updating preferences: %v", err)
	}
	if updated.DiffViewSideBySide {
		t.Fatal("expected diff view preference to be updated")
	}

	persisted, err := settingsSvc.GetPreferences(ctx, user.ID)
	if err != nil {
		t.Fatalf("getting persisted preferences: %v", err)
	}
	if persisted.DiffViewSideBySide {
		t.Fatal("expected persisted diff view preference to be inline")
	}
}

func TestHandler_UserPreferences(t *testing.T) {
	settingsSvc, authSvc, _ := setupTestServices(t)
	user := createTestUser(t, authSvc)
	handler := NewHandler(settingsSvc)

	req := httptest.NewRequest(http.MethodGet, "/api/settings/preferences", nil)
	ctx := context.WithValue(req.Context(), auth.UserContextKey, user)
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()

	handler.HandleGetPreferences(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"diffViewSideBySide":true`) {
		t.Errorf("expected default side-by-side preference, got: %s", rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodPut, "/api/settings/preferences", strings.NewReader(`{"diffViewSideBySide":false}`))
	ctx = context.WithValue(req.Context(), auth.UserContextKey, user)
	req = req.WithContext(ctx)
	rec = httptest.NewRecorder()

	handler.HandleUpdatePreferences(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"diffViewSideBySide":false`) {
		t.Errorf("expected updated inline preference, got: %s", rec.Body.String())
	}
}

func TestService_SSHKey_GenerateAndGet(t *testing.T) {
	settingsSvc, authSvc, _ := setupTestServices(t)
	user := createTestUser(t, authSvc)
	ctx := context.Background()

	// Initially no SSH key
	pubKey, err := settingsSvc.GetSSHPublicKey(ctx, user.ID)
	if err != nil {
		t.Fatalf("getting SSH key: %v", err)
	}
	if pubKey != "" {
		t.Errorf("expected empty public key, got %q", pubKey)
	}

	// Generate SSH key
	pubKey, err = settingsSvc.GenerateSSHKey(ctx, user.ID)
	if err != nil {
		t.Fatalf("generating SSH key: %v", err)
	}
	if !strings.HasPrefix(pubKey, "ssh-ed25519 ") {
		t.Errorf("expected public key starting with 'ssh-ed25519 ', got %q", pubKey)
	}
	if !strings.Contains(pubKey, "devpad-testuser") {
		t.Errorf("expected comment containing 'devpad-testuser', got %q", pubKey)
	}

	// Get SSH key should return the same key
	gotKey, err := settingsSvc.GetSSHPublicKey(ctx, user.ID)
	if err != nil {
		t.Fatalf("getting SSH key: %v", err)
	}
	if gotKey != pubKey {
		t.Errorf("expected %q, got %q", pubKey, gotKey)
	}
}

func TestService_SSHKey_Regenerate(t *testing.T) {
	settingsSvc, authSvc, _ := setupTestServices(t)
	user := createTestUser(t, authSvc)
	ctx := context.Background()

	firstKey, err := settingsSvc.GenerateSSHKey(ctx, user.ID)
	if err != nil {
		t.Fatalf("generating SSH key: %v", err)
	}

	secondKey, err := settingsSvc.GenerateSSHKey(ctx, user.ID)
	if err != nil {
		t.Fatalf("regenerating SSH key: %v", err)
	}

	if firstKey == secondKey {
		t.Error("expected different keys after regeneration")
	}
}

func TestHandler_GetSSHKey(t *testing.T) {
	settingsSvc, authSvc, _ := setupTestServices(t)
	user := createTestUser(t, authSvc)
	handler := NewHandler(settingsSvc)

	req := httptest.NewRequest(http.MethodGet, "/api/settings/ssh-key", nil)
	ctx := context.WithValue(req.Context(), auth.UserContextKey, user)
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()

	handler.HandleGetSSHKey(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"publicKey"`) {
		t.Errorf("expected publicKey in response, got: %s", rec.Body.String())
	}
}

func TestHandler_GenerateSSHKey(t *testing.T) {
	settingsSvc, authSvc, _ := setupTestServices(t)
	user := createTestUser(t, authSvc)
	handler := NewHandler(settingsSvc)

	req := httptest.NewRequest(http.MethodPost, "/api/settings/ssh-key/generate", nil)
	ctx := context.WithValue(req.Context(), auth.UserContextKey, user)
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()

	handler.HandleGenerateSSHKey(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `ssh-ed25519`) {
		t.Errorf("expected ssh-ed25519 key in response, got: %s", rec.Body.String())
	}
}

func TestHandler_GetSSHKey_Unauthenticated(t *testing.T) {
	settingsSvc, _, _ := setupTestServices(t)
	handler := NewHandler(settingsSvc)

	req := httptest.NewRequest(http.MethodGet, "/api/settings/ssh-key", nil)
	rec := httptest.NewRecorder()

	handler.HandleGetSSHKey(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected status 401, got %d", rec.Code)
	}
}
