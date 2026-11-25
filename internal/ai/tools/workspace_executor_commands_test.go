package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/devpad-org/devpad/internal/agent"
)

// fakeCommand is one managed command in a stubbed workspace.
type fakeCommand struct {
	output   string
	exitCode int
	// running is how many waits the command is still running for; -1 is
	// every wait.
	running int
	// dropped is how much of the output's start the agent no longer keeps.
	dropped int64

	command, cwd string
	stopped      bool
	waits        []time.Duration
	reads        []agent.CommandOutputOptions
}

func (f *fakeCommand) ops() *stubWorkspaceOps {
	return &stubWorkspaceOps{
		startCommandFn: func(_ context.Context, _, _ int64, command, cwd string) (*agent.ManagedCommand, error) {
			f.command, f.cwd = command, cwd
			return &agent.ManagedCommand{CommandID: "cmd_1", Command: command, Status: "running"}, nil
		},
		waitCommandFn: func(ctx context.Context, _, _ int64, _ string, wait time.Duration) (*agent.ManagedCommand, error) {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			f.waits = append(f.waits, wait)
			status := f.status()
			if f.running > 0 {
				f.running--
			}
			return status, nil
		},
		readCommandOutputFn: func(_ context.Context, _, _ int64, _ string, opts agent.CommandOutputOptions) (*agent.CommandOutput, error) {
			f.reads = append(f.reads, opts)
			end := int64(len(f.output))
			cursor := min(max(opts.Cursor, f.dropped), end)
			next := min(cursor+int64(opts.MaxBytes), end)
			return &agent.CommandOutput{
				CommandID:  "cmd_1",
				Status:     f.status().Status,
				Output:     f.output[cursor:next],
				Cursor:     cursor,
				NextCursor: next,
				Truncated:  opts.Cursor < f.dropped,
				HasMore:    next < end,
			}, nil
		},
		stopCommandFn: func(context.Context, int64, int64, string) (*agent.ManagedCommand, error) {
			f.stopped = true
			return f.status(), nil
		},
	}
}

func (f *fakeCommand) status() *agent.ManagedCommand {
	status := &agent.ManagedCommand{CommandID: "cmd_1", Command: f.command, Cursor: int64(len(f.output))}
	switch {
	case f.stopped:
		status.Status = "stopped"
		code := -1
		status.ExitCode = &code
	case f.running != 0:
		status.Status = "running"
	default:
		status.Status = "exited"
		code := f.exitCode
		status.ExitCode = &code
	}
	return status
}

// numberedLines is output of n lines of ten bytes each, "line 0000\n" first.
func numberedLines(n int) string {
	var b strings.Builder
	for i := range n {
		fmt.Fprintf(&b, "line %04d\n", i)
	}
	return b.String()
}

func shortenCommandSecond(t *testing.T) {
	t.Helper()
	saved := commandSecond
	commandSecond = time.Millisecond
	t.Cleanup(func() { commandSecond = saved })
}

func TestRunCommandWaitsForTheCommandAndReturnsItsOutput(t *testing.T) {
	f := &fakeCommand{output: "ok\n"}
	result := NewWorkspaceExecutor(f.ops()).ExecuteTool(context.Background(), toolReq(1, 2, "run_command", []byte(`{"command":"go test ./...","cwd":"backend"}`)))

	if result.IsError || result.Content != "ok\n" {
		t.Fatalf("expected the command's output, got %+v", result)
	}
	if f.command != "go test ./..." || f.cwd != "backend" {
		t.Fatalf("started %q in %q", f.command, f.cwd)
	}
	if len(f.waits) != 1 || f.waits[0] <= 119*time.Second || f.waits[0] > 120*time.Second {
		t.Fatalf("expected one wait of the default two minutes, got %v", f.waits)
	}
	if f.stopped {
		t.Fatal("stopped a command that finished")
	}
}

func TestRunCommandWaitsForTheTimeoutItIsGiven(t *testing.T) {
	f := &fakeCommand{}
	NewWorkspaceExecutor(f.ops()).ExecuteTool(context.Background(), toolReq(1, 2, "run_command", []byte(`{"command":"make","timeout_seconds":30}`)))

	if len(f.waits) != 1 || f.waits[0] <= 29*time.Second || f.waits[0] > 30*time.Second {
		t.Fatalf("expected one wait of 30 seconds, got %v", f.waits)
	}
}

