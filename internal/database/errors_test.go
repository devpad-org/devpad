package database

import (
	"errors"
	"fmt"
	"path/filepath"
	"testing"
)

func TestIsUniqueViolation(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "devpad.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	if _, err := db.Conn().Exec(`CREATE TABLE t (id INTEGER PRIMARY KEY, name TEXT UNIQUE, note TEXT NOT NULL)`); err != nil {
		t.Fatalf("create table: %v", err)
	}
	if _, err := db.Conn().Exec(`INSERT INTO t (name, note) VALUES ('a', 'x')`); err != nil {
		t.Fatalf("insert: %v", err)
	}

	_, err = db.Conn().Exec(`INSERT INTO t (name, note) VALUES ('a', 'y')`)
	if !IsUniqueViolation(fmt.Errorf("wrapped: %w", err)) {
		t.Fatalf("duplicate name: expected a unique violation, got %v", err)
	}

	_, err = db.Conn().Exec(`INSERT INTO t (name, note) VALUES ('b', NULL)`)
	if err == nil || IsUniqueViolation(err) {
		t.Fatalf("NOT NULL failure: expected an error that is not a unique violation, got %v", err)
	}

	if IsUniqueViolation(errors.New("UNIQUE constraint failed: t.name")) {
		t.Fatal("a plain error mentioning UNIQUE is not a unique violation")
	}
	if IsUniqueViolation(nil) {
		t.Fatal("nil is not a unique violation")
	}
}
