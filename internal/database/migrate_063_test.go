package database

import (
	"path/filepath"
	"slices"
	"testing"
	"time"
)

func TestMigration063GivesProjectsTheirWorkspaceServices(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "devpad.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	migrateBefore(t, db, "063")

	created := time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)
	acceptance := `{"userId":1,"acceptedAt":"2026-11-01T09:00:00Z","termsUrl":"https://go.microsoft.com/fwlink/?LinkId=746388","image":"mcr.microsoft.com/mssql/server:2025-latest"}`
	for _, stmt := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO users (id, username, email, password) VALUES (1, 'owner', 'owner@example.com', 'x')`, nil},
		{`INSERT INTO projects (id, user_id, name, status) VALUES (1, 1, 'shop', 'ready')`, nil},
		{`INSERT INTO projects (id, user_id, name, status) VALUES (2, 1, 'blog', 'ready')`, nil},
		// The project workspace, a task sandbox, and a standalone workspace.
		{`INSERT INTO workspaces (id, user_id, name, project_id, kind) VALUES (1, 1, 'shop', 1, 'interactive')`, nil},
		{`INSERT INTO workspaces (id, user_id, name, project_id, kind, branch) VALUES (2, 1, 'shop: cart', 1, 'task', 'cart')`, nil},
		{`INSERT INTO workspaces (id, user_id, name) VALUES (3, 1, 'old')`, nil},
		{`INSERT INTO workspace_services (workspace_id, service_type, volume_name, status, config, created_at, updated_at)
		  VALUES (1, 'postgres', 'v1', 'running', '{"image":"postgres:17"}', ?, ?)`, []any{created, created}},
		{`INSERT INTO workspace_services (workspace_id, service_type, volume_name, status, config, created_at, updated_at)
		  VALUES (1, 'sqlserver', 'v2', 'stopped', '{"image":"mcr.microsoft.com/mssql/server:2025-latest","licenseAcceptance":` + acceptance + `}', ?, ?)`, []any{created, created}},
		{`INSERT INTO workspace_services (workspace_id, service_type, volume_name, status, config, created_at, updated_at)
		  VALUES (2, 'mongodb', 'v3', 'running', '{}', ?, ?)`, []any{created, created}},
		{`INSERT INTO workspace_services (workspace_id, service_type, volume_name, status, config, created_at, updated_at)
		  VALUES (3, 'mariadb', 'v4', 'running', '{}', ?, ?)`, []any{created, created}},
	} {
		if _, err := db.conn.Exec(stmt.sql, stmt.args...); err != nil {
			t.Fatalf("%s: %v", stmt.sql, err)
		}
	}

	if err := db.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	rows, err := db.conn.Query(`SELECT project_id, service_type, COALESCE(license_acceptance, '') FROM project_services ORDER BY project_id, service_type`)
	if err != nil {
		t.Fatalf("listing project services: %v", err)
	}
	defer rows.Close()
	var got []string
	var gotAcceptance string
	for rows.Next() {
		var projectID int64
		var serviceType, accepted string
		if err := rows.Scan(&projectID, &serviceType, &accepted); err != nil {
			t.Fatalf("scanning: %v", err)
		}
		got = append(got, serviceType)
		if serviceType == "sqlserver" {
			gotAcceptance = accepted
		} else if accepted != "" {
			t.Errorf("%s: expected no licence acceptance, got %s", serviceType, accepted)
		}
	}
	// Only the project workspace's services: never a sandbox's or a
	// standalone workspace's.
	if want := []string{"postgres", "sqlserver"}; !slices.Equal(got, want) {
		t.Fatalf("expected project services %v, got %v", want, got)
	}
	if gotAcceptance != acceptance {
		t.Fatalf("expected the licence acceptance to be kept, got %s", gotAcceptance)
	}

	if _, err := db.conn.Exec(`INSERT INTO project_services (project_id, service_type, created_at) VALUES (1, 'postgres', ?)`, created); !IsUniqueViolation(err) {
		t.Fatalf("expected a duplicate to be a unique violation, got %v", err)
	}
	if _, err := db.conn.Exec(`INSERT INTO project_services (project_id, service_type, created_at) VALUES (2, 'postgres', ?)`, created); err != nil {
		t.Fatalf("adding a service to project 2: %v", err)
	}
	if _, err := db.conn.Exec(`DELETE FROM projects WHERE id = 2`); err != nil {
		t.Fatalf("deleting project: %v", err)
	}
	var left int
	if err := db.conn.QueryRow(`SELECT COUNT(*) FROM project_services WHERE project_id = 2`).Scan(&left); err != nil || left != 0 {
		t.Fatalf("expected deleting the project to delete its services, %d left (%v)", left, err)
	}
}
