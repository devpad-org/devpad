package auth

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
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
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);
	CREATE TABLE sessions (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		token TEXT NOT NULL UNIQUE,
		expires_at DATETIME NOT NULL,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);`
	if _, err := db.Exec(schema); err != nil {
		t.Fatalf("creating schema: %v", err)
	}
	return db
}

func setupTestService(t *testing.T) (Service, *sql.DB) {
	t.Helper()
	db := setupTestDB(t)
	users := NewUserRepository(db)
	sessions := NewSessionRepository(db)
	svc := NewService(users, sessions)
	return svc, db
}

func TestService_NeedsSetup(t *testing.T) {
	svc, _ := setupTestService(t)
	ctx := context.Background()

	needs, err := svc.NeedsSetup(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !needs {
		t.Fatal("expected NeedsSetup to return true for empty database")
	}
}

func TestService_Setup(t *testing.T) {
	svc, _ := setupTestService(t)
	ctx := context.Background()

	user, err := svc.Setup(ctx, "admin", "admin@test.com", "password123")
	if err != nil {
		t.Fatalf("setup failed: %v", err)
	}
	if user.Username != "admin" {
		t.Errorf("expected username 'admin', got %q", user.Username)
	}
	if !user.IsAdmin {
		t.Error("expected admin user to have IsAdmin=true")
	}

	// NeedsSetup should now return false
	needs, err := svc.NeedsSetup(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if needs {
		t.Fatal("expected NeedsSetup=false after setup")
	}

	// Second setup should fail
	_, err = svc.Setup(ctx, "admin2", "admin2@test.com", "password123")
	if err != ErrSetupCompleted {
		t.Fatalf("expected ErrSetupCompleted, got: %v", err)
	}
}

func TestService_LoginLogout(t *testing.T) {
	svc, _ := setupTestService(t)
	ctx := context.Background()

	// Setup admin
	_, err := svc.Setup(ctx, "admin", "admin@test.com", "password123")
	if err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	// Login with correct credentials
	session, err := svc.Login(ctx, "admin", "password123", "")
	if err != nil {
		t.Fatalf("login failed: %v", err)
	}
	if session.Token == "" {
		t.Fatal("expected non-empty session token")
	}

	// Validate session
	user, err := svc.ValidateSession(ctx, session.Token)
	if err != nil {
		t.Fatalf("validate session failed: %v", err)
	}
	if user == nil {
		t.Fatal("expected valid user from session")
	}
	if user.Username != "admin" {
		t.Errorf("expected username 'admin', got %q", user.Username)
	}

	// Logout
	if err := svc.Logout(ctx, session.Token); err != nil {
		t.Fatalf("logout failed: %v", err)
	}

	// Session should be invalid
	user, err = svc.ValidateSession(ctx, session.Token)
	if err != nil {
		t.Fatalf("validate session failed: %v", err)
	}
	if user != nil {
		t.Fatal("expected nil user after logout")
	}
}

func TestService_LoginInvalidCredentials(t *testing.T) {
	svc, _ := setupTestService(t)
	ctx := context.Background()

	_, err := svc.Setup(ctx, "admin", "admin@test.com", "password123")
	if err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	tests := []struct {
		name     string
		username string
		password string
	}{
		{"wrong password", "admin", "wrongpassword"},
		{"wrong username", "nonexistent", "password123"},
		{"both wrong", "wrong", "wrong"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := svc.Login(ctx, tt.username, tt.password, "")
			if err != ErrInvalidCredentials {
				t.Errorf("expected ErrInvalidCredentials, got: %v", err)
			}
		})
	}
}

func TestHandler_SetupCheck(t *testing.T) {
	svc, _ := setupTestService(t)
	handler := NewHandler(svc, false)

	req := httptest.NewRequest(http.MethodGet, "/api/auth/setup", nil)
	rec := httptest.NewRecorder()

	handler.HandleSetupCheck(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"needsSetup":true`) {
		t.Errorf("expected needsSetup=true, got: %s", rec.Body.String())
	}
}