func TestRunCommandHandsOverACommandThatOutlastsACall(t *testing.T) {
	shortenCommandSecond(t)
	saved := maxToolWait
	maxToolWait = 20 * time.Millisecond
	t.Cleanup(func() { maxToolWait = saved })

	f := &fakeCommand{output: "running tests\n", running: -1}
	ops := f.ops()
	stopped := make(chan time.Time, 1)
	ops.stopCommandFn = func(context.Context, int64, int64, string) (*agent.ManagedCommand, error) {
		stopped <- time.Now()
		return &agent.ManagedCommand{CommandID: "cmd_1", Status: "stopped"}, nil
	}
	began := time.Now()
	result := NewWorkspaceExecutor(ops).ExecuteTool(context.Background(), toolReq(1, 2, "run_command", []byte(`{"command":"go test ./...","timeout_seconds":300}`)))

	if result.IsError || !strings.Contains(result.Content, "still running") || !strings.Contains(result.Content, `command_id "cmd_1", wait_for_exit true, cursor 14`) || !strings.HasSuffix(result.Content, "\nrunning tests\n") {
		t.Fatalf("expected the command handed over, got %+v", result)
	}
	select {
	case <-stopped:
		t.Fatal("stopped a command handed over before its timeout")
	default:
	}
	// Its timeout of 300 shortened seconds is up 300ms after it started.
	select {
	case at := <-stopped:
		if at.Sub(began) < 250*time.Millisecond {
			t.Fatalf("stopped after %s, before its timeout", at.Sub(began))
		}
	case <-time.After(5 * time.Second):
		t.Fatal("never stopped the command at its timeout")
	}
}

func TestRunCommandReportsTheExitCode(t *testing.T) {
	f := &fakeCommand{output: "FAIL ./internal/auth\n", exitCode: 2}
	result := NewWorkspaceExecutor(f.ops()).ExecuteTool(context.Background(), toolReq(1, 2, "run_command", []byte(`{"command":"go test ./..."}`)))

	if !result.IsError || result.Name != "run_command" {
		t.Fatalf("expected a failed run_command, got %+v", result)
	}
	if !strings.Contains(result.Content, "exit code 2") || !strings.Contains(result.Content, "FAIL ./internal/auth") {
		t.Fatalf("expected the exit code and output, got %q", result.Content)
	}
}

func TestRunCommandStopsACommandThatTimesOut(t *testing.T) {
	shortenCommandSecond(t)
	f := &fakeCommand{output: "still going\n", running: -1}
	result := NewWorkspaceExecutor(f.ops()).ExecuteTool(context.Background(), toolReq(1, 2, "run_command", []byte(`{"command":"sleep 1000","timeout_seconds":1}`)))

	if !f.stopped {
		t.Fatal("left the command running")
	}
	if !result.IsError || !strings.Contains(result.Content, "timed out after 1 second and was stopped") || !strings.Contains(result.Content, "still going") {
		t.Fatalf("expected a timeout with the output so far, got %q", result.Content)
	}
}

func TestRunCommandStopsTheCommandWhenTheCallEnds(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	f := &fakeCommand{running: -1}
	ops := f.ops()
	start := ops.startCommandFn
	ops.startCommandFn = func(ctx context.Context, userID, workspaceID int64, command, cwd string) (*agent.ManagedCommand, error) {
		defer cancel()
		return start(ctx, userID, workspaceID, command, cwd)
	}
	NewWorkspaceExecutor(ops).ExecuteTool(ctx, toolReq(1, 2, "run_command", []byte(`{"command":"go test ./..."}`)))

	if !f.stopped {
		t.Fatal("left the command running after its call ended")
	}
}

func TestRunCommandKeepsTheStartAndEndOfLongOutput(t *testing.T) {
	f := &fakeCommand{output: numberedLines(3000), exitCode: 1}
	result := NewWorkspaceExecutor(f.ops()).ExecuteTool(context.Background(), toolReq(1, 2, "run_command", []byte(`{"command":"go test ./..."}`)))

	// The head is the first 4096 bytes cut after their last whole line; the
	// tail the last 12288 cut before their first.
	headEnd, tailStart := 4090, 17720
	marker := fmt.Sprintf("line 0408\n\n[… %d bytes of output left out here. Read them with read_command_output: command_id %q, cursor %d; they end at cursor %d.]\n\nline 1772\n", tailStart-headEnd, "cmd_1", headEnd, tailStart)
	if !strings.Contains(result.Content, marker) {
		t.Fatalf("expected the part left out marked %q, got %q", marker, result.Content)
	}
	if !strings.HasPrefix(result.Content, "Command failed (exit code 1):\nline 0000\n") || !strings.HasSuffix(result.Content, "line 2999\n") {
		t.Fatalf("expected the output's first and last lines, got %q", result.Content)
	}
	if strings.Contains(result.Content, "line 0409") || strings.Contains(result.Content, "line 1771") {
		t.Fatalf("expected the middle left out, got %q", result.Content)
	}
	if len(result.Content) > maxToolTextBytes+300 {
		t.Fatalf("returned %d bytes", len(result.Content))
	}
}

