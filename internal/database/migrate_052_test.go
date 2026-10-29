package database

import (
	"path/filepath"
	"testing"
)

func TestMigration052MovesOpenOpus47AndSonnet46ChoicesTo55(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "devpad.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	migrateBefore(t, db, "052")

	for _, stmt := range []string{
		`INSERT INTO users (id, username, email, password) VALUES (1, 'owner', 'owner@example.com', 'x')`,
		`INSERT INTO projects (id, user_id, name, status) VALUES (1, 1, 'shop', 'ready')`,
		`INSERT INTO ai_agent_defaults (agent_id, model, effort) VALUES ('task', 'claude-opus-4-7', 'xhigh')`,
		`INSERT INTO ai_agent_defaults (agent_id, model) VALUES ('explore', 'claude-sonnet-4-6')`,
		`INSERT INTO ai_agent_defaults (agent_id, model, effort) VALUES ('review', 'claude-haiku-5-5', 'high')`,
		`INSERT INTO tasks (id, project_id, user_id, title, status, model) VALUES (1, 1, 1, 'todo', 'todo', 'claude-opus-4-7')`,
		`INSERT INTO tasks (id, project_id, user_id, title, status, model) VALUES (2, 1, 1, 'review', 'in_review', 'claude-sonnet-4-6')`,
		`INSERT INTO tasks (id, project_id, user_id, title, status, model) VALUES (3, 1, 1, 'merged', 'merged', 'claude-opus-4-7')`,
		`INSERT INTO tasks (id, project_id, user_id, title, status, model) VALUES (4, 1, 1, 'cancelled', 'cancelled', 'claude-sonnet-4-6')`,
		`INSERT INTO tasks (id, project_id, user_id, title, status, model) VALUES (5, 1, 1, 'other', 'todo', 'claude-haiku-5-5')`,
		`INSERT INTO merge_requests (id, project_id, user_id, title, source_branch, target_branch) VALUES (1, 1, 1, 'cart', 'cart', 'main')`,
		`INSERT INTO merge_request_reviews (id, merge_request_id, author, status, model) VALUES (1, 1, 'agent', 'pending', 'claude-opus-4-7')`,
		`INSERT INTO merge_request_reviews (id, merge_request_id, author, status, model) VALUES (2, 1, 'agent', 'pending', 'claude-sonnet-4-6')`,
		`INSERT INTO merge_request_reviews (id, merge_request_id, author, status, model) VALUES (3, 1, 'agent', 'submitted', 'claude-opus-4-7')`,
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
		{`SELECT model || '/' || effort FROM ai_agent_defaults WHERE agent_id = 'task'`, "claude-opus-5-5/xhigh"},
		{`SELECT model || '/' || effort FROM ai_agent_defaults WHERE agent_id = 'explore'`, "claude-sonnet-5-5/"},
		{`SELECT model || '/' || effort FROM ai_agent_defaults WHERE agent_id = 'review'`, "claude-haiku-5-5/high"},
		{`SELECT model FROM tasks WHERE id = 1`, "claude-opus-5-5"},
		{`SELECT model FROM tasks WHERE id = 2`, "claude-sonnet-5-5"},
		{`SELECT model FROM tasks WHERE id = 3`, "claude-opus-4-7"},
		{`SELECT model FROM tasks WHERE id = 4`, "claude-sonnet-4-6"},
		{`SELECT model FROM tasks WHERE id = 5`, "claude-haiku-5-5"},
		{`SELECT model FROM merge_request_reviews WHERE id = 1`, "claude-opus-5-5"},
		{`SELECT model FROM merge_request_reviews WHERE id = 2`, "claude-sonnet-5-5"},
		{`SELECT model FROM merge_request_reviews WHERE id = 3`, "claude-opus-4-7"},
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
