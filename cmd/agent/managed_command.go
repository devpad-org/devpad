package main

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"sync"
	"time"
)

const (
	defaultCommandReadBytes     = 12 * 1024
	maxCommandReadBytes         = 64 * 1024
	maxManagedCommandOutput     = 1024 * 1024
	maxManagedCommandSessions   = 8
	managedCommandCleanupAfter  = 30 * time.Minute
	managedCommandStopGraceTime = 2 * time.Second
)

var (
	errCommandSessionNotFound = errors.New("command session not found")
	errTooManyCommandSessions = errors.New("too many running command sessions")
	commandSessions           = newCommandSessionManager(maxManagedCommandOutput, maxManagedCommandSessions, managedCommandCleanupAfter)
)

type commandSessionManager struct {
	mu           sync.Mutex
	sessions     map[string]*managedCommand
	maxOutput    int
	maxSessions  int
	cleanupAfter time.Duration
}

type managedCommand struct {
	mu            sync.Mutex
	id            string
	command       string
	cwd           string
	cmd           *exec.Cmd
	pid           int
	status        string
	exitCode      *int
	errorMessage  string
	startedAt     time.Time
	finishedAt    *time.Time
	stopRequested bool
	output        *commandOutputBuffer
	done          chan struct{}
}

type commandSessionResult struct {
	CommandID  string `json:"command_id"`
	Command    string `json:"command"`
	CWD        string `json:"cwd"`
	Status     string `json:"status"`
	ExitCode   *int   `json:"exit_code,omitempty"`
	Error      string `json:"error,omitempty"`
	StartedAt  string `json:"started_at"`
	FinishedAt string `json:"finished_at,omitempty"`
	Cursor     int64  `json:"cursor"`
}

type commandOutputResult struct {
	CommandID  string `json:"command_id"`
	Status     string `json:"status"`
	Output     string `json:"output"`
	Cursor     int64  `json:"cursor"`
	NextCursor int64  `json:"next_cursor"`
	Truncated  bool   `json:"truncated"`
	HasMore    bool   `json:"has_more"`
	ExitCode   *int   `json:"exit_code,omitempty"`
	Error      string `json:"error,omitempty"`
}

type commandOutputBuffer struct {
	mu     sync.Mutex
	data   []byte
	start  int64
	next   int64
	max    int
	notify chan struct{}
}

func newCommandSessionManager(maxOutput, maxSessions int, cleanupAfter time.Duration) *commandSessionManager {
	return &commandSessionManager{
		sessions:     make(map[string]*managedCommand),
		maxOutput:    maxOutput,
		maxSessions:  maxSessions,
		cleanupAfter: cleanupAfter,
	}
}

func (m *commandSessionManager) Start(command, cwd string) (*commandSessionResult, error) {
	if command == "" {
		return nil, errors.New("command is required")
	}
	if cwd == "" {
		cwd = workspaceRoot
	}

	id, err := newCommandSessionID()
	if err != nil {
		return nil, fmt.Errorf("generating command session id: %w", err)
	}

	session := &managedCommand{
		id:        id,
		command:   command,
		cwd:       cwd,
		status:    "starting",
		startedAt: time.Now().UTC(),
		output:    newCommandOutputBuffer(m.maxOutput),
		done:      make(chan struct{}),
	}

	m.mu.Lock()
	m.pruneLocked(time.Now().UTC())
	if m.runningSessionsLocked() >= m.maxSessions {
		m.mu.Unlock()
		return nil, errTooManyCommandSessions
	}
	m.sessions[id] = session
	m.mu.Unlock()

	cmd := exec.Command(commandShell, "-lc", command)
	cmd.Dir = cwd
	cmd.SysProcAttr = commandSysProcAttr()
	cmd.Stdout = session.output
	cmd.Stderr = session.output

	if err := cmd.Start(); err != nil {
		m.remove(id)
		return nil, fmt.Errorf("starting command with %s: %w", commandShell, err)
	}

	session.mu.Lock()
	session.cmd = cmd
	session.pid = cmd.Process.Pid
	session.status = "running"
	session.mu.Unlock()

	go session.wait()

	result := session.snapshot()
	return &result, nil
}

func (m *commandSessionManager) Status(commandID string) (*commandSessionResult, error) {
	session, err := m.get(commandID)
	if err != nil {
		return nil, err
	}
	result := session.snapshot()
	return &result, nil
}

func (m *commandSessionManager) ReadOutput(ctx context.Context, commandID string, cursor int64, maxBytes, waitMS int) (*commandOutputResult, error) {
	session, err := m.get(commandID)
	if err != nil {
		return nil, err
	}
	if maxBytes <= 0 {
		maxBytes = defaultCommandReadBytes
	}
	if maxBytes > maxCommandReadBytes {
		maxBytes = maxCommandReadBytes
	}
	if waitMS < 0 {
		waitMS = 0
	}
	if waitMS > 5000 {
		waitMS = 5000
	}

	deadline := time.NewTimer(time.Duration(waitMS) * time.Millisecond)
	defer deadline.Stop()

	for {
		result, notify := session.outputSnapshot(cursor, maxBytes)
		if result.Output != "" || result.Status != "running" || waitMS == 0 {
			return &result, nil
		}

		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("reading command output: %w", ctx.Err())
		case <-notify:
		case <-session.done:
		case <-deadline.C:
			result, _ := session.outputSnapshot(cursor, maxBytes)
			return &result, nil
		}
	}
}

func (m *commandSessionManager) Stop(ctx context.Context, commandID string) (*commandSessionResult, error) {
	session, err := m.get(commandID)
	if err != nil {
		return nil, err
	}
	if err := session.stop(ctx); err != nil {
		return nil, err
	}
	result := session.snapshot()
	return &result, nil
}

func (m *commandSessionManager) get(commandID string) (*managedCommand, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	session, ok := m.sessions[commandID]
	if !ok {
		return nil, errCommandSessionNotFound
	}
	return session, nil
}

func (m *commandSessionManager) remove(commandID string) {
	m.mu.Lock()
	delete(m.sessions, commandID)
	m.mu.Unlock()
}

func (m *commandSessionManager) runningSessionsLocked() int {
	running := 0
	for _, session := range m.sessions {
		if session.isActive() {
			running++
		}
	}
	return running
}

func (m *commandSessionManager) pruneLocked(now time.Time) {
	for id, session := range m.sessions {
		finishedAt := session.finishedAtTime()
		if finishedAt != nil && now.Sub(*finishedAt) > m.cleanupAfter {
			delete(m.sessions, id)
		}
	}
}
