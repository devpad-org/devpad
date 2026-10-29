package database

import (
	"path/filepath"
	"testing"
)

func TestMigration051MovesOpenHaiku45ChoicesToHaiku55(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "devpad.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	migrateBefore(t, db, "051")

	for _, stmt := range []string{
		`INSERT INTO users (id, username, email, password) VALUES (1, 'owner', 'owner@example.com', 'x')`,
		`INSERT INTO projects (id, user_id, name, status) VALUES (1, 1, 'shop', 'ready')`,
		`INSERT INTO ai_agent_defaults (agent_id, model) VALUES ('explore', 'claude-haiku-4-5')`,
		`INSERT INTO ai_agent_defaults (agent_id, model, effort) VALUES ('review', 'claude-opus-5-5', 'high')`,
		`INSERT INTO tasks (id, project_id, user_id, title, status, model) VALUES (1, 1, 1, 'todo', 'todo', 'claude-haiku-4-5')`,
		`INSERT INTO tasks (id, project_id, user_id, title, status, model) VALUES (2, 1, 1, 'review', 'in_review', 'claude-haiku-4-5')`,
		`INSERT INTO tasks (id, project_id, user_id, title, status, model) VALUES (3, 1, 1, 'merged', 'merged', 'claude-haiku-4-5')`,
		`INSERT INTO tasks (id, project_id, user_id, title, status, model) VALUES (4, 1, 1, 'cancelled', 'cancelled', 'claude-haiku-4-5')`,
		`INSERT INTO tasks (id, project_id, user_id, title, status, model) VALUES (5, 1, 1, 'other', 'todo', 'claude-sonnet-5-5')`,
		`INSERT INTO merge_requests (id, project_id, user_id, title, source_branch, target_branch) VALUES (1, 1, 1, 'cart', 'cart', 'main')`,
		`INSERT INTO merge_request_reviews (id, merge_request_id, author, status, model) VALUES (1, 1, 'agent', 'pending', 'claude-haiku-4-5')`,
		`INSERT INTO merge_request_reviews (id, merge_request_id, author, status, model) VALUES (2, 1, 'agent', 'submitted', 'claude-haiku-4-5')`,
	} {
		if _, err := db.conn.Exec(stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}

	if err := db.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	for _, tt := range []struct {
		query string
		want  string
	}{
		{`SELECT model FROM ai_agent_defaults WHERE agent_id = 'explore'`, "claude-haiku-5-5"},
		{`SELECT model || '/' || effort FROM ai_agent_defaults WHERE agent_id = 'review'`, "claude-opus-5-5/high"},
		{`SELECT model FROM tasks WHERE id = 1`, "claude-haiku-5-5"},
		{`SELECT model FROM tasks WHERE id = 2`, "claude-haiku-5-5"},
		{`SELECT model FROM tasks WHERE id = 3`, "claude-haiku-4-5"},
		{`SELECT model FROM tasks WHERE id = 4`, "claude-haiku-4-5"},
		{`SELECT model FROM tasks WHERE id = 5`, "claude-sonnet-5-5"},
		{`SELECT model FROM merge_request_reviews WHERE id = 1`, "claude-haiku-5-5"},
		{`SELECT model FROM merge_request_reviews WHERE id = 2`, "claude-haiku-4-5"},
	} {
		var got string
		if err := db.conn.QueryRow(tt.query).Scan(&got); err != nil {
			t.Fatalf("%s: %v", tt.query, err)
		}
		if got != tt.want {
			t.Errorf("%s = %q, want %q", tt.query, got, tt.want)
		}
	}
}
