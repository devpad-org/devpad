package tools

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/devpad-org/devpad/internal/agent"
	"github.com/devpad-org/devpad/internal/ai/domain"
)

const (
	// run_command waits this long for a command to finish unless the call
	// asks for another timeout_seconds, at most maxRunCommandSeconds. A
	// command still running then is stopped.
	defaultRunCommandSeconds = 120
	maxRunCommandSeconds     = 600
	// maxExitWait is the longest read_command_output waits for a command to
	// exit.
	maxExitWait = 10 * time.Minute
	// commandWaitChunk is the longest one request to the agent waits, which
	// the agent holds to five minutes, below its client's request timeout.
	commandWaitChunk = 5 * time.Minute
	// minCommandPoll paces the waits on an agent that answers at once, as
	// one from before agents could wait does.
	minCommandPoll = 250 * time.Millisecond
	// defaultCommandReadBytes is what read_command_output returns unless
	// asked for less; it returns at most maxToolTextBytes.
	defaultCommandReadBytes = 12 * 1024
	// commandStopTimeout bounds stopping a command, which the agent kills
	// two seconds after asking it to end.
	commandStopTimeout = 10 * time.Second
)

// commandSecond is the unit timeout_seconds counts in, which tests shorten.
var commandSecond = time.Second

// runCommand runs a command as a managed command and waits for it to
// finish, so that output too long to return stays readable by cursor.
func (e *WorkspaceExecutor) runCommand(ctx context.Context, userID, workspaceID int64, params map[string]any) domain.ToolResultPart {
	command, _ := params["command"].(string)
	if command == "" {
		return toolFailure("run_command", "Error: command is required")
	}
	cwd, _ := params["cwd"].(string)
	seconds := defaultRunCommandSeconds
	if value, ok := params["timeout_seconds"].(float64); ok {
		seconds = clamp(int(value), 1, maxRunCommandSeconds)
	}

	started, err := e.ws.StartCommand(ctx, userID, workspaceID, command, cwd)
	if err != nil {
		return commandStartFailure("run_command", err)
	}
	if started == nil {
		return toolFailure("run_command", "Error: empty command start response")
	}
	status, err := e.waitForExit(ctx, userID, workspaceID, started.CommandID, time.Duration(seconds)*commandSecond)
	if err != nil {
		// The command must not outlive the call that ran it, such as one
		// whose run was cancelled.
		e.stopCommandQuietly(ctx, userID, workspaceID, started.CommandID)
		return toolFailure("run_command", "Error: %v", err)
	}
	timedOut := commandActive(status.Status)
	var stopErr error
	if timedOut {
		stopCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), commandStopTimeout)
		stopped, err := e.ws.StopCommand(stopCtx, userID, workspaceID, started.CommandID)
		cancel()
		if err != nil {
			stopErr = err
		} else if stopped != nil {
			status = stopped
		}
	}

	output, err := e.commandOutput(ctx, userID, workspaceID, started.CommandID, 0, status.Cursor, maxToolTextBytes)
	if err != nil {
		return toolFailure("run_command", "Error reading the command's output: %v", err)
	}
	var message string
	switch {
	case timedOut && stopErr != nil:
		message = fmt.Sprintf("Command timed out after %s, and stopping it failed: %v. Stop it with stop_command, command_id %q.", plural(seconds, "second", "seconds"), stopErr, started.CommandID)
	case timedOut:
		message = fmt.Sprintf("Command timed out after %s and was stopped. Give a command that takes longer a higher timeout_seconds, at most %d, or run it with start_command.", plural(seconds, "second", "seconds"), maxRunCommandSeconds)
	case status.ExitCode == nil:
		message = "Command failed"
		if status.Error != "" {
			message += ": " + status.Error
		}
	case *status.ExitCode != 0:
		message = fmt.Sprintf("Command failed (exit code %d):", *status.ExitCode)
	default:
		return toolSuccess("run_command", output)
	}
	if output != "" {
		message += "\n" + output
	}
	return toolFailure("run_command", "%s", message)
}

// commandStartFailure reports a command that could not start, saying what
// to do when the workspace already runs as many commands as it may.
func commandStartFailure(toolName string, err error) domain.ToolResultPart {
	var agentErr *agent.AgentError
	if errors.As(err, &agentErr) && agentErr.StatusCode == http.StatusTooManyRequests {
		return toolFailure(toolName, "Error: %v. Commands from run_command and start_command count until they finish: stop one you started with start_command and no longer need, using stop_command, then try again.", err)
	}
	return toolFailure(toolName, "Error: %v", err)
}

