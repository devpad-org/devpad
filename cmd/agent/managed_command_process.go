package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os/exec"
	"syscall"
	"time"
)

func (s *managedCommand) wait() {
	err := s.cmd.Wait()

	s.mu.Lock()
	defer s.mu.Unlock()

	finishedAt := time.Now().UTC()
	s.finishedAt = &finishedAt
	if s.stopRequested {
		s.status = "stopped"
	} else {
		s.status = "exited"
	}

	if err == nil {
		code := 0
		s.exitCode = &code
	} else if exitErr, ok := err.(*exec.ExitError); ok {
		code := exitErr.ExitCode()
		s.exitCode = &code
	} else {
		s.errorMessage = err.Error()
	}

	close(s.done)
	s.output.signal()
}

func (s *managedCommand) stop(ctx context.Context) error {
	s.mu.Lock()
	if s.status != "running" && s.status != "starting" {
		s.mu.Unlock()
		return nil
	}
	s.stopRequested = true
	s.status = "stopping"
	pid := s.pid
	done := s.done
	s.mu.Unlock()

	if err := signalProcessGroup(pid, syscall.SIGTERM); err != nil {
		return fmt.Errorf("stopping command: %w", err)
	}

	timer := time.NewTimer(managedCommandStopGraceTime)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return fmt.Errorf("stopping command: %w", ctx.Err())
	case <-done:
		return nil
	case <-timer.C:
	}

	if err := signalProcessGroup(pid, syscall.SIGKILL); err != nil {
		return fmt.Errorf("killing command: %w", err)
	}

	select {
	case <-ctx.Done():
		return fmt.Errorf("waiting for killed command: %w", ctx.Err())
	case <-done:
		return nil
	}
}

func (s *managedCommand) isActive() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.status == "starting" || s.status == "running" || s.status == "stopping"
}

func (s *managedCommand) finishedAtTime() *time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.finishedAt == nil {
		return nil
	}
	finishedAt := *s.finishedAt
	return &finishedAt
}

func (s *managedCommand) snapshot() commandSessionResult {
	s.mu.Lock()
	defer s.mu.Unlock()

	result := commandSessionResult{
		CommandID: s.id,
		Command:   s.command,
		CWD:       s.cwd,
		Status:    s.status,
		ExitCode:  cloneInt(s.exitCode),
		Error:     s.errorMessage,
		StartedAt: s.startedAt.Format(time.RFC3339Nano),
		Cursor:    s.output.nextCursor(),
	}
	if s.finishedAt != nil {
		result.FinishedAt = s.finishedAt.Format(time.RFC3339Nano)
	}
	return result
}

func (s *managedCommand) outputSnapshot(cursor int64, maxBytes int) (commandOutputResult, <-chan struct{}) {
	output, effectiveCursor, nextCursor, truncated, hasMore, notify := s.output.snapshot(cursor, maxBytes)
	status := s.snapshot()

	return commandOutputResult{
		CommandID:  s.id,
		Status:     status.Status,
		Output:     output,
		Cursor:     effectiveCursor,
		NextCursor: nextCursor,
		Truncated:  truncated,
		HasMore:    hasMore,
		ExitCode:   status.ExitCode,
		Error:      status.Error,
	}, notify
}

func commandSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setpgid: true}
}

func signalProcessGroup(pid int, sig syscall.Signal) error {
	if pid <= 0 {
		return errors.New("command process has not started")
	}
	err := syscall.Kill(-pid, sig)
	if err == syscall.ESRCH {
		return nil
	}
	return err
}

func killCommandProcessGroup(cmd *exec.Cmd, sig syscall.Signal) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	return signalProcessGroup(cmd.Process.Pid, sig)
}

func newCommandSessionID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return "cmd_" + hex.EncodeToString(raw[:]), nil
}

func cloneInt(value *int) *int {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}