func TestRunCommandKeepsCursorsExactWhenItCannotCutAtALine(t *testing.T) {
	// The agent sends a read cut through a character with U+FFFD in place
	// of its bytes, so the text is longer than the bytes it stands for.
	ops := (&fakeCommand{output: strings.Repeat("x", 30000)}).ops()
	read := ops.readCommandOutputFn
	ops.readCommandOutputFn = func(ctx context.Context, userID, workspaceID int64, commandID string, opts agent.CommandOutputOptions) (*agent.CommandOutput, error) {
		out, err := read(ctx, userID, workspaceID, commandID, opts)
		if err == nil {
			// Newlines near both ends invite a cut at a line.
			out.Output = "�\n" + out.Output[2:len(out.Output)-1] + "\n"
		}
		return out, err
	}
	result := NewWorkspaceExecutor(ops).ExecuteTool(context.Background(), toolReq(1, 2, "run_command", []byte(`{"command":"cat"}`)))

	if !strings.Contains(result.Content, "cursor 4096; they end at cursor 17712.") {
		t.Fatalf("expected the cursors of the reads, got %q", result.Content)
	}
}

func TestRunCommandSaysWhenTheStartOfItsOutputIsGone(t *testing.T) {
	f := &fakeCommand{output: numberedLines(20), dropped: 100}
	result := NewWorkspaceExecutor(f.ops()).ExecuteTool(context.Background(), toolReq(1, 2, "run_command", []byte(`{"command":"make"}`)))

	if !strings.HasPrefix(result.Content, "[Output before cursor 100 is no longer kept.]\nline 0010\n") {
		t.Fatalf("expected a note of the output lost, got %q", result.Content)
	}
}

func TestRunCommandSaysWhatToDoWhenTooManyCommandsRun(t *testing.T) {
	ops := &stubWorkspaceOps{startCommandFn: func(context.Context, int64, int64, string, string) (*agent.ManagedCommand, error) {
		return nil, &agent.AgentError{StatusCode: http.StatusTooManyRequests, Message: "too many running command sessions"}
	}}
	result := NewWorkspaceExecutor(ops).ExecuteTool(context.Background(), toolReq(1, 2, "run_command", []byte(`{"command":"make"}`)))

	if !result.IsError || !strings.Contains(result.Content, "stop_command") {
		t.Fatalf("expected a hint to stop a command, got %q", result.Content)
	}
}

func TestReadCommandOutputWaitsForTheCommandToExit(t *testing.T) {
	f := &fakeCommand{output: "building\ndone\n", running: 1}
	result := NewWorkspaceExecutor(f.ops()).ExecuteTool(context.Background(), toolReq(1, 2, "read_command_output", []byte(`{"command_id":"cmd_1","wait_for_exit":true,"wait_ms":60000}`)))

	if result.IsError {
		t.Fatalf("expected success, got %+v", result)
	}
	var payload agent.CommandOutput
	if err := json.Unmarshal([]byte(result.Content), &payload); err != nil {
		t.Fatalf("unmarshal result content: %v", err)
	}
	if payload.Status != "exited" || payload.ExitCode == nil || *payload.ExitCode != 0 || payload.Output != "building\ndone\n" || payload.NextCursor != 14 {
		t.Fatalf("unexpected payload: %+v", payload)
	}
	if len(f.waits) != 2 || f.waits[0] <= 59*time.Second || f.waits[0] > 60*time.Second {
		t.Fatalf("expected a wait of a minute, asked again while it ran, got %v", f.waits)
	}
}

func TestReadCommandOutputAtExitKeepsTheStartAndEndFromItsCursor(t *testing.T) {
	f := &fakeCommand{output: numberedLines(300)}
	result := NewWorkspaceExecutor(f.ops()).ExecuteTool(context.Background(), toolReq(1, 2, "read_command_output", []byte(`{"command_id":"cmd_1","cursor":100,"max_bytes":1000,"wait_for_exit":true}`)))

	var payload agent.CommandOutput
	if err := json.Unmarshal([]byte(result.Content), &payload); err != nil {
		t.Fatalf("unmarshal result content: %v", err)
	}
	if !strings.HasPrefix(payload.Output, "line 0010\n") || !strings.HasSuffix(payload.Output, "line 0299\n") || !strings.Contains(payload.Output, "left out here") {
		t.Fatalf("expected the start and end of the output after cursor 100, got %q", payload.Output)
	}
	if payload.Cursor != 100 || payload.NextCursor != 3000 {
		t.Fatalf("expected cursors 100 to 3000, got %+v", payload)
	}
	if len(f.waits) != 1 || f.waits[0] <= 3*time.Minute || f.waits[0] > maxToolWait {
		t.Fatalf("expected one wait of as long as a tool call waits, got %v", f.waits)
	}
}

func TestReadCommandOutputCapsMaxBytes(t *testing.T) {
	f := &fakeCommand{output: "x"}
	NewWorkspaceExecutor(f.ops()).ExecuteTool(context.Background(), toolReq(1, 2, "read_command_output", []byte(`{"command_id":"cmd_1","max_bytes":1000000}`)))

	if len(f.reads) != 1 || f.reads[0].MaxBytes != maxToolTextBytes {
		t.Fatalf("expected a read of at most %d bytes, got %+v", maxToolTextBytes, f.reads)
	}
}