// commandActive reports whether a command with this status has yet to
// finish.
func commandActive(status string) bool {
	return status == "starting" || status == "running" || status == "stopping"
}

// waitForExit returns a command's status once it has finished, or once
// wait has passed while it still runs, waiting in requests short enough for
// the agent to hold each.
func (e *WorkspaceExecutor) waitForExit(ctx context.Context, userID, workspaceID int64, commandID string, wait time.Duration) (*agent.ManagedCommand, error) {
	deadline := time.Now().Add(wait)
	for {
		chunk := max(min(time.Until(deadline), commandWaitChunk), 0)
		began := time.Now()
		status, err := e.ws.WaitCommand(ctx, userID, workspaceID, commandID, chunk)
		if err != nil {
			return nil, err
		}
		if status == nil {
			return nil, errors.New("empty command status response")
		}
		if !commandActive(status.Status) || chunk == 0 {
			return status, nil
		}
		if pause := min(chunk, minCommandPoll) - time.Since(began); pause > 0 {
			timer := time.NewTimer(pause)
			select {
			case <-ctx.Done():
				timer.Stop()
				return nil, fmt.Errorf("waiting for the command: %w", ctx.Err())
			case <-timer.C:
			}
		}
	}
}

// stopCommandQuietly stops a command on a best-effort basis, even once ctx
// is done.
func (e *WorkspaceExecutor) stopCommandQuietly(ctx context.Context, userID, workspaceID int64, commandID string) {
	stopCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), commandStopTimeout)
	defer cancel()
	_, _ = e.ws.StopCommand(stopCtx, userID, workspaceID, commandID)
}

// commandOutput returns a command's output from cursor from to cursor end:
// all of it when it fits in budget bytes, else its start and its end, where
// builds and tests report what failed, with a note of the part left out,
// which read_command_output can read.
func (e *WorkspaceExecutor) commandOutput(ctx context.Context, userID, workspaceID int64, commandID string, from, end int64, budget int) (string, error) {
	read := func(cursor int64, size int) (*agent.CommandOutput, error) {
		out, err := e.ws.ReadCommandOutput(ctx, userID, workspaceID, commandID, agent.CommandOutputOptions{Cursor: cursor, MaxBytes: size})
		if err == nil && out == nil {
			err = errors.New("empty command output response")
		}
		return out, err
	}
	if end-from <= int64(budget) {
		out, err := read(from, budget)
		if err != nil {
			return "", err
		}
		return lostOutputNote(out) + out.Output, nil
	}

	headSize := budget / 4
	tailSize := budget - headSize
	head, err := read(from, headSize)
	if err != nil {
		return "", err
	}
	tail, err := read(end-int64(tailSize), tailSize)
	if err != nil {
		return "", err
	}
	headText, headEnd := headLines(head)
	tailText, tailStart := tailLines(tail)
	return fmt.Sprintf("%s%s\n\n[… %d bytes of output left out here. Read them with read_command_output: command_id %q, cursor %d; they end at cursor %d.]\n\n%s",
		lostOutputNote(head), strings.TrimSuffix(headText, "\n"), tailStart-headEnd, commandID, headEnd, tailStart, tailText), nil
}

// lostOutputNote says when a read starts later than asked because the agent
// no longer keeps the output before it.
func lostOutputNote(out *agent.CommandOutput) string {
	if !out.Truncated {
		return ""
	}
	return fmt.Sprintf("[Output before cursor %d is no longer kept.]\n", out.Cursor)
}

// headLines cuts a read after its last whole line, when that keeps at least
// half of it, and returns the cursor where what it keeps ends.
func headLines(out *agent.CommandOutput) (string, int64) {
	text := out.Output
	if !exactRead(out) {
		return text, out.NextCursor
	}
	if i := strings.LastIndexByte(text, '\n'); i >= len(text)/2 {
		return text[:i+1], out.Cursor + int64(i+1)
	}
	return text, out.NextCursor
}

// tailLines cuts a read before its first whole line, when that keeps at
// least half of it, and returns the cursor where what it keeps starts.
func tailLines(out *agent.CommandOutput) (string, int64) {
	text := out.Output
	if !exactRead(out) {
		return text, out.Cursor
	}
	if i := strings.IndexByte(text, '\n'); i >= 0 && i < len(text)/2 {
		return text[i+1:], out.Cursor + int64(i+1)
	}
	return text, out.Cursor
}

