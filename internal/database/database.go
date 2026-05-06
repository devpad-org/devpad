package database

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

const sqliteBusyTimeout = 10 * time.Second

// DB wraps a sql.DB connection with application-specific methods.
type DB struct {
	conn *sql.DB
}

// Open creates a new database connection.
func Open(path string) (*DB, error) {
	conn, err := sql.Open("sqlite3", sqliteDSN(path))
	if err != nil {
		return nil, fmt.Errorf("opening sqlite: %w", err)
	}
	conn.SetMaxOpenConns(1)

	if err := conn.Ping(); err != nil {
		return nil, fmt.Errorf("pinging database: %w", err)
	}

	return &DB{conn: conn}, nil
}

// Conn returns the underlying sql.DB connection.
func (db *DB) Conn() *sql.DB {
	return db.conn
}

// Close closes the database connection.
func (db *DB) Close() error {
	return db.conn.Close()
}

func sqliteDSN(path string) string {
	separator := "?"
	if strings.Contains(path, "?") {
		separator = "&"
	}
	return fmt.Sprintf(
		"%s%s_journal_mode=WAL&_foreign_keys=on&_busy_timeout=%d&_txlock=immediate",
		path,
		separator,
		sqliteBusyTimeout.Milliseconds(),
	)
}
