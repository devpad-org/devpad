package database

import (
	"path/filepath"
	"testing"
)

func TestMigration080UnlinksContinuedConversationsFromTheirFirstRun(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "devpad.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	migrateBefore(t, db, "080")

	const (
		prompt  = `[{"Role":"user","Parts":[{"Kind":"text","Text":"Review the diff"}]}]`
		history = `[{"Role":"user","Parts":[{"Kind":"text","Text":"Start this api"}]},
		            {"Role":"assistant","Parts":[{"Kind":"text","Text":"Started."}]},
		            {"Role":"user","Parts":[{"Kind":"text","Text":"Insert a few more tasks"}]}]`
	)
	for _, stmt := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO users (id, username, email, password) VALUES (1, 'owner', 'owner@example.com', 'x')`, nil},
		{`INSERT INTO workspaces (id, user_id, name) VALUES (1, 1, 'shop')`, nil},
		{`INSERT INTO ai_conversations (id, user_id, workspace_id) VALUES (1, 1, 1)`, nil},
		// The conversation's first message.
		{`INSERT INTO ai_agent_runs (id, user_id, workspace_id, conversation_id, model, status, input_turns)
		  VALUES (1, 1, 1, 1, 'm', 'completed', ?)`, []any{prompt}},
		// A sub-agent it started, from its one prompt.
		{`INSERT INTO ai_agent_runs (id, parent_run_id, user_id, workspace_id, conversation_id, model, status, input_turns)
		  VALUES (2, 1, 1, 1, 1, 'm', 'completed', ?)`, []any{prompt}},
		// A message continuing the conversation, linked to its first run.
		{`INSERT INTO ai_agent_runs (id, parent_run_id, user_id, workspace_id, conversation_id, model, status, input_turns)
		  VALUES (3, 1, 1, 1, 1, 'm', 'completed', ?)`, []any{history}},
		// Turns that aren't JSON are left alone rather than failing the migration.
		{`INSERT INTO ai_agent_runs (id, parent_run_id, user_id, workspace_id, conversation_id, model, status, input_turns)
		  VALUES (4, 1, 1, 1, 1, 'm', 'failed', 'not json')`, nil},
	} {
		if _, err := db.conn.Exec(stmt.sql, stmt.args...); err != nil {
			t.Fatalf("%s: %v", stmt.sql, err)
		}
	}

	if err := db.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	want := map[int64]int64{1: 0, 2: 1, 3: 0, 4: 1}
	rows, err := db.conn.Query(`SELECT id, COALESCE(parent_run_id, 0) FROM ai_agent_runs ORDER BY id`)
	if err != nil {
		t.Fatalf("listing runs: %v", err)
	}
	defer rows.Close()
	got := map[int64]int64{}
	for rows.Next() {
		var id, parent int64
		if err := rows.Scan(&id, &parent); err != nil {
			t.Fatalf("scanning run: %v", err)
		}
		got[id] = parent
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("listing runs: %v", err)
	}
	if len(got) != len(want) {
		t.Fatalf("parents = %v, want %v", got, want)
	}
	for id, parent := range want {
		if got[id] != parent {
			t.Fatalf("parents = %v, want %v", got, want)
		}
	}
}
