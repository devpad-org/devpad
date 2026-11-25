package main

import (
	"context"
	"errors"
	"path/filepath"
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
		status, err := manager.Wait(context.Background(), commandID, 0)
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

	status, err := manager.Wait(context.Background(), commandID, 0)
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

func TestCommandSessionManagerWaitReturnsOnceTheCommandFinishes(t *testing.T) {
	requireCommandShell(t)
	manager := newCommandSessionManager(64*1024, 4, time.Minute)

	session, err := manager.Start(`sleep 0.1; exit 3`, t.TempDir())
	if err != nil {
		t.Fatalf("start command: %v", err)
	}

	began := time.Now()
	status, err := manager.Wait(context.Background(), session.CommandID, 10*time.Second)
	if err != nil {
		t.Fatalf("wait for command: %v", err)
	}
	if status.Status != "exited" || status.ExitCode == nil || *status.ExitCode != 3 {
		t.Fatalf("expected exit code 3, got %+v", status)
	}
	if waited := time.Since(began); waited > 5*time.Second {
		t.Fatalf("wait went on for %s after the command finished", waited)
	}
}

func TestCommandSessionManagerWaitReturnsAtItsDeadline(t *testing.T) {
	requireCommandShell(t)
	manager := newCommandSessionManager(64*1024, 4, time.Minute)

	session, err := manager.Start(`while true; do sleep 1; done`, t.TempDir())
	if err != nil {
		t.Fatalf("start command: %v", err)
	}
	defer manager.Stop(context.Background(), session.CommandID)

	status, err := manager.Wait(context.Background(), session.CommandID, 50*time.Millisecond)
	if err != nil {
		t.Fatalf("wait for command: %v", err)
	}
	if status.Status != "running" {
		t.Fatalf("expected the command still running, got %+v", status)
	}
}

func TestCommandSessionManagerKeepsTheNewestFinishedCommands(t *testing.T) {
	requireCommandShell(t)
	manager := newCommandSessionManager(64*1024, 4, time.Minute)

	var ids []string
	for range maxFinishedCommandSessions + 2 {
		session, err := manager.Start(`true`, t.TempDir())
		if err != nil {
			t.Fatalf("start command: %v", err)
		}
		if _, err := manager.Wait(context.Background(), session.CommandID, 10*time.Second); err != nil {
			t.Fatalf("wait for command: %v", err)
		}
		ids = append(ids, session.CommandID)
	}
	// The last start pruned the commands before it down to the most kept.
	if _, err := manager.Start(`true`, t.TempDir()); err != nil {
		t.Fatalf("start command: %v", err)
	}

	for i, id := range ids {
		_, err := manager.Wait(context.Background(), id, 0)
		if gone := errors.Is(err, errCommandSessionNotFound); gone != (i < 2) {
			t.Fatalf("command %d: gone=%t (err %v), want gone only for the two oldest", i, gone, err)
		}
	}
}

func TestCommandSessionManagerRefusesAMissingWorkingDirectory(t *testing.T) {
	manager := newCommandSessionManager(64*1024, 4, time.Minute)

	_, err := manager.Start(`true`, filepath.Join(t.TempDir(), "missing"))
	if !errors.Is(err, errNoWorkingDirectory) {
		t.Fatalf("expected a missing working directory refused, got %v", err)
	}
}
