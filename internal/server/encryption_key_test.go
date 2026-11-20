package server

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Devpad refuses to start without a usable encryption key, before it opens
// or migrates the database, rather than seal secrets with a key it would
// lose on restart.
func TestNewRefusesToStartWithoutAUsableEncryptionKey(t *testing.T) {
	for _, tt := range []struct {
		name string
		key  string
		want string
	}{
		{"missing", "", "an encryption key is required"},
		{"not hex", strings.Repeat("z", 64), "invalid encryption key"},
		{"too short", strings.Repeat("a", 32), "invalid encryption key"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dbPath := filepath.Join(t.TempDir(), "devpad.db")
			srv, err := New(Config{DBPath: dbPath, EncryptionKey: tt.key})
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("expected an error containing %q, got %v", tt.want, err)
			}
			if srv != nil {
				t.Fatalf("expected no server, got %+v", srv)
			}
			if _, err := os.Stat(dbPath); !errors.Is(err, fs.ErrNotExist) {
				t.Fatalf("expected no database created, got %v", err)
			}
		})
	}
}
