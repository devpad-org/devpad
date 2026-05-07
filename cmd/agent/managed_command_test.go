package main

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestCommandSessionManagerCapturesOutputAndExit(t *testing.T) {
	requireCommandShell(t)
	manager := newCommandSessionManager(64*1024, 4, time.Minute)

	session, err := manager.Start(`printf "hello"; exit 7`, t.TempDir())
	if err != nil {
		t.Fatalf("start command: %v", err)
	}

	status := waitForCommandStatus(t, manager, session.CommandID, "exited")
	if status.ExitCode == nil || *status.ExitCode != 7 {
		t.Fatalf("expected exit code 7, got %+v", status.ExitCode)
	}

	output, err := manager.ReadOutput(context.Background(), session.CommandID, 0, 1024, 0)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	if output.Output != "hello" {
		t.Fatalf("expected captured output, got %q", output.Output)
	}
	if output.Status != "exited" {
		t.Fatalf("expected exited output status, got %q", output.Status)
	}
}

func TestCommandSessionManagerReadOutputWaitsForData(t *testing.T) {
	requireCommandShell(t)
	manager := newCommandSessionManager(64*1024, 4, time.Minute)

	session, err := manager.Start(`sleep 0.1; printf "ready"`, t.TempDir())
	if err != nil {
		t.Fatalf("start command: %v", err)
	}

	output, err := manager.ReadOutput(context.Background(), session.CommandID, 0, 1024, 1000)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	if output.Output != "ready" {
		t.Fatalf("expected delayed output, got %q", output.Output)
	}
}

func TestCommandSessionManagerStopsRunningCommand(t *testing.T) {
	requireCommandShell(t)
	manager := newCommandSessionManager(64*1024, 4, time.Minute)

	session, err := manager.Start(`while true; do sleep 1; done`, t.TempDir())
	if err != nil {
		t.Fatalf("start command: %v", err)
	}

	status, err := manager.Stop(context.Background(), session.CommandID)
	if err != nil {
		t.Fatalf("stop command: %v", err)
	}
	if status.Status != "stopped" {
		t.Fatalf("expected stopped status, got %+v", status)
	}
}

func TestCommandOutputBufferTruncatesOldData(t *testing.T) {
	buffer := newCommandOutputBuffer(5)
	if _, err := buffer.Write([]byte("hello")); err != nil {
		t.Fatalf("write output: %v", err)
	}
	if _, err := buffer.Write([]byte(" world")); err != nil {
		t.Fatalf("write output: %v", err)
	}

	output, cursor, nextCursor, truncated, hasMore, _ := buffer.snapshot(0, 10)
	if output != "world" {
		t.Fatalf("expected retained tail output, got %q", output)
	}
	if cursor != 6 {
		t.Fatalf("expected effective cursor 6, got %d", cursor)
	}
	if nextCursor != 11 {
		t.Fatalf("expected next cursor 11, got %d", nextCursor)
	}
	if !truncated {
		t.Fatal("expected truncated result")
	}
	if hasMore {
		t.Fatal("did not expect more output")
	}
}

func waitForCommandStatus(t *testing.T, manager *commandSessionManager, commandID, want string) *commandSessionResult {
	t.Helper()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		status, err := manager.Status(commandID)
		if err != nil {
			t.Fatalf("command status: %v", err)
		}
		if status.Status == want {
			return status
		}
		if status.Status != "running" && status.Status != "starting" && status.Status != "stopping" {
			t.Fatalf("command finished with unexpected status %q", status.Status)
		}
		time.Sleep(10 * time.Millisecond)
	}

	status, err := manager.Status(commandID)
	if err != nil {
		t.Fatalf("command status: %v", err)
	}
	t.Fatalf("timed out waiting for status %q; last status was %q output cursor=%d", want, status.Status, status.Cursor)
	return nil
}

func TestCommandSessionManagerReadOutputReportsHasMore(t *testing.T) {
	buffer := newCommandOutputBuffer(100)
	if _, err := buffer.Write([]byte("abcdef")); err != nil {
		t.Fatalf("write output: %v", err)
	}

	output, _, nextCursor, truncated, hasMore, _ := buffer.snapshot(0, 3)
	if output != "abc" {
		t.Fatalf("expected first chunk, got %q", output)
	}
	if nextCursor != 3 {
		t.Fatalf("expected next cursor 3, got %d", nextCursor)
	}
	if truncated {
		t.Fatal("did not expect truncation")
	}
	if !hasMore {
		t.Fatal("expected more output")
	}
}

func TestCommandSessionManagerRejectsTooManyRunningCommands(t *testing.T) {
	requireCommandShell(t)
	manager := newCommandSessionManager(64*1024, 1, time.Minute)

	first, err := manager.Start(`while true; do sleep 1; done`, t.TempDir())
	if err != nil {
		t.Fatalf("start first command: %v", err)
	}
	defer manager.Stop(context.Background(), first.CommandID)

	_, err = manager.Start(`while true; do sleep 1; done`, t.TempDir())
	if err == nil {
		t.Fatal("expected too many sessions error")
	}
	if !strings.Contains(err.Error(), errTooManyCommandSessions.Error()) {
		t.Fatalf("expected too many sessions error, got %v", err)
	}
}
