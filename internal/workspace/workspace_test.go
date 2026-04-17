package workspace

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/devpad-org/devpad/internal/auth"
	"github.com/devpad-org/devpad/internal/container"
	_ "github.com/mattn/go-sqlite3"
)

// mockContainerManager is a test double for container.Manager.
type mockContainerManager struct {
	lastCreatedID string
}

func (m *mockContainerManager) Create(_ context.Context, name, volumeName string, env []string) (string, error) {
	m.lastCreatedID = "mock-container-" + name
	return m.lastCreatedID, nil
}
func (m *mockContainerManager) Start(_ context.Context, _ string) error   { return nil }
func (m *mockContainerManager) Stop(_ context.Context, _ string) error    { return nil }
func (m *mockContainerManager) Restart(_ context.Context, _ string) error { return nil }
func (m *mockContainerManager) Remove(_ context.Context, _ string) error  { return nil }
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
	return "mock-exec-id", nil
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
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);
	INSERT INTO users (id, username, email, password) VALUES (1, 'testuser', 'test@test.com', 'hashed');
	INSERT INTO users (id, username, email, password) VALUES (2, 'otheruser', 'other@test.com', 'hashed');`
	if _, err := db.Exec(schema); err != nil {
		t.Fatalf("creating schema: %v", err)
	}
	return db
}

func setupTestService(t *testing.T) (Service, *sql.DB) {
	t.Helper()
	db := setupTestDB(t)
	repo := NewRepository(db)
	cm := &mockContainerManager{}
	svc := NewService(repo, cm)
	return svc, db
}

func testUser(id int64) *auth.User {
	return &auth.User{ID: id, Username: "testuser"}
}

func authedRequest(r *http.Request, user *auth.User) *http.Request {
	ctx := context.WithValue(r.Context(), auth.UserContextKey, user)
	return r.WithContext(ctx)
}

func TestService_CreateAndGet(t *testing.T) {
	svc, _ := setupTestService(t)
	ctx := context.Background()

	ws, err := svc.Create(ctx, 1, "My Project", "A test workspace")
	if err != nil {
		t.Fatalf("create failed: %v", err)
	}
	if ws.Name != "My Project" {
		t.Errorf("expected name 'My Project', got %q", ws.Name)
	}
	if ws.Description != "A test workspace" {
		t.Errorf("expected description 'A test workspace', got %q", ws.Description)
	}
	if ws.Status != StatusRunning {
		t.Errorf("expected status 'running', got %q", ws.Status)
	}
	if ws.ID == 0 {
		t.Error("expected non-zero ID")
	}

	got, err := svc.Get(ctx, 1, ws.ID)
	if err != nil {
		t.Fatalf("get failed: %v", err)
	}
	if got.Name != ws.Name {
		t.Errorf("expected name %q, got %q", ws.Name, got.Name)
	}
}

func TestService_GetNotFound(t *testing.T) {
	svc, _ := setupTestService(t)
	ctx := context.Background()

	_, err := svc.Get(ctx, 1, 999)
	if err != ErrNotFound {
		t.Fatalf("expected ErrNotFound, got: %v", err)
	}
}

func TestService_GetForbidden(t *testing.T) {
	svc, _ := setupTestService(t)
	ctx := context.Background()

	ws, err := svc.Create(ctx, 1, "User1 Project", "")
	if err != nil {
		t.Fatalf("create failed: %v", err)
	}

	_, err = svc.Get(ctx, 2, ws.ID)
	if err != ErrForbidden {
		t.Fatalf("expected ErrForbidden, got: %v", err)
	}
}

func TestService_List(t *testing.T) {
	svc, _ := setupTestService(t)
	ctx := context.Background()

	// Create workspaces for user 1
	if _, err := svc.Create(ctx, 1, "Project A", ""); err != nil {
		t.Fatalf("create failed: %v", err)
	}
	if _, err := svc.Create(ctx, 1, "Project B", ""); err != nil {
		t.Fatalf("create failed: %v", err)
	}
	// Create workspace for user 2
	if _, err := svc.Create(ctx, 2, "Other Project", ""); err != nil {
		t.Fatalf("create failed: %v", err)
	}

	list, err := svc.List(ctx, 1)
	if err != nil {
		t.Fatalf("list failed: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("expected 2 workspaces, got %d", len(list))
	}
}

func TestService_Update(t *testing.T) {
	svc, _ := setupTestService(t)
	ctx := context.Background()

	ws, err := svc.Create(ctx, 1, "Original", "Original desc")
	if err != nil {
		t.Fatalf("create failed: %v", err)
	}

	updated, err := svc.Update(ctx, 1, ws.ID, "Updated", "Updated desc")
	if err != nil {
		t.Fatalf("update failed: %v", err)
	}
	if updated.Name != "Updated" {
		t.Errorf("expected name 'Updated', got %q", updated.Name)
	}
	if updated.Description != "Updated desc" {
		t.Errorf("expected description 'Updated desc', got %q", updated.Description)
	}
}

func TestService_UpdateForbidden(t *testing.T) {
	svc, _ := setupTestService(t)
	ctx := context.Background()

	ws, _ := svc.Create(ctx, 1, "Project", "")
	_, err := svc.Update(ctx, 2, ws.ID, "Hacked", "")
	if err != ErrForbidden {
		t.Fatalf("expected ErrForbidden, got: %v", err)
	}
}

func TestService_Delete(t *testing.T) {
	svc, _ := setupTestService(t)
	ctx := context.Background()

	ws, _ := svc.Create(ctx, 1, "ToDelete", "")
	if err := svc.Delete(ctx, 1, ws.ID); err != nil {
		t.Fatalf("delete failed: %v", err)
	}

	_, err := svc.Get(ctx, 1, ws.ID)
	if err != ErrNotFound {
		t.Fatalf("expected ErrNotFound after delete, got: %v", err)
	}
}

func TestService_DeleteForbidden(t *testing.T) {
	svc, _ := setupTestService(t)
	ctx := context.Background()

	ws, _ := svc.Create(ctx, 1, "Project", "")
	err := svc.Delete(ctx, 2, ws.ID)
	if err != ErrForbidden {
		t.Fatalf("expected ErrForbidden, got: %v", err)
	}
}

func TestHandler_CreateAndList(t *testing.T) {
	svc, _ := setupTestService(t)
	handler := NewHandler(svc, nil)

	// Create
	body := `{"name":"Test Project","description":"A test"}`
	req := httptest.NewRequest(http.MethodPost, "/api/workspaces", strings.NewReader(body))
	req = authedRequest(req, testUser(1))
	rec := httptest.NewRecorder()
	handler.HandleCreate(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected status 201, got %d: %s", rec.Code, rec.Body.String())
	}

	var createResp map[string]any
	json.NewDecoder(rec.Body).Decode(&createResp)
	ws := createResp["workspace"].(map[string]any)
	if ws["name"] != "Test Project" {
		t.Errorf("expected name 'Test Project', got %v", ws["name"])
	}

	// List
	req = httptest.NewRequest(http.MethodGet, "/api/workspaces", nil)
	req = authedRequest(req, testUser(1))
	rec = httptest.NewRecorder()
	handler.HandleList(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}

	var listResp map[string]any
	json.NewDecoder(rec.Body).Decode(&listResp)
	items := listResp["workspaces"].([]any)
	if len(items) != 1 {
		t.Errorf("expected 1 workspace, got %d", len(items))
	}
}

func TestHandler_CreateValidation(t *testing.T) {
	svc, _ := setupTestService(t)
	handler := NewHandler(svc, nil)

	body := `{"name":"","description":"no name"}`
	req := httptest.NewRequest(http.MethodPost, "/api/workspaces", strings.NewReader(body))
	req = authedRequest(req, testUser(1))
	rec := httptest.NewRecorder()
	handler.HandleCreate(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", rec.Code)
	}
}

func TestHandler_GetNotFound(t *testing.T) {
	svc, _ := setupTestService(t)
	handler := NewHandler(svc, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/999", nil)
	req.SetPathValue("id", "999")
	req = authedRequest(req, testUser(1))
	rec := httptest.NewRecorder()
	handler.HandleGet(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected status 404, got %d", rec.Code)
	}
}

func TestHandler_Delete(t *testing.T) {
	svc, _ := setupTestService(t)
	handler := NewHandler(svc, nil)
	ctx := context.Background()

	ws, _ := svc.Create(ctx, 1, "ToDelete", "")

	req := httptest.NewRequest(http.MethodDelete, "/api/workspaces/1", nil)
	req.SetPathValue("id", "1")
	req = authedRequest(req, testUser(1))
	rec := httptest.NewRecorder()
	handler.HandleDelete(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rec.Code)
	}

	_, err := svc.Get(ctx, 1, ws.ID)
	if err != ErrNotFound {
		t.Errorf("expected workspace to be deleted, got: %v", err)
	}
}
