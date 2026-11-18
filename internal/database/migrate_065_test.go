package database

import (
	"path/filepath"
	"testing"
)

func TestMigration065KeepsOneServiceOfEachTypePerWorkspace(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "devpad.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	migrateBefore(t, db, "065")

	for _, stmt := range []string{
		`INSERT INTO users (id, username, email, password) VALUES (1, 'owner', 'owner@example.com', 'x')`,
		`INSERT INTO workspaces (id, user_id, name) VALUES (1, 1, 'one')`,
		`INSERT INTO workspaces (id, user_id, name) VALUES (2, 1, 'two')`,
		// A race left two Postgres records on workspace 1; only the later
		// one got the container.
		`INSERT INTO workspace_services (id, workspace_id, service_type, container_id, volume_name, status, config) VALUES (3, 1, 'postgres', '', 'devpad-svc-1-postgres', 'stopped', '{}')`,
		`INSERT INTO workspace_services (id, workspace_id, service_type, container_id, volume_name, status, config) VALUES (4, 1, 'postgres', 'abc', 'devpad-svc-1-postgres', 'running', '{}')`,
		// Two without containers: the older stays.
		`INSERT INTO workspace_services (id, workspace_id, service_type, volume_name, status, config) VALUES (5, 1, 'mongodb', 'devpad-svc-1-mongodb', 'stopped', '{}')`,
		`INSERT INTO workspace_services (id, workspace_id, service_type, volume_name, status, config) VALUES (6, 1, 'mongodb', 'devpad-svc-1-mongodb', 'stopped', '{}')`,
		// The same type on another workspace is no duplicate.
		`INSERT INTO workspace_services (id, workspace_id, service_type, volume_name, status, config) VALUES (7, 2, 'postgres', 'devpad-svc-2-postgres', 'stopped', '{}')`,
	} {
		if _, err := db.conn.Exec(stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}

	if err := db.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	rows, err := db.conn.Query(`SELECT id FROM workspace_services ORDER BY id`)
	if err != nil {
		t.Fatalf("listing services: %v", err)
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("scanning: %v", err)
		}
		ids = append(ids, id)
	}
	if len(ids) != 3 || ids[0] != 4 || ids[1] != 5 || ids[2] != 7 {
		t.Fatalf("expected services 4, 5 and 7 kept, got %v", ids)
	}

	_, err = db.conn.Exec(`INSERT INTO workspace_services (workspace_id, service_type, volume_name, status, config) VALUES (1, 'postgres', 'v', 'stopped', '{}')`)
	if !IsUniqueViolation(err) {
		t.Fatalf("expected a second Postgres refused as a UNIQUE violation, got %v", err)
	}
	if _, err := db.conn.Exec(`INSERT INTO workspace_services (workspace_id, service_type, volume_name, status, config) VALUES (2, 'mongodb', 'v', 'stopped', '{}')`); err != nil {
		t.Fatalf("expected another type allowed: %v", err)
	}
}
