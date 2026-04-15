package admin

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/devpad-org/devpad/internal/auth"
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

func setupTestService(t *testing.T) (Service, auth.UserRepository) {
	t.Helper()
	db := setupTestDB(t)
	userRepo := auth.NewUserRepository(db)
	svc := NewService(userRepo)
	return svc, userRepo
}

func seedAdmin(t *testing.T, svc Service, ctx context.Context) *auth.User {
	t.Helper()
	user, err := svc.CreateUser(ctx, "admin", "admin@test.com", "password123", true)
	if err != nil {
		t.Fatalf("creating admin: %v", err)
	}
	return user
}

func TestService_ListUsers(t *testing.T) {
	svc, _ := setupTestService(t)
	ctx := context.Background()

	// Empty initially
	users, err := svc.ListUsers(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(users) != 0 {
		t.Fatalf("expected 0 users, got %d", len(users))
	}

	// Create users
	seedAdmin(t, svc, ctx)
	if _, err := svc.CreateUser(ctx, "user1", "user1@test.com", "password123", false); err != nil {
		t.Fatalf("creating user: %v", err)
	}

	users, err = svc.ListUsers(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(users) != 2 {
		t.Fatalf("expected 2 users, got %d", len(users))
	}
}

func TestService_CreateUser(t *testing.T) {
	svc, _ := setupTestService(t)
	ctx := context.Background()

	user, err := svc.CreateUser(ctx, "newuser", "new@test.com", "password123", false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if user.Username != "newuser" {
		t.Errorf("expected username 'newuser', got %q", user.Username)
	}
	if user.IsAdmin {
		t.Error("expected non-admin user")
	}
	if user.ID == 0 {
		t.Error("expected non-zero user ID")
	}
}

func TestService_UpdateUser(t *testing.T) {
	svc, _ := setupTestService(t)
	ctx := context.Background()

	user := seedAdmin(t, svc, ctx)

	updated, err := svc.UpdateUser(ctx, user.ID, "renamed", "new@test.com", true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if updated.Username != "renamed" {
		t.Errorf("expected username 'renamed', got %q", updated.Username)
	}
	if updated.Email != "new@test.com" {
		t.Errorf("expected email 'new@test.com', got %q", updated.Email)
	}
}

func TestService_UpdateUser_NotFound(t *testing.T) {
	svc, _ := setupTestService(t)
	ctx := context.Background()

	_, err := svc.UpdateUser(ctx, 999, "x", "x@test.com", false)
	if err != ErrUserNotFound {
		t.Fatalf("expected ErrUserNotFound, got: %v", err)
	}
}

func TestService_ResetPassword(t *testing.T) {
	svc, _ := setupTestService(t)
	ctx := context.Background()

	user := seedAdmin(t, svc, ctx)

	if err := svc.ResetPassword(ctx, user.ID, "newpassword123"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestService_ResetPassword_NotFound(t *testing.T) {
	svc, _ := setupTestService(t)
	ctx := context.Background()

	err := svc.ResetPassword(ctx, 999, "newpassword123")
	if err != ErrUserNotFound {
		t.Fatalf("expected ErrUserNotFound, got: %v", err)
	}
}

func TestService_DeleteUser(t *testing.T) {
	svc, _ := setupTestService(t)
	ctx := context.Background()

	admin := seedAdmin(t, svc, ctx)
	user, _ := svc.CreateUser(ctx, "user1", "user1@test.com", "password123", false)

	// Delete non-self user
	if err := svc.DeleteUser(ctx, admin.ID, user.ID); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify deleted
	users, _ := svc.ListUsers(ctx)
	if len(users) != 1 {
		t.Fatalf("expected 1 user remaining, got %d", len(users))
	}
}

func TestService_DeleteUser_SelfDelete(t *testing.T) {
	svc, _ := setupTestService(t)
	ctx := context.Background()

	admin := seedAdmin(t, svc, ctx)

	err := svc.DeleteUser(ctx, admin.ID, admin.ID)
	if err != ErrSelfDelete {
		t.Fatalf("expected ErrSelfDelete, got: %v", err)
	}
}

func TestService_DeleteUser_NotFound(t *testing.T) {
	svc, _ := setupTestService(t)
	ctx := context.Background()

	admin := seedAdmin(t, svc, ctx)

	err := svc.DeleteUser(ctx, admin.ID, 999)
	if err != ErrUserNotFound {
		t.Fatalf("expected ErrUserNotFound, got: %v", err)
	}
}

// Handler tests

func setupTestHandler(t *testing.T) (*Handler, Service) {
	t.Helper()
	svc, _ := setupTestService(t)
	handler := NewHandler(svc)
	return handler, svc
}

func withAdminContext(r *http.Request, user *auth.User) *http.Request {
	ctx := context.WithValue(r.Context(), auth.UserContextKey, user)
	return r.WithContext(ctx)
}

func TestHandler_ListUsers(t *testing.T) {
	handler, svc := setupTestHandler(t)
	ctx := context.Background()

	seedAdmin(t, svc, ctx)

	req := httptest.NewRequest(http.MethodGet, "/api/admin/users", nil)
	rec := httptest.NewRecorder()

	handler.HandleListUsers(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Users []map[string]any `json:"users"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if len(resp.Users) != 1 {
		t.Errorf("expected 1 user, got %d", len(resp.Users))
	}
}

func TestHandler_CreateUser(t *testing.T) {
	handler, _ := setupTestHandler(t)

	body := `{"username":"newuser","email":"new@test.com","password":"password123","isAdmin":false}`
	req := httptest.NewRequest(http.MethodPost, "/api/admin/users", strings.NewReader(body))
	rec := httptest.NewRecorder()

	handler.HandleCreateUser(rec, req)

	if rec.Code != http.StatusCreated {
		t.Errorf("expected status 201, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHandler_CreateUser_Validation(t *testing.T) {
	handler, _ := setupTestHandler(t)

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
			req := httptest.NewRequest(http.MethodPost, "/api/admin/users", strings.NewReader(tt.body))
			rec := httptest.NewRecorder()
			handler.HandleCreateUser(rec, req)
			if rec.Code != tt.status {
				t.Errorf("expected status %d, got %d: %s", tt.status, rec.Code, rec.Body.String())
			}
		})
	}
}

func TestHandler_DeleteUser_SelfProtection(t *testing.T) {
	handler, svc := setupTestHandler(t)
	ctx := context.Background()

	admin := seedAdmin(t, svc, ctx)

	req := httptest.NewRequest(http.MethodDelete, "/api/admin/users/"+fmt.Sprint(admin.ID), nil)
	req.SetPathValue("id", fmt.Sprint(admin.ID))
	req = withAdminContext(req, admin)
	rec := httptest.NewRecorder()

	handler.HandleDeleteUser(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d: %s", rec.Code, rec.Body.String())
	}
}
