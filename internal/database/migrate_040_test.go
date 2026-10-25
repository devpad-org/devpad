package database

import (
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// migrateBefore applies the migrations that sort before version, as an
// existing database would have them.
func migrateBefore(t *testing.T, db *DB, version string) {
	t.Helper()
	if _, err := db.conn.Exec(`CREATE TABLE schema_migrations (version TEXT PRIMARY KEY, applied_at DATETIME DEFAULT CURRENT_TIMESTAMP)`); err != nil {
		t.Fatalf("creating migrations table: %v", err)
	}
	entries, err := migrationFS.ReadDir("migrations")
	if err != nil {
		t.Fatalf("reading migrations: %v", err)
	}
	var names []string
	for _, e := range entries {
		if name := e.Name(); strings.HasSuffix(name, ".sql") && name < version {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	for _, name := range names {
		content, err := migrationFS.ReadFile("migrations/" + name)
		if err != nil {
			t.Fatalf("reading %s: %v", name, err)
		}
		if _, err := db.conn.Exec(string(content)); err != nil {
			t.Fatalf("applying %s: %v", name, err)
		}
		if _, err := db.conn.Exec(`INSERT INTO schema_migrations (version) VALUES (?)`, strings.TrimSuffix(name, ".sql")); err != nil {
			t.Fatalf("recording %s: %v", name, err)
		}
	}
}

func TestMigration040KeepsCommentReplies(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "devpad.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	migrateBefore(t, db, "040")

	for _, stmt := range []string{
		`INSERT INTO users (id, username, email, password) VALUES (1, 'owner', 'owner@example.com', 'x')`,
		`INSERT INTO projects (id, user_id, name, status) VALUES (1, 1, 'shop', 'ready')`,
		`INSERT INTO merge_requests (id, project_id, user_id, title, source_branch, target_branch) VALUES (1, 1, 1, 'Cart', 'cart', 'main')`,
		`INSERT INTO merge_request_comments (id, merge_request_id, author, path, line, body) VALUES (1, 1, 'user', 'cart.go', 12, 'This panics on nil.')`,
		`INSERT INTO merge_request_comments (id, merge_request_id, reply_to, author, path, line, body) VALUES (2, 1, 1, 'task_agent', 'cart.go', 12, 'Added a nil check.')`,
		`INSERT INTO merge_request_comments (id, merge_request_id, author, body) VALUES (3, 1, 'agent', 'Looks good.')`,
	} {
		if _, err := db.conn.Exec(stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}

	if err := db.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	rows, err := db.conn.Query(`SELECT id, COALESCE(reply_to, 0), author FROM merge_request_comments ORDER BY id`)
	if err != nil {
		t.Fatalf("listing comments: %v", err)
	}
	defer rows.Close()
	type comment struct {
		id, replyTo int64
		author      string
	}
	var got []comment
	for rows.Next() {
		var c comment
		if err := rows.Scan(&c.id, &c.replyTo, &c.author); err != nil {
			t.Fatalf("scanning comment: %v", err)
		}
		got = append(got, c)
	}
	want := []comment{{1, 0, "user"}, {2, 1, "task_agent"}, {3, 0, "agent"}}
	if len(got) != len(want) {
		t.Fatalf("comments = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("comments = %+v, want %+v", got, want)
		}
	}

	if _, err := db.conn.Exec(`INSERT INTO merge_request_comments (merge_request_id, reply_to, author, body) VALUES (1, 1, 'assistant', 'Fixed.')`); err != nil {
		t.Fatalf("an assistant reply should be allowed: %v", err)
	}
	// The reply link still cascades: deleting a thread removes its replies.
	if _, err := db.conn.Exec(`DELETE FROM merge_request_comments WHERE id = 1`); err != nil {
		t.Fatalf("deleting thread: %v", err)
	}
	var left int
	if err := db.conn.QueryRow(`SELECT COUNT(*) FROM merge_request_comments`).Scan(&left); err != nil || left != 1 {
		t.Fatalf("expected only the unrelated comment left, got %d (%v)", left, err)
	}
}
