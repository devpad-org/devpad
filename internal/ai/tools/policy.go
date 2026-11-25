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

// concurrentTools are built-in tools that only read the workspace, so that
// calls to them in one response can run at the same time.
var concurrentTools = map[string]bool{
	"read_file":       true,
	"read_file_lines": true,
	"summarize_file":  true,
	"list_files":      true,
	"search_files":    true,
}

// RunsConcurrently reports whether a call to the tool may run at the same
// time as the calls to such tools beside it in one response. A run of them
// changes nothing another reads, so their results are the same in any
// order.
func RunsConcurrently(toolName string) bool {
	return concurrentTools[toolName]
}
