package database

import (
	"path/filepath"
	"strings"
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

func TestMigrateAddsWaitingUserStatusToExistingAgentRuns(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "devpad.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	createPreWaitingUserAgentRunSchema(t, db)
	markMigrationsAppliedBefore(t, db, "023_add_waiting_user_agent_run_status")
	if _, err := db.Conn().Exec(`INSERT INTO users (id, username, email, password) VALUES (1, 'test', 'test@example.com', 'hashed')`); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	if _, err := db.Conn().Exec(`INSERT INTO workspaces (id, user_id, name) VALUES (1, 1, 'workspace')`); err != nil {
		t.Fatalf("insert workspace: %v", err)
	}
	if _, err := db.Conn().Exec(`INSERT INTO ai_conversations (id, user_id, workspace_id, title, model) VALUES (1, 1, 1, 'Chat', 'gpt-5.4')`); err != nil {
		t.Fatalf("insert conversation: %v", err)
	}
	if _, err := db.Conn().Exec(`
		INSERT INTO ai_agent_runs (
			id, user_id, workspace_id, conversation_id, agent_id, model, status, error, input_turns, thinking
		)
		VALUES (1, 1, 1, 1, 'default', 'gpt-5.4', 'running', '', '[]', '')
	`); err != nil {
		t.Fatalf("insert agent run: %v", err)
	}
	if _, err := db.Conn().Exec(`INSERT INTO ai_agent_run_events (id, run_id, sequence, event_json) VALUES (1, 1, 1, '{"content":"hello"}')`); err != nil {
		t.Fatalf("insert agent run event: %v", err)
	}

	if err := db.Migrate(); err != nil {
		t.Fatalf("migrate db: %v", err)
	}

	if _, err := db.Conn().Exec(`UPDATE ai_agent_runs SET status = 'waiting_user' WHERE id = 1`); err != nil {
		t.Fatalf("update waiting_user status: %v", err)
	}
	var eventJSON string
	if err := db.Conn().QueryRow(`SELECT event_json FROM ai_agent_run_events WHERE run_id = 1 AND sequence = 1`).Scan(&eventJSON); err != nil {
		t.Fatalf("query preserved event: %v", err)
	}
	if eventJSON != `{"content":"hello"}` {
		t.Fatalf("unexpected preserved event json: %s", eventJSON)
	}
}

func createPreWaitingUserAgentRunSchema(t *testing.T, db *DB) {
	t.Helper()
	_, err := db.Conn().Exec(`
		CREATE TABLE users (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			username TEXT NOT NULL,
			email TEXT NOT NULL,
			password TEXT NOT NULL
		);
		CREATE TABLE workspaces (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			name TEXT NOT NULL
		);
		CREATE TABLE ai_conversations (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			workspace_id INTEGER NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
			title TEXT NOT NULL,
			model TEXT NOT NULL
		);
		CREATE TABLE ai_agent_runs (
			id              INTEGER PRIMARY KEY AUTOINCREMENT,
			parent_run_id   INTEGER REFERENCES ai_agent_runs(id) ON DELETE SET NULL,
			user_id         INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			workspace_id    INTEGER NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
			conversation_id INTEGER REFERENCES ai_conversations(id) ON DELETE SET NULL,
			model           TEXT    NOT NULL,
			status          TEXT    NOT NULL CHECK(status IN ('queued','running','waiting_approval','completed','failed','cancelled')),
			error           TEXT    NOT NULL DEFAULT '',
			input_turns     TEXT    NOT NULL DEFAULT '[]',
			thinking        TEXT    NOT NULL DEFAULT '',
			created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			started_at      DATETIME,
			completed_at    DATETIME,
			agent_id        TEXT NOT NULL DEFAULT 'default'
		);
		CREATE TABLE ai_agent_run_events (
			id         INTEGER PRIMARY KEY AUTOINCREMENT,
			run_id     INTEGER NOT NULL REFERENCES ai_agent_runs(id) ON DELETE CASCADE,
			sequence   INTEGER NOT NULL,
			event_json TEXT    NOT NULL,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			UNIQUE(run_id, sequence)
		);
	`)
	if err != nil {
		t.Fatalf("create pre-waiting_user schema: %v", err)
	}
}

func markMigrationsAppliedBefore(t *testing.T, db *DB, stopVersion string) {
	t.Helper()
	if _, err := db.Conn().Exec(`CREATE TABLE schema_migrations (version TEXT PRIMARY KEY, applied_at DATETIME DEFAULT CURRENT_TIMESTAMP)`); err != nil {
		t.Fatalf("create schema_migrations: %v", err)
	}
	entries, err := migrationFS.ReadDir("migrations")
	if err != nil {
		t.Fatalf("read migrations: %v", err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		version := strings.TrimSuffix(entry.Name(), ".sql")
		if version >= stopVersion {
			continue
		}
		if _, err := db.Conn().Exec(`INSERT INTO schema_migrations (version) VALUES (?)`, version); err != nil {
			t.Fatalf("mark migration %s applied: %v", version, err)
		}
	}
}