// exactRead reports whether a read's text is the bytes between its cursors.
// The agent replaces bytes that are not UTF-8, as a read cut through a
// character starts or ends with, which shifts the text against the cursors.
func exactRead(out *agent.CommandOutput) bool {
	return int64(len(out.Output)) == out.NextCursor-out.Cursor
}

func (e *WorkspaceExecutor) startCommand(ctx context.Context, userID, workspaceID int64, params map[string]any) domain.ToolResultPart {
	command, _ := params["command"].(string)
	if command == "" {
		return toolFailure("start_command", "Error: command is required")
	}
	cwd, _ := params["cwd"].(string)

	result, err := e.ws.StartCommand(ctx, userID, workspaceID, command, cwd)
	if err != nil {
		return commandStartFailure("start_command", err)
	}
	return marshalToolResponse("start_command", result)
}

func (e *WorkspaceExecutor) readCommandOutput(ctx context.Context, userID, workspaceID int64, params map[string]any) domain.ToolResultPart {
	commandID, _ := params["command_id"].(string)
	if commandID == "" {
		return toolFailure("read_command_output", "Error: command_id is required")
	}

	opts := agent.CommandOutputOptions{MaxBytes: defaultCommandReadBytes}
	if value, ok := params["cursor"].(float64); ok {
		opts.Cursor = max(int64(value), 0)
	}
	if value, ok := params["max_bytes"].(float64); ok {
		opts.MaxBytes = clamp(int(value), 1, maxToolTextBytes)
	}
	if value, ok := params["wait_ms"].(float64); ok {
		opts.WaitMS = int(value)
	}
	if untilExit, _ := params["wait_for_exit"].(bool); untilExit {
		return e.readCommandOutputAtExit(ctx, userID, workspaceID, commandID, opts)
	}

	result, err := e.ws.ReadCommandOutput(ctx, userID, workspaceID, commandID, opts)
	if err != nil {
		return toolFailure("read_command_output", "Error: %v", err)
	}
	return marshalToolResponse("read_command_output", result)
}

// readCommandOutputAtExit waits for a command to exit, for up to the call's
// wait_ms, and returns its output since the call's cursor as run_command
// would: its start and end when it is too long.
func (e *WorkspaceExecutor) readCommandOutputAtExit(ctx context.Context, userID, workspaceID int64, commandID string, opts agent.CommandOutputOptions) domain.ToolResultPart {
	wait := maxExitWait
	if opts.WaitMS > 0 {
		wait = min(time.Duration(opts.WaitMS)*time.Millisecond, maxExitWait)
	}
	status, err := e.waitForExit(ctx, userID, workspaceID, commandID, wait)
	if err != nil {
		return toolFailure("read_command_output", "Error: %v", err)
	}
	from := min(opts.Cursor, status.Cursor)
	output, err := e.commandOutput(ctx, userID, workspaceID, commandID, from, status.Cursor, opts.MaxBytes)
	if err != nil {
		return toolFailure("read_command_output", "Error: %v", err)
	}
	return marshalToolResponse("read_command_output", agent.CommandOutput{
		CommandID:  commandID,
		Status:     status.Status,
		Output:     output,
		Cursor:     from,
		NextCursor: status.Cursor,
		ExitCode:   status.ExitCode,
		Error:      status.Error,
	})
}

func (e *WorkspaceExecutor) commandStatus(ctx context.Context, userID, workspaceID int64, params map[string]any) domain.ToolResultPart {
	commandID, _ := params["command_id"].(string)
	if commandID == "" {
		return toolFailure("command_status", "Error: command_id is required")
	}

	result, err := e.ws.CommandStatus(ctx, userID, workspaceID, commandID)
	if err != nil {
		return toolFailure("command_status", "Error: %v", err)
	}
	return marshalToolResponse("command_status", result)
}

func (e *WorkspaceExecutor) stopCommand(ctx context.Context, userID, workspaceID int64, params map[string]any) domain.ToolResultPart {
	commandID, _ := params["command_id"].(string)
	if commandID == "" {
		return toolFailure("stop_command", "Error: command_id is required")
	}

	result, err := e.ws.StopCommand(ctx, userID, workspaceID, commandID)
	if err != nil {
		return toolFailure("stop_command", "Error: %v", err)
	}
	return marshalToolResponse("stop_command", result)
}
