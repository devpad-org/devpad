package database

import (
	"path/filepath"
	"testing"
	"time"
)

func TestMigration050CopiesRunUsageIntoTheLedger(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "devpad.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	migrateBefore(t, db, "050")

	// Timestamps are bound as time.Time, as the repositories write them.
	started := time.Date(2026, 10, 20, 14, 30, 5, 123456789, time.UTC)
	for _, stmt := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO users (id, username, email, password) VALUES (1, 'owner', 'owner@example.com', 'x')`, nil},
		{`INSERT INTO projects (id, user_id, name, status) VALUES (1, 1, 'shop', 'ready')`, nil},
		{`INSERT INTO workspaces (id, user_id, name, project_id, kind, branch) VALUES (1, 1, 'shop', 1, 'task', 'cart')`, nil},
		{`INSERT INTO workspaces (id, user_id, name) VALUES (2, 1, 'old')`, nil},
		{`INSERT INTO ai_agent_runs (id, user_id, workspace_id, model, status, input_tokens, output_tokens, cost_usd, started_at)
		  VALUES (1, 1, 1, 'claude-opus-5-5', 'completed', 1000, 200, 0.25, ?)`, []any{started}},
		// Unpriced, never started.
		{`INSERT INTO ai_agent_runs (id, user_id, workspace_id, model, status, input_tokens, created_at)
		  VALUES (2, 1, 2, 'local-model', 'failed', 50, ?)`, []any{started.Add(time.Hour)}},
		// Made no requests.
		{`INSERT INTO ai_agent_runs (id, user_id, workspace_id, model, status) VALUES (3, 1, 1, 'claude-opus-5-5', 'cancelled')`, nil},
	} {
		if _, err := db.conn.Exec(stmt.sql, stmt.args...); err != nil {
			t.Fatalf("%s: %v", stmt.sql, err)
		}
	}

	if err := db.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	rows, err := db.conn.Query(`SELECT run_id, COALESCE(project_id, 0), project_name, workspace_kind, model,
	                                   input_tokens, output_tokens, COALESCE(cost_usd, -1), created_at
	                            FROM ai_usage ORDER BY run_id`)
	if err != nil {
		t.Fatalf("listing usage: %v", err)
	}
	defer rows.Close()
	type usage struct {
		runID, projectID  int64
		projectName, kind string
		model             string
		input, output     int64
		cost              float64
		createdAt         int64
	}
	var got []usage
	for rows.Next() {
		var u usage
		if err := rows.Scan(&u.runID, &u.projectID, &u.projectName, &u.kind, &u.model, &u.input, &u.output, &u.cost, &u.createdAt); err != nil {
			t.Fatalf("scanning usage: %v", err)
		}
		got = append(got, u)
	}
	want := []usage{
		{1, 1, "shop", "task", "claude-opus-5-5", 1000, 200, 0.25, started.Unix()},
		{2, 0, "", "interactive", "local-model", 50, 0, -1, started.Add(time.Hour).Unix()},
	}
	if len(got) != len(want) {
		t.Fatalf("usage = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("usage = %+v, want %+v", got, want)
		}
	}
}
