package tools

import (
	"context"
	"fmt"

	"github.com/devpad-org/devpad/internal/ai/domain"
)

func (e *WorkspaceExecutor) runCommand(ctx context.Context, userID, workspaceID int64, params map[string]any) domain.ToolResultPart {
	command, _ := params["command"].(string)
	if command == "" {
		return toolFailure("run_command", "Error: command is required")
	}
	result, err := e.ws.RunCommand(ctx, userID, workspaceID, command)
	if err != nil {
		return toolFailure("run_command", "Error: %v", err)
	}
	if result.ExitCode != 0 {
		message := fmt.Sprintf("Command failed (exit code %d):\n%s", result.ExitCode, result.Output)
		if result.Error != "" {
			message += "\n" + result.Error
		}
		return toolFailure("run_command", "%s", message)
	}
	if result.Error != "" {
		return toolFailure("run_command", "Command timed out:\n%s\n%s", result.Output, result.Error)
	}

	return toolSuccess("run_command", result.Output)
}

func (e *WorkspaceExecutor) startCommand(ctx context.Context, userID, workspaceID int64, params map[string]any) domain.ToolResultPart {
	command, _ := params["command"].(string)
	if command == "" {
		return toolFailure("start_command", "Error: command is required")
	}
	cwd, _ := params["cwd"].(string)

	result, err := e.ws.StartCommand(ctx, userID, workspaceID, command, cwd)
	if err != nil {
		return toolFailure("start_command", "Error: %v", err)
	}
	return marshalToolResponse("start_command", result)
}

func (e *WorkspaceExecutor) readCommandOutput(ctx context.Context, userID, workspaceID int64, params map[string]any) domain.ToolResultPart {
	commandID, _ := params["command_id"].(string)
	if commandID == "" {
		return toolFailure("read_command_output", "Error: command_id is required")
	}

	cursor := int64(0)
	if value, ok := params["cursor"].(float64); ok {
		cursor = int64(value)
	}
	maxBytes := 12 * 1024
	if value, ok := params["max_bytes"].(float64); ok {
		maxBytes = int(value)
	}
	waitMS := 0
	if value, ok := params["wait_ms"].(float64); ok {
		waitMS = int(value)
	}

	result, err := e.ws.ReadCommandOutput(ctx, userID, workspaceID, commandID, cursor, maxBytes, waitMS)
	if err != nil {
		return toolFailure("read_command_output", "Error: %v", err)
	}
	return marshalToolResponse("read_command_output", result)
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
