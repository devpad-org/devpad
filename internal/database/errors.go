package database

import (
	"errors"

	"github.com/mattn/go-sqlite3"
)

// IsUniqueViolation reports whether err, or an error it wraps, is SQLite
// refusing a write because it would break a UNIQUE constraint.
func IsUniqueViolation(err error) bool {
	var se sqlite3.Error
	return errors.As(err, &se) && se.ExtendedCode == sqlite3.ErrConstraintUnique
}
