package database

import (
	"path/filepath"
	"testing"
)

// Every user starts from the defaults and budgets the admin had set, and
// the server's provider keys go to no one.
func TestMigration056MovesAISettingsToEachUser(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "devpad.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	migrateBefore(t, db, "056")

	for _, stmt := range []string{
		`INSERT INTO users (id, username, email, password) VALUES (1, 'admin', 'admin@example.com', 'x'), (2, 'dev', 'dev@example.com', 'x')`,
		`INSERT INTO ai_providers (id, api_key, enabled) VALUES ('anthropic', 'sk-server', 1)`,
		`INSERT INTO ai_agent_defaults (agent_id, model, effort) VALUES ('review', 'claude-opus-5-5', 'high')`,
		`UPDATE ai_budget_defaults SET task_usd = 30, review_usd = 7, assistant_usd = 1 WHERE id = 1`,
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
		{`SELECT COUNT(*) FROM user_ai_provider_keys`, "0"},
		{`SELECT group_concat(user_id || ':' || model || '/' || effort, ',') FROM (SELECT * FROM user_ai_agent_defaults WHERE agent_id = 'review' ORDER BY user_id)`, "1:claude-opus-5-5/high,2:claude-opus-5-5/high"},
		{`SELECT group_concat(user_id || ':' || task_budget_usd || '/' || review_budget_usd || '/' || assistant_budget_usd || '/' || summary_model, ',') FROM (SELECT * FROM user_ai_settings ORDER BY user_id)`, "1:30.0/7.0/1.0/,2:30.0/7.0/1.0/"},
		{`SELECT COUNT(*) FROM sqlite_master WHERE name IN ('ai_providers', 'ai_agent_defaults', 'ai_budget_defaults')`, "0"},
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
