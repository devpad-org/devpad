package database

import (
	"database/sql"
	"path/filepath"
	"slices"
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

func TestMigrateAllowsEveryWorkspaceServiceType(t *testing.T) {
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
	for _, serviceType := range []string{"postgres", "mongodb", "mariadb", "couchdb", "meilisearch", "sqlserver"} {
		if _, err := db.Conn().Exec(`INSERT INTO workspace_services (workspace_id, service_type, status, config) VALUES (1, ?, 'stopped', '{}')`, serviceType); err != nil {
			t.Fatalf("insert %s workspace service: %v", serviceType, err)
		}
	}
	if _, err := db.Conn().Exec(`INSERT INTO workspace_services (workspace_id, service_type, status, config) VALUES (1, 'oracle', 'stopped', '{}')`); err == nil {
		t.Fatal("expected an unknown service type to be refused")
	}
}

func TestMigrateKeepsWorkspaceServicesWhenAddingSQLServer(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "devpad.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	createPreWaitingUserAgentRunSchema(t, db)
	markMigrationsAppliedBefore(t, db, "059_add_sqlserver_service_type")
	if _, err := db.Conn().Exec(`INSERT INTO users (id, username, email, password) VALUES (1, 'test', 'test@example.com', 'hashed')`); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	if _, err := db.Conn().Exec(`INSERT INTO workspaces (id, user_id, name) VALUES (1, 1, 'workspace')`); err != nil {
		t.Fatalf("insert workspace: %v", err)
	}
	if _, err := db.Conn().Exec(`INSERT INTO workspace_services (id, workspace_id, service_type, container_id, volume_name, status, config) VALUES (7, 1, 'postgres', 'abc', 'vol', 'running', '{"port":5432}')`); err != nil {
		t.Fatalf("insert workspace service: %v", err)
	}

	if err := db.Migrate(); err != nil {
		t.Fatalf("migrate db: %v", err)
	}

	var serviceType, containerID, volumeName, status, config string
	if err := db.Conn().QueryRow(`SELECT service_type, container_id, volume_name, status, config FROM workspace_services WHERE id = 7`).Scan(&serviceType, &containerID, &volumeName, &status, &config); err != nil {
		t.Fatalf("read workspace service: %v", err)
	}
	if serviceType != "postgres" || containerID != "abc" || volumeName != "vol" || status != "running" || config != `{"port":5432}` {
		t.Fatalf("workspace service changed: %s %s %s %s %s", serviceType, containerID, volumeName, status, config)
	}
	if _, err := db.Conn().Exec(`INSERT INTO workspace_services (workspace_id, service_type) VALUES (1, 'sqlserver')`); err != nil {
		t.Fatalf("insert sqlserver workspace service: %v", err)
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
		CREATE TABLE user_preferences (
			user_id INTEGER PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
			diff_view_side_by_side BOOLEAN NOT NULL DEFAULT 1,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);
		CREATE TABLE workspaces (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			name TEXT NOT NULL
		);
		CREATE TABLE workspace_services (
			id            INTEGER PRIMARY KEY AUTOINCREMENT,
			workspace_id  INTEGER NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
			service_type  TEXT    NOT NULL CHECK(service_type IN ('postgres','mongodb','mariadb','couchdb','meilisearch')),
			container_id  TEXT    NOT NULL DEFAULT '',
			volume_name   TEXT    NOT NULL DEFAULT '',
			status        TEXT    NOT NULL DEFAULT 'stopped' CHECK(status IN ('running','stopped')),
			config        TEXT    NOT NULL DEFAULT '{}',
			created_at    DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at    DATETIME DEFAULT CURRENT_TIMESTAMP
		);
		CREATE INDEX idx_workspace_services_workspace_id ON workspace_services(workspace_id);
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
		CREATE TABLE ai_turns (
			id              INTEGER PRIMARY KEY AUTOINCREMENT,
			conversation_id INTEGER NOT NULL REFERENCES ai_conversations(id) ON DELETE CASCADE,
			position        INTEGER NOT NULL,
			role            TEXT    NOT NULL CHECK(role IN ('system','user','assistant')),
			UNIQUE(conversation_id, position)
		);
		CREATE TABLE ai_parts (
			id              INTEGER PRIMARY KEY AUTOINCREMENT,
			turn_id         INTEGER NOT NULL REFERENCES ai_turns(id) ON DELETE CASCADE,
			position        INTEGER NOT NULL,
			kind            TEXT    NOT NULL CHECK(kind IN ('text','thinking','tool_call','tool_result')),
			text                   TEXT    NOT NULL DEFAULT '',
			thinking_state         TEXT    NOT NULL DEFAULT '',
			tool_call_id           TEXT    NOT NULL DEFAULT '',
			tool_call_name         TEXT    NOT NULL DEFAULT '',
			tool_call_args         TEXT    NOT NULL DEFAULT '',
			tool_result_call_id    TEXT    NOT NULL DEFAULT '',
			tool_result_name       TEXT    NOT NULL DEFAULT '',
			tool_result_content    TEXT    NOT NULL DEFAULT '',
			tool_result_is_error   INTEGER NOT NULL DEFAULT 0,
			tool_call_item_id      TEXT    NOT NULL DEFAULT '',
			UNIQUE(turn_id, position)
		);
	`)
	if err != nil {
		t.Fatalf("create pre-waiting_user schema: %v", err)
	}
}

func TestMigrateAddsImagePartsKeepingExistingParts(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "devpad.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	// The pre-023 schema has every table 048 touches, but not those later
	// migrations need.
	createPreWaitingUserAgentRunSchema(t, db)
	markMigrationsAppliedBefore(t, db, "048_ai_images")
	markMigrationsAppliedFrom(t, db, "050_ai_usage")
	for _, stmt := range []string{
		`INSERT INTO users (id, username, email, password) VALUES (1, 'test', 'test@example.com', 'hashed')`,
		`INSERT INTO workspaces (id, user_id, name) VALUES (1, 1, 'workspace')`,
		`INSERT INTO ai_conversations (id, user_id, workspace_id, title, model) VALUES (1, 1, 1, 'Chat', 'gpt-5.4')`,
		`INSERT INTO ai_turns (id, conversation_id, position, role) VALUES (1, 1, 0, 'user')`,
		`INSERT INTO ai_parts (id, turn_id, position, kind, text, tool_call_item_id) VALUES (1, 1, 0, 'text', 'hello', 'item')`,
	} {
		if _, err := db.Conn().Exec(stmt); err != nil {
			t.Fatalf("seed %q: %v", stmt, err)
		}
	}

	if err := db.Migrate(); err != nil {
		t.Fatalf("migrate db: %v", err)
	}

	var text, itemID string
	if err := db.Conn().QueryRow(`SELECT text, tool_call_item_id FROM ai_parts WHERE id = 1`).Scan(&text, &itemID); err != nil {
		t.Fatalf("query preserved part: %v", err)
	}
	if text != "hello" || itemID != "item" {
		t.Fatalf("preserved part = (%q, %q), want (hello, item)", text, itemID)
	}
	if _, err := db.Conn().Exec(`INSERT INTO ai_parts (turn_id, position, kind, image_ref) VALUES (1, 1, 'image', '{"attachmentId":1}')`); err != nil {
		t.Fatalf("insert image part: %v", err)
	}
	if _, err := db.Conn().Exec(`INSERT INTO ai_attachments (user_id, conversation_id, media_type, width, height, byte_size, data) VALUES (1, 1, 'image/png', 1, 1, 1, x'00')`); err != nil {
		t.Fatalf("insert attachment: %v", err)
	}
	if _, err := db.Conn().Exec(`DELETE FROM ai_conversations WHERE id = 1`); err != nil {
		t.Fatalf("delete conversation: %v", err)
	}
	var attachments int
	if err := db.Conn().QueryRow(`SELECT COUNT(*) FROM ai_attachments`).Scan(&attachments); err != nil {
		t.Fatalf("count attachments: %v", err)
	}
	if attachments != 0 {
		t.Fatalf("attachments after deleting their conversation = %d, want 0", attachments)
	}
}

// 058 makes the theme nullable, keeping every row; the theme 057 stored as
// the default becomes NULL, a theme chosen otherwise stays.
func TestMigrateMakesUserThemeNullable(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "devpad.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	// Apply everything up to 057, seed, then apply 058.
	markMigrationsAppliedFrom(t, db, "058_user_theme_nullable")
	if err := db.Migrate(); err != nil {
		t.Fatalf("migrate to 057: %v", err)
	}
	for _, stmt := range []string{
		`INSERT INTO users (id, username, email, password) VALUES (1, 'a', 'a@example.com', 'hashed'), (2, 'b', 'b@example.com', 'hashed')`,
		`INSERT INTO user_preferences (user_id, diff_view_side_by_side) VALUES (1, 0)`,
		`INSERT INTO user_preferences (user_id, diff_view_side_by_side, theme) VALUES (2, 1, 'modern-dark')`,
		`DELETE FROM schema_migrations WHERE version = '058_user_theme_nullable'`,
	} {
		if _, err := db.Conn().Exec(stmt); err != nil {
			t.Fatalf("seed %q: %v", stmt, err)
		}
	}
	if err := db.Migrate(); err != nil {
		t.Fatalf("migrate db: %v", err)
	}

	rows, err := db.Conn().Query(`SELECT user_id, diff_view_side_by_side, theme FROM user_preferences ORDER BY user_id`)
	if err != nil {
		t.Fatalf("query preferences: %v", err)
	}
	defer rows.Close()
	type row struct {
		userID     int64
		sideBySide bool
		theme      sql.NullString
	}
	var got []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.userID, &r.sideBySide, &r.theme); err != nil {
			t.Fatalf("scan preferences: %v", err)
		}
		got = append(got, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read preferences: %v", err)
	}
	want := []row{
		{1, false, sql.NullString{}},
		{2, true, sql.NullString{String: "modern-dark", Valid: true}},
	}
	if !slices.Equal(got, want) {
		t.Fatalf("preferences = %+v, want %+v", got, want)
	}

	if _, err := db.Conn().Exec(`INSERT INTO users (id, username, email, password) VALUES (3, 'c', 'c@example.com', 'hashed')`); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	if _, err := db.Conn().Exec(`INSERT INTO user_preferences (user_id, diff_view_side_by_side, theme) VALUES (3, 1, NULL)`); err != nil {
		t.Fatalf("insert preferences with no theme: %v", err)
	}
	// The table still goes with its user.
	if _, err := db.Conn().Exec(`DELETE FROM users WHERE id = 3`); err != nil {
		t.Fatalf("delete user: %v", err)
	}
	var left int
	if err := db.Conn().QueryRow(`SELECT COUNT(*) FROM user_preferences WHERE user_id = 3`).Scan(&left); err != nil {
		t.Fatalf("count preferences: %v", err)
	}
	if left != 0 {
		t.Fatalf("preferences left after deleting their user = %d, want 0", left)
	}
}

func markMigrationsAppliedBefore(t *testing.T, db *DB, stopVersion string) {
	t.Helper()
	markMigrationsApplied(t, db, func(version string) bool { return version < stopVersion })
}

// markMigrationsAppliedFrom marks startVersion and every later migration
// applied.
func markMigrationsAppliedFrom(t *testing.T, db *DB, startVersion string) {
	t.Helper()
	markMigrationsApplied(t, db, func(version string) bool { return version >= startVersion })
}

func markMigrationsApplied(t *testing.T, db *DB, applied func(version string) bool) {
	t.Helper()
	if _, err := db.Conn().Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (version TEXT PRIMARY KEY, applied_at DATETIME DEFAULT CURRENT_TIMESTAMP)`); err != nil {
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
		if !applied(version) {
			continue
		}
		if _, err := db.Conn().Exec(`INSERT INTO schema_migrations (version) VALUES (?)`, version); err != nil {
			t.Fatalf("mark migration %s applied: %v", version, err)
		}
	}
}
