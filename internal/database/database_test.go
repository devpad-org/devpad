package database

import (
	"path/filepath"
	"testing"
)

func TestOpenConfiguresSQLiteForConcurrentUse(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "devpad.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	if got := db.Conn().Stats().MaxOpenConnections; got != 1 {
		t.Fatalf("expected one open SQLite connection, got %d", got)
	}

	var busyTimeout int
	if err := db.Conn().QueryRow("PRAGMA busy_timeout").Scan(&busyTimeout); err != nil {
		t.Fatalf("query busy_timeout: %v", err)
	}
	if want := int(sqliteBusyTimeout.Milliseconds()); busyTimeout != want {
		t.Fatalf("expected busy_timeout %d, got %d", want, busyTimeout)
	}

	var foreignKeys int
	if err := db.Conn().QueryRow("PRAGMA foreign_keys").Scan(&foreignKeys); err != nil {
		t.Fatalf("query foreign_keys: %v", err)
	}
	if foreignKeys != 1 {
		t.Fatalf("expected foreign_keys enabled, got %d", foreignKeys)
	}
}

func TestMigrateAllowsMeilisearchWorkspaceService(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "devpad.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	if err := db.Migrate(); err != nil {
		t.Fatalf("migrate db: %v", err)
	}

	if _, err := db.Conn().Exec(`INSERT INTO users (id, username, email, password) VALUES (1, 'test', 'test@example.com', 'hashed')`); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	if _, err := db.Conn().Exec(`INSERT INTO workspaces (id, user_id, name) VALUES (1, 1, 'workspace')`); err != nil {
		t.Fatalf("insert workspace: %v", err)
	}
	if _, err := db.Conn().Exec(`INSERT INTO workspace_services (workspace_id, service_type, status, config) VALUES (1, 'meilisearch', 'stopped', '{}')`); err != nil {
		t.Fatalf("insert Meilisearch workspace service: %v", err)
	}
}
