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

const commandTimeout = 30 * time.Second
const maxOutputSize = 64 * 1024 // 64KB

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

	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", req.Command)
	cmd.Dir = workspaceRoot

	output, err := cmd.CombinedOutput()

	// Truncate output if too large
	result := string(output)
	if len(result) > maxOutputSize {
		result = result[:maxOutputSize] + fmt.Sprintf("\n... (output truncated, %d bytes total)", len(output))
	}

	exitCode := 0
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			writeJSON(w, http.StatusOK, map[string]any{
				"output":    strings.TrimRight(result, "\n"),
				"exit_code": -1,
				"error":     "command timed out after 30 seconds",
			})
			return
		}
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			writeErr(w, http.StatusInternalServerError, fmt.Sprintf("failed to execute command: %v", err))
			return
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"output":    strings.TrimRight(result, "\n"),
		"exit_code": exitCode,
	})
}
