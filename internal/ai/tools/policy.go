package tools

import (
	"encoding/json"
	"strings"
)

type runCommandParams struct {
	Command string `json:"command"`
}

// CommandFromArgs extracts the run_command shell command from encoded tool args.
func CommandFromArgs(args string) string {
	var params runCommandParams
	if err := json.Unmarshal([]byte(args), &params); err != nil {
		return ""
	}

	return params.Command
}

// CommandNeedsSudoApproval returns whether the encoded run_command args require approval.
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
