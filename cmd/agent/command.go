package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os/exec"
	"strings"
	"time"
)

const (
	commandTimeout = 30 * time.Second
	commandShell   = "/bin/bash"
	maxOutputSize  = 64 * 1024 // 64KB
)

type commandResult struct {
	Output   string `json:"output"`
	ExitCode int    `json:"exit_code"`
	Error    string `json:"error,omitempty"`
}

func handleRunCommand(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Command string `json:"command"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.Command == "" {
		writeErr(w, http.StatusBadRequest, "command is required")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), commandTimeout)
	defer cancel()

	result, err := runShellCommand(ctx, req.Command, workspaceRoot)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, result)
}

func runShellCommand(ctx context.Context, command, workDir string) (commandResult, error) {
	cmd := exec.CommandContext(ctx, commandShell, "-lc", command)
	cmd.Dir = workDir
	output, err := cmd.CombinedOutput()

	result := string(output)
	if len(result) > maxOutputSize {
		result = result[:maxOutputSize] + fmt.Sprintf("\n... (output truncated, %d bytes total)", len(output))
	}
	result = strings.TrimRight(result, "\n")

	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return commandResult{
				Output:   result,
				ExitCode: -1,
				Error:    "command timed out after 30 seconds",
			}, nil
		}
		if exitErr, ok := err.(*exec.ExitError); ok {
			return commandResult{
				Output:   result,
				ExitCode: exitErr.ExitCode(),
			}, nil
		}
		return commandResult{}, fmt.Errorf("failed to execute command with %s: %w", commandShell, err)
	}

	return commandResult{
		Output:   result,
		ExitCode: 0,
	}, nil
}
