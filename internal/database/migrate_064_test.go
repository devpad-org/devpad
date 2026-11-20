package database

import (
	"path/filepath"
	"testing"
	"time"
)

func TestMigration064KeepsSetupRunsAndAllowsPending(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "devpad.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	migrateBefore(t, db, "064")

	started := time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)
	for _, stmt := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO users (id, username, email, password) VALUES (1, 'owner', 'owner@example.com', 'x')`, nil},
		{`INSERT INTO workspaces (id, user_id, name) VALUES (1, 1, 'one')`, nil},
		{`INSERT INTO workspaces (id, user_id, name) VALUES (2, 1, 'two')`, nil},
		{`INSERT INTO workspace_setups (workspace_id, user_id, script, status, command_id, exit_code, output, error, started_at, finished_at)
		  VALUES (1, 1, 'npm ci', 'failed', 'cmd-1', 2, 'npm ERR!', '', ?, ?)`, []any{started, started}},
	} {
		if _, err := db.conn.Exec(stmt.sql, stmt.args...); err != nil {
			t.Fatalf("%s: %v", stmt.sql, err)
		}
	}
	if _, err := db.conn.Exec(`INSERT INTO workspace_setups (workspace_id, user_id, script, status, started_at) VALUES (2, 1, 'x', 'pending', ?)`, started); err == nil {
		t.Fatal("expected pending refused before 064")
	}

	if err := db.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	var script, status, commandID, output string
	var exitCode int
	if err := db.conn.QueryRow(`SELECT script, status, command_id, exit_code, output FROM workspace_setups WHERE workspace_id = 1`).Scan(&script, &status, &commandID, &exitCode, &output); err != nil {
		t.Fatalf("reading the kept run: %v", err)
	}
	if script != "npm ci" || status != "failed" || commandID != "cmd-1" || exitCode != 2 || output != "npm ERR!" {
		t.Fatalf("run not kept: %q %q %q %d %q", script, status, commandID, exitCode, output)
	}
	if _, err := db.conn.Exec(`INSERT INTO workspace_setups (workspace_id, user_id, script, status, started_at) VALUES (2, 1, 'x', 'pending', ?)`, started); err != nil {
		t.Fatalf("expected pending allowed: %v", err)
	}
	if _, err := db.conn.Exec(`INSERT INTO workspace_setups (workspace_id, user_id, script, status, started_at) VALUES (1, 1, 'x', 'bogus', ?) ON CONFLICT(workspace_id) DO UPDATE SET status = excluded.status`, started); err == nil {
		t.Fatal("expected an unknown status still refused")
	}
	// Removing a workspace still removes its run.
	if _, err := db.conn.Exec(`DELETE FROM workspaces WHERE id = 2`); err != nil {
		t.Fatalf("delete workspace: %v", err)
	}
	var n int
	if err := db.conn.QueryRow(`SELECT COUNT(*) FROM workspace_setups WHERE workspace_id = 2`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("expected the run removed with its workspace, got %d %v", n, err)
	}
}
