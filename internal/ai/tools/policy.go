package tools

import (
	"encoding/json"
	"strings"
)

type runCommandParams struct {
	Command string `json:"command"`
}

// CommandFromArgs extracts the shell command from encoded command-tool args.
func CommandFromArgs(args string) string {
	var params runCommandParams
	if err := json.Unmarshal([]byte(args), &params); err != nil {
		return ""
	}

	return params.Command
}

// CommandNeedsSudoApproval returns whether the encoded command-tool args require approval.
func CommandNeedsSudoApproval(args string) bool {
	command := CommandFromArgs(args)
	if command == "" {
		return false
	}

	fields := strings.Fields(command)
	for _, field := range fields {
		if field == "sudo" {
			return true
		}
	}

	return false
}

// ToolCallNeedsSudoApproval returns whether a tool call should require user approval.
func ToolCallNeedsSudoApproval(toolName, args string) bool {
	switch toolName {
	case "run_command", "start_command":
		return CommandNeedsSudoApproval(args)
	default:
		return false
	}
}