func TestHandler_SetupAndLogin(t *testing.T) {
	svc, _ := setupTestService(t)
	handler := NewHandler(svc, false)

	// Setup
	body := `{"username":"admin","email":"admin@test.com","password":"password123"}`
	req := httptest.NewRequest(http.MethodPost, "/api/auth/setup", strings.NewReader(body))
	rec := httptest.NewRecorder()

	handler.HandleSetup(rec, req)

	if rec.Code != http.StatusCreated {
		t.Errorf("expected status 201, got %d: %s", rec.Code, rec.Body.String())
	}

	// Login
	body = `{"username":"admin","password":"password123"}`
	req = httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(body))
	rec = httptest.NewRecorder()

	handler.HandleLogin(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}

	// Check session cookie was set
	cookies := rec.Result().Cookies()
	var sessionCookie *http.Cookie
	for _, c := range cookies {
		if c.Name == cookieName {
			sessionCookie = c
		}
	}
	if sessionCookie == nil {
		t.Fatal("expected session cookie to be set")
	}
	if !sessionCookie.HttpOnly {
		t.Error("expected HttpOnly cookie")
	}

	// Access /me with session cookie
	req = httptest.NewRequest(http.MethodGet, "/api/auth/me", nil)
	req.AddCookie(sessionCookie)
	rec = httptest.NewRecorder()

	handler.HandleMe(rec, req)

	// HandleMe uses UserFromContext, so we need to go through middleware
	middleware := NewMiddleware(svc)
	req = httptest.NewRequest(http.MethodGet, "/api/auth/me", nil)
	req.AddCookie(sessionCookie)
	rec = httptest.NewRecorder()

	middleware.RequireAuth(http.HandlerFunc(handler.HandleMe)).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"username":"admin"`) {
		t.Errorf("expected admin user in response, got: %s", rec.Body.String())
	}
}

func TestHandler_SetupValidation(t *testing.T) {
	svc, _ := setupTestService(t)
	handler := NewHandler(svc, false)

	tests := []struct {
		name   string
		body   string
		status int
	}{
		{"empty body", `{}`, http.StatusBadRequest},
		{"missing password", `{"username":"a","email":"a@b.com"}`, http.StatusBadRequest},
		{"short password", `{"username":"a","email":"a@b.com","password":"short"}`, http.StatusBadRequest},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/auth/setup", strings.NewReader(tt.body))
			rec := httptest.NewRecorder()
			handler.HandleSetup(rec, req)
			if rec.Code != tt.status {
				t.Errorf("expected status %d, got %d: %s", tt.status, rec.Code, rec.Body.String())
			}
		})
	}
}

func TestMiddleware_RequireAuth_NoSession(t *testing.T) {
	svc, _ := setupTestService(t)
	middleware := NewMiddleware(svc)

	called := false
	handler := middleware.RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected status 401, got %d", rec.Code)
	}
	if called {
		t.Error("handler should not have been called")
	}
}

func TestMiddleware_RequireAdmin_NonAdmin(t *testing.T) {
	svc, db := setupTestService(t)
	ctx := context.Background()

	// Create admin via setup, then create a non-admin user
	_, err := svc.Setup(ctx, "admin", "admin@test.com", "password123")
	if err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	// Create non-admin user directly
	userRepo := NewUserRepository(db)
	nonAdmin := &User{Username: "regular", Email: "regular@test.com", Password: "hash", IsAdmin: false}
	if err := userRepo.Create(ctx, nonAdmin); err != nil {
		t.Fatalf("creating user: %v", err)
	}

	// Login as non-admin
	sessionRepo := NewSessionRepository(db)
	session := &Session{UserID: nonAdmin.ID, ExpiresAt: time.Now().Add(sessionDuration)}
	if err := sessionRepo.Create(ctx, session); err != nil {
		t.Fatalf("creating session: %v", err)
	}

	middleware := NewMiddleware(svc)
	called := false
	handler := middleware.RequireAdmin(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/admin/test", nil)
	req.AddCookie(&http.Cookie{Name: cookieName, Value: session.Token})
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("expected status 403, got %d: %s", rec.Code, rec.Body.String())
	}
	if called {
		t.Error("handler should not have been called for non-admin")
	}
}

func TestMiddleware_RequireAdmin_Admin(t *testing.T) {
	svc, _ := setupTestService(t)
	ctx := context.Background()

	_, err := svc.Setup(ctx, "admin", "admin@test.com", "password123")
	if err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	session, err := svc.Login(ctx, "admin", "password123", "")
	if err != nil {
		t.Fatalf("login failed: %v", err)
	}

	middleware := NewMiddleware(svc)
	called := false
	handler := middleware.RequireAdmin(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/admin/test", nil)
	req.AddCookie(&http.Cookie{Name: cookieName, Value: session.Token})
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if !called {
		t.Error("handler should have been called for admin")
	}
}
