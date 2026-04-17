package preview

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/devpad-org/devpad/internal/auth"
	"github.com/devpad-org/devpad/internal/container"
	"github.com/devpad-org/devpad/internal/workspace"
	_ "github.com/mattn/go-sqlite3"
)

// mockContainerManager is a test double for container.Manager.
type mockContainerManager struct{}

func (m *mockContainerManager) Create(_ context.Context, _, _ string, _ []string, _, _ int64) (string, error) {
	return "mock-container", nil
}
func (m *mockContainerManager) Start(_ context.Context, _ string) error   { return nil }
func (m *mockContainerManager) Stop(_ context.Context, _ string) error    { return nil }
func (m *mockContainerManager) Restart(_ context.Context, _ string) error { return nil }
func (m *mockContainerManager) Remove(_ context.Context, _ string) error  { return nil }
func (m *mockContainerManager) UpdateResources(_ context.Context, _ string, _, _ int64) error {
	return nil
}
func (m *mockContainerManager) GetIP(_ context.Context, _ string) (string, error) {
	return "172.17.0.2", nil
}
func (m *mockContainerManager) GetEnv(_ context.Context, _ string) ([]string, error) {
	return nil, nil
}
func (m *mockContainerManager) Stats(_ context.Context, _ string) (*container.ContainerStats, error) {
	return &container.ContainerStats{}, nil
}
func (m *mockContainerManager) CreateVolume(_ context.Context, _ string) error { return nil }
func (m *mockContainerManager) RemoveVolume(_ context.Context, _ string) error { return nil }
func (m *mockContainerManager) Exec(_ context.Context, _ string, _ []string) (string, error) {
	return "", nil
}
func (m *mockContainerManager) ExecAttach(_ context.Context, _ string) (container.HijackedResponse, error) {
	return container.HijackedResponse{}, nil
}
func (m *mockContainerManager) ExecResize(_ context.Context, _ string, _, _ uint) error {
	return nil
}
func (m *mockContainerManager) CopyFileToContainer(_ context.Context, _, _ string, _ []byte, _ int64) error {
	return nil
}

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
	CREATE TABLE workspaces (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		name TEXT NOT NULL,
		description TEXT NOT NULL DEFAULT '',
		status TEXT NOT NULL DEFAULT 'stopped' CHECK(status IN ('creating','running','stopped')),
		container_id TEXT NOT NULL DEFAULT '',
		volume_name TEXT NOT NULL DEFAULT '',
		agent_token TEXT NOT NULL DEFAULT '',
		memory_limit INTEGER NOT NULL DEFAULT 2147483648,
		nano_cpus INTEGER NOT NULL DEFAULT 2000000000,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);
	CREATE TABLE preview_tokens (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		token TEXT NOT NULL UNIQUE,
		user_id INTEGER NOT NULL,
		workspace_id INTEGER NOT NULL,
		port INTEGER NOT NULL,
		used INTEGER NOT NULL DEFAULT 0,
		expires_at DATETIME NOT NULL,
		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE,
		FOREIGN KEY (workspace_id) REFERENCES workspaces(id) ON DELETE CASCADE
	);
	CREATE INDEX idx_preview_tokens_token ON preview_tokens(token);
	INSERT INTO users (id, username, email, password) VALUES (1, 'testuser', 'test@test.com', 'hashed');
	INSERT INTO users (id, username, email, password) VALUES (2, 'otheruser', 'other@test.com', 'hashed');`
	if _, err := db.Exec(schema); err != nil {
		t.Fatalf("creating schema: %v", err)
	}
	return db
}

func createRunningWorkspace(t *testing.T, db *sql.DB, userID int64, name string) int64 {
	t.Helper()
	result, err := db.Exec(
		`INSERT INTO workspaces (user_id, name, status, container_id, volume_name) VALUES (?, ?, 'running', 'ctr-123', 'vol-123')`,
		userID, name,
	)
	if err != nil {
		t.Fatalf("inserting workspace: %v", err)
	}
	id, _ := result.LastInsertId()
	return id
}

func createStoppedWorkspace(t *testing.T, db *sql.DB, userID int64, name string) int64 {
	t.Helper()
	result, err := db.Exec(
		`INSERT INTO workspaces (user_id, name, status) VALUES (?, ?, 'stopped')`,
		userID, name,
	)
	if err != nil {
		t.Fatalf("inserting workspace: %v", err)
	}
	id, _ := result.LastInsertId()
	return id
}

func setupTestService(t *testing.T) (Service, *sql.DB) {
	t.Helper()
	db := setupTestDB(t)
	repo := NewRepository(db)
	wsRepo := workspace.NewRepository(db)
	cm := &mockContainerManager{}
	svc := NewService(repo, wsRepo, cm)
	return svc, db
}

func setupTestHandler(t *testing.T) (*Handler, Service, *sql.DB) {
	t.Helper()
	svc, db := setupTestService(t)
	handler := NewHandler(svc, "preview.example.com", 443)
	return handler, svc, db
}

func testUser(id int64) *auth.User {
	return &auth.User{ID: id, Username: "testuser"}
}

func authedRequest(r *http.Request, user *auth.User) *http.Request {
	ctx := context.WithValue(r.Context(), auth.UserContextKey, user)
	return r.WithContext(ctx)
}

// --- Service Tests ---

func TestService_GenerateURL(t *testing.T) {
	svc, db := setupTestService(t)
	ctx := context.Background()

	wsID := createRunningWorkspace(t, db, 1, "myproject")

	url, err := svc.GenerateURL(ctx, 1, wsID, 3000, "preview.example.com", 443)
	if err != nil {
		t.Fatalf("GenerateURL failed: %v", err)
	}

	expectedPrefix := "https://"
	if !strings.HasPrefix(url, expectedPrefix) {
		t.Errorf("expected URL to start with %q, got %q", expectedPrefix, url)
	}
	if !strings.Contains(url, "preview.example.com") {
		t.Errorf("expected URL to contain preview domain, got %q", url)
	}
	if !strings.Contains(url, "token=") {
		t.Errorf("expected URL to contain token param, got %q", url)
	}
}

func TestService_GenerateURL_StoppedWorkspace(t *testing.T) {
	svc, db := setupTestService(t)
	ctx := context.Background()

	wsID := createStoppedWorkspace(t, db, 1, "stopped-project")

	_, err := svc.GenerateURL(ctx, 1, wsID, 3000, "preview.example.com", 443)
	if err == nil {
		t.Fatal("expected error for stopped workspace")
	}
	if !strings.Contains(err.Error(), "not running") {
		t.Errorf("expected 'not running' error, got: %v", err)
	}
}

func TestService_GenerateURL_WrongUser(t *testing.T) {
	svc, db := setupTestService(t)
	ctx := context.Background()

	wsID := createRunningWorkspace(t, db, 1, "user1project")

	_, err := svc.GenerateURL(ctx, 2, wsID, 3000, "preview.example.com", 443)
	if err == nil {
		t.Fatal("expected error for wrong user")
	}
}

func TestService_ValidateToken(t *testing.T) {
	svc, db := setupTestService(t)
	ctx := context.Background()

	wsID := createRunningWorkspace(t, db, 1, "tokentest")

	url, err := svc.GenerateURL(ctx, 1, wsID, 8080, "preview.example.com", 443)
	if err != nil {
		t.Fatalf("GenerateURL failed: %v", err)
	}

	// Extract token from URL.
	parts := strings.SplitN(url, "token=", 2)
	if len(parts) != 2 {
		t.Fatalf("could not extract token from URL: %s", url)
	}
	tokenStr := parts[1]

	tok, err := svc.ValidateToken(ctx, tokenStr)
	if err != nil {
		t.Fatalf("ValidateToken failed: %v", err)
	}
	if tok.WorkspaceID != wsID {
		t.Errorf("expected workspace ID %d, got %d", wsID, tok.WorkspaceID)
	}
	if tok.Port != 8080 {
		t.Errorf("expected port 8080, got %d", tok.Port)
	}

	// Second use should fail.
	_, err = svc.ValidateToken(ctx, tokenStr)
	if err == nil {
		t.Fatal("expected error on second token use")
	}
}

func TestService_ValidateToken_Invalid(t *testing.T) {
	svc, _ := setupTestService(t)
	ctx := context.Background()

	_, err := svc.ValidateToken(ctx, "nonexistent-token")
	if err == nil {
		t.Fatal("expected error for invalid token")
	}
}

func TestService_ResolveContainerAddr(t *testing.T) {
	svc, db := setupTestService(t)
	ctx := context.Background()

	wsID := createRunningWorkspace(t, db, 1, "addrtest")

	addr, _, err := svc.ResolveContainerAddr(ctx, wsID)
	if err != nil {
		t.Fatalf("ResolveContainerAddr failed: %v", err)
	}
	if addr != "172.17.0.2:9100" {
		t.Errorf("expected '172.17.0.2:9100', got %q", addr)
	}
}

func TestService_ResolveContainerAddr_StoppedWorkspace(t *testing.T) {
	svc, db := setupTestService(t)
	ctx := context.Background()

	wsID := createStoppedWorkspace(t, db, 1, "stopped-addr")

	_, _, err := svc.ResolveContainerAddr(ctx, wsID)
	if err == nil {
		t.Fatal("expected error for stopped workspace")
	}
}

// --- Handler Tests ---

func TestHandler_GenerateURL(t *testing.T) {
	handler, _, db := setupTestHandler(t)

	wsID := createRunningWorkspace(t, db, 1, "handler-test")

	body := `{"port": 3000}`
	r := httptest.NewRequest("POST", "/api/workspaces/1/preview", strings.NewReader(body))
	r.SetPathValue("id", "1")
	r = authedRequest(r, testUser(1))

	// Need to use the actual workspace ID
	r2 := httptest.NewRequest("POST", "/api/workspaces/"+strconv.FormatInt(wsID, 10)+"/preview", strings.NewReader(body))
	r2.SetPathValue("id", strconv.FormatInt(wsID, 10))
	r2 = authedRequest(r2, testUser(1))

	w := httptest.NewRecorder()
	handler.HandleGenerateURL(w, r2)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]string
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if resp["url"] == "" {
		t.Error("expected non-empty URL")
	}
}

func TestHandler_GenerateURL_InvalidPort(t *testing.T) {
	handler, _, _ := setupTestHandler(t)

	body := `{"port": 99999}`
	r := httptest.NewRequest("POST", "/api/workspaces/1/preview", strings.NewReader(body))
	r.SetPathValue("id", "1")
	r = authedRequest(r, testUser(1))

	w := httptest.NewRecorder()
	handler.HandleGenerateURL(w, r)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandler_GenerateURL_Unauthenticated(t *testing.T) {
	handler, _, _ := setupTestHandler(t)

	body := `{"port": 3000}`
	r := httptest.NewRequest("POST", "/api/workspaces/1/preview", strings.NewReader(body))
	r.SetPathValue("id", "1")

	w := httptest.NewRecorder()
	handler.HandleGenerateURL(w, r)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

func TestHandler_ParseSubdomain(t *testing.T) {
	handler, _, _ := setupTestHandler(t)

	tests := []struct {
		name    string
		host    string
		wantWS  int64
		wantP   int
		wantErr bool
	}{
		{"valid", "3-8080.preview.example.com", 3, 8080, false},
		{"valid with port", "5-3000.preview.example.com:443", 5, 3000, false},
		{"wrong domain", "3-8080.other.com", 0, 0, true},
		{"missing port", "3.preview.example.com", 0, 0, true},
		{"invalid ws id", "abc-8080.preview.example.com", 0, 0, true},
		{"invalid port", "3-abc.preview.example.com", 0, 0, true},
		{"port out of range", "3-99999.preview.example.com", 0, 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wsID, port, err := handler.parseSubdomain(tt.host)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got ws=%d port=%d", wsID, port)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if wsID != tt.wantWS {
				t.Errorf("workspace ID: got %d, want %d", wsID, tt.wantWS)
			}
			if port != tt.wantP {
				t.Errorf("port: got %d, want %d", port, tt.wantP)
			}
		})
	}
}

func TestHandler_CookieSignAndValidate(t *testing.T) {
	handler, _, _ := setupTestHandler(t)

	claims := previewClaims{
		userID:      1,
		workspaceID: 5,
		port:        3000,
		expiresAt:   time.Now().Add(1 * time.Hour),
	}

	cookie := handler.signCookie(claims)
	got, err := handler.validateCookie(cookie)
	if err != nil {
		t.Fatalf("validateCookie failed: %v", err)
	}
	if got.userID != 1 || got.workspaceID != 5 || got.port != 3000 {
		t.Errorf("claims mismatch: %+v", got)
	}
}

func TestHandler_CookieExpired(t *testing.T) {
	handler, _, _ := setupTestHandler(t)

	claims := previewClaims{
		userID:      1,
		workspaceID: 5,
		port:        3000,
		expiresAt:   time.Now().Add(-1 * time.Hour),
	}

	cookie := handler.signCookie(claims)
	_, err := handler.validateCookie(cookie)
	if err == nil {
		t.Fatal("expected error for expired cookie")
	}
}

func TestHandler_CookieTampered(t *testing.T) {
	handler, _, _ := setupTestHandler(t)

	claims := previewClaims{
		userID:      1,
		workspaceID: 5,
		port:        3000,
		expiresAt:   time.Now().Add(1 * time.Hour),
	}

	cookie := handler.signCookie(claims)
	// Tamper with the cookie.
	tampered := "2" + cookie[1:]

	_, err := handler.validateCookie(tampered)
	if err == nil {
		t.Fatal("expected error for tampered cookie")
	}
}

func TestHandler_ProxySubdomainNoAuth(t *testing.T) {
	handler, _, _ := setupTestHandler(t)

	r := httptest.NewRequest("GET", "/", nil)
	r.Host = "1-3000.preview.example.com"

	w := httptest.NewRecorder()
	handler.HandleProxy(w, r)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d: %s", w.Code, w.Body.String())
	}
}

// --- Repository Tests ---

func TestRepository_CreateAndGetByToken(t *testing.T) {
	db := setupTestDB(t)
	createRunningWorkspace(t, db, 1, "repo-test")
	repo := NewRepository(db)
	ctx := context.Background()

	tok := &Token{
		UserID:      1,
		WorkspaceID: 1,
		Port:        3000,
		ExpiresAt:   time.Now().Add(30 * time.Second),
	}
	if err := repo.Create(ctx, tok); err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	if tok.Token == "" {
		t.Fatal("expected non-empty token")
	}
	if tok.ID == 0 {
		t.Fatal("expected non-zero ID")
	}

	got, err := repo.GetByToken(ctx, tok.Token)
	if err != nil {
		t.Fatalf("GetByToken failed: %v", err)
	}
	if got == nil {
		t.Fatal("expected token, got nil")
	}
	if got.Port != 3000 {
		t.Errorf("expected port 3000, got %d", got.Port)
	}
}

func TestRepository_GetByToken_Expired(t *testing.T) {
	db := setupTestDB(t)
	createRunningWorkspace(t, db, 1, "expired-test")
	repo := NewRepository(db)
	ctx := context.Background()

	tok := &Token{
		UserID:      1,
		WorkspaceID: 1,
		Port:        3000,
		ExpiresAt:   time.Now().Add(-1 * time.Second),
	}
	if err := repo.Create(ctx, tok); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	got, err := repo.GetByToken(ctx, tok.Token)
	if err != nil {
		t.Fatalf("GetByToken failed: %v", err)
	}
	if got != nil {
		t.Fatal("expected nil for expired token")
	}
}

func TestRepository_MarkUsed(t *testing.T) {
	db := setupTestDB(t)
	createRunningWorkspace(t, db, 1, "used-test")
	repo := NewRepository(db)
	ctx := context.Background()

	tok := &Token{
		UserID:      1,
		WorkspaceID: 1,
		Port:        3000,
		ExpiresAt:   time.Now().Add(30 * time.Second),
	}
	if err := repo.Create(ctx, tok); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	if err := repo.MarkUsed(ctx, tok.ID); err != nil {
		t.Fatalf("MarkUsed failed: %v", err)
	}

	got, err := repo.GetByToken(ctx, tok.Token)
	if err != nil {
		t.Fatalf("GetByToken failed: %v", err)
	}
	if got == nil {
		t.Fatal("expected token, got nil")
	}
	if !got.Used {
		t.Error("expected token to be marked as used")
	}
}

func TestRepository_DeleteExpired(t *testing.T) {
	db := setupTestDB(t)
	createRunningWorkspace(t, db, 1, "cleanup-test")
	repo := NewRepository(db)
	ctx := context.Background()

	// Create an expired token.
	tok := &Token{
		UserID:      1,
		WorkspaceID: 1,
		Port:        3000,
		ExpiresAt:   time.Now().Add(-1 * time.Minute),
	}
	if err := repo.Create(ctx, tok); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	if err := repo.DeleteExpired(ctx); err != nil {
		t.Fatalf("DeleteExpired failed: %v", err)
	}

	// Verify it's gone by querying directly (GetByToken already filters by expiry).
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM preview_tokens WHERE id = ?", tok.ID).Scan(&count); err != nil {
		t.Fatalf("counting: %v", err)
	}
	if count != 0 {
		t.Errorf("expected 0 tokens, got %d", count)
	}
}
