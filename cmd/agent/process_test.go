package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseProcessReadsProcMetadata(t *testing.T) {
	root := t.TempDir()
	procDir := filepath.Join(root, "42")
	if err := os.Mkdir(procDir, 0o755); err != nil {
		t.Fatalf("creating proc dir: %v", err)
	}

	stat := "42 (node server.js) S 1 0 0 0 0 0 0 0 0 0 0 300 50\n"
	if err := os.WriteFile(filepath.Join(procDir, "stat"), []byte(stat), 0o644); err != nil {
		t.Fatalf("writing stat: %v", err)
	}
	status := "Name:\tnode\nUid:\t1000\t1000\t1000\t1000\nVmRSS:\t2048 kB\n"
	if err := os.WriteFile(filepath.Join(procDir, "status"), []byte(status), 0o644); err != nil {
		t.Fatalf("writing status: %v", err)
	}
	if err := os.WriteFile(filepath.Join(procDir, "cmdline"), []byte("node\x00server.js\x00"), 0o644); err != nil {
		t.Fatalf("writing cmdline: %v", err)
	}

	got, err := parseProcess(root, 42, map[string]string{"1000": "dev"})
	if err != nil {
		t.Fatalf("parsing process: %v", err)
	}
	if got.PID != 42 || got.PPID != 1 || got.User != "dev" || got.State != "S" {
		t.Fatalf("unexpected process identity: %+v", got)
	}
	if got.Command != "node server.js" {
		t.Fatalf("expected command from cmdline, got %q", got.Command)
	}
	if got.MemoryBytes != 2048*1024 {
		t.Fatalf("expected memory bytes %d, got %d", 2048*1024, got.MemoryBytes)
	}
	if got.CPUTimeSeconds != 3 {
		t.Fatalf("expected cpu time 3, got %d", got.CPUTimeSeconds)
	}
	if !got.Killable {
		t.Fatalf("expected process to be killable")
	}
}

func TestParseProcessFallsBackToStatCommand(t *testing.T) {
	root := t.TempDir()
	procDir := filepath.Join(root, "7")
	if err := os.Mkdir(procDir, 0o755); err != nil {
		t.Fatalf("creating proc dir: %v", err)
	}
	stat := "7 (sleep) S 1 0 0 0 0 0 0 0 0 0 0 0 0\n"
	if err := os.WriteFile(filepath.Join(procDir, "stat"), []byte(stat), 0o644); err != nil {
		t.Fatalf("writing stat: %v", err)
	}
	if err := os.WriteFile(filepath.Join(procDir, "cmdline"), nil, 0o644); err != nil {
		t.Fatalf("writing cmdline: %v", err)
	}

	got, err := parseProcess(root, 7, nil)
	if err != nil {
		t.Fatalf("parsing process: %v", err)
	}
	if got.Command != "[sleep]" {
		t.Fatalf("expected fallback command, got %q", got.Command)
	}
}
