package main

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestRunShellCommandUsesBash(t *testing.T) {
	requireCommandShell(t)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	result, err := runShellCommand(ctx, `[[ -n "$BASH_VERSION" ]] && printf "bash"`, t.TempDir())
	if err != nil {
		t.Fatalf("run shell command: %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("expected exit code 0, got %d with output %q", result.ExitCode, result.Output)
	}
	if result.Output != "bash" {
		t.Fatalf("expected bash output, got %q", result.Output)
	}
}

func TestRunShellCommandReturnsExitCodeAndOutput(t *testing.T) {
	requireCommandShell(t)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	result, err := runShellCommand(ctx, `printf "failed"; exit 7`, t.TempDir())
	if err != nil {
		t.Fatalf("run shell command: %v", err)
	}
	if result.ExitCode != 7 {
		t.Fatalf("expected exit code 7, got %d", result.ExitCode)
	}
	if result.Output != "failed" {
		t.Fatalf("expected failed output, got %q", result.Output)
	}
}

func requireCommandShell(t *testing.T) {
	t.Helper()

	if _, err := os.Stat(commandShell); err != nil {
		t.Skipf("%s is not available: %v", commandShell, err)
	}
}
