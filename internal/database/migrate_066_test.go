package database

import (
	"path/filepath"
	"testing"
)

func TestMigration066MovesOpenGPT5ChoicesToSol(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "devpad.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	migrateBefore(t, db, "066")

	for _, stmt := range []string{
		`INSERT INTO users (id, username, email, password) VALUES (1, 'owner', 'owner@example.com', 'x')`,
		`INSERT INTO users (id, username, email, password) VALUES (2, 'other', 'other@example.com', 'x')`,
		`INSERT INTO users (id, username, email, password) VALUES (3, 'third', 'third@example.com', 'x')`,
		`INSERT INTO projects (id, user_id, name, status) VALUES (1, 1, 'shop', 'ready')`,
		`INSERT INTO user_ai_agent_defaults (user_id, agent_id, model, effort) VALUES (1, 'task', 'gpt-5.5', 'xhigh')`,
		`INSERT INTO user_ai_agent_defaults (user_id, agent_id, model) VALUES (1, 'explore', 'gpt-5.4')`,
		`INSERT INTO user_ai_agent_defaults (user_id, agent_id, model, effort) VALUES (1, 'review', 'claude-haiku-5-5', 'high')`,
		`INSERT INTO user_ai_settings (user_id, summary_model) VALUES (1, 'gpt-5.4')`,
		`INSERT INTO user_ai_settings (user_id, summary_model) VALUES (2, 'gpt-5.5')`,
		`INSERT INTO user_ai_settings (user_id, summary_model) VALUES (3, '')`,
		`INSERT INTO tasks (id, project_id, user_id, title, status, model) VALUES (1, 1, 1, 'todo', 'todo', 'gpt-5.5')`,
		`INSERT INTO tasks (id, project_id, user_id, title, status, model) VALUES (2, 1, 1, 'review', 'in_review', 'gpt-5.4')`,
		`INSERT INTO tasks (id, project_id, user_id, title, status, model) VALUES (3, 1, 1, 'merged', 'merged', 'gpt-5.5')`,
		`INSERT INTO tasks (id, project_id, user_id, title, status, model) VALUES (4, 1, 1, 'cancelled', 'cancelled', 'gpt-5.4')`,
		`INSERT INTO tasks (id, project_id, user_id, title, status, model) VALUES (5, 1, 1, 'other', 'todo', 'claude-haiku-5-5')`,
		`INSERT INTO merge_requests (id, project_id, user_id, title, source_branch, target_branch) VALUES (1, 1, 1, 'cart', 'cart', 'main')`,
		`INSERT INTO merge_request_reviews (id, merge_request_id, author, status, model) VALUES (1, 1, 'agent', 'pending', 'gpt-5.5')`,
		`INSERT INTO merge_request_reviews (id, merge_request_id, author, status, model) VALUES (2, 1, 'agent', 'pending', 'gpt-5.4')`,
		`INSERT INTO merge_request_reviews (id, merge_request_id, author, status, model) VALUES (3, 1, 'agent', 'submitted', 'gpt-5.5')`,
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
		{`SELECT model || '/' || effort FROM user_ai_agent_defaults WHERE user_id = 1 AND agent_id = 'task'`, "gpt-6.2-sol/xhigh"},
		{`SELECT model || '/' || effort FROM user_ai_agent_defaults WHERE user_id = 1 AND agent_id = 'explore'`, "gpt-6.2-sol/"},
		{`SELECT model || '/' || effort FROM user_ai_agent_defaults WHERE user_id = 1 AND agent_id = 'review'`, "claude-haiku-5-5/high"},
		{`SELECT summary_model FROM user_ai_settings WHERE user_id = 1`, "gpt-6.2-sol"},
		{`SELECT summary_model FROM user_ai_settings WHERE user_id = 2`, "gpt-6.2-sol"},
		{`SELECT summary_model FROM user_ai_settings WHERE user_id = 3`, ""},
		{`SELECT model FROM tasks WHERE id = 1`, "gpt-6.2-sol"},
		{`SELECT model FROM tasks WHERE id = 2`, "gpt-6.2-sol"},
		{`SELECT model FROM tasks WHERE id = 3`, "gpt-5.5"},
		{`SELECT model FROM tasks WHERE id = 4`, "gpt-5.4"},
		{`SELECT model FROM tasks WHERE id = 5`, "claude-haiku-5-5"},
		{`SELECT model FROM merge_request_reviews WHERE id = 1`, "gpt-6.2-sol"},
		{`SELECT model FROM merge_request_reviews WHERE id = 2`, "gpt-6.2-sol"},
		{`SELECT model FROM merge_request_reviews WHERE id = 3`, "gpt-5.5"},
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
